// internal/server/server.go
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
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

// Deprecated: New creates a full server with all routes (session + legacy).
// Kept for existing tests. Production code should use NewLegacy + RegisterLegacyRoutes.
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
	return s
}

// NewLegacy creates a server that handles only legacy routes (handoff, history,
// bridge, events, config). Session and terminal routes are now handled by the
// session module. Does not call routes() or resetStaleModes().
func NewLegacy(cfg config.Config, cfgPath string, st *store.Store, tx tmux.Executor) *Server {
	return &Server{
		cfg:          cfg,
		cfgPath:      cfgPath,
		store:        st,
		tmux:         tx,
		bridge:       bridge.New(),
		events:       NewEventsBroadcaster(),
		detector:     detect.New(tx, cfg.Detect.CCCommands),
		handoffLocks: newHandoffLocks(),
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

// Deprecated: routes registers ALL routes on the internal mux (session + legacy).
// Kept for existing tests that use New(). Production code uses RegisterLegacyRoutes.
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

// RegisterLegacyRoutes registers only the routes not yet migrated to modules.
// Session and terminal routes are handled by the session module.
func (s *Server) RegisterLegacyRoutes(mux *http.ServeMux) {
	// Handoff + history (still use legacy store with integer id)
	mux.HandleFunc("POST /api/sessions/{id}/handoff", s.handleHandoff)
	mux.HandleFunc("GET /api/sessions/{id}/history", s.handleHistory)

	// Bridge WS (still use session name)
	mux.HandleFunc("/ws/cli-bridge/{session}", s.handleCliBridge)
	mux.HandleFunc("/ws/cli-bridge-sub/{session}", s.handleCliBridgeSubscribe)

	// Events (stays here until 1.6b)
	mux.HandleFunc("/ws/session-events", s.handleSessionEvents)

	// Config
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)
}

// RestoreWindowSizing clears manual window-size set by resize-window
// and restores automatic sizing with the given mode.
func (s *Server) RestoreWindowSizing(target, windowSizeMode string) {
	if err := s.tmux.ResizeWindowAuto(target); err != nil {
		log.Printf("RestoreWindowSizing: ResizeWindowAuto(%s): %v", target, err)
	}
	if err := s.tmux.SetWindowOption(target, "window-size", windowSizeMode); err != nil {
		log.Printf("RestoreWindowSizing: SetWindowOption(%s): %v", target, err)
	}
}

// BuildTerminalRelay returns the command, args, and cleanup function for a terminal relay.
func (s *Server) BuildTerminalRelay(name string) (cmd string, args []string, cleanup func(), err error) {
	args = []string{"attach-session", "-t", name}
	if s.cfg.Terminal.GetSizingMode() == "terminal-first" {
		args = append(args, "-f", "ignore-size")
	}
	return "tmux", args, func() {}, nil
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

	sizingMode := s.cfg.Terminal.GetSizingMode()
	relay := terminal.NewRelay(cmd, args, "/")
	switch sizingMode {
	case "terminal-first":
		// no OnStart — relay uses -f ignore-size, sizing handled by terminal
	case "minimal-first":
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				s.RestoreWindowSizing(name, "smallest")
			}()
		}
	default:
		if sizingMode != "auto" && sizingMode != "" {
			log.Printf("handleTerminal: unknown sizing_mode %q, falling back to auto", sizingMode)
		}
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				s.RestoreWindowSizing(name, "latest")
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
