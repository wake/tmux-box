package store

// Migration coverage for the trace store: legacy rebuild paths, the pinned
// connection, the dedup column additions, and restart behaviour — every case
// that exercises migrateTraceDB against a database that already holds rows.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestTraceStore_MigratesLegacySchemaAndReadsListChains(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceSchema(t, s.db)
	seedLegacyTraceData(t, s.db)

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces: %v", err)
	}
	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}

	page, err := store.ListChains(TraceListFilter{
		TmuxSession: "proj-legacy",
		PaneID:      "%9",
		AgentType:   "cc",
		EventName:   "Stop",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("chains = %d, want 1", len(page.Chains))
	}
	if page.Chains[0].StartedAt != 123 {
		t.Fatalf("started_at = %d, want 123", page.Chains[0].StartedAt)
	}
	if page.Chains[0].StepCount != 2 {
		t.Fatalf("step_count = %d, want 2", page.Chains[0].StepCount)
	}
}

func TestTraceStore_MigratesLegacyChainsWithoutStepsTable(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceChainsOnly(t, s.db)

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces: %v", err)
	}

	store := &TraceStore{db: s.db, maxChains: 10, maxSteps: 10}
	page, err := store.ListChains(TraceListFilter{
		TmuxSession: "proj-legacy",
		PaneID:      "%9",
		AgentType:   "cc",
		EventName:   "Stop",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("chains = %d, want 1", len(page.Chains))
	}
	if page.Chains[0].StepCount != 0 {
		t.Fatalf("step_count = %d, want 0", page.Chains[0].StepCount)
	}
}

func TestTraceStore_MigratesLegacyStepsWithoutChainsTable(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceStepsOnly(t, s.db)

	if _, err := s.Traces(); err == nil {
		t.Fatal("expected Traces to fail fast")
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps`).Scan(&count); err != nil {
		t.Fatalf("count legacy steps: %v", err)
	}
	if count != 1 {
		t.Fatalf("legacy step count = %d, want 1", count)
	}
}

func TestTraceStore_MigratesLegacyStepsWithOrphanChainReference(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceStepsWithUnrelatedChain(t, s.db)

	if _, err := s.Traces(); err == nil {
		t.Fatal("expected Traces to fail fast")
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps`).Scan(&count); err != nil {
		t.Fatalf("count legacy steps: %v", err)
	}
	if count != 1 {
		t.Fatalf("legacy step count = %d, want 1", count)
	}
}

func TestTraceStore_MigratesLegacyStepSchemaAndBlocksCrossChainParent(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedIntermediateTraceSchema(t, s.db)
	seedIntermediateTraceData(t, s.db)

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces: %v", err)
	}

	_, err := s.db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at
		) VALUES
		('b-1', 'chain-b', 'a-1', 1, 'decision', 'proj-a', '%1', 'cc', 'frame-b', '', 'Stop', 'continue', 'needs-parent', 'null', 'null', 'null', 3)
	`)
	if err == nil {
		t.Fatal("expected cross-chain parent insert to fail")
	}
}

func seedLegacyTraceSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_chains (
			chain_id     TEXT PRIMARY KEY,
			tmux_session TEXT NOT NULL,
			pane_id      TEXT NOT NULL,
			agent_type   TEXT NOT NULL,
			event_name   TEXT NOT NULL,
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy chains: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id        TEXT PRIMARY KEY,
			chain_id       TEXT NOT NULL,
			parent_step_id TEXT,
			step_name      TEXT NOT NULL,
			payload        TEXT NOT NULL DEFAULT 'null',
			step_index     INTEGER NOT NULL,
			created_at     INTEGER NOT NULL,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create legacy steps: %v", err)
	}
}

func seedLegacyTraceChainsOnly(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_chains (
			chain_id     TEXT PRIMARY KEY,
			tmux_session TEXT NOT NULL,
			pane_id      TEXT NOT NULL,
			agent_type   TEXT NOT NULL,
			event_name   TEXT NOT NULL,
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy chains: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, tmux_session, pane_id, agent_type, event_name, created_at, updated_at
		) VALUES
		('legacy-chain', 'proj-legacy', '%9', 'cc', 'Stop', 123, 456)
	`); err != nil {
		t.Fatalf("seed legacy chain: %v", err)
	}
}

