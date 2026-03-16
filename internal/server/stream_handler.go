// internal/server/stream_handler.go
package server

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/stream"
)

var streamUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// HandleStreamWS bridges a WebSocket connection to a StreamSession's stdin/stdout.
func HandleStreamWS(w http.ResponseWriter, r *http.Request, sess *stream.StreamSession) {
	conn, err := streamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("stream ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	// Subscribe to session output
	ch := sess.Subscribe()
	defer sess.Unsubscribe(ch)

	done := make(chan struct{})

	// Session stdout → WebSocket
	go func() {
		defer close(done)
		for line := range ch {
			if err := conn.WriteMessage(websocket.TextMessage, line); err != nil {
				return
			}
		}
	}()

	// WebSocket → Session stdin
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			sess.Send(msg)
		}
	}()

	// Wait for either direction to close
	select {
	case <-done:
	case <-sess.Done():
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session ended"))
	}
}
