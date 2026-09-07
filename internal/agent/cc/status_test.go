package cc_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	cc "github.com/wake/purdex/internal/agent/cc"
)

func deriveViaProvider(purdexName string, rawEvent map[string]any) agent.DeriveResult {
	p := cc.NewProvider(nil, nil, nil, nil)
	raw, _ := json.Marshal(rawEvent)
	return p.DeriveStatus(purdexName, raw)
}

func TestDeriveCCStatus_PdxSessionStart_Idle(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"source": "startup"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxSessionStart_Compact_InvalidWithReason(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"source": "compact"})
	if r.Valid {
		t.Fatal("compact PdxSessionStart should be ignored")
	}
	if r.Reason != "compact_ignored" {
		t.Fatalf("expected reason=compact_ignored, got %q", r.Reason)
	}
}

func TestDeriveCCStatus_PdxNotification_UnknownTypeReason(t *testing.T) {
	r := deriveViaProvider("PdxNotification", map[string]any{"notification_type": "weird"})
	if r.Valid {
		t.Fatal("unknown notification_type should be invalid")
	}
	if r.Reason != "notification_unknown_type" {
		t.Fatalf("expected reason=notification_unknown_type, got %q", r.Reason)
	}
}

func TestDeriveCCStatus_UnknownEventEmptyReason(t *testing.T) {
	r := deriveViaProvider("PdxFutureEvent", map[string]any{})
	if r.Valid {
		t.Fatal("unknown event should be invalid")
	}
	if r.Reason != "" {
		t.Fatalf("expected empty Reason for unknown event name, got %q", r.Reason)
	}
}