func seedLegacyTraceStepsOnly(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
	})

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id        TEXT PRIMARY KEY,
			chain_id       TEXT NOT NULL,
			parent_step_id TEXT,
			step_name      TEXT NOT NULL,
			payload        TEXT NOT NULL DEFAULT 'null',
			step_index     INTEGER NOT NULL,
			created_at     INTEGER NOT NULL,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create legacy steps: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, step_name, payload, step_index, created_at
		) VALUES
		('legacy-step-1', 'legacy-chain', NULL, 'root', 'null', 1, 124)
	`); err != nil {
		t.Fatalf("seed legacy step: %v", err)
	}
}

func seedLegacyTraceStepsWithUnrelatedChain(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`PRAGMA foreign_keys = ON`)
	})

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_chains (
			chain_id     TEXT PRIMARY KEY,
			tmux_session TEXT NOT NULL,
			pane_id      TEXT NOT NULL,
			agent_type   TEXT NOT NULL,
			event_name   TEXT NOT NULL,
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy chains: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, tmux_session, pane_id, agent_type, event_name, created_at, updated_at
		) VALUES
		('other-chain', 'proj-legacy', '%9', 'cc', 'Stop', 123, 456)
	`); err != nil {
		t.Fatalf("seed unrelated chain: %v", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_steps (
			step_id        TEXT PRIMARY KEY,
			chain_id       TEXT NOT NULL,
			parent_step_id TEXT,
			step_name      TEXT NOT NULL,
			payload        TEXT NOT NULL DEFAULT 'null',
			step_index     INTEGER NOT NULL,
			created_at     INTEGER NOT NULL,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create legacy steps: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, step_name, payload, step_index, created_at
		) VALUES
		('legacy-step-1', 'missing-chain', NULL, 'root', 'null', 1, 124)
	`); err != nil {
		t.Fatalf("seed orphan legacy step: %v", err)
	}
}

func seedLegacyTraceData(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, tmux_session, pane_id, agent_type, event_name, created_at, updated_at
		) VALUES
		('legacy-chain', 'proj-legacy', '%9', 'cc', 'Stop', 123, 456)
	`); err != nil {
		t.Fatalf("seed legacy chain: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, step_name, payload, step_index, created_at
		) VALUES
		('legacy-step-1', 'legacy-chain', NULL, 'root', 'null', 1, 124),
		('legacy-step-2', 'legacy-chain', 'legacy-step-1', 'terminal', 'null', 2, 125)
	`); err != nil {
		t.Fatalf("seed legacy steps: %v", err)
	}
}

func seedIntermediateTraceSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	if err := createTraceChainsTable(context.Background(), db); err != nil {
		t.Fatalf("create chains: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE agent_trace_steps (
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
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		t.Fatalf("create intermediate steps: %v", err)
	}
}

func seedIntermediateTraceData(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES
		('chain-a', 1, 2, 'done', 'ok', 'proj-a', '%1', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 1, 2),
		('chain-b', 3, 4, 'done', 'ok', 'proj-a', '%1', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 1, 4)
	`); err != nil {
		t.Fatalf("seed chains: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at
		) VALUES
		('a-1', 'chain-a', NULL, 1, 'root', 'proj-a', '%1', 'cc', 'frame-a', '', 'Stop', '', '', 'null', 'null', 'null', 1)
	`); err != nil {
		t.Fatalf("seed step: %v", err)
	}
}

// TestMigrateTraceDB_RunsOnPinnedConnection verifies that migrateTraceDB
// succeeds against a file-backed pool with FK=1 on all pool connections.
// Before the fix, the FK=OFF PRAGMA on a random pool connection left other
// connections with FK=ON during DDL, breaking schema creation.
func TestMigrateTraceDB_RunsOnPinnedConnection(t *testing.T) {
	s := openFileTraceStore(t)

	// Save a chain with steps to confirm schema is fully functional.
	record := TraceRecord{
		Chain: TraceChain{
			ChainID:        "pinned-chain",
			StartedAt:      1,
			CompletedAt:    2,
			TerminalStatus: "done",
			TmuxSession:    "proj-pinned",
			PaneID:         "%1",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "ok",
		},
		Steps: []TraceStep{
			{StepID: "pinned-s1", ChainID: "pinned-chain", Seq: 1, Kind: "root", TmuxSession: "proj-pinned", PaneID: "%1", AgentType: "cc", EventName: "Stop", CreatedAt: 1},
			{StepID: "pinned-s2", ChainID: "pinned-chain", ParentStepID: "pinned-s1", Seq: 2, Kind: "terminal", TmuxSession: "proj-pinned", PaneID: "%1", AgentType: "cc", EventName: "Stop", CreatedAt: 2},
		},
	}
	if err := s.SaveChain(record); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.GetChainRecord("pinned-chain")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil || len(got.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %v", got)
	}
}

// TestMigrateTraceDB_CascadeRegression_FileBackedPool runs the prune-cascade
// scenario (F3) on a file-backed pool to ensure FK enforcement works across
// pool connections — the scenario that :memory: single-conn tests cannot catch.
func TestMigrateTraceDB_CascadeRegression_FileBackedPool(t *testing.T) {
	s := openFileTraceStore(t)
	s.maxChains = 5
	s.maxSteps = 1000

	for i := 0; i < 20; i++ {
		chainID := fmt.Sprintf("fp-chain-%02d", i)
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:          chainID,
				StartedAt:        int64(i + 1),
				CompletedAt:      int64(i + 2),
				TerminalStatus:   "done",
				TerminalReason:   "ok",
				TmuxSession:      "proj-fp",
				PaneID:           "%8",
				RootAgentType:    "cc",
				RootEventName:    "Stop",
				RootReason:       "ok",
				LatestStepKind:   "terminal",
				LatestDecision:   "done",
				LatestStepReason: "ok",
			},
			Steps: []TraceStep{
				{StepID: fmt.Sprintf("fp%02d-1", i), ChainID: chainID, Seq: 1, Kind: "root", TmuxSession: "proj-fp", PaneID: "%8", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 1)},
				{StepID: fmt.Sprintf("fp%02d-2", i), ChainID: chainID, ParentStepID: fmt.Sprintf("fp%02d-1", i), Seq: 2, Kind: "terminal", TmuxSession: "proj-fp", PaneID: "%8", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 2)},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	var chainCount, orphans int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_trace_chains").Scan(&chainCount); err != nil {
		t.Fatalf("count chains: %v", err)
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_trace_steps
		WHERE chain_id NOT IN (SELECT chain_id FROM agent_trace_chains)
	`).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if chainCount > 5 {
		t.Errorf("chainCount = %d, want ≤5", chainCount)
	}
	if orphans != 0 {
		t.Errorf("orphan steps = %d after file-backed pool prune: ON DELETE CASCADE did not fire", orphans)
	}
}

