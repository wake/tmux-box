package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// --- Provider method tests ---

func TestListSessionsMergesMeta(t *testing.T) {
	mod, meta, fake := newTestModule(t)

	fake.AddSession("dev", "/home/dev")
	fake.AddSession("prod", "/home/prod")

	// Set meta for first session only
	require.NoError(t, meta.SetMeta("$0", store.SessionMeta{
		TmuxID:  "$0",
		Mode:    "stream",
		CCModel: "opus",
	}))

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// First session should have merged meta
	assert.Equal(t, "dev", sessions[0].Name)
	assert.Equal(t, "stream", sessions[0].Mode)
	assert.Equal(t, "opus", sessions[0].CCModel)
	assert.NotEmpty(t, sessions[0].Code)

	// Second session should have default mode
	assert.Equal(t, "prod", sessions[1].Name)
	assert.Equal(t, "terminal", sessions[1].Mode)
	assert.NotEmpty(t, sessions[1].Code)
}

func TestListSessionsIncludesPaneTitleMetadata(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("dev", "/home/dev")
	fake.SetActivePaneMetadata("dev", tmux.TmuxPaneMetadata{
		SessionID:          "$0",
		SessionName:        "dev",
		WindowID:           "@1",
		PaneID:             "%2",
		PaneTitle:          "Planning",
		WindowName:         "editor",
		PaneCurrentCommand: "claude",
	})

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "Planning", sessions[0].PaneTitle)
	assert.Equal(t, "editor", sessions[0].WindowName)
	assert.Equal(t, "claude", sessions[0].CurrentCommand)
}

func TestListSessionsContinuesWhenPaneMetadataFails(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("dev", "/home/dev")
	fake.SetActivePaneMetadataError("dev", errors.New("display-message failed"))

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "dev", sessions[0].Name)
	assert.Empty(t, sessions[0].PaneTitle)
	assert.Empty(t, sessions[0].WindowName)
	assert.Empty(t, sessions[0].CurrentCommand)
}

func TestListSessionsUsesActivePaneTitleForMultiPaneSession(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("multi", "/workspace")
	fake.SetPaneCommand("multi:0.0", "wrong-pane-command")
	fake.SetActivePaneMetadata("multi", tmux.TmuxPaneMetadata{
		SessionID:          "$0",
		SessionName:        "multi",
		WindowID:           "@active",
		PaneID:             "%active",
		PaneTitle:          "active pane title",
		WindowName:         "active window",
		PaneCurrentCommand: "active-command",
	})

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "active pane title", sessions[0].PaneTitle)
	assert.Equal(t, "active window", sessions[0].WindowName)
	assert.Equal(t, "active-command", sessions[0].CurrentCommand)
	assert.Zero(t, fake.PaneCommandCallCount("multi:0.0"), "session list should not merge arbitrary list-panes data")
}

func TestListSessionsCleansOrphans(t *testing.T) {
	mod, meta, fake := newTestModule(t)

	fake.AddSession("alive", "/tmp")

	// Create orphan meta for a session that doesn't exist in tmux
	require.NoError(t, meta.SetMeta("$99", store.SessionMeta{
		TmuxID: "$99",
		Mode:   "stream",
	}))

	// ListSessions triggers orphan cleanup
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)

	// Orphan should be cleaned
	orphan, err := meta.GetMeta("$99")
	require.NoError(t, err)
	assert.Nil(t, orphan, "orphan meta should be deleted")
}

func TestGetSessionByCode(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("my-session", "/home/test")

	// Get the code from ListSessions
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	info, err := mod.GetSession(code)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "my-session", info.Name)
	assert.Equal(t, code, info.Code)
}

func TestGetSessionNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)

	info, err := mod.GetSession("zzzzzz")
	require.NoError(t, err)
	assert.Nil(t, info)
}

