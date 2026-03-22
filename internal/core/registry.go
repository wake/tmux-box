package core

import (
	"fmt"
	"sync"
)

// ServiceRegistry is a simple type-erased service locator.
type ServiceRegistry struct {
	mu       sync.RWMutex
	services map[string]any
}

// NewServiceRegistry creates an empty registry.
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{services: make(map[string]any)}
}

// Register stores a service under the given name.
func (r *ServiceRegistry) Register(name string, svc any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[name] = svc
}

// Get retrieves a service by name. Returns (nil, false) if not found.
func (r *ServiceRegistry) Get(name string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[name]
	return svc, ok
}

// MustGet retrieves a service by name, panicking if not found.
func (r *ServiceRegistry) MustGet(name string) any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[name]
	if !ok {
		panic(fmt.Sprintf("service %q not registered", name))
	}
	return svc
}
