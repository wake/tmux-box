// internal/server/bridge_handler.go
package server

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
)

var bridgeUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleCliBridge handles WebSocket from tbox relay (producer).
// Only one relay per session is allowed.
func (s *Server) handleCliBridge(w http.ResponseWriter, r *http.Request) {
	sessionName := r.PathValue("session")

	// Pre-check to return HTTP 409 before WebSocket upgrade.
	// RegisterRelay below is the authoritative atomic check.
	if s.bridge.HasRelay(sessionName) {
		http.Error(w, "relay already connected", http.StatusConflict)
		return
	}

	conn, err := bridgeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	relayCh, err := s.bridge.RegisterRelay(sessionName)
	if err != nil {
		// Race: another relay registered between HasRelay and here.
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, err.Error()))
		return
	}
	defer func() {
		s.bridge.UnregisterRelay(sessionName)
		// When relay disconnects, revert session mode to "term" if it was in stream/jsonl.
		// This prevents the session from being stuck in stream mode after a failed handoff.
		s.revertModeOnRelayDisconnect(sessionName)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Relay WS → bridge (subprocess stdout → SPA subscribers)
	go func() {
		defer cancel()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			s.bridge.RelayToSubscribers(sessionName, msg)
		}
	}()

	// Bridge → relay WS (SPA user input → subprocess stdin)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-relayCh:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}
}

// handleCliBridgeSubscribe handles WebSocket from SPA clients (consumer).
// Multiple SPA subscribers can connect to the same session.
func (s *Server) handleCliBridgeSubscribe(w http.ResponseWriter, r *http.Request) {
	sessionName := r.PathValue("session")
	if !s.bridge.HasRelay(sessionName) {
		http.Error(w, "no relay connected", http.StatusNotFound)
		return
	}

	conn, err := bridgeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	id, subCh := s.bridge.Subscribe(sessionName)
	if subCh == nil {
		return
	}
	defer s.bridge.Unsubscribe(sessionName, id)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SPA WS → bridge (user input → relay stdin)
	go func() {
		defer cancel()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			s.bridge.SubscriberToRelay(sessionName, msg)
		}
	}()

	// Bridge → SPA WS (relay output → browser)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-subCh:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}
}
