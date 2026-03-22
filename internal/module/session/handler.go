package session

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/terminal"
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// --- SessionProvider implementation ---

// ListSessions returns all live tmux sessions merged with cached meta.
func (m *SessionModule) ListSessions() ([]SessionInfo, error) {
	sessions, err := m.tmux.ListSessions()
	if err != nil {
		return nil, err
	}

	// Build live ID set for orphan cleanup
	liveIDs := make([]string, len(sessions))
	for i, s := range sessions {
		liveIDs[i] = s.ID
	}
	if _, err := m.meta.CleanOrphans(liveIDs); err != nil {
		return nil, err
	}

	result := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		code, err := EncodeSessionID(s.ID)
		if err != nil {
			continue // skip sessions with invalid IDs
		}
		info := SessionInfo{
			Code:   code,
			TmuxID: s.ID,
			Name:   s.Name,
			Exists: true,
			Mode:   "term", // default
			Cwd:    s.Cwd,
		}

		// Merge meta from DB
		meta, err := m.meta.GetMeta(s.ID)
		if err != nil {
			return nil, err
		}
		if meta != nil {
			info.Mode = meta.Mode
			info.CCSessionID = meta.CCSessionID
			info.CCModel = meta.CCModel
			if meta.Cwd != "" {
				info.Cwd = meta.Cwd
			}
		}

		result = append(result, info)
	}

	return result, nil
}

// GetSession returns a single session by its code, or nil if not found.
func (m *SessionModule) GetSession(code string) (*SessionInfo, error) {
	tmuxID, err := DecodeSessionID(code)
	if err != nil {
		return nil, nil // invalid code → not found
	}

	sessions, err := m.tmux.ListSessions()
	if err != nil {
		return nil, err
	}

	for _, s := range sessions {
		if s.ID == tmuxID {
			info := &SessionInfo{
				Code:   code,
				TmuxID: s.ID,
				Name:   s.Name,
				Exists: true,
				Mode:   "term",
				Cwd:    s.Cwd,
			}

			meta, err := m.meta.GetMeta(s.ID)
			if err != nil {
				return nil, err
			}
			if meta != nil {
				info.Mode = meta.Mode
				info.CCSessionID = meta.CCSessionID
				info.CCModel = meta.CCModel
				if meta.Cwd != "" {
					info.Cwd = meta.Cwd
				}
			}

			return info, nil
		}
	}

	// Not found in tmux — clean up orphan meta
	_ = m.meta.DeleteMeta(tmuxID)
	return nil, nil
}

// UpdateMeta performs a partial meta update for the session identified by code.
func (m *SessionModule) UpdateMeta(code string, update MetaUpdate) error {
	tmuxID, err := DecodeSessionID(code)
	if err != nil {
		return err
	}

	storeUpdate := store.MetaUpdate{
		Mode:        update.Mode,
		CCSessionID: update.CCSessionID,
		CCModel:     update.CCModel,
		Cwd:         update.Cwd,
	}

	return m.meta.UpdateMeta(tmuxID, storeUpdate)
}

// HandleTerminalWS attaches a WebSocket connection to the tmux session PTY relay.
func (m *SessionModule) HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string) {
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Determine sizing mode from config (default to "auto" if no config).
	sizingMode := "auto"
	if m.core != nil && m.core.Cfg != nil {
		sizingMode = m.core.Cfg.Terminal.GetSizingMode()
	}

	// Build tmux attach-session command and args.
	target := info.Name + ":0"
	args := []string{"attach-session", "-t", target}
	if sizingMode == "terminal-first" {
		args = append(args, "-f", "ignore-size")
	}

	relay := terminal.NewRelay("tmux", args, "/")

	switch sizingMode {
	case "terminal-first":
		// no OnStart — relay uses -f ignore-size, sizing handled by terminal
	case "minimal-first":
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				if err := m.tmux.ResizeWindowAuto(target); err != nil {
					log.Printf("HandleTerminalWS: ResizeWindowAuto(%s): %v", target, err)
				}
				if err := m.tmux.SetWindowOption(target, "window-size", "smallest"); err != nil {
					log.Printf("HandleTerminalWS: SetWindowOption(%s): %v", target, err)
				}
			}()
		}
	default:
		if sizingMode != "auto" && sizingMode != "" {
			log.Printf("HandleTerminalWS: unknown sizing_mode %q, falling back to auto", sizingMode)
		}
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				if err := m.tmux.ResizeWindowAuto(target); err != nil {
					log.Printf("HandleTerminalWS: ResizeWindowAuto(%s): %v", target, err)
				}
				if err := m.tmux.SetWindowOption(target, "window-size", "latest"); err != nil {
					log.Printf("HandleTerminalWS: SetWindowOption(%s): %v", target, err)
				}
			}()
		}
	}

	relay.HandleWebSocket(w, r)
}

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
