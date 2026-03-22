// internal/store/meta.go
package store

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// SessionMeta is the DB representation of session meta cache.
// It stores ONLY metadata that can't be retrieved from tmux in real-time.
type SessionMeta struct {
	TmuxID      string
	Mode        string
	CCSessionID string
	CCModel     string
	Cwd         string
}

// MetaUpdate supports partial updates (nil = no change).
type MetaUpdate struct {
	Mode        *string
	CCSessionID *string
	CCModel     *string
	Cwd         *string
}

// MetaStore is a lightweight DB for session metadata cache.
type MetaStore struct{ db *sql.DB }

// OpenMeta opens (or creates) a MetaStore DB at path, runs migration, and
// enables WAL mode. Use ":memory:" for tests.
func OpenMeta(path string) (*MetaStore, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open meta db: %w", err)
	}
	if err := migrateMetaDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate meta db: %w", err)
	}
	return &MetaStore{db: db}, nil
}

func migrateMetaDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS session_meta (
			tmux_id       TEXT PRIMARY KEY,
			mode          TEXT DEFAULT 'term',
			cc_session_id TEXT DEFAULT '',
			cc_model      TEXT DEFAULT '',
			cwd           TEXT DEFAULT '',
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// Close closes the underlying DB connection.
func (m *MetaStore) Close() error { return m.db.Close() }

// SetMeta upserts a SessionMeta record (INSERT OR REPLACE).
func (m *MetaStore) SetMeta(tmuxID string, meta SessionMeta) error {
	_, err := m.db.Exec(`
		INSERT INTO session_meta (tmux_id, mode, cc_session_id, cc_model, cwd)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tmux_id) DO UPDATE SET
			mode          = excluded.mode,
			cc_session_id = excluded.cc_session_id,
			cc_model      = excluded.cc_model,
			cwd           = excluded.cwd
	`, tmuxID, meta.Mode, meta.CCSessionID, meta.CCModel, meta.Cwd)
	return err
}

// GetMeta returns the SessionMeta for tmuxID, or nil if not found (not an error).
func (m *MetaStore) GetMeta(tmuxID string) (*SessionMeta, error) {
	var meta SessionMeta
	err := m.db.QueryRow(`
		SELECT tmux_id, mode, cc_session_id, cc_model, cwd
		FROM session_meta WHERE tmux_id = ?
	`, tmuxID).Scan(&meta.TmuxID, &meta.Mode, &meta.CCSessionID, &meta.CCModel, &meta.Cwd)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &meta, nil
}

// ListMeta returns all SessionMeta records ordered by tmux_id.
func (m *MetaStore) ListMeta() ([]SessionMeta, error) {
	rows, err := m.db.Query(`
		SELECT tmux_id, mode, cc_session_id, cc_model, cwd
		FROM session_meta ORDER BY tmux_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var meta SessionMeta
		if err := rows.Scan(&meta.TmuxID, &meta.Mode, &meta.CCSessionID, &meta.CCModel, &meta.Cwd); err != nil {
			return nil, err
		}
		out = append(out, meta)
	}
	return out, rows.Err()
}

// UpdateMeta performs a partial update; only non-nil fields are written.
func (m *MetaStore) UpdateMeta(tmuxID string, update MetaUpdate) error {
	var setClauses []string
	var args []any

	if update.Mode != nil {
		setClauses = append(setClauses, "mode = ?")
		args = append(args, *update.Mode)
	}
	if update.CCSessionID != nil {
		setClauses = append(setClauses, "cc_session_id = ?")
		args = append(args, *update.CCSessionID)
	}
	if update.CCModel != nil {
		setClauses = append(setClauses, "cc_model = ?")
		args = append(args, *update.CCModel)
	}
	if update.Cwd != nil {
		setClauses = append(setClauses, "cwd = ?")
		args = append(args, *update.Cwd)
	}

	if len(setClauses) == 0 {
		return nil // nothing to update
	}

	args = append(args, tmuxID)
	query := fmt.Sprintf("UPDATE session_meta SET %s WHERE tmux_id = ?",
		strings.Join(setClauses, ", "))
	_, err := m.db.Exec(query, args...)
	return err
}

// DeleteMeta removes the record for tmuxID (no-op if not found).
func (m *MetaStore) DeleteMeta(tmuxID string) error {
	_, err := m.db.Exec("DELETE FROM session_meta WHERE tmux_id = ?", tmuxID)
	return err
}

// CleanOrphans deletes meta records whose tmux_id is not in liveTmuxIDs.
// Returns the number of rows deleted.
func (m *MetaStore) CleanOrphans(liveTmuxIDs []string) (int, error) {
	if len(liveTmuxIDs) == 0 {
		// Delete everything
		res, err := m.db.Exec("DELETE FROM session_meta")
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}

	placeholders := strings.Repeat("?,", len(liveTmuxIDs))
	placeholders = placeholders[:len(placeholders)-1] // trim trailing comma

	args := make([]any, len(liveTmuxIDs))
	for i, id := range liveTmuxIDs {
		args[i] = id
	}

	query := fmt.Sprintf("DELETE FROM session_meta WHERE tmux_id NOT IN (%s)", placeholders)
	res, err := m.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResetStaleModes resets all sessions with a non-'term' mode back to 'term'.
// Called on daemon startup to clear modes that were active when the daemon last stopped.
func (m *MetaStore) ResetStaleModes() error {
	_, err := m.db.Exec("UPDATE session_meta SET mode = 'term' WHERE mode != 'term'")
	return err
}
