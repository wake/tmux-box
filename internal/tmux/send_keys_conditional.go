// internal/tmux/send_keys_conditional.go — send keys only if the tmux server
// is still the one the caller means (spec §4.6.2).
//
// The problem this solves is not "check more carefully"; it is that a check and
// a send performed by two different tmux invocations are two different server
// connections. Between them the server can restart, and since a session code is
// a reversible encoding of `$N`, the new server mints `$0` again — so the keys
// pass a check made against the old server and land in a stranger's pane. No
// amount of re-sampling removes the window; it only narrows it.
//
// `tmux if-shell -F <format> <command> <else>` removes it. The format is
// expanded and the command run by the server that received the invocation, so
// the server that evaluates the condition IS the server that receives the keys.
// A restart before the connection is made produces "no server running" (an
// error, nothing sent); a restart after it means the new server evaluates the
// condition itself and declines.
package tmux

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ErrUnsafeInstance reports a generation string that cannot be embedded in a
// tmux format. The expectation is interpolated into `#{==:…,…}`, so a value
// carrying `,`, `}` or `#` could change the shape of the condition rather than
// be compared by it — "always true" being the interesting one. Refused before
// tmux is invoked at all.
var ErrUnsafeInstance = errors.New("tmux generation is not safely comparable")

// A generation is `<pid>:<start_time>` as `GetTmuxInstance` reads it
// (`internal/config/hostid.go`). The pattern is deliberately a little wider
// than that — a tmux that rendered `start_time` differently should still work —
// but admits nothing that carries meaning inside a format string.
var instancePattern = regexp.MustCompile(`^[0-9A-Za-z:._-]+$`)

// ValidInstance reports whether a generation string can be compared inside a
// tmux format. Exported so a caller can refuse a malformed expectation as a bad
// request — which is what it is — instead of discovering it as an executor
// error one layer down.
func ValidInstance(s string) bool {
	return instancePattern.MatchString(s)
}

// Session ids are `$` followed by digits. Nothing else is accepted: the whole
// point of targeting by id is that, unlike a name, it cannot be re-pointed
// between the caller's decision and the send.
var sessionIDPattern = regexp.MustCompile(`^\$[0-9]+$`)

// generationRefusedSentinel is what the else branch prints. It travels back on
// stdout of the same invocation, which is the only channel that can report the
// verdict of a condition evaluated inside the server: `if-shell` exits 0
// whichever branch it took.
const generationRefusedSentinel = "pdx-generation-refused"

// conditionalSendArgs builds the single tmux invocation.
//
// Keys are sent as `-H` hex bytes rather than as a literal string for two
// reasons. The send is nested inside a tmux COMMAND STRING, which tmux parses
// again — quotes, `$` and `#` in a resume command would all be re-read there —
// and hex digits are inert under every quoting rule. It also stops tmux's
// key-name lookup from turning a payload that happens to spell a key name into
// that key.
func conditionalSendArgs(sessionID, expectedInstance string, keys ...string) ([]string, error) {
	if !sessionIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("tmux send-keys: %q is not a session id", sessionID)
	}
	if !instancePattern.MatchString(expectedInstance) {
		return nil, fmt.Errorf("%w: %q", ErrUnsafeInstance, expectedInstance)
	}

	var send strings.Builder
	// Single quotes: tmux expands `$name` inside double quotes, and a session
	// id starts with `$`. Session ids contain no quote to escape.
	fmt.Fprintf(&send, "send-keys -t '%s:' -H", sessionID)
	for _, k := range keys {
		for i := 0; i < len(k); i++ {
			fmt.Fprintf(&send, " %02x", k[i])
		}
	}

	condition := fmt.Sprintf("#{==:#{pid}:#{start_time},%s}", expectedInstance)
	return []string{
		"if-shell", "-F", condition,
		send.String(),
		"display-message -p " + generationRefusedSentinel,
	}, nil
}

// SendKeysIfInstance sends keys to the session's active pane only if the tmux
// server's generation equals expectedInstance, with the comparison and the send
// performed by one server connection.
//
// Returns (true, nil) when the keys went out, (false, nil) when the server
// evaluated the condition and declined, and (false, err) when the invocation
// could not be completed — the caller must treat the last as "unknown, nothing
// sent" rather than as a refusal.
func (r *RealExecutor) SendKeysIfInstance(sessionID, expectedInstance string, keys ...string) (bool, error) {
	args, err := conditionalSendArgs(sessionID, expectedInstance, keys...)
	if err != nil {
		return false, err
	}
	cmd := exec.Command("tmux", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("tmux if-shell send-keys: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if strings.Contains(string(out), generationRefusedSentinel) {
		return false, nil
	}
	return true, nil
}
