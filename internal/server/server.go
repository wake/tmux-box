// internal/server/server.go
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/wake/tmux-box/internal/bridge"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/terminal"
	"github.com/wake/tmux-box/internal/tmux"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	tmux     tmux.Executor
	bridge   *bridge.Bridge
	events   *EventsBroadcaster
	detector *detect.Detector
	mux      *http.ServeMux
}

func New(cfg config.Config, st *store.Store, tx tmux.Executor) *Server {
	s := &Server{
		cfg:      cfg,
		store:    st,
		tmux:     tx,
		bridge:   bridge.New(),
		events:   NewEventsBroadcaster(),
		detector: detect.New(tx, cfg.Detect.CCCommands),
		mux:      http.NewServeMux(),
	}
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
	s.mux.HandleFunc("/ws/session-events", s.handleSessionEvents)
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
					status := s.detector.Detect(sess.Name)
					if prev, ok := lastStatus[sess.Name]; !ok || prev != status {
						lastStatus[sess.Name] = status
						s.events.Broadcast(sess.Name, "status", string(status))
					}
				}
			}
		}
	}()
}
