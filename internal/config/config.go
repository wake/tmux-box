package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Preset struct {
	Name    string `toml:"name"    json:"name"`
	Command string `toml:"command" json:"command"`
}

type StreamConfig struct {
	Presets []Preset `toml:"presets" json:"presets"`
}

type JSONLConfig struct {
	Presets []Preset `toml:"presets" json:"presets"`
}

type DetectConfig struct {
	CCCommands   []string `toml:"cc_commands"   json:"cc_commands"`
	PollInterval int      `toml:"poll_interval" json:"poll_interval"`
}

type TerminalConfig struct {
	AutoResize   *bool `toml:"auto_resize"  json:"auto_resize"`
	SessionGroup *bool `toml:"session_group" json:"session_group"`
}

func (tc TerminalConfig) IsAutoResize() bool {
	return tc.AutoResize == nil || *tc.AutoResize
}

func (tc TerminalConfig) IsSessionGroup() bool {
	return tc.SessionGroup != nil && *tc.SessionGroup
}

type Config struct {
	Bind         string         `toml:"bind"           json:"bind"`
	Port         int            `toml:"port"           json:"port"`
	Token        string         `toml:"token"          json:"token"`
	Allow        []string       `toml:"allow"          json:"allow"`
	DataDir      string         `toml:"data_dir"       json:"data_dir"`
	AllowedPaths []string       `toml:"allowed_paths"  json:"allowed_paths"`
	Terminal     TerminalConfig `toml:"terminal"       json:"terminal"`
	Stream       StreamConfig   `toml:"stream"         json:"stream"`
	JSONL        JSONLConfig    `toml:"jsonl"          json:"jsonl"`
	Detect       DetectConfig   `toml:"detect"         json:"detect"`
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Bind:    "127.0.0.1",
		Port:    7860,
		DataDir: filepath.Join(home, ".config", "tbox"),
		Stream: StreamConfig{
			Presets: []Preset{{
				Name:    "cc",
				Command: "claude -p --verbose --input-format stream-json --output-format stream-json",
			}},
		},
		Detect: DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
	}
}

// Load reads config from path. Empty path → tries ~/.config/tbox/config.toml.
// Missing file → returns defaults (no error). Invalid TOML → returns error.
func Load(path string) (Config, error) {
	cfg := defaults()

	if path == "" {
		path = filepath.Join(cfg.DataDir, "config.toml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}
