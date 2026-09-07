// internal/tmux/executor_test.go
package tmux_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/tmux"
)

func TestListSessions(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("dev", "/home/user/dev")
	fake.AddSession("prod", "/home/user/prod")

	sessions, err := fake.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(sessions))
	}
}

func TestNewSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()

	err := fake.NewSession("test", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !fake.HasSession("test") {
		t.Error("session should exist after creation")
	}
}

func TestKillSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("doomed", "/tmp")

	fake.KillSession("doomed")

	if fake.HasSession("doomed") {
		t.Error("session should not exist after kill")
	}
}

func TestKillNonexistent(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	err := fake.KillSession("nope")
	if err != tmux.ErrNoSession {
		t.Errorf("want ErrNoSession, got %v", err)
	}
}

func TestHasSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("exists", "/tmp")

	if !fake.HasSession("exists") {
		t.Error("want true")
	}
	if fake.HasSession("nope") {
		t.Error("want false")
	}
}

func TestSendKeysRaw(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("test", "/tmp")

	err := fake.SendKeysRaw("test", "C-u")
	if err != nil {
		t.Fatal(err)
	}

	keys := fake.RawKeysSent()
	if len(keys) != 1 || keys[0].Target != "test" || keys[0].Keys[0] != "C-u" {
		t.Errorf("unexpected raw keys: %+v", keys)
	}
}

func TestSendKeysRawMultiple(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("test", "/tmp")

	err := fake.SendKeysRaw("test", "C-u", "C-c")
	if err != nil {
		t.Fatal(err)
	}

	keys := fake.RawKeysSent()
	if len(keys) != 1 || len(keys[0].Keys) != 2 {
		t.Errorf("want 1 call with 2 keys, got %+v", keys)
	}
}

func TestListSessionsIncludesID(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("main", "/home/user")
	sessions, err := fake.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].Name != "main" {
		t.Errorf("want name 'main', got %q", sessions[0].Name)
	}
	if sessions[0].ID == "" {
		t.Error("session ID should be set")
	}
	if !strings.HasPrefix(sessions[0].ID, "$") {
		t.Errorf("ID should start with $, got %q", sessions[0].ID)
	}
}

func TestFakeExecutorIDAutoIncrement(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("alpha", "/tmp/a")
	fake.AddSession("beta", "/tmp/b")
	fake.AddSession("gamma", "/tmp/c")

	sessions, err := fake.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(sessions))
	}
	// IDs should be $0, $1, $2 in insertion order
	for i, s := range sessions {
		want := fmt.Sprintf("$%d", i)
		if s.ID != want {
			t.Errorf("session[%d] want ID %q, got %q", i, want, s.ID)
		}
	}
}

func TestAddSessionWithID(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSessionWithID("$42", "custom", "/tmp/custom")

	sessions, err := fake.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "$42" {
		t.Errorf("want ID '$42', got %q", sessions[0].ID)
	}
	if sessions[0].Name != "custom" {
		t.Errorf("want name 'custom', got %q", sessions[0].Name)
	}
}

func TestNewSessionAssignsID(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	err := fake.NewSession("auto", "/tmp/auto")
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := fake.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].ID == "" {
		t.Error("NewSession should assign an ID")
	}
	if !strings.HasPrefix(sessions[0].ID, "$") {
		t.Errorf("ID should start with $, got %q", sessions[0].ID)
	}
}

func TestTmuxAliveDefault(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	// Default should be true (tmux assumed alive)
	if !fake.TmuxAlive() {
		t.Error("TmuxAlive() default = false, want true")
	}
}

func TestTmuxAliveSetFalse(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetAlive(false)
	if fake.TmuxAlive() {
		t.Error("TmuxAlive() = true after SetAlive(false), want false")
	}
}

func TestPasteText(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("test", "/tmp")

	err := fake.PasteText("test", "/path/to/image.png")
	if err != nil {
		t.Fatal(err)
	}

	pastes := fake.PastesSent()
	if len(pastes) != 1 {
		t.Fatalf("want 1 paste call, got %d", len(pastes))
	}
	if pastes[0].Target != "test" {
		t.Errorf("want target 'test', got %q", pastes[0].Target)
	}
	if pastes[0].Text != "/path/to/image.png" {
		t.Errorf("want text '/path/to/image.png', got %q", pastes[0].Text)
	}
}

