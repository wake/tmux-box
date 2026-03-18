// internal/tmux/executor.go
package tmux

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrNoSession = errors.New("no such session")

type TmuxSession struct {
	Name string
	Cwd  string
}

// Executor abstracts tmux CLI for testability.
type Executor interface {
	ListSessions() ([]TmuxSession, error)
	NewSession(name, cwd string) error
	KillSession(name string) error
	HasSession(name string) bool
	SendKeys(target, keys string) error
	SendKeysRaw(target string, keys ...string) error
	PaneCurrentCommand(target string) (string, error)
	PanePID(target string) (string, error)
	PaneChildCommands(target string) ([]string, error)
	CapturePaneContent(target string, lastN int) (string, error)
	PaneSize(target string) (cols, rows int, err error)
	ResizeWindow(target string, cols, rows int) error
	ResizeWindowAuto(target string) error
}

// --- Real Executor ---

type RealExecutor struct{}

func NewRealExecutor() *RealExecutor { return &RealExecutor{} }

func (r *RealExecutor) ListSessions() ([]TmuxSession, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}\t#{session_path}").Output()
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
		parts := strings.SplitN(line, "\t", 2)
		s := TmuxSession{Name: parts[0]}
		if len(parts) > 1 {
			s.Cwd = parts[1]
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (r *RealExecutor) NewSession(name, cwd string) error {
	return exec.Command("tmux", "new-session", "-d", "-s", name, "-c", cwd).Run()
}

func (r *RealExecutor) KillSession(name string) error {
	err := exec.Command("tmux", "kill-session", "-t", name).Run()
	if err != nil {
		return ErrNoSession
	}
	return nil
}

func (r *RealExecutor) HasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func (r *RealExecutor) SendKeys(target, keys string) error {
	return exec.Command("tmux", "send-keys", "-t", target, keys, "Enter").Run()
}

func (r *RealExecutor) SendKeysRaw(target string, keys ...string) error {
	args := []string{"send-keys", "-t", target}
	args = append(args, keys...)
	return exec.Command("tmux", args...).Run()
}

func (r *RealExecutor) PaneCurrentCommand(target string) (string, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_current_command}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux list-panes: %w", err)
	}
	// Return the first line (active pane's command).
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}

func (r *RealExecutor) PanePID(target string) (string, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_pid}").Output()
	if err != nil {
		return "", fmt.Errorf("tmux list-panes pid: %w", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}

func (r *RealExecutor) PaneChildCommands(target string) ([]string, error) {
	panePID, err := r.PanePID(target)
	if err != nil {
		return nil, err
	}
	// ps -ax -o pid,ppid,comm → find children of the pane's shell PID
	out, err := exec.Command("ps", "-ax", "-o", "pid,ppid,comm").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var cmds []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == panePID {
			cmds = append(cmds, fields[2])
		}
	}
	return cmds, nil
}

func (r *RealExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	arg := fmt.Sprintf("-%d", lastN)
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", arg).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
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

// --- Fake Executor ---

type RawKeysCall struct {
	Target string
	Keys   []string
}

type KeysCall struct {
	Target string
	Keys   string
}

type FakeExecutor struct {
	sessions         map[string]TmuxSession
	paneCommands     map[string]string   // target → command name
	paneContents     map[string]string   // target → captured text
	paneChildren     map[string][]string // target → child command names
	paneSizes        map[string][2]int   // target → [cols, rows]
	rawKeysCalls     []RawKeysCall
	keysCalls        []KeysCall
	autoResizeCalls  []string // targets passed to ResizeWindowAuto
}

func NewFakeExecutor() *FakeExecutor {
	return &FakeExecutor{
		sessions:     make(map[string]TmuxSession),
		paneCommands: make(map[string]string),
		paneContents: make(map[string]string),
		paneChildren: make(map[string][]string),
		paneSizes:    make(map[string][2]int),
	}
}

func (f *FakeExecutor) AddSession(name, cwd string) {
	f.sessions[name] = TmuxSession{Name: name, Cwd: cwd}
}

func (f *FakeExecutor) ListSessions() ([]TmuxSession, error) {
	out := make([]TmuxSession, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, s)
	}
	return out, nil
}

func (f *FakeExecutor) NewSession(name, cwd string) error {
	f.sessions[name] = TmuxSession{Name: name, Cwd: cwd}
	return nil
}

func (f *FakeExecutor) KillSession(name string) error {
	if _, ok := f.sessions[name]; !ok {
		return ErrNoSession
	}
	delete(f.sessions, name)
	return nil
}

func (f *FakeExecutor) HasSession(name string) bool {
	_, ok := f.sessions[name]
	return ok
}

func (f *FakeExecutor) SendKeys(target, keys string) error {
	f.keysCalls = append(f.keysCalls, KeysCall{Target: target, Keys: keys})
	return nil
}

func (f *FakeExecutor) KeysSent() []KeysCall {
	return f.keysCalls
}

func (f *FakeExecutor) SendKeysRaw(target string, keys ...string) error {
	f.rawKeysCalls = append(f.rawKeysCalls, RawKeysCall{Target: target, Keys: keys})
	return nil
}

func (f *FakeExecutor) RawKeysSent() []RawKeysCall {
	return f.rawKeysCalls
}

func (f *FakeExecutor) SetPaneCommand(target, cmd string) {
	f.paneCommands[target] = cmd
}

func (f *FakeExecutor) SetPaneContent(target, content string) {
	f.paneContents[target] = content
}

func (f *FakeExecutor) SetPaneChildren(target string, cmds []string) {
	f.paneChildren[target] = cmds
}

func (f *FakeExecutor) PanePID(target string) (string, error) {
	return "fake-pid", nil
}

func (f *FakeExecutor) PaneChildCommands(target string) ([]string, error) {
	cmds, ok := f.paneChildren[target]
	if !ok {
		return nil, nil // no children
	}
	return cmds, nil
}

func (f *FakeExecutor) PaneCurrentCommand(target string) (string, error) {
	cmd, ok := f.paneCommands[target]
	if !ok {
		return "", fmt.Errorf("no pane command for target %q", target)
	}
	return cmd, nil
}

func (f *FakeExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	content, ok := f.paneContents[target]
	if !ok {
		return "", fmt.Errorf("no pane content for target %q", target)
	}
	return content, nil
}

func (f *FakeExecutor) SetPaneSize(target string, cols, rows int) {
	f.paneSizes[target] = [2]int{cols, rows}
}

func (f *FakeExecutor) PaneSizeOf(target string) ([2]int, bool) {
	sz, ok := f.paneSizes[target]
	return sz, ok
}

func (f *FakeExecutor) PaneSize(target string) (int, int, error) {
	sz, ok := f.paneSizes[target]
	if !ok {
		return 80, 24, nil // default
	}
	return sz[0], sz[1], nil
}

func (f *FakeExecutor) ResizeWindow(target string, cols, rows int) error {
	f.paneSizes[target] = [2]int{cols, rows}
	return nil
}

func (f *FakeExecutor) ResizeWindowAuto(target string) error {
	f.autoResizeCalls = append(f.autoResizeCalls, target)
	return nil
}

func (f *FakeExecutor) AutoResizeCalls() []string {
	return f.autoResizeCalls
}
