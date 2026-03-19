// internal/server/server.go
package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/wake/tmux-box/internal/bridge"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/terminal"
	"github.com/wake/tmux-box/internal/tmux"
)

type Server struct {
	cfg          config.Config
	cfgMu        sync.RWMutex
	cfgPath      string
	store        *store.Store
	tmux         tmux.Executor
	bridge       *bridge.Bridge
	events       *EventsBroadcaster
	detector     *detect.Detector
	handoffLocks *handoffLocks
	mux          *http.ServeMux
}

func New(cfg config.Config, st *store.Store, tx tmux.Executor, cfgPath string) *Server {
	s := &Server{
		cfg:          cfg,
		cfgPath:      cfgPath,
		store:        st,
		tmux:         tx,
		bridge:       bridge.New(),
		events:       NewEventsBroadcaster(),
		detector:     detect.New(tx, cfg.Detect.CCCommands),
		handoffLocks: newHandoffLocks(),
		mux:          http.NewServeMux(),
	}
	s.routes()
	s.resetStaleModes()
	s.CleanupStaleRelays()
	return s
}

// CleanupStaleRelays removes tmux sessions created by session group mode
// that were not cleaned up (e.g., daemon crashed). Matches pattern: {name}-tbox-{8 hex chars}.
func (s *Server) CleanupStaleRelays() {
	names, err := s.tmux.ListSessionNames()
	if err != nil {
		return
	}
	re := regexp.MustCompile(`^.+-tbox-[0-9a-f]{8}$`)
	for _, name := range names {
		if re.MatchString(name) {
			s.tmux.KillSession(name)
		}
	}
}

// resetStaleModes resets any sessions stuck in stream/jsonl mode back to term.
// On daemon startup no relays can be connected, so non-term modes are stale.
func (s *Server) resetStaleModes() {
	sessions, err := s.store.ListSessions()
	if err != nil {
		return
	}
	termMode := "term"
	for _, sess := range sessions {
		if sess.Mode != "term" {
			s.store.UpdateSession(sess.ID, store.SessionUpdate{Mode: &termMode})
		}
	}
}

func (s *Server) routes() {
	sh := NewSessionHandler(s.store, s.tmux, s.bridge)
	s.mux.HandleFunc("GET /api/sessions", sh.List)
	s.mux.HandleFunc("POST /api/sessions", sh.Create)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", sh.Delete)
	s.mux.HandleFunc("POST /api/sessions/{id}/mode", sh.SwitchMode)
	s.mux.HandleFunc("POST /api/sessions/{id}/handoff", s.handleHandoff)
	s.mux.HandleFunc("GET /api/sessions/{id}/history", s.handleHistory)
	s.mux.HandleFunc("/ws/terminal/{session}", s.handleTerminal)
	s.mux.HandleFunc("/ws/cli-bridge/{session}", s.handleCliBridge)
	s.mux.HandleFunc("/ws/cli-bridge-sub/{session}", s.handleCliBridgeSubscribe)
	s.mux.HandleFunc("/ws/session-events", s.handleSessionEvents)
	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("PUT /api/config", s.handlePutConfig)
}

// RestoreWindowSizing clears manual window-size set by resize-window
// and restores automatic sizing based on the latest client.
func (s *Server) RestoreWindowSizing(target string) {
	s.tmux.ResizeWindowAuto(target)
	s.tmux.SetWindowOption(target, "window-size", "latest")
}

// BuildTerminalRelay returns the command, args, and cleanup function for a terminal relay.
// When session_group is enabled, it creates a grouped session for size isolation.
func (s *Server) BuildTerminalRelay(name string) (cmd string, args []string, cleanup func(), err error) {
	if !s.cfg.Terminal.IsSessionGroup() {
		return "tmux", []string{"attach-session", "-t", name}, func() {}, nil
	}

	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", nil, nil, fmt.Errorf("generate relay ID: %w", err)
	}
	relaySession := fmt.Sprintf("%s-tbox-%x", name, b)

	// Retry up to 3 times on name collision
	for attempt := 0; attempt < 3; attempt++ {
		err = s.tmux.NewGroupedSession(name, relaySession)
		if err == nil {
			break
		}
		b = make([]byte, 4)
		rand.Read(b)
		relaySession = fmt.Sprintf("%s-tbox-%x", name, b)
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("create grouped session: %w", err)
	}

	cleanup = func() {
		s.tmux.KillSession(relaySession)
	}

	return "tmux", []string{"attach-session", "-t", relaySession}, cleanup, nil
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("session")
	if !s.tmux.HasSession(name) {
		http.Error(w, "session not found", 404)
		return
	}

	cmd, args, cleanup, err := s.BuildTerminalRelay(name)
	if err != nil {
		http.Error(w, "relay setup failed: "+err.Error(), 500)
		return
	}
	defer cleanup()

	relay := terminal.NewRelay(cmd, args, "/")
	// session_group=true: each grouped session has only one client,
	// so window-size latest auto-adjusts — no need for resize-window -A.
	if s.cfg.Terminal.IsAutoResize() && !s.cfg.Terminal.IsSessionGroup() {
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				s.RestoreWindowSizing(name)
			}()
		}
	}
	relay.HandleWebSocket(w, r)
}

func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = TokenAuth(s.cfg.Token)(h)
	h = IPWhitelist(s.cfg.Allow)(h)
	h = CORS(h)
	return h
}

func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Bind, s.cfg.Port)
	return http.ListenAndServe(addr, s.Handler())
}

// BroadcastEvent exposes the events broadcaster for external callers (e.g. tests, handoff).
func (s *Server) BroadcastEvent(session, eventType, value string) {
	s.events.Broadcast(session, eventType, value)
}

// StartStatusPoller starts a background goroutine that polls tmux session status
// and broadcasts changes to connected WebSocket subscribers. The goroutine exits
// when the context is cancelled.
func (s *Server) StartStatusPoller(ctx context.Context) {
	interval := s.cfg.Detect.PollInterval
	if interval <= 0 {
		interval = 2
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)

	go func() {
		defer ticker.Stop()

		lastStatus := make(map[string]detect.Status)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.events.HasSubscribers() {
					continue // skip polling when nobody is listening
				}
				sessions, err := s.store.ListSessions()
				if err != nil {
					log.Printf("status poller: list sessions: %v", err)
					continue
				}

				for _, sess := range sessions {
					detectTarget := sess.TmuxTarget
					if detectTarget == "" {
						detectTarget = sess.Name + ":0"
					}
					status := s.detector.Detect(detectTarget)
					if prev, ok := lastStatus[sess.Name]; !ok || prev != status {
						lastStatus[sess.Name] = status
						s.events.Broadcast(sess.Name, "status", string(status))
					}
				}
			}
		}
	}()
}