// TestMigrateTraceDB_CleansLegacyOrphans verifies that migrateTraceDB
// deletes pre-existing orphan agent_trace_steps (rows whose chain_id has no
// corresponding agent_trace_chains row) accumulated while FK enforcement was
// unreliable.
func TestMigrateTraceDB_CleansLegacyOrphans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orphan.db")

	// First open: create schema and seed orphan steps via a pinned conn with FK off.
	s1, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	ctx := context.Background()
	conn, err := s1.db.Conn(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("fk off: %v", err)
	}

	// Ensure trace tables exist before inserting.
	if _, err := s1.Traces(); err != nil {
		t.Fatalf("first Traces: %v", err)
	}

	// Insert 5 orphan steps — chain_id 'ghost-chain' never appears in agent_trace_chains.
	for i := 1; i <= 5; i++ {
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO agent_trace_steps (step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id, agent_type, frame_id, parent_frame_id, event_name, decision, reason, payload_json, before_json, after_json, created_at)
			 VALUES (?, 'ghost-chain', NULL, ?, 'root', '', '', '', '', '', '', '', '', 'null', 'null', 'null', ?)`,
			fmt.Sprintf("ghost-step-%d", i), i, i,
		); err != nil {
			t.Fatalf("insert orphan step %d: %v", i, err)
		}
	}
	_ = conn.Close()
	_ = s1.Close()

	// Second open re-runs migrateTraceDB, which must delete the 5 orphans.
	s2, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()

	if _, err := s2.Traces(); err != nil {
		t.Fatalf("second Traces: %v", err)
	}

	var orphanCount int
	if err := s2.db.QueryRow(`
		SELECT COUNT(*) FROM agent_trace_steps
		WHERE NOT EXISTS (
			SELECT 1 FROM agent_trace_chains
			WHERE agent_trace_chains.chain_id = agent_trace_steps.chain_id
		)
	`).Scan(&orphanCount); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphanCount != 0 {
		t.Errorf("orphan steps after migration = %d, want 0", orphanCount)
	}
}

// currentTraceChainColumns is the agent_trace_chains column set of a pre-dedup
// ("current", already migrated) database.
func currentTraceChainColumns() map[string]bool {
	cols := map[string]bool{"chain_id": true}
	for _, name := range []string{
		"started_at", "completed_at", "terminal_status", "terminal_reason",
		"tmux_session", "pane_id", "root_agent_type", "root_event_name", "root_reason",
		"latest_step_kind", "latest_decision", "latest_step_reason", "step_count", "updated_at",
	} {
		cols[name] = true
	}
	return cols
}

// currentTraceStepColumns is the agent_trace_steps column set of a pre-dedup
// ("current", already migrated) database.
func currentTraceStepColumns() map[string]bool {
	cols := map[string]bool{"step_id": true, "chain_id": true, "parent_step_id": true}
	for _, name := range []string{
		"seq", "kind", "tmux_session", "pane_id", "agent_type", "frame_id",
		"parent_frame_id", "event_name", "decision", "reason",
		"payload_json", "before_json", "after_json", "created_at",
	} {
		cols[name] = true
	}
	return cols
}

// TestNeedsChainRebuild_DedupColumnsNotRequired is the spec case 15 regression
// guard. Adding root_payload_json to needsChainRebuild's required list would
// send every current-schema database down the legacy rebuild path, whose chain
// copier reads created_at / agent_type — columns the current table does not
// have — so migration would fail outright.
func TestNeedsChainRebuild_DedupColumnsNotRequired(t *testing.T) {
	if needsChainRebuild(currentTraceChainColumns()) {
		t.Fatal("needsChainRebuild(current schema) = true, want false")
	}
	withDedup := currentTraceChainColumns()
	withDedup["root_payload_json"] = true
	if needsChainRebuild(withDedup) {
		t.Fatal("needsChainRebuild(current schema + root_payload_json) = true, want false")
	}
}

// TestNeedsStepRebuild_DedupColumnsNotRequired is the step-table half of the
// spec case 15 regression guard.
func TestNeedsStepRebuild_DedupColumnsNotRequired(t *testing.T) {
	s := openTestTraceStore(t)
	ctx := context.Background()

	if needsStepRebuild(ctx, currentTraceStepColumns(), s.db) {
		t.Fatal("needsStepRebuild(current schema) = true, want false")
	}
	withDedup := currentTraceStepColumns()
	withDedup["payload_is_root"] = true
	if needsStepRebuild(ctx, withDedup, s.db) {
		t.Fatal("needsStepRebuild(current schema + payload_is_root) = true, want false")
	}
}

// seedPreDedupTraceSchema creates the trace tables exactly as they looked
// before the payload-dedup columns were introduced, and seeds one chain with
// two steps.
func seedPreDedupTraceSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		CREATE TABLE agent_trace_chains (
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
			updated_at          INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		t.Fatalf("create pre-dedup chains: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE agent_trace_steps (
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
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (chain_id, parent_step_id) REFERENCES agent_trace_steps(chain_id, step_id) ON DELETE CASCADE
		)
	`); err != nil {
		t.Fatalf("create pre-dedup steps: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_trace_steps_chain_step ON agent_trace_steps(chain_id, step_id)`); err != nil {
		t.Fatalf("create pre-dedup unique index: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES
		('pre-chain', 10, 20, 'done', 'ok', 'proj-pre', '%3', 'cc', 'Stop', 'root', 'terminal', 'done', 'ok', 2, 20)
	`); err != nil {
		t.Fatalf("seed pre-dedup chain: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at
		) VALUES
		('pre-1', 'pre-chain', NULL, 1, 'trigger', 'proj-pre', '%3', 'cc', 'f1', '', 'Stop', '', '', '{"a":1}', 'null', 'null', 11),
		('pre-2', 'pre-chain', 'pre-1', 2, 'emit', 'proj-pre', '%3', 'cc', 'f1', '', 'Stop', '', '', '{"a":1}', 'null', 'null', 12)
	`); err != nil {
		t.Fatalf("seed pre-dedup steps: %v", err)
	}
}

func traceColumnPresent(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	cols, err := tableColumns(context.Background(), db, table)
	if err != nil {
		t.Fatalf("tableColumns(%s): %v", table, err)
	}
	return cols[column]
}

func assertDedupColumnsPresent(t *testing.T, db *sql.DB) {
	t.Helper()
	if !traceColumnPresent(t, db, "agent_trace_chains", "root_payload_json") {
		t.Error("agent_trace_chains.root_payload_json missing after migration")
	}
	if !traceColumnPresent(t, db, "agent_trace_steps", "payload_is_root") {
		t.Error("agent_trace_steps.payload_is_root missing after migration")
	}
}

// TestMigrateTraceDB_FreshDBHasDedupColumns — spec case 14, fresh DB.
func TestMigrateTraceDB_FreshDBHasDedupColumns(t *testing.T) {
	s := openTestTraceStore(t)
	assertDedupColumnsPresent(t, s.db)
}

// TestMigrateTraceDB_AddsDedupColumnsToCurrentSchema — spec case 14 (current
// schema missing both columns) plus the end-to-end half of case 15: a
// current-schema database carrying data migrates without taking the rebuild
// path (which would fail), keeps its rows, and still saves and reads after.
func TestMigrateTraceDB_AddsDedupColumnsToCurrentSchema(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedPreDedupTraceSchema(t, s.db)

	traces, err := s.Traces()
	if err != nil {
		t.Fatalf("Traces: %v", err)
	}
	assertDedupColumnsPresent(t, s.db)

	got, err := traces.GetChainRecord("pre-chain")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil || len(got.Steps) != 2 {
		t.Fatalf("record after migration = %+v, want 2 steps", got)
	}
	if string(got.Steps[0].PayloadJSON) != `{"a":1}` || string(got.Steps[1].PayloadJSON) != `{"a":1}` {
		t.Fatalf("payloads after migration = %q / %q", got.Steps[0].PayloadJSON, got.Steps[1].PayloadJSON)
	}

	traces.maxChains = 10
	traces.maxSteps = 100
	if err := traces.SaveChain(TraceRecord{
		Chain: TraceChain{ChainID: "post-chain", StartedAt: 30, CompletedAt: 31, TmuxSession: "proj-pre", PaneID: "%3", RootAgentType: "cc", RootEventName: "Stop"},
		Steps: []TraceStep{
			{StepID: "post-1", ChainID: "post-chain", Seq: 1, Kind: "trigger", PayloadJSON: json.RawMessage(`{"b":2}`), CreatedAt: 30},
			{StepID: "post-2", ChainID: "post-chain", ParentStepID: "post-1", Seq: 2, Kind: "emit", PayloadJSON: json.RawMessage(`{"b":2}`), CreatedAt: 31},
		},
	}); err != nil {
		t.Fatalf("SaveChain after migration: %v", err)
	}
	post, err := traces.GetChainRecord("post-chain")
	if err != nil {
		t.Fatalf("GetChainRecord post: %v", err)
	}
	if post == nil || len(post.Steps) != 2 {
		t.Fatalf("post record = %+v, want 2 steps", post)
	}
	for i, step := range post.Steps {
		if string(step.PayloadJSON) != `{"b":2}` {
			t.Fatalf("post step %d payload = %q, want the saved payload", i, step.PayloadJSON)
		}
	}
}

// TestMigrateTraceDB_AddsMissingDedupColumnOnly — spec case 14: only one of the
// two columns already present; migration must fill in the other.
func TestMigrateTraceDB_AddsMissingDedupColumnOnly(t *testing.T) {
	cases := []struct {
		name  string
		alter string
	}{
		{"chain column already present", `ALTER TABLE agent_trace_chains ADD COLUMN root_payload_json TEXT NOT NULL DEFAULT 'null'`},
		{"step column already present", `ALTER TABLE agent_trace_steps ADD COLUMN payload_is_root INTEGER NOT NULL DEFAULT 0`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestAgentEventStore(t)
			seedPreDedupTraceSchema(t, s.db)
			if _, err := s.db.Exec(tc.alter); err != nil {
				t.Fatalf("pre-alter: %v", err)
			}
			if _, err := s.Traces(); err != nil {
				t.Fatalf("Traces: %v", err)
			}
			assertDedupColumnsPresent(t, s.db)
		})
	}
}

