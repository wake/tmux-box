// internal/tmux/send_keys_conditional_test.go — the conditional send.
//
// A generation check that is a SEPARATE tmux invocation from the send has a
// window between the two: the server can restart in it, and the keys then land
// on the new one having passed a check made against the old. `if-shell -F`
// closes that window — the format is expanded and the send performed by the
// same server connection, so a restart either fails the whole invocation or is
// refused by the new server, never half-succeeds.
package tmux_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/tmux"
)

// The exact argv, asserted by a stub tmux: ONE `if-shell -F` carrying the
// condition, the send and the refusal branch. Keys go as `-H` hex bytes, so no
// resume command — quotes, `$`, `#` and all — can be re-read by tmux's own
// command parser on the way through.
func TestSendKeysIfInstance_SingleInvocationCarriesConditionAndSend(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1" != "if-shell" ] || [ "$2" != "-F" ]; then
  printf 'not a single conditional invocation: %s\n' "$*" >&2
  exit 2
fi
if [ "$3" != '#{==:#{pid}:#{start_time},4471:1788740000}' ]; then
  printf 'unexpected condition: %s\n' "$3" >&2
  exit 2
fi
if [ "$4" != "send-keys -t '\$3:' -H 68 69 0a" ]; then
  printf 'unexpected send: %s\n' "$4" >&2
  exit 2
fi
case "$5" in
  display-message*) ;;
  *) printf 'unexpected else branch: %s\n' "$5" >&2; exit 2 ;;
esac
if [ -n "$6" ]; then
  printf 'unexpected extra args: %s\n' "$*" >&2
  exit 2
fi
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sent, err := (&tmux.RealExecutor{}).SendKeysIfInstance("$3", "4471:1788740000", "hi\n")
	if err != nil {
		t.Fatalf("SendKeysIfInstance returned error: %v", err)
	}
	if !sent {
		t.Fatal("SendKeysIfInstance reported the keys were not sent")
	}
}

// The server evaluated the condition and declined. That is an answer, not a
// failure — and it has to be distinguishable from "sent".
func TestSendKeysIfInstance_ServerRefuses_ReportsNotSent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
# What tmux prints when the else branch runs.
printf 'pdx-generation-refused\n'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sent, err := (&tmux.RealExecutor{}).SendKeysIfInstance("$0", "4471:1788740000", "hi\n")
	if err != nil {
		t.Fatalf("a refusal is not an error, got %v", err)
	}
	if sent {
		t.Fatal("a refused send must not report as sent")
	}
}

// A tmux that cannot be reached at all — no server on the socket, which is
// what a restart looks like from the outside — is an error, and nothing was
// sent.
func TestSendKeysIfInstance_InvocationFails_ReportsErrorNotSent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'no server running on /tmp/tmux-501/default\n' >&2
exit 1
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	sent, err := (&tmux.RealExecutor{}).SendKeysIfInstance("$0", "4471:1788740000", "hi\n")
	if sent || err == nil {
		t.Fatalf("a failed invocation must report not-sent and an error, got sent=%v err=%v", sent, err)
	}
}

// The expectation is interpolated into a tmux format string, so anything that
// could change the SHAPE of that format is refused before tmux is invoked at
// all — a doctored value must never become "condition always true".
func TestSendKeysIfInstance_RejectsUnsafeExpectation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'tmux must not be invoked at all: %s\n' "$*" >&2
exit 3
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, bad := range []string{"", "1,1}#{==:1,1", "4471:1788740000}", "#{pid}", "4471 1788740000"} {
		sent, err := (&tmux.RealExecutor{}).SendKeysIfInstance("$0", bad, "hi\n")
		if sent {
			t.Fatalf("expectation %q must not send", bad)
		}
		if !errors.Is(err, tmux.ErrUnsafeInstance) {
			t.Fatalf("expectation %q: want ErrUnsafeInstance, got %v", bad, err)
		}
	}
}

// The target is a session ID, not a name: a rename between the check and the
// send would change what a name resolves to, and an ID cannot be re-pointed.
func TestSendKeysIfInstance_RejectsNonSessionIDTarget(t *testing.T) {
	for _, bad := range []string{"=dev:", "dev", "$", "$0:", ""} {
		sent, err := (&tmux.RealExecutor{}).SendKeysIfInstance(bad, "4471:1788740000", "hi\n")
		if sent || err == nil {
			t.Fatalf("target %q must be refused, got sent=%v err=%v", bad, sent, err)
		}
	}
}

// The fake models the same contract: it holds a generation, and refuses —
// recording nothing — when the caller's expectation does not match it.
func TestFakeExecutor_SendKeysIfInstance(t *testing.T) {
	f := tmux.NewFakeExecutor()
	f.SetInstance("111:1000")

	sent, err := f.SendKeysIfInstance("$0", "222:2000", "nope\n")
	if err != nil || sent {
		t.Fatalf("mismatch must refuse: sent=%v err=%v", sent, err)
	}
	if len(f.RawKeysSent()) != 0 {
		t.Fatalf("a refused send must deliver nothing, got %v", f.RawKeysSent())
	}

	sent, err = f.SendKeysIfInstance("$0", "111:1000", "yes\n")
	if err != nil || !sent {
		t.Fatalf("match must send: sent=%v err=%v", sent, err)
	}
	calls := f.RawKeysSent()
	if len(calls) != 1 || calls[0].Target != "$0:" || calls[0].Keys[0] != "yes\n" {
		t.Fatalf("unexpected delivery: %+v", calls)
	}
}
