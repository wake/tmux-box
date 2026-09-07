package tmux

import (
	"fmt"
	"strings"
	"sync"
)

// --- Fake Executor (test double) ---

type RawKeysCall struct {
	Target string
	Keys   []string
}

type KeysCall struct {
	Target string
	Keys   string
}

type SetWindowOptionCall struct {
	Target string
	Option string
	Value  string
	Global bool
}

type PasteCall struct {
	Target string
	Text   string
}

type FakeExecutor struct {
	mu                   sync.Mutex
	sessions             map[string]TmuxSession // keyed by name for O(1) lookup
	sessionOrder         []string               // insertion order of session names
	nextID               int                    // auto-incrementing ID counter
	paneCommands         map[string]string      // target → command name
	paneCommandCalls     map[string]int         // target → PaneCurrentCommand call count
	activePaneMetadata   map[string]TmuxPaneMetadata
	activePaneMetaErrors map[string]error
	// activePaneMetaHook runs after each ActivePaneMetadata read, outside the
	// lock. It is the seam for "the world moved while the daemon was reading
	// metadata" — a tmux server restart being the case that matters.
	activePaneMetaHook func(sessionName string)
	// instance is the fake server's generation, the value SendKeysIfInstance
	// compares against. Tests move it to model a restart.
	instance             string
	paneContents         map[string]string   // target → captured text
	paneContentByRange   map[string][]string // target → per-line slice (for CapturePaneRange / CapturePaneTopLines)
	paneChildren         map[string][]string // target → child command names
	paneDescendants      map[string][]string // target → recursive descendant command names
	panePIDs             map[string]string   // target → pane pid
	activePanePIDs       map[string]string   // target → active pane pid
	paneSessions         map[string]string   // target → session name
	paneCwds             map[string]string   // target → pane_current_path
	paneSizes            map[string][2]int   // target → [cols, rows]
	rawKeysCalls         []RawKeysCall
	keysCalls            []KeysCall
	pasteCalls           []PasteCall
	autoResizeCalls      []string              // targets passed to ResizeWindowAuto
	setWindowOptionCalls []SetWindowOptionCall // calls to SetWindowOption
	windowOptions        map[string]string
	paneIDs              []string // global pane id list for HasPane
	hasPaneErr           error    // simulated transient tmux error for HasPane
	listCallCount        int      // how many times ListSessions was called
	alive                bool     // whether tmux server is "alive"
	HooksOutput          string   // returned by ShowHooksGlobal
	FailSendKeys         bool     // if true, SendKeysRaw returns an error
	FailPasteText        bool     // if true, PasteText returns an error
}

func NewFakeExecutor() *FakeExecutor {
	return &FakeExecutor{
		sessions:             make(map[string]TmuxSession),
		paneCommands:         make(map[string]string),
		paneCommandCalls:     make(map[string]int),
		activePaneMetadata:   make(map[string]TmuxPaneMetadata),
		activePaneMetaErrors: make(map[string]error),
		paneContents:         make(map[string]string),
		paneContentByRange:   make(map[string][]string),
		paneChildren:         make(map[string][]string),
		paneDescendants:      make(map[string][]string),
		panePIDs:             make(map[string]string),
		activePanePIDs:       make(map[string]string),
		paneSessions:         make(map[string]string),
		paneCwds:             make(map[string]string),
		paneSizes:            make(map[string][2]int),
		windowOptions:        make(map[string]string),
		alive:                true,
	}
}

// AddSession adds a session with an auto-assigned ID ($0, $1, …).
func (f *FakeExecutor) AddSession(name, cwd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("$%d", f.nextID)
	f.nextID++
	f.sessions[name] = TmuxSession{ID: id, Name: name, Cwd: cwd}
	f.sessionOrder = append(f.sessionOrder, name)
}

// AddSessionWithID adds a session with an explicit ID (for test control).
func (f *FakeExecutor) AddSessionWithID(id, name, cwd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[name] = TmuxSession{ID: id, Name: name, Cwd: cwd}
	f.sessionOrder = append(f.sessionOrder, name)
}

