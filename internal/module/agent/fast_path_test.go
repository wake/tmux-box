package agent

import (
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/module/session"
)

// Compile-time guard: the production *session.SessionModule must satisfy the
// unexported sessionCodeLookuper interface. If Task A's method signature ever
// drifts (e.g. someone changes the return shape), this fails to compile and
// surfaces the regression at build time instead of in production hot path.
var _ sessionCodeLookuper = (*session.SessionModule)(nil)

// fakeFastSessionProvider implements both session.SessionProvider AND
// LookupCodeByName, mirroring the production *session.SessionModule. Counters
// let tests assert which code path resolveSessionCode took: the fast-path
// branch must call LookupCodeByName and skip ListSessions; only a fast-path
// miss is allowed to fall through to ListSessions.
type fakeFastSessionProvider struct {
	sessions     []session.SessionInfo
	lookup       map[string]string
	listCalls    int
	lookupCalls  int
	lookupHits   int
	tmuxInstance string
}

func (f *fakeFastSessionProvider) ListSessions() ([]session.SessionInfo, error) {
	f.listCalls++
	return f.sessions, nil
}

func (f *fakeFastSessionProvider) GetSession(code string) (*session.SessionInfo, error) {
	for _, s := range f.sessions {
		if s.Code == code {
			return &s, nil
		}
	}
	return nil, nil
}

func (f *fakeFastSessionProvider) UpdateMeta(string, session.MetaUpdate) error { return nil }
func (f *fakeFastSessionProvider) HandleTerminalWS(http.ResponseWriter, *http.Request, string) {
}

func (f *fakeFastSessionProvider) TmuxInstance() string { return f.tmuxInstance }

// LookupCodeByName satisfies the unexported sessionCodeLookuper interface in
// the agent package. Counts every call so tests can assert hit / miss.
func (f *fakeFastSessionProvider) LookupCodeByName(name string) (string, bool) {
	f.lookupCalls++
	if f.lookup == nil {
		return "", false
	}
	code, ok := f.lookup[name]
	if ok {
		f.lookupHits++
	}
	return code, ok
}

// TestResolveSessionCode_UsesFastPathWhenAvailable verifies the entire point of
// the optimization: when the provider exposes LookupCodeByName, the hot path
// MUST hit the cache and skip ListSessions (which fans out 1+7×S tmux
// subprocesses per call).
func TestResolveSessionCode_UsesFastPathWhenAvailable(t *testing.T) {
	m := &Module{}
	fake := &fakeFastSessionProvider{
		// ListSessions returns data too — this lets us prove via listCalls==0
		// that the fast path bypassed it, rather than relying on an empty list
		// that could mask incorrect fall-through.
		sessions: []session.SessionInfo{{Name: "foo", Code: "slow-foo"}},
		lookup:   map[string]string{"foo": "code-foo"},
	}
	m.sessions = fake

	got := m.resolveSessionCode("foo")

	if got != "code-foo" {
		t.Errorf("resolveSessionCode = %q; want %q (fast-path code)", got, "code-foo")
	}
	if fake.lookupCalls != 1 {
		t.Errorf("LookupCodeByName calls = %d; want 1", fake.lookupCalls)
	}
	if fake.listCalls != 0 {
		t.Errorf("ListSessions calls = %d; want 0 (fast-path must skip slow path)", fake.listCalls)
	}
}

// TestResolveSessionCode_FallsBackOnFastPathMiss covers the safety-net branch:
// if the cache reports the name as unknown (recently created/renamed session,
// cache invalidation race), the slow path runs once and returns the canonical
// answer. Critical for correctness — fast path must never silently drop.
func TestResolveSessionCode_FallsBackOnFastPathMiss(t *testing.T) {
	m := &Module{}
	fake := &fakeFastSessionProvider{
		sessions: []session.SessionInfo{{Name: "missing", Code: "slow-code"}},
		lookup:   map[string]string{}, // empty → every lookup misses
	}
	m.sessions = fake

	got := m.resolveSessionCode("missing")

	if got != "slow-code" {
		t.Errorf("resolveSessionCode = %q; want %q (fallback code)", got, "slow-code")
	}
	if fake.lookupCalls != 1 {
		t.Errorf("LookupCodeByName calls = %d; want 1", fake.lookupCalls)
	}
	if fake.listCalls != 1 {
		t.Errorf("ListSessions calls = %d; want 1 (fallback should fire once)", fake.listCalls)
	}
}

// TestResolveSessionCode_FallsBackWhenNoFastPathInterface guarantees backward
// compatibility: providers that don't implement LookupCodeByName (e.g. the
// existing fakeSessionProvider used by ~20 other tests) keep working through
// the slow path. If this regressed, the entire suite would break.
func TestResolveSessionCode_FallsBackWhenNoFastPathInterface(t *testing.T) {
	m := &Module{}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Name: "alpha", Code: "alpha-code"}},
	}

	got := m.resolveSessionCode("alpha")

	if got != "alpha-code" {
		t.Errorf("resolveSessionCode = %q; want %q", got, "alpha-code")
	}
}
