package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultTraceMaxChains = 10000
	defaultTraceMaxSteps  = 100000
)

// TraceChain is the summary row for a trace chain.
type TraceChain struct {
	ChainID          string `json:"chain_id"`
	StartedAt        int64  `json:"started_at"`
	CompletedAt      int64  `json:"completed_at"`
	TerminalStatus   string `json:"terminal_status"`
	TerminalReason   string `json:"terminal_reason"`
	TmuxSession      string `json:"tmux_session"`
	PaneID           string `json:"pane_id"`
	RootAgentType    string `json:"root_agent_type"`
	RootEventName    string `json:"root_event_name"`
	RootReason       string `json:"root_reason"`
	LatestStepKind   string `json:"latest_step_kind"`
	LatestDecision   string `json:"latest_decision"`
	LatestStepReason string `json:"latest_step_reason"`
	StepCount        int    `json:"step_count,omitempty"`
}

// TraceStep is a single ordered trace step within a chain.
type TraceStep struct {
	StepID        string          `json:"step_id"`
	ChainID       string          `json:"chain_id"`
	ParentStepID  string          `json:"parent_step_id,omitempty"`
	Seq           int             `json:"seq"`
	Kind          string          `json:"kind"`
	TmuxSession   string          `json:"tmux_session"`
	PaneID        string          `json:"pane_id"`
	AgentType     string          `json:"agent_type"`
	FrameID       string          `json:"frame_id"`
	ParentFrameID string          `json:"parent_frame_id,omitempty"`
	EventName     string          `json:"event_name"`
	Decision      string          `json:"decision"`
	Reason        string          `json:"reason"`
	PayloadJSON   json.RawMessage `json:"payload_json,omitempty"`
	BeforeJSON    json.RawMessage `json:"before_json,omitempty"`
	AfterJSON     json.RawMessage `json:"after_json,omitempty"`
	CreatedAt     int64           `json:"created_at"`
}

// TraceRecord combines a chain summary with its ordered steps.
type TraceRecord struct {
	Chain TraceChain  `json:"chain"`
	Steps []TraceStep `json:"steps"`
}

// TraceListFilter filters and paginates trace chains.
type TraceListFilter struct {
	TmuxSession string
	PaneID      string
	AgentType   string
	EventName   string
	Limit       int
	Cursor      string
	Before      bool
}

// TraceChainPage is a page of chain summaries.
type TraceChainPage struct {
	Chains     []TraceChain
	NextCursor string
}

// TraceStore persists trace chains and ordered steps.
type TraceStore struct {
	db        *sql.DB
	maxChains int
	maxSteps  int
}

// sqlQuerier is satisfied by both *sql.DB and *sql.Conn so migration helpers
// can be called on a pinned connection without duplicating function bodies.
type sqlQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// migrateTraceDB pins a single pool connection for the entire migration so
// that "PRAGMA foreign_keys = OFF" and the subsequent DDL all execute on the
// same connection. Without pinning, the DSN foreign_keys(1) pragma (active on
// every other pool connection) would cause DROP TABLE / ALTER TABLE to fail
// when cascading FK children exist.
func migrateTraceDB(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, "PRAGMA foreign_keys = ON") //nolint:errcheck

	chainCols, err := tableColumns(ctx, conn, "agent_trace_chains")
	if err != nil {
		return err
	}
	if len(chainCols) == 0 {
		if err := createTraceChainsTable(ctx, conn); err != nil {
			return err
		}
	} else if needsChainRebuild(chainCols) {
		if err := rebuildLegacyTraceChains(ctx, conn); err != nil {
			return err
		}
	}

	stepCols, err := tableColumns(ctx, conn, "agent_trace_steps")
	if err != nil {
		return err
	}
	if len(stepCols) == 0 {
		if err := createTraceStepsTable(ctx, conn); err != nil {
			return err
		}
	} else if needsStepRebuild(ctx, stepCols, conn) {
		if err := rebuildLegacyTraceSteps(ctx, conn); err != nil {
			return err
		}
	}

	if err := addMissingTraceDedupColumns(ctx, conn); err != nil {
		return err
	}

	if err := createTraceIndexes(ctx, conn); err != nil {
		return err
	}

	// Clean up orphan steps that accumulated while FK enforcement was
	// unreliable (pool-PRAGMA leak in earlier daemon versions). Safe no-op
	// on already-clean DBs.
	_, err = conn.ExecContext(ctx, `
		DELETE FROM agent_trace_steps
		WHERE NOT EXISTS (
			SELECT 1 FROM agent_trace_chains
			WHERE agent_trace_chains.chain_id = agent_trace_steps.chain_id
		)
	`)
	return err
}

