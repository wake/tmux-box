package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/tmux"
)

// Module is the interface all daemon modules implement.
type Module interface {
	Name() string
	Init(core *Core) error
	RegisterRoutes(mux *http.ServeMux)
	Start(ctx context.Context) error
	Stop() error
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
	Tmux     tmux.Executor
	Registry *ServiceRegistry
	modules  []Module
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
	}
}

// AddModule appends a module to the lifecycle.
func (c *Core) AddModule(m Module) {
	c.modules = append(c.modules, m)
}

// InitModules calls Init on each module in registration order.
func (c *Core) InitModules() error {
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
func (c *Core) StopModules() error {
	var errs []error
	for i := len(c.modules) - 1; i >= 0; i-- {
		if err := c.modules[i].Stop(); err != nil {
			errs = append(errs, fmt.Errorf("module %s stop: %w", c.modules[i].Name(), err))
		}
	}
	return errors.Join(errs...)
}