func (f *FakeExecutor) ListSessions() ([]TmuxSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCallCount++
	out := make([]TmuxSession, 0, len(f.sessionOrder))
	for _, name := range f.sessionOrder {
		if s, ok := f.sessions[name]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *FakeExecutor) SetActivePaneMetadata(sessionName string, metadata TmuxPaneMetadata) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activePaneMetadata[sessionName] = metadata
	delete(f.activePaneMetaErrors, sessionName)
}

func (f *FakeExecutor) SetActivePaneMetadataError(sessionName string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activePaneMetaErrors[sessionName] = err
	delete(f.activePaneMetadata, sessionName)
}

// SetActivePaneMetadataHook installs a callback run after every
// ActivePaneMetadata read, with the fake's lock released so the callback may
// mutate it. On the real thing this read is several tmux subprocesses, which
// is where a server restart has time to land.
func (f *FakeExecutor) SetActivePaneMetadataHook(fn func(sessionName string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activePaneMetaHook = fn
}

// SetInstance sets the fake server's generation. Changing it models a tmux
// server restart: the sessions stay (a new server mints the same ids), but
// nothing that was authorised against the old generation may still act.
func (f *FakeExecutor) SetInstance(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instance = v
}

// Instance reads the fake server's generation. Assign it as a module's
// `tmuxInstanceFn` so the daemon and the executor sample one world.
func (f *FakeExecutor) Instance() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.instance
}

func (f *FakeExecutor) ActivePaneMetadata(sessionName string) (TmuxPaneMetadata, error) {
	metadata, err := f.readActivePaneMetadata(sessionName)
	if hook := f.metadataHook(); hook != nil {
		hook(sessionName)
	}
	return metadata, err
}

func (f *FakeExecutor) metadataHook() func(string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activePaneMetaHook
}

func (f *FakeExecutor) readActivePaneMetadata(sessionName string) (TmuxPaneMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.activePaneMetaErrors[sessionName]; ok {
		return TmuxPaneMetadata{}, err
	}
	metadata, ok := f.activePaneMetadata[sessionName]
	if !ok {
		return TmuxPaneMetadata{}, fmt.Errorf("active pane metadata not configured for %s", sessionName)
	}
	metadata.SessionID = sanitizeTmuxMetadata(metadata.SessionID)
	metadata.SessionName = sanitizeTmuxMetadata(metadata.SessionName)
	metadata.WindowID = sanitizeTmuxMetadata(metadata.WindowID)
	metadata.PaneID = sanitizeTmuxMetadata(metadata.PaneID)
	metadata.PaneTitle = sanitizeTmuxMetadata(metadata.PaneTitle)
	metadata.WindowName = sanitizeTmuxMetadata(metadata.WindowName)
	metadata.PaneCurrentCommand = sanitizeTmuxMetadata(metadata.PaneCurrentCommand)
	return metadata, nil
}

// ListCallCount returns how many times ListSessions was called.
func (f *FakeExecutor) ListCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCallCount
}

// NewSession creates a session with an auto-assigned ID.
func (f *FakeExecutor) NewSession(name, cwd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("$%d", f.nextID)
	f.nextID++
	f.sessions[name] = TmuxSession{ID: id, Name: name, Cwd: cwd}
	f.sessionOrder = append(f.sessionOrder, name)
	return nil
}

func (f *FakeExecutor) KillSession(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[name]; !ok {
		return ErrNoSession
	}
	delete(f.sessions, name)
	// Remove from insertion-order slice.
	for i, n := range f.sessionOrder {
		if n == name {
			f.sessionOrder = append(f.sessionOrder[:i], f.sessionOrder[i+1:]...)
			break
		}
	}
	return nil
}

func (f *FakeExecutor) RenameSession(oldName, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[oldName]
	if !ok {
		return ErrNoSession
	}
	delete(f.sessions, oldName)
	s.Name = newName
	f.sessions[newName] = s
	for i, n := range f.sessionOrder {
		if n == oldName {
			f.sessionOrder[i] = newName
			break
		}
	}
	return nil
}

func (f *FakeExecutor) HasSession(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sessions[name]
	return ok
}

