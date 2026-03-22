package core

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderTracker struct {
	calls []string
}

type fakeModule struct {
	name    string
	tracker *orderTracker
	initErr error
}

func (m *fakeModule) Name() string         { return m.name }
func (m *fakeModule) Dependencies() []string { return nil }
func (m *fakeModule) Init(c *Core) error {
	m.tracker.calls = append(m.tracker.calls, m.name+".Init")
	return m.initErr
}
func (m *fakeModule) RegisterRoutes(mux *http.ServeMux) {
	m.tracker.calls = append(m.tracker.calls, m.name+".RegisterRoutes")
}
func (m *fakeModule) Start(ctx context.Context) error {
	m.tracker.calls = append(m.tracker.calls, m.name+".Start")
	return nil
}
func (m *fakeModule) Stop(_ context.Context) error {
	m.tracker.calls = append(m.tracker.calls, m.name+".Stop")
	return nil
}

func TestCoreLifecycleOrder(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeModule{name: "a", tracker: tracker})
	c.AddModule(&fakeModule{name: "b", tracker: tracker})

	err := c.InitModules()
	require.NoError(t, err)
	c.RegisterRoutes(http.NewServeMux())
	err = c.StartModules(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{
		"a.Init", "b.Init",
		"a.RegisterRoutes", "b.RegisterRoutes",
		"a.Start", "b.Start",
	}, tracker.calls)
}

func TestCoreStopReverseOrder(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeModule{name: "a", tracker: tracker})
	c.AddModule(&fakeModule{name: "b", tracker: tracker})
	_ = c.InitModules()
	_ = c.StartModules(context.Background())

	tracker.calls = nil // reset
	err := c.StopModules(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"b.Stop", "a.Stop"}, tracker.calls)
}

func TestCoreInitErrorStops(t *testing.T) {
	tracker := &orderTracker{}
	c := New(CoreDeps{})
	c.AddModule(&fakeModule{name: "a", tracker: tracker, initErr: fmt.Errorf("boom")})
	c.AddModule(&fakeModule{name: "b", tracker: tracker})

	err := c.InitModules()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.Equal(t, []string{"a.Init"}, tracker.calls)
}