// TestMigrateTraceDB_RerunIsNoOp — spec case 14: migration re-run.
func TestMigrateTraceDB_RerunIsNoOp(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedPreDedupTraceSchema(t, s.db)

	for i := 0; i < 3; i++ {
		if _, err := s.Traces(); err != nil {
			t.Fatalf("Traces run %d: %v", i, err)
		}
	}
	assertDedupColumnsPresent(t, s.db)

	var stepCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps`).Scan(&stepCount); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if stepCount != 2 {
		t.Fatalf("step count after re-runs = %d, want 2", stepCount)
	}
}

// TestMigrateTraceDB_LegacyRebuildProducesDedupColumns — spec case 14: a
// genuine legacy schema still goes through the rebuild path and ends with both
// columns.
func TestMigrateTraceDB_LegacyRebuildProducesDedupColumns(t *testing.T) {
	s := openTestAgentEventStore(t)
	seedLegacyTraceSchema(t, s.db)
	seedLegacyTraceData(t, s.db)

	if _, err := s.Traces(); err != nil {
		t.Fatalf("Traces: %v", err)
	}
	assertDedupColumnsPresent(t, s.db)

	var stepCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps`).Scan(&stepCount); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if stepCount != 2 {
		t.Fatalf("step count after legacy rebuild = %d, want 2", stepCount)
	}
}

