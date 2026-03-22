// internal/core/config_handler.go
package core

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/wake/tmux-box/internal/config"
)

// handleGetConfig returns the current config as JSON with the token field redacted.
func (c *Core) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	c.CfgMu.RLock()
	defer c.CfgMu.RUnlock()

	// Shallow copy to redact token; encode under lock to avoid slice race
	cfg := *c.Cfg
	cfg.Token = ""

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// configUpdateRequest defines the fields that can be updated via PUT /api/config.
type configUpdateRequest struct {
	Stream   *config.StreamConfig   `json:"stream,omitempty"`
	JSONL    *config.JSONLConfig    `json:"jsonl,omitempty"`
	Detect   *detectUpdateRequest   `json:"detect,omitempty"`
	Terminal *config.TerminalConfig `json:"terminal,omitempty"`
}

// detectUpdateRequest allows partial updates to detect config.
// Using pointers so we can distinguish "not provided" from "zero value".
type detectUpdateRequest struct {
	CCCommands   *[]string `json:"cc_commands,omitempty"`
	PollInterval *int      `json:"poll_interval,omitempty"`
}

// handlePutConfig accepts a partial config update, persists it to disk, and returns the updated config.
func (c *Core) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var req configUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	c.CfgMu.Lock()

	// Validate before mutating
	if req.Terminal != nil && req.Terminal.SizingMode != "" {
		switch req.Terminal.SizingMode {
		case "auto", "terminal-first", "minimal-first":
			// valid
		default:
			c.CfgMu.Unlock()
			http.Error(w, "invalid sizing_mode: must be auto, terminal-first, or minimal-first", http.StatusBadRequest)
			return
		}
	}

	detectChanged := false

	// Apply updates — only the allowed fields
	if req.Stream != nil {
		c.Cfg.Stream = *req.Stream
	}
	if req.JSONL != nil {
		c.Cfg.JSONL = *req.JSONL
	}
	if req.Detect != nil {
		if req.Detect.CCCommands != nil {
			c.Cfg.Detect.CCCommands = *req.Detect.CCCommands
			detectChanged = true
		}
		if req.Detect.PollInterval != nil && *req.Detect.PollInterval > 0 {
			c.Cfg.Detect.PollInterval = *req.Detect.PollInterval
		}
	}

	if req.Terminal != nil && req.Terminal.SizingMode != "" {
		c.Cfg.Terminal.SizingMode = req.Terminal.SizingMode
	}

	// Write back to config file
	if c.CfgPath != "" {
		if err := writeConfig(c.CfgPath, *c.Cfg); err != nil {
			c.CfgMu.Unlock()
			http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Return updated config (redacted)
	cfg := *c.Cfg
	c.CfgMu.Unlock()

	// Notify registered callbacks about config changes (outside lock)
	if detectChanged {
		c.NotifyConfigChange()
	}

	cfg.Token = ""
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// writeConfig serialises the config to TOML and writes it to the given path atomically.
func writeConfig(path string, cfg config.Config) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
