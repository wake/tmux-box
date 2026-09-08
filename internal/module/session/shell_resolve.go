// internal/module/session/shell_resolve.go — POST /api/shell/resolve-command
// (spec §4.4).
//
// A resume template names a program, and the program a user actually runs is
// very often a shell function or an alias that only exists inside their
// interactive login shell (spec §3.6). So the check is "ask that shell", not
// "look on PATH".
//
// The contract is deliberately narrow. 400 means the request body was
// malformed; EVERYTHING else — not found, a timeout, a shell that would not
// start — is a 200 with a verdict, because all of them are answers about the
// command and the UI renders them the same way. There is no `kind` field:
// `resolved` comes from the exit status and `detail` is what the shell
// printed, and the API makes no claim about what species of thing was found.
// Two earlier designs tried to classify and both were wrong — `type` output
// differs between zsh and bash (bash prints the whole function body), and
// `command -v` output is not "a path or a word" (an alias prints its
// definition, a relative PATH entry prints a relative path).
package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// shellProbeTimeout bounds the WHOLE pipeline — choosing a shell as well as
// running it. The probe is an approximation, not a guarantee; it must never
// become a way to run something long or large.
//
// A var only so tests can shorten it; production never changes it.
var shellProbeTimeout = 5 * time.Second

const (
	// Bounded BEFORE the display truncation: an rc that prints a megabyte
	// must not be buffered in full just to have 512 bytes taken off it.
	shellProbeMaxOutput = 8 << 10
	shellProbeMaxDetail = 512
	// The token is what we agree to hand a shell. The template itself is not
	// restricted — only this.
	shellProbeMaxToken = 256
	// The body bound has to sit in FRONT of the decoder: shellProbeMaxToken is
	// checked on the decoded value, and by then the decoder has already
	// buffered the whole string. Sized well clear of the largest legitimate
	// body — a 256-byte token whose every byte needed a \uXXXX escape is
	// ~1.5 KiB — so nothing real is ever refused by it.
	shellResolveMaxBody = 16 << 10
	// After the shell exits, anything still holding the output pipe is a
	// stray descendant. This is how long we wait for the reader to notice the
	// pipe closed after the process group is killed.
	shellProbeReadGrace = time.Second
)

// shellMetacharacters is the §4.4 rejection set. The token never enters the
// script text — it is a positional parameter — so this is defence in depth
// rather than the only thing between us and injection.
const shellMetacharacters = "|&;<>()$`\\\"'\n"

// shellProbeOutcome is what one invocation of the shell reports back. It
// carries no verdict: mapping an exit status onto the wire shape is the
// handler's job, so the exec seam can be replaced without replacing the
// contract.
type shellProbeOutcome struct {
	Stdout   string
	ExitCode int
	// TimedOut is set when the deadline fired, whatever the process then did.
	TimedOut bool
	// Err is set when the shell could not be run at all.
	Err error
}

type shellProbeFunc func(ctx context.Context, shell, token string) shellProbeOutcome

type shellResolveRequest struct {
	// A pointer so a missing key and an explicit null are both "absent",
	// while a non-string value fails the decode.
	Command *string `json:"command"`
}

type shellResolveResponse struct {
	Resolved bool   `json:"resolved"`
	Detail   string `json:"detail,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func (m *SessionModule) handleShellResolveCommand(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, shellResolveMaxBody)
	var req shellResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == nil {
		http.Error(w, "command must be a string", http.StatusBadRequest)
		return
	}
	writeShellResolveVerdict(w, m.resolveCommandWord(r.Context(), *req.Command))
}

func writeShellResolveVerdict(w http.ResponseWriter, resp shellResolveResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// resolveCommandWord runs the whole verdict pipeline: reject, choose a shell,
// probe, read the exit status.
func (m *SessionModule) resolveCommandWord(ctx context.Context, token string) shellResolveResponse {
	if reason := rejectShellToken(token); reason != "" {
		return shellResolveResponse{Resolved: false, Reason: reason}
	}

	probeCtx, cancel := context.WithTimeout(ctx, shellProbeTimeout)
	defer cancel()

	// Choosing the shell is itself two possible subprocesses — `tmux
	// show-options` and, on darwin, `dscl` — so it runs under the same
	// deadline as the probe. A tmux server that has stopped answering would
	// otherwise hold the handler open indefinitely and let concurrent requests
	// pile up behind a step nothing was watching.
	shell := m.probeShell(probeCtx)
	// And the deadline is honoured between the two halves: a request with no
	// time left must not start a shell it cannot wait for. An expired context
	// would make exec fail with a context error, which reads as `shell_failed`
	// — a claim about the user's shell that is not true.
	if probeCtx.Err() != nil {
		return shellResolveResponse{Resolved: false, Reason: "timeout"}
	}

	out := m.shellProbe(probeCtx, shell, token)
	switch {
	case out.TimedOut:
		return shellResolveResponse{Resolved: false, Reason: "timeout"}
	case out.Err != nil:
		return shellResolveResponse{Resolved: false, Reason: "shell_failed"}
	case out.ExitCode != 0:
		return shellResolveResponse{Resolved: false, Reason: "not_found"}
	}
	return shellResolveResponse{Resolved: true, Detail: displayDetail(out.Stdout)}
}

