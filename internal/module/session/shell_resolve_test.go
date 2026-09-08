package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- harness ---------------------------------------------------------------

type probeSpy struct {
	mu      sync.Mutex
	calls   int
	shells  []string
	tokens  []string
	outcome shellProbeOutcome
}

func (s *probeSpy) fn(_ context.Context, shell, token string) shellProbeOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.shells = append(s.shells, shell)
	s.tokens = append(s.tokens, token)
	return s.outcome
}

// newShellResolveServer wires the module's route onto a test server and swaps
// the exec seam for a spy, so a rejection can be proved to have happened
// BEFORE any shell was started.
func newShellResolveServer(t *testing.T) (*httptest.Server, *probeSpy, *SessionModule) {
	t.Helper()
	mod, _, _ := newTestModule(t)
	spy := &probeSpy{}
	mod.shellProbe = spy.fn
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, spy, mod
}

type resolveVerdict struct {
	Resolved bool   `json:"resolved"`
	Detail   string `json:"detail"`
	Reason   string `json:"reason"`
}

func postResolve(t *testing.T, srv *httptest.Server, body string) (int, resolveVerdict) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/api/shell/resolve-command", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, resolveVerdict{}
	}
	var v resolveVerdict
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	return resp.StatusCode, v
}

func resolveCommand(t *testing.T, srv *httptest.Server, command string) resolveVerdict {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"command": command})
	require.NoError(t, err)
	status, v := postResolve(t, srv, string(payload))
	require.Equal(t, http.StatusOK, status)
	return v
}

// --- the contract: 400 is only ever a malformed body ------------------------

func TestShellResolve_MalformedBodyIsTheOnly400(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)

	for _, body := range []string{
		`{}`,                 // missing
		`{"command": 5}`,     // not a string
		`{"command": null}`,  // explicitly absent
		`{"command": ["a"]}`, // not a string
		`not json at all`,
	} {
		status, _ := postResolve(t, srv, body)
		assert.Equal(t, http.StatusBadRequest, status, "body %s", body)
	}
	assert.Zero(t, spy.calls, "a malformed body must never reach a shell")
}

// --- the body is bounded before it is decoded --------------------------------

// The token cap is checked AFTER the decode, and a JSON decoder has already
// buffered the whole string value by the time it can be applied — so a
// multi-megabyte `command` is read into memory in full before anything rejects
// it. The bound has to sit in front of the decoder.
func TestShellResolve_OversizeBodyIsRefusedBeforeItIsBuffered(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)

	status, _ := postResolve(t, srv, `{"command":"`+strings.Repeat("a", 1<<20)+`"}`)
	assert.Equal(t, http.StatusBadRequest, status, "an oversize body must be refused, not decoded")
	assert.Zero(t, spy.calls, "an oversize body must never reach a shell")

	// The token check still owns everything that fits: a 257-byte token in a
	// small body is `too_long` with a 200, not a transport error.
	v := resolveCommand(t, srv, strings.Repeat("a", 257))
	assert.Equal(t, "too_long", v.Reason)
	assert.Zero(t, spy.calls)
}

// --- rejected before exec ---------------------------------------------------

func TestShellResolve_RejectsShellMetacharactersWithoutExec(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)

	// Every character §4.4 names, plus a newline and a leading dash.
	for _, token := range []string{
		"a|b", "a&b", "a;b", "a<b", "a>b", "a(b", "a)b", "a$b",
		"a`b", `a\b`, `a"b`, "a'b", "a\nb", "-rf",
	} {
		v := resolveCommand(t, srv, token)
		assert.False(t, v.Resolved, "token %q", token)
		assert.Equal(t, "shell_metacharacters", v.Reason, "token %q", token)
		assert.Empty(t, v.Detail, "token %q", token)
	}
	assert.Zero(t, spy.calls, "a rejected token must never reach a shell")
}

