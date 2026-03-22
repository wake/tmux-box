// internal/server/session_handler.go
package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"

	"github.com/wake/tmux-box/internal/bridge"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

// TODO(1.6b): remove validSessionName after session module fully handles Create validation.
var validSessionName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// SessionResponse wraps store.Session with runtime state for API responses.
// TODO(1.6b): remove after session module exposes its own DTO with has_relay.
type SessionResponse struct {
	store.Session
	HasRelay bool `json:"has_relay"`
}

// Deprecated: SessionHandler handles session CRUD for legacy routes().
// Session CRUD has been moved to the session module. This type is kept for
// test compatibility only. TODO(1.6b): remove after migrating all tests.
type SessionHandler struct {
	store  *store.Store
	tmux   tmux.Executor
	bridge *bridge.Bridge
}

// Deprecated: NewSessionHandler creates a legacy SessionHandler.
// TODO(1.6b): remove after migrating all tests to the session module.
func NewSessionHandler(s *store.Store, t tmux.Executor, b *bridge.Bridge) *SessionHandler {
	return &SessionHandler{store: s, tmux: t, bridge: b}
}

type switchModeReq struct {
	Mode string `json:"mode"`
}

// SwitchMode switches a session between term, stream, and jsonl modes.
// Deprecated: handled by session module. TODO(1.6b): remove after migrating tests.
func (h *SessionHandler) SwitchMode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	var req switchModeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	if req.Mode != "term" && req.Mode != "stream" && req.Mode != "jsonl" {
		http.Error(w, "mode must be term, stream, or jsonl", 400)
		return
	}

	sess, err := h.store.GetSession(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, err.Error(), 500)
		}
		return
	}

	if sess.Mode == req.Mode {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "already in mode " + req.Mode})
		return
	}

	if err := h.store.UpdateSession(id, store.SessionUpdate{Mode: &req.Mode}); err != nil {
		http.Error(w, "update session: "+err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "switched to " + req.Mode})
}

type createReq struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
	Mode string `json:"mode"`
}

// List lists all sessions with relay status.
// Deprecated: handled by session module. TODO(1.6b): remove after migrating tests.
func (h *SessionHandler) List(w http.ResponseWriter, r *http.Request) {
	// Sync: discover tmux sessions not yet in DB
	h.syncTmuxSessions()

	sessions, err := h.store.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	result := make([]SessionResponse, len(sessions))
	for i, s := range sessions {
		result[i] = SessionResponse{
			Session:  s,
			HasRelay: h.bridge.HasRelay(s.Name),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// syncTmuxSessions discovers tmux sessions not yet tracked in SQLite and adds them.
// TODO(1.6b): remove after session module handles sync.
func (h *SessionHandler) syncTmuxSessions() {
	tmuxSessions, err := h.tmux.ListSessions()
	if err != nil {
		return
	}

	dbSessions, err := h.store.ListSessions()
	if err != nil {
		return
	}

	known := make(map[string]bool, len(dbSessions))
	for _, s := range dbSessions {
		known[s.Name] = true
	}

	for _, ts := range tmuxSessions {
		if known[ts.Name] {
			continue
		}
		h.store.CreateSession(store.Session{
			Name:       ts.Name,
			TmuxTarget: ts.Name + ":0",
			Cwd:        ts.Cwd,
			Mode:       "term",
		})
	}
}

// Create creates a new session and starts a tmux session.
// Deprecated: handled by session module. TODO(1.6b): remove after migrating tests.
func (h *SessionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}
	if req.Name == "" || req.Cwd == "" {
		http.Error(w, "name and cwd required", 400)
		return
	}
	if !validSessionName.MatchString(req.Name) {
		http.Error(w, "invalid session name: must match [a-zA-Z0-9_-]+", 400)
		return
	}
	if req.Mode == "" {
		req.Mode = "term"
	}

	if err := h.tmux.NewSession(req.Name, req.Cwd); err != nil {
		http.Error(w, "tmux: "+err.Error(), 500)
		return
	}

	sess := store.Session{
		Name:       req.Name,
		TmuxTarget: req.Name + ":0",
		Cwd:        req.Cwd,
		Mode:       req.Mode,
	}
	id, err := h.store.CreateSession(sess)
	if err != nil {
		// Rollback: kill the tmux session we just created
		h.tmux.KillSession(req.Name)
		http.Error(w, err.Error(), 500)
		return
	}
	sess.ID = id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(sess)
}

// Delete deletes a session and kills the tmux session.
// Deprecated: handled by session module. TODO(1.6b): remove after migrating tests.
func (h *SessionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	// Find session name for tmux kill
	sessions, err := h.store.ListSessions()
	if err != nil {
		log.Printf("list sessions for delete: %v", err)
	}
	for _, s := range sessions {
		if s.ID == id {
			h.tmux.KillSession(s.Name)
			break
		}
	}

	if err := h.store.DeleteSession(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}