// rejectShellToken returns the §4.4 reason to refuse before starting anything,
// or "" to go ahead.
func rejectShellToken(token string) string {
	if len(token) > shellProbeMaxToken {
		return "too_long"
	}
	if strings.HasPrefix(token, "-") || strings.ContainsAny(token, shellMetacharacters) {
		return "shell_metacharacters"
	}
	return ""
}

// displayDetail takes the LAST non-empty line: an interactive login shell
// prints whatever the user's rc prints before it gets to our command, so the
// answer is at the end, not the beginning.
func displayDetail(stdout string) string {
	detail := ""
	for _, line := range strings.Split(stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			detail = trimmed
		}
	}
	if len(detail) > shellProbeMaxDetail {
		detail = detail[:shellProbeMaxDetail]
	}
	return detail
}

// probeShell picks the shell tmux would actually start, and falls back only
// when it cannot be asked. An empty answer counts as no answer — tmux prints
// nothing for an option it does not know, and "" is not a program.
func (m *SessionModule) probeShell(ctx context.Context) string {
	if shell, err := m.tmux.ShowGlobalOption(ctx, "default-shell"); err == nil {
		if shell = strings.TrimSpace(shell); shell != "" {
			return shell
		}
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	if m.passwdShell != nil {
		if shell := strings.TrimSpace(m.passwdShell(ctx)); shell != "" {
			return shell
		}
	}
	return "/bin/sh"
}

// runShellProbe starts the shell as an INTERACTIVE LOGIN shell (`-l -i -c`),
// because that is how tmux starts a pane's shell with an empty
// `default-command` — and it is the only mode in which the user's functions
// and aliases exist at all (spec §3.6).
//
// The token is argv, never script text. `builtin` defeats an rc that redefined
// `command`; shells that have no `builtin` get the plain form.
func runShellProbe(ctx context.Context, shell, token string) shellProbeOutcome {
	script := `command -v "$1"`
	switch filepath.Base(shell) {
	case "zsh", "bash":
		script = `builtin command -v "$1"`
	}

	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", script, "_", token)

	// An rc file can start anything. Setpgid puts the shell and everything it
	// spawns in one group so cancellation can take the whole group, which
	// exec.CommandContext's default (kill the direct child) would not.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// A descendant holding a pipe must not make Wait hang after the kill.
	cmd.WaitDelay = time.Second

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return shellProbeOutcome{Err: err}
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	// Our own pipes rather than exec's byte buffers: `cmd.Stdout = io.Writer`
	// makes Wait block on a copier goroutine that a stray descendant can hold
	// open, and there would be no way to bound what is kept.
	//
	// Both pipes are drained to EOF; only stdout keeps anything, and only its
	// last 8 KiB, which is where displayDetail's answer lives.
	stdout, err := newCappedPipe(shellProbeMaxOutput)
	if err != nil {
		return shellProbeOutcome{Err: err}
	}
	defer stdout.close()
	// stderr is drained and discarded (a limit of 0). Drained so a chatty rc
	// cannot fill the pipe and wedge the shell; discarded so an interactive
	// shell's prompt noise cannot become the answer.
	stderr, err := newCappedPipe(0)
	if err != nil {
		return shellProbeOutcome{Err: err}
	}
	defer stderr.close()
	cmd.Stdout, cmd.Stderr = stdout.w, stderr.w

	if err := cmd.Start(); err != nil {
		return shellProbeOutcome{Err: err}
	}
	pgid := cmd.Process.Pid
	// The parent's copies of the write ends, so EOF depends only on the
	// children.
	stdout.closeWriter()
	stderr.closeWriter()

	waitErr := cmd.Wait()

	// The shell has exited, so anything left in its group is a stray holding
	// our pipe open. Kill the group unconditionally: waiting for it instead
	// would stall the request for the whole deadline over output nobody wants.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	out := shellProbeOutcome{Stdout: stdout.finish(shellProbeReadGrace)}
	stderr.finish(shellProbeReadGrace)

	if ctx.Err() != nil {
		out.TimedOut = true
		return out
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			out.ExitCode = exitErr.ExitCode()
			// A signalled process reports -1; treat that as "did not resolve"
			// rather than as success.
			if out.ExitCode <= 0 {
				out.ExitCode = 1
			}
			return out
		}
		out.Err = waitErr
	}
	return out
}

