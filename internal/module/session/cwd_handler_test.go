package session

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSessionCwd_ReturnsCwd(t *testing.T) {
	mod, _, fake := newTestModule(t)

	// Create a real session via fake; this generates a tmux ID like "$0" and stores name "my-sess"
	fake.AddSession("my-sess", "/initial")
	fake.SetPaneCwd("my-sess", "/home/user/proj")

	// Find the code by listing sessions — codec maps tmuxID → code
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Cwd string `json:"cwd"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "/home/user/proj", body.Cwd)
}

func TestHandleSessionCwd_NotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Use a syntactically valid but non-existent code
	resp, err := http.Get(srv.URL + "/api/sessions/nosession/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleSessionCwd_TmuxError(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("my-sess", "/initial")
	// NOT calling SetPaneCwd → PaneCurrentPath will return an error

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// --- Generation preconditions (spec §4.6.2) ---

// TestHandleSessionCwd_ReturnsTmuxInstance guards the wire shape: the cwd
// response self-describes the generation it was sampled in, so the probe can
// refuse to write it under a binding it does not belong to.
func TestHandleSessionCwd_ReturnsTmuxInstance(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func() string { return "111:1000" }

	fake.AddSession("my-sess", "/initial")
	fake.SetPaneCwd("my-sess", "/home/user/proj")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Cwd          string `json:"cwd"`
		TmuxInstance string `json:"tmux_instance"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "/home/user/proj", body.Cwd)
	assert.Equal(t, "111:1000", body.TmuxInstance)
}

// TestHandleSessionCwd_TmuxInstanceKeyAlwaysPresent — "" is a transmitted
// value meaning "unknown", never an elided field (spec §4.6).
func TestHandleSessionCwd_TmuxInstanceKeyAlwaysPresent(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = func() string { return "" }

	fake.AddSession("my-sess", "/initial")
	fake.SetPaneCwd("my-sess", "/home/user/proj")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + sessions[0].Code + "/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"tmux_instance":""`)
}

// TestHandleSessionCwd_RestartDuringRead_ReportsUnknown — the cwd and the
// generation must be sampled TOGETHER. A tmux restart between resolving the
// session and reading the pane path would otherwise stamp the new server's
// cwd with the old server's generation, and the probe — which only compares
// the returned instance against the one it asked with — would accept it.
func TestHandleSessionCwd_RestartDuringRead_ReportsUnknown(t *testing.T) {
	mod, _, fake := newTestModule(t)

	fake.AddSession("my-sess", "/initial")
	fake.SetPaneCwd("my-sess", "/home/user/proj")

	mod.tmuxInstanceFn = func() string { return "111:1000" }
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	code := sessions[0].Code

	// The handler samples the instance twice: once resolving the session and
	// once after reading the pane path. Flip the answer between them.
	calls := 0
	mod.tmuxInstanceFn = func() string {
		calls++
		if calls == 1 {
			return "111:1000"
		}
		return "222:2000"
	}

	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/cwd")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Cwd          string `json:"cwd"`
		TmuxInstance string `json:"tmux_instance"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "", body.TmuxInstance,
		"a generation that changed mid-read is unknown, and unknown never authorises a write")
}
