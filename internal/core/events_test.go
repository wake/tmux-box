package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dialWS is a test helper that connects to a WS endpoint via httptest.Server.
func dialWS(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/session-events"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestBroadcastSendsToAllSubscribers(t *testing.T) {
	eb := NewEventsBroadcaster()
	server := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer server.Close()

	const numClients = 3
	conns := make([]*websocket.Conn, numClients)
	for i := range conns {
		conns[i] = dialWS(t, server)
	}

	// Allow subscribers to register
	time.Sleep(50 * time.Millisecond)

	eb.Broadcast("my-session", "status", "running")

	for i, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := conn.ReadMessage()
		require.NoError(t, err, "client %d should receive message", i)

		var evt SessionEvent
		require.NoError(t, json.Unmarshal(msg, &evt))
		assert.Equal(t, "status", evt.Type)
		assert.Equal(t, "my-session", evt.Session)
		assert.Equal(t, "running", evt.Value)
	}
}

func TestSlowSubscriberBroadcastNeverBlocks(t *testing.T) {
	eb := NewEventsBroadcaster()
	server := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer server.Close()

	_ = dialWS(t, server)
	time.Sleep(50 * time.Millisecond)

	// Flood the broadcaster with more messages than the subscriber buffer (64).
	// The key assertion: Broadcast must return without blocking even when the
	// subscriber's send channel is full.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			eb.Broadcast("s", "status", "running")
		}
		close(done)
	}()

	select {
	case <-done:
		// OK — Broadcast returned without blocking despite full buffer
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked — should be non-blocking for slow subscribers")
	}
}

func TestRemoveCleansUpSubscriber(t *testing.T) {
	eb := NewEventsBroadcaster()
	server := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer server.Close()

	conn := dialWS(t, server)
	time.Sleep(50 * time.Millisecond)

	assert.True(t, eb.HasSubscribers())

	// Close the connection from client side — the handler's read loop should
	// detect the error and call Remove.
	conn.Close()
	time.Sleep(100 * time.Millisecond)

	assert.False(t, eb.HasSubscribers())
}

func TestHasSubscribersReturnsCorrectState(t *testing.T) {
	eb := NewEventsBroadcaster()

	assert.False(t, eb.HasSubscribers(), "no subscribers initially")

	server := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer server.Close()

	conn := dialWS(t, server)
	time.Sleep(50 * time.Millisecond)

	assert.True(t, eb.HasSubscribers(), "should have subscriber after connect")

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	assert.False(t, eb.HasSubscribers(), "no subscribers after disconnect")
}

func TestOnSubscribeCallbackCalledOnConnect(t *testing.T) {
	eb := NewEventsBroadcaster()

	var mu sync.Mutex
	var callbackCalls int
	var lastSub *EventSubscriber

	eb.OnSubscribe(func(sub *EventSubscriber) {
		mu.Lock()
		defer mu.Unlock()
		callbackCalls++
		lastSub = sub

		// Push a snapshot event to the new subscriber
		evt := SessionEvent{Type: "status", Session: "test-sess", Value: "idle"}
		data, _ := json.Marshal(evt)
		sub.Send(data)
	})

	server := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer server.Close()

	conn := dialWS(t, server)

	// Read the snapshot event pushed by the callback
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var evt SessionEvent
	require.NoError(t, json.Unmarshal(msg, &evt))
	assert.Equal(t, "status", evt.Type)
	assert.Equal(t, "test-sess", evt.Session)
	assert.Equal(t, "idle", evt.Value)

	mu.Lock()
	assert.Equal(t, 1, callbackCalls)
	assert.NotNil(t, lastSub)
	mu.Unlock()
}

func TestOnSubscribeMultipleCallbacks(t *testing.T) {
	eb := NewEventsBroadcaster()

	var mu sync.Mutex
	var order []string

	eb.OnSubscribe(func(sub *EventSubscriber) {
		mu.Lock()
		order = append(order, "callback-1")
		mu.Unlock()
		evt := SessionEvent{Type: "status", Session: "s1", Value: "running"}
		data, _ := json.Marshal(evt)
		sub.Send(data)
	})
	eb.OnSubscribe(func(sub *EventSubscriber) {
		mu.Lock()
		order = append(order, "callback-2")
		mu.Unlock()
		evt := SessionEvent{Type: "relay", Session: "s2", Value: "connected"}
		data, _ := json.Marshal(evt)
		sub.Send(data)
	})

	server := httptest.NewServer(http.HandlerFunc(eb.HandleSessionEvents))
	defer server.Close()

	conn := dialWS(t, server)

	// Should receive two snapshot events (one from each callback)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, msg1, err := conn.ReadMessage()
	require.NoError(t, err)
	_, msg2, err := conn.ReadMessage()
	require.NoError(t, err)

	var evt1, evt2 SessionEvent
	require.NoError(t, json.Unmarshal(msg1, &evt1))
	require.NoError(t, json.Unmarshal(msg2, &evt2))

	assert.Equal(t, "status", evt1.Type)
	assert.Equal(t, "relay", evt2.Type)

	mu.Lock()
	assert.Equal(t, []string{"callback-1", "callback-2"}, order)
	mu.Unlock()
}

func TestEventSubscriberSendDropsWhenFull(t *testing.T) {
	// Create a subscriber directly (no write pump) to test Send in isolation.
	sub := &EventSubscriber{
		send: make(chan []byte, 64),
	}

	// Fill the buffer completely
	for i := 0; i < 64; i++ {
		sub.Send([]byte(`{"type":"test"}`))
	}
	assert.Len(t, sub.send, 64)

	// This should NOT block — it should drop silently
	done := make(chan struct{})
	go func() {
		sub.Send([]byte(`{"type":"overflow"}`))
		close(done)
	}()

	select {
	case <-done:
		// OK — Send returned without blocking
	case <-time.After(1 * time.Second):
		t.Fatal("Send blocked on full buffer — should be non-blocking")
	}

	// Buffer should still be exactly 64 (overflow was dropped)
	assert.Len(t, sub.send, 64)
}

func TestRegisterCoreRoutes(t *testing.T) {
	c := New(CoreDeps{})
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	// Verify the route is registered by making a non-WS request (should get upgrade error, not 404)
	req := httptest.NewRequest("GET", "/ws/session-events", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// gorilla/websocket returns 400 for non-WS requests, not 404
	assert.NotEqual(t, http.StatusNotFound, rec.Code)
}
