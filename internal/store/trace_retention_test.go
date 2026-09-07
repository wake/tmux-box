package store

// Retention coverage: pruneTraceChains evicting whole chains when the chain or
// step cap is exceeded, and the cascade to their steps.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
)

func TestTraceStore_RetentionDropsOldestWholeChainWhenStepCapExceeded(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 3

	first := TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-a",
			StartedAt:      1,
			CompletedAt:    2,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "a",
		},
		Steps: []TraceStep{
			{StepID: "a-1", ChainID: "chain-a", Seq: 1, Kind: "root", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 1},
			{StepID: "a-2", ChainID: "chain-a", ParentStepID: "a-1", Seq: 2, Kind: "decision", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 2},
		},
	}
	second := TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-b",
			StartedAt:      3,
			CompletedAt:    4,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "b",
		},
		Steps: []TraceStep{
			{StepID: "b-1", ChainID: "chain-b", Seq: 1, Kind: "root", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 3},
			{StepID: "b-2", ChainID: "chain-b", ParentStepID: "b-1", Seq: 2, Kind: "decision", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 4},
		},
	}

	if err := s.SaveChain(first); err != nil {
		t.Fatalf("SaveChain first: %v", err)
	}
	if err := s.SaveChain(second); err != nil {
		t.Fatalf("SaveChain second: %v", err)
	}

	gotA, err := s.GetChainRecord("chain-a")
	if err != nil {
		t.Fatalf("GetChainRecord a: %v", err)
	}
	if gotA != nil {
		t.Fatalf("expected chain-a to be evicted, got %+v", gotA.Chain)
	}

	gotB, err := s.GetChainRecord("chain-b")
	if err != nil {
		t.Fatalf("GetChainRecord b: %v", err)
	}
	if gotB == nil {
		t.Fatal("expected chain-b to remain")
	}
	if len(gotB.Steps) != 2 {
		t.Fatalf("chain-b steps = %d, want 2", len(gotB.Steps))
	}
}

func TestTraceStore_RetentionDropsOldestWholeChainWhenChainCapExceeded(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 2
	s.maxSteps = 100

	for i := 0; i < 3; i++ {
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:        fmt.Sprintf("chain-%d", i),
				StartedAt:      int64(i + 1),
				CompletedAt:    int64(i + 2),
				TerminalStatus: "done",
				TmuxSession:    "proj-a",
				PaneID:         "%5",
				RootAgentType:  "cc",
				RootEventName:  "Stop",
				RootReason:     fmt.Sprintf("reason-%d", i),
			},
			Steps: []TraceStep{
				{StepID: fmt.Sprintf("step-%d", i), ChainID: fmt.Sprintf("chain-%d", i), Seq: 1, Kind: "terminal", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i + 1)},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	got0, err := s.GetChainRecord("chain-0")
	if err != nil {
		t.Fatalf("GetChainRecord chain-0: %v", err)
	}
	if got0 != nil {
		t.Fatalf("expected chain-0 to be evicted, got %+v", got0.Chain)
	}
}

// TestTraceStore_PruneCascadesStepsToChainsAfterEviction verifies that when
// pruneTraceChains evicts old chains, their steps are also removed via
// ON DELETE CASCADE. Saves more chains than maxChains so prune fires, then
// asserts no orphan steps remain.
//
// Why: without foreign_keys pragma active on the connection that executes the
// prune DELETE, SQLite silently skips the cascade — leaving steps referencing
// deleted chains (orphans). This was the root cause of the 18 GB DB bloat
// (137,550 steps vs 10,000 chains).
func TestTraceStore_PruneCascadesStepsToChainsAfterEviction(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 5
	s.maxSteps = 1000

	// Save 20 chains, each with 3 steps → 60 steps total.
	// After prune: ≤5 chains remain; all surviving steps must reference a
	// surviving chain.
	for i := 0; i < 20; i++ {
		chainID := fmt.Sprintf("cascade-chain-%02d", i)
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:          chainID,
				StartedAt:        int64(i + 1),
				CompletedAt:      int64(i + 2),
				TerminalStatus:   "done",
				TerminalReason:   "ok",
				TmuxSession:      "proj-cascade",
				PaneID:           "%7",
				RootAgentType:    "cc",
				RootEventName:    "Stop",
				RootReason:       "bootstrap",
				LatestStepKind:   "terminal",
				LatestDecision:   "done",
				LatestStepReason: "ok",
			},
			Steps: []TraceStep{
				{StepID: fmt.Sprintf("s%02d-1", i), ChainID: chainID, Seq: 1, Kind: "root", TmuxSession: "proj-cascade", PaneID: "%7", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 1)},
				{StepID: fmt.Sprintf("s%02d-2", i), ChainID: chainID, ParentStepID: fmt.Sprintf("s%02d-1", i), Seq: 2, Kind: "decision", TmuxSession: "proj-cascade", PaneID: "%7", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 2)},
				{StepID: fmt.Sprintf("s%02d-3", i), ChainID: chainID, ParentStepID: fmt.Sprintf("s%02d-2", i), Seq: 3, Kind: "terminal", TmuxSession: "proj-cascade", PaneID: "%7", AgentType: "cc", EventName: "Stop", CreatedAt: int64(i*10 + 3)},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	var chainCount, stepCount, orphans int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_trace_chains").Scan(&chainCount); err != nil {
		t.Fatalf("count chains: %v", err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM agent_trace_steps").Scan(&stepCount); err != nil {
		t.Fatalf("count steps: %v", err)
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
		t.Errorf("orphan steps = %d (stepCount=%d, chainCount=%d): ON DELETE CASCADE did not fire — foreign_keys pragma missing on prune connection", orphans, stepCount, chainCount)
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