func TestUpdateMeta(t *testing.T) {
	mod, meta, fake := newTestModule(t)

	fake.AddSession("work", "/home/work")

	// Ensure meta exists first
	require.NoError(t, meta.SetMeta("$0", store.SessionMeta{
		TmuxID: "$0",
		Mode:   "terminal",
	}))

	// Get code
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	// Update mode via provider
	mode := "stream"
	err = mod.UpdateMeta(code, MetaUpdate{Mode: &mode})
	require.NoError(t, err)

	// Verify persisted
	stored, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "stream", stored.Mode)
}

// --- HTTP handler tests ---

func TestHandlerListSessions(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("alpha", "/tmp/alpha")
	fake.AddSession("beta", "/tmp/beta")

	// Set meta on first
	require.NoError(t, meta.SetMeta("$0", store.SessionMeta{
		TmuxID: "$0",
		Mode:   "stream",
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var sessions []SessionInfo
	err := json.NewDecoder(w.Body).Decode(&sessions)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
	assert.Equal(t, "alpha", sessions[0].Name)
	assert.Equal(t, "stream", sessions[0].Mode)
}

func TestHandlerListSessionsEmpty(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Should be [], not null
	body := strings.TrimSpace(w.Body.String())
	assert.Equal(t, "[]", body)
}

func TestHandlerGetSession(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp/target")

	// First get the code
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+code, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var info SessionInfo
	err = json.NewDecoder(w.Body).Decode(&info)
	require.NoError(t, err)
	assert.Equal(t, "target", info.Name)
	assert.Equal(t, code, info.Code)
}

func TestHandlerGetSessionNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/zzzzzz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerCreateSession(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "new-session", "cwd": "/tmp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	err := json.NewDecoder(w.Body).Decode(&info)
	require.NoError(t, err)
	assert.Equal(t, "new-session", info.Name)
	assert.Equal(t, "terminal", info.Mode)
	assert.NotEmpty(t, info.Code)

	// Verify session exists via ListSessions
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}

// TestHandlerCreateSession_StampsTmuxInstance guards the create response, which
// builds its SessionInfo by hand rather than going through ListSessions. The
// rebuild engine re-points a pane using the generation in this very response
// (spec v4 §4.8 step 4), so an unstamped create leaves the rebuilt pane with an
// unknown generation until the next sessions broadcast.
func TestHandlerCreateSession_StampsTmuxInstance(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mod.tmuxInstanceFn = func() string { return "4471:1788740000" }
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "stamped", "cwd": "/tmp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&info))
	assert.Equal(t, "4471:1788740000", info.TmuxInstance)
}

func TestHandlerCreateSessionWithMode(t *testing.T) {
	mod, meta, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "stream-session", "cwd": "/tmp", "mode": "stream"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var info SessionInfo
	err := json.NewDecoder(w.Body).Decode(&info)
	require.NoError(t, err)
	assert.Equal(t, "stream", info.Mode)

	// Verify meta persisted with correct mode
	stored, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "stream", stored.Mode)
}

func TestHandlerCreateSessionInvalidMode(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"name": "bad-mode", "cwd": "/tmp", "mode": "invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid mode")
}

func TestHandlerCreateSessionDuplicate(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("existing", "/tmp")

	body := `{"name": "existing", "cwd": "/tmp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "session already exists")
}

// TestHandlerCreateSessionConcurrentSameName asserts that N simultaneous POSTs
// for the same session name result in exactly one 201 and N-1 409s, with no
// duplicate entry in the underlying store. Without createMu, the
// HasSession→NewSession TOCTOU window lets two (or more) creates slip past
// the duplicate check and FakeExecutor appends repeat entries to sessionOrder,
// which is deterministically visible via ListSessions length > 1.
func TestHandlerCreateSessionConcurrentSameName(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	// N chosen generously: FakeExecutor serializes each individual tmux
	// op with its own mutex, so the handler-level TOCTOU window is just
	// the scheduler gap between HasSession and NewSession. N=100 keeps
	// the test reliably RED before the fix on modern multi-core hosts.
	const N = 100
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, N)
	bodies := make([]string, N)

	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines simultaneously
			req := httptest.NewRequest(
				http.MethodPost, "/api/sessions",
				strings.NewReader(`{"name":"dup","cwd":"/tmp"}`),
			)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			codes[i] = w.Code
			bodies[i] = w.Body.String()
		}()
	}
	close(start)
	wg.Wait()

	var created, conflict, other int
	for i, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		default:
			other++
			t.Logf("unexpected status %d body=%q", c, bodies[i])
		}
	}
	assert.Equal(t, 1, created, "exactly one request should succeed")
	assert.Equal(t, N-1, conflict, "other requests should return 409")
	assert.Equal(t, 0, other, "no unexpected statuses")

	// Underlying store must contain exactly one session named "dup".
	// FakeExecutor.NewSession appends to sessionOrder on every call (it
	// overwrites the sessions map but never dedupes the slice), so a
	// lost race is deterministically visible here as length > 1.
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "no duplicate session should exist in store")
	if len(sessions) == 1 {
		assert.Equal(t, "dup", sessions[0].Name)
	}
}

func TestHandlerCreateSessionInvalidName(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	tests := []struct {
		name string
		body string
	}{
		{"empty name", `{"name": ""}`},
		{"spaces", `{"name": "has spaces"}`},
		{"special chars", `{"name": "bad@name"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestHandlerRenameSessionDuplicate(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("alpha", "/tmp")
	fake.AddSession("beta", "/tmp")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code // alpha

	body := `{"name": "beta"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+code, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "session already exists")
}

// TestRenameSessionAtomic_HardErrorNoAgentModule verifies that when the
// agent module is not registered, the rename helper returns a clear error
// (hard-fail) instead of silently falling back to a partial rename.
func TestRenameSessionAtomic_HardErrorNoAgentModule(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	// agent.module is deliberately NOT registered.
	err := mod.renameSessionAtomic("alpha", "beta")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.module not registered")

	// tmux must NOT have been modified.
	assert.True(t, fake.HasSession("alpha"))
	assert.False(t, fake.HasSession("beta"))
}

// spyEventRenamer records Rename calls and can be configured to fail.
type spyEventRenamer struct {
	calls  [][2]string
	failOn string // if non-empty, Rename("old", "new") where old == failOn returns error
}

func (s *spyEventRenamer) Rename(oldName, newName string) error {
	s.calls = append(s.calls, [2]string{oldName, newName})
	if s.failOn != "" && oldName == s.failOn {
		return errors.New("simulated DB rename failure")
	}
	return nil
}

// stubAtomicRenamer implements atomicRenamer by immediately running doRename.
// No in-memory state to transfer in this test — we only care about doRename
// behavior and rollback semantics.
type stubAtomicRenamer struct{}

func (stubAtomicRenamer) RenameSessionAtomic(oldName, newName string, doRename func() error) error {
	return doRename()
}

// TestRenameSessionAtomic_RollbackOnTmuxFailure verifies that when tmux
// rename fails after DB rename succeeded, the DB rename is rolled back
// so all three layers (tmux, DB, in-memory) stay consistent.
func TestRenameSessionAtomic_RollbackOnTmuxFailure(t *testing.T) {
	mod, _, _ := newTestModule(t)
	// Note: deliberately do NOT add "alpha" to fake → tmux.RenameSession
	// will fail with ErrNoSession, triggering the rollback path.

	spy := &spyEventRenamer{}
	mod.core.Registry.Register("agent.events", spy)
	mod.core.Registry.Register("agent.module", stubAtomicRenamer{})

	err := mod.renameSessionAtomic("alpha", "beta")
	require.Error(t, err)

	// Expect two DB calls: forward rename + rollback
	require.Len(t, spy.calls, 2, "expected DB rename + rollback, got %v", spy.calls)
	assert.Equal(t, [2]string{"alpha", "beta"}, spy.calls[0], "first call should be forward rename")
	assert.Equal(t, [2]string{"beta", "alpha"}, spy.calls[1], "second call should be rollback")
}

// TestRenameSessionAtomic_HappyPath verifies DB rename is called exactly once
// when tmux rename succeeds (no rollback).
func TestRenameSessionAtomic_HappyPath(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	spy := &spyEventRenamer{}
	mod.core.Registry.Register("agent.events", spy)
	mod.core.Registry.Register("agent.module", stubAtomicRenamer{})

	err := mod.renameSessionAtomic("alpha", "beta")
	require.NoError(t, err)

	require.Len(t, spy.calls, 1, "expected single DB rename, got %v", spy.calls)
	assert.Equal(t, [2]string{"alpha", "beta"}, spy.calls[0])
	assert.True(t, fake.HasSession("beta"))
	assert.False(t, fake.HasSession("alpha"))
}

// TestRenameSessionAtomic_DBRenameFailsNoTmuxChange verifies that when DB
// rename fails first, tmux is never touched.
func TestRenameSessionAtomic_DBRenameFailsNoTmuxChange(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	spy := &spyEventRenamer{failOn: "alpha"}
	mod.core.Registry.Register("agent.events", spy)
	mod.core.Registry.Register("agent.module", stubAtomicRenamer{})

	err := mod.renameSessionAtomic("alpha", "beta")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated DB rename failure")

	// Only the forward attempt, no rollback (nothing to roll back).
	require.Len(t, spy.calls, 1)
	// tmux unchanged.
	assert.True(t, fake.HasSession("alpha"))
	assert.False(t, fake.HasSession("beta"))
}

func TestHandlerDeleteSession(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("doomed", "/tmp/doomed")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+code, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify session is gone
	sessions, err = mod.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestHandlerDeleteSessionNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/zzzzzz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerSwitchMode(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("mode-test", "/tmp")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	body := `{"mode": "stream"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify mode persisted
	stored, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "stream", stored.Mode)
}

func TestHandlerSwitchModeInvalid(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("mode-test", "/tmp")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	body := `{"mode": "invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerSwitchModeNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"mode": "stream"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/zzzzzz/mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerSendKeys(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	body := `{"keys":"echo hello\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/send-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify keys were sent via SendKeysRaw
	calls := fake.RawKeysSent()
	require.Len(t, calls, 1)
	assert.Equal(t, "=target:", calls[0].Target)
	assert.Equal(t, []string{"echo hello\n"}, calls[0].Keys)
}

func TestHandlerSendKeysNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"keys":"echo hello\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/zzzzzz/send-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleList_CacheDebounce(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("test", "/tmp")

	handler := http.HandlerFunc(mod.handleList)

	// First call — fetches from tmux
	req1 := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first call: want 200, got %d", w1.Code)
	}
	// ListSessions is called once by handleList, but also internally by
	// ListSessions → listSessions which may call tmux.ListSessions.
	// We count tmux-level ListSessions calls via FakeExecutor.
	firstCount := fake.ListCallCount()
	if firstCount < 1 {
		t.Fatalf("first call: want ≥1 tmux ListSessions calls, got %d", firstCount)
	}

	// Second call within TTL — should use cache (no additional tmux calls)
	req2 := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if fake.ListCallCount() != firstCount {
		t.Fatalf("second call: want %d tmux calls (cached), got %d", firstCount, fake.ListCallCount())
	}

	// Verify both responses are identical
	if w1.Body.String() != w2.Body.String() {
		t.Error("cached response differs from original")
	}
}

func TestHandlerTerminalWSNotFound(t *testing.T) {
	// Setup module with no sessions
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	// Request with invalid code — session does not exist
	req := httptest.NewRequest("GET", "/ws/terminal/zzzzzz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
