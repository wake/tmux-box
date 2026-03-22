package session

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/wake/tmux-box/internal/store"
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// --- HTTP Handlers ---

func (m *SessionModule) handleList(w http.ResponseWriter, r *http.Request) {
	sessions, err := m.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Return empty array, not null
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (m *SessionModule) handleGet(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

type createRequest struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
}

func (m *SessionModule) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || !nameRegex.MatchString(req.Name) {
		http.Error(w, "invalid session name: must match ^[a-zA-Z0-9_-]+$", http.StatusBadRequest)
		return
	}

	if req.Cwd == "" {
		req.Cwd = "/"
	}

	// Create the tmux session
	if err := m.tmux.NewSession(req.Name, req.Cwd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Find the newly created session to get its tmux ID
	sessions, err := m.tmux.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var info *SessionInfo
	for _, s := range sessions {
		if s.Name == req.Name {
			code, err := EncodeSessionID(s.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// Set initial meta
			if err := m.meta.SetMeta(s.ID, store.SessionMeta{
				TmuxID: s.ID,
				Mode:   "term",
				Cwd:    req.Cwd,
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			info = &SessionInfo{
				Code:   code,
				TmuxID: s.ID,
				Name:   s.Name,
				Exists: true,
				Mode:   "term",
				Cwd:    req.Cwd,
			}
			break
		}
	}

	if info == nil {
		http.Error(w, "session created but not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

func (m *SessionModule) handleDelete(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Kill tmux session by name
	if err := m.tmux.KillSession(info.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Delete meta
	_ = m.meta.DeleteMeta(info.TmuxID)

	w.WriteHeader(http.StatusNoContent)
}

type switchModeRequest struct {
	Mode string `json:"mode"`
}

func (m *SessionModule) handleSwitchMode(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	var req switchModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate mode
	switch req.Mode {
	case "term", "stream", "jsonl":
		// valid
	default:
		http.Error(w, "invalid mode: must be term, stream, or jsonl", http.StatusBadRequest)
		return
	}

	// Verify session exists
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Ensure meta record exists before updating
	if err := m.meta.SetMeta(info.TmuxID, store.SessionMeta{
		TmuxID: info.TmuxID,
		Mode:   info.Mode,
		Cwd:    info.Cwd,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mode := req.Mode
	if err := m.UpdateMeta(code, MetaUpdate{Mode: &mode}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (m *SessionModule) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	m.HandleTerminalWS(w, r, code)
}
