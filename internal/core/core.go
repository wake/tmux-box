package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/tmux"
)

// Module is the interface all daemon modules implement.
type Module interface {
	Name() string
	Dependencies() []string
	Init(core *Core) error
	RegisterRoutes(mux *http.ServeMux)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// CoreDeps holds the shared infrastructure injected into Core.
type CoreDeps struct {
	Config   *config.Config
	Tmux     tmux.Executor
	Registry *ServiceRegistry
}

// Core holds shared infrastructure and manages module lifecycle.
type Core struct {
	Cfg      *config.Config
	CfgMu   sync.RWMutex // protects Cfg
	CfgPath  string       // path to config.toml for persistence
	Tmux     tmux.Executor
	Registry *ServiceRegistry
	Events   *EventsBroadcaster
	modules  []Module
	onConfigChange []func() // config change callbacks
}

// New creates a Core from the given dependencies.
func New(deps CoreDeps) *Core {
	reg := deps.Registry
	if reg == nil {
		reg = NewServiceRegistry()
	}
	return &Core{
		Cfg:      deps.Config,
		Tmux:     deps.Tmux,
		Registry: reg,
		Events:   NewEventsBroadcaster(),
	}
}

// AddModule appends a module to the lifecycle.
func (c *Core) AddModule(m Module) {
	c.modules = append(c.modules, m)
}

// InitModules sorts modules by dependency order, then calls Init on each.
func (c *Core) InitModules() error {
	sorted, err := topoSort(c.modules)
	if err != nil {
		return fmt.Errorf("dependency sort: %w", err)
	}
	c.modules = sorted

	for _, m := range c.modules {
		if err := m.Init(c); err != nil {
			return fmt.Errorf("module %s init: %w", m.Name(), err)
		}
	}
	return nil
}

// RegisterRoutes calls RegisterRoutes on each module in registration order.
func (c *Core) RegisterRoutes(mux *http.ServeMux) {
	for _, m := range c.modules {
		m.RegisterRoutes(mux)
	}
}

// StartModules calls Start on each module in registration order.
func (c *Core) StartModules(ctx context.Context) error {
	for _, m := range c.modules {
		if err := m.Start(ctx); err != nil {
			return fmt.Errorf("module %s start: %w", m.Name(), err)
		}
	}
	return nil
}

// StopModules calls Stop on each module in reverse registration order.
// All modules are stopped even if some return errors.
func (c *Core) StopModules(ctx context.Context) error {
	var errs []error
	for i := len(c.modules) - 1; i >= 0; i-- {
		if err := c.modules[i].Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("module %s stop: %w", c.modules[i].Name(), err))
		}
	}
	return errors.Join(errs...)
}

// OnConfigChange registers a callback invoked after config is updated via PUT.
func (c *Core) OnConfigChange(fn func()) {
	c.onConfigChange = append(c.onConfigChange, fn)
}

// NotifyConfigChange invokes all registered config change callbacks.
func (c *Core) NotifyConfigChange() {
	for _, fn := range c.onConfigChange {
		fn()
	}
}

// RegisterCoreRoutes registers routes owned by Core itself (not by modules).
func (c *Core) RegisterCoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/session-events", c.Events.HandleSessionEvents)
	mux.HandleFunc("GET /api/config", c.handleGetConfig)
	mux.HandleFunc("PUT /api/config", c.handlePutConfig)
}
