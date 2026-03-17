// Package detect provides session status detection via tmux pane inspection.
package detect

import (
	"strings"
	"sync"

	"github.com/wake/tmux-box/internal/tmux"
)

// Status represents the detected state of a tmux session.
type Status string

const (
	StatusNormal    Status = "normal"     // shell prompt, no foreground process
	StatusNotInCC   Status = "not-in-cc"  // non-CC foreground process (e.g. node, vim)
	StatusCCIdle    Status = "cc-idle"    // CC is running, showing input prompt
	StatusCCRunning Status = "cc-running" // CC is executing a tool / generating
	StatusCCWaiting Status = "cc-waiting" // CC is waiting for user permission
	// TODO(Phase 3): StatusCCUnread requires per-session subscriber tracking in the
	// bridge to know when nobody is watching + last-seen position tracking per session.
	// This is genuinely complex and is deferred to Phase 3.
	StatusCCUnread Status = "cc-unread" // set externally by bridge, not detected here
)

// defaultShells lists common shell names for detecting "normal" (shell idle) state.
var defaultShells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true,
}

// Detector inspects tmux pane state to determine session status.
type Detector struct {
	mu         sync.RWMutex
	tmux       tmux.Executor
	ccCommands map[string]bool
}

// New creates a Detector. ccCommands lists the binary names that indicate
// Claude Code is running (e.g. "claude", "cld").
func New(executor tmux.Executor, ccCommands []string) *Detector {
	cmds := make(map[string]bool, len(ccCommands))
	for _, c := range ccCommands {
		cmds[c] = true
	}
	return &Detector{tmux: executor, ccCommands: cmds}
}

// UpdateCommands replaces the set of CC command names used for detection.
// This is called when the config API updates detect.cc_commands.
func (d *Detector) UpdateCommands(cmds []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ccCommands = make(map[string]bool, len(cmds))
	for _, c := range cmds {
		d.ccCommands[c] = true
	}
}

// Detect returns the current Status for the given tmux session/target.
//
// Detection strategy (hybrid):
//  1. pane_current_command is a shell → StatusNormal (fast path)
//  2. pane_current_command matches ccCommands → inspect pane content for sub-state
//  3. Otherwise, check child processes of pane for CC commands (handles symlink/version issues)
//  4. If child matches → inspect pane content for sub-state
//  5. If no child matches → check pane content for CC UI signatures as fallback
//  6. Nothing matches → StatusNotInCC
func (d *Detector) Detect(session string) Status {
	cmd, err := d.tmux.PaneCurrentCommand(session)
	if err != nil {
		return StatusNormal
	}
	cmd = strings.TrimSpace(cmd)

	// Fast path: shell → normal
	if defaultShells[cmd] {
		return StatusNormal
	}

	d.mu.RLock()
	isCC := d.ccCommands[cmd]
	d.mu.RUnlock()

	// Fast path: known CC command
	if isCC {
		return d.detectCCSubState(session)
	}

	// Strategy A: check child processes of pane shell for CC binary names
	children, err := d.tmux.PaneChildCommands(session)
	if err == nil {
		for _, child := range children {
			// Match basename (e.g. "/Users/wake/.local/bin/claude" → "claude")
			base := child
			if idx := strings.LastIndex(child, "/"); idx >= 0 {
				base = child[idx+1:]
			}
			d.mu.RLock()
			childIsCC := d.ccCommands[base]
			d.mu.RUnlock()
			if childIsCC {
				return d.detectCCSubState(session)
			}
		}
	}

	// Strategy B fallback: check pane content for CC UI signatures
	content, err := d.tmux.CapturePaneContent(session, 5)
	if err != nil {
		return StatusNotInCC
	}
	if looksLikeCC(content) {
		return d.detectCCSubState(session)
	}

	return StatusNotInCC
}

// detectCCSubState inspects pane content to distinguish idle/running/waiting.
func (d *Detector) detectCCSubState(session string) Status {
	content, err := d.tmux.CapturePaneContent(session, 5)
	if err != nil {
		return StatusCCRunning // can't read pane, assume running
	}

	// Check for permission prompt (Allow / Deny buttons).
	if strings.Contains(content, "Allow") && strings.Contains(content, "Deny") {
		return StatusCCWaiting
	}

	// Check for idle prompt (❯).
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) > 0 {
		lastLine := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasPrefix(lastLine, "❯") {
			return StatusCCIdle
		}
	}

	return StatusCCRunning
}

// looksLikeCC checks pane content for CC UI signatures when process detection fails.
// Looks for CC-specific UI elements: the ❯ prompt, status bar with model info, etc.
func looksLikeCC(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// CC idle prompt
		if strings.HasPrefix(trimmed, "❯") {
			return true
		}
		// CC status bar contains model identifiers
		if strings.Contains(trimmed, "Opus") || strings.Contains(trimmed, "Sonnet") || strings.Contains(trimmed, "Haiku") {
			return true
		}
		// CC permission prompt
		if strings.Contains(trimmed, "Allow") && strings.Contains(trimmed, "Deny") {
			return true
		}
	}
	return false
}
