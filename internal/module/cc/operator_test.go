package cc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/tmux"
)

func TestInterrupt_Success(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	// Start with CC running (busy)
	fake.SetPaneCommand(target, "claude")
	fake.SetPaneContent(target, "Running tool...")

	// After a short delay, simulate CC becoming idle
	go func() {
		time.Sleep(300 * time.Millisecond)
		fake.SetPaneContent(target, "some output\n❯ ")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := mod.Interrupt(ctx, target)
	assert.NoError(t, err)

	// Verify C-u and C-c were sent
	rawKeys := fake.RawKeysSent()
	assert.GreaterOrEqual(t, len(rawKeys), 2)
	assert.Equal(t, tmux.RawKeysCall{Target: target, Keys: []string{"C-u"}}, rawKeys[0])
	assert.Equal(t, tmux.RawKeysCall{Target: target, Keys: []string{"C-c"}}, rawKeys[1])
}

func TestInterrupt_Timeout(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	// CC is running and never becomes idle
	fake.SetPaneCommand(target, "claude")
	fake.SetPaneContent(target, "Running tool...")

	// Use a short context deadline — the operator relies on ctx for timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := mod.Interrupt(ctx, target)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestInterrupt_ContextCancelled(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	fake.SetPaneCommand(target, "claude")
	fake.SetPaneContent(target, "Running tool...")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	err := mod.Interrupt(ctx, target)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestExit_Success(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	// Start with CC idle
	fake.SetPaneCommand(target, "claude")
	fake.SetPaneContent(target, "some output\n❯ ")

	// After a short delay, simulate CC exiting to normal shell
	go func() {
		time.Sleep(800 * time.Millisecond)
		fake.SetPaneCommand(target, "zsh")
		fake.SetPaneContent(target, "$ ")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := mod.Exit(ctx, target)
	assert.NoError(t, err)

	// Verify /exit was sent via SendKeys
	keysCalls := fake.KeysSent()
	found := false
	for _, call := range keysCalls {
		if call.Target == target && call.Keys == "/exit" {
			found = true
			break
		}
	}
	assert.True(t, found, "SendKeys should have been called with /exit")
}

func TestExit_Timeout(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	// CC is idle but never exits
	fake.SetPaneCommand(target, "claude")
	fake.SetPaneContent(target, "some output\n❯ ")

	// Use a short context deadline — the operator relies on ctx for timeout.
	// The pane preparation sleeps will consume most of this budget.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := mod.Exit(ctx, target)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestGetStatus_Success(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	fake.SetPaneCommand(target, "claude")
	// Pane is large enough (default 80x24 in fake), no resize needed.
	// Set pane content with session ID and cwd.
	statusContent := `Session ID: 01234567-abcd-ef01-2345-6789abcdef01
  cwd: /Users/wake/projects/myapp
  model: claude-sonnet-4-20250514
❯ `
	fake.SetPaneContent(target, statusContent)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := mod.GetStatus(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "01234567-abcd-ef01-2345-6789abcdef01", info.SessionID)
	assert.Equal(t, "/Users/wake/projects/myapp", info.Cwd)

	// Verify staged /status send: "/" then "status" then Enter
	rawKeys := fake.RawKeysSent()
	var foundSlash, foundStatus, foundEnter bool
	for _, call := range rawKeys {
		if call.Target == target {
			if len(call.Keys) == 2 && call.Keys[0] == "-l" && call.Keys[1] == "/" {
				foundSlash = true
			}
			if len(call.Keys) == 2 && call.Keys[0] == "-l" && call.Keys[1] == "status" {
				foundStatus = true
			}
			if len(call.Keys) == 1 && call.Keys[0] == "Enter" {
				foundEnter = true
			}
		}
	}
	assert.True(t, foundSlash, "should send / via SendKeysRaw -l")
	assert.True(t, foundStatus, "should send status via SendKeysRaw -l")
	assert.True(t, foundEnter, "should send Enter via SendKeysRaw")
}

func TestGetStatus_SmallPaneResizes(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	fake.SetPaneCommand(target, "claude")
	// Set a small pane size to trigger resize
	fake.SetPaneSize(target, 40, 10)

	statusContent := `Session ID: 01234567-abcd-ef01-2345-6789abcdef01
  cwd: /tmp
❯ `
	fake.SetPaneContent(target, statusContent)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := mod.GetStatus(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, "01234567-abcd-ef01-2345-6789abcdef01", info.SessionID)

	// Verify resize was called (fake stores resize in paneSizes)
	sz, ok := fake.PaneSizeOf(target)
	assert.True(t, ok)
	assert.Equal(t, [2]int{80, 24}, sz, "pane should have been resized to 80x24")

	// Verify RestoreWindowSizing was called (ResizeWindowAuto + SetWindowOption)
	assert.Contains(t, fake.AutoResizeCalls(), target,
		"ResizeWindowAuto should be called for restore")
	optCalls := fake.SetWindowOptionCalls()
	found := false
	for _, call := range optCalls {
		if call.Target == target && call.Option == "window-size" && call.Value == "latest" {
			found = true
			break
		}
	}
	assert.True(t, found, "SetWindowOption should be called with window-size=latest")
}

func TestGetStatus_NoSessionID(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	fake.SetPaneCommand(target, "claude")
	// Pane content without a session ID
	fake.SetPaneContent(target, "some random content\n❯ ")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := mod.GetStatus(ctx, target)
	assert.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "could not extract session ID")
}

func TestLaunch_Success(t *testing.T) {
	c, fake := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	target := "test:0"
	cmd := "tbox relay --session myapp --daemon ws://127.0.0.1:7860 --token-file /tmp/token -- claude -p --resume abc123"

	ctx := context.Background()
	err := mod.Launch(ctx, target, cmd)
	assert.NoError(t, err)

	// Verify SendKeys was called with the correct command
	keysCalls := fake.KeysSent()
	require.Len(t, keysCalls, 1)
	assert.Equal(t, target, keysCalls[0].Target)
	assert.Equal(t, cmd, keysCalls[0].Keys)
}

func TestOperator_RegisteredInInit(t *testing.T) {
	c, _ := newTestCoreWithSession(t)

	mod := New()
	require.NoError(t, mod.Init(c))

	// Verify CCOperator is registered
	svc, ok := c.Registry.Get(OperatorKey)
	assert.True(t, ok, "CCOperator should be registered at %q", OperatorKey)

	_, isOperator := svc.(CCOperator)
	assert.True(t, isOperator, "registered service should implement CCOperator")
}
