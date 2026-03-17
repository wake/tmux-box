// internal/server/handoff_handler.go
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/store"
)

// handoffLocks tracks per-session handoff mutexes using a tryLock pattern.
// If a handoff is already in progress for a session, new requests are rejected.
type handoffLocks struct {
	mu    sync.Mutex
	locks map[string]bool // session name → locked
}

func newHandoffLocks() *handoffLocks {
	return &handoffLocks{locks: make(map[string]bool)}
}

// TryLock attempts to acquire the lock for the named session.
// Returns true if the lock was acquired, false if already held.
func (h *handoffLocks) TryLock(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.locks[name] {
		return false
	}
	h.locks[name] = true
	return true
}

// Unlock releases the lock for the named session.
func (h *handoffLocks) Unlock(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.locks, name)
}

type handoffRequest struct {
	Mode   string `json:"mode"`
	Preset string `json:"preset"`
}

// handleHandoff handles POST /api/sessions/{id}/handoff.
// It validates the request, acquires a per-session lock, returns 202 immediately,
// then orchestrates the mode switch asynchronously in a goroutine.
func (s *Server) handleHandoff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var req handoffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	if req.Mode != "stream" && req.Mode != "jsonl" {
		http.Error(w, "mode must be stream or jsonl", http.StatusBadRequest)
		return
	}

	// Lookup session
	sess, err := s.store.GetSession(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Snapshot config under read lock
	s.cfgMu.RLock()
	presets := s.cfg.Stream.Presets
	if req.Mode == "jsonl" {
		presets = s.cfg.JSONL.Presets
	}
	token := s.cfg.Token
	port := s.cfg.Port
	bind := s.cfg.Bind
	s.cfgMu.RUnlock()

	// Find preset command
	var command string
	for _, p := range presets {
		if p.Name == req.Preset {
			command = p.Command
			break
		}
	}
	if command == "" {
		http.Error(w, "preset not found", http.StatusBadRequest)
		return
	}

	// Try per-session lock
	if !s.handoffLocks.TryLock(sess.Name) {
		http.Error(w, "handoff already in progress", http.StatusConflict)
		return
	}

	// Generate handoff ID
	handoffID := generateHandoffID()

	// Return 202 Accepted immediately
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"handoff_id": handoffID})

	// Run async handoff in goroutine
	go s.runHandoff(sess, req.Mode, command, handoffID, token, port, bind)
}

// runHandoff executes the handoff sequence asynchronously:
//  1. Disconnect existing relay if present
//  2. Detect current session state
//  3. Stop CC if running (send C-c and poll)
//  4. Launch tbox relay with the preset command
//  5. Wait for relay to connect back via cli-bridge
//  6. Update DB mode and broadcast success
func (s *Server) runHandoff(sess store.Session, mode, command, handoffID, token string, port int, bind string) {
	defer s.handoffLocks.Unlock(sess.Name)

	broadcast := func(value string) {
		s.events.Broadcast(sess.Name, "handoff", value)
	}

	// Step 1: If relay already connected, shut it down
	if s.bridge.HasRelay(sess.Name) {
		broadcast("stopping-relay")
		s.bridge.SubscriberToRelay(sess.Name, []byte(`{"type":"shutdown"}`))
		// Wait for relay disconnect (max 5s)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !s.bridge.HasRelay(sess.Name) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if s.bridge.HasRelay(sess.Name) {
			broadcast("failed:existing relay did not disconnect")
			return
		}
	}

	// Step 2: Detect current state
	broadcast("detecting")
	status := s.detector.Detect(sess.Name)

	// Step 3: If CC running, stop it
	if status != detect.StatusNormal && status != detect.StatusNotInCC {
		broadcast("stopping-cc")
		s.tmux.SendKeys(sess.Name, "C-c")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			st := s.detector.Detect(sess.Name)
			if st == detect.StatusNormal {
				break
			}
		}
		if s.detector.Detect(sess.Name) != detect.StatusNormal {
			broadcast("failed:could not stop CC within 10s")
			return
		}
	}

	// Step 4: Launch tbox relay
	broadcast("launching")

	// C3 fix: Write token to a temporary file instead of embedding in command line.
	// The relay reads and deletes the file, so the token never appears in pane/history.
	tokenFile := filepath.Join(os.TempDir(), fmt.Sprintf("tbox-token-%s", handoffID))
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		broadcast("failed:write token file: " + err.Error())
		return
	}
	// Safety net: delete token file after 30s in case relay never reads it.
	time.AfterFunc(30*time.Second, func() {
		os.Remove(tokenFile) // no-op if already deleted by relay
	})

	relayCmd := fmt.Sprintf("tbox relay --session %s --daemon ws://127.0.0.1:%d --token-file %s -- %s",
		sess.Name, port, tokenFile, command)
	if err := s.tmux.SendKeys(sess.Name, relayCmd); err != nil {
		os.Remove(tokenFile)
		broadcast("failed:send-keys error: " + err.Error())
		return
	}

	// Step 5: Wait for relay to connect to cli-bridge
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s.bridge.HasRelay(sess.Name) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !s.bridge.HasRelay(sess.Name) {
		broadcast("failed:relay did not connect within 15s")
		return
	}

	// Step 6: Update DB mode and broadcast success
	if err := s.store.UpdateSession(sess.ID, store.SessionUpdate{Mode: &mode}); err != nil {
		broadcast("failed:db update error: " + err.Error())
		return
	}
	broadcast("connected")
}

func generateHandoffID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
