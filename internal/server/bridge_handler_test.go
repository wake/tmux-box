// internal/server/bridge_handler_test.go
package server_test

import (
	"encoding/json"
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
	_, srv := setupServerWithDB(t)
	return srv
}

// setupServerWithDB creates a test server and returns the DB for direct assertions.
func setupServerWithDB(t *testing.T) (*store.Store, *httptest.Server) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Config{}
	tx := tmux.NewFakeExecutor()
	s := server.New(cfg, db, nil, tx, "")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return db, srv
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

func TestBridgeInitMetadataSavesToDB(t *testing.T) {
	db, srv := setupServerWithDB(t)

	// Create session in DB
	sessID, err := db.CreateSession(store.Session{Name: "model-test", Cwd: "/tmp", Mode: "stream"})
	if err != nil {
		t.Fatal(err)
	}

	// Connect relay
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/model-test"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// Relay sends init message (simulating CC output)
	relay.WriteMessage(websocket.TextMessage, []byte(
		`{"type":"system","subtype":"init","model":"claude-opus-4-6","session_id":"xyz"}`,
	))
	time.Sleep(200 * time.Millisecond)

	// Verify DB has the model
	got, err := db.GetSession(sessID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CCModel != "claude-opus-4-6" {
		t.Fatalf("want claude-opus-4-6, got %q", got.CCModel)
	}
}

func TestSessionListIncludesHasRelay(t *testing.T) {
	db, srv := setupServerWithDB(t)

	// Create session
	db.CreateSession(store.Session{Name: "relay-dto", Cwd: "/tmp", Mode: "term"})

	// Before relay: has_relay should be false
	resp, _ := http.Get(srv.URL + "/api/sessions")
	var sessions []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&sessions)
	resp.Body.Close()

	var found map[string]interface{}
	for _, s := range sessions {
		if s["name"] == "relay-dto" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("session not found")
	}
	if found["has_relay"] != false {
		t.Fatalf("want has_relay=false before relay, got %v", found["has_relay"])
	}

	// Connect relay
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/relay-dto"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// After relay: has_relay should be true
	resp, _ = http.Get(srv.URL + "/api/sessions")
	json.NewDecoder(resp.Body).Decode(&sessions)
	resp.Body.Close()

	found = nil
	for _, s := range sessions {
		if s["name"] == "relay-dto" {
			found = s
			break
		}
	}
	if found["has_relay"] != true {
		t.Fatalf("want has_relay=true after relay, got %v", found["has_relay"])
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
