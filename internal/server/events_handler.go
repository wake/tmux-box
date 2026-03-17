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

// EventsBroadcaster manages WebSocket subscribers for session events.
type EventsBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[*websocket.Conn]struct{}
}

// NewEventsBroadcaster creates a new EventsBroadcaster.
func NewEventsBroadcaster() *EventsBroadcaster {
	return &EventsBroadcaster{
		subscribers: make(map[*websocket.Conn]struct{}),
	}
}

// Add registers a WebSocket connection as subscriber.
func (eb *EventsBroadcaster) Add(conn *websocket.Conn) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.subscribers[conn] = struct{}{}
}

// Remove unregisters a WebSocket connection.
func (eb *EventsBroadcaster) Remove(conn *websocket.Conn) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.subscribers, conn)
}

// Broadcast sends a JSON event to all subscribers.
// Failed writes cause the connection to be removed.
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
	conns := make([]*websocket.Conn, 0, len(eb.subscribers))
	for c := range eb.subscribers {
		conns = append(conns, c)
	}
	eb.mu.RUnlock()

	var failed []*websocket.Conn
	for _, c := range conns {
		if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
			failed = append(failed, c)
		}
	}

	if len(failed) > 0 {
		eb.mu.Lock()
		for _, c := range failed {
			delete(eb.subscribers, c)
		}
		eb.mu.Unlock()
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
	defer conn.Close()

	s.events.Add(conn)
	defer s.events.Remove(conn)

	// Keep connection alive — read (and discard) messages to detect disconnect.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
