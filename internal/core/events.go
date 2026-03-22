package core

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// SessionEvent is the JSON structure broadcast to all WS subscribers.
type SessionEvent struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Value   string `json:"value"`
}

// EventSubscriber wraps a WebSocket connection with a buffered send channel.
// A dedicated goroutine per subscriber handles all writes to avoid concurrent
// WriteMessage calls (gorilla/websocket requires one concurrent writer max).
type EventSubscriber struct {
	conn *websocket.Conn
	send chan []byte
}

// Send pushes data to the subscriber's write pump. Non-blocking — if the
// buffer is full the message is silently dropped.
func (sub *EventSubscriber) Send(data []byte) {
	select {
	case sub.send <- data:
	default: // drop if full
	}
}

// EventsBroadcaster manages WebSocket subscribers for session events.
type EventsBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[*EventSubscriber]struct{}
	onSubscribe []func(*EventSubscriber)
}

// NewEventsBroadcaster creates a new EventsBroadcaster.
func NewEventsBroadcaster() *EventsBroadcaster {
	return &EventsBroadcaster{
		subscribers: make(map[*EventSubscriber]struct{}),
	}
}

// Add registers a WebSocket connection as subscriber and starts its write pump.
// Returns the subscriber handle (needed for Remove).
func (eb *EventsBroadcaster) Add(conn *websocket.Conn) *EventSubscriber {
	sub := &EventSubscriber{
		conn: conn,
		send: make(chan []byte, 64),
	}
	eb.mu.Lock()
	eb.subscribers[sub] = struct{}{}
	eb.mu.Unlock()

	// Start write pump — the only goroutine that calls WriteMessage on this conn.
	go func() {
		for msg := range sub.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				// Write failed — close the connection so the read loop in
				// HandleSessionEvents unblocks and calls Remove.
				conn.Close()
				return
			}
		}
	}()

	return sub
}

// Remove unregisters a subscriber and closes its send channel.
func (eb *EventsBroadcaster) Remove(sub *EventSubscriber) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if _, ok := eb.subscribers[sub]; ok {
		delete(eb.subscribers, sub)
		close(sub.send)
		sub.conn.Close()
	}
}

// Broadcast sends a JSON event to all subscribers.
// Messages are sent non-blocking; slow subscribers that have a full buffer are dropped.
func (eb *EventsBroadcaster) Broadcast(session, eventType, value string) {
	msg, err := json.Marshal(SessionEvent{
		Type:    eventType,
		Session: session,
		Value:   value,
	})
	if err != nil {
		log.Printf("events: marshal error: %v", err)
		return
	}

	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for sub := range eb.subscribers {
		select {
		case sub.send <- msg:
		default:
			// Subscriber too slow — drop this message.
		}
	}
}

// HasSubscribers returns true if any clients are connected.
func (eb *EventsBroadcaster) HasSubscribers() bool {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.subscribers) > 0
}

// OnSubscribe registers a callback invoked when a new WS subscriber connects.
// Callbacks receive the subscriber and can use sub.Send() to push snapshot data.
func (eb *EventsBroadcaster) OnSubscribe(fn func(sub *EventSubscriber)) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.onSubscribe = append(eb.onSubscribe, fn)
}

// HandleSessionEvents handles /ws/session-events — SPA subscribes for
// status, relay, handoff, and init events.
func (eb *EventsBroadcaster) HandleSessionEvents(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	sub := eb.Add(conn)
	defer eb.Remove(sub)

	// Call all registered OnSubscribe callbacks.
	eb.mu.RLock()
	callbacks := make([]func(*EventSubscriber), len(eb.onSubscribe))
	copy(callbacks, eb.onSubscribe)
	eb.mu.RUnlock()
	for _, fn := range callbacks {
		fn(sub)
	}

	// Keep connection alive — read (and discard) messages to detect disconnect.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
