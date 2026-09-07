package store

// Root-payload dedup: which steps get merged onto the chain row, and how the
// read path puts the payload back.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// dedupTestStore returns a store with limits high enough that pruning never
// interferes with dedup assertions.
func dedupTestStore(t *testing.T) *TraceStore {
	t.Helper()
	s := openTestTraceStore(t)
	s.maxChains = 1000
	s.maxSteps = 10000
	return s
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