// addMissingTraceDedupColumns fills in the payload-dedup columns on databases
// created before they existed. It runs after the create / legacy-rebuild steps
// on the same pinned connection and re-reads table_info, so it is correct
// whether the table was just created, just rebuilt, or left untouched, and
// whether none, one, or both columns are already present.
//
// These columns are deliberately absent from the needsChainRebuild /
// needsStepRebuild required lists: listing them there would send every
// current-schema database down the legacy rebuild path, whose chain copier
// reads created_at / agent_type — columns the current table does not have.
func addMissingTraceDedupColumns(ctx context.Context, q sqlQuerier) error {
	additions := []struct {
		table  string
		column string
		ddl    string
	}{
		{"agent_trace_chains", "root_payload_json", `ALTER TABLE agent_trace_chains ADD COLUMN root_payload_json TEXT NOT NULL DEFAULT 'null'`},
		{"agent_trace_steps", "payload_is_root", `ALTER TABLE agent_trace_steps ADD COLUMN payload_is_root INTEGER NOT NULL DEFAULT 0`},
	}
	for _, add := range additions {
		cols, err := tableColumns(ctx, q, add.table)
		if err != nil {
			return err
		}
		if len(cols) == 0 || cols[add.column] {
			continue
		}
		if _, err := q.ExecContext(ctx, add.ddl); err != nil {
			return err
		}
	}
	return nil
}

func tableColumns(ctx context.Context, q sqlQuerier, table string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var (
			cid     int
			name    string
			colType string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func needsChainRebuild(cols map[string]bool) bool {
	required := []string{
		"started_at",
		"completed_at",
		"terminal_status",
		"terminal_reason",
		"tmux_session",
		"pane_id",
		"root_agent_type",
		"root_event_name",
		"root_reason",
		"latest_step_kind",
		"latest_decision",
		"latest_step_reason",
		"step_count",
		"updated_at",
	}
	for _, col := range required {
		if !cols[col] {
			return true
		}
	}
	return false
}

func needsStepRebuild(ctx context.Context, cols map[string]bool, q sqlQuerier) bool {
	required := []string{
		"seq",
		"kind",
		"tmux_session",
		"pane_id",
		"agent_type",
		"frame_id",
		"parent_frame_id",
		"event_name",
		"decision",
		"reason",
		"payload_json",
		"before_json",
		"after_json",
		"created_at",
	}
	for _, col := range required {
		if !cols[col] {
			return true
		}
	}
	return !hasStepParentCompositeFK(ctx, q)
}

func hasStepParentCompositeFK(ctx context.Context, q sqlQuerier) bool {
	rows, err := q.QueryContext(ctx, `PRAGMA foreign_key_list(agent_trace_steps)`)
	if err != nil {
		return false
	}
	defer rows.Close()

	type fkPart struct {
		table string
		from  string
		to    string
	}
	parts := make(map[int][]fkPart)
	for rows.Next() {
		var (
			id    int
			seq   int
			table string
			from  string
			to    string
			onUpd string
			onDel string
			match string
		)
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpd, &onDel, &match); err != nil {
			return false
		}
		_ = seq
		parts[id] = append(parts[id], fkPart{table: table, from: from, to: to})
	}
	for _, group := range parts {
		if len(group) != 2 {
			continue
		}
		var hasChainRef, hasParentRef bool
		for _, part := range group {
			if part.table != "agent_trace_steps" {
				continue
			}
			if part.from == "chain_id" && part.to == "chain_id" {
				hasChainRef = true
			}
			if part.from == "parent_step_id" && part.to == "step_id" {
				hasParentRef = true
			}
		}
		if hasChainRef && hasParentRef {
			return true
		}
	}
	return false
}

func createTraceChainsTable(ctx context.Context, q sqlQuerier) error {
	_, err := q.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS agent_trace_chains (
			chain_id           TEXT PRIMARY KEY,
			started_at         INTEGER NOT NULL DEFAULT 0,
			completed_at       INTEGER NOT NULL DEFAULT 0,
			terminal_status     TEXT NOT NULL DEFAULT '',
			terminal_reason     TEXT NOT NULL DEFAULT '',
			tmux_session        TEXT NOT NULL DEFAULT '',
			pane_id             TEXT NOT NULL DEFAULT '',
			root_agent_type     TEXT NOT NULL DEFAULT '',
			root_event_name     TEXT NOT NULL DEFAULT '',
			root_reason         TEXT NOT NULL DEFAULT '',
			latest_step_kind    TEXT NOT NULL DEFAULT '',
			latest_decision     TEXT NOT NULL DEFAULT '',
			latest_step_reason  TEXT NOT NULL DEFAULT '',
			step_count          INTEGER NOT NULL DEFAULT 0,
			updated_at          INTEGER NOT NULL DEFAULT 0,
			root_payload_json   TEXT NOT NULL DEFAULT 'null'
		)
	`)
	return err
}

func createTraceStepsTable(ctx context.Context, q sqlQuerier) error {
	_, err := q.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS agent_trace_steps (
			step_id         TEXT PRIMARY KEY,
			chain_id        TEXT NOT NULL,
			parent_step_id  TEXT,
			seq             INTEGER NOT NULL,
			kind            TEXT NOT NULL DEFAULT '',
			tmux_session    TEXT NOT NULL DEFAULT '',
			pane_id         TEXT NOT NULL DEFAULT '',
			agent_type      TEXT NOT NULL DEFAULT '',
			frame_id        TEXT NOT NULL DEFAULT '',
			parent_frame_id TEXT NOT NULL DEFAULT '',
			event_name      TEXT NOT NULL DEFAULT '',
			decision        TEXT NOT NULL DEFAULT '',
			reason          TEXT NOT NULL DEFAULT '',
			payload_json    TEXT NOT NULL DEFAULT 'null',
			before_json     TEXT NOT NULL DEFAULT 'null',
			after_json      TEXT NOT NULL DEFAULT 'null',
			created_at      INTEGER NOT NULL DEFAULT 0,
			payload_is_root INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (chain_id, parent_step_id) REFERENCES agent_trace_steps(chain_id, step_id) ON DELETE CASCADE
		)
	`)
	return err
}

