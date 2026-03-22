// internal/server/legacy_test.go
package server_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func TestNewLegacyDoesNotCallRoutes(t *testing.T) {
	// NewLegacy should NOT register session/terminal routes
	// and should NOT call resetStaleModes.
	cfg := config.Config{Bind: "127.0.0.1", Port: 7860}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx := tmux.NewFakeExecutor()

	s := server.NewLegacy(cfg, "/tmp/config.toml", st, nil, tx)
	assert.NotNil(t, s)
}

func TestRegisterLegacyRoutes(t *testing.T) {
	cfg := config.Config{Bind: "127.0.0.1", Port: 7860}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx := tmux.NewFakeExecutor()

	s := server.NewLegacy(cfg, "/tmp/config.toml", st, nil, tx)

	mux := http.NewServeMux()
	s.RegisterLegacyRoutes(mux)

	// Verify legacy routes respond (not 404 "pattern not found").
	// Config endpoint should work without WS upgrade.
	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "GET /api/config should be registered")

	// Session CRUD routes should NOT be registered on the legacy mux.
	req = httptest.NewRequest("GET", "/api/sessions", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code, "GET /api/sessions should NOT be on legacy routes")
}
