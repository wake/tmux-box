package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Task 1 — schema + migration for the payload-dedup columns
// ---------------------------------------------------------------------------

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

// rawTraceStep is one agent_trace_steps row as actually stored.
type rawTraceStep struct {
	StepID  string
	Kind    string
	Payload string
	IsRoot  int
}

func readRawTraceSteps(t *testing.T, db *sql.DB, chainID string) []rawTraceStep {
	t.Helper()
	rows, err := db.Query(`
		SELECT step_id, kind, payload_json, payload_is_root
		FROM agent_trace_steps
		WHERE chain_id = ?
		ORDER BY seq ASC, created_at ASC, step_id ASC
	`, chainID)
	if err != nil {
		t.Fatalf("read raw steps: %v", err)
	}
	defer rows.Close()

	var out []rawTraceStep
	for rows.Next() {
		var step rawTraceStep
		if err := rows.Scan(&step.StepID, &step.Kind, &step.Payload, &step.IsRoot); err != nil {
			t.Fatalf("scan raw step: %v", err)
		}
		out = append(out, step)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("raw steps rows: %v", err)
	}
	return out
}

func readRawTraceRoot(t *testing.T, db *sql.DB, chainID string) string {
	t.Helper()
	var root string
	if err := db.QueryRow(`SELECT root_payload_json FROM agent_trace_chains WHERE chain_id = ?`, chainID).Scan(&root); err != nil {
		t.Fatalf("read raw root: %v", err)
	}
	return root
}

// assertRawShape pins both the chain's stored root payload and every step row.
func assertRawShape(t *testing.T, db *sql.DB, chainID, wantRoot string, want []rawTraceStep) {
	t.Helper()
	if got := readRawTraceRoot(t, db, chainID); got != wantRoot {
		t.Errorf("root_payload_json = %q, want %q", got, wantRoot)
	}
	got := readRawTraceSteps(t, db, chainID)
	if len(got) != len(want) {
		t.Fatalf("stored steps = %d (%+v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d stored as %+v, want %+v", i, got[i], want[i])
		}
	}
}

// dedupTestStore returns a store with limits high enough that pruning never
// interferes with dedup assertions.
func dedupTestStore(t *testing.T) *TraceStore {
	t.Helper()
	s := openTestTraceStore(t)
	s.maxChains = 1000
	s.maxSteps = 10000
	return s
}

// dedupStep builds a step with only the fields these tests care about. A nil
// payload is passed through untouched so the nil / empty conventions can be
// exercised.
func dedupStep(chainID, stepID, kind string, seq int, createdAt int64, payload json.RawMessage) TraceStep {
	return TraceStep{
		StepID:      stepID,
		ChainID:     chainID,
		Seq:         seq,
		Kind:        kind,
		TmuxSession: "proj-dedup",
		PaneID:      "%1",
		AgentType:   "cc",
		EventName:   "PdxPostToolUse",
		PayloadJSON: payload,
		CreatedAt:   createdAt,
	}
}

func dedupRecord(chainID string, steps ...TraceStep) TraceRecord {
	return TraceRecord{
		Chain: TraceChain{
			ChainID:       chainID,
			StartedAt:     1,
			CompletedAt:   2,
			TmuxSession:   "proj-dedup",
			PaneID:        "%1",
			RootAgentType: "cc",
			RootEventName: "PdxPostToolUse",
		},
		Steps: steps,
	}
}

// TestSaveChain_DedupsSharedRootPayload — spec case 1. The real hook chain
// shape: trigger / verify / frame / projection carry one identical payload and
// emit carries its own.
func TestSaveChain_DedupsSharedRootPayload(t *testing.T) {
	s := dedupTestStore(t)
	shared := json.RawMessage(`{"tmux_session":"work","result":"big"}`)
	emit := json.RawMessage(`{"agent_type":"cc"}`)

	if err := s.SaveChain(dedupRecord("c",
		dedupStep("c", "s1", "trigger", 1, 10, shared),
		dedupStep("c", "s2", "verify", 2, 20, shared),
		dedupStep("c", "s3", "frame", 3, 30, shared),
		dedupStep("c", "s4", "projection", 4, 40, shared),
		dedupStep("c", "s5", "emit", 5, 50, emit),
	)); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	assertRawShape(t, s.db, "c", `{"tmux_session":"work","result":"big"}`, []rawTraceStep{
		{"s1", "trigger", "", 1},
		{"s2", "verify", "", 1},
		{"s3", "frame", "", 1},
		{"s4", "projection", "", 1},
		{"s5", "emit", `{"agent_type":"cc"}`, 0},
	})
}

