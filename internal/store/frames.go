package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	agentpkg "github.com/wake/purdex/internal/agent"
)

type Frame struct {
	FrameID          string
	PaneID           string
	AgentType        string
	PID              int
	PPID             int
	ProcessStartTime string
	ParentFrameID    string
	Subagents        []agentpkg.SubagentRef
	Status           agentpkg.Status
	StartedAt        int64
	LastSeenAt       int64
	Verified         bool
	// SessionID and Cwd are the agent's *own* identity, as reported by its
	// hook payloads. They are written by the INSERT half of Upsert and after
	// that only ever by UpdateSessionIdentity — never inside a whole-row
	// round-trip write, so no optimistic-retry loop can clobber them.
	SessionID string
	Cwd       string
}

type FramesStore struct {
	db *sql.DB
}

func migrateFramesDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_frames (
			frame_id            TEXT PRIMARY KEY,
			pane_id             TEXT NOT NULL,
			agent_type          TEXT NOT NULL,
			pid                 INTEGER NOT NULL,
			ppid                INTEGER NOT NULL,
			process_start_time  TEXT NOT NULL,
			parent_frame_id     TEXT,
			subagents_json      TEXT NOT NULL DEFAULT '[]',
			status              TEXT NOT NULL,
			started_at          INTEGER NOT NULL,
			last_seen_at        INTEGER NOT NULL,
			verified            INTEGER NOT NULL DEFAULT 1,
			session_id          TEXT NOT NULL DEFAULT '',
			cwd                 TEXT NOT NULL DEFAULT '',
			identity_seq        INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (parent_frame_id) REFERENCES agent_frames(frame_id) ON DELETE SET NULL
		)
	`)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_frames_pane_pid_start ON agent_frames(pane_id, pid, process_start_time)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_frames_pane ON agent_frames(pane_id)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_frames_agent_type ON agent_frames(agent_type)`); err != nil {
		return err
	}
	if err := addFrameIdentityColumns(db); err != nil {
		return err
	}
	return clearStaleSubagentsJSON(db)
}

// addFrameIdentityColumns brings a table created before the session_id / cwd /
// identity_seq columns existed up to the current schema. Purely additive:
// existing rows get the empty string, which is exactly right — that frame has
// not told us its identity yet — and identity_seq 0, which is older than any
// event, so the first identity write after the migration applies.
// Guarded by a column-existence check so re-running the migration (every
// daemon start) is a no-op rather than a duplicate-column error.
func addFrameIdentityColumns(db *sql.DB) error {
	existing, err := frameColumnNames(db)
	if err != nil {
		return err
	}
	columns := []struct{ name, decl string }{
		{"session_id", `TEXT NOT NULL DEFAULT ''`},
		{"cwd", `TEXT NOT NULL DEFAULT ''`},
		{"identity_seq", `INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range columns {
		if existing[column.name] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE agent_frames ADD COLUMN ` + column.name + ` ` + column.decl); err != nil {
			return fmt.Errorf("add agent_frames.%s: %w", column.name, err)
		}
	}
	return nil
}

func frameColumnNames(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('agent_frames')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	return names, rows.Err()
}

