// internal/server/e2e_test.go
package server_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/relay"
)

// TestE2EPipelineSPAThroughRelay verifies the full message pipeline:
// SPA WS → daemon bridge → relay WS → subprocess stdin → stdout → relay WS → bridge → SPA WS
//
// This is the critical integration test that the existing tests miss:
// - relay_test.go tests relay↔subprocess with a direct WS (no bridge)
// - bridge_handler_test.go tests bridge routing (no subprocess)
// - This test connects them end-to-end.
func TestE2EPipelineSPAThroughRelay(t *testing.T) {
	srv := setupServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start relay with "cat" as subprocess (echoes stdin to stdout line by line)
	r := &relay.Relay{
		SessionName: "e2e-test",
		DaemonURL:   wsURL(srv, "/ws/cli-bridge/e2e-test"),
		Command:     []string{"cat"},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	// Wait for relay to register in bridge
	time.Sleep(200 * time.Millisecond)

	// Connect subscriber (mock SPA)
	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/e2e-test"))
	defer sub.Close()

	// SPA sends user message (same format as real SPA StreamInput)
	msg := `{"type":"user","message":{"role":"user","content":"ping"}}`
	if err := sub.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("subscriber write: %v", err)
	}

	// Expect cat to echo it back through the full pipeline
	sub.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, got, err := sub.ReadMessage()
	if err != nil {
		t.Fatalf("subscriber read: %v — message did not flow through the pipeline", err)
	}
	if string(got) != msg {
		t.Fatalf("got %q, want %q", got, msg)
	}

	cancel()
	<-errCh
}

// TestE2EMultipleMessages verifies multiple sequential messages flow through.
func TestE2EMultipleMessages(t *testing.T) {
	srv := setupServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := &relay.Relay{
		SessionName: "e2e-multi",
		DaemonURL:   wsURL(srv, "/ws/cli-bridge/e2e-multi"),
		Command:     []string{"cat"},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)

	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/e2e-multi"))
	defer sub.Close()

	messages := []string{
		`{"type":"user","message":{"role":"user","content":"first"}}`,
		`{"type":"user","message":{"role":"user","content":"second"}}`,
		`{"type":"user","message":{"role":"user","content":"third"}}`,
	}

	for _, msg := range messages {
		if err := sub.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	for i, want := range messages {
		sub.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, got, err := sub.ReadMessage()
		if err != nil {
			t.Fatalf("message %d: read: %v", i, err)
		}
		if string(got) != want {
			t.Fatalf("message %d: got %q, want %q", i, got, want)
		}
	}

	cancel()
	<-errCh
}

// TestRelayStdinReceivesExactBytes verifies the exact byte format written to subprocess stdin.
// Uses tee to capture stdin to a file while echoing to stdout.
func TestRelayStdinReceivesExactBytes(t *testing.T) {
	srv := setupServer(t)

	outFile := filepath.Join(t.TempDir(), "stdin-capture.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// tee captures stdin to file AND echoes to stdout
	r := &relay.Relay{
		SessionName: "stdin-capture",
		DaemonURL:   wsURL(srv, "/ws/cli-bridge/stdin-capture"),
		Command:     []string{"tee", outFile},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)

	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/stdin-capture"))
	defer sub.Close()

	msg := `{"type":"user","message":{"role":"user","content":"test-input"}}`
	if err := sub.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Wait for echo back to confirm message flowed through
	sub.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, got, err := sub.ReadMessage()
	if err != nil {
		t.Fatalf("subscriber read: %v", err)
	}
	if string(got) != msg {
		t.Fatalf("echo mismatch: got %q", got)
	}

	// Shutdown relay so tee flushes and closes
	cancel()
	<-errCh

	// Verify captured stdin file contains exactly: message + newline
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}

	want := msg + "\n"
	if string(data) != want {
		t.Fatalf("stdin captured:\n  got:  %q\n  want: %q", data, want)
	}
}

// TestE2ESubprocessOutputWithoutInput documents a known timing issue:
// subprocess init output is lost because the SPA subscriber connects after the message
// was already sent through the bridge with no subscribers. This will be fixed by
// the stream WS lifecycle redesign (see docs/superpowers/specs/2026-03-18-stream-ws-lifecycle-design.md).
func TestE2ESubprocessOutputWithoutInput(t *testing.T) {
	t.Skip("Known issue: init message lost before subscriber connects — fix planned in stream WS lifecycle redesign")
	srv := setupServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subprocess that outputs a line immediately without waiting for stdin
	initMsg := `{"type":"system","subtype":"init","session_id":"abc123"}`
	r := &relay.Relay{
		SessionName: "init-output",
		DaemonURL:   wsURL(srv, "/ws/cli-bridge/init-output"),
		Command:     []string{"sh", "-c", fmt.Sprintf("echo '%s'; cat", initMsg)},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)

	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/init-output"))
	defer sub.Close()

	// Should receive the init message that subprocess printed on startup
	sub.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, got, err := sub.ReadMessage()
	if err != nil {
		t.Fatalf("subscriber read: %v — init output did not reach SPA", err)
	}
	if string(got) != initMsg {
		t.Fatalf("got %q, want %q", got, initMsg)
	}

	cancel()
	<-errCh
}
