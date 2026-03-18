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
//  4. Extract session ID + cwd via /status
//  5. Exit CC gracefully
//  6. Launch tbox relay with --resume
//  7. Wait for relay to connect back via cli-bridge
//  8. Update DB (mode + cc_session_id + cwd) and broadcast success
func (s *Server) runHandoff(sess store.Session, mode, command, handoffID, token string, port int, bind string) {
	defer s.handoffLocks.Unlock(sess.Name)

	broadcast := func(value string) {
		s.events.Broadcast(sess.Name, "handoff", value)
	}

	// Use TmuxTarget (session:window format, e.g. "myapp:0") for all tmux
	// Executor calls to prevent ambiguous target resolution.
	target := sess.TmuxTarget
	if target == "" {
		target = sess.Name + ":0"
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

	// Prepare pane — exit tmux copy-mode (if active) and clear any partial
	// input. Escape exits copy-mode, closes CC dialogs, and may interrupt a
	// running tool (Step 3 handles the idle check regardless). C-u clears
	// the input line. Both are safe no-ops in normal idle state.
	if err := s.tmux.SendKeysRaw(target, "Escape"); err != nil {
		broadcast("failed:send Escape: " + err.Error())
		return
	}
	s.tmux.SendKeysRaw(target, "C-u") // best-effort; Escape success means target exists
	time.Sleep(200 * time.Millisecond)

	// Step 2: Prerequisite — CC must be running
	broadcast("detecting")
	status := s.detector.Detect(target)
	if status == detect.StatusNormal || status == detect.StatusNotInCC {
		broadcast("failed:no CC running")
		return
	}

	// Step 3: If CC is busy (not idle), interrupt to idle
	if status != detect.StatusCCIdle {
		broadcast("stopping-cc")
		if err := s.tmux.SendKeysRaw(target, "C-u"); err != nil {
			broadcast("failed:send C-u: " + err.Error())
			return
		}
		if err := s.tmux.SendKeysRaw(target, "C-c"); err != nil {
			broadcast("failed:send C-c: " + err.Error())
			return
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			st := s.detector.Detect(target)
			if st == detect.StatusCCIdle {
				break
			}
		}
		if s.detector.Detect(target) != detect.StatusCCIdle {
			broadcast("failed:could not reach CC idle")
			return
		}
	}

	// Step 3.5: Ensure pane is large enough for /status TUI to render.
	// When xterm.js container is display:none (user on stream page), the PTY
	// client may have a tiny size (e.g. 10x5), causing /status to render garbled
	// text that capture-pane cannot parse.
	didManualResize := false
	if cols, rows, err := s.tmux.PaneSize(target); err == nil && (cols < 80 || rows < 24) {
		if err := s.tmux.ResizeWindow(target, 80, 24); err != nil {
			broadcast("failed:resize pane: " + err.Error())
			return
		}
		didManualResize = true
		time.Sleep(200 * time.Millisecond)
	}

	// Step 4: Extract session ID + cwd via /status
	// Send /status then rapidly capture full pane content. The /status dialog
	// may auto-dismiss quickly, so retry capture several times.
	broadcast("extracting-id")
	if err := s.tmux.SendKeys(target, "/status"); err != nil {
		if didManualResize {
			s.tmux.ResizeWindowAuto(target)
		}
		broadcast("failed:send /status: " + err.Error())
		return
	}
	var statusInfo detect.StatusInfo
	for attempt := 0; attempt < 6; attempt++ {
		time.Sleep(500 * time.Millisecond)
		paneContent, err := s.tmux.CapturePaneContent(target, 200)
		if err != nil {
			continue
		}
		info, err := detect.ExtractStatusInfo(paneContent)
		if err == nil {
			statusInfo = info
			break
		}
	}
	// Step 3.5 cleanup: restore automatic window sizing now that /status is done.
	if didManualResize {
		s.tmux.ResizeWindowAuto(target)
	}
	if statusInfo.SessionID == "" {
		broadcast("failed:could not extract session ID")
		return
	}

	// Step 5: Exit CC — Escape dismisses /status dialog, then /exit to quit gracefully.
	// Both /exit and Ctrl+C preserve session state for --resume. /exit is preferred
	// because a command is more stable than a key combination.
	broadcast("exiting-cc")
	if err := s.tmux.SendKeysRaw(target, "Escape"); err != nil {
		broadcast("failed:send Escape: " + err.Error())
		return
	}
	time.Sleep(500 * time.Millisecond)
	if err := s.tmux.SendKeys(target, "/exit"); err != nil {
		broadcast("failed:send /exit: " + err.Error())
		return
	}
	exitDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(exitDeadline) {
		time.Sleep(500 * time.Millisecond)
		if s.detector.Detect(target) == detect.StatusNormal {
			break
		}
	}
	if s.detector.Detect(target) != detect.StatusNormal {
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

	relayCmd := fmt.Sprintf("tbox relay --session %s --daemon ws://%s:%d --token-file %s -- %s --resume %s",
		sess.Name, bind, port, tokenFile, command, statusInfo.SessionID)
	if err := s.tmux.SendKeys(target, relayCmd); err != nil {
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
		os.Remove(tokenFile)
		broadcast("failed:relay did not connect within 15s")
		return
	}

	// Step 8: Update DB (mode + cc_session_id + cwd) and broadcast success
	ccID := statusInfo.SessionID
	update := store.SessionUpdate{Mode: &mode, CCSessionID: &ccID}
	if statusInfo.Cwd != "" {
		update.Cwd = &statusInfo.Cwd
	}
	if err := s.store.UpdateSession(sess.ID, update); err != nil {
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

	// Use TmuxTarget for all tmux operations (same rationale as runHandoff).
	target := sess.TmuxTarget
	if target == "" {
		target = sess.Name + ":0"
	}

	// Step 1: Get session ID from DB
	current, err := s.store.GetSession(sess.ID)
	if err != nil {
		broadcast("failed:db lookup error: " + err.Error())
		return
	}
	if current.CCSessionID == "" {
		broadcast("failed:no CC session ID stored")
		return
	}
	sessionID := current.CCSessionID

	// Pre-update mode to "term" before shutting down relay.
	// This prevents revertModeOnRelayDisconnect from firing a spurious
	// "failed:relay disconnected" event during an intentional handoff.
	// If later steps fail, the deferred rollback restores the original mode.
	origMode := current.Mode
	termMode := "term"
	if err := s.store.UpdateSession(sess.ID, store.SessionUpdate{Mode: &termMode}); err != nil {
		broadcast("failed:db pre-update error: " + err.Error())
		return
	}
	rollbackMode := func() {
		s.store.UpdateSession(sess.ID, store.SessionUpdate{Mode: &origMode})
	}

	// Shut down relay
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
			rollbackMode()
			broadcast("failed:relay did not disconnect")
			return
		}
	}

	// Wait for shell
	broadcast("waiting-shell")
	shellDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(shellDeadline) {
		if s.detector.Detect(target) == detect.StatusNormal {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if s.detector.Detect(target) != detect.StatusNormal {
		rollbackMode()
		broadcast("failed:shell did not recover")
		return
	}

	// Launch interactive CC with --resume
	broadcast("launching-cc")
	resumeCmd := fmt.Sprintf("claude --resume %s", sessionID)
	if err := s.tmux.SendKeys(target, resumeCmd); err != nil {
		rollbackMode()
		broadcast("failed:send-keys error: " + err.Error())
		return
	}

	// Verify CC started
	ccDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(ccDeadline) {
		st := s.detector.Detect(target)
		if st == detect.StatusCCIdle || st == detect.StatusCCRunning || st == detect.StatusCCWaiting {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	finalSt := s.detector.Detect(target)
	if finalSt != detect.StatusCCIdle && finalSt != detect.StatusCCRunning && finalSt != detect.StatusCCWaiting {
		rollbackMode()
		broadcast("failed:CC did not start")
		return
	}

	// Clear cc_session_id (mode already set to "term" above).
	// Cwd is intentionally kept — it still represents the CC project directory
	// and is needed by the history handler if the user later handoffs back to stream.
	emptyID := ""
	if err := s.store.UpdateSession(sess.ID, store.SessionUpdate{
		CCSessionID: &emptyID,
	}); err != nil {
		broadcast("failed:db update error: " + err.Error())
		return
	}
	broadcast("connected")
}

// revertModeOnRelayDisconnect reverts the session mode to "term" when a relay
// disconnects. This prevents sessions from being stuck in stream mode after
// a failed or interrupted handoff.
func (s *Server) revertModeOnRelayDisconnect(sessionName string) {
	sessions, err := s.store.ListSessions()
	if err != nil {
		return
	}
	for _, sess := range sessions {
		if sess.Name == sessionName && sess.Mode != "term" {
			termMode := "term"
			s.store.UpdateSession(sess.ID, store.SessionUpdate{Mode: &termMode})
			s.events.Broadcast(sessionName, "handoff", "failed:relay disconnected")
			return
		}
	}
}

func generateHandoffID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
