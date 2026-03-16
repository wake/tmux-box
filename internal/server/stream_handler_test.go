// internal/server/stream_handler_test.go
package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/stream"
)

func TestStreamWSEcho(t *testing.T) {
	mgr := stream.NewManager()
	defer mgr.StopAll()

	// Start a "cat" session to echo input
	mgr.Start("echo-test", "cat", []string{}, "/tmp")

	handler := &streamTestHandler{mgr: mgr}
	srv := httptest.NewServer(http.HandlerFunc(handler.handle))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "?session=echo-test"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send a JSON message
	msg := map[string]interface{}{"type": "user", "message": map[string]string{"role": "user", "content": "hello"}}
	data, _ := json.Marshal(msg)
	ws.WriteMessage(websocket.TextMessage, data)

	// Read echo
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, reply, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reply), "hello") {
		t.Errorf("want echo containing hello, got %s", reply)
	}
}

type streamTestHandler struct {
	mgr *stream.Manager
}

func (h *streamTestHandler) handle(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("session")
	sess := h.mgr.Get(name)
	if sess == nil {
		http.Error(w, "not found", 404)
		return
	}
	server.HandleStreamWS(w, r, sess)
}