func TestPasteTextFail(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.FailPasteText = true

	err := fake.PasteText("test", "anything")
	if err == nil {
		t.Error("want error when FailPasteText is true")
	}
}

func TestFakeExecutor_PaneCurrentPath(t *testing.T) {
	f := tmux.NewFakeExecutor()
	f.SetPaneCwd("sess-A", "/home/user/proj")
	got, err := f.PaneCurrentPath("sess-A")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "/home/user/proj" {
		t.Errorf("got %q, want /home/user/proj", got)
	}
}

func TestRealExecutorShowWindowOptionUsesWindowShowOptions(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1" != "show-options" ] || [ "$2" != "-w" ] || [ "$3" != "-g" ] || [ "$4" != "-q" ] || [ "$5" != "-v" ] || [ "$6" != "allow-set-title" ] || [ -n "$7" ]; then
  printf 'unexpected args: %s\n' "$*" >&2
  exit 2
fi
printf 'on\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	value, err := (&tmux.RealExecutor{}).ShowWindowOption("allow-set-title")
	if err != nil {
		t.Fatalf("ShowWindowOption returned error: %v", err)
	}
	if value != "on" {
		t.Fatalf("ShowWindowOption() = %q, want on", value)
	}
}

// `default-shell` is a server option, and `show-options -w` cannot read one:
// asking through ShowWindowOption returns empty and the shell probe would
// silently fall through to $SHELL. So the argv is asserted, not just the value.
func TestRealExecutorShowGlobalOptionUsesServerShowOptions(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1" != "show-options" ] || [ "$2" != "-g" ] || [ "$3" != "-q" ] || [ "$4" != "-v" ] || [ "$5" != "default-shell" ] || [ -n "$6" ]; then
  printf 'unexpected args: %s\n' "$*" >&2
  exit 2
fi
printf '/bin/zsh\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	value, err := (&tmux.RealExecutor{}).ShowGlobalOption("default-shell")
	if err != nil {
		t.Fatalf("ShowGlobalOption returned error: %v", err)
	}
	if value != "/bin/zsh" {
		t.Fatalf("ShowGlobalOption() = %q, want /bin/zsh", value)
	}
}

// The fake keeps global options in their own map: a caller that reaches for
// ShowWindowOption instead must not accidentally find the value there.
func TestFakeExecutor_ShowGlobalOption(t *testing.T) {
	f := tmux.NewFakeExecutor()

	value, err := f.ShowGlobalOption("default-shell")
	if err != nil {
		t.Fatalf("ShowGlobalOption on an unset option returned error: %v", err)
	}
	if value != "" {
		t.Fatalf("ShowGlobalOption() = %q, want empty", value)
	}

	f.SetGlobalOptionValue("default-shell", "/bin/zsh")
	value, err = f.ShowGlobalOption("default-shell")
	if err != nil {
		t.Fatalf("ShowGlobalOption returned error: %v", err)
	}
	if value != "/bin/zsh" {
		t.Fatalf("ShowGlobalOption() = %q, want /bin/zsh", value)
	}
	if got, err := f.ShowWindowOption("default-shell"); err != nil || got != "" {
		t.Fatalf("ShowWindowOption saw the global option: %q, %v", got, err)
	}
	if calls := f.ShowGlobalOptionCalls(); len(calls) != 2 || calls[0] != "default-shell" {
		t.Fatalf("ShowGlobalOptionCalls() = %v", calls)
	}

	f.SetGlobalOptionError("default-shell", fmt.Errorf("no server running"))
	if _, err := f.ShowGlobalOption("default-shell"); err == nil {
		t.Fatal("expected the seeded error")
	}
}

func TestFakeExecutor_PaneCurrentPath_NotSet(t *testing.T) {
	f := tmux.NewFakeExecutor()
	_, err := f.PaneCurrentPath("sess-nope")
	if err == nil {
		t.Error("expected error for unset pane cwd")
	}
}

