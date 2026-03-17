// internal/server/events_handler.go
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// sessionEvent is the JSON structure broadcast to all WS subscribers.
type sessionEvent struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Value   string `json:"value"`
}

// eventSubscriber wraps a WebSocket connection with a buffered send channel.
// A dedicated goroutine per subscriber handles all writes to avoid concurrent
// WriteMessage calls (gorilla/websocket requires one concurrent writer max).
type eventSubscriber struct {
	conn *websocket.Conn
	send chan []byte
}

// EventsBroadcaster manages WebSocket subscribers for session events.
type EventsBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[*eventSubscriber]struct{}
}

// NewEventsBroadcaster creates a new EventsBroadcaster.
func NewEventsBroadcaster() *EventsBroadcaster {
	return &EventsBroadcaster{
		subscribers: make(map[*eventSubscriber]struct{}),
	}
}

// Add registers a WebSocket connection as subscriber and starts its write pump.
// Returns the subscriber handle (needed for Remove).
func (eb *EventsBroadcaster) Add(conn *websocket.Conn) *eventSubscriber {
	sub := &eventSubscriber{
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
				// Write failed — close send channel won't help here since we're
				// the reader; the read loop in handleSessionEvents will detect
				// the broken connection and call Remove.
				return
			}
		}
	}()

	return sub
}

// Remove unregisters a subscriber and closes its send channel.
func (eb *EventsBroadcaster) Remove(sub *eventSubscriber) {
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
	msg, err := json.Marshal(sessionEvent{
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

// handleSessionEvents handles /ws/session-events — SPA subscribes for status + handoff events.
func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := bridgeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	sub := s.events.Add(conn)
	defer s.events.Remove(sub)

	// Keep connection alive — read (and discard) messages to detect disconnect.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
