// internal/server/server.go
package server

import (
	"fmt"
	"net/http"

	"github.com/wake/tmux-box/internal/bridge"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/terminal"
	"github.com/wake/tmux-box/internal/tmux"
)

type Server struct {
	cfg    config.Config
	store  *store.Store
	tmux   tmux.Executor
	bridge *bridge.Bridge
	mux    *http.ServeMux
}

func New(cfg config.Config, st *store.Store, tx tmux.Executor) *Server {
	s := &Server{cfg: cfg, store: st, tmux: tx, bridge: bridge.New(), mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	sh := NewSessionHandler(s.store, s.tmux)
	s.mux.HandleFunc("GET /api/sessions", sh.List)
	s.mux.HandleFunc("POST /api/sessions", sh.Create)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", sh.Delete)
	s.mux.HandleFunc("POST /api/sessions/{id}/mode", sh.SwitchMode)
	s.mux.HandleFunc("/ws/terminal/{session}", s.handleTerminal)
	s.mux.HandleFunc("/ws/cli-bridge/{session}", s.handleCliBridge)
	s.mux.HandleFunc("/ws/cli-bridge-sub/{session}", s.handleCliBridgeSubscribe)
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("session")
	if !s.tmux.HasSession(name) {
		http.Error(w, "session not found", 404)
		return
	}
	// cwd doesn't matter for tmux attach-session; tmux manages its own working directory.
	relay := terminal.NewRelay("tmux", []string{"attach-session", "-t", name}, "/")
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
