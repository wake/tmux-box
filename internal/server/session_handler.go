// internal/server/session_handler.go
package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"

	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

var validSessionName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type SessionHandler struct {
	store *store.Store
	tmux  tmux.Executor
}

func NewSessionHandler(s *store.Store, t tmux.Executor) *SessionHandler {
	return &SessionHandler{store: s, tmux: t}
}

type createReq struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
	Mode string `json:"mode"`
}

func (h *SessionHandler) List(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.store.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if sessions == nil {
		sessions = []store.Session{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

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
