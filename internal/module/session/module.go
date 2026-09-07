package session

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

const listCacheTTL = time.Second

// SessionModule manages tmux sessions, meta cache, and HTTP API.
type SessionModule struct {
	meta            *store.MetaStore
	tmux            tmux.Executor
	core            *core.Core
	shellHomeReader func(pid string) (string, error)
	// tmuxInstanceFn reads the tmux server identity; swapped in tests.
	tmuxInstanceFn func() string
	// shellProbe runs the command-word probe (spec §4.4). A field, not a
	// package global, so a test can prove a rejected token never reached a
	// shell without mutating shared state.
	shellProbe shellProbeFunc
	// passwdShell is the third rung of the probe's shell ladder.
	passwdShell func() string
	cancelWatch context.CancelFunc
	wstate      watcherState
	waitForGate chan bool
	// createMu serializes handleCreate's HasSession→NewSession→SetMeta
	// critical section so two concurrent POSTs with the same name can't
	// both slip past the duplicate check. See #61.
	createMu sync.Mutex

	// listCache debounces rapid ListSessions calls (1s TTL). See #128.
	listCacheMu   sync.Mutex
	listCacheData []SessionInfo
	listCacheAt   time.Time

	// nameCache backs LookupCodeByName: a name→code map plus a TTL stamp.
	// Separate from listCache because the lookup fast path runs only one
	// tmux subprocess (list-sessions) — no meta merge, no per-session
	// metadata fan-out — and is hit on the hook hot path (#?).
	nameCacheMu   sync.Mutex
	nameCacheData map[string]string
	nameCacheAt   time.Time
}

// NewSessionModule creates a SessionModule with the given MetaStore.
func NewSessionModule(meta *store.MetaStore) *SessionModule {
	return &SessionModule{
		meta:            meta,
		shellHomeReader: readShellHomeFromProc,
		tmuxInstanceFn:  config.GetTmuxInstance,
		shellProbe:      runShellProbe,
		passwdShell:     passwdShellForCurrentUser,
	}
}

func (m *SessionModule) Name() string           { return "session" }
func (m *SessionModule) Dependencies() []string { return nil }

func (m *SessionModule) Init(c *core.Core) error {
	m.core = c
	m.tmux = c.Tmux
	c.Registry.Register(RegistryKey, SessionProvider(m))
	return nil
}

func (m *SessionModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", m.handleList)
	mux.HandleFunc("GET /api/sessions/{code}", m.handleGet)
	mux.HandleFunc("GET /api/sessions/{code}/home", m.handleSessionHome)
	mux.HandleFunc("GET /api/sessions/{code}/cwd", m.handleSessionCwd)
	mux.HandleFunc("POST /api/sessions", m.handleCreate)
	mux.HandleFunc("PATCH /api/sessions/{code}", m.handleRename)
	mux.HandleFunc("DELETE /api/sessions/{code}", m.handleDelete)
	mux.HandleFunc("POST /api/sessions/{code}/mode", m.handleSwitchMode)
	mux.HandleFunc("POST /api/sessions/{code}/send-keys", m.handleSendKeys)
	mux.HandleFunc("/ws/terminal/{code}", m.handleTerminalWS)
	mux.HandleFunc("POST /api/shell/resolve-command", m.handleShellResolveCommand)
	mux.HandleFunc("GET /api/hooks/tmux/status", m.handleTmuxHookStatus)
	mux.HandleFunc("POST /api/hooks/tmux/setup", m.handleTmuxHookSetup)
}

func (m *SessionModule) Start(ctx context.Context) error {
	if err := m.meta.ResetStaleModes(); err != nil {
		return err
	}

	// Install tmux hooks (log warning on error, don't fail startup).
	if err := m.installTmuxHooks(); err != nil {
		log.Printf("session: failed to install tmux hooks: %v (continuing without push)", err)
	}

	// Start session watcher with a child context.
	watchCtx, cancel := context.WithCancel(ctx)
	m.cancelWatch = cancel
	m.wstate.setTmuxAlive(m.tmux.TmuxAlive())
	m.core.TmuxAliveFunc = m.TmuxAlive
	m.watchSessions(watchCtx)

	// Register OnSubscribe callback to send initial sessions snapshot.
	m.core.Events.OnSubscribe(func(sub *core.EventSubscriber) {
		sessions, err := m.ListSessions()
		if err != nil {
			log.Printf("session: OnSubscribe list error: %v", err)
			return
		}
		if sessions == nil {
			sessions = []SessionInfo{}
		}
		data, err := json.Marshal(core.HostEvent{
			Type:  "sessions",
			Value: mustMarshal(sessions),
		})
		if err != nil {
			return
		}
		sub.Send(data)
	})

	return nil
}

func (m *SessionModule) Stop(_ context.Context) error {
	// Cancel watcher goroutines.
	if m.cancelWatch != nil {
		m.cancelWatch()
	}
	// Remove tmux hooks (best-effort).
	m.removeTmuxHooks()
	return nil
}
