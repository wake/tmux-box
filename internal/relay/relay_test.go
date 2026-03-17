// internal/relay/relay_test.go
package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRelayBridgesStdinStdout(t *testing.T) {
	var received []string
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()

		// Send a message to relay (simulating SPA user input)
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"user","message":"hello"}`))

		// Read what relay sends back (subprocess stdout)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			received = append(received, string(msg))
			mu.Unlock()
		}
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]

	// Use "cat" as subprocess — it echoes stdin lines to stdout
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	r := &Relay{
		SessionName: "test",
		DaemonURL:   wsURL,
		Token:       "",
		Command:     []string{"cat"},
	}

	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	// Wait for data to flow: WS → cat stdin → cat stdout → WS
	time.Sleep(500 * time.Millisecond)
	cancel()
	<-errCh

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("expected relay to bridge at least one message, got none")
	}
	if received[0] != `{"type":"user","message":"hello"}` {
		t.Fatalf("unexpected message: %q", received[0])
	}
}

func TestRelayNoCommand(t *testing.T) {
	r := &Relay{Command: nil}
	err := r.Run(context.Background())
	if err == nil || err.Error() != "no command specified" {
		t.Fatalf("expected 'no command specified', got %v", err)
	}
}
