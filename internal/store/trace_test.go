package store

// Core trace store behaviour: save/read round trip, cursor pagination, and
// step-parent validation.

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestTraceStore_SaveAndGetChainRecord(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 10

	record := TraceRecord{
		Chain: TraceChain{
			ChainID:          "chain-1",
			StepCount:        99,
			StartedAt:        100,
			CompletedAt:      200,
			TerminalStatus:   "done",
			TerminalReason:   "completed",
			TmuxSession:      "proj-a",
			PaneID:           "%5",
			RootAgentType:    "cc",
			RootEventName:    "SessionStart",
			RootReason:       "bootstrap",
			LatestStepKind:   "decision",
			LatestDecision:   "continue",
			LatestStepReason: "ready",
		},
		Steps: []TraceStep{
			{
				StepID:      "step-1",
				ChainID:     "chain-1",
				Seq:         1,
				Kind:        "decision",
				TmuxSession: "proj-a",
				PaneID:      "%5",
				AgentType:   "cc",
				FrameID:     "frame-1",
				EventName:   "SessionStart",
				Decision:    "continue",
				Reason:      "ready",
				PayloadJSON: json.RawMessage(`{"status":"queued"}`),
				BeforeJSON:  json.RawMessage(`{"before":true}`),
				AfterJSON:   json.RawMessage(`{"after":true}`),
				CreatedAt:   101,
			},
			{
				StepID:        "step-2",
				ChainID:       "chain-1",
				ParentStepID:  "step-1",
				Seq:           2,
				Kind:          "terminal",
				TmuxSession:   "proj-a",
				PaneID:        "%5",
				AgentType:     "cc",
				FrameID:       "frame-1",
				ParentFrameID: "frame-0",
				EventName:     "Stop",
				Decision:      "done",
				Reason:        "completed",
				PayloadJSON:   json.RawMessage(`{"status":"done"}`),
				CreatedAt:     102,
			},
		},
	}

	if err := s.SaveChain(record); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	got, err := s.GetChainRecord("chain-1")
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got == nil {
		t.Fatal("expected record, got nil")
	}
	if got.Chain.StepCount != 2 {
		t.Fatalf("step_count = %d, want 2", got.Chain.StepCount)
	}
	if got.Chain.TerminalStatus != "done" {
		t.Fatalf("terminal_status = %q, want done", got.Chain.TerminalStatus)
	}
	if got.Chain.RootAgentType != "cc" || got.Chain.LatestDecision != "done" || got.Chain.LatestStepKind != "terminal" {
		t.Fatalf("chain summary = %+v", got.Chain)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(got.Steps))
	}
	if got.Steps[0].Seq != 1 || got.Steps[0].Kind != "decision" {
		t.Fatalf("first step = %+v", got.Steps[0])
	}
	if got.Steps[1].ParentStepID != "step-1" {
		t.Fatalf("parent_step_id = %q, want step-1", got.Steps[1].ParentStepID)
	}
	if string(got.Steps[0].BeforeJSON) != `{"before":true}` || string(got.Steps[0].AfterJSON) != `{"after":true}` {
		t.Fatalf("payload fields = %+v", got.Steps[0])
	}
}

func TestTraceStore_ListChains_PaginatesWithCursorAndBefore(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 20

	for i := 0; i < 4; i++ {
		record := TraceRecord{
			Chain: TraceChain{
				ChainID:          fmt.Sprintf("chain-%d", i),
				StartedAt:        int64(100 + i),
				CompletedAt:      int64(200 + i),
				TerminalStatus:   "done",
				TerminalReason:   "ok",
				TmuxSession:      "proj-a",
				PaneID:           "%5",
				RootAgentType:    "cc",
				RootEventName:    "Stop",
				RootReason:       "bootstrap",
				LatestStepKind:   "terminal",
				LatestDecision:   "done",
				LatestStepReason: "ok",
			},
			Steps: []TraceStep{
				{
					StepID:      fmt.Sprintf("step-%d", i),
					ChainID:     fmt.Sprintf("chain-%d", i),
					Seq:         1,
					Kind:        "terminal",
					TmuxSession: "proj-a",
					PaneID:      "%5",
					AgentType:   "cc",
					EventName:   "Stop",
					Decision:    "done",
					Reason:      "ok",
					CreatedAt:   int64(100 + i),
				},
			},
		}
		if err := s.SaveChain(record); err != nil {
			t.Fatalf("SaveChain %d: %v", i, err)
		}
	}

	page1, err := s.ListChains(TraceListFilter{
		TmuxSession: "proj-a",
		PaneID:      "%5",
		AgentType:   "cc",
		EventName:   "Stop",
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("ListChains page1: %v", err)
	}
	if len(page1.Chains) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1.Chains))
	}
	if page1.Chains[0].ChainID != "chain-3" || page1.Chains[1].ChainID != "chain-2" {
		t.Fatalf("page1 chains = [%s, %s]", page1.Chains[0].ChainID, page1.Chains[1].ChainID)
	}
	if page1.NextCursor == "" {
		t.Fatal("expected next cursor")
	}

	page2, err := s.ListChains(TraceListFilter{
		TmuxSession: "proj-a",
		PaneID:      "%5",
		AgentType:   "cc",
		EventName:   "Stop",
		Limit:       2,
		Cursor:      page1.NextCursor,
		Before:      true,
	})
	if err != nil {
		t.Fatalf("ListChains page2: %v", err)
	}
	if len(page2.Chains) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2.Chains))
	}
	if page2.Chains[0].ChainID != "chain-1" || page2.Chains[1].ChainID != "chain-0" {
		t.Fatalf("page2 chains = [%s, %s]", page2.Chains[0].ChainID, page2.Chains[1].ChainID)
	}
}

func TestTraceStore_RejectsCrossChainParentStep(t *testing.T) {
	s := openTestTraceStore(t)
	s.maxChains = 10
	s.maxSteps = 10

	if err := s.SaveChain(TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-a",
			StartedAt:      1,
			CompletedAt:    2,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "root",
		},
		Steps: []TraceStep{
			{StepID: "a-1", ChainID: "chain-a", Seq: 1, Kind: "root", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 1},
		},
	}); err != nil {
		t.Fatalf("SaveChain chain-a: %v", err)
	}

	err := s.SaveChain(TraceRecord{
		Chain: TraceChain{
			ChainID:        "chain-b",
			StartedAt:      3,
			CompletedAt:    4,
			TerminalStatus: "done",
			TmuxSession:    "proj-a",
			PaneID:         "%5",
			RootAgentType:  "cc",
			RootEventName:  "Stop",
			RootReason:     "root",
		},
		Steps: []TraceStep{
			{StepID: "b-1", ChainID: "chain-b", ParentStepID: "a-1", Seq: 1, Kind: "decision", TmuxSession: "proj-a", PaneID: "%5", AgentType: "cc", EventName: "Stop", CreatedAt: 3},
		},
	})
	if err == nil {
		t.Fatal("expected cross-chain parent step to fail")
	}
}