// --- Slice 0: CapturePaneRange / CapturePaneTopLines tests (TT1-TT4) ---

// TT1: RealExecutor passes -S/-E args through to tmux capture-pane.
func TestCapturePaneRange_RealExecutor_PassesArgs(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1" != "capture-pane" ] || [ "$2" != "-e" ] || [ "$3" != "-p" ] || [ "$4" != "-t" ] || [ "$5" != "sess:" ] || [ "$6" != "-S" ] || [ "$7" != "2" ] || [ "$8" != "-E" ] || [ "$9" != "5" ]; then
  printf 'unexpected args: %s\n' "$*" >&2
  exit 2
fi
printf 'line2\nline3\nline4\nline5\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := (&tmux.RealExecutor{}).CapturePaneRange("sess:", 2, 5)
	if err != nil {
		t.Fatalf("CapturePaneRange returned error: %v", err)
	}
	if out != "line2\nline3\nline4\nline5\n" {
		t.Fatalf("CapturePaneRange = %q, want 4 lines", out)
	}
}

// TT2: CapturePaneTopLines delegates to CapturePaneRange(target, 0, n-1).
func TestCapturePaneTopLines_DelegatesToRange(t *testing.T) {
	cases := []struct {
		n       int
		wantEnd string // expected $6 (the -E value) in the script-mode mock
	}{
		{n: 1, wantEnd: "0"},
		{n: 3, wantEnd: "2"},
		{n: 10, wantEnd: "9"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("n=%d", tc.n), func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "tmux")
			body := fmt.Sprintf(`#!/bin/sh
if [ "$1" != "capture-pane" ] || [ "$2" != "-e" ] || [ "$3" != "-p" ] || [ "$4" != "-t" ] || [ "$5" != "sess:" ] || [ "$6" != "-S" ] || [ "$7" != "0" ] || [ "$8" != "-E" ] || [ "$9" != "%s" ]; then
  printf 'unexpected args: %%s\n' "$*" >&2
  exit 2
fi
printf 'ok\n'
`, tc.wantEnd)
			if err := os.WriteFile(script, []byte(body), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			out, err := (&tmux.RealExecutor{}).CapturePaneTopLines("sess:", tc.n)
			if err != nil {
				t.Fatalf("CapturePaneTopLines(n=%d) error: %v", tc.n, err)
			}
			if out != "ok\n" {
				t.Fatalf("CapturePaneTopLines(n=%d) = %q, want %q", tc.n, out, "ok\n")
			}
		})
	}
}

// TT3: CapturePaneTopLines with n<=0 returns "" without invoking tmux.
func TestCapturePaneTopLines_ZeroOrNegative_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	// A tmux shim that fails the test if invoked.
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'tmux must not be invoked for n<=0\n' >&2
exit 99
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, n := range []int{0, -1} {
		out, err := (&tmux.RealExecutor{}).CapturePaneTopLines("sess:", n)
		if err != nil {
			t.Errorf("CapturePaneTopLines(n=%d) error = %v, want nil", n, err)
		}
		if out != "" {
			t.Errorf("CapturePaneTopLines(n=%d) = %q, want \"\"", n, out)
		}
	}
}

// TT4: FakeExecutor range methods round-trip per-line content.
func TestFakeExecutor_RangeMethods_Roundtrip(t *testing.T) {
	f := tmux.NewFakeExecutor()
	lines := []string{"line0", "line1", "line2", "line3", "line4"}
	f.SetPaneContentByRange("sess:", lines)

	cases := []struct {
		name  string
		start int
		end   int
		want  string
	}{
		{name: "single line 0", start: 0, end: 0, want: "line0\n"},
		{name: "lines 1..3", start: 1, end: 3, want: "line1\nline2\nline3\n"},
		{name: "all 0..4", start: 0, end: 4, want: "line0\nline1\nline2\nline3\nline4\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.CapturePaneRange("sess:", tc.start, tc.end)
			if err != nil {
				t.Fatalf("CapturePaneRange(%d,%d) error: %v", tc.start, tc.end, err)
			}
			if got != tc.want {
				t.Errorf("CapturePaneRange(%d,%d) = %q, want %q", tc.start, tc.end, got, tc.want)
			}
		})
	}

	// CapturePaneTopLines(_, 3) → first 3 lines.
	got, err := f.CapturePaneTopLines("sess:", 3)
	if err != nil {
		t.Fatalf("CapturePaneTopLines(3) error: %v", err)
	}
	want := "line0\nline1\nline2\n"
	if got != want {
		t.Errorf("CapturePaneTopLines(3) = %q, want %q", got, want)
	}
}