// TestSaveChain_AllDistinctPayloadsAreNotDeduped — spec case 2.
func TestSaveChain_AllDistinctPayloadsAreNotDeduped(t *testing.T) {
	s := dedupTestStore(t)
	if err := s.SaveChain(dedupRecord("c",
		dedupStep("c", "s1", "trigger", 1, 10, json.RawMessage(`{"a":1}`)),
		dedupStep("c", "s2", "verify", 2, 20, json.RawMessage(`{"b":2}`)),
		dedupStep("c", "s3", "emit", 3, 30, json.RawMessage(`{"c":3}`)),
	)); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	assertRawShape(t, s.db, "c", "null", []rawTraceStep{
		{"s1", "trigger", `{"a":1}`, 0},
		{"s2", "verify", `{"b":2}`, 0},
		{"s3", "emit", `{"c":3}`, 0},
	})
}

// TestSaveChain_PartialDedup — spec case 3. Only the root payload is deduped:
// [A,B,B] collapses nothing, [A,A,B,B] collapses only the A pair.
func TestSaveChain_PartialDedup(t *testing.T) {
	a := json.RawMessage(`{"p":"a"}`)
	b := json.RawMessage(`{"p":"b"}`)

	t.Run("A,B,B dedups nothing", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, a),
			dedupStep("c", "s2", "verify", 2, 20, b),
			dedupStep("c", "s3", "emit", 3, 30, b),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", "null", []rawTraceStep{
			{"s1", "trigger", `{"p":"a"}`, 0},
			{"s2", "verify", `{"p":"b"}`, 0},
			{"s3", "emit", `{"p":"b"}`, 0},
		})
	})

	t.Run("A,A,B,B dedups only the A pair", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, a),
			dedupStep("c", "s2", "verify", 2, 20, a),
			dedupStep("c", "s3", "frame", 3, 30, b),
			dedupStep("c", "s4", "emit", 4, 40, b),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", `{"p":"a"}`, []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "", 1},
			{"s3", "frame", `{"p":"b"}`, 0},
			{"s4", "emit", `{"p":"b"}`, 0},
		})
	})
}

// TestSaveChain_TwoStepAndSingleStepChains — spec case 4. The "verify rejected"
// two-step shape dedups; a genuine single-step chain does not, and must not
// gain a copy of its payload on the chain row.
func TestSaveChain_TwoStepAndSingleStepChains(t *testing.T) {
	payload := json.RawMessage(`{"tmux_session":"work"}`)

	t.Run("two-step trigger+verify dedups", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, payload),
			dedupStep("c", "s2", "verify", 2, 20, payload),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", `{"tmux_session":"work"}`, []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "", 1},
		})
	})

	t.Run("single-step probe-intent chain does not dedup", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "probe-intent", 1, 10, payload),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", "null", []rawTraceStep{
			{"s1", "probe-intent", `{"tmux_session":"work"}`, 0},
		})
	})
}

// TestSaveChain_ByteFidelity — spec case 5. Merging is by stored bytes only:
// semantically equal payloads that differ in key order or whitespace keep their
// own copies.
func TestSaveChain_ByteFidelity(t *testing.T) {
	t.Run("byte-different variants never merge", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, json.RawMessage(`{"a":1,"b":2}`)),
			dedupStep("c", "s2", "verify", 2, 20, json.RawMessage(`{"b":2,"a":1}`)),
			dedupStep("c", "s3", "emit", 3, 30, json.RawMessage(`{"a":1, "b":2}`)),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", "null", []rawTraceStep{
			{"s1", "trigger", `{"a":1,"b":2}`, 0},
			{"s2", "verify", `{"b":2,"a":1}`, 0},
			{"s3", "emit", `{"a":1, "b":2}`, 0},
		})
	})

	t.Run("byte-identical pair merges, near-miss stays inline", func(t *testing.T) {
		s := dedupTestStore(t)
		exact := json.RawMessage(`{"a":1,"b":2}`)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, exact),
			dedupStep("c", "s2", "verify", 2, 20, exact),
			dedupStep("c", "s3", "emit", 3, 30, json.RawMessage(`{"b":2,"a":1}`)),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", `{"a":1,"b":2}`, []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "", 1},
			{"s3", "emit", `{"b":2,"a":1}`, 0},
		})
	})
}

