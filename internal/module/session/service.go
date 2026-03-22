package session

import (
	"log"
	"net/http"
	"time"

	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/terminal"
)

// --- SessionProvider implementation ---

// ListSessions returns all live tmux sessions merged with cached meta.
func (m *SessionModule) ListSessions() ([]SessionInfo, error) {
	sessions, err := m.tmux.ListSessions()
	if err != nil {
		return nil, err
	}

	// Build live ID set for orphan cleanup
	liveIDs := make([]string, len(sessions))
	for i, s := range sessions {
		liveIDs[i] = s.ID
	}
	if _, err := m.meta.CleanOrphans(liveIDs); err != nil {
		return nil, err
	}

	result := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		code, err := EncodeSessionID(s.ID)
		if err != nil {
			continue // skip sessions with invalid IDs
		}
		info := SessionInfo{
			Code:   code,
			TmuxID: s.ID,
			Name:   s.Name,
			Exists: true,
			Mode:   "term", // default
			Cwd:    s.Cwd,
		}

		// Merge meta from DB (Cwd always comes from tmux — SOT)
		meta, err := m.meta.GetMeta(s.ID)
		if err != nil {
			return nil, err
		}
		if meta != nil {
			info.Mode = meta.Mode
			info.CCSessionID = meta.CCSessionID
			info.CCModel = meta.CCModel
		}

		result = append(result, info)
	}

	return result, nil
}

// GetSession returns a single session by its code, or nil if not found.
func (m *SessionModule) GetSession(code string) (*SessionInfo, error) {
	tmuxID, err := DecodeSessionID(code)
	if err != nil {
		return nil, nil // invalid code → not found
	}

	sessions, err := m.tmux.ListSessions()
	if err != nil {
		return nil, err
	}

	for _, s := range sessions {
		if s.ID == tmuxID {
			info := &SessionInfo{
				Code:   code,
				TmuxID: s.ID,
				Name:   s.Name,
				Exists: true,
				Mode:   "term",
				Cwd:    s.Cwd,
			}

			// Merge meta from DB (Cwd always comes from tmux — SOT)
			meta, err := m.meta.GetMeta(s.ID)
			if err != nil {
				return nil, err
			}
			if meta != nil {
				info.Mode = meta.Mode
				info.CCSessionID = meta.CCSessionID
				info.CCModel = meta.CCModel
			}

			return info, nil
		}
	}

	// Not found in tmux — clean up orphan meta
	_ = m.meta.DeleteMeta(tmuxID)
	return nil, nil
}

// UpdateMeta performs a partial meta update for the session identified by code.
func (m *SessionModule) UpdateMeta(code string, update MetaUpdate) error {
	tmuxID, err := DecodeSessionID(code)
	if err != nil {
		return err
	}

	storeUpdate := store.MetaUpdate{
		Mode:        update.Mode,
		CCSessionID: update.CCSessionID,
		CCModel:     update.CCModel,
		Cwd:         update.Cwd,
	}

	return m.meta.UpdateMeta(tmuxID, storeUpdate)
}

// HandleTerminalWS attaches a WebSocket connection to the tmux session PTY relay.
func (m *SessionModule) HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string) {
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Determine sizing mode from config (default to "auto" if no config).
	sizingMode := "auto"
	if m.core != nil && m.core.Cfg != nil {
		sizingMode = m.core.Cfg.Terminal.GetSizingMode()
	}

	// Build tmux attach-session command and args.
	target := info.Name
	args := []string{"attach-session", "-t", target}
	if sizingMode == "terminal-first" {
		args = append(args, "-f", "ignore-size")
	}

	relay := terminal.NewRelay("tmux", args, "/")

	switch sizingMode {
	case "terminal-first":
		// no OnStart — relay uses -f ignore-size, sizing handled by terminal
	case "minimal-first":
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				if err := m.tmux.ResizeWindowAuto(target); err != nil {
					log.Printf("HandleTerminalWS: ResizeWindowAuto(%s): %v", target, err)
				}
				if err := m.tmux.SetWindowOption(target, "window-size", "smallest"); err != nil {
					log.Printf("HandleTerminalWS: SetWindowOption(%s): %v", target, err)
				}
			}()
		}
	default:
		if sizingMode != "auto" && sizingMode != "" {
			log.Printf("HandleTerminalWS: unknown sizing_mode %q, falling back to auto", sizingMode)
		}
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				if err := m.tmux.ResizeWindowAuto(target); err != nil {
					log.Printf("HandleTerminalWS: ResizeWindowAuto(%s): %v", target, err)
				}
				if err := m.tmux.SetWindowOption(target, "window-size", "latest"); err != nil {
					log.Printf("HandleTerminalWS: SetWindowOption(%s): %v", target, err)
				}
			}()
		}
	}

	relay.HandleWebSocket(w, r)
}