// clearStaleSubagentsJSON scans every `agent_frames.subagents_json` and
// classifies rows as new ([]SubagentRef) / legacy ([]string) / malformed.
// All new → no-op. Any legacy (and nothing malformed) → TRUNCATE table;
// frames are ephemeral telemetry so clearing is lossless. Any malformed →
// return a startup error so the daemon refuses to run rather than silently
// wiping unknown on-disk state.
//
// Full-table scan (not LIMIT 1) because SQLite does not guarantee row order
// and a single probe can hit a new-format row while legacy rows survive —
// scanFrame would then crash on later ListAll/GetByIdentity calls.
func clearStaleSubagentsJSON(db *sql.DB) error {
	rows, err := db.Query(`SELECT frame_id, subagents_json FROM agent_frames`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var hasLegacy bool
	var malformedID string // non-empty = malformed detected
	for rows.Next() {
		var id string
		var js sql.NullString
		if err := rows.Scan(&id, &js); err != nil {
			return err
		}
		raw := ""
		if js.Valid {
			raw = js.String
		}
		var newDst []agentpkg.SubagentRef
		if json.Unmarshal([]byte(raw), &newDst) == nil {
			continue
		}
		var legacyDst []string
		if json.Unmarshal([]byte(raw), &legacyDst) == nil {
			hasLegacy = true
			continue
		}
		if malformedID == "" {
			malformedID = id
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if malformedID != "" {
		return fmt.Errorf("agent_frames row %q has malformed subagents_json; refusing to start — inspect or remove the row manually (Phase 2 PR-2a)", malformedID)
	}
	if !hasLegacy {
		return nil
	}
	if _, err := db.Exec(`DELETE FROM agent_frames`); err != nil {
		return err
	}
	log.Printf("[store] cleared agent_frames: legacy subagents_json schema detected (Phase 2 PR-2a upgrade)")
	return nil
}

func (s *FramesStore) Upsert(frame Frame) (Frame, error) {
	existing, err := s.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
	if err != nil {
		return Frame{}, err
	}
	if existing != nil {
		if frame.FrameID == "" {
			frame.FrameID = existing.FrameID
		}
		if frame.StartedAt == 0 {
			frame.StartedAt = existing.StartedAt
		}
		if frame.Subagents == nil {
			frame.Subagents = existing.Subagents
		}
		if frame.ParentFrameID == "" {
			frame.ParentFrameID = existing.ParentFrameID
		}
	} else {
		if frame.FrameID == "" {
			frame.FrameID = uuid.NewString()
		}
		if frame.StartedAt == 0 {
			frame.StartedAt = frame.LastSeenAt
		}
		if frame.Subagents == nil {
			frame.Subagents = []agentpkg.SubagentRef{}
		}
	}
	if frame.LastSeenAt == 0 {
		frame.LastSeenAt = frame.StartedAt
	}
	subagentsJSON, err := json.Marshal(frame.Subagents)
	if err != nil {
		return Frame{}, fmt.Errorf("marshal subagents: %w", err)
	}
	// session_id / cwd are in the INSERT column list but deliberately NOT in
	// the DO UPDATE SET list: an existing row keeps the identity it already
	// learnt, and only UpdateSessionIdentity may change it afterwards. The
	// method re-SELECTs below, so the returned struct carries the *stored*
	// values by construction — no zero-value merge is needed above.
	_, err = s.db.Exec(`
		INSERT INTO agent_frames (
			frame_id, pane_id, agent_type, pid, ppid, process_start_time,
			parent_frame_id, subagents_json, status, started_at, last_seen_at, verified,
			session_id, cwd
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pane_id, pid, process_start_time) DO UPDATE SET
			agent_type = excluded.agent_type,
			ppid = excluded.ppid,
			parent_frame_id = excluded.parent_frame_id,
			subagents_json = excluded.subagents_json,
			status = excluded.status,
			started_at = excluded.started_at,
			last_seen_at = excluded.last_seen_at,
			verified = excluded.verified
	`, frame.FrameID, frame.PaneID, frame.AgentType, frame.PID, frame.PPID, frame.ProcessStartTime,
		nullString(frame.ParentFrameID), string(subagentsJSON), string(frame.Status), frame.StartedAt, frame.LastSeenAt, boolToInt(frame.Verified),
		frame.SessionID, frame.Cwd)
	if err != nil {
		return Frame{}, err
	}
	stored, err := s.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
	if err != nil {
		return Frame{}, err
	}
	if stored == nil {
		return Frame{}, sql.ErrNoRows
	}
	return *stored, nil
}

func (s *FramesStore) GetByIdentity(paneID string, pid int, startTime string) (*Frame, error) {
	row := s.db.QueryRow(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified,
		       session_id, cwd
		FROM agent_frames
		WHERE pane_id = ? AND pid = ? AND process_start_time = ?
	`, paneID, pid, startTime)
	frame, err := scanFrame(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &frame, nil
}

func (s *FramesStore) FindByPanePID(paneID string, pid int) (*Frame, error) {
	row := s.db.QueryRow(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified,
		       session_id, cwd
		FROM agent_frames
		WHERE pane_id = ? AND pid = ?
		ORDER BY started_at DESC
		LIMIT 1
	`, paneID, pid)
	frame, err := scanFrame(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &frame, nil
}

func (s *FramesStore) ListByPane(paneID string) ([]Frame, error) {
	rows, err := s.db.Query(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified,
		       session_id, cwd
		FROM agent_frames
		WHERE pane_id = ?
		ORDER BY started_at ASC
	`, paneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFrames(rows)
}

func (s *FramesStore) ListAll() ([]Frame, error) {
	rows, err := s.db.Query(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified,
		       session_id, cwd
		FROM agent_frames
		ORDER BY pane_id ASC, started_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFrames(rows)
}

func (s *FramesStore) Delete(frameID string) error {
	_, err := s.db.Exec(`DELETE FROM agent_frames WHERE frame_id = ?`, frameID)
	return err
}

// DeleteIfUnchanged removes the frame only if its last_seen_at matches the
// provided value — a concurrent Upsert that refreshed the row will bump
// last_seen_at and cause this DELETE to match 0 rows, returning (false, nil).
// Caller should treat (false, nil) as "frame got refreshed, skip this sweep".
func (s *FramesStore) DeleteIfUnchanged(frameID string, lastSeenAt int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM agent_frames WHERE frame_id = ? AND last_seen_at = ?`, frameID, lastSeenAt)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// UpdateStatusAndLastSeen updates only the status + last_seen_at columns of
// the frame identified by frameID. Narrow by design: probe-driven status
// transitions must not round-trip through a whole-frame write because doing
// so would clobber concurrent Subagents mutations (see #632 R7). Returns
// sql.ErrNoRows if the frame does not exist.
func (s *FramesStore) UpdateStatusAndLastSeen(frameID string, status agentpkg.Status, lastSeenAt int64) error {
	res, err := s.db.Exec(`
		UPDATE agent_frames SET status = ?, last_seen_at = ?
		WHERE frame_id = ?
	`, string(status), lastSeenAt, frameID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ErrIdentityOutOfOrder reports that an identity write was refused because the
// row already carries a NEWER event's identity. It is not a failure: the write
// that has already landed is the one that should be there.
var ErrIdentityOutOfOrder = errors.New("identity event is older than the stored one")

// UpdateSessionIdentity is the only post-insert writer of session_id / cwd.
// A dedicated narrow UPDATE keyed by frame_id, so the identity never rides a
// read-modify-write and no CAS retry loop has to re-apply it.
//
// Each column is written only when its argument is non-empty: an event that
// carries a session_id but no cwd must not blank the cwd an earlier event
// recorded. Two empty arguments run no SQL at all and return nil.
//
// `seq` versions the write and orders it against the ones around it. Two hooks
// from ONE process can be in flight at the same time, and nothing makes them
// reach this UPDATE in the order the events were emitted: the older one
// arriving last would otherwise write its identity over the newer one's, and
// leave a row describing neither event — last_seen_at from one, session_id
// from the other. `identity_seq` is a column of its own, and deliberately not
// last_seen_at: an unrelated writer (a proxy attach, a probe status
// transition) moves last_seen_at without touching the identity, and ordering
// identity writes by it would let those reorder them.
//
// Equal versions still apply, so retrying one event is idempotent. The stored
// default is 0, which is older than any event, so the first write on a
// migrated row lands.
//
// Returns sql.ErrNoRows if the frame does not exist — a frame deleted between
// the mutation and this call is normal, and callers may swallow it — and
// ErrIdentityOutOfOrder if the row already holds a newer event's identity.
func (s *FramesStore) UpdateSessionIdentity(frameID, sessionID, cwd string, seq int64) error {
	if sessionID == "" && cwd == "" {
		return nil
	}
	setClauses := make([]string, 0, 3)
	args := make([]any, 0, 5)
	if sessionID != "" {
		setClauses = append(setClauses, "session_id = ?")
		args = append(args, sessionID)
	}
	if cwd != "" {
		setClauses = append(setClauses, "cwd = ?")
		args = append(args, cwd)
	}
	setClauses = append(setClauses, "identity_seq = ?")
	args = append(args, seq, frameID, seq)

	res, err := s.db.Exec(
		`UPDATE agent_frames SET `+strings.Join(setClauses, ", ")+
			` WHERE frame_id = ? AND identity_seq <= ?`, args...)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// Zero rows means one of two different things, and callers log them
		// differently: the frame is gone, or the guard refused a stale write.
		return s.classifyIdentityMiss(frameID)
	}
	return nil
}

// classifyIdentityMiss tells "the frame is gone" apart from "the write was
// stale" after a guarded UPDATE matched nothing.
func (s *FramesStore) classifyIdentityMiss(frameID string) error {
	var stored int64
	err := s.db.QueryRow(`SELECT identity_seq FROM agent_frames WHERE frame_id = ?`, frameID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	return ErrIdentityOutOfOrder
}

// UpdateHookPath updates the columns that a general-hook status transition
// (applyFrameEvent's frame != nil branch for non-SessionEnd/SubagentStart/
// SubagentStop events) needs to touch on an existing frame — everything
// except started_at and, crucially, subagents_json. Leaving subagents_json
// out of the SQL prevents a hook handler's stale baseline from clobbering
// concurrent proxy/native subagent mutations on the same row (#632 R8).
// Returns sql.ErrNoRows if the frame does not exist.
func (s *FramesStore) UpdateHookPath(frame Frame) error {
	res, err := s.db.Exec(`
		UPDATE agent_frames SET
			agent_type = ?,
			ppid = ?,
			parent_frame_id = ?,
			status = ?,
			last_seen_at = ?,
			verified = ?
		WHERE frame_id = ?
	`, frame.AgentType, frame.PPID, nullString(frame.ParentFrameID),
		string(frame.Status), frame.LastSeenAt, boolToInt(frame.Verified), frame.FrameID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateHookPathAndResetSubagents is UpdateHookPath plus an explicit
// subagents_json = '[]' write, used only by SessionStart on an existing
// frame where the intent is to wipe the previous session's ref list.
// Race semantics under concurrent attach are "SessionStart wins" — the
// new session's empty list overwrites any in-flight attach; the attach's
// sender will emit its own SubagentStart afterward if still relevant.
// Returns sql.ErrNoRows if the frame does not exist.
func (s *FramesStore) UpdateHookPathAndResetSubagents(frame Frame) error {
	res, err := s.db.Exec(`
		UPDATE agent_frames SET
			agent_type = ?,
			ppid = ?,
			parent_frame_id = ?,
			subagents_json = '[]',
			status = ?,
			last_seen_at = ?,
			verified = ?
		WHERE frame_id = ?
	`, frame.AgentType, frame.PPID, nullString(frame.ParentFrameID),
		string(frame.Status), frame.LastSeenAt, boolToInt(frame.Verified), frame.FrameID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpsertIfUnchanged updates an existing frame atomically, returning
// (false, zeroFrame, nil) if the row's last_seen_at no longer matches
// expectedLastSeenAt — i.e. a concurrent writer changed the row between our
// read and write. Used by subagents-mutation paths (proxy attach / detach /
// SubagentStart / SubagentStop) to serialize read-modify-write cycles.
//
// Unlike Upsert, this is update-only: the frame must already exist and
// frame.FrameID must be set. Caller retries by reloading the row, re-merging
// the subagents list against the new baseline, and calling again.
func (s *FramesStore) UpsertIfUnchanged(frame Frame, expectedLastSeenAt int64) (bool, Frame, error) {
	if frame.FrameID == "" {
		return false, Frame{}, fmt.Errorf("UpsertIfUnchanged: frame.FrameID required")
	}
	if frame.Subagents == nil {
		frame.Subagents = []agentpkg.SubagentRef{}
	}
	subagentsJSON, err := json.Marshal(frame.Subagents)
	if err != nil {
		return false, Frame{}, fmt.Errorf("marshal subagents: %w", err)
	}
	res, err := s.db.Exec(`
		UPDATE agent_frames SET
			agent_type = ?,
			ppid = ?,
			parent_frame_id = ?,
			subagents_json = ?,
			status = ?,
			started_at = ?,
			last_seen_at = ?,
			verified = ?
		WHERE frame_id = ? AND last_seen_at = ?
	`, frame.AgentType, frame.PPID, nullString(frame.ParentFrameID),
		string(subagentsJSON), string(frame.Status), frame.StartedAt, frame.LastSeenAt,
		boolToInt(frame.Verified), frame.FrameID, expectedLastSeenAt)
	if err != nil {
		return false, Frame{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, Frame{}, err
	}
	if affected == 0 {
		return false, Frame{}, nil
	}
	stored, err := s.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
	if err != nil {
		return false, Frame{}, err
	}
	if stored == nil {
		return false, Frame{}, sql.ErrNoRows
	}
	return true, *stored, nil
}

func collectFrames(rows *sql.Rows) ([]Frame, error) {
	var frames []Frame
	for rows.Next() {
		frame, err := scanFrame(rows)
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, rows.Err()
}

type frameScanner interface {
	Scan(dest ...any) error
}

func scanFrame(scanner frameScanner) (Frame, error) {
	var frame Frame
	var parent sql.NullString
	var subagentsJSON string
	var status string
	var verified int
	err := scanner.Scan(
		&frame.FrameID,
		&frame.PaneID,
		&frame.AgentType,
		&frame.PID,
		&frame.PPID,
		&frame.ProcessStartTime,
		&parent,
		&subagentsJSON,
		&status,
		&frame.StartedAt,
		&frame.LastSeenAt,
		&verified,
		&frame.SessionID,
		&frame.Cwd,
	)
	if err != nil {
		return Frame{}, err
	}
	frame.ParentFrameID = parent.String
	frame.Status = agentpkg.Status(status)
	frame.Verified = verified != 0
	if err := json.Unmarshal([]byte(subagentsJSON), &frame.Subagents); err != nil {
		return Frame{}, fmt.Errorf("unmarshal subagents: %w", err)
	}
	if frame.Subagents == nil {
		frame.Subagents = []agentpkg.SubagentRef{}
	}
	return frame, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
