// internal/server/session_handler_test.go
package server_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/stream"
	"github.com/wake/tmux-box/internal/tmux"
)

func setupHandler(t *testing.T) *server.SessionHandler {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return server.NewSessionHandler(db, tmux.NewFakeExecutor(), stream.NewManager())
}

func TestListEmpty(t *testing.T) {
	h := setupHandler(t)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest("GET", "/api/sessions", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var list []store.Session
	json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 0 {
		t.Errorf("want empty, got %d", len(list))
	}
}

func TestCreateSession(t *testing.T) {
	h := setupHandler(t)
	body, _ := json.Marshal(map[string]string{"name": "test", "cwd": "/tmp", "mode": "term"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))
	if rec.Code != 201 {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var s store.Session
	json.NewDecoder(rec.Body).Decode(&s)
	if s.Name != "test" {
		t.Errorf("want name test, got %s", s.Name)
	}
}

func TestCreateMissingFields(t *testing.T) {
	h := setupHandler(t)
	body, _ := json.Marshal(map[string]string{"name": "test"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("want 400 for missing cwd, got %d", rec.Code)
	}
}

func TestCreateInvalidJSON(t *testing.T) {
	h := setupHandler(t)
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader([]byte("not json"))))
	if rec.Code != 400 {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestDeleteSession(t *testing.T) {
	h := setupHandler(t)
	// Create
	body, _ := json.Marshal(map[string]string{"name": "del", "cwd": "/tmp", "mode": "term"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))

	// Delete
	req := httptest.NewRequest("DELETE", "/api/sessions/1", nil)
	req.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != 204 {
		t.Errorf("want 204, got %d", rec.Code)
	}
}

func TestDeleteNotFound(t *testing.T) {
	h := setupHandler(t)
	req := httptest.NewRequest("DELETE", "/api/sessions/999", nil)
	req.SetPathValue("id", "999")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != 404 {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestSwitchModeToStream(t *testing.T) {
	h := setupHandler(t)

	// Create a term session
	body, _ := json.Marshal(map[string]string{"name": "switch-test", "cwd": "/tmp", "mode": "term"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))
	if rec.Code != 201 {
		t.Fatalf("create: want 201, got %d", rec.Code)
	}

	// Switch to stream
	switchBody, _ := json.Marshal(map[string]string{"mode": "stream"})
	req := httptest.NewRequest("POST", "/api/sessions/1/mode", bytes.NewReader(switchBody))
	req.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	h.SwitchMode(rec, req)

	if rec.Code != 200 {
		t.Errorf("switch: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify mode changed via List
	listRec := httptest.NewRecorder()
	h.List(listRec, httptest.NewRequest("GET", "/api/sessions", nil))
	var sessions []store.Session
	json.NewDecoder(listRec.Body).Decode(&sessions)
	if len(sessions) == 0 || sessions[0].Mode != "stream" {
		mode := ""
		if len(sessions) > 0 {
			mode = sessions[0].Mode
		}
		t.Errorf("want mode stream, got %q", mode)
	}
}

func TestSwitchModeInvalidMode(t *testing.T) {
	h := setupHandler(t)

	body, _ := json.Marshal(map[string]string{"name": "test", "cwd": "/tmp", "mode": "term"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))

	switchBody, _ := json.Marshal(map[string]string{"mode": "invalid"})
	req := httptest.NewRequest("POST", "/api/sessions/1/mode", bytes.NewReader(switchBody))
	req.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	h.SwitchMode(rec, req)

	if rec.Code != 400 {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