// TestSaveChain_OutOfOrderStepsPickReadOrderRoot — spec case 6. The root
// candidate is the step the read path shows first, not the first one the caller
// happened to pass. Supplying them in reverse would pick B (a single occurrence,
// no dedup) if input order were used.
func TestSaveChain_OutOfOrderStepsPickReadOrderRoot(t *testing.T) {
	s := dedupTestStore(t)
	a := json.RawMessage(`{"p":"a"}`)
	b := json.RawMessage(`{"p":"b"}`)

	if err := s.SaveChain(dedupRecord("c",
		dedupStep("c", "s3", "emit", 3, 30, b),
		dedupStep("c", "s1", "trigger", 1, 10, a),
		dedupStep("c", "s2", "verify", 2, 20, a),
	)); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	assertRawShape(t, s.db, "c", `{"p":"a"}`, []rawTraceStep{
		{"s1", "trigger", "", 1},
		{"s2", "verify", "", 1},
		{"s3", "emit", `{"p":"b"}`, 0},
	})
}

// TestSaveChain_SeqDefaultsAndTieBreaks — spec case 7. Seq is not unique, so the
// created_at and step_id tie-breaks decide which step is the root candidate.
func TestSaveChain_SeqDefaultsAndTieBreaks(t *testing.T) {
	a := json.RawMessage(`{"p":"a"}`)
	b := json.RawMessage(`{"p":"b"}`)

	t.Run("Seq 0 defaults to input position", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 0, 10, a),
			dedupStep("c", "s2", "verify", 0, 20, a),
			dedupStep("c", "s3", "emit", 0, 30, b),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", `{"p":"a"}`, []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "", 1},
			{"s3", "emit", `{"p":"b"}`, 0},
		})
	})

	t.Run("equal Seq breaks on created_at", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s3", "emit", 5, 30, b),
			dedupStep("c", "s1", "trigger", 5, 10, a),
			dedupStep("c", "s2", "verify", 5, 20, a),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", `{"p":"a"}`, []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "", 1},
			{"s3", "emit", `{"p":"b"}`, 0},
		})
	})

	t.Run("equal Seq and created_at break on step_id", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s-z", "emit", 5, 10, b),
			dedupStep("c", "s-a", "trigger", 5, 10, a),
			dedupStep("c", "s-b", "verify", 5, 10, a),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", `{"p":"a"}`, []rawTraceStep{
			{"s-a", "trigger", "", 1},
			{"s-b", "verify", "", 1},
			{"s-z", "emit", `{"p":"b"}`, 0},
		})
	})
}