// cappedPipe reads one fd on its own goroutine, all the way to EOF, keeping at
// most `limit` bytes — the LAST `limit` bytes.
//
// Draining to the end matters as much as the bound. A reader that stops once it
// has its bound leaves the read end open with nobody emptying it, so an rc that
// keeps printing fills the pipe and the shell blocks in write() until the
// deadline kills it — reporting `timeout` for a command that resolved perfectly
// well.
//
// The TAIL rather than the head, because the contract is displayDetail's "last
// non-empty line": an interactive login shell prints whatever the user's rc
// prints before it ever reaches our command, so keeping the FIRST `limit` bytes
// keeps the noise and throws away the answer.
//
// A limit of 0 means drained and discarded, which is all stderr needs.
type cappedPipe struct {
	r *os.File
	w *os.File
	// tail is written by the reader goroutine and read only after done is
	// closed.
	tail []byte
	done chan struct{}
}

func newCappedPipe(limit int) (*cappedPipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	p := &cappedPipe{r: r, w: w, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		p.tail = drainTail(p.r, limit)
	}()
	return p, nil
}

// drainTail reads r until it stops yielding, keeping at most `limit` bytes of
// what it saw last. Any read error ends the drain — EOF, a closed descriptor,
// or the read deadline finish() sets — and what has been kept by then is the
// answer.
func drainTail(r io.Reader, limit int) []byte {
	if limit < 0 {
		limit = 0
	}
	chunk := make([]byte, 32<<10)
	tail := make([]byte, 0, limit)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			tail = appendTail(tail, chunk[:n], limit)
		}
		if err != nil {
			return tail
		}
	}
}

// appendTail keeps the last `limit` bytes of tail+chunk, reusing tail's
// storage so a megabyte of rc output costs one buffer rather than a megabyte.
func appendTail(tail, chunk []byte, limit int) []byte {
	if len(chunk) >= limit {
		return append(tail[:0], chunk[len(chunk)-limit:]...)
	}
	if overflow := len(tail) + len(chunk) - limit; overflow > 0 {
		// Overlapping, and safe: append copies, and copy is a memmove.
		tail = append(tail[:0], tail[overflow:]...)
	}
	return append(tail, chunk...)
}

func (p *cappedPipe) closeWriter() { _ = p.w.Close() }

// finish waits for the reader to see EOF, then gives up. The read deadline —
// rather than closing the fd out from under a blocked Read — is what makes
// giving up safe: closing would race the reader onto a recycled descriptor.
func (p *cappedPipe) finish(grace time.Duration) string {
	select {
	case <-p.done:
	case <-time.After(grace):
		_ = p.r.SetReadDeadline(time.Now())
		<-p.done
	}
	return string(p.tail)
}

func (p *cappedPipe) close() {
	_ = p.w.Close()
	_ = p.r.Close()
}

// passwdShellForCurrentUser is the third rung of the ladder — reached only
// when tmux cannot be asked AND $SHELL is unset, which is what a daemon
// started by launchd looks like.
func passwdShellForCurrentUser(ctx context.Context) string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "darwin" {
		// Normal macOS accounts are in directory services, not /etc/passwd.
		// Under the caller's deadline like every other subprocess on this
		// path: directory services can be slow or wedged, and this rung is
		// reached exactly when the machine is already in an odd state.
		out, err := exec.CommandContext(ctx, "dscl", ".", "-read", "/Users/"+u.Username, "UserShell").Output()
		if err == nil {
			if shell := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "UserShell:")); shell != "" {
				return shell
			}
		}
	}
	raw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	return passwdShellFromEntries(string(raw), u.Username, u.Uid)
}

// passwdShellFromEntries picks field 7 of the line matching this user, by name
// or by uid.
func passwdShellFromEntries(passwd, username, uid string) string {
	for _, line := range strings.Split(passwd, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		if fields[0] != username && fields[2] != uid {
			continue
		}
		if shell := strings.TrimSpace(fields[6]); shell != "" {
			return shell
		}
	}
	return ""
}
