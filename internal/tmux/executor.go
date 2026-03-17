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
	CapturePaneContent(target string, lastN int) (string, error)
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

func (r *RealExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	arg := fmt.Sprintf("-%d", lastN)
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", arg).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// --- Fake Executor ---

type RawKeysCall struct {
	Target string
	Keys   []string
}

type FakeExecutor struct {
	sessions     map[string]TmuxSession
	paneCommands map[string]string // target → command name
	paneContents map[string]string // target → captured text
	rawKeysCalls []RawKeysCall
}

func NewFakeExecutor() *FakeExecutor {
	return &FakeExecutor{
		sessions:     make(map[string]TmuxSession),
		paneCommands: make(map[string]string),
		paneContents: make(map[string]string),
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

func (f *FakeExecutor) SendKeys(_, _ string) error { return nil }

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