// TestSaveChain_EmptyPayloadConventions — spec case 8. rawJSONText maps nil and
// an empty RawMessage to the string "null"; a literal null and {} are stored as
// written. None of them may ever be confused with the ” dedup marker, and a
// root payload that happens to be "null" must still be driven by the per-step
// flag, not by the chain value.
func TestSaveChain_EmptyPayloadConventions(t *testing.T) {
	t.Run("zero steps", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c")); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", "null", nil)
	})

	t.Run("nil and empty RawMessage store as null and dedup on the flag", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, nil),
			dedupStep("c", "s2", "verify", 2, 20, json.RawMessage{}),
			dedupStep("c", "s3", "emit", 3, 30, json.RawMessage(`{"x":1}`)),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		// root_payload_json is the literal string "null" here — identical to the
		// column default — so correctness can only come from payload_is_root.
		assertRawShape(t, s.db, "c", "null", []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "", 1},
			{"s3", "emit", `{"x":1}`, 0},
		})
	})

	t.Run("single null payload stays inline as null, not as the marker", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, nil),
			dedupStep("c", "s2", "verify", 2, 20, json.RawMessage(`{}`)),
			dedupStep("c", "s3", "emit", 3, 30, json.RawMessage(`null`)),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		// s1 and s3 both store "null", so they are the two that match — but s1 is
		// the root candidate, so both are flagged and {} stays inline.
		assertRawShape(t, s.db, "c", "null", []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "{}", 0},
			{"s3", "emit", "", 1},
		})
	})

	t.Run("no genuine payload is ever stored as the empty marker", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, json.RawMessage(`{}`)),
			dedupStep("c", "s2", "verify", 2, 20, json.RawMessage(`null`)),
		)); err != nil {
			t.Fatalf("SaveChain: %v", err)
		}
		assertRawShape(t, s.db, "c", "null", []rawTraceStep{
			{"s1", "trigger", "{}", 0},
			{"s2", "verify", "null", 0},
		})
		var bad int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps WHERE payload_json = '' AND payload_is_root = 0`).Scan(&bad); err != nil {
			t.Fatalf("count bad marker rows: %v", err)
		}
		if bad != 0 {
			t.Errorf("%d rows store the '' marker without payload_is_root", bad)
		}
	})
}

// TestSaveChain_ResaveTransitions — spec case 13. root_payload_json is
// recomputed and rewritten on every upsert.
func TestSaveChain_ResaveTransitions(t *testing.T) {
	a := json.RawMessage(`{"p":"a"}`)
	b := json.RawMessage(`{"p":"b"}`)

	t.Run("inline to dedup", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, a),
			dedupStep("c", "s2", "verify", 2, 20, b),
		)); err != nil {
			t.Fatalf("SaveChain inline: %v", err)
		}
		assertRawShape(t, s.db, "c", "null", []rawTraceStep{
			{"s1", "trigger", `{"p":"a"}`, 0},
			{"s2", "verify", `{"p":"b"}`, 0},
		})

		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, a),
			dedupStep("c", "s2", "verify", 2, 20, a),
		)); err != nil {
			t.Fatalf("SaveChain dedup: %v", err)
		}
		assertRawShape(t, s.db, "c", `{"p":"a"}`, []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "", 1},
		})
	})

	t.Run("dedup to inline resets the root", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, a),
			dedupStep("c", "s2", "verify", 2, 20, a),
		)); err != nil {
			t.Fatalf("SaveChain dedup: %v", err)
		}
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, a),
			dedupStep("c", "s2", "verify", 2, 20, b),
		)); err != nil {
			t.Fatalf("SaveChain inline: %v", err)
		}
		assertRawShape(t, s.db, "c", "null", []rawTraceStep{
			{"s1", "trigger", `{"p":"a"}`, 0},
			{"s2", "verify", `{"p":"b"}`, 0},
		})
	})

	t.Run("root A to root B", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, a),
			dedupStep("c", "s2", "verify", 2, 20, a),
		)); err != nil {
			t.Fatalf("SaveChain A: %v", err)
		}
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, b),
			dedupStep("c", "s2", "verify", 2, 20, b),
		)); err != nil {
			t.Fatalf("SaveChain B: %v", err)
		}
		assertRawShape(t, s.db, "c", `{"p":"b"}`, []rawTraceStep{
			{"s1", "trigger", "", 1},
			{"s2", "verify", "", 1},
		})
	})

	t.Run("steps shrinking to empty resets the root", func(t *testing.T) {
		s := dedupTestStore(t)
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "s1", "trigger", 1, 10, a),
			dedupStep("c", "s2", "verify", 2, 20, a),
		)); err != nil {
			t.Fatalf("SaveChain dedup: %v", err)
		}
		if err := s.SaveChain(dedupRecord("c")); err != nil {
			t.Fatalf("SaveChain empty: %v", err)
		}
		assertRawShape(t, s.db, "c", "null", nil)
	})
}

// TestSaveChain_FailedStepInsertLeavesPreviousChainIntact — spec case 16. The
// failure is injected with a step_id that another chain already owns: step_id is
// the table's primary key, so the second INSERT fails *after* the chain root was
// upserted and the first step row was written. (Injecting via a missing or
// cross-chain parent would instead be rejected during normalization, well before
// the transaction opens, and would prove nothing about rollback.)
func TestSaveChain_FailedStepInsertLeavesPreviousChainIntact(t *testing.T) {
	s := dedupTestStore(t)
	a := json.RawMessage(`{"p":"a"}`)
	b := json.RawMessage(`{"p":"b"}`)

	if err := s.SaveChain(dedupRecord("other",
		dedupStep("other", "taken-step", "trigger", 1, 5, json.RawMessage(`{"p":"other"}`)),
	)); err != nil {
		t.Fatalf("SaveChain other: %v", err)
	}
	if err := s.SaveChain(dedupRecord("c",
		dedupStep("c", "s1", "trigger", 1, 10, a),
		dedupStep("c", "s2", "verify", 2, 20, a),
	)); err != nil {
		t.Fatalf("SaveChain A: %v", err)
	}

	err := s.SaveChain(dedupRecord("c",
		dedupStep("c", "s1", "trigger", 1, 10, b),
		dedupStep("c", "taken-step", "verify", 2, 20, b),
	))
	if err == nil {
		t.Fatal("expected SaveChain to fail on the duplicate step_id")
	}

	assertRawShape(t, s.db, "c", `{"p":"a"}`, []rawTraceStep{
		{"s1", "trigger", "", 1},
		{"s2", "verify", "", 1},
	})
	assertRawShape(t, s.db, "other", "null", []rawTraceStep{
		{"taken-step", "trigger", `{"p":"other"}`, 0},
	})
}

// ---------------------------------------------------------------------------
// Task 3 — read-side rehydration
// ---------------------------------------------------------------------------

// TestGetChainRecord_RehydratesDedupedPayloads — spec case 9. Every step must
// read back byte-identical to what was saved, asserted against the saved values
// rather than against the deduped storage.
func TestGetChainRecord_RehydratesDedupedPayloads(t *testing.T) {
	cases := []struct {
		name     string
		payloads []json.RawMessage
		want     []string
	}{
		{
			name: "real hook chain shape",
			payloads: []json.RawMessage{
				json.RawMessage(`{"tmux_session":"work","result":"big"}`),
				json.RawMessage(`{"tmux_session":"work","result":"big"}`),
				json.RawMessage(`{"tmux_session":"work","result":"big"}`),
				json.RawMessage(`{"tmux_session":"work","result":"big"}`),
				json.RawMessage(`{"agent_type":"cc"}`),
			},
			want: []string{
				`{"tmux_session":"work","result":"big"}`,
				`{"tmux_session":"work","result":"big"}`,
				`{"tmux_session":"work","result":"big"}`,
				`{"tmux_session":"work","result":"big"}`,
				`{"agent_type":"cc"}`,
			},
		},
		{
			name:     "nothing deduped",
			payloads: []json.RawMessage{json.RawMessage(`{"a":1}`), json.RawMessage(`{"b":2}`)},
			want:     []string{`{"a":1}`, `{"b":2}`},
		},
		{
			// rawJSONText maps nil and an empty RawMessage to "null", so that is
			// the expected read-back — not the original nil bytes.
			name:     "nil and empty RawMessage read back as null",
			payloads: []json.RawMessage{nil, json.RawMessage{}, json.RawMessage(`{"x":1}`)},
			want:     []string{"null", "null", `{"x":1}`},
		},
		{
			name:     "literal null and empty object",
			payloads: []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(`null`)},
			want:     []string{"null", "{}", "null"},
		},
		{
			name:     "byte-different variants keep their own bytes",
			payloads: []json.RawMessage{json.RawMessage(`{"a":1,"b":2}`), json.RawMessage(`{"a":1,"b":2}`), json.RawMessage(`{"b":2,"a":1}`)},
			want:     []string{`{"a":1,"b":2}`, `{"a":1,"b":2}`, `{"b":2,"a":1}`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := dedupTestStore(t)
			steps := make([]TraceStep, 0, len(tc.payloads))
			for i, payload := range tc.payloads {
				steps = append(steps, dedupStep("c", fmt.Sprintf("s%d", i+1), "step", i+1, int64(10*(i+1)), payload))
			}
			if err := s.SaveChain(dedupRecord("c", steps...)); err != nil {
				t.Fatalf("SaveChain: %v", err)
			}

			got, err := s.GetChainRecord("c")
			if err != nil {
				t.Fatalf("GetChainRecord: %v", err)
			}
			if got == nil || len(got.Steps) != len(tc.want) {
				t.Fatalf("record = %+v, want %d steps", got, len(tc.want))
			}
			for i, want := range tc.want {
				if string(got.Steps[i].PayloadJSON) != want {
					t.Errorf("step %d payload = %q, want %q", i, got.Steps[i].PayloadJSON, want)
				}
			}
		})
	}
}

// TestGetChainRecord_LegacyRowsReadUnchanged — spec case 10. Rows written before
// this change (payload_is_root = 0, inline payload, root_payload_json = 'null')
// are inserted directly via SQL and must read back untouched — no backfill.
func TestGetChainRecord_LegacyRowsReadUnchanged(t *testing.T) {
	s := dedupTestStore(t)

	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES ('legacy', 1, 2, 'done', 'ok', 'proj-legacy', '%2', 'cc', 'Stop', 'root', 'emit', 'done', 'ok', 3, 2)
	`); err != nil {
		t.Fatalf("insert legacy chain: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO agent_trace_steps (
			step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
			agent_type, frame_id, parent_frame_id, event_name, decision, reason,
			payload_json, before_json, after_json, created_at
		) VALUES
		('l1', 'legacy', NULL, 1, 'trigger', 'proj-legacy', '%2', 'cc', 'f', '', 'Stop', '', '', '{"same":true}', 'null', 'null', 10),
		('l2', 'legacy', 'l1', 2, 'verify', 'proj-legacy', '%2', 'cc', 'f', '', 'Stop', '', '', '{"same":true}', 'null', 'null', 20),
		('l3', 'legacy', 'l2', 3, 'emit', 'proj-legacy', '%2', 'cc', 'f', '', 'Stop', '', '', 'null', 'null', 'null', 30)
	`); err != nil {
		t.Fatalf("insert legacy steps: %v", err)
	}

	// The chain row keeps the column default; nothing was deduped.
	if root := readRawTraceRoot(t, s.db, "legacy"); root != "null" {
		t.Fatalf("legacy root_payload_json = %q, want null", root)
	}

	got, err := s.GetChainRecord("legacy")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil || len(got.Steps) != 3 {
		t.Fatalf("record = %+v, want 3 steps", got)
	}
	want := []string{`{"same":true}`, `{"same":true}`, "null"}
	for i, w := range want {
		if string(got.Steps[i].PayloadJSON) != w {
			t.Errorf("legacy step %d payload = %q, want %q", i, got.Steps[i].PayloadJSON, w)
		}
	}
}

// TestGetChainRecord_ReadSaveRoundTrip — spec case 11. Re-saving what
// GetChainRecord returned reproduces the same storage shape and the same
// read-back, and SaveChain does not mutate the caller's payload slices.
func TestGetChainRecord_ReadSaveRoundTrip(t *testing.T) {
	s := dedupTestStore(t)
	shared := json.RawMessage(`{"tmux_session":"work","result":"big"}`)
	sharedCopy := append(json.RawMessage(nil), shared...)
	emit := json.RawMessage(`{"agent_type":"cc"}`)
	emitCopy := append(json.RawMessage(nil), emit...)

	record := dedupRecord("c",
		dedupStep("c", "s1", "trigger", 1, 10, shared),
		dedupStep("c", "s2", "verify", 2, 20, shared),
		dedupStep("c", "s3", "emit", 3, 30, emit),
	)
	if err := s.SaveChain(record); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	if string(shared) != string(sharedCopy) || string(emit) != string(emitCopy) {
		t.Fatalf("SaveChain mutated the caller's payloads: %q / %q", shared, emit)
	}

	first, err := s.GetChainRecord("c")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	wantShape := []rawTraceStep{
		{"s1", "trigger", "", 1},
		{"s2", "verify", "", 1},
		{"s3", "emit", `{"agent_type":"cc"}`, 0},
	}
	assertRawShape(t, s.db, "c", `{"tmux_session":"work","result":"big"}`, wantShape)

	if err := s.SaveChain(*first); err != nil {
		t.Fatalf("re-SaveChain: %v", err)
	}
	assertRawShape(t, s.db, "c", `{"tmux_session":"work","result":"big"}`, wantShape)

	second, err := s.GetChainRecord("c")
	if err != nil {
		t.Fatalf("GetChainRecord again: %v", err)
	}
	if len(second.Steps) != len(first.Steps) {
		t.Fatalf("round trip changed step count: %d -> %d", len(first.Steps), len(second.Steps))
	}
	for i := range first.Steps {
		if string(first.Steps[i].PayloadJSON) != string(second.Steps[i].PayloadJSON) {
			t.Errorf("round trip changed step %d payload: %q -> %q", i, first.Steps[i].PayloadJSON, second.Steps[i].PayloadJSON)
		}
	}
	// The rehydrated slices the caller was handed must survive the re-save too.
	for i, step := range first.Steps {
		if len(step.PayloadJSON) == 0 {
			t.Errorf("step %d payload was emptied by the re-save", i)
		}
	}
}

// interleavingQuerier delegates to a real *sql.DB but runs a hook exactly once,
// immediately before the step query. That is the deterministic seam spec case 12
// needs: a goroutine race cannot reliably land a write between the two reads.
type interleavingQuerier struct {
	db     *sql.DB
	before func()
	once   sync.Once
}

func (q *interleavingQuerier) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return q.db.ExecContext(ctx, query, args...)
}

func (q *interleavingQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "agent_trace_steps") {
		q.once.Do(q.before)
	}
	return q.db.QueryContext(ctx, query, args...)
}

func (q *interleavingQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return q.db.QueryRowContext(ctx, query, args...)
}

// TestGetChainRecord_InterleavedSaveKeepsStepsAndPayloadOneVersion — spec case
// 12. A SaveChain that switches the chain's root payload from A to B lands
// between the chain query and the step query. The steps that come back must be
// paired with their own version's payload; pairing B's step rows with A's root
// payload would be a silent data error.
//
// Only the steps and their payloads are asserted: the chain summary still comes
// from its own earlier query, and the JOIN does not — and is not meant to — make
// the whole TraceRecord a single snapshot.
func TestGetChainRecord_InterleavedSaveKeepsStepsAndPayloadOneVersion(t *testing.T) {
	s := openFileTraceStore(t)
	s.maxChains = 100
	s.maxSteps = 1000

	a := json.RawMessage(`{"p":"a"}`)
	b := json.RawMessage(`{"p":"b"}`)

	if err := s.SaveChain(dedupRecord("c",
		dedupStep("c", "a1", "trigger", 1, 10, a),
		dedupStep("c", "a2", "verify", 2, 20, a),
	)); err != nil {
		t.Fatalf("SaveChain A: %v", err)
	}

	q := &interleavingQuerier{db: s.db, before: func() {
		if err := s.SaveChain(dedupRecord("c",
			dedupStep("c", "b1", "trigger", 1, 10, b),
			dedupStep("c", "b2", "verify", 2, 20, b),
		)); err != nil {
			t.Errorf("interleaved SaveChain B: %v", err)
		}
	}}

	got, err := getTraceChainRecord(context.Background(), q, "c")
	if err != nil {
		t.Fatalf("getTraceChainRecord: %v", err)
	}
	if got == nil || len(got.Steps) != 2 {
		t.Fatalf("record = %+v, want 2 steps", got)
	}
	if got.Steps[0].StepID != "b1" || got.Steps[1].StepID != "b2" {
		t.Fatalf("step ids = %q/%q, want b1/b2", got.Steps[0].StepID, got.Steps[1].StepID)
	}
	for i, step := range got.Steps {
		if string(step.PayloadJSON) != `{"p":"b"}` {
			t.Errorf("step %d (%s) payload = %q, want B's payload — B's steps were paired with A's root", i, step.StepID, step.PayloadJSON)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 4 — restart safety and pruning across mixed chains
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

// seedLegacyDedupFreeChain inserts a chain the way pre-dedup code would have:
// every payload inline, payload_is_root left at 0, root_payload_json left at the
// 'null' default. step_count is set explicitly because pruneTraceChains budgets
// from the chain row, not from a COUNT over the step rows.
func seedLegacyDedupFreeChain(t *testing.T, db *sql.DB, chainID string, startedAt int64, payloads []string) {
	t.Helper()

	if _, err := db.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, started_at, completed_at, terminal_status, terminal_reason,
			tmux_session, pane_id, root_agent_type, root_event_name, root_reason,
			latest_step_kind, latest_decision, latest_step_reason, step_count, updated_at
		) VALUES (?, ?, ?, 'done', 'ok', 'proj-mixed', '%4', 'cc', 'Stop', 'root', 'emit', 'done', 'ok', ?, ?)
	`, chainID, startedAt, startedAt+1, len(payloads), startedAt+1); err != nil {
		t.Fatalf("seed legacy chain %s: %v", chainID, err)
	}
	for i, payload := range payloads {
		if _, err := db.Exec(`
			INSERT INTO agent_trace_steps (
				step_id, chain_id, parent_step_id, seq, kind, tmux_session, pane_id,
				agent_type, frame_id, parent_frame_id, event_name, decision, reason,
				payload_json, before_json, after_json, created_at
			) VALUES (?, ?, NULL, ?, 'step', 'proj-mixed', '%4', 'cc', 'f', '', 'Stop', '', '', ?, 'null', 'null', ?)
		`, fmt.Sprintf("%s-%d", chainID, i+1), chainID, i+1, payload, startedAt*100+int64(i)); err != nil {
			t.Fatalf("seed legacy step %s-%d: %v", chainID, i+1, err)
		}
	}
}

