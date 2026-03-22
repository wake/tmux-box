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
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

// newHandoffTestServer creates a test server with preset config suitable for handoff tests.
// It registers "test-session" in FakeExecutor with tmux ID "$0" and returns the encoded code.
func newHandoffTestServer(t *testing.T) (*httptest.Server, *store.MetaStore, *tmux.FakeExecutor, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	meta, err := store.OpenMeta(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { meta.Close() })

	fakeTmux := tmux.NewFakeExecutor()
	// Register session in tmux (handoff now looks up via tmux, not legacy store)
	fakeTmux.AddSession("test-session", "/tmp")
	// Register at TmuxTarget format (session:window) to match handoff code.
	fakeTmux.SetPaneCommand("test-session:0", "claude") // CC running (idle)
	fakeTmux.SetPaneContent("test-session:0", "  Session ID: deadbeef-1234-5678-9abc-def012345678\n\u276f ")

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

	s := server.New(cfg, db, meta, fakeTmux, "")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	// Encode $0 → session code
	code, err := session.EncodeSessionID("$0")
	if err != nil {
		t.Fatal(err)
	}

	return srv, meta, fakeTmux, code
}

func TestHandoffHappyPath(t *testing.T) {
	srv, _, _, code := newHandoffTestServer(t)

	// POST handoff
	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
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
	srv, _, _, code := newHandoffTestServer(t)

	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "nonexistent"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandoffSessionNotFound(t *testing.T) {
	srv, _, _, _ := newHandoffTestServer(t)

	// Use a valid code that decodes to a tmux ID not in FakeExecutor
	code, _ := session.EncodeSessionID("$999")
	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestHandoffConflict(t *testing.T) {
	srv, _, _, code := newHandoffTestServer(t)

	// First handoff — should get 202
	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp1, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("first handoff: want 202, got %d", resp1.StatusCode)
	}

	// Second handoff immediately — should get 409 (lock held by async goroutine)
	body2, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp2, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body2))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second handoff: want 409, got %d", resp2.StatusCode)
	}
}

