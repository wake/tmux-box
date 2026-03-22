// internal/server/bridge_handler.go
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/wake/tmux-box/internal/store"

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
	s.events.Broadcast(sessionName, "relay", "connected")
	defer func() {
		s.events.Broadcast(sessionName, "relay", "disconnected")
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
		initCaptured := false
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			s.bridge.RelayToSubscribers(sessionName, msg)

			// One-shot init metadata capture: extract model from CC init message
			if !initCaptured && bytes.Contains(msg, []byte(`"subtype":"init"`)) {
				var init struct {
					Type    string `json:"type"`
					Subtype string `json:"subtype"`
					Model   string `json:"model"`
				}
				if json.Unmarshal(msg, &init) == nil && init.Type == "system" && init.Subtype == "init" {
					initCaptured = true
					if init.Model != "" {
						if sess, err := s.store.GetSessionByName(sessionName); err == nil {
							s.store.UpdateSession(sess.ID, store.SessionUpdate{CCModel: &init.Model})
							// Sync MetaStore: update cc_model
							if s.meta != nil {
								tmuxSessions, err := s.tmux.ListSessions()
								if err == nil {
									for _, ts := range tmuxSessions {
										if ts.Name == sessionName {
											if err := s.meta.UpdateMeta(ts.ID, store.MetaUpdate{CCModel: &init.Model}); err != nil {
												log.Printf("bridge: meta sync cc_model error: %v", err)
											}
											break
										}
									}
								}
							}
							s.events.Broadcast(sessionName, "init", init.Model)
						}
					}
				}
			}
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
