package cc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

// newTestCoreWithSession creates a Core with a session module already initialised,
// ready for the CC module to depend on.
func newTestCoreWithSession(t *testing.T) (*core.Core, *tmux.FakeExecutor) {
	t.Helper()

	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })

	fake := tmux.NewFakeExecutor()
	cfg := &config.Config{
		Detect: config.DetectConfig{
			CCCommands: []string{"claude", "cld"},
		},
	}

	reg := core.NewServiceRegistry()
	c := core.New(core.CoreDeps{
		Config:   cfg,
		Tmux:     fake,
		Registry: reg,
	})

	// Init session module first (CC depends on it)
	sessMod := session.NewSessionModule(meta)
	require.NoError(t, sessMod.Init(c))

	return c, fake
}

func TestCCModule_RegistersDetector(t *testing.T) {
	c, _ := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	// Verify CCDetector is registered
	svc, ok := c.Registry.Get(DetectorKey)
	assert.True(t, ok, "CCDetector should be registered at %q", DetectorKey)

	_, isDetector := svc.(CCDetector)
	assert.True(t, isDetector, "registered service should implement CCDetector")
}

func TestCCModule_DetectorDelegates(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	// Set up fake tmux: "test:0" has claude running and is idle
	fake.SetPaneCommand("test:0", "claude")
	fake.SetPaneContent("test:0", "some output\n❯ ")

	mod := New()
	require.NoError(t, mod.Init(c))

	// Use the module as CCDetector
	status := mod.Detect("test:0")
	assert.Equal(t, detect.StatusCCIdle, status,
		"Detect should delegate to internal detector and return cc-idle")
}

func TestCCModule_DetectorDelegates_NotInCC(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	// Set up fake tmux: "test:0" has vim running (not CC)
	fake.SetPaneCommand("test:0", "vim")
	fake.SetPaneContent("test:0", "~\n~\n~")

	mod := New()
	require.NoError(t, mod.Init(c))

	status := mod.Detect("test:0")
	assert.Equal(t, detect.StatusNotInCC, status,
		"Detect should return not-in-cc for non-CC process")
}

func TestCCModule_DetectorDelegates_Normal(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	// Set up fake tmux: "test:0" is at shell prompt
	fake.SetPaneCommand("test:0", "zsh")

	mod := New()
	require.NoError(t, mod.Init(c))

	status := mod.Detect("test:0")
	assert.Equal(t, detect.StatusNormal, status,
		"Detect should return normal for shell process")
}

func TestCCModule_ConfigChangeUpdatesDetector(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	// Initially "claude" and "cld" are configured as CC commands.
	// "my-claude" is NOT a CC command, so it should be detected as not-in-cc.
	// Use pane content without CC UI signatures to avoid fallback detection.
	fake.SetPaneCommand("test:0", "my-claude")
	fake.SetPaneContent("test:0", "some output\n$ ")
	status := mod.Detect("test:0")
	assert.Equal(t, detect.StatusNotInCC, status,
		"my-claude should not be detected as CC before config change")

	// Update config to include "my-claude"
	c.Cfg.Detect.CCCommands = []string{"claude", "cld", "my-claude"}

	// Trigger all registered OnConfigChange callbacks
	// (simulates what handlePutConfig does)
	c.NotifyConfigChange()

	// Now "my-claude" should be detected as CC.
	// Update pane content to show CC idle prompt for sub-state detection.
	fake.SetPaneContent("test:0", "some output\n❯ ")
	status = mod.Detect("test:0")
	assert.Equal(t, detect.StatusCCIdle, status,
		"my-claude should be detected as CC after config change")
}

func TestCCModule_NameAndDependencies(t *testing.T) {
	mod := New()
	assert.Equal(t, "cc", mod.Name())
	assert.Equal(t, []string{"session"}, mod.Dependencies())
}

func TestCCModule_HistoryNotRegistered(t *testing.T) {
	c, _ := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	// HistoryKey should NOT be registered yet (Task 8)
	_, ok := c.Registry.Get(HistoryKey)
	assert.False(t, ok, "CCHistoryProvider should not be registered yet")

	// OperatorKey SHOULD be registered (Task 7)
	svc, ok := c.Registry.Get(OperatorKey)
	assert.True(t, ok, "CCOperator should be registered")
	_, isOp := svc.(CCOperator)
	assert.True(t, isOp, "registered service should implement CCOperator")
}
