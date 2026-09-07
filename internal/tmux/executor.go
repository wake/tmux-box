// internal/tmux/executor.go
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var ErrNoSession = errors.New("no such session")

type TmuxSession struct {
	ID   string // tmux session ID, e.g. "$0"
	Name string
	Cwd  string
}

type TmuxPaneMetadata struct {
	SessionID          string
	SessionName        string
	WindowID           string
	PaneID             string
	PaneTitle          string
	WindowName         string
	PaneCurrentCommand string
}

// Executor abstracts tmux CLI for testability.
type Executor interface {
	ListSessions() ([]TmuxSession, error)
	ActivePaneMetadata(sessionName string) (TmuxPaneMetadata, error)
	NewSession(name, cwd string) error
	KillSession(name string) error
	RenameSession(oldName, newName string) error
	HasSession(name string) bool
	// HasPane reports whether the given pane id (e.g. "%5") still exists in
	// the global tmux pane list. Returns (false, nil) on empty paneID or
	// confirmed absence; (true, nil) on confirmed presence; (_, err) on a
	// transient tmux command failure (no server, exec error). Per round-4
	// audit: callers that derive "pane gone" semantics MUST distinguish
	// confirmed-absence from query-error — collapsing both into false
	// causes false-positive "pane gone" / clear emissions during tmux
	// hiccups while the pane is still alive.
	HasPane(paneID string) (exists bool, err error)
	SendKeys(target, keys string) error
	SendKeysRaw(target string, keys ...string) error
	// SendKeysIfInstance sends keys to the session id's active pane only when
	// the tmux server's generation ("<pid>:<start_time>") equals
	// expectedInstance, evaluated by the SAME server connection that performs
	// the send. See send_keys_conditional.go for why nothing weaker works.
	//
	// (true, nil) sent; (false, nil) the server declined; (false, err) the
	// invocation could not be completed and nothing was sent.
	SendKeysIfInstance(sessionID, expectedInstance string, keys ...string) (sent bool, err error)
	PasteText(target, text string) error
	PaneCurrentPath(target string) (string, error)
	PaneSessionName(target string) (string, error)
	// PaneSessionID returns the tmux session ID ("$N") the pane belongs to.
	// Unlike the session name it is immutable for the life of the session, so
	// a caller that must not be confused by a rename asks for this instead.
	PaneSessionID(target string) (string, error)
	PanePID(target string) (string, error)
	ActivePanePID(target string) (string, error)
	PaneChildCommands(target string) ([]string, error)
	CapturePaneContent(target string, lastN int) (string, error)
	// CapturePaneRange returns lines [start, endInclusive] of the pane.
	// Indexing follows tmux capture-pane -S/-E semantics:
	//   - start/end are line indices; 0 = top of visible pane
	//   - negative values reference history above the visible pane
	// Empty output is legal (not treated as an error).
	CapturePaneRange(target string, start, endInclusive int) (string, error)
	// CapturePaneTopLines returns the top n lines (0..n-1) of the pane.
	// Equivalent to CapturePaneRange(target, 0, n-1).
	// n <= 0 returns "" without invoking tmux (caller-friendly disable).
	CapturePaneTopLines(target string, n int) (string, error)
	PaneSize(target string) (cols, rows int, err error)
	ResizeWindow(target string, cols, rows int) error
	ResizeWindowAuto(target string) error
	SetWindowOption(target, option, value string) error
	SetWindowOptionGlobal(option, value string) error
	ShowWindowOption(option string) (string, error)
	// ShowGlobalOption reads a SERVER/global option (`show-options -g`).
	// ShowWindowOption passes `-w` and can only see window options, so it
	// returns "" for a server option such as `default-shell` — which reads as
	// "unset" and is indistinguishable from a real absence.
	ShowGlobalOption(option string) (string, error)
	SetHookGlobal(event, command string) error
	RemoveHookGlobal(event string) error
	ShowHooksGlobal() (string, error)
	TmuxAlive() bool
}

// --- Real Executor ---

type RealExecutor struct{}

func NewRealExecutor() *RealExecutor { return &RealExecutor{} }

