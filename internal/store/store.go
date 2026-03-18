// internal/store/store.go
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

type Session struct {
	ID         int64  `json:"id"`
	UID        string `json:"uid"`
	Name       string `json:"name"`
	TmuxTarget string `json:"tmux_target"`
	Cwd        string `json:"cwd"`
	Mode       string `json:"mode"`
	GroupID    int64  `json:"group_id"`
	SortOrder   int    `json:"sort_order"`
	CCSessionID string `json:"cc_session_id"`
	CCModel     string `json:"cc_model"`
}

// generateUID creates a short URL-safe unique ID (8 chars, ~40 bits entropy).
func generateUID() string {
	b := make([]byte, 5) // 5 bytes = 40 bits
	rand.Read(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

type SessionUpdate struct {
	Name        *string `json:"name,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	GroupID     *int64  `json:"group_id,omitempty"`
	CCSessionID *string `json:"cc_session_id,omitempty"`
	CCModel     *string `json:"cc_model,omitempty"`
}

type Group struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	Collapsed bool   `json:"collapsed"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			uid TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			tmux_target TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'term',
			group_id INTEGER NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			collapsed INTEGER NOT NULL DEFAULT 0
		);
	`)
	if err != nil {
		return err
	}
	// Migration: add uid column if missing (existing DBs)
	var hasUID bool
	rows, _ := db.Query("PRAGMA table_info(sessions)")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt sql.NullString
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == "uid" {
				hasUID = true
			}
		}
	}
	if !hasUID {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN uid TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add uid column: %w", err)
		}
	}
	// Backfill empty UIDs using Go-based generateUID() for consistent format
	rows2, err := db.Query("SELECT id FROM sessions WHERE uid = ''")
	if err != nil {
		return fmt.Errorf("query empty uids: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var id int64
		if err := rows2.Scan(&id); err == nil {
			if _, err := db.Exec("UPDATE sessions SET uid = ? WHERE id = ?", generateUID(), id); err != nil {
				return fmt.Errorf("backfill uid for id %d: %w", id, err)
			}
		}
	}
	// Ensure UID uniqueness
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_uid ON sessions(uid) WHERE uid != ''")
	// Migration: add cc_session_id column if missing
	var hasCCSessionID bool
	rows3, _ := db.Query("PRAGMA table_info(sessions)")
	if rows3 != nil {
		defer rows3.Close()
		for rows3.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt sql.NullString
			var pk int
			rows3.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == "cc_session_id" {
				hasCCSessionID = true
			}
		}
	}
	if !hasCCSessionID {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN cc_session_id TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add cc_session_id column: %w", err)
		}
	}
	// Migration: add cc_model column if missing
	var hasCCModel bool
	rows4, _ := db.Query("PRAGMA table_info(sessions)")
	if rows4 != nil {
		defer rows4.Close()
		for rows4.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt sql.NullString
			var pk int
			rows4.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == "cc_model" {
				hasCCModel = true
			}
		}
	}
	if !hasCCModel {
		if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN cc_model TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add cc_model column: %w", err)
		}
	}
	return nil
}

func (s *Store) CreateSession(sess Session) (int64, error) {
	if sess.UID == "" {
		sess.UID = generateUID()
	}
	res, err := s.db.Exec(
		"INSERT INTO sessions (uid, name, tmux_target, cwd, mode, group_id) VALUES (?, ?, ?, ?, ?, ?)",
		sess.UID, sess.Name, sess.TmuxTarget, sess.Cwd, sess.Mode, sess.GroupID,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListSessions() ([]Session, error) {
	rows, err := s.db.Query("SELECT id, uid, name, tmux_target, cwd, mode, group_id, sort_order, cc_session_id, cc_model FROM sessions ORDER BY sort_order")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var v Session
		if err := rows.Scan(&v.ID, &v.UID, &v.Name, &v.TmuxTarget, &v.Cwd, &v.Mode, &v.GroupID, &v.SortOrder, &v.CCSessionID, &v.CCModel); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSession(id int64, u SessionUpdate) error {
	updated := false
	if u.Name != nil {
		res, err := s.db.Exec("UPDATE sessions SET name = ? WHERE id = ?", *u.Name, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			updated = true
		}
	}
	if u.Mode != nil {
		res, err := s.db.Exec("UPDATE sessions SET mode = ? WHERE id = ?", *u.Mode, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			updated = true
		}
	}
	if u.GroupID != nil {
		res, err := s.db.Exec("UPDATE sessions SET group_id = ? WHERE id = ?", *u.GroupID, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			updated = true
		}
	}
	if u.CCSessionID != nil {
		res, err := s.db.Exec("UPDATE sessions SET cc_session_id = ? WHERE id = ?", *u.CCSessionID, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			updated = true
		}
	}
	if u.CCModel != nil {
		res, err := s.db.Exec("UPDATE sessions SET cc_model = ? WHERE id = ?", *u.CCModel, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			updated = true
		}
	}
	if !updated {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSession(id int64) error {
	res, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetSession(id int64) (Session, error) {
	var sess Session
	err := s.db.QueryRow(
		"SELECT id, uid, name, tmux_target, cwd, mode, group_id, sort_order, cc_session_id, cc_model FROM sessions WHERE id = ?", id,
	).Scan(&sess.ID, &sess.UID, &sess.Name, &sess.TmuxTarget, &sess.Cwd, &sess.Mode, &sess.GroupID, &sess.SortOrder, &sess.CCSessionID, &sess.CCModel)
	if err != nil {
		if err == sql.ErrNoRows {
			return sess, ErrNotFound
		}
		return sess, err
	}
	return sess, nil
}

func (s *Store) CreateGroup(name string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO groups (name) VALUES (?)", name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query("SELECT id, name, sort_order, collapsed FROM groups ORDER BY sort_order")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.SortOrder, &g.Collapsed); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) UpdateGroup(id int64, name string) error {
	res, err := s.db.Exec("UPDATE groups SET name = ? WHERE id = ?", name, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
