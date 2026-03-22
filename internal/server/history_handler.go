// internal/server/history_handler.go
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wake/tmux-box/internal/history"
	"github.com/wake/tmux-box/internal/module/session"
)

const maxJSONLBytes = 2 * 1024 * 1024 // 2MB

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	tmuxID, err := session.DecodeSessionID(code)
	if err != nil {
		http.Error(w, "invalid session code", http.StatusBadRequest)
		return
	}

	// Get cc_session_id and cwd: prefer MetaStore, fall back to legacy store.
	var ccSessionID, cwd string
	if s.meta != nil {
		meta, err := s.meta.GetMeta(tmuxID)
		if err != nil {
			http.Error(w, "meta lookup error", http.StatusInternalServerError)
			return
		}
		if meta == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		ccSessionID = meta.CCSessionID
		cwd = meta.Cwd
	} else {
		// Legacy fallback: resolve tmuxID → session name → legacy store
		tmuxSessions, err := s.tmux.ListSessions()
		if err != nil {
			http.Error(w, "failed to list tmux sessions", http.StatusInternalServerError)
			return
		}
		var sessionName string
		for _, ts := range tmuxSessions {
			if ts.ID == tmuxID {
				sessionName = ts.Name
				break
			}
		}
		if sessionName == "" {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		sess, err := s.store.GetSessionByName(sessionName)
		if err != nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		ccSessionID = sess.CCSessionID
		cwd = sess.Cwd
	}

	w.Header().Set("Content-Type", "application/json")

	if ccSessionID == "" {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	projectHash := history.CCProjectPath(cwd)
	jsonlPath := filepath.Join(home, ".claude", "projects", projectHash, ccSessionID+".jsonl")

	f, err := os.Open(jsonlPath)
	if err != nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	defer f.Close()

	messages, err := history.ParseJSONL(f, maxJSONLBytes)
	if err != nil || messages == nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	json.NewEncoder(w).Encode(messages)
}