// saveDedupedChain writes a chain whose first two steps share one payload, so it
// is stored deduped.
func saveDedupedChain(t *testing.T, s *TraceStore, chainID string, startedAt int64) {
	t.Helper()
	shared := json.RawMessage(fmt.Sprintf(`{"chain":%q}`, chainID))
	record := dedupRecord(chainID,
		dedupStep(chainID, chainID+"-1", "trigger", 1, startedAt*100, shared),
		dedupStep(chainID, chainID+"-2", "verify", 2, startedAt*100+1, shared),
		dedupStep(chainID, chainID+"-3", "emit", 3, startedAt*100+2, json.RawMessage(`{"emit":true}`)),
	)
	record.Chain.StartedAt = startedAt
	record.Chain.CompletedAt = startedAt + 1
	if err := s.SaveChain(record); err != nil {
		t.Fatalf("SaveChain %s: %v", chainID, err)
	}
}

func assertNoOrphanSteps(t *testing.T, db *sql.DB) {
	t.Helper()
	var orphans int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM agent_trace_steps
		WHERE chain_id NOT IN (SELECT chain_id FROM agent_trace_chains)
	`).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Errorf("orphan steps = %d, want 0", orphans)
	}
}

// TestTraceStore_PruneMixedDedupedAndLegacyChains — spec case 17. Both caps are
// exercised separately, and survivors of either must still rehydrate.
func TestTraceStore_PruneMixedDedupedAndLegacyChains(t *testing.T) {
	t.Run("chain cap", func(t *testing.T) {
		s := openTestTraceStore(t)
		s.maxChains = 3
		s.maxSteps = 10000

		seedLegacyDedupFreeChain(t, s.db, "legacy-1", 1, []string{`{"l":1}`, `{"l":1}`})
		seedLegacyDedupFreeChain(t, s.db, "legacy-2", 2, []string{`{"l":2}`, `{"l":2}`})
		saveDedupedChain(t, s, "dedup-1", 3)
		saveDedupedChain(t, s, "dedup-2", 4)

		var chains int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_chains`).Scan(&chains); err != nil {
			t.Fatalf("count chains: %v", err)
		}
		if chains != 3 {
			t.Fatalf("chains = %d, want 3", chains)
		}
		evicted, err := s.GetChainRecord("legacy-1")
		if err != nil {
			t.Fatalf("GetChainRecord evicted: %v", err)
		}
		if evicted != nil {
			t.Errorf("legacy-1 = %+v, want evicted as the oldest chain", evicted)
		}
		assertNoOrphanSteps(t, s.db)
		assertMixedSurvivorsRehydrate(t, s, "legacy-2", `{"l":2}`, "dedup-2")
	})

	t.Run("step cap", func(t *testing.T) {
		s := openTestTraceStore(t)
		s.maxChains = 1000
		s.maxSteps = 8

		seedLegacyDedupFreeChain(t, s.db, "legacy-1", 1, []string{`{"l":1}`, `{"l":1}`})
		seedLegacyDedupFreeChain(t, s.db, "legacy-2", 2, []string{`{"l":2}`, `{"l":2}`})
		saveDedupedChain(t, s, "dedup-1", 3)
		saveDedupedChain(t, s, "dedup-2", 4)

		var steps int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_trace_steps`).Scan(&steps); err != nil {
			t.Fatalf("count steps: %v", err)
		}
		if steps > 8 {
			t.Fatalf("steps = %d, want <= 8", steps)
		}
		assertNoOrphanSteps(t, s.db)
		assertMixedSurvivorsRehydrate(t, s, "legacy-2", `{"l":2}`, "dedup-2")
	})
}

func assertMixedSurvivorsRehydrate(t *testing.T, s *TraceStore, legacyID, legacyPayload, dedupedID string) {
	t.Helper()

	legacy, err := s.GetChainRecord(legacyID)
	if err != nil {
		t.Fatalf("GetChainRecord %s: %v", legacyID, err)
	}
	if legacy == nil || len(legacy.Steps) != 2 {
		t.Fatalf("legacy survivor = %+v, want 2 steps", legacy)
	}
	for i, step := range legacy.Steps {
		if string(step.PayloadJSON) != legacyPayload {
			t.Errorf("legacy survivor step %d payload = %q, want %q", i, step.PayloadJSON, legacyPayload)
		}
	}

	deduped, err := s.GetChainRecord(dedupedID)
	if err != nil {
		t.Fatalf("GetChainRecord %s: %v", dedupedID, err)
	}
	if deduped == nil || len(deduped.Steps) != 3 {
		t.Fatalf("deduped survivor = %+v, want 3 steps", deduped)
	}
	wantShared := fmt.Sprintf(`{"chain":%q}`, dedupedID)
	for i, want := range []string{wantShared, wantShared, `{"emit":true}`} {
		if string(deduped.Steps[i].PayloadJSON) != want {
			t.Errorf("deduped survivor step %d payload = %q, want %q", i, deduped.Steps[i].PayloadJSON, want)
		}
	}
}