func createTraceIndexes(ctx context.Context, q sqlQuerier) error {
	if _, err := q.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trace_chains_started ON agent_trace_chains(started_at DESC, chain_id DESC)`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trace_chains_session_started ON agent_trace_chains(tmux_session, started_at DESC, chain_id DESC)`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trace_chains_pane_started ON agent_trace_chains(pane_id, started_at DESC, chain_id DESC)`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trace_chains_agent_event_started ON agent_trace_chains(root_agent_type, root_event_name, started_at DESC, chain_id DESC)`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trace_steps_chain_seq ON agent_trace_steps(chain_id, seq ASC, created_at ASC, step_id ASC)`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_trace_steps_chain_step ON agent_trace_steps(chain_id, step_id)`); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trace_steps_parent ON agent_trace_steps(chain_id, parent_step_id)`); err != nil {
		return err
	}
	return nil
}

func rebuildLegacyTraceChains(ctx context.Context, q sqlQuerier) error {
	stepCounts, err := legacyTraceStepCounts(ctx, q)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `ALTER TABLE agent_trace_chains RENAME TO agent_trace_chains_legacy`); err != nil {
		return err
	}
	if err := createTraceChainsTable(ctx, q); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		)
		SELECT
			chain_id,
			created_at,
			updated_at,
			'',
			'',
			tmux_session,
			pane_id,
			agent_type,
			event_name,
			'',
			'',
			'',
			'',
			0,
			updated_at
		FROM agent_trace_chains_legacy
	`)
	if err != nil {
		return err
	}
	if len(stepCounts) > 0 {
		for chainID, count := range stepCounts {
			if _, err := q.ExecContext(ctx, `UPDATE agent_trace_chains SET step_count = ? WHERE chain_id = ?`, count, chainID); err != nil {
				return err
			}
		}
	}
	_, err = q.ExecContext(ctx, `DROP TABLE agent_trace_chains_legacy`)
	return err
}

