// internal/server/bridge_handler_test.go
package server_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

// setupServer creates a test HTTP server with real bridge wiring.
func setupServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Config{} // no token/IP restriction for tests
	s := server.New(cfg, db, tmux.NewFakeExecutor(), "")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	if resp.Body != nil {
		resp.Body.Close()
	}
	return conn
}

func TestBridgeRelayAndSubscriber(t *testing.T) {
	srv := setupServer(t)

	// Connect relay (producer)
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/test-session"))
	defer relay.Close()

	// Give server a moment to register the relay
	time.Sleep(50 * time.Millisecond)

	// Connect subscriber (consumer)
	sub := dial(t, wsURL(srv, "/ws/cli-bridge-sub/test-session"))
	defer sub.Close()

	// Relay sends a message → subscriber receives
	relay.WriteMessage(websocket.TextMessage, []byte(`{"type":"assistant","content":"hello"}`))

	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := sub.ReadMessage()
	if err != nil {
		t.Fatalf("subscriber read: %v", err)
	}
	if string(msg) != `{"type":"assistant","content":"hello"}` {
		t.Fatalf("subscriber got %q", msg)
	}

	// Subscriber sends a message → relay receives
	sub.WriteMessage(websocket.TextMessage, []byte(`{"type":"user","content":"hi"}`))

	relay.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err = relay.ReadMessage()
	if err != nil {
		t.Fatalf("relay read: %v", err)
	}
	if string(msg) != `{"type":"user","content":"hi"}` {
		t.Fatalf("relay got %q", msg)
	}
}

func TestBridgeFanOutMultipleSubscribers(t *testing.T) {
	srv := setupServer(t)

	relay := dial(t, wsURL(srv, "/ws/cli-bridge/fanout"))
	defer relay.Close()

	time.Sleep(50 * time.Millisecond)

	sub1 := dial(t, wsURL(srv, "/ws/cli-bridge-sub/fanout"))
	defer sub1.Close()
	sub2 := dial(t, wsURL(srv, "/ws/cli-bridge-sub/fanout"))
	defer sub2.Close()

	relay.WriteMessage(websocket.TextMessage, []byte(`{"data":"broadcast"}`))

	for i, sub := range []*websocket.Conn{sub1, sub2} {
		sub.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := sub.ReadMessage()
		if err != nil {
			t.Fatalf("sub%d read: %v", i+1, err)
		}
		if string(msg) != `{"data":"broadcast"}` {
			t.Fatalf("sub%d got %q", i+1, msg)
		}
	}
}

func TestBridgeSubscribeNoRelay(t *testing.T) {
	srv := setupServer(t)

	// Try to subscribe without relay → 404
	resp, err := http.Get(srv.URL + "/ws/cli-bridge-sub/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestBridgeDuplicateRelay(t *testing.T) {
	srv := setupServer(t)

	relay := dial(t, wsURL(srv, "/ws/cli-bridge/dup"))
	defer relay.Close()

	time.Sleep(50 * time.Millisecond)

	// Second relay → 409 Conflict
	resp, err := http.Get(srv.URL + "/ws/cli-bridge/dup")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
}
