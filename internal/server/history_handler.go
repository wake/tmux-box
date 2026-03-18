// internal/server/history_handler.go
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wake/tmux-box/internal/history"
)

const maxJSONLBytes = 2 * 1024 * 1024 // 2MB

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	sess, err := s.store.GetSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if sess.CCSessionID == "" {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	home, _ := os.UserHomeDir()
	projectHash := history.CCProjectPath(sess.Cwd)
	jsonlPath := filepath.Join(home, ".claude", "projects", projectHash, sess.CCSessionID+".jsonl")

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