func legacyTraceStepCounts(ctx context.Context, q sqlQuerier) (map[string]int, error) {
	exists, err := traceTableExists(ctx, q, "agent_trace_steps")
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]int{}, nil
	}

	rows, err := q.QueryContext(ctx, `SELECT chain_id, COUNT(*) FROM agent_trace_steps GROUP BY chain_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var chainID string
		var count int
		if err := rows.Scan(&chainID, &count); err != nil {
			return nil, err
		}
		counts[chainID] = count
	}
	return counts, rows.Err()
}

func traceTableExists(ctx context.Context, q sqlQuerier, table string) (bool, error) {
	var name string
	err := q.QueryRowContext(ctx, `
		SELECT name
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func rebuildLegacyTraceSteps(ctx context.Context, q sqlQuerier) error {
	cols, err := tableColumns(ctx, q, "agent_trace_steps")
	if err != nil {
		return err
	}
	stepRows, err := traceTableRowCount(ctx, q, "agent_trace_steps")
	if err != nil {
		return err
	}
	chainRows, err := traceTableRowCount(ctx, q, "agent_trace_chains")
	if err != nil {
		return err
	}
	if stepRows > 0 && chainRows == 0 {
		return fmt.Errorf("cannot migrate legacy trace steps without legacy trace chains")
	}
	if stepRows > 0 && chainRows > 0 {
		orphanSteps, err := legacyTraceOrphanStepCount(ctx, q)
		if err != nil {
			return err
		}
		if orphanSteps > 0 {
			return fmt.Errorf("cannot migrate legacy trace steps with %d orphan step references", orphanSteps)
		}
	}
	if _, err := q.ExecContext(ctx, `ALTER TABLE agent_trace_steps RENAME TO agent_trace_steps_legacy`); err != nil {
		return err
	}
	if err := createTraceStepsTable(ctx, q); err != nil {
		return err
	}
	var copyQuery string
	if cols["seq"] {
		copyQuery = `
			INSERT INTO agent_trace_steps (
				step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
				agent_type, frame_id, parent_frame_id, event_name, decision, reason,
				payload_json, before_json, after_json, created_at
			)
			SELECT
				s.step_id,
				s.chain_id,
				CASE
					WHEN s.parent_step_id IS NOT NULL
					 AND EXISTS (
						SELECT 1
						FROM agent_trace_steps_legacy p
						WHERE p.chain_id = s.chain_id AND p.step_id = s.parent_step_id
					 )
					THEN s.parent_step_id
					ELSE NULL
				END,
				s.seq,
				s.kind,
				s.tmux_session,
				s.pane_id,
				s.agent_type,
				s.frame_id,
				s.parent_frame_id,
				s.event_name,
				s.decision,
				s.reason,
				s.payload_json,
				s.before_json,
				s.after_json,
				s.created_at
			FROM agent_trace_steps_legacy s
			ORDER BY s.chain_id ASC, s.seq ASC, s.created_at ASC, s.step_id ASC
		`
	} else {
		copyQuery = `
			INSERT INTO agent_trace_steps (
				step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
				agent_type, frame_id, parent_frame_id, event_name, decision, reason,
				payload_json, before_json, after_json, created_at
			)
			SELECT
				s.step_id,
				s.chain_id,
				CASE
					WHEN s.parent_step_id IS NOT NULL
					 AND EXISTS (
						SELECT 1
						FROM agent_trace_steps_legacy p
						WHERE p.chain_id = s.chain_id AND p.step_id = s.parent_step_id
					 )
					THEN s.parent_step_id
					ELSE NULL
				END,
				s.step_index,
				s.step_name,
				c.tmux_session,
				c.pane_id,
				c.root_agent_type,
				'',
				'',
				c.root_event_name,
				'',
				'',
				COALESCE(s.payload, 'null'),
				'null',
				'null',
				s.created_at
			FROM agent_trace_steps_legacy s
			JOIN agent_trace_chains c ON c.chain_id = s.chain_id
			ORDER BY s.chain_id ASC, s.step_index ASC, s.created_at ASC, s.step_id ASC
		`
	}
	_, err = q.ExecContext(ctx, copyQuery)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `DROP TABLE agent_trace_steps_legacy`)
	return err
}

