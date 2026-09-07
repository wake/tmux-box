package opencode

import (
	"encoding/json"

	"github.com/wake/purdex/internal/agent"
)

func deriveOpenCodeStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	var raw map[string]any
	_ = json.Unmarshal(rawEvent, &raw)

	switch eventName {
	case "PdxSessionStart":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle, Detail: agent.DetailStrings(raw, "session_id", "cwd")}
	case "PdxUserPromptSubmit":
		return agent.DeriveResult{Valid: true, Status: agent.StatusRunning, Model: strVal(raw, "modelName")}
	case "PdxSubagentStart", "PdxSubagentStop":
		return agent.DeriveResult{Valid: true, Detail: detailSubset(raw, "agent_id", "agent_type", "description", "prompt", "title", "output")}
	case "PdxPermissionRequest":
		return agent.DeriveResult{Valid: true, Status: agent.StatusWaiting, Detail: detailSubset(raw, "request_type", "permission", "patterns", "questions")}
	case "PdxStop":
		return agent.DeriveResult{Valid: true, Status: agent.StatusIdle, Detail: map[string]any{
			"notification_silent": true,
			"stop_source":          "session.status.idle",
		}}
	case "PdxStopFailure":
		// agent_id surfaces the failing subagent's identity for parity
		// with cc/codex. opencode plugin_template currently does NOT
		// emit agent_id on session.error so detailSubset omits the key
		// in practice — derive contract is pinned for the day the
		// plugin adds it (no daemon-side change required when that
		// happens).
		return agent.DeriveResult{Valid: true, Status: agent.StatusError, Detail: detailSubset(raw, "error", "error_details", "agent_id")}
	case "PdxSessionEnd":
		return agent.DeriveResult{Valid: true, Status: agent.StatusClear}
	default:
		return agent.DeriveResult{Valid: false}
	}
}

func detailSubset(raw map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			out[key] = value
		}
	}
	return out
}

func strVal(raw map[string]any, key string) string {
	if value, ok := raw[key].(string); ok {
		return value
	}
	return ""
}