// HasPane reports whether paneID exists in the fake's recorded pane list.
// Tests register panes via SetPanes (or the existing per-target maps). Empty
// paneID returns false without lookup, matching RealExecutor semantics.
func (f *FakeExecutor) HasPane(paneID string) (bool, error) {
	if paneID == "" {
		return false, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasPaneErr != nil {
		return false, f.hasPaneErr
	}
	for _, id := range f.paneIDs {
		if id == paneID {
			return true, nil
		}
	}
	return false, nil
}

// SetHasPaneError configures the fake to surface this error from the next
// HasPane invocations until cleared (passing nil clears). Used by tests
// that exercise the round-4 transient-tmux-error semantics — pane
// existence is unknown rather than confirmed-absent.
func (f *FakeExecutor) SetHasPaneError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hasPaneErr = err
}

// SetPanes replaces the fake's known pane id list. Used by tests that rely
// on HasPane (e.g. ProbeIntent ProcessDead detector integration tests).
func (f *FakeExecutor) SetPanes(paneIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneIDs = append(f.paneIDs[:0:0], paneIDs...)
}

func (f *FakeExecutor) SendKeys(target, keys string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keysCalls = append(f.keysCalls, KeysCall{Target: target, Keys: keys})
	return nil
}

func (f *FakeExecutor) KeysSent() []KeysCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keysCalls
}

func (f *FakeExecutor) SendKeysRaw(target string, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rawKeysCalls = append(f.rawKeysCalls, RawKeysCall{Target: target, Keys: keys})
	if f.FailSendKeys {
		return fmt.Errorf("send-keys: simulated failure")
	}
	return nil
}

// SendKeysIfInstance models the real contract: the generation is compared by
// the same "server" that would deliver the keys, so a refusal records nothing
// at all. An empty expectation matches nothing — unknown authorises no
// keystroke (spec §4.6.2).
func (f *FakeExecutor) SendKeysIfInstance(sessionID, expectedInstance string, keys ...string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailSendKeys {
		return false, fmt.Errorf("send-keys: simulated failure")
	}
	if expectedInstance == "" || expectedInstance != f.instance {
		return false, nil
	}
	f.rawKeysCalls = append(f.rawKeysCalls, RawKeysCall{Target: sessionID + ":", Keys: keys})
	return true, nil
}

func (f *FakeExecutor) PasteText(target, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pasteCalls = append(f.pasteCalls, PasteCall{Target: target, Text: text})
	if f.FailPasteText {
		return fmt.Errorf("paste-buffer: simulated failure")
	}
	return nil
}

func (f *FakeExecutor) PastesSent() []PasteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pasteCalls
}

func (f *FakeExecutor) RawKeysSent() []RawKeysCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rawKeysCalls
}

func (f *FakeExecutor) SetPaneCommand(target, cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneCommands[target] = cmd
}

func (f *FakeExecutor) SetPaneContent(target, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneContents[target] = content
}

func (f *FakeExecutor) SetPaneChildren(target string, cmds []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneChildren[target] = cmds
}

func (f *FakeExecutor) SetPaneDescendants(target string, cmds []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneDescendants[target] = cmds
}

func (f *FakeExecutor) SetPanePID(target, pid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panePIDs[target] = pid
}

func (f *FakeExecutor) SetPaneSessionName(target, session string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneSessions[target] = session
}

func (f *FakeExecutor) PaneSessionName(target string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if session, ok := f.paneSessions[target]; ok {
		return session, nil
	}
	return "", fmt.Errorf("pane session not configured for %s", target)
}

func (f *FakeExecutor) PanePID(target string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pid, ok := f.panePIDs[target]; ok {
		return pid, nil
	}
	return "fake-pid", nil
}

func (f *FakeExecutor) SetActivePanePID(target, pid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activePanePIDs[target] = pid
}

func (f *FakeExecutor) ActivePanePID(target string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pid, ok := f.activePanePIDs[target]; ok {
		return pid, nil
	}
	// Fall through to panePIDs so tests that only set PanePID still work.
	if pid, ok := f.panePIDs[target]; ok {
		return pid, nil
	}
	return "fake-active-pid", nil
}

func (f *FakeExecutor) PaneChildCommands(target string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmds, ok := f.paneChildren[target]
	if !ok {
		return nil, nil // no children
	}
	return append([]string(nil), cmds...), nil
}

