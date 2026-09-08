// internal/store/agent_event.go
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

// AgentEvent is a single hook event stored per tmux session.
type AgentEvent struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
	BroadcastTs int64           `json:"broadcast_ts"`
}

// AgentEventStore persists the latest agent hook event per tmux session.
type AgentEventStore struct{ db *sql.DB }

// OpenAgentEvent opens (or creates) an AgentEventStore DB at path, runs
// migration, and enables WAL mode. Use ":memory:" for tests.
func OpenAgentEvent(path string) (*AgentEventStore, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout(500): make transient write contention WAIT instead of
		// returning SQLITE_BUSY immediately — protects tail latency under
		// concurrent hooks/sweep/checkpoint without changing durability.
		// 500ms catches typical SSD checkpoint contention (<300ms observed)
		// without blocking the hot path past user-perceived UI latency.
		//
		// foreign_keys(1): enable ON DELETE CASCADE enforcement. Must live in
		// the DSN _pragma param (applied by the driver at every new-connection
		// dial time) rather than a post-Open db.Exec, which would only reach
		// whichever single pool connection happened to be checked out and leave
		// the remaining 3 connections with FK silently disabled.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open agent event db: %w", err)
	}
	if path == ":memory:" {
		// Keep a single connection so the in-memory schema is shared across
		// all queries and transactions in tests.
		db.SetMaxOpenConns(1)
		// :memory: skips the DSN _pragma path above, so enable FK explicitly.
		// Safe on a single connection — no pool-miss risk.
		if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable foreign keys: %w", err)
		}
	} else {
		// File-backed: bound the pool so concurrent goroutines can't fan out
		// fds. agent_event DB is shared with FramesStore + TraceStore via
		// .Frames() / .Traces(), so cap=4 governs all three together.
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(4)
	}
	if err := migrateAgentEventDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate agent event db: %w", err)
	}
	return &AgentEventStore{db: db}, nil
}

func migrateAgentEventDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_events (
			tmux_session TEXT PRIMARY KEY,
			event_name   TEXT NOT NULL,
			raw_event    TEXT NOT NULL,
			updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	// Idempotent migration: add agent_type column if not exists.
	_, _ = db.Exec(`ALTER TABLE agent_events ADD COLUMN agent_type TEXT NOT NULL DEFAULT ''`)
	// Idempotent migration: add broadcast_ts column if not exists.
	_, _ = db.Exec(`ALTER TABLE agent_events ADD COLUMN broadcast_ts INTEGER NOT NULL DEFAULT 0`)
	return nil
}

// Close closes the underlying DB connection.
func (s *AgentEventStore) Close() error { return s.db.Close() }

// ExecRawForTest runs a raw SQL statement on the underlying DB. It exists
// solely for cross-package tests that need to seed rows the public API
// does not allow (e.g. a malformed `subagents_json` to exercise the
// migration fail path). Do not call from production code.
func (s *AgentEventStore) ExecRawForTest(query string, args ...any) (sql.Result, error) {
	return s.db.Exec(query, args...)
}

// Set upserts the latest agent hook event for a tmux session.
func (s *AgentEventStore) Set(tmuxSession, eventName string, rawEvent json.RawMessage, agentType string, broadcastTs int64) error {
	_, err := s.db.Exec(`
		INSERT INTO agent_events (tmux_session, event_name, raw_event, agent_type, broadcast_ts, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tmux_session) DO UPDATE SET
			event_name   = excluded.event_name,
			raw_event    = excluded.raw_event,
			agent_type   = excluded.agent_type,
			broadcast_ts = excluded.broadcast_ts,
			updated_at   = CURRENT_TIMESTAMP
	`, tmuxSession, eventName, string(rawEvent), agentType, broadcastTs)
	return err
}

// Get returns the latest AgentEvent for tmuxSession, or nil if not found.
func (s *AgentEventStore) Get(tmuxSession string) (*AgentEvent, error) {
	var ev AgentEvent
	var raw string
	err := s.db.QueryRow(`
		SELECT tmux_session, event_name, raw_event, agent_type, broadcast_ts
		FROM agent_events WHERE tmux_session = ?
	`, tmuxSession).Scan(&ev.TmuxSession, &ev.EventName, &raw, &ev.AgentType, &ev.BroadcastTs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ev.RawEvent = json.RawMessage(raw)
	return &ev, nil
}

// ListAll returns all stored agent events ordered by tmux_session.
func (s *AgentEventStore) ListAll() ([]AgentEvent, error) {
	rows, err := s.db.Query(`
		SELECT tmux_session, event_name, raw_event, agent_type, broadcast_ts
		FROM agent_events ORDER BY tmux_session
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentEvent
	for rows.Next() {
		var ev AgentEvent
		var raw string
		if err := rows.Scan(&ev.TmuxSession, &ev.EventName, &raw, &ev.AgentType, &ev.BroadcastTs); err != nil {
			return nil, err
		}
		ev.RawEvent = json.RawMessage(raw)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Rename updates the primary key from oldName to newName so that stored
// events follow a tmux session rename and don't become orphaned.
func (s *AgentEventStore) Rename(oldName, newName string) error {
	_, err := s.db.Exec("UPDATE agent_events SET tmux_session = ? WHERE tmux_session = ?", newName, oldName)
	return err
}

// Delete removes the event for tmuxSession (no-op if not found).
func (s *AgentEventStore) Delete(tmuxSession string) error {
	_, err := s.db.Exec("DELETE FROM agent_events WHERE tmux_session = ?", tmuxSession)
	return err
}

func (s *AgentEventStore) Frames() (*FramesStore, error) {
	if err := migrateFramesDB(s.db); err != nil {
		return nil, err
	}
	frames := &FramesStore{db: s.db}
	// The identity version counter continues from what is already on disk. A
	// counter restarting at zero would hand every event after a reboot a
	// version below the ones stored, and UpdateSessionIdentity would refuse
	// them all — silently, for as long as it took to climb back. Read after
	// the migration, so the column is there to read; NULL (an empty table, or
	// a table migrated a moment ago) is the 0 the counter would have started
	// at anyway.
	var maxSeq sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(identity_seq) FROM agent_frames`).Scan(&maxSeq); err != nil {
		return nil, fmt.Errorf("frames store: read identity_seq high-water mark: %w", err)
	}
	if maxSeq.Valid {
		frames.identitySeq.Store(maxSeq.Int64)
	}
	return frames, nil
}

func (s *AgentEventStore) Traces() (*TraceStore, error) {
	if err := migrateTraceDB(s.db); err != nil {
		return nil, err
	}
	return &TraceStore{
		db:        s.db,
		maxChains: defaultTraceMaxChains,
		maxSteps:  defaultTraceMaxSteps,
	}, nil
}