// --- P2-T1: Executor.HasPane(paneID) tests ---

// TestHasPane_PaneExists_ReturnsTrue verifies that HasPane returns true
// when the requested pane id appears in `tmux list-panes -a -F '#{pane_id}'`.
func TestHasPane_PaneExists_ReturnsTrue(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	// Mimic `tmux list-panes -a -F '#{pane_id}'` returning two panes.
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1" != "list-panes" ] || [ "$2" != "-a" ] || [ "$3" != "-F" ] || [ "$4" != "#{pane_id}" ]; then
  printf 'unexpected args: %s\n' "$*" >&2
  exit 2
fi
printf '%%5\n%%7\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := (&tmux.RealExecutor{}).HasPane("%5")
	if err != nil {
		t.Fatalf("HasPane unexpected err: %v", err)
	}
	if !got {
		t.Errorf("HasPane(%q) = false, want true", "%5")
	}
}

// TestHasPane_PaneNotInList_ReturnsFalse verifies that HasPane returns false
// when the pane id is absent from `tmux list-panes` output.
func TestHasPane_PaneNotInList_ReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1" != "list-panes" ] || [ "$2" != "-a" ] || [ "$3" != "-F" ] || [ "$4" != "#{pane_id}" ]; then
  printf 'unexpected args: %s\n' "$*" >&2
  exit 2
fi
printf '%%5\n%%7\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := (&tmux.RealExecutor{}).HasPane("%9")
	if err != nil {
		t.Fatalf("HasPane unexpected err: %v", err)
	}
	if got {
		t.Errorf("HasPane(%q) = true, want false", "%9")
	}
}

// TestHasPane_TransientTmuxError_ReturnsError verifies that a generic
// tmux invocation error (NOT the documented "no server running" branch
// — that one is confirmed absence per F10) surfaces as err != nil. Per
// round-4 audit: callers distinguish transient query failure from
// confirmed absence to avoid false-positive "pane gone" emissions
// during tmux hiccups.
func TestHasPane_TransientTmuxError_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'something else went wrong\n' >&2
exit 1
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := (&tmux.RealExecutor{}).HasPane("%5")
	if err == nil {
		t.Fatalf("HasPane returned err=nil on transient tmux error, want non-nil")
	}
	if got {
		t.Errorf("HasPane returned true on tmux error, want false")
	}
}

// TestHasPane_NoServerRunning_ReturnsConfirmedAbsence verifies the round-5
// F10 fix: tmux exits with stderr "no server running" when there are no
// active sessions. That is global confirmed absence (every pane is gone),
// not a transient query failure — caller should drive the clear path.
func TestHasPane_NoServerRunning_ReturnsConfirmedAbsence(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'no server running on /private/tmp/tmux-501/default\n' >&2
exit 1
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := (&tmux.RealExecutor{}).HasPane("%5")
	if err != nil {
		t.Fatalf("HasPane returned err=%v on no-server, want nil (confirmed absence)", err)
	}
	if got {
		t.Errorf("HasPane returned true on no-server, want false")
	}
}

// TestHasPane_EmptyPaneID_ReturnsFalse verifies that an empty paneID short
// circuits to false without invoking tmux. The script below would fail the
// test if executed. err must be nil — empty paneID is confirmed absent,
// not a query failure.
func TestHasPane_EmptyPaneID_ReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'tmux must not be invoked for empty paneID\n' >&2
exit 99
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := (&tmux.RealExecutor{}).HasPane("")
	if err != nil {
		t.Fatalf("HasPane(\"\") unexpected err: %v", err)
	}
	if got {
		t.Errorf("HasPane(\"\") = true, want false")
	}
}
