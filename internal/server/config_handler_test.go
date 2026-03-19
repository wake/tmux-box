// internal/server/config_handler_test.go
package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

const testToken = "secret-token"

// newConfigTestServer creates a test server with a known config and a temp config file path.
func newConfigTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfgPath := filepath.Join(t.TempDir(), "config.toml")

	cfg := config.Config{
		Bind:  "127.0.0.1",
		Port:  7860,
		Token: testToken,
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

	s := server.New(cfg, db, tmux.NewFakeExecutor(), cfgPath)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	return srv, cfgPath
}

// authGet performs an authenticated GET request.
func authGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// authPut performs an authenticated PUT request with the given body.
func authPut(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGetConfig(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	resp := authGet(t, srv.URL+"/api/config")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var cfg config.Config
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Token must be redacted
	if cfg.Token != "" {
		t.Errorf("token should be redacted, got %q", cfg.Token)
	}

	// Stream presets should be present
	if len(cfg.Stream.Presets) != 1 {
		t.Fatalf("want 1 stream preset, got %d", len(cfg.Stream.Presets))
	}
	if cfg.Stream.Presets[0].Name != "cc" {
		t.Errorf("want preset name 'cc', got %q", cfg.Stream.Presets[0].Name)
	}

	// Detect config should be present
	if cfg.Detect.PollInterval != 2 {
		t.Errorf("want poll_interval 2, got %d", cfg.Detect.PollInterval)
	}
	if len(cfg.Detect.CCCommands) != 1 || cfg.Detect.CCCommands[0] != "claude" {
		t.Errorf("want cc_commands [claude], got %v", cfg.Detect.CCCommands)
	}
}

func TestPutConfigUpdatesPresets(t *testing.T) {
	srv, cfgPath := newConfigTestServer(t)

	// PUT: update stream presets
	update := map[string]any{
		"stream": map[string]any{
			"presets": []map[string]string{
				{"name": "cc", "command": "claude -p --input-format stream-json --output-format stream-json"},
				{"name": "custom", "command": "my-custom-command"},
			},
		},
	}
	body, _ := json.Marshal(update)
	resp := authPut(t, srv.URL+"/api/config", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, string(b))
	}

	var putResult config.Config
	json.NewDecoder(resp.Body).Decode(&putResult)

	// Token must be redacted in response
	if putResult.Token != "" {
		t.Errorf("token should be redacted in PUT response, got %q", putResult.Token)
	}

	// Verify updated presets in response
	if len(putResult.Stream.Presets) != 2 {
		t.Fatalf("want 2 stream presets, got %d", len(putResult.Stream.Presets))
	}
	if putResult.Stream.Presets[1].Name != "custom" {
		t.Errorf("want second preset name 'custom', got %q", putResult.Stream.Presets[1].Name)
	}

	// GET: verify update persisted
	getResp := authGet(t, srv.URL+"/api/config")
	defer getResp.Body.Close()

	var getCfg config.Config
	json.NewDecoder(getResp.Body).Decode(&getCfg)
	if len(getCfg.Stream.Presets) != 2 {
		t.Fatalf("GET after PUT: want 2 stream presets, got %d", len(getCfg.Stream.Presets))
	}

	// Verify config file was written
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("config file should not be empty after PUT")
	}
}

func TestPutConfigUpdatesDetect(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	update := map[string]any{
		"detect": map[string]any{
			"cc_commands":   []string{"claude", "aider"},
			"poll_interval": 5,
		},
	}
	body, _ := json.Marshal(update)
	resp := authPut(t, srv.URL+"/api/config", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, string(b))
	}

	var cfg config.Config
	json.NewDecoder(resp.Body).Decode(&cfg)

	if len(cfg.Detect.CCCommands) != 2 {
		t.Fatalf("want 2 cc_commands, got %d", len(cfg.Detect.CCCommands))
	}
	if cfg.Detect.CCCommands[1] != "aider" {
		t.Errorf("want cc_commands[1]='aider', got %q", cfg.Detect.CCCommands[1])
	}
	if cfg.Detect.PollInterval != 5 {
		t.Errorf("want poll_interval 5, got %d", cfg.Detect.PollInterval)
	}
}

