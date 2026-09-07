package store

// Fixtures shared by more than one trace store test file: the store
// constructors, and the readers used to assert raw stored shape.
//
// A fixture used by exactly one file lives in that file instead.

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func openTestTraceStore(t *testing.T) *TraceStore {
	t.Helper()
	events := openTestAgentEventStore(t)
	traces, err := events.Traces()
	if err != nil {
		t.Fatalf("traces: %v", err)
	}
	return traces
}

// openFileTraceStore opens a file-backed AgentEventStore in t.TempDir() and
// warms the pool to maxConns concurrent connections before returning
// the TraceStore. This exercises the pool-PRAGMA path that :memory: skips.
func openFileTraceStore(t *testing.T) *TraceStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := OpenAgentEvent(path)
	if err != nil {
		t.Fatalf("OpenAgentEvent: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// Warm 4 idle pool connections so subsequent migration/ops fan out.
	ctx := context.Background()
	conns := make([]*sql.Conn, 4)
	for i := range conns {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("db.Conn warm %d: %v", i, err)
		}
		conns[i] = c
	}
	for _, c := range conns {
		_ = c.Close()
	}

	ts, err := s.Traces()
	if err != nil {
		t.Fatalf("Traces: %v", err)
	}
	ts.maxChains = 10
	ts.maxSteps = 1000
	return ts
}

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