func TestHandoffInvalidMode(t *testing.T) {
	srv, _, _, code := newHandoffTestServer(t)

	body, _ := json.Marshal(map[string]string{"mode": "invalid", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandoffInvalidCode(t *testing.T) {
	srv, _, _, _ := newHandoffTestServer(t)

	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	// "abc" is not a valid 6-char base36 code
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
	srv, _, _, code := newHandoffTestServer(t)

	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandoffTermMode(t *testing.T) {
	srv, meta, _, code := newHandoffTestServer(t)

	// Set cc_session_id in MetaStore so handoff-to-term has something to work with
	ccID := "01abc234-5678-9def-0123-456789abcdef"
	meta.SetMeta("$0", store.SessionMeta{
		TmuxID:      "$0",
		Mode:        "stream",
		CCSessionID: ccID,
	})

	// POST handoff with mode=term (no preset)
	body, _ := json.Marshal(map[string]string{"mode": "term"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
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

func TestHandoffTermModeNoPresetRequired(t *testing.T) {
	srv, meta, _, code := newHandoffTestServer(t)

	meta.SetMeta("$0", store.SessionMeta{
		TmuxID:      "$0",
		Mode:        "stream",
		CCSessionID: "some-session-id",
	})

	// mode=term without preset should still be accepted (not 400)
	body, _ := json.Marshal(map[string]string{"mode": "term"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
}

// TestHandoffUsesTmuxTarget verifies that runHandoff sends all tmux commands
// (detect, send-keys, capture-pane) to TmuxTarget ("session:0" format)
// rather than the bare session name.
func TestHandoffUsesTmuxTarget(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	meta, err := store.OpenMeta(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { meta.Close() })

	fakeTmux := tmux.NewFakeExecutor()
	// Register session in tmux
	fakeTmux.AddSession("my-session", "/tmp")
	// Register pane data at TmuxTarget "my-session:0" — NOT bare "my-session".
	// If handoff code uses bare Name, detect/capture will fail (map miss).
	fakeTmux.SetPaneCommand("my-session:0", "claude")
	fakeTmux.SetPaneContent("my-session:0", "  Session ID: deadbeef-1234\n  Cwd: /tmp/test\n\u276f ")

	cfg := config.Config{
		Port: 7860,
		Bind: "127.0.0.1",
		Stream: config.StreamConfig{
			Presets: []config.Preset{
				{Name: "cc", Command: "claude -p --input-format stream-json --output-format stream-json"},
			},
		},
		Detect: config.DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
	}

	s := server.New(cfg, db, meta, fakeTmux, "")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	code, _ := session.EncodeSessionID("$0")

	// Trigger handoff
	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	// Wait for async handoff goroutine to progress past detect + send-keys steps.
	// It will eventually fail waiting for relay (expected), but the tmux calls
	// should have been recorded by then.
	time.Sleep(8 * time.Second)

	// Verify SendKeys calls used TmuxTarget, not bare Name
	for _, call := range fakeTmux.KeysSent() {
		if call.Target == "my-session" {
			t.Errorf("SendKeys used bare session name %q instead of TmuxTarget; keys=%q", call.Target, call.Keys)
		}
	}
	for _, call := range fakeTmux.RawKeysSent() {
		if call.Target == "my-session" {
			t.Errorf("SendKeysRaw used bare session name %q instead of TmuxTarget; keys=%v", call.Target, call.Keys)
		}
	}

	// Verify at least some calls were made to the correct target
	hasCorrectTarget := false
	for _, call := range fakeTmux.KeysSent() {
		if call.Target == "my-session:0" {
			hasCorrectTarget = true
			break
		}
	}
	for _, call := range fakeTmux.RawKeysSent() {
		if call.Target == "my-session:0" {
			hasCorrectTarget = true
			break
		}
	}
	if !hasCorrectTarget {
		t.Error("no tmux commands were sent to TmuxTarget \"my-session:0\"")
	}
}

// TestHandoffResizesPaneTooSmall verifies that handoff enlarges a too-small pane
// before sending /status, so capture-pane can extract the session ID.
func TestHandoffResizesPaneTooSmall(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	meta, err := store.OpenMeta(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { meta.Close() })

	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("small-session", "/tmp")
	fakeTmux.SetPaneCommand("small-session:0", "claude")
	fakeTmux.SetPaneContent("small-session:0", "  Session ID: deadbeef-1234\n  Cwd: /tmp/test\n\u276f ")
	// Simulate a tiny pane (like when xterm.js container is display:none)
	fakeTmux.SetPaneSize("small-session:0", 10, 5)

	cfg := config.Config{
		Port: 7860,
		Bind: "127.0.0.1",
		Stream: config.StreamConfig{
			Presets: []config.Preset{
				{Name: "cc", Command: "claude -p --input-format stream-json --output-format stream-json"},
			},
		},
		Detect: config.DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
	}

	s := server.New(cfg, db, meta, fakeTmux, "")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	code, _ := session.EncodeSessionID("$0")

	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	time.Sleep(8 * time.Second)

	// Verify that ResizeWindow was called to enlarge the pane
	sz, ok := fakeTmux.PaneSizeOf("small-session:0")
	if !ok {
		t.Fatal("pane size not tracked")
	}
	if sz[0] < 80 || sz[1] < 24 {
		t.Errorf("pane should have been resized to at least 80x24, got %dx%d", sz[0], sz[1])
	}

	// Verify that ResizeWindowAuto was called to restore auto-sizing
	autoCalls := fakeTmux.AutoResizeCalls()
	if len(autoCalls) == 0 {
		t.Fatal("ResizeWindowAuto should have been called to restore auto-sizing after /status extraction")
	}
	if autoCalls[0] != "small-session:0" {
		t.Errorf("ResizeWindowAuto target: want small-session:0, got %s", autoCalls[0])
	}

	// Verify SetWindowOption was called to restore window-size to latest
	swoCalls := fakeTmux.SetWindowOptionCalls()
	swoFound := false
	for _, c := range swoCalls {
		if c.Target == "small-session:0" && c.Option == "window-size" && c.Value == "latest" {
			swoFound = true
			break
		}
	}
	if !swoFound {
		t.Error("expected SetWindowOption(small-session:0, window-size, latest) after handoff cleanup")
	}
}

func TestHandoffSendsEscapeAndCcBeforeDetect(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	meta, err := store.OpenMeta(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { meta.Close() })

	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("test-session", "/tmp")
	fakeTmux.SetPaneCommand("test-session:0", "claude")
	fakeTmux.SetPaneContent("test-session:0", "  Session ID: deadbeef-1234\n\u276f ")

	cfg := config.Config{
		Port: 7860,
		Stream: config.StreamConfig{
			Presets: []config.Preset{
				{Name: "cc", Command: "claude -p --input-format stream-json --output-format stream-json"},
			},
		},
		Detect: config.DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
	}

	s := server.New(cfg, db, meta, fakeTmux, "")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	code, _ := session.EncodeSessionID("$0")

	body, _ := json.Marshal(map[string]string{"mode": "stream", "preset": "cc"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	time.Sleep(2 * time.Second)

	// Verify the first three raw key calls: -X cancel (exit copy-mode), Escape, C-c
	rawKeys := fakeTmux.RawKeysSent()
	if len(rawKeys) < 3 {
		t.Fatalf("expected at least 3 raw key calls, got %d", len(rawKeys))
	}
	if len(rawKeys[0].Keys) < 2 || rawKeys[0].Keys[0] != "-X" || rawKeys[0].Keys[1] != "cancel" {
		t.Errorf("first raw key should be [-X cancel], got %v", rawKeys[0].Keys)
	}
	if len(rawKeys[1].Keys) == 0 || rawKeys[1].Keys[0] != "Escape" {
		t.Errorf("second raw key should be Escape, got %v", rawKeys[1].Keys)
	}
	if len(rawKeys[2].Keys) == 0 || rawKeys[2].Keys[0] != "C-c" {
		t.Errorf("third raw key should be C-c, got %v", rawKeys[2].Keys)
	}
}

func TestHandoffJSONLPreset(t *testing.T) {
	srv, _, _, code := newHandoffTestServer(t)

	// Use jsonl mode with jsonl preset
	body, _ := json.Marshal(map[string]string{"mode": "jsonl", "preset": "cc-jsonl"})
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", bytes.NewReader(body))
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
