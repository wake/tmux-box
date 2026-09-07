package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"
)

// watcherState tracks the NORMAL / TMUX_DOWN state machine.
type watcherState struct {
	mu            sync.RWMutex
	tmuxAlive     bool
	lastHash      string
	lastBroadcast time.Time // debounce: tracks last broadcastSessions call time
}

func (ws *watcherState) getTmuxAlive() bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.tmuxAlive
}

func (ws *watcherState) setTmuxAlive(v bool) (changed bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	changed = ws.tmuxAlive != v
	ws.tmuxAlive = v
	return
}

// updateHash compares and updates the hash, returns true if changed.
func (ws *watcherState) updateHash(newHash string) bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if newHash == ws.lastHash {
		return false
	}
	ws.lastHash = newHash
	return true
}

// TmuxAlive returns the cached tmux status (thread-safe).
func (m *SessionModule) TmuxAlive() bool {
	return m.wstate.getTmuxAlive()
}

// checkAndBroadcast performs one tick of the watcher state machine.
func (m *SessionModule) checkAndBroadcast() {
	if m.wstate.getTmuxAlive() {
		m.tickNormal()
	} else {
		m.tickTmuxDown()
	}
}

func (m *SessionModule) tickNormal() {
	sessions, err := m.ListSessions()
	if err != nil {
		log.Printf("session: watcher list error: %v", err)
		return
	}

	if len(sessions) == 0 {
		if !m.tmux.TmuxAlive() {
			if m.wstate.setTmuxAlive(false) {
				m.broadcastTmuxStatus("unavailable")
			}
			m.notifyWaitFor(false)
			return
		}
	}

	hash := hashSessions(payloadInstance(sessions), sessions)
	if m.wstate.updateHash(hash) {
		// Hash changed = session list mutated (possibly by external tmux
		// commands that bypass the HTTP handlers' invalidation). Bust the
		// name cache before broadcasting so the next LookupCodeByName
		// refreshes from tmux.
		m.invalidateNameCache()
		if m.core.Events.HasSubscribers() {
			data := mustMarshal(sessions)
			m.core.Events.Broadcast("", "sessions", data)
		}
	}
}

func (m *SessionModule) tickTmuxDown() {
	if m.tmux.TmuxAlive() {
		m.wstate.setTmuxAlive(true)
		m.broadcastTmuxStatus("ok")
		m.notifyWaitFor(true)
		m.broadcastSessions()
	}
}

func (m *SessionModule) broadcastTmuxStatus(value string) {
	if m.core.Events.HasSubscribers() {
		m.core.Events.Broadcast("", "tmux", value)
	}
}

func (m *SessionModule) broadcastSessions() {
	// Goroutine A's wait-for unblocks here whenever tmux signals a
	// session/window/pane change — including external `tmux rename-session`
	// that bypasses the HTTP handlers' explicit invalidation. Bust the name
	// cache up front so stale name→code mappings can't survive the 1s TTL.
	m.invalidateNameCache()

	if !m.core.Events.HasSubscribers() {
		return
	}

	// Debounce: skip if last broadcast was within 500ms to prevent duplicate
	// broadcasts when goroutine A (wait-for) and goroutine B (5s ticker) fire
	// nearly simultaneously.
	m.wstate.mu.Lock()
	if time.Since(m.wstate.lastBroadcast) < 500*time.Millisecond {
		m.wstate.mu.Unlock()
		return
	}
	m.wstate.lastBroadcast = time.Now()
	m.wstate.mu.Unlock()

	sessions, err := m.ListSessions()
	if err != nil {
		log.Printf("session: broadcast list error: %v", err)
		return
	}
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	data := mustMarshal(sessions)
	m.core.Events.Broadcast("", "sessions", data)
}

func (m *SessionModule) watchSessions(ctx context.Context) {
	m.waitForGate = make(chan bool, 1)

	// Goroutine A: tmux wait-for loop with pause/resume gate
	go func() {
		active := m.wstate.getTmuxAlive()
		for {
			if !active {
				select {
				case <-ctx.Done():
					return
				case active = <-m.waitForGate:
					continue
				}
			}

			cmd := exec.CommandContext(ctx, "tmux", "wait-for", waitForChannel)
			err := cmd.Run()

			if ctx.Err() != nil {
				return
			}

			if err != nil {
				select {
				case v := <-m.waitForGate:
					active = v
					continue
				default:
				}
				log.Printf("session: wait-for error: %v, retrying in 1s", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				case v := <-m.waitForGate:
					active = v
				}
				continue
			}

			m.broadcastSessions()
		}
	}()

	// Goroutine B: polling fallback with 5s ticker
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.checkAndBroadcast()
			}
		}
	}()
}

func (m *SessionModule) notifyWaitFor(active bool) {
	// Drain any pending signal to ensure the new one is delivered
	select {
	case <-m.waitForGate:
	default:
	}
	select {
	case m.waitForGate <- active:
	default:
	}
}

// payloadInstance returns the generation already stamped on the payload that is
// about to be hashed and broadcast. Taking it from the payload rather than
// re-probing keeps hash and broadcast in lockstep (a fresh probe could return a
// third value that matches neither) and costs no extra tmux subprocess. With
// zero sessions there is no generation to report and nothing to protect — every
// pane is already marked dead by the code-absence rule (spec §4.6).
func payloadInstance(sessions []SessionInfo) string {
	if len(sessions) == 0 {
		return ""
	}
	return sessions[0].TmuxInstance
}

// hashSessions folds the tmux server identity into the change signal. Hashing
// the list alone would miss a tmux restart that recreates a byte-identical
// session list between two ticks, and no broadcast would ever tell the SPA the
// generation moved (spec §4.6). "" (probe failure) is hashed like any other
// value: the next successful tick changes the hash again, so it self-heals.
func hashSessions(tmuxInstance string, sessions []SessionInfo) string {
	data, _ := json.Marshal(struct {
		Instance string        `json:"i"`
		Sessions []SessionInfo `json:"s"`
	}{tmuxInstance, sessions})
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

func mustMarshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