// ---------------------------------------------------------------------------
// Task 2 — write-side dedup. Every assertion here reads the raw columns via
// SQL, so storage shape is pinned independently of the read path.
// ---------------------------------------------------------------------------

// TestTraceStore_DedupedChainSurvivesRestart is the restart regression. Rerunning
// migration on an empty database proves nothing about data that is already
// deduped, so this writes a deduped chain, closes the store, reopens it (which
// re-runs migrateTraceDB against the on-disk schema), and checks both the raw
// storage and the rehydrated read.
func TestTraceStore_DedupedChainSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	shared := json.RawMessage(`{"tmux_session":"work","result":"big"}`)
	emit := json.RawMessage(`{"agent_type":"cc"}`)

	first, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	traces, err := first.Traces()
	if err != nil {
		t.Fatalf("first Traces: %v", err)
	}
	traces.maxChains = 100
	traces.maxSteps = 1000
	if err := traces.SaveChain(dedupRecord("c",
		dedupStep("c", "s1", "trigger", 1, 10, shared),
		dedupStep("c", "s2", "verify", 2, 20, shared),
		dedupStep("c", "s3", "emit", 3, 30, emit),
	)); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	reopened, err := second.Traces()
	if err != nil {
		t.Fatalf("second Traces: %v", err)
	}
	assertDedupColumnsPresent(t, second.db)
	assertRawShape(t, second.db, "c", `{"tmux_session":"work","result":"big"}`, []rawTraceStep{
		{"s1", "trigger", "", 1},
		{"s2", "verify", "", 1},
		{"s3", "emit", `{"agent_type":"cc"}`, 0},
	})

	got, err := reopened.GetChainRecord("c")
	if err != nil {
		t.Fatalf("GetChainRecord after restart: %v", err)
	}
	if got == nil || len(got.Steps) != 3 {
		t.Fatalf("record after restart = %+v, want 3 steps", got)
	}
	want := []string{`{"tmux_session":"work","result":"big"}`, `{"tmux_session":"work","result":"big"}`, `{"agent_type":"cc"}`}
	for i, w := range want {
		if string(got.Steps[i].PayloadJSON) != w {
			t.Errorf("step %d payload after restart = %q, want %q", i, got.Steps[i].PayloadJSON, w)
		}
	}
}
