package cc

import (
	"context"

	"github.com/wake/tmux-box/internal/detect"
)

const (
	DetectorKey = "cc.detector"
	HistoryKey  = "cc.history"
	OperatorKey = "cc.operator"
)

// CCDetector detects Claude Code status in a tmux pane.
type CCDetector interface {
	Detect(tmuxTarget string) detect.Status
}

// CCHistoryProvider retrieves CC conversation history.
type CCHistoryProvider interface {
	GetHistory(cwd string, ccSessionID string) ([]map[string]any, error)
}

// CCOperator performs atomic CC operations (exit, launch, interrupt, status).
type CCOperator interface {
	Exit(ctx context.Context, tmuxTarget string) error
	Launch(ctx context.Context, tmuxTarget string, cmd string) error
	Interrupt(ctx context.Context, tmuxTarget string) error
	GetStatus(ctx context.Context, tmuxTarget string) (*detect.StatusInfo, error)
}
