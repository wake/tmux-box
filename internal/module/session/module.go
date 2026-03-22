package session

import (
	"context"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

// SessionModule manages tmux sessions, meta cache, and HTTP API.
type SessionModule struct {
	meta *store.MetaStore
	tmux tmux.Executor
	core *core.Core
}

// NewSessionModule creates a SessionModule with the given MetaStore.
func NewSessionModule(meta *store.MetaStore) *SessionModule {
	return &SessionModule{meta: meta}
}

func (m *SessionModule) Name() string { return "session" }

func (m *SessionModule) Init(c *core.Core) error {
	m.core = c
	m.tmux = c.Tmux
	c.Registry.Register(RegistryKey, SessionProvider(m))
	return nil
}

func (m *SessionModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", m.handleList)
	mux.HandleFunc("GET /api/sessions/{code}", m.handleGet)
	mux.HandleFunc("POST /api/sessions", m.handleCreate)
	mux.HandleFunc("DELETE /api/sessions/{code}", m.handleDelete)
	mux.HandleFunc("POST /api/sessions/{code}/mode", m.handleSwitchMode)
	mux.HandleFunc("/ws/terminal/{code}", m.handleTerminalWS)
}

func (m *SessionModule) Start(ctx context.Context) error {
	return m.meta.ResetStaleModes()
}

func (m *SessionModule) Stop() error {
	return nil
}
