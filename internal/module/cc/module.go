package cc

import (
	"context"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/module/session"
)

// CCModule groups Claude Code-related functionality: detection, history, and operations.
type CCModule struct {
	core     *core.Core
	detector *detect.Detector
	sessions session.SessionProvider
}

// New creates a new CCModule.
func New() *CCModule { return &CCModule{} }

func (m *CCModule) Name() string          { return "cc" }
func (m *CCModule) Dependencies() []string { return []string{"session"} }

func (m *CCModule) Init(c *core.Core) error {
	m.core = c
	m.detector = detect.New(c.Tmux, c.Cfg.Detect.CCCommands)
	m.sessions = c.Registry.MustGet(session.RegistryKey).(session.SessionProvider)

	// Register CCDetector
	c.Registry.Register(DetectorKey, CCDetector(m))

	// Listen for config changes to update detector commands
	c.OnConfigChange(func() {
		m.detector.UpdateCommands(c.Cfg.Detect.CCCommands)
	})

	return nil
}

func (m *CCModule) RegisterRoutes(mux *http.ServeMux) {
	// History route will be added in Task 8
}

func (m *CCModule) Start(ctx context.Context) error {
	// Status poller will be added in Task 8
	return nil
}

func (m *CCModule) Stop(_ context.Context) error {
	return nil
}
