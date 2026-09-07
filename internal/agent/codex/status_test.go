package codex_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

func deriveViaProvider(eventName string) agent.DeriveResult {
	p := codex.NewProvider()
	return p.DeriveStatus(eventName, json.RawMessage(`{}`))
}

func deriveWithRaw(eventName, rawJSON string) agent.DeriveResult {
	p := codex.NewProvider()
	return p.DeriveStatus(eventName, json.RawMessage(rawJSON))
}

func TestCodexDeriveStatus_PdxSessionStart(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart")
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestCodexDeriveStatus_PdxUserPromptSubmit(t *testing.T) {
	r := deriveViaProvider("PdxUserPromptSubmit")
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
}

func TestCodexDeriveStatus_PdxStop(t *testing.T) {
	r := deriveViaProvider("PdxStop")
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

// TestCodexDeriveStatus_UnknownEvent is the post-Phase-1 smoke test for
// unknown events. Notification is now a known event (see CD1-CD3), so we
// switch to a synthetic future-event name that no provider claims.
func TestCodexDeriveStatus_UnknownEvent(t *testing.T) {
	r := deriveViaProvider("FutureEvent")
	if r.Valid {
		t.Fatal("Codex should not handle unknown future events")
	}
}

// CD1: Notification(permission_prompt) → Waiting + detail
func TestCodexDeriveStatus_PdxNotificationPermission(t *testing.T) {
	r := deriveWithRaw("PdxNotification", `{"notification_type":"permission_prompt","message":"hi"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != agent.StatusWaiting {
		t.Fatalf("status = %q, want waiting", r.Status)
	}
	if r.Detail["notification_type"] != "permission_prompt" {
		t.Fatalf("detail.notification_type = %v, want permission_prompt", r.Detail["notification_type"])
	}
	if r.Detail["message"] != "hi" {
		t.Fatalf("detail.message = %v, want hi", r.Detail["message"])
	}
}

// CD1b: Notification(elicitation_dialog) → Waiting (mirrors cc)
func TestCodexDeriveStatus_PdxNotificationElicitation(t *testing.T) {
	r := deriveWithRaw("PdxNotification", `{"notification_type":"elicitation_dialog"}`)
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
}

// CD2: Notification(idle_prompt) → Idle
func TestCodexDeriveStatus_PdxNotificationIdle(t *testing.T) {
	r := deriveWithRaw("PdxNotification", `{"notification_type":"idle_prompt"}`)
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

// CD2b: Notification(auth_success) → Idle (mirrors cc)
func TestCodexDeriveStatus_PdxNotificationAuthSuccess(t *testing.T) {
	r := deriveWithRaw("PdxNotification", `{"notification_type":"auth_success"}`)
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

// CD3: Notification(unknown subtype) → Valid=false + Reason=notification_unknown_type
// (so handler can distinguish payload-schema drift from truly unknown event names).
func TestCodexDeriveStatus_PdxNotificationUnknown(t *testing.T) {
	r := deriveWithRaw("PdxNotification", `{"notification_type":"weird"}`)
	if r.Valid {
		t.Fatalf("expected Valid=false for unknown notification subtype, got %+v", r)
	}
	if r.Reason != "notification_unknown_type" {
		t.Fatalf("expected reason=notification_unknown_type, got %q", r.Reason)
	}
}

// Truly unknown event names (not in catalog) must leave Reason empty so the
// handler can fall back to the generic "event_not_in_catalog" classification.
func TestCodexDeriveStatus_UnknownEventEmptyReason(t *testing.T) {
	r := deriveViaProvider("FutureMysteryEvent")
	if r.Valid {
		t.Fatalf("expected Valid=false, got %+v", r)
	}
	if r.Reason != "" {
		t.Fatalf("expected empty Reason for unknown event name, got %q", r.Reason)
	}
}

// CD4: PermissionRequest → Waiting + detail.tool_name
func TestCodexDeriveStatus_PdxPermissionRequest(t *testing.T) {
	r := deriveWithRaw("PdxPermissionRequest", `{"tool_name":"Bash"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != agent.StatusWaiting {
		t.Fatalf("status = %q, want waiting", r.Status)
	}
	if r.Detail["tool_name"] != "Bash" {
		t.Fatalf("detail.tool_name = %v, want Bash", r.Detail["tool_name"])
	}
}

// CD5: SubagentStart → Valid=true, Status="" (detail-only), detail.agent_id
func TestCodexDeriveStatus_PdxSubagentStart(t *testing.T) {
	r := deriveWithRaw("PdxSubagentStart", `{"agent_id":"abc"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != "" {
		t.Fatalf("status = %q, want empty (detail-only event)", r.Status)
	}
	if r.Detail["agent_id"] != "abc" {
		t.Fatalf("detail.agent_id = %v, want abc", r.Detail["agent_id"])
	}
}

// CD6: SubagentStop → Valid=true, Status=""
func TestCodexDeriveStatus_PdxSubagentStop(t *testing.T) {
	r := deriveWithRaw("PdxSubagentStop", `{"agent_id":"xyz"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != "" {
		t.Fatalf("status = %q, want empty (detail-only event)", r.Status)
	}
	if r.Detail["agent_id"] != "xyz" {
		t.Fatalf("detail.agent_id = %v, want xyz", r.Detail["agent_id"])
	}
}

// CD7: SessionEnd → Clear
func TestCodexDeriveStatus_PdxSessionEnd(t *testing.T) {
	r := deriveWithRaw("PdxSessionEnd", `{}`)
	if !r.Valid || r.Status != agent.StatusClear {
		t.Fatalf("expected clear, got %+v", r)
	}
}

// CD8: StopFailure → Error + detail.error
func TestCodexDeriveStatus_PdxStopFailure(t *testing.T) {
	r := deriveWithRaw("PdxStopFailure", `{"error":"OOM","error_details":"out of memory"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Status != agent.StatusError {
		t.Fatalf("status = %q, want error", r.Status)
	}
	if r.Detail["error"] != "OOM" {
		t.Fatalf("detail.error = %v, want OOM", r.Detail["error"])
	}
	if r.Detail["error_details"] != "out of memory" {
		t.Fatalf("detail.error_details = %v, want 'out of memory'", r.Detail["error_details"])
	}
}

// TestCodexDeriveStatus_PdxStopFailure_AgentIdInDetail mirrors cc spec §3.1
// for codex provider: raw["agent_id"] must surface into Detail so the
// handler-side native detach branch can read the failing subagent ID.
func TestCodexDeriveStatus_PdxStopFailure_AgentIdInDetail(t *testing.T) {
	r := deriveWithRaw("PdxStopFailure", `{"agent_id":"codex-sub-1","error":"rate_limit"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Detail["agent_id"] != "codex-sub-1" {
		t.Fatalf("Detail[agent_id] = %#v, want codex-sub-1", r.Detail["agent_id"])
	}
}

// TestCodexDeriveStatus_PdxStopFailure_AgentIdMissing locks down nil-safe.
func TestCodexDeriveStatus_PdxStopFailure_AgentIdMissing(t *testing.T) {
	r := deriveWithRaw("PdxStopFailure", `{"error":"rate_limit"}`)
	if !r.Valid {
		t.Fatalf("expected Valid, got %+v", r)
	}
	if r.Detail["agent_id"] != nil {
		t.Fatalf("Detail[agent_id] = %#v, want nil for missing key", r.Detail["agent_id"])
	}
}

// TestDeriveCodexStatus_PdxPreToolUse asserts PdxPreToolUse returns
// Valid=true with Status="" so that handler.go does not early-return on the
// Valid=false branch and the new LifecycleUserPromptSubmit case in
// applyFrameEvent can run for codex non-prompt turns (spec §3.3.C strategy a).
func TestDeriveCodexStatus_PdxPreToolUse(t *testing.T) {
	r := deriveWithRaw("PdxPreToolUse", `{"hook":"PdxPreToolUse"}`)
	if !r.Valid {
		t.Fatalf("expected Valid=true, got %+v", r)
	}
	if r.Status != "" {
		t.Fatalf("expected empty Status (detail-only), got %q", r.Status)
	}
}

// TestCodexDeriveStatus_LegacyNames_Invalid asserts the pre-W2 raw
// event-name literals are no longer a catalog match for codex post-P2-T1.
// Phase 2's daemon path falls back through isLegacyHookForUnmigrated only
// for opencode; for codex, a legacy literal is genuinely a catalog miss
// (Valid=false, empty Reason). P2-T5 then strips codex from the predicate.
func TestCodexDeriveStatus_LegacyNames_Invalid(t *testing.T) {
	for _, legacy := range []string{"SessionStart", "UserPromptSubmit", "Stop", "SessionEnd", "Notification", "PermissionRequest", "StopFailure", "SubagentStart", "SubagentStop"} {
		r := deriveViaProvider(legacy)
		if r.Valid {
			t.Errorf("legacy %q must not be valid post-rename", legacy)
		}
	}
}

// TestCodexDeriveStatus_SessionStart_CarriesProvenance pins the observed codex
// SessionStart payload (spec 3.1).
func TestCodexDeriveStatus_SessionStart_CarriesProvenance(t *testing.T) {
	r := deriveWithRaw("PdxSessionStart", `{"session_id":"01a07ace","cwd":"/w/purdex","hook_event_name":"SessionStart","model":"gpt-6-astra","permission_mode":"bypassPermissions","source":"startup"}`)
	if !r.Valid {
		t.Fatalf("Valid = false, got %+v", r)
	}
	if r.Detail["session_id"] != "01a07ace" {
		t.Fatalf("session_id = %v", r.Detail["session_id"])
	}
	if r.Detail["cwd"] != "/w/purdex" {
		t.Fatalf("cwd = %v", r.Detail["cwd"])
	}
}

// TestCodexDeriveStatus_SessionStart_OmitsAbsentKeys — an absent key stays
// absent so nothing serializes as null on the wire.
func TestCodexDeriveStatus_SessionStart_OmitsAbsentKeys(t *testing.T) {
	r := deriveWithRaw("PdxSessionStart", `{"source":"startup"}`)
	if _, ok := r.Detail["session_id"]; ok {
		t.Fatalf("session_id present for a payload that has none: %+v", r.Detail)
	}
	if _, ok := r.Detail["cwd"]; ok {
		t.Fatalf("cwd present for a payload that has none: %+v", r.Detail)
	}
}