func TestShellResolve_RejectsOversizeTokenWithoutExec(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)

	v := resolveCommand(t, srv, strings.Repeat("a", 257))
	assert.False(t, v.Resolved)
	assert.Equal(t, "too_long", v.Reason)
	assert.Zero(t, spy.calls)

	// 256 is allowed, so the boundary is not off by one.
	spy.outcome = shellProbeOutcome{ExitCode: 1}
	v = resolveCommand(t, srv, strings.Repeat("a", 256))
	assert.Equal(t, "not_found", v.Reason)
	assert.Equal(t, 1, spy.calls)
}

// --- verdicts ---------------------------------------------------------------

func TestShellResolve_TimeoutIsAVerdictNotATransportError(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)
	spy.outcome = shellProbeOutcome{TimedOut: true}

	status, v := postResolve(t, srv, `{"command":"cld-yolo"}`)
	assert.Equal(t, http.StatusOK, status)
	assert.False(t, v.Resolved)
	assert.Equal(t, "timeout", v.Reason)
}

func TestShellResolve_NonZeroExitIsNotFound(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)
	spy.outcome = shellProbeOutcome{ExitCode: 1, Stdout: ""}

	v := resolveCommand(t, srv, "cld-yolo")
	assert.False(t, v.Resolved)
	assert.Equal(t, "not_found", v.Reason)
}

func TestShellResolve_UnstartableShellIsShellFailed(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)
	spy.outcome = shellProbeOutcome{Err: fmt.Errorf("fork/exec /nope/zsh: no such file or directory")}

	v := resolveCommand(t, srv, "cld-yolo")
	assert.False(t, v.Resolved)
	assert.Equal(t, "shell_failed", v.Reason)
}

// rc chatter lands on stdout before the answer, so the LAST non-empty line is
// the answer — not the first, and not the whole buffer.
func TestShellResolve_DetailIsTheLastNonEmptyStdoutLine(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)
	spy.outcome = shellProbeOutcome{
		ExitCode: 0,
		Stdout:   "rc chatter\nnvm loaded\n\n/Users/wake/.local/bin/claude\n\n",
	}

	v := resolveCommand(t, srv, "claude")
	assert.True(t, v.Resolved)
	assert.Equal(t, "/Users/wake/.local/bin/claude", v.Detail)
	assert.Empty(t, v.Reason)
}

func TestShellResolve_DetailIsTruncatedForDisplay(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)
	spy.outcome = shellProbeOutcome{ExitCode: 0, Stdout: strings.Repeat("x", 2000)}

	v := resolveCommand(t, srv, "claude")
	assert.True(t, v.Resolved)
	assert.Len(t, v.Detail, 512)
}

// A zero exit with nothing printed is still a resolution; there is simply
// nothing to show. (`resolved` comes from the exit status, never from `detail`.)
func TestShellResolve_ResolvedWithNoOutput(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)
	spy.outcome = shellProbeOutcome{ExitCode: 0, Stdout: "\n \n"}

	v := resolveCommand(t, srv, "claude")
	assert.True(t, v.Resolved)
	assert.Empty(t, v.Reason)
}

// --- the shell-selection ladder ---------------------------------------------

func TestShellResolve_UsesTmuxDefaultShell(t *testing.T) {
	srv, spy, mod := newShellResolveServer(t)
	t.Setenv("SHELL", "/env/shell")
	fake := mod.tmux.(interface {
		SetGlobalOptionValue(option, value string)
		ShowGlobalOptionCalls() []string
	})
	fake.SetGlobalOptionValue("default-shell", "/tmux/zsh")

	resolveCommand(t, srv, "claude")

	require.Equal(t, []string{"/tmux/zsh"}, spy.shells)
	require.Equal(t, []string{"claude"}, spy.tokens)
	// Asked of the SERVER option table. `show-options -w` cannot read
	// default-shell, so a window-option read would silently return "".
	assert.Equal(t, []string{"default-shell"}, fake.ShowGlobalOptionCalls())
}

func TestShellResolve_FallsBackToEnvShellWhenTmuxFails(t *testing.T) {
	srv, spy, mod := newShellResolveServer(t)
	t.Setenv("SHELL", "/env/shell")
	mod.tmux.(interface {
		SetGlobalOptionError(option string, err error)
	}).SetGlobalOptionError("default-shell", fmt.Errorf("no server running"))

	resolveCommand(t, srv, "claude")
	assert.Equal(t, []string{"/env/shell"}, spy.shells)
}

