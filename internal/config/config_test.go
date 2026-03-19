package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/config"
)

func TestLoadDefaultsWhenFileNotExist(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bind != "127.0.0.1" {
		t.Errorf("bind: want 127.0.0.1, got %s", cfg.Bind)
	}
	if cfg.Port != 7860 {
		t.Errorf("port: want 7860, got %d", cfg.Port)
	}
}

func TestLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
bind = "100.64.0.2"
port = 9090
token = "secret123"
allow = ["10.0.0.0/8"]
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bind != "100.64.0.2" {
		t.Errorf("bind: want 100.64.0.2, got %s", cfg.Bind)
	}
	if cfg.Port != 9090 {
		t.Errorf("port: want 9090, got %d", cfg.Port)
	}
	if cfg.Token != "secret123" {
		t.Errorf("token: want secret123, got %s", cfg.Token)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "10.0.0.0/8" {
		t.Errorf("allow: want [10.0.0.0/8], got %v", cfg.Allow)
	}
}

func TestLoadAutoDefaultPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7860 {
		t.Errorf("port: want 7860, got %d", cfg.Port)
	}
}

func TestLoadConfigWithPresets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`
bind = "0.0.0.0"
port = 8080

[[stream.presets]]
name = "cc"
command = "claude -p --input-format stream-json --output-format stream-json"

[[stream.presets]]
name = "dangerous"
command = "claude -p --input-format stream-json --output-format stream-json --dangerously-skip-permissions"

[[jsonl.presets]]
name = "cc"
command = ""

[detect]
cc_commands = ["claude", "cld"]
poll_interval = 3
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Stream.Presets) != 2 {
		t.Fatalf("expected 2 stream presets, got %d", len(cfg.Stream.Presets))
	}
	if cfg.Stream.Presets[0].Name != "cc" {
		t.Fatalf("expected first preset name 'cc', got %q", cfg.Stream.Presets[0].Name)
	}
	if len(cfg.Detect.CCCommands) != 2 {
		t.Fatalf("expected 2 cc_commands, got %d", len(cfg.Detect.CCCommands))
	}
	if cfg.Detect.PollInterval != 3 {
		t.Fatalf("expected poll_interval 3, got %d", cfg.Detect.PollInterval)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Load(filepath.Join(dir, "missing.toml"))
	if len(cfg.Stream.Presets) != 1 {
		t.Fatalf("expected 1 default stream preset, got %d", len(cfg.Stream.Presets))
	}
	if cfg.Detect.PollInterval != 2 {
		t.Fatalf("expected default poll_interval 2, got %d", cfg.Detect.PollInterval)
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	os.WriteFile(path, []byte(`not valid toml {{{{`), 0644)

	_, err := config.Load(path)
	if err == nil {
		t.Error("want error for invalid TOML")
	}
}

func TestIsSessionGroupDefaultFalse(t *testing.T) {
	tc := config.TerminalConfig{}
	if tc.IsSessionGroup() {
		t.Error("expected IsSessionGroup() to be false by default")
	}
}

func TestIsSessionGroupTrue(t *testing.T) {
	v := true
	tc := config.TerminalConfig{SessionGroup: &v}
	if !tc.IsSessionGroup() {
		t.Error("expected IsSessionGroup() to be true when set")
	}
}
