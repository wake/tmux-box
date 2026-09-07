package fs

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

// fakeSessionProvider is a stub implementation of session.SessionProvider for
// fs module tests that need to resolve session-cwd capability roots.
type fakeSessionProvider struct {
	sessions map[string]*session.SessionInfo
}

func newFakeSessionProvider() *fakeSessionProvider {
	return &fakeSessionProvider{sessions: map[string]*session.SessionInfo{}}
}

func (f *fakeSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	out := make([]session.SessionInfo, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, *s)
	}
	return out, nil
}

func (f *fakeSessionProvider) GetSession(code string) (*session.SessionInfo, error) {
	if s, ok := f.sessions[code]; ok {
		return s, nil
	}
	return nil, nil
}

func (f *fakeSessionProvider) UpdateMeta(_ string, _ session.MetaUpdate) error { return nil }

func (f *fakeSessionProvider) HandleTerminalWS(_ http.ResponseWriter, _ *http.Request, _ string) {
}

func (f *fakeSessionProvider) TmuxInstance() string { return "" }

func (f *fakeSessionProvider) setCwd(code, cwd string) {
	f.sessions[code] = &session.SessionInfo{Code: code, Cwd: cwd, Exists: true}
}

func newTestFsModule(t *testing.T, provider session.SessionProvider) *FsModule {
	t.Helper()
	c := core.New(core.CoreDeps{
		Registry: core.NewServiceRegistry(),
	})
	if provider != nil {
		c.Registry.Register(session.RegistryKey, provider)
	}
	m := New()
	require.NoError(t, m.Init(c))
	return m
}

func TestFsModule_InitGetsSessionProvider(t *testing.T) {
	provider := newFakeSessionProvider()
	m := newTestFsModule(t, provider)
	require.NotNil(t, m.sessions, "sessions provider should be set after Init")
	assert.Same(t, session.SessionProvider(provider), m.sessions)
}

func TestFsModule_DependenciesIncludeSession(t *testing.T) {
	m := New()
	deps := m.Dependencies()
	assert.Contains(t, deps, "session", "fs depends on session for capability root resolution")
}
