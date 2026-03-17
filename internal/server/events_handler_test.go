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
		defer conn.Close()

		eb.Add(conn)
		defer eb.Remove(conn)

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
		defer conn.Close()

		eb.Add(conn)
		defer eb.Remove(conn)

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
		defer conn.Close()

		eb.Add(conn)
		defer eb.Remove(conn)

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
	s := server.New(cfg, db, tmux.NewFakeExecutor())
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

func TestSessionEventsDisconnectedSubscriber(t *testing.T) {
	eb := server.NewEventsBroadcaster()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		eb.Add(conn)
		defer eb.Remove(conn)

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
