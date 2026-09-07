package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/wake/purdex/internal/store"
)

// dedupChainRecord is the real hook chain shape: trigger / verify / frame /
// projection all carry the identical EventRequest payload, and emit carries a
// genuinely different one. This is exactly the shape the store dedups.
//
// IDs and timestamps are fixed so the whole response is byte-stable.
func dedupChainRecord() store.TraceRecord {
	shared := json.RawMessage(`{"tmux_session":"work","tool":"Read","result":"file contents"}`)
	step := func(id, parent, kind string, seq int, createdAt int64, payload json.RawMessage) store.TraceStep {
		return store.TraceStep{
			StepID:        id,
			ChainID:       "dedup-chain",
			ParentStepID:  parent,
			Seq:           seq,
			Kind:          kind,
			TmuxSession:   "work",
			PaneID:        "%7",
			AgentType:     "codex",
			FrameID:       "frame-1",
			ParentFrameID: "",
			EventName:     "PdxPostToolUse",
			Decision:      "accepted",
			Reason:        "hook_post",
			PayloadJSON:   payload,
			BeforeJSON:    json.RawMessage(`null`),
			AfterJSON:     json.RawMessage(`{"accepted":true}`),
			CreatedAt:     createdAt,
		}
	}
	return store.TraceRecord{
		Chain: store.TraceChain{
			ChainID:          "dedup-chain",
			StartedAt:        100,
			CompletedAt:      140,
			TerminalStatus:   "completed",
			TerminalReason:   "emit_broadcasted",
			TmuxSession:      "work",
			PaneID:           "%7",
			RootAgentType:    "codex",
			RootEventName:    "PdxPostToolUse",
			RootReason:       "hook_post",
			LatestStepKind:   "emit",
			LatestDecision:   "broadcasted",
			LatestStepReason: "session_code_resolved",
		},
		Steps: []store.TraceStep{
			step("s1", "", "trigger", 1, 100, shared),
			step("s2", "s1", "verify", 2, 110, shared),
			step("s3", "s2", "frame", 3, 120, shared),
			step("s4", "s3", "projection", 4, 130, shared),
			step("s5", "s4", "emit", 5, 140, json.RawMessage(`{"status":"running"}`)),
		},
	}
}

// dedupChainGoldenResponse is the exact GET /api/agent/monitor/chains/{id}
// response body recorded from the pre-dedup implementation. Dedup must not
// change a single byte of it — including the trailing newline the JSON encoder
// writes. It is a frozen literal on purpose: deriving it from GetChainRecord or
// the DTO builder would let both sides drift together.
const dedupChainGoldenResponse = `{"chain":{"chain_id":"dedup-chain","started_at":100,"completed_at":140,"terminal_status":"completed","terminal_reason":"emit_broadcasted","tmux_session":"work","pane_id":"%7","root_agent_type":"codex","root_event_name":"PdxPostToolUse","root_reason":"hook_post","latest_step_kind":"emit","latest_decision":"accepted","latest_step_reason":"hook_post","step_count":5},"step_tree":[{"step":{"step_id":"s1","chain_id":"dedup-chain","seq":1,"kind":"trigger","tmux_session":"work","pane_id":"%7","agent_type":"codex","frame_id":"frame-1","event_name":"PdxPostToolUse","decision":"accepted","reason":"hook_post","payload_json":"{\"tmux_session\":\"work\",\"tool\":\"Read\",\"result\":\"file contents\"}","before_json":"null","after_json":"{\"accepted\":true}","created_at":100},"children":[{"step":{"step_id":"s2","chain_id":"dedup-chain","parent_step_id":"s1","seq":2,"kind":"verify","tmux_session":"work","pane_id":"%7","agent_type":"codex","frame_id":"frame-1","event_name":"PdxPostToolUse","decision":"accepted","reason":"hook_post","payload_json":"{\"tmux_session\":\"work\",\"tool\":\"Read\",\"result\":\"file contents\"}","before_json":"null","after_json":"{\"accepted\":true}","created_at":110},"children":[{"step":{"step_id":"s3","chain_id":"dedup-chain","parent_step_id":"s2","seq":3,"kind":"frame","tmux_session":"work","pane_id":"%7","agent_type":"codex","frame_id":"frame-1","event_name":"PdxPostToolUse","decision":"accepted","reason":"hook_post","payload_json":"{\"tmux_session\":\"work\",\"tool\":\"Read\",\"result\":\"file contents\"}","before_json":"null","after_json":"{\"accepted\":true}","created_at":120},"children":[{"step":{"step_id":"s4","chain_id":"dedup-chain","parent_step_id":"s3","seq":4,"kind":"projection","tmux_session":"work","pane_id":"%7","agent_type":"codex","frame_id":"frame-1","event_name":"PdxPostToolUse","decision":"accepted","reason":"hook_post","payload_json":"{\"tmux_session\":\"work\",\"tool\":\"Read\",\"result\":\"file contents\"}","before_json":"null","after_json":"{\"accepted\":true}","created_at":130},"children":[{"step":{"step_id":"s5","chain_id":"dedup-chain","parent_step_id":"s4","seq":5,"kind":"emit","tmux_session":"work","pane_id":"%7","agent_type":"codex","frame_id":"frame-1","event_name":"PdxPostToolUse","decision":"accepted","reason":"hook_post","payload_json":"{\"status\":\"running\"}","before_json":"null","after_json":"{\"accepted\":true}","created_at":140},"children":[]}]}]}]}]}]}
`

// TestHandleMonitorChain_DedupedChainResponseBytesUnchanged is spec case 18.
// The fixture is a chain that the store dedups (four byte-identical payloads
// plus a distinct emit); the assertion is the full response body, so every
// step's rehydrated payload is covered, not just the root's.
func TestHandleMonitorChain_DedupedChainResponseBytesUnchanged(t *testing.T) {
	m := newTestModule(t)
	if err := m.traces.SaveChain(dedupChainRecord()); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent/monitor/chains/dedup-chain", nil)
	req.SetPathValue("id", "dedup-chain")
	w := httptest.NewRecorder()

	m.handleMonitorChain(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if dump := os.Getenv("PDX_DUMP_GOLDEN"); dump != "" {
		if err := os.WriteFile(dump, w.Body.Bytes(), 0o644); err != nil {
			t.Fatalf("dump golden: %v", err)
		}
	}
	if got := w.Body.String(); got != dedupChainGoldenResponse {
		t.Fatalf("response body changed.\n got: %s\nwant: %s", got, dedupChainGoldenResponse)
	}
}
