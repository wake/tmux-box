// internal/server/bridge_handler.go
package server

import (
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
	if s.bridge.HasRelay(sessionName) {
		http.Error(w, "relay already connected", http.StatusConflict)
		return
	}

	conn, err := bridgeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	relayCh := s.bridge.RegisterRelay(sessionName)
	defer s.bridge.UnregisterRelay(sessionName)

	done := make(chan struct{})

	// Relay WS → bridge (subprocess stdout → SPA subscribers)
	go func() {
		defer close(done)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			s.bridge.RelayToSubscribers(sessionName, msg)
		}
	}()

	// Bridge → relay WS (SPA user input → subprocess stdin)
	go func() {
		for msg := range relayCh {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	<-done
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

	done := make(chan struct{})

	// Bridge → SPA WS (relay output → browser)
	go func() {
		defer close(done)
		for msg := range subCh {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// SPA WS → bridge (user input → relay stdin)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			s.bridge.SubscriberToRelay(sessionName, msg)
		}
	}()

	<-done
}
