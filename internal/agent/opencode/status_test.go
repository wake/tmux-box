package opencode_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
)

func deriveViaProvider(eventName string, rawEvent map[string]any) agent.DeriveResult {
	p := opencode.NewProvider()
	raw, _ := json.Marshal(rawEvent)
	return p.DeriveStatus(eventName, raw)
}

func TestOpenCodeDeriveStatus_PdxSessionStart(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
}

func TestOpenCodeDeriveStatus_PdxUserPromptSubmit(t *testing.T) {
	r := deriveViaProvider("PdxUserPromptSubmit", map[string]any{"modelName": "openai/gpt-5.4"})
	if !r.Valid || r.Status != agent.StatusRunning {
		t.Fatalf("expected running, got %+v", r)
	}
	if r.Model != "openai/gpt-5.4" {
		t.Fatalf("expected model extraction, got %+v", r)
	}
}

func TestOpenCodeDeriveStatus_PdxPermissionRequest(t *testing.T) {
	r := deriveViaProvider("PdxPermissionRequest", map[string]any{"request_type": "permission", "permission": "bash"})
	if !r.Valid || r.Status != agent.StatusWaiting {
		t.Fatalf("expected waiting, got %+v", r)
	}
	if r.Detail["permission"] != "bash" {
		t.Fatalf("expected detail.permission bash, got %+v", r.Detail)
	}
}

func TestOpenCodeDeriveStatus_PdxSubagentStart(t *testing.T) {
	r := deriveViaProvider("PdxSubagentStart", map[string]any{"agent_id": "call-1", "agent_type": "Explore"})
	if !r.Valid {
		t.Fatal("PdxSubagentStart should be valid")
	}
	if r.Status != "" {
		t.Fatalf("PdxSubagentStart should not set status, got %q", r.Status)
	}
	if r.Detail["agent_id"] != "call-1" {
		t.Fatalf("agent_id = %#v, want call-1", r.Detail["agent_id"])
	}
}

func TestOpenCodeDeriveStatus_PdxSubagentStop(t *testing.T) {
	r := deriveViaProvider("PdxSubagentStop", map[string]any{"agent_id": "call-1", "agent_type": "Explore", "title": "done"})
	if !r.Valid {
		t.Fatal("PdxSubagentStop should be valid")
	}
	if r.Status != "" {
		t.Fatalf("PdxSubagentStop should not set status, got %q", r.Status)
	}
	if r.Detail["agent_type"] != "Explore" {
		t.Fatalf("agent_type = %#v, want Explore", r.Detail["agent_type"])
	}
}

func TestOpenCodeDeriveStatus_PdxStop(t *testing.T) {
	r := deriveViaProvider("PdxStop", map[string]any{})
	if !r.Valid || r.Status != agent.StatusIdle {
		t.Fatalf("expected idle, got %+v", r)
	}
	if r.Detail["notification_silent"] != true {
		t.Fatalf("notification_silent = %#v, want true", r.Detail["notification_silent"])
	}
	if r.Detail["stop_source"] != "session.status.idle" {
		t.Fatalf("stop_source = %#v, want session.status.idle", r.Detail["stop_source"])
	}
}

func TestOpenCodeDeriveStatus_PdxStopFailure(t *testing.T) {
	r := deriveViaProvider("PdxStopFailure", map[string]any{"error": "provider_error", "error_details": "boom"})
	if !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("expected error, got %+v", r)
	}
	if r.Detail["error_details"] != "boom" {
		t.Fatalf("expected error details, got %+v", r.Detail)
	}
}

// TestOpenCodeDeriveStatus_PdxStopFailure_AgentIdInDetail mirrors spec §3.1
// for opencode: even though opencode plugin_template currently does not
// emit agent_id in its session.error payload, the derive contract is pinned
// for parity so the day the plugin adds it, native detach activates without
// further daemon code change. detailSubset sets the key only when the raw
// payload carries it.
func TestOpenCodeDeriveStatus_PdxStopFailure_AgentIdInDetail(t *testing.T) {
	r := deriveViaProvider("PdxStopFailure", map[string]any{
		"agent_id": "opencode-sub-1",
		"error":    "rate_limit",
	})
	if !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("expected error, got %+v", r)
	}
	if r.Detail["agent_id"] != "opencode-sub-1" {
		t.Fatalf("Detail[agent_id] = %#v, want opencode-sub-1", r.Detail["agent_id"])
	}
}

