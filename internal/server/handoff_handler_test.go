// internal/server/handoff_handler_test.go
package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

// newHandoffTestServer creates a test server with preset config suitable for handoff tests.
func newHandoffTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneCommand("test-session", "zsh") // normal shell state

	cfg := config.Config{
		Port: 7860,
		Stream: config.StreamConfig{
			Presets: []config.Preset{
				{Name: "cc", Command: "claude -p --input-format stream-json --output-format stream-json"},
			},
		},
		JSONL: config.JSONLConfig{
			Presets: []config.Preset{
				{Name: "cc-jsonl", Command: "claude --jsonl"},
			},
		},
		Detect: config.DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
	}

	s := server.New(cfg, db, fakeTmux)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	return srv, db
}

func TestHandoffHappyPath(t *testing.T) {
	srv, db := newHandoffTestServer(t)

	// Create a session in the DB
	_, err := db.CreateSession(store.Session{
		Name:       "test-session",
		TmuxTarget: "test-session:0",
		Cwd:        "/tmp",
		Mode:       "term",
	})
	if err != nil {
		t.Fatal(err)
	}

	// POST handoff
	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["handoff_id"] == "" {
		t.Fatal("expected handoff_id in response")
	}
	if len(result["handoff_id"]) != 16 { // 8 bytes = 16 hex chars
		t.Errorf("handoff_id length: want 16, got %d", len(result["handoff_id"]))
	}

	// Wait a bit for the async goroutine to proceed
	// (it will eventually timeout waiting for relay to connect — that's expected)
	time.Sleep(100 * time.Millisecond)
}

func TestHandoffPresetNotFound(t *testing.T) {
	srv, db := newHandoffTestServer(t)

	_, err := db.CreateSession(store.Session{
		Name:       "test-session",
		TmuxTarget: "test-session:0",
		Cwd:        "/tmp",
		Mode:       "term",
	})
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "nonexistent"})
	resp, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandoffSessionNotFound(t *testing.T) {
	srv, _ := newHandoffTestServer(t)

	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/999/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestHandoffConflict(t *testing.T) {
	srv, db := newHandoffTestServer(t)

	_, err := db.CreateSession(store.Session{
		Name:       "test-session",
		TmuxTarget: "test-session:0",
		Cwd:        "/tmp",
		Mode:       "term",
	})
	if err != nil {
		t.Fatal(err)
	}

	// First handoff — should get 202
	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp1, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("first handoff: want 202, got %d", resp1.StatusCode)
	}

	// Second handoff immediately — should get 409 (lock held by async goroutine)
	body2, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp2, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader(body2))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second handoff: want 409, got %d", resp2.StatusCode)
	}
}

func TestHandoffInvalidMode(t *testing.T) {
	srv, db := newHandoffTestServer(t)

	_, err := db.CreateSession(store.Session{
		Name:       "test-session",
		TmuxTarget: "test-session:0",
		Cwd:        "/tmp",
		Mode:       "term",
	})
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"mode": "invalid", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandoffInvalidID(t *testing.T) {
	srv, _ := newHandoffTestServer(t)

	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/abc/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandoffInvalidBody(t *testing.T) {
	srv, _ := newHandoffTestServer(t)

	resp, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandoffJSONLPreset(t *testing.T) {
	srv, db := newHandoffTestServer(t)

	_, err := db.CreateSession(store.Session{
		Name:       "test-session",
		TmuxTarget: "test-session:0",
		Cwd:        "/tmp",
		Mode:       "term",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use jsonl mode with jsonl preset
	body, _ := json.Marshal(map[string]string{"mode": "jsonl", "preset": "cc-jsonl"})
	resp, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["handoff_id"] == "" {
		t.Fatal("expected handoff_id in response")
	}

	time.Sleep(100 * time.Millisecond)
}
