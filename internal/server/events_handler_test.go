// internal/server/events_handler_test.go
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

func TestEventsBroadcasterAddRemove(t *testing.T) {
	eb := server.NewEventsBroadcaster()

	if eb.HasSubscribers() {
		t.Fatal("new broadcaster should have no subscribers")
	}

	// Use a real WS server to get proper connections
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}

		sub := eb.Add(conn)
		defer eb.Remove(sub)

		// Keep alive
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	time.Sleep(50 * time.Millisecond)
	if !eb.HasSubscribers() {
		t.Fatal("should have 1 subscriber")
	}

	c.Close()
	time.Sleep(50 * time.Millisecond)
	// Note: Remove is called in defer when the read loop exits
}

func TestEventsBroadcasterBroadcast(t *testing.T) {
	eb := server.NewEventsBroadcaster()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}

		sub := eb.Add(conn)
		defer eb.Remove(sub)

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	time.Sleep(50 * time.Millisecond)

	eb.Broadcast("myproject", "status", "cc-running")

	c.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	var evt map[string]string
	if err := json.Unmarshal(msg, &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt["type"] != "status" {
		t.Errorf("want type=status, got %q", evt["type"])
	}
	if evt["session"] != "myproject" {
		t.Errorf("want session=myproject, got %q", evt["session"])
	}
	if evt["value"] != "cc-running" {
		t.Errorf("want value=cc-running, got %q", evt["value"])
	}
}

func TestEventsBroadcasterFanOut(t *testing.T) {
	eb := server.NewEventsBroadcaster()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}

		sub := eb.Add(conn)
		defer eb.Remove(sub)

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	c1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()

	c2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	time.Sleep(50 * time.Millisecond)

	eb.Broadcast("sess1", "handoff", "connected")

	for i, c := range []*websocket.Conn{c1, c2} {
		c.SetReadDeadline(time.Now().Add(time.Second))
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("client%d read: %v", i+1, err)
		}
		if !strings.Contains(string(msg), "connected") {
			t.Fatalf("client%d unexpected: %s", i+1, msg)
		}
	}
}

func TestSessionEventsEndpoint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.Config{
		Detect: config.DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
	}
	s := server.New(cfg, db, nil, tmux.NewFakeExecutor(), "")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	// Connect to session-events WS
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/session-events"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	time.Sleep(50 * time.Millisecond)

	// Broadcast via the server's events broadcaster
	s.BroadcastEvent("test-session", "status", "cc-idle")

	c.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var evt map[string]string
	if err := json.Unmarshal(msg, &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt["type"] != "status" {
		t.Errorf("want type=status, got %q", evt["type"])
	}
	if evt["session"] != "test-session" {
		t.Errorf("want session=test-session, got %q", evt["session"])
	}
	if evt["value"] != "cc-idle" {
		t.Errorf("want value=cc-idle, got %q", evt["value"])
	}
}

func TestRelayEventsSnapshot(t *testing.T) {
	srv := setupServer(t)

	// Connect a relay first (creates relay state)
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/snap-test"))
	defer relay.Close()
	time.Sleep(50 * time.Millisecond)

	// Connect session-events subscriber — should receive snapshot with relay status
	sub := dial(t, wsURL(srv, "/ws/session-events"))
	defer sub.Close()

	// Read messages until we find relay event or timeout
	var relayEvent map[string]string
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := sub.ReadMessage()
		if err != nil {
			break
		}
		var ev map[string]string
		json.Unmarshal(msg, &ev)
		if ev["type"] == "relay" && ev["session"] == "snap-test" {
			relayEvent = ev
			break
		}
	}

	if relayEvent == nil {
		t.Fatal("expected relay snapshot event for snap-test")
	}
	if relayEvent["value"] != "connected" {
		t.Fatalf("want connected, got %q", relayEvent["value"])
	}
}

func TestRelayEventsLive(t *testing.T) {
	srv := setupServer(t)

	// Connect session-events subscriber
	sub := dial(t, wsURL(srv, "/ws/session-events"))
	defer sub.Close()
	time.Sleep(50 * time.Millisecond)

	// Connect relay — triggers relay:connected broadcast
	relay := dial(t, wsURL(srv, "/ws/cli-bridge/live-test"))
	time.Sleep(100 * time.Millisecond)

	// Read messages, skip non-relay events (status snapshots etc), find relay:connected
	var foundConnected bool
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := sub.ReadMessage()
		if err != nil {
			break
		}
		var ev map[string]string
		json.Unmarshal(msg, &ev)
		if ev["type"] == "relay" && ev["session"] == "live-test" && ev["value"] == "connected" {
			foundConnected = true
			break
		}
	}
	if !foundConnected {
		t.Fatal("expected relay:connected event for live-test")
	}

	// Disconnect relay — triggers relay:disconnected broadcast
	relay.Close()
	time.Sleep(100 * time.Millisecond)

	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	var foundDisconnected bool
	for {
		_, msg, err := sub.ReadMessage()
		if err != nil {
			break
		}
		var ev map[string]string
		json.Unmarshal(msg, &ev)
		if ev["type"] == "relay" && ev["session"] == "live-test" && ev["value"] == "disconnected" {
			foundDisconnected = true
			break
		}
	}
	if !foundDisconnected {
		t.Fatal("expected relay:disconnected event for live-test")
	}
}

func TestSessionEventsDisconnectedSubscriber(t *testing.T) {
	eb := server.NewEventsBroadcaster()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}

		sub := eb.Add(conn)
		defer eb.Remove(sub)

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	if !eb.HasSubscribers() {
		t.Fatal("should have subscriber")
	}

	// Close the client — the server-side Remove should fire from the deferred call
	c.Close()
	time.Sleep(100 * time.Millisecond)

	// Broadcast should not panic even if connection is gone
	eb.Broadcast("sess", "status", "normal")
}