func traceTableRowCount(ctx context.Context, q sqlQuerier, table string) (int, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func legacyTraceOrphanStepCount(ctx context.Context, q sqlQuerier) (int, error) {
	var count int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM agent_trace_steps s
		LEFT JOIN agent_trace_chains c ON c.chain_id = s.chain_id
		WHERE c.chain_id IS NULL
	`).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// planTraceRootDedup decides which steps share the chain's root payload, so
// that payload can be stored once on the chain row instead of once per step.
//
// steps must already be in read order — normalizeTraceRecord ends with a
// sort.SliceStable on Seq → CreatedAt → StepID, exactly matching the read
// query's ORDER BY — so the root candidate is simply steps[0]. Seq is not
// unique, which is why those tie-breaks matter.
//
// Comparison is on the *stored* form (rawJSONText), never on the raw
// json.RawMessage: only payloads whose stored bytes are equal may be merged, so
// the read path can return byte-identical values. Semantically equal but
// byte-different payloads simply keep their own copies.
//
// Returns the stored form of the root payload — "null" when dedup is off, which
// is also the column default — and a slice parallel to steps marking the ones
// that carry it. Correctness never depends on the returned string: a chain whose
// steps all have a genuine null payload legitimately stores "null" as its root
// with the flags set, so only the per-step flag distinguishes the two cases.
func planTraceRootDedup(steps []TraceStep) (string, []bool) {
	isRoot := make([]bool, len(steps))
	if len(steps) == 0 {
		return "null", isRoot
	}

	stored := make([]string, len(steps))
	for i := range steps {
		stored[i] = rawJSONText(steps[i].PayloadJSON)
	}

	candidate := stored[0]
	matches := 0
	for _, text := range stored {
		if text == candidate {
			matches++
		}
	}
	if matches < 2 {
		return "null", isRoot
	}
	for i, text := range stored {
		isRoot[i] = text == candidate
	}
	return candidate, isRoot
}

// SaveChain stores a chain and its steps atomically, replacing any existing
// record for the same chain_id.
func (s *TraceStore) SaveChain(record TraceRecord) (err error) {
	if s == nil || s.db == nil {
		return fmt.Errorf("trace store is nil")
	}
	chain, steps, err := normalizeTraceRecord(record)
	if err != nil {
		return err
	}
	rootPayload, payloadIsRoot := planTraceRootDedup(steps)

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`DELETE FROM agent_trace_steps WHERE chain_id = ?`, chain.ChainID); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at,
			root_payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chain_id) DO UPDATE SET
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			terminal_status = excluded.terminal_status,
			terminal_reason = excluded.terminal_reason,
			tmux_session = excluded.tmux_session,
			pane_id = excluded.pane_id,
			root_agent_type = excluded.root_agent_type,
			root_event_name = excluded.root_event_name,
			root_reason = excluded.root_reason,
			latest_step_kind = excluded.latest_step_kind,
			latest_decision = excluded.latest_decision,
			latest_step_reason = excluded.latest_step_reason,
			step_count = excluded.step_count,
			updated_at = excluded.updated_at,
			root_payload_json = excluded.root_payload_json
	`, chain.ChainID, chain.StartedAt, chain.CompletedAt, chain.TerminalStatus, chain.TerminalReason,
		chain.TmuxSession, chain.PaneID, chain.RootAgentType, chain.RootEventName, chain.RootReason,
		chain.LatestStepKind, chain.LatestDecision, chain.LatestStepReason, chain.StepCount, time.Now().UnixNano(),
		rootPayload); err != nil {
		return err
	}

	for i, step := range steps {
		// payload_is_root is what says a step references the chain's payload; the
		// column itself just holds the empty string so the bytes are not repeated.
		// It is written as a literal SQL value rather than through rawJSONText,
		// which would expand empty to "null" and store four pointless bytes.
		payload := rawJSONText(step.PayloadJSON)
		isRoot := 0
		if payloadIsRoot[i] {
			payload = ""
			isRoot = 1
		}
		if _, err = tx.Exec(`
			INSERT INTO agent_trace_steps (
				step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
				agent_type, frame_id, parent_frame_id, event_name, decision, reason,
				payload_json, before_json, after_json, created_at, payload_is_root
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, step.StepID, step.ChainID, nullString(step.ParentStepID), step.Seq, step.Kind, step.TmuxSession, step.PaneID,
			step.AgentType, step.FrameID, step.ParentFrameID, step.EventName, step.Decision, step.Reason,
			payload, rawJSONText(step.BeforeJSON), rawJSONText(step.AfterJSON), step.CreatedAt, isRoot); err != nil {
			return err
		}
	}

	maxChains, maxSteps := s.traceLimits()
	if err = pruneTraceChains(tx, maxChains, maxSteps); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ListChains returns chain summaries ordered newest-first.
func (s *TraceStore) ListChains(filter TraceListFilter) (TraceChainPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	limit++

	query, args, err := buildTraceChainListQuery(filter, limit)
	if err != nil {
		return TraceChainPage{}, err
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return TraceChainPage{}, err
	}
	defer rows.Close()

	chains, err := collectTraceChains(rows)
	if err != nil {
		return TraceChainPage{}, err
	}

	page := TraceChainPage{Chains: chains}
	if len(page.Chains) > limit-1 {
		last := page.Chains[limit-2]
		page.NextCursor = encodeTraceCursor(last.StartedAt, last.ChainID)
		page.Chains = page.Chains[:limit-1]
	}
	return page, nil
}

// GetChainRecord returns a full chain and its ordered steps.
func (s *TraceStore) GetChainRecord(chainID string) (*TraceRecord, error) {
	return getTraceChainRecord(context.Background(), s.db, chainID)
}

// getTraceChainRecord reads a chain and its steps through the given querier.
func getTraceChainRecord(ctx context.Context, q sqlQuerier, chainID string) (*TraceRecord, error) {
	var chain TraceChain
	err := q.QueryRowContext(ctx, `
		SELECT chain_id, started_at, completed_at, terminal_status, terminal_reason,
		       tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
		       latest_step_kind, latest_decision, latest_step_reason, step_count
		FROM agent_trace_chains
		WHERE chain_id = ?
	`, chainID).Scan(
		&chain.ChainID,
		&chain.StartedAt,
		&chain.CompletedAt,
		&chain.TerminalStatus,
		&chain.TerminalReason,
		&chain.TmuxSession,
		&chain.PaneID,
		&chain.RootAgentType,
		&chain.RootEventName,
		&chain.RootReason,
		&chain.LatestStepKind,
		&chain.LatestDecision,
		&chain.LatestStepReason,
		&chain.StepCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// The payload of a deduped step lives on the chain row, so flag and payload
	// must come from one snapshot. Resolving it in the JOIN makes that
	// structural: reading the chain and the steps as two independent queries
	// and pairing them in Go would let an interleaved SaveChain hand root B's
	// payload to root A's steps.
	//
	// collectTraceSteps scans positionally, so the column order below is load
	// bearing — the CASE has to stay in the payload column's position.
	rows, err := q.QueryContext(ctx, `
		SELECT s.step_id, s.chain_id, s.parent_step_id, s.seq, s.kind, s.tmux_session, s.pane_id,
		       s.agent_type, s.frame_id, s.parent_frame_id, s.event_name, s.decision, s.reason,
		       CASE WHEN s.payload_is_root = 1
		            THEN c.root_payload_json ELSE s.payload_json END AS payload_json,
		       s.before_json, s.after_json, s.created_at
		FROM agent_trace_steps s
		JOIN agent_trace_chains c ON c.chain_id = s.chain_id
		WHERE s.chain_id = ?
		ORDER BY s.seq ASC, s.created_at ASC, s.step_id ASC
	`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	steps, err := collectTraceSteps(rows)
	if err != nil {
		return nil, err
	}
	chain.StepCount = len(steps)
	return &TraceRecord{Chain: chain, Steps: steps}, nil
}

func (s *TraceStore) traceLimits() (int, int) {
	maxChains := s.maxChains
	if maxChains <= 0 {
		maxChains = defaultTraceMaxChains
	}
	maxSteps := s.maxSteps
	if maxSteps <= 0 {
		maxSteps = defaultTraceMaxSteps
	}
	return maxChains, maxSteps
}

func normalizeTraceRecord(record TraceRecord) (TraceChain, []TraceStep, error) {
	chain := record.Chain
	if chain.ChainID == "" {
		chain.ChainID = uuid.NewString()
	}

	steps := make([]TraceStep, len(record.Steps))
	copy(steps, record.Steps)
	for i := range steps {
		if steps[i].StepID == "" {
			steps[i].StepID = uuid.NewString()
		}
		if steps[i].ChainID == "" {
			steps[i].ChainID = chain.ChainID
		}
		if steps[i].ChainID != chain.ChainID {
			return TraceChain{}, nil, fmt.Errorf("step %s belongs to chain %s, want %s", steps[i].StepID, steps[i].ChainID, chain.ChainID)
		}
		if steps[i].Seq == 0 {
			steps[i].Seq = i + 1
		}
		if steps[i].TmuxSession == "" {
			steps[i].TmuxSession = chain.TmuxSession
		}
		if chain.TmuxSession == "" {
			chain.TmuxSession = steps[i].TmuxSession
		}
		if steps[i].PaneID == "" {
			steps[i].PaneID = chain.PaneID
		}
		if chain.PaneID == "" {
			chain.PaneID = steps[i].PaneID
		}
		if steps[i].AgentType == "" {
			steps[i].AgentType = chain.RootAgentType
		}
		if steps[i].EventName == "" {
			steps[i].EventName = chain.RootEventName
		}
		if steps[i].CreatedAt == 0 {
			steps[i].CreatedAt = time.Now().UnixNano() + int64(i)
		}
	}

	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].Seq != steps[j].Seq {
			return steps[i].Seq < steps[j].Seq
		}
		if steps[i].CreatedAt != steps[j].CreatedAt {
			return steps[i].CreatedAt < steps[j].CreatedAt
		}
		return steps[i].StepID < steps[j].StepID
	})

	seen := make(map[string]struct{}, len(steps))
	for i := range steps {
		if steps[i].ParentStepID != "" {
			if _, ok := seen[steps[i].ParentStepID]; !ok {
				return TraceChain{}, nil, fmt.Errorf("step %s references missing parent step %s", steps[i].StepID, steps[i].ParentStepID)
			}
		}
		seen[steps[i].StepID] = struct{}{}
	}

	if len(steps) > 0 {
		first := steps[0]
		last := steps[len(steps)-1]
		if chain.StartedAt == 0 {
			chain.StartedAt = first.CreatedAt
		}
		if chain.CompletedAt == 0 {
			chain.CompletedAt = last.CreatedAt
		}
		if chain.TmuxSession == "" {
			chain.TmuxSession = first.TmuxSession
		}
		if chain.PaneID == "" {
			chain.PaneID = first.PaneID
		}
		if chain.RootAgentType == "" {
			chain.RootAgentType = first.AgentType
		}
		if chain.RootEventName == "" {
			chain.RootEventName = first.EventName
		}
		if chain.RootReason == "" {
			chain.RootReason = first.Reason
		}
		chain.LatestStepKind = last.Kind
		chain.LatestDecision = last.Decision
		chain.LatestStepReason = last.Reason
	} else {
		now := time.Now().UnixNano()
		if chain.StartedAt == 0 {
			chain.StartedAt = now
		}
		if chain.CompletedAt == 0 {
			chain.CompletedAt = chain.StartedAt
		}
	}

	if chain.CompletedAt == 0 {
		chain.CompletedAt = chain.StartedAt
	}
	chain.StepCount = len(steps)
	return chain, steps, nil
}

func pruneTraceChains(tx *sql.Tx, maxChains, maxSteps int) error {
	if maxChains <= 0 || maxSteps <= 0 {
		return nil
	}

	rows, err := tx.Query(`
		SELECT chain_id, step_count
		FROM agent_trace_chains
		ORDER BY started_at ASC, chain_id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type chainStat struct {
		chainID   string
		stepCount int
	}

	stats := make([]chainStat, 0, 64)
	totalSteps := 0
	for rows.Next() {
		var stat chainStat
		if err := rows.Scan(&stat.chainID, &stat.stepCount); err != nil {
			return err
		}
		stats = append(stats, stat)
		totalSteps += stat.stepCount
	}
	if err := rows.Err(); err != nil {
		return err
	}

	totalChains := len(stats)
	var evict []string
	for _, stat := range stats {
		if totalChains <= maxChains && totalSteps <= maxSteps {
			break
		}
		evict = append(evict, stat.chainID)
		totalChains--
		totalSteps -= stat.stepCount
	}
	if len(evict) == 0 {
		return nil
	}

	placeholders := strings.Repeat("?,", len(evict))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(evict))
	for i, chainID := range evict {
		args[i] = chainID
	}
	_, err = tx.Exec(fmt.Sprintf(`DELETE FROM agent_trace_chains WHERE chain_id IN (%s)`, placeholders), args...)
	return err
}

func buildTraceChainListQuery(filter TraceListFilter, limit int) (string, []any, error) {
	var clauses []string
	var args []any

	if filter.TmuxSession != "" {
		clauses = append(clauses, "c.tmux_session = ?")
		args = append(args, filter.TmuxSession)
	}
	if filter.PaneID != "" {
		clauses = append(clauses, "c.pane_id = ?")
		args = append(args, filter.PaneID)
	}
	if filter.AgentType != "" {
		clauses = append(clauses, "c.root_agent_type = ?")
		args = append(args, filter.AgentType)
	}
	if filter.EventName != "" {
		clauses = append(clauses, "c.root_event_name = ?")
		args = append(args, filter.EventName)
	}
	if filter.Cursor != "" {
		startedAt, chainID, err := decodeTraceCursor(filter.Cursor)
		if err != nil {
			return "", nil, err
		}
		op := "<"
		if !filter.Before {
			op = ">"
		}
		clauses = append(clauses, fmt.Sprintf("(c.started_at %s ? OR (c.started_at = ? AND c.chain_id %s ?))", op, op))
		args = append(args, startedAt, startedAt, chainID)
	}

	query := `
		SELECT c.chain_id, c.started_at, c.completed_at, c.terminal_status, c.terminal_reason,
		       c.tmux_session, c.pane_id, c.root_agent_type, c.root_event_name, c.root_reason,
		       c.latest_step_kind, c.latest_decision, c.latest_step_reason, c.step_count
		FROM agent_trace_chains c
	`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += `
		ORDER BY c.started_at DESC, c.chain_id DESC
		LIMIT ?
	`
	args = append(args, limit)
	return query, args, nil
}

func collectTraceChains(rows *sql.Rows) ([]TraceChain, error) {
	var chains []TraceChain
	for rows.Next() {
		var chain TraceChain
		if err := rows.Scan(
			&chain.ChainID,
			&chain.StartedAt,
			&chain.CompletedAt,
			&chain.TerminalStatus,
			&chain.TerminalReason,
			&chain.TmuxSession,
			&chain.PaneID,
			&chain.RootAgentType,
			&chain.RootEventName,
			&chain.RootReason,
			&chain.LatestStepKind,
			&chain.LatestDecision,
			&chain.LatestStepReason,
			&chain.StepCount,
		); err != nil {
			return nil, err
		}
		chains = append(chains, chain)
	}
	return chains, rows.Err()
}

func collectTraceSteps(rows *sql.Rows) ([]TraceStep, error) {
	var steps []TraceStep
	for rows.Next() {
		var step TraceStep
		var parent sql.NullString
		var payload, before, after string
		if err := rows.Scan(
			&step.StepID,
			&step.ChainID,
			&parent,
			&step.Seq,
			&step.Kind,
			&step.TmuxSession,
			&step.PaneID,
			&step.AgentType,
			&step.FrameID,
			&step.ParentFrameID,
			&step.EventName,
			&step.Decision,
			&step.Reason,
			&payload,
			&before,
			&after,
			&step.CreatedAt,
		); err != nil {
			return nil, err
		}
		step.ParentStepID = parent.String
		step.PayloadJSON = json.RawMessage(payload)
		step.BeforeJSON = json.RawMessage(before)
		step.AfterJSON = json.RawMessage(after)
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func encodeTraceCursor(startedAt int64, chainID string) string {
	return strconv.FormatInt(startedAt, 10) + "|" + chainID
}

func decodeTraceCursor(cursor string) (int64, string, error) {
	startedAtText, chainID, ok := strings.Cut(cursor, "|")
	if !ok {
		return 0, "", fmt.Errorf("invalid trace cursor %q", cursor)
	}
	startedAt, err := strconv.ParseInt(startedAtText, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid trace cursor %q: %w", cursor, err)
	}
	if chainID == "" {
		return 0, "", fmt.Errorf("invalid trace cursor %q", cursor)
	}
	return startedAt, chainID, nil
}

func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
