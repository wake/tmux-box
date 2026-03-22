// internal/store/migration_test.go
package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/tmux"
	_ "modernc.org/sqlite"
)

func TestMigrateFromLegacy(t *testing.T) {
	// Create a legacy DB with old schema
	legacyDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer legacyDB.Close()

	legacyDB.Exec(`CREATE TABLE sessions (
		id INTEGER PRIMARY KEY, uid TEXT, name TEXT,
		tmux_target TEXT, cwd TEXT, mode TEXT,
		group_id INTEGER, sort_order INTEGER,
		cc_session_id TEXT, cc_model TEXT
	)`)
	legacyDB.Exec(`INSERT INTO sessions (name, mode, cc_session_id, cc_model, cwd)
		VALUES ('work', 'stream', 'sess-123', 'opus', '/home')`)
	legacyDB.Exec(`INSERT INTO sessions (name, mode, cc_session_id, cc_model, cwd)
		VALUES ('play', 'term', '', '', '/tmp')`)

	// Mock tmux sessions with IDs
	tmuxSessions := []tmux.TmuxSession{
		{ID: "$0", Name: "work", Cwd: "/home"},
		{ID: "$1", Name: "play", Cwd: "/tmp"},
	}

	meta, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer meta.Close()

	err = meta.MigrateFromLegacy(legacyDB, tmuxSessions)
	require.NoError(t, err)

	m0, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, m0)
	assert.Equal(t, "term", m0.Mode) // always "term" on migration — stale modes are meaningless after restart
	assert.Equal(t, "sess-123", m0.CCSessionID)
	assert.Equal(t, "opus", m0.CCModel)

	m1, err := meta.GetMeta("$1")
	require.NoError(t, err)
	require.NotNil(t, m1)
	assert.Equal(t, "term", m1.Mode)
}

func TestMigrateFromLegacyNoTmux(t *testing.T) {
	meta, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer meta.Close()

	// tmux unavailable — nil sessions
	err = meta.MigrateFromLegacy(nil, nil)
	require.NoError(t, err, "should succeed with empty migration")
}

func TestMigrateFromLegacyNoLegacyTable(t *testing.T) {
	// Legacy DB exists but has no sessions table
	legacyDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer legacyDB.Close()

	meta, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer meta.Close()

	tmuxSessions := []tmux.TmuxSession{{ID: "$0", Name: "test", Cwd: "/"}}

	err = meta.MigrateFromLegacy(legacyDB, tmuxSessions)
	require.NoError(t, err, "should not error if legacy table doesn't exist")
}
