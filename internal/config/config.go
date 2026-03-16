package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Bind         string   `toml:"bind"`
	Port         int      `toml:"port"`
	Token        string   `toml:"token"`
	Allow        []string `toml:"allow"`
	DataDir      string   `toml:"data_dir"`
	AllowedPaths []string `toml:"allowed_paths"`
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Bind:    "127.0.0.1",
		Port:    7860,
		DataDir: filepath.Join(home, ".config", "tbox"),
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
