package cc

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/tmux"
)

// Interrupt sends C-u + C-c and waits for CC to reach idle state.
func (m *CCModule) Interrupt(ctx context.Context, tmuxTarget string) error {
	tx := m.core.Tmux
	if err := tx.SendKeysRaw(tmuxTarget, "C-u"); err != nil {
		return fmt.Errorf("send C-u: %w", err)
	}
	if err := tx.SendKeysRaw(tmuxTarget, "C-c"); err != nil {
		return fmt.Errorf("send C-c: %w", err)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if m.detector.Detect(tmuxTarget) == detect.StatusCCIdle {
				return nil
			}
		}
	}
}

// Exit prepares the pane, sends /exit, and waits for CC to exit (StatusNormal).
func (m *CCModule) Exit(ctx context.Context, tmuxTarget string) error {
	tx := m.core.Tmux

	// Prepare pane: exit copy-mode, escape, clear
	if err := tx.SendKeysRaw(tmuxTarget, "-X", "cancel"); err != nil {
		log.Printf("cc: Exit pane-prep cancel (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "Escape"); err != nil {
		log.Printf("cc: Exit pane-prep Escape (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeysRaw(tmuxTarget, "C-c"); err != nil {
		log.Printf("cc: Exit pane-prep C-c (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Send /exit
	if err := tx.SendKeysRaw(tmuxTarget, "Escape"); err != nil {
		log.Printf("cc: Exit pane-prep Escape2 (%s): %v", tmuxTarget, err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tx.SendKeys(tmuxTarget, "/exit"); err != nil {
		return fmt.Errorf("send /exit: %w", err)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if m.detector.Detect(tmuxTarget) == detect.StatusNormal {
				return nil
			}
		}
	}
}

// GetStatus sends /status to CC, captures pane content, and parses session info.
// Handles pane resize if too small, restores sizing after.
func (m *CCModule) GetStatus(ctx context.Context, tmuxTarget string) (*detect.StatusInfo, error) {
	tx := m.core.Tmux

	// Check pane size — resize if too small for /status TUI
	didManualResize := false
	if cols, rows, err := tx.PaneSize(tmuxTarget); err == nil && (cols < 80 || rows < 24) {
		if err := tx.ResizeWindow(tmuxTarget, 80, 24); err != nil {
			return nil, fmt.Errorf("resize pane: %w", err)
		}
		didManualResize = true
		sleepCtx(ctx, 200*time.Millisecond)
	}

	// defer cleanup instead of repeating the check at every return point
	if didManualResize {
		defer restoreWindowSizing(tx, tmuxTarget)
	}

	// Staged /status send
	if err := tx.SendKeysRaw(tmuxTarget, "-l", "/"); err != nil {
		return nil, fmt.Errorf("send /: %w", err)
	}
	sleepCtx(ctx, 1*time.Second)
	if err := tx.SendKeysRaw(tmuxTarget, "-l", "status"); err != nil {
		return nil, fmt.Errorf("send status: %w", err)
	}
	sleepCtx(ctx, 500*time.Millisecond)
	if err := tx.SendKeysRaw(tmuxTarget, "Enter"); err != nil {
		return nil, fmt.Errorf("send Enter: %w", err)
	}

	// Poll capture-pane for status info
	var statusInfo detect.StatusInfo
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		sleepCtx(ctx, 500*time.Millisecond)
		if ctx.Err() != nil {
			break
		}
		paneContent, err := tx.CapturePaneContent(tmuxTarget, 200)
		if err != nil {
			lastErr = err
			continue
		}
		info, err := detect.ExtractStatusInfo(paneContent)
		if err == nil {
			statusInfo = info
			break
		}
		lastErr = err
	}

	if statusInfo.SessionID == "" {
		if lastErr != nil {
			return nil, fmt.Errorf("could not extract session ID: %w", lastErr)
		}
		return nil, fmt.Errorf("could not extract session ID")
	}
	return &statusInfo, nil
}

// Launch sends a command string to the tmux pane via SendKeys.
func (m *CCModule) Launch(ctx context.Context, tmuxTarget string, cmd string) error {
	return m.core.Tmux.SendKeys(tmuxTarget, cmd)
}

// restoreWindowSizing clears manual window-size and restores automatic sizing.
func restoreWindowSizing(tx tmux.Executor, target string) {
	if err := tx.ResizeWindowAuto(target); err != nil {
		log.Printf("restoreWindowSizing: ResizeWindowAuto(%s): %v", target, err)
	}
	if err := tx.SetWindowOption(target, "window-size", "latest"); err != nil {
		log.Printf("restoreWindowSizing: SetWindowOption(%s): %v", target, err)
	}
}

// sleepCtx sleeps for the given duration or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
