package session

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/store"
)

// --- Provider method tests ---

func TestListSessionsMergesMeta(t *testing.T) {
	mod, meta, fake := newTestModule(t)

	fake.AddSession("dev", "/home/dev")
	fake.AddSession("prod", "/home/prod")

	// Set meta for first session only
	require.NoError(t, meta.SetMeta("$0", store.SessionMeta{
		TmuxID: "$0",
		Mode:   "stream",
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
	assert.Equal(t, "term", sessions[1].Mode)
	assert.NotEmpty(t, sessions[1].Code)
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
		Mode:   "term",
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
	assert.Equal(t, "term", info.Mode)
	assert.NotEmpty(t, info.Code)

	// Verify session exists via ListSessions
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
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