func (f *FakeExecutor) PaneDescendantCommands(target string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cmds, ok := f.paneDescendants[target]; ok {
		return append([]string(nil), cmds...), nil
	}
	if cmds, ok := f.paneChildren[target]; ok {
		return append([]string(nil), cmds...), nil
	}
	return nil, nil
}

func (f *FakeExecutor) PaneCurrentCommand(target string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneCommandCalls[target]++
	cmd, ok := f.paneCommands[target]
	if !ok {
		return "", fmt.Errorf("no pane command for target %q", target)
	}
	return cmd, nil
}

func (f *FakeExecutor) SetPaneCwd(target, cwd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneCwds[target] = cwd
}

func (f *FakeExecutor) PaneCurrentPath(target string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cwd, ok := f.paneCwds[target]
	if !ok {
		return "", fmt.Errorf("fake: no pane cwd set for %q", target)
	}
	return cwd, nil
}

// PaneCommandCallCount returns how many times PaneCurrentCommand was called for a target.
func (f *FakeExecutor) PaneCommandCallCount(target string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paneCommandCalls[target]
}

func (f *FakeExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	content, ok := f.paneContents[target]
	if !ok {
		return "", fmt.Errorf("no pane content for target %q", target)
	}
	return content, nil
}

// SetPaneContentByRange installs a per-line slice for the target. Both
// CapturePaneRange and CapturePaneTopLines read from this same source so
// tests can inject any window of lines and assert the slice they receive.
func (f *FakeExecutor) SetPaneContentByRange(target string, lines []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneContentByRange[target] = append([]string(nil), lines...)
}

func (f *FakeExecutor) CapturePaneRange(target string, start, endInclusive int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lines, ok := f.paneContentByRange[target]
	if !ok {
		return "", fmt.Errorf("no pane content by range for target %q", target)
	}
	if start < 0 {
		start = 0
	}
	if endInclusive >= len(lines) {
		endInclusive = len(lines) - 1
	}
	if start > endInclusive {
		return "", nil
	}
	var b strings.Builder
	for i := start; i <= endInclusive; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func (f *FakeExecutor) CapturePaneTopLines(target string, n int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	return f.CapturePaneRange(target, 0, n-1)
}

func (f *FakeExecutor) SetPaneSize(target string, cols, rows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneSizes[target] = [2]int{cols, rows}
}

func (f *FakeExecutor) PaneSizeOf(target string) ([2]int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sz, ok := f.paneSizes[target]
	return sz, ok
}

func (f *FakeExecutor) PaneSize(target string) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sz, ok := f.paneSizes[target]
	if !ok {
		return 80, 24, nil // default
	}
	return sz[0], sz[1], nil
}

func (f *FakeExecutor) ResizeWindow(target string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneSizes[target] = [2]int{cols, rows}
	return nil
}

func (f *FakeExecutor) ResizeWindowAuto(target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoResizeCalls = append(f.autoResizeCalls, target)
	return nil
}

func (f *FakeExecutor) AutoResizeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.autoResizeCalls
}

func (f *FakeExecutor) SetWindowOption(target, option, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setWindowOptionCalls = append(f.setWindowOptionCalls, SetWindowOptionCall{
		Target: target,
		Option: option,
		Value:  value,
	})
	f.windowOptions[option] = value
	return nil
}

func (f *FakeExecutor) SetWindowOptionGlobal(option, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setWindowOptionCalls = append(f.setWindowOptionCalls, SetWindowOptionCall{
		Option: option,
		Value:  value,
		Global: true,
	})
	f.windowOptions[option] = value
	return nil
}

func (f *FakeExecutor) SetWindowOptionValue(option, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windowOptions[option] = value
}

func (f *FakeExecutor) ShowWindowOption(option string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.windowOptions[option], nil
}

func (f *FakeExecutor) SetWindowOptionCalls() []SetWindowOptionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setWindowOptionCalls
}

func (f *FakeExecutor) SetHookGlobal(event, command string) error { return nil }
func (f *FakeExecutor) RemoveHookGlobal(event string) error       { return nil }

func (f *FakeExecutor) ShowHooksGlobal() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.HooksOutput, nil
}

func (f *FakeExecutor) SetAlive(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive = v
}

func (f *FakeExecutor) TmuxAlive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive
}
