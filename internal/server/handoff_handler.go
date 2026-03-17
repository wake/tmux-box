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

	if req.Mode != "stream" && req.Mode != "jsonl" && req.Mode != "term" {
		http.Error(w, "mode must be stream, jsonl, or term", http.StatusBadRequest)
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

	// Find preset command (not required for term mode)
	var command string
	if req.Mode != "term" {
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
	if req.Mode == "term" {
		go s.runHandoffToTerm(sess, handoffID)
	} else {
		go s.runHandoff(sess, req.Mode, command, handoffID, token, port, bind)
	}
}

// runHandoff executes the handoff-to-stream sequence asynchronously:
//  1. Disconnect existing relay if present
//  2. Verify CC is running (prerequisite)
//  3. If CC is busy, interrupt to idle
//  4. Extract session ID via /status
//  5. Exit CC gracefully
//  6. Launch tbox relay with --resume
//  7. Wait for relay to connect back via cli-bridge
//  8. Update DB (mode + cc_session_id) and broadcast success
func (s *Server) runHandoff(sess store.Session, mode, command, handoffID, token string, port int, bind string) {
	defer s.handoffLocks.Unlock(sess.Name)

	broadcast := func(value string) {
		s.events.Broadcast(sess.Name, "handoff", value)
	}

	// Step 1: If relay already connected, shut it down
	if s.bridge.HasRelay(sess.Name) {
		broadcast("stopping-relay")
		s.bridge.SubscriberToRelay(sess.Name, []byte(`{"type":"shutdown"}`))
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

	// Step 2: Prerequisite — CC must be running
	broadcast("detecting")
	status := s.detector.Detect(sess.Name)
	if status == detect.StatusNormal || status == detect.StatusNotInCC {
		broadcast("failed:no CC running")
		return
	}

	// Step 3: If CC is busy (not idle), interrupt to idle
	if status != detect.StatusCCIdle {
		broadcast("stopping-cc")
		s.tmux.SendKeysRaw(sess.Name, "C-u")
		s.tmux.SendKeysRaw(sess.Name, "C-c")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			st := s.detector.Detect(sess.Name)
			if st == detect.StatusCCIdle {
				break
			}
		}
		if s.detector.Detect(sess.Name) != detect.StatusCCIdle {
			broadcast("failed:could not reach CC idle")
			return
		}
	}

	// Step 4: Extract session ID via /status
	broadcast("extracting-id")
	if err := s.tmux.SendKeys(sess.Name, "/status"); err != nil {
		broadcast("failed:send /status: " + err.Error())
		return
	}
	time.Sleep(2 * time.Second)
	paneContent, err := s.tmux.CapturePaneContent(sess.Name, 40)
	if err != nil {
		broadcast("failed:capture pane: " + err.Error())
		return
	}
	sessionID, err := detect.ExtractSessionID(paneContent)
	if err != nil {
		broadcast("failed:could not extract session ID")
		return
	}

	// Step 5: Exit CC gracefully
	broadcast("exiting-cc")
	s.tmux.SendKeysRaw(sess.Name, "Escape")
	time.Sleep(500 * time.Millisecond)
	if err := s.tmux.SendKeys(sess.Name, "/exit"); err != nil {
		broadcast("failed:send /exit: " + err.Error())
		return
	}
	exitDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(exitDeadline) {
		time.Sleep(500 * time.Millisecond)
		if s.detector.Detect(sess.Name) == detect.StatusNormal {
			break
		}
	}
	if s.detector.Detect(sess.Name) != detect.StatusNormal {
		broadcast("failed:CC did not exit")
		return
	}

	// Step 6: Launch tbox relay with --resume
	broadcast("launching")
	tokenFile := filepath.Join(os.TempDir(), fmt.Sprintf("tbox-token-%s", handoffID))
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		broadcast("failed:write token file: " + err.Error())
		return
	}
	time.AfterFunc(30*time.Second, func() {
		os.Remove(tokenFile)
	})

	relayCmd := fmt.Sprintf("tbox relay --session %s --daemon ws://127.0.0.1:%d --token-file %s -- %s --resume %s",
		sess.Name, port, tokenFile, command, sessionID)
	if err := s.tmux.SendKeys(sess.Name, relayCmd); err != nil {
		os.Remove(tokenFile)
		broadcast("failed:send-keys error: " + err.Error())
		return
	}

	// Step 7: Wait for relay to connect
	relayDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(relayDeadline) {
		if s.bridge.HasRelay(sess.Name) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !s.bridge.HasRelay(sess.Name) {
		broadcast("failed:relay did not connect within 15s")
		return
	}

	// Step 8: Update DB (mode + cc_session_id together) and broadcast success
	ccID := sessionID
	if err := s.store.UpdateSession(sess.ID, store.SessionUpdate{Mode: &mode, CCSessionID: &ccID}); err != nil {
		broadcast("failed:db update: " + err.Error())
		return
	}
	broadcast("connected")
}

// runHandoffToTerm handles the handoff from stream back to interactive terminal mode.
// It shuts down the relay, waits for shell, then launches claude --resume.
func (s *Server) runHandoffToTerm(sess store.Session, handoffID string) {
	defer s.handoffLocks.Unlock(sess.Name)

	broadcast := func(value string) {
		s.events.Broadcast(sess.Name, "handoff", value)
	}

	// Step 1: Get session ID from DB
	current, err := s.store.GetSession(sess.ID)
	if err != nil || current.CCSessionID == "" {
		broadcast("failed:no session ID available")
		return
	}
	sessionID := current.CCSessionID

	// Step 2: Shut down relay
	if s.bridge.HasRelay(sess.Name) {
		broadcast("stopping-relay")
		s.bridge.SubscriberToRelay(sess.Name, []byte(`{"type":"shutdown"}`))
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !s.bridge.HasRelay(sess.Name) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if s.bridge.HasRelay(sess.Name) {
			broadcast("failed:relay did not disconnect")
			return
		}
	}

	// Step 3: Wait for shell
	broadcast("waiting-shell")
	shellDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(shellDeadline) {
		if s.detector.Detect(sess.Name) == detect.StatusNormal {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if s.detector.Detect(sess.Name) != detect.StatusNormal {
		broadcast("failed:shell did not recover")
		return
	}

	// Step 4: Launch interactive CC with --resume
	broadcast("launching-cc")
	resumeCmd := fmt.Sprintf("claude --resume %s", sessionID)
	if err := s.tmux.SendKeys(sess.Name, resumeCmd); err != nil {
		broadcast("failed:send-keys error: " + err.Error())
		return
	}

	// Step 5: Verify CC started
	ccDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(ccDeadline) {
		st := s.detector.Detect(sess.Name)
		if st == detect.StatusCCIdle || st == detect.StatusCCRunning || st == detect.StatusCCWaiting {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	finalSt := s.detector.Detect(sess.Name)
	if finalSt != detect.StatusCCIdle && finalSt != detect.StatusCCRunning && finalSt != detect.StatusCCWaiting {
		broadcast("failed:CC did not start")
		return
	}

	// Step 6: Update DB (mode=term, clear cc_session_id)
	termMode := "term"
	emptyID := ""
	if err := s.store.UpdateSession(sess.ID, store.SessionUpdate{
		Mode:        &termMode,
		CCSessionID: &emptyID,
	}); err != nil {
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