func TestDeriveCCStatus_PdxUserPromptSubmit_Running(t *testing.T) {
	r := deriveViaProvider("PdxUserPromptSubmit", map[string]any{})
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxNotification_PermissionPrompt_Waiting(t *testing.T) {
	r := deriveViaProvider("PdxNotification", map[string]any{"notification_type": "permission_prompt"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxNotification_IdlePrompt_Idle(t *testing.T) {
	r := deriveViaProvider("PdxNotification", map[string]any{"notification_type": "idle_prompt"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxPermissionRequest_Waiting(t *testing.T) {
	r := deriveViaProvider("PdxPermissionRequest", map[string]any{"tool_name": "Bash"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Fatalf("expected tool_name Bash in detail")
	}
}

// TestDeriveCCStatus_PdxPostToolUse_Running asserts the W6-1a contract:
// PostToolUse fires after a tool completes — including after a permission
// grant — and must flip status to running so the waiting → running transition
// has an actual hook signal between Notification(permission_prompt) and the
// eventual Stop.
func TestDeriveCCStatus_PdxPostToolUse_Running(t *testing.T) {
	r := deriveViaProvider("PdxPostToolUse", map[string]any{"tool_name": "Bash"})
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Fatalf("expected tool_name Bash in detail")
	}
}

func TestDeriveCCStatus_PdxStop_Idle(t *testing.T) {
	r := deriveViaProvider("PdxStop", map[string]any{"last_assistant_message": "Done"})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxStopFailure_Error(t *testing.T) {
	r := deriveViaProvider("PdxStopFailure", map[string]any{"error": "OOM"})
	if !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("expected error, got %+v", r)
	}
}

// TestDeriveCCStatus_PdxStopFailure_AgentIdInDetail pins the spec §3.1
// contract: cc PdxStopFailure derive must surface raw["agent_id"] into
// Detail so downstream applyFrameEvent can pre-check ref presence and
// detach the matching native SubagentRef on rate-limit storms.
func TestDeriveCCStatus_PdxStopFailure_AgentIdInDetail(t *testing.T) {
	r := deriveViaProvider("PdxStopFailure", map[string]any{
		"agent_id": "aac56e6312afceb04",
		"error":    "rate_limit",
	})
	if !r.Valid {
		t.Fatalf("expected Valid=true, got %+v", r)
	}
	if r.Detail["agent_id"] != "aac56e6312afceb04" {
		t.Fatalf("Detail[agent_id] = %#v, want aac56e6312afceb04", r.Detail["agent_id"])
	}
}

// TestDeriveCCStatus_PdxStopFailure_AgentIdMissing locks down the nil-safe
// branch: when raw payload omits agent_id, Detail["agent_id"] must be nil
// (key set, value nil) so handler's type assertion `agentID, _ := ...(string)`
// yields "" and the legacy break path fires.
func TestDeriveCCStatus_PdxStopFailure_AgentIdMissing(t *testing.T) {
	r := deriveViaProvider("PdxStopFailure", map[string]any{
		"error": "rate_limit",
	})
	if !r.Valid {
		t.Fatalf("expected Valid=true, got %+v", r)
	}
	if r.Detail["agent_id"] != nil {
		t.Fatalf("Detail[agent_id] = %#v, want nil for missing key", r.Detail["agent_id"])
	}
}

func TestDeriveCCStatus_PdxSessionEnd_Clear(t *testing.T) {
	r := deriveViaProvider("PdxSessionEnd", map[string]any{})
	if !r.Valid || r.Status != agent.StatusClear {
		t.Fatalf("expected clear, got %+v", r)
	}
}

func TestDeriveCCStatus_PdxSubagentStart_DetailOnly(t *testing.T) {
	r := deriveViaProvider("PdxSubagentStart", map[string]any{"agent_id": "abc"})
	if !r.Valid {
		t.Fatal("PdxSubagentStart should be valid")
	}
	if r.Status != "" {
		t.Fatalf("PdxSubagentStart should not set status, got %s", r.Status)
	}
}

// TestDeriveCCStatus_PdxPreToolUse_DetailOnly pins the round-1 codex review fix:
// production cc provider must classify PdxPreToolUse as Valid=true (detail-only)
// so handler.go reaches the broadcast path. Before the fix, this returned
// Valid=false, so the delegation flag mark would mutate the frame DB but never
// emit to the SPA. Mirrors the existing PdxSubagentStart detail-only pattern.
func TestDeriveCCStatus_PdxPreToolUse_DetailOnly(t *testing.T) {
	r := deriveViaProvider("PdxPreToolUse", map[string]any{
		"tool_name":   "Bash",
		"tool_use_id": "T1",
		"agent_id":    "agent-X",
	})
	if !r.Valid {
		t.Fatalf("PdxPreToolUse should be valid (detail-only); got %+v", r)
	}
	if r.Status != "" {
		t.Fatalf("PdxPreToolUse should not set status (detail-only); got %s", r.Status)
	}
	if r.Detail == nil {
		t.Fatal("PdxPreToolUse should carry Detail map")
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Errorf("Detail[tool_name] = %v, want Bash", r.Detail["tool_name"])
	}
	if r.Detail["tool_use_id"] != "T1" {
		t.Errorf("Detail[tool_use_id] = %v, want T1", r.Detail["tool_use_id"])
	}
	if r.Detail["agent_id"] != "agent-X" {
		t.Errorf("Detail[agent_id] = %v, want agent-X", r.Detail["agent_id"])
	}
}

// TestDeriveCCStatus_PdxPostToolUseFailure_DetailOnly is the sister pin for the
// unmark broadcast path. Same rationale as PdxPreToolUse — without the fix the
// production cc provider returned Valid=false and the unmark would silently
// mutate DB without broadcasting cleared state.
func TestDeriveCCStatus_PdxPostToolUseFailure_DetailOnly(t *testing.T) {
	r := deriveViaProvider("PdxPostToolUseFailure", map[string]any{
		"tool_name":   "Bash",
		"tool_use_id": "T2",
		"agent_id":    "agent-Y",
	})
	if !r.Valid {
		t.Fatalf("PdxPostToolUseFailure should be valid (detail-only); got %+v", r)
	}
	if r.Status != "" {
		t.Fatalf("PdxPostToolUseFailure should not set status (detail-only); got %s", r.Status)
	}
	if r.Detail == nil {
		t.Fatal("PdxPostToolUseFailure should carry Detail map")
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Errorf("Detail[tool_name] = %v, want Bash", r.Detail["tool_name"])
	}
	if r.Detail["tool_use_id"] != "T2" {
		t.Errorf("Detail[tool_use_id] = %v, want T2", r.Detail["tool_use_id"])
	}
	if r.Detail["agent_id"] != "agent-Y" {
		t.Errorf("Detail[agent_id] = %v, want agent-Y", r.Detail["agent_id"])
	}
}

func TestDeriveCCStatus_PdxModelExtraction(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"source": "startup", "modelName": "opus-4"})
	if r.Model != "opus-4" {
		t.Fatalf("expected model opus-4, got %s", r.Model)
	}
}

// TestDeriveCCStatus_LegacySessionStart_Invalid asserts the pre-W2 raw
// event-name literal "SessionStart" is no longer a catalog match for cc.
// Phase 1's daemon path falls back through isLegacyHookForUnmigrated only
// for codex / opencode; for cc, a legacy literal is genuinely a catalog
// miss and DeriveStatus returns Valid=false with empty Reason.
func TestDeriveCCStatus_LegacySessionStart_Invalid(t *testing.T) {
	for _, legacy := range []string{"SessionStart", "UserPromptSubmit", "Stop", "SessionEnd", "Notification"} {
		r := deriveViaProvider(legacy, map[string]any{})
		if r.Valid {
			t.Errorf("legacy %q must not be valid post-rename", legacy)
		}
	}
}

// TestDeriveCCStatus_SessionStart_CarriesProvenance pins the observed cc
// SessionStart payload (spec 3.1): session_id and cwd sit at the top level of
// raw_event and must reach Detail so the frame layer can build the rebuild
// record.
func TestDeriveCCStatus_SessionStart_CarriesProvenance(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{
		"session_id": "441c80d5",
		"cwd":        "/w/csp",
		"source":     "startup",
		"modelName":  "opus",
	})
	if !r.Valid {
		t.Fatalf("Valid = false, got %+v", r)
	}
	if r.Detail["session_id"] != "441c80d5" {
		t.Fatalf("session_id = %v", r.Detail["session_id"])
	}
	if r.Detail["cwd"] != "/w/csp" {
		t.Fatalf("cwd = %v", r.Detail["cwd"])
	}
}

// TestDeriveCCStatus_SessionStart_OmitsAbsentKeys guards the wire shape: an
// absent key must stay absent from Detail rather than present with a nil
// value, which would serialize as "session_id": null.
func TestDeriveCCStatus_SessionStart_OmitsAbsentKeys(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"source": "startup"})
	if _, ok := r.Detail["session_id"]; ok {
		t.Fatalf("session_id present for a payload that has none: %+v", r.Detail)
	}
	if _, ok := r.Detail["cwd"]; ok {
		t.Fatalf("cwd present for a payload that has none: %+v", r.Detail)
	}
}

// TestDeriveCCStatus_SessionStart_CompactStillIgnored keeps the cc-only
// compact early return above the provenance passthrough.
func TestDeriveCCStatus_SessionStart_CompactStillIgnored(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"source": "compact", "session_id": "x"})
	if r.Valid {
		t.Fatalf("compact must stay Valid=false, got %+v", r)
	}
}