func TestPutConfigPartialDetect(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	// Only update cc_commands, leave poll_interval unchanged
	update := map[string]any{
		"detect": map[string]any{
			"cc_commands": []string{"claude", "cursor"},
		},
	}
	body, _ := json.Marshal(update)
	resp := authPut(t, srv.URL+"/api/config", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var cfg config.Config
	json.NewDecoder(resp.Body).Decode(&cfg)

	// cc_commands updated
	if len(cfg.Detect.CCCommands) != 2 {
		t.Fatalf("want 2 cc_commands, got %d", len(cfg.Detect.CCCommands))
	}
	// poll_interval should remain at original value (2)
	if cfg.Detect.PollInterval != 2 {
		t.Errorf("poll_interval should remain 2, got %d", cfg.Detect.PollInterval)
	}
}

func TestPutConfigUpdatesTerminal(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	update := map[string]any{
		"terminal": map[string]any{
			"sizing_mode": "terminal-first",
		},
	}
	body, _ := json.Marshal(update)
	resp := authPut(t, srv.URL+"/api/config", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, string(b))
	}

	var cfg config.Config
	json.NewDecoder(resp.Body).Decode(&cfg)

	if cfg.Terminal.SizingMode != "terminal-first" {
		t.Errorf("want sizing_mode=terminal-first, got %q", cfg.Terminal.SizingMode)
	}

	// GET: verify persisted
	getResp := authGet(t, srv.URL+"/api/config")
	defer getResp.Body.Close()

	var getCfg config.Config
	json.NewDecoder(getResp.Body).Decode(&getCfg)

	if getCfg.Terminal.SizingMode != "terminal-first" {
		t.Errorf("GET after PUT: want sizing_mode=terminal-first, got %q", getCfg.Terminal.SizingMode)
	}
}

func TestPutConfigPartialTerminal(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	// Empty terminal object should not change sizing_mode
	update := map[string]any{
		"terminal": map[string]any{},
	}
	body, _ := json.Marshal(update)
	resp := authPut(t, srv.URL+"/api/config", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var cfg config.Config
	json.NewDecoder(resp.Body).Decode(&cfg)

	// sizing_mode should remain empty (default "auto")
	if cfg.Terminal.SizingMode != "" {
		t.Errorf("sizing_mode should remain empty, got %q", cfg.Terminal.SizingMode)
	}
}

func TestPutConfigRejectsInvalidSizingMode(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	update := map[string]any{
		"terminal": map[string]any{
			"sizing_mode": "banana",
		},
	}
	body, _ := json.Marshal(update)
	resp := authPut(t, srv.URL+"/api/config", body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid sizing_mode, got %d", resp.StatusCode)
	}
}

func TestPutConfigInvalidJSON(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	resp := authPut(t, srv.URL+"/api/config", []byte("not json"))
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestPutConfigEmptyBody(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	// Empty JSON object — should be a no-op, return current config
	resp := authPut(t, srv.URL+"/api/config", []byte("{}"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var cfg config.Config
	json.NewDecoder(resp.Body).Decode(&cfg)

	// Original values should remain
	if len(cfg.Stream.Presets) != 1 {
		t.Errorf("stream presets should remain unchanged, got %d", len(cfg.Stream.Presets))
	}
}

func TestPutConfigIgnoresZeroPollInterval(t *testing.T) {
	srv, _ := newConfigTestServer(t)

	// Sending poll_interval=0 should be ignored (keep current)
	update := map[string]any{
		"detect": map[string]any{
			"poll_interval": 0,
		},
	}
	body, _ := json.Marshal(update)
	resp := authPut(t, srv.URL+"/api/config", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var cfg config.Config
	json.NewDecoder(resp.Body).Decode(&cfg)

	if cfg.Detect.PollInterval != 2 {
		t.Errorf("poll_interval should remain 2 when 0 is sent, got %d", cfg.Detect.PollInterval)
	}
}