// An empty answer is not an answer: tmux prints nothing for an option it does
// not know, and that must not be started as a program.
func TestShellResolve_FallsBackToEnvShellWhenTmuxAnswersEmpty(t *testing.T) {
	srv, spy, _ := newShellResolveServer(t)
	t.Setenv("SHELL", "/env/shell")

	resolveCommand(t, srv, "claude")
	assert.Equal(t, []string{"/env/shell"}, spy.shells)
}

func TestShellResolve_FallsBackToPasswdShell(t *testing.T) {
	srv, spy, mod := newShellResolveServer(t)
	t.Setenv("SHELL", "")
	mod.passwdShell = func() string { return "/passwd/fish" }

	resolveCommand(t, srv, "claude")
	assert.Equal(t, []string{"/passwd/fish"}, spy.shells)
}

func TestShellResolve_FallsBackToBinSh(t *testing.T) {
	srv, spy, mod := newShellResolveServer(t)
	t.Setenv("SHELL", "")
	mod.passwdShell = func() string { return "" }

	resolveCommand(t, srv, "claude")
	assert.Equal(t, []string{"/bin/sh"}, spy.shells)
}

func TestPasswdShellFromEntries(t *testing.T) {
	const passwd = `# comment line
root:*:0:0:System Administrator:/var/root:/bin/sh
truncated:*:1
wake:*:501:20:Wake:/Users/wake:/bin/zsh
noshell:*:502:20:No Shell:/Users/noshell:
`
	assert.Equal(t, "/bin/zsh", passwdShellFromEntries(passwd, "wake", "501"))
	// The uid matches even when the account name does not.
	assert.Equal(t, "/bin/zsh", passwdShellFromEntries(passwd, "renamed", "501"))
	// An empty shell field is not an answer, and neither is a missing user.
	assert.Empty(t, passwdShellFromEntries(passwd, "noshell", "502"))
	assert.Empty(t, passwdShellFromEntries(passwd, "nobody", "999"))
}

// --- integration: a real interactive login shell -----------------------------

// writeZshrc builds a ZDOTDIR whose .zshrc reproduces the §4.4 shapes: rc
// chatter on stdout before the answer, a function, an alias, and a PATH whose
// first entry is RELATIVE (measured on this machine: `command -v` then prints
// `usr/bin/dirname`, which is why no output shape can be classified).
func writeZshrc(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	rc := `echo "rc chatter on stdout"
demo_fn() { :; }
alias demo_alias='/bin/echo hello'
cd /
PATH="usr/bin:$PATH"
` + extra
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(rc), 0o644))
	return dir
}

func requireZsh(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	return path
}

func TestShellResolve_Integration_Shapes(t *testing.T) {
	zsh := requireZsh(t)
	mod, _, _ := newTestModule(t)
	mod.tmux.(interface {
		SetGlobalOptionValue(option, value string)
	}).SetGlobalOptionValue("default-shell", zsh)
	t.Setenv("ZDOTDIR", writeZshrc(t, ""))
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Only `resolved` is asserted: §4.4 makes no claim about the species of
	// thing found, precisely because the printed shapes are not classifiable.
	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"absolute path", "/bin/ls", true},
		{"relative PATH entry", "dirname", true},
		{"shell function from the rc", "demo_fn", true},
		{"alias from the rc", "demo_alias", true},
		{"builtin", "cd", true},
		{"keyword", "if", true},
		{"nothing of the sort", "pdx-definitely-not-a-command", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := resolveCommand(t, srv, tc.token)
			assert.Equal(t, tc.want, v.Resolved, "detail=%q reason=%q", v.Detail, v.Reason)
			if tc.want {
				// The rc chatter printed before the answer is dropped.
				assert.NotContains(t, v.Detail, "rc chatter")
			} else {
				assert.Equal(t, "not_found", v.Reason)
			}
		})
	}
}

