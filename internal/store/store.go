// internal/store/store.go
package store

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

type Session struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	TmuxTarget string `json:"tmux_target"`
	Cwd        string `json:"cwd"`
	Mode       string `json:"mode"`
	GroupID    int64  `json:"group_id"`
	SortOrder  int    `json:"sort_order"`
}

type SessionUpdate struct {
	Name    *string `json:"name,omitempty"`
	Mode    *string `json:"mode,omitempty"`
	GroupID *int64  `json:"group_id,omitempty"`
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
	return err
}

func (s *Store) CreateSession(sess Session) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO sessions (name, tmux_target, cwd, mode, group_id) VALUES (?, ?, ?, ?, ?)",
		sess.Name, sess.TmuxTarget, sess.Cwd, sess.Mode, sess.GroupID,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListSessions() ([]Session, error) {
	rows, err := s.db.Query("SELECT id, name, tmux_target, cwd, mode, group_id, sort_order FROM sessions ORDER BY sort_order")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var v Session
		if err := rows.Scan(&v.ID, &v.Name, &v.TmuxTarget, &v.Cwd, &v.Mode, &v.GroupID, &v.SortOrder); err != nil {
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
