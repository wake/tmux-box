// internal/stream/manager.go
package stream

import (
	"fmt"
	"sync"
)

// Manager tracks multiple StreamSessions by name.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*StreamSession
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*StreamSession)}
}

// Start creates and tracks a new stream session.
func (m *Manager) Start(name, command string, args []string, cwd string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[name]; exists {
		return fmt.Errorf("stream session %q already running", name)
	}

	s, err := NewSession(command, args, cwd)
	if err != nil {
		return fmt.Errorf("start stream %q: %w", name, err)
	}

	m.sessions[name] = s

	// Auto-remove when process exits
	go func() {
		<-s.Done()
		m.mu.Lock()
		delete(m.sessions, name)
		m.mu.Unlock()
	}()

	return nil
}

// Get returns the session by name, or nil.
func (m *Manager) Get(name string) *StreamSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[name]
}

// Has checks if a session exists.
func (m *Manager) Has(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[name]
	return ok
}

// Stop terminates a specific session.
func (m *Manager) Stop(name string) {
	m.mu.Lock()
	s, ok := m.sessions[name]
	if ok {
		delete(m.sessions, name)
	}
	m.mu.Unlock()

	if s != nil {
		s.Stop()
	}
}

// StopAll terminates all sessions.
func (m *Manager) StopAll() {
	m.mu.Lock()
	sessions := make(map[string]*StreamSession, len(m.sessions))
	for k, v := range m.sessions {
		sessions[k] = v
	}
	m.sessions = make(map[string]*StreamSession)
	m.mu.Unlock()

	for _, s := range sessions {
		s.Stop()
	}
}
