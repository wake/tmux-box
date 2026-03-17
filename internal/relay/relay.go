// internal/relay/relay.go
package relay

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// Relay bridges a subprocess (typically claude -p stream-json) to the daemon
// via WebSocket. Subprocess stdout is read line-by-line to preserve NDJSON
// boundaries and tee'd to stderr for terminal visibility.
type Relay struct {
	SessionName string
	DaemonURL   string
	Token       string
	TokenFile   string // If set and Token is empty, read token from this file then delete it.
	Command     []string
}

// Run connects to the daemon WebSocket, starts the subprocess, and bridges
// data between them until the context is cancelled or the subprocess exits.
func (r *Relay) Run(ctx context.Context) error {
	if len(r.Command) == 0 {
		return fmt.Errorf("no command specified")
	}

	// Resolve token: prefer Token field, fallback to TokenFile.
	token := r.Token
	if token == "" && r.TokenFile != "" {
		data, err := os.ReadFile(r.TokenFile)
		if err != nil {
			return fmt.Errorf("read token file: %w", err)
		}
		token = string(data)
		// Remove token file after reading for security.
		os.Remove(r.TokenFile)
	}

	// Connect to daemon WebSocket
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, r.DaemonURL, header)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	// Start subprocess — use plain exec.Command (NOT CommandContext) to avoid
	// SIGKILL on context cancel. We handle graceful shutdown manually below.
	cmd := exec.Command(r.Command[0], r.Command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	// Graceful shutdown: on context cancel, send SIGTERM then SIGKILL after 5s.
	go func() {
		<-ctx.Done()
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			time.AfterFunc(5*time.Second, func() {
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
			})
		}
	}()

	var wg sync.WaitGroup

	// Subprocess stdout → line-buffered tee to stderr + send to daemon WS
	// IMPORTANT: use bufio.Scanner for line-based reading to preserve NDJSON boundaries
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "subprocess exited"))
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line
		for scanner.Scan() {
			// I2 fix: scanner.Text() returns a string copy, avoiding buffer aliasing.
			line := scanner.Text()
			os.Stderr.WriteString(line)
			os.Stderr.WriteString("\n")
			// I1 fix: break on write error.
			if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
				return
			}
		}
	}()

	// Daemon WS → subprocess stdin
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stdin.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				// WS disconnected — send SIGTERM to subprocess
				if cmd.Process != nil {
					cmd.Process.Signal(syscall.SIGTERM)
				}
				return
			}
			// Check for shutdown signal
			if string(msg) == `{"type":"shutdown"}` {
				if cmd.Process != nil {
					cmd.Process.Signal(os.Interrupt)
				}
				return
			}
			// I1 fix: break on write error.
			if _, err := stdin.Write(msg); err != nil {
				return
			}
			if _, err := stdin.Write([]byte("\n")); err != nil {
				return
			}
		}
	}()

	cmdErr := cmd.Wait()
	wg.Wait()

	if cmdErr != nil {
		fmt.Fprintf(os.Stderr, "tbox relay: subprocess exited: %v\n", cmdErr)
	}
	return cmdErr
}