// An rc that leaves a background process holding the output pipe must not hold
// the request open: the shell exited, so its stray descendants are killed with
// the process group and the handler returns immediately.
func TestShellResolve_Integration_LongLivedDescendantDoesNotHang(t *testing.T) {
	zsh := requireZsh(t)
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	mod, _, _ := newTestModule(t)
	mod.tmux.(interface {
		SetGlobalOptionValue(option, value string)
	}).SetGlobalOptionValue("default-shell", zsh)
	// The descendant inherits the probe's stdout, so the pipe stays open after
	// the shell itself has exited.
	t.Setenv("PDX_TEST_PIDFILE", pidFile)
	t.Setenv("ZDOTDIR", writeZshrc(t, "sleep 300 &\nprint $! >! $PDX_TEST_PIDFILE\n"))
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	start := time.Now()
	v := resolveCommand(t, srv, "cd")
	elapsed := time.Since(start)

	assert.True(t, v.Resolved, "reason=%q", v.Reason)
	// Well inside the 5 s deadline: the answer does not wait on the descendant.
	assert.Less(t, elapsed, 4*time.Second, "the request waited on the descendant")

	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err, "the rc did not record the descendant's pid")
	var pid int
	_, err = fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid)
	require.NoError(t, err)
	require.Greater(t, pid, 1)

	// The whole process group went with the probe.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("descendant %d survived the probe", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An rc that prints far more than the output bound must not turn a perfectly
// good answer into a timeout, and must not become the answer either.
//
// Both halves come from the same mistake: stopping the read at the bound. Once
// the reader stops, the read end stays open with nobody draining it, the rc
// fills the pipe, and the shell blocks in write() until the deadline kills it —
// so a command that resolves fine is reported as `timeout`. And what was kept
// is the FIRST bound-worth of output, i.e. the rc's noise, when the contract
// (displayDetail) is "the LAST non-empty line" — the answer is at the end.
//
// ~200 KiB of chatter is well past both the 8 KiB bound and any pipe buffer.
func TestShellResolve_Integration_ChattyRcIsDrainedAndTheAnswerSurvives(t *testing.T) {
	zsh := requireZsh(t)
	mod, _, _ := newTestModule(t)
	mod.tmux.(interface {
		SetGlobalOptionValue(option, value string)
	}).SetGlobalOptionValue("default-shell", zsh)
	noise := "rc chatter on stdout " + strings.Repeat("x", 80)
	t.Setenv("ZDOTDIR", writeZshrc(t, "repeat 2000 print -r -- '"+noise+"'\n"))
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	start := time.Now()
	v := resolveCommand(t, srv, "cd")
	elapsed := time.Since(start)

	// (a) the shell was never wedged, so this is an answer and not a deadline.
	assert.NotEqual(t, "timeout", v.Reason, "a chatty rc wedged the shell: the pipe stopped being drained")
	assert.True(t, v.Resolved, "reason=%q detail=%q", v.Reason, v.Detail)
	assert.Less(t, elapsed, 4*time.Second, "the request waited out the deadline")
	// (b) the answer survived the noise that preceded it.
	assert.Equal(t, "cd", v.Detail)
	assert.NotContains(t, v.Detail, "rc chatter")
}

// TestCappedPipe_DrainsToEOFAndKeepsTheTail is the same contract without a
// shell in the way: the writer must never block on the bound, and what is kept
// is the END of the stream.
func TestCappedPipe_DrainsToEOFAndKeepsTheTail(t *testing.T) {
	const limit = 64
	p, err := newCappedPipe(limit)
	require.NoError(t, err)
	defer p.close()

	payload := strings.Repeat("a", 256<<10) + "TAIL"
	written := make(chan error, 1)
	go func() {
		_, werr := io.WriteString(p.w, payload)
		p.closeWriter()
		written <- werr
	}()

	select {
	case werr := <-written:
		require.NoError(t, werr)
	case <-time.After(5 * time.Second):
		t.Fatal("the writer blocked: the reader stopped at the bound instead of draining to EOF")
	}

	assert.Equal(t, payload[len(payload)-limit:], p.finish(time.Second))
}
