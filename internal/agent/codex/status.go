package codex

import (
	"encoding/json"

	"github.com/wake/purdex/internal/agent"
)

func deriveCodexStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	var raw map[string]any
	_ = json.Unmarshal(rawEvent, &raw)

	switch eventName {
	case "PdxSessionStart":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusIdle,
			Detail: agent.DetailStrings(raw, "session_id", "cwd"),
		}

	case "PdxUserPromptSubmit":
		return agent.DeriveResult{Valid: true, Status: agent.StatusRunning}

	case "PdxNotification":
		// Mirror cc/status.go subtype mapping; codex hooks share the pdx
		// hook CLI schema, so notification_type values align.
		nt := strVal(raw, "notification_type")
		var status agent.Status
		switch nt {
		case "permission_prompt", "elicitation_dialog":
			status = agent.StatusWaiting
		case "idle_prompt", "auth_success":
			status = agent.StatusIdle
		default:
			return agent.DeriveResult{Valid: false, Reason: "notification_unknown_type"}
		}
		return agent.DeriveResult{
			Valid:  true,
			Status: status,
			Detail: map[string]any{
				"notification_type": nt,
				"message":           raw["message"],
			},
		}

	case "PdxPermissionRequest":
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusWaiting,
			Detail: map[string]any{
				"tool_name": raw["tool_name"],
			},
		}

	case "PdxStop":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle}

	case "PdxStopFailure":
		// agent_id surfaces the failing subagent's identity for parity
		// with cc; nil-safe pass-through, downstream pre-checks before
		// mutating native ref list.
		return agent.DeriveResult{
			Valid:  true,
			Status: agent.StatusError,
			Detail: map[string]any{
				"error_details": raw["error_details"],
				"error":         raw["error"],
				"agent_id":      raw["agent_id"],
			},
		}

	case "PdxSessionEnd":
		return agent.DeriveResult{Valid: true, Status: agent.StatusClear}

	case "PdxSubagentStart", "PdxSubagentStop":
		// Detail-only event: Valid=true, Status="" (mirrors cc/opencode).
		return agent.DeriveResult{
			Valid:  true,
			Detail: map[string]any{"agent_id": raw["agent_id"]},
		}

	case "PdxPreToolUse":
		// L2: detail-only Valid=true so handler.go does not early-return on
		// the Valid=false branch; the new LifecycleUserPromptSubmit case in
		// applyFrameEvent then attaches the codex broker proxy ref for
		// non-prompt turns (spec §3.3.C strategy a).
		return agent.DeriveResult{Valid: true}
	}

	return agent.DeriveResult{Valid: false}
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
