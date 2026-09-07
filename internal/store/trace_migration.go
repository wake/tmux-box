package store

// Schema creation, migration and legacy-table rebuild for the trace store.
//
// migrateTraceDB pins one connection for the whole migration so that
// "PRAGMA foreign_keys = OFF" and the DDL that follows run together; see its
// own comment for why that matters.

import (
	"context"
	"database/sql"
	"fmt"
)

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
