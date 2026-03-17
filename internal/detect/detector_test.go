package detect

import (
	"testing"

	"github.com/wake/tmux-box/internal/tmux"
)

func TestDetectStatus(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	d := New(fake, []string{"claude", "cld"})

	tests := []struct {
		name     string
		cmd      string
		content  string
		expected Status
	}{
		{"shell idle", "zsh", "", StatusNormal},
		{"non-cc command", "node", "", StatusNotInCC},
		{"cc idle", "claude", "❯ ", StatusCCIdle},
		{"cc running", "claude", "⠋ Reading file...", StatusCCRunning},
		{"cc waiting permission", "claude", "Allow  Deny", StatusCCWaiting},
		{"cc alias idle", "cld", "❯ ", StatusCCIdle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.SetPaneCommand("test", tt.cmd)
			fake.SetPaneContent("test", tt.content)
			status := d.Detect("test")
			if status != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, status)
			}
		})
	}
}