func (r *RealExecutor) ListSessions() ([]TmuxSession, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{session_path}").Output()
	if err != nil {
		if strings.Contains(err.Error(), "no server running") ||
			strings.Contains(string(out), "no server running") {
			return nil, nil
		}
		// exit status 1 with "no sessions" is normal
		if exitErr, ok := err.(*exec.ExitError); ok {
			if strings.Contains(string(exitErr.Stderr), "no server running") ||
				strings.Contains(string(exitErr.Stderr), "no sessions") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	var sessions []TmuxSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		s := TmuxSession{ID: parts[0]}
		if len(parts) > 1 {
			s.Name = parts[1]
		}
		if len(parts) > 2 {
			s.Cwd = parts[2]
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (r *RealExecutor) ActivePaneMetadata(sessionName string) (TmuxPaneMetadata, error) {
	target := activePaneTarget(sessionName)
	query := func(format string) (string, error) {
		out, err := exec.Command("tmux", "display-message", "-p", "-t", target, format).Output()
		if err != nil {
			return "", fmt.Errorf("tmux display-message %s: %w", format, err)
		}
		return sanitizeTmuxMetadata(strings.TrimSuffix(string(out), "\n")), nil
	}

	sessionID, err := query("#{session_id}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	resolvedSessionName, err := query("#{session_name}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	windowID, err := query("#{window_id}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	paneID, err := query("#{pane_id}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	paneTitle, err := query("#{pane_title}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	windowName, err := query("#{window_name}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}
	paneCurrentCommand, err := query("#{pane_current_command}")
	if err != nil {
		return TmuxPaneMetadata{}, err
	}

	return TmuxPaneMetadata{
		SessionID:          sessionID,
		SessionName:        resolvedSessionName,
		WindowID:           windowID,
		PaneID:             paneID,
		PaneTitle:          paneTitle,
		WindowName:         windowName,
		PaneCurrentCommand: paneCurrentCommand,
	}, nil
}

func activePaneTarget(sessionName string) string {
	return "=" + sessionName + ":"
}

func sanitizeTmuxMetadata(s string) string {
	mapped := strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r':
			return ' '
		}
		if (r >= 0 && r < 0x20) || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(mapped), " ")
}

func (r *RealExecutor) NewSession(name, cwd string) error {
	return exec.Command("tmux", "new-session", "-d", "-s", name, "-c", cwd).Run()
}

func (r *RealExecutor) KillSession(name string) error {
	err := exec.Command("tmux", "kill-session", "-t", "="+name).Run()
	if err != nil {
		return ErrNoSession
	}
	return nil
}

func (r *RealExecutor) RenameSession(oldName, newName string) error {
	err := exec.Command("tmux", "rename-session", "-t", "="+oldName, newName).Run()
	if err != nil {
		return fmt.Errorf("tmux rename-session: %w", err)
	}
	return nil
}

func (r *RealExecutor) HasSession(name string) bool {
	// Use "=" prefix for exact name matching (tmux 3.2+).
	// Without it, "has-session -t foo" matches "foobar" via prefix.
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// HasPane reports whether the given pane id is currently listed by
// `tmux list-panes -a -F '#{pane_id}'`. Conservative on any error: a
// non-zero exit (no server running, etc) returns false. Empty paneID
// short-circuits to false without invoking tmux.
//
// Used by codex ProbeIntent ProcessDead detector — see
// internal/agent/codex/probe_intent_process_dead.go.
func (r *RealExecutor) HasPane(paneID string) (bool, error) {
	if paneID == "" {
		return false, nil
	}
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Round-5 audit F10: classify "no server running" as confirmed
		// global absence (false, nil) rather than a transient query
		// failure. Without this branch the detector would poll forever
		// when the user tears down the last tmux session — codex pane
		// is definitively gone but the lights stay armed.
		if strings.Contains(stderr.String(), "no server running") {
			return false, nil
		}
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == paneID {
			return true, nil
		}
	}
	return false, nil
}

func (r *RealExecutor) SendKeys(target, keys string) error {
	return exec.Command("tmux", "send-keys", "-t", target, keys, "Enter").Run()
}

func (r *RealExecutor) SendKeysRaw(target string, keys ...string) error {
	args := []string{"send-keys", "-t", target}
	args = append(args, keys...)
	return exec.Command("tmux", args...).Run()
}

func (r *RealExecutor) PasteText(target, text string) error {
	// Use a named buffer to avoid races when multiple goroutines paste
	// concurrently (the default anonymous buffer is shared globally).
	bufName := fmt.Sprintf("pdx-%d", time.Now().UnixNano())

	cmd := exec.Command("tmux", "load-buffer", "-b", bufName, "-")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux load-buffer: %w", err)
	}
	// -d  deletes the named buffer after pasting.
	// -p  wraps content in bracketed paste markers (\e[200~ … \e[201~)
	//     so the receiving application (e.g. Claude Code) recognises it as
	//     a paste event rather than typed input.
	// -r  preserves LF as-is (default converts LF → CR).
	return exec.Command("tmux", "paste-buffer", "-b", bufName, "-t", target, "-d", "-p", "-r").Run()
}

func (r *RealExecutor) PaneCurrentPath(target string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_current_path}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message pane_current_path: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) PaneSessionName(target string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{session_name}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message session_name: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) PaneSessionID(target string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{session_id}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message session_id: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) PanePID(target string) (string, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_pid}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux list-panes pid: %w", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}

// ActivePanePID returns the PID of the currently active pane of the target
// session/window, unlike PanePID which returns the first listed pane. Used
// when a value must come from the pane the user is looking at (e.g. shell
// HOME for tilde-path expansion).
func (r *RealExecutor) ActivePanePID(target string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_pid}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux display-message pane_pid: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) PaneChildCommands(target string) ([]string, error) {
	return r.paneProcessCommands(target, false)
}

