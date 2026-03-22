package cc

import "github.com/wake/tmux-box/internal/detect"

// Detect implements CCDetector by delegating to the internal detector.
func (m *CCModule) Detect(tmuxTarget string) detect.Status {
	return m.detector.Detect(tmuxTarget)
}