// TestOpenCodeDeriveStatus_PdxStopFailure_AgentIdMissing pins the
// detailSubset semantics: when raw payload omits agent_id, the key is
// absent from Detail (not present-with-nil). Handler-side type assertion
// `Detail["agent_id"].(string)` still yields "" via the zero-value branch
// so behaviour is identical to cc/codex's nil-key case.
func TestOpenCodeDeriveStatus_PdxStopFailure_AgentIdMissing(t *testing.T) {
	r := deriveViaProvider("PdxStopFailure", map[string]any{
		"error": "rate_limit",
	})
	if !r.Valid || r.Status != agent.StatusError {
		t.Fatalf("expected error, got %+v", r)
	}
	if _, present := r.Detail["agent_id"]; present {
		t.Fatalf("expected Detail[agent_id] absent for missing key (detailSubset omits absent keys), got %#v", r.Detail["agent_id"])
	}
}

func TestOpenCodeDeriveStatus_PdxSessionEnd(t *testing.T) {
	r := deriveViaProvider("PdxSessionEnd", map[string]any{})
	if !r.Valid || r.Status != agent.StatusClear {
		t.Fatalf("expected clear, got %+v", r)
	}
}

func TestOpenCodeDeriveStatus_UnknownEvent(t *testing.T) {
	r := deriveViaProvider("PdxNotification", map[string]any{})
	if r.Valid {
		t.Fatalf("unexpected valid result: %+v", r)
	}
}

// TestOpenCodeDeriveStatus_LegacyNames_Invalid pins the post-P3 boundary:
// pre-W2 legacy literals (raw upstream Bus event names that the plugin used
// to emit verbatim before the PURDEX_EVENT const refactor) no longer hit the
// catalog. Mirrors cc/codex equivalents (P1-T8 / P2-T2). After P3-T6 removes
// the lifecycle fallback path entirely these will surface as
// event_not_in_catalog at the handler.
func TestOpenCodeDeriveStatus_LegacyNames_Invalid(t *testing.T) {
	legacy := []string{
		"SessionStart",
		"UserPromptSubmit",
		"SubagentStart",
		"SubagentStop",
		"PermissionRequest",
		"Stop",
		"StopFailure",
		"SessionEnd",
	}
	for _, name := range legacy {
		r := deriveViaProvider(name, map[string]any{})
		if r.Valid {
			t.Errorf("opencode legacy literal %q: DeriveStatus returned Valid=true; want Valid=false (post-P3 catalog only accepts PdxXxx)", name)
		}
	}
}

// TestOpenCodeDeriveStatus_SessionStart_CarriesProvenance — the plugin emits
// session_id today and cwd from Task 3 on; both pass through Detail.
func TestOpenCodeDeriveStatus_SessionStart_CarriesProvenance(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{
		"session_id": "ses_abc123",
		"cwd":        "/w/purdex",
	})
	if !r.Valid {
		t.Fatalf("Valid = false, got %+v", r)
	}
	if r.Detail["session_id"] != "ses_abc123" {
		t.Fatalf("session_id = %v", r.Detail["session_id"])
	}
	if r.Detail["cwd"] != "/w/purdex" {
		t.Fatalf("cwd = %v", r.Detail["cwd"])
	}
}

// TestOpenCodeDeriveStatus_SessionStart_OmitsAbsentKeys — the plugin does not
// emit cwd until Task 3, so the key must be absent rather than null.
func TestOpenCodeDeriveStatus_SessionStart_OmitsAbsentKeys(t *testing.T) {
	r := deriveViaProvider("PdxSessionStart", map[string]any{"session_id": "ses_abc123"})
	if _, ok := r.Detail["cwd"]; ok {
		t.Fatalf("cwd present for a payload that has none: %+v", r.Detail)
	}
}