func (r *RealExecutor) PaneDescendantCommands(target string) ([]string, error) {
	return r.paneProcessCommands(target, true)
}

func (r *RealExecutor) paneProcessCommands(target string, recursive bool) ([]string, error) {
	panePID, err := r.PanePID(target)
	if err != nil {
		return nil, err
	}
	// Build a process tree from ps output, then walk the pane shell's descendants.
	out, err := exec.Command("ps", "-ax", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	childrenByPPID := make(map[string][]processEntry)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		entry := processEntry{
			pid:     fields[0],
			ppid:    fields[1],
			command: fields[2],
		}
		childrenByPPID[entry.ppid] = append(childrenByPPID[entry.ppid], entry)
	}

	queue := append([]string(nil), panePID)
	var cmds []string
	for len(queue) > 0 {
		parentPID := queue[0]
		queue = queue[1:]
		for _, child := range childrenByPPID[parentPID] {
			cmds = append(cmds, child.command)
			if recursive {
				queue = append(queue, child.pid)
			}
		}
		if !recursive {
			break
		}
	}

	return cmds, nil
}

func (r *RealExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	arg := fmt.Sprintf("-%d", lastN)
	out, err := exec.Command("tmux", "capture-pane", "-e", "-t", target, "-p", "-S", arg).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

func (r *RealExecutor) CapturePaneRange(target string, start, endInclusive int) (string, error) {
	startArg := fmt.Sprintf("%d", start)
	endArg := fmt.Sprintf("%d", endInclusive)
	// -e preserves ANSI escape sequences. Without it, tmux normalizes the
	// pane to plain text and ANSI-only changes (spinner color, status
	// highlights) hash to the same content as the previous tick — so a
	// TopLines watcher would miss "running" signals on agents that animate
	// purely via color (cc's spinner is one). Mirrors CapturePaneContent.
	out, err := exec.Command("tmux", "capture-pane", "-e", "-p", "-t", target, "-S", startArg, "-E", endArg).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane range: %w", err)
	}
	return string(out), nil
}

func (r *RealExecutor) CapturePaneTopLines(target string, n int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	return r.CapturePaneRange(target, 0, n-1)
}

func (r *RealExecutor) PaneSize(target string) (cols, rows int, err error) {
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_width} #{pane_height}").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("tmux list-panes size: %w", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	var c, r2 int
	if _, err := fmt.Sscanf(line, "%d %d", &c, &r2); err != nil {
		return 0, 0, fmt.Errorf("parse pane size: %w", err)
	}
	return c, r2, nil
}

func (r *RealExecutor) ResizeWindow(target string, cols, rows int) error {
	return exec.Command("tmux", "resize-window", "-t", target,
		"-x", fmt.Sprintf("%d", cols), "-y", fmt.Sprintf("%d", rows)).Run()
}

func (r *RealExecutor) ResizeWindowAuto(target string) error {
	return exec.Command("tmux", "resize-window", "-A", "-t", target).Run()
}

func (r *RealExecutor) SetWindowOption(target, option, value string) error {
	return exec.Command("tmux", "set-window-option", "-t", target, option, value).Run()
}

func (r *RealExecutor) SetWindowOptionGlobal(option, value string) error {
	return exec.Command("tmux", "set-window-option", "-g", option, value).Run()
}

func (r *RealExecutor) ShowWindowOption(option string) (string, error) {
	out, err := exec.Command("tmux", "show-options", "-w", "-g", "-q", "-v", option).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "no server running") {
				return "", nil
			}
		}
		return "", fmt.Errorf("tmux show-options -w %s: %w", option, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) ShowGlobalOption(option string) (string, error) {
	out, err := exec.Command("tmux", "show-options", "-g", "-q", "-v", option).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "no server running") {
				return "", nil
			}
		}
		return "", fmt.Errorf("tmux show-options -g %s: %w", option, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *RealExecutor) SetHookGlobal(event, command string) error {
	return exec.Command("tmux", "set-hook", "-g", event, command).Run()
}

func (r *RealExecutor) RemoveHookGlobal(event string) error {
	return exec.Command("tmux", "set-hook", "-gu", event).Run()
}

func (r *RealExecutor) ShowHooksGlobal() (string, error) {
	out, err := exec.Command("tmux", "show-hooks", "-g").Output()
	if err != nil {
		// "no hooks" is a normal condition — return empty string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "no hooks") ||
				strings.Contains(stderr, "no server running") {
				return "", nil
			}
		}
		return "", fmt.Errorf("tmux show-hooks: %w", err)
	}
	return string(out), nil
}

func (r *RealExecutor) TmuxAlive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", "info").Run() == nil
}

type processEntry struct {
	pid     string
	ppid    string
	command string
}
