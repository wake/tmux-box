package agent

import agentpkg "github.com/wake/purdex/internal/agent"

// Provenance is the self-contained rebuild-record envelope. It is deliberately
// separate from NormalizedEvent.AgentType: on a proxy-collapsed event the
// outer type names the session projection winner, which may be a different
// agent in a different tmux pane (frame_ops.go:1129, :1170-1182). Consumers of
// the rebuild record read ONLY this struct. See spec §4.3.1.
type Provenance struct {
	OwnerSessionStart bool   `json:"owner_session_start"`
	AgentType         string `json:"agent_type"`
	SessionID         string `json:"session_id,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	TmuxPaneID        string `json:"tmux_pane_id"`
	TmuxInstance      string `json:"tmux_instance"`
}

// buildProvenance assembles the envelope from the request that produced the
// frame mutation plus the derive result that carries session_id / cwd. The
// caller is responsible for the ownership gate; reaching here means the
// mutation outcome already confirmed the sender kept its own top-level frame.
func buildProvenance(req EventRequest, result agentpkg.DeriveResult, tmuxInstance string) Provenance {
	return Provenance{
		OwnerSessionStart: true,
		AgentType:         req.AgentType,
		SessionID:         strFromDetail(result.Detail, "session_id"),
		Cwd:               strFromDetail(result.Detail, "cwd"),
		TmuxPaneID:        req.TmuxPaneID,
		TmuxInstance:      tmuxInstance,
	}
}

// attachProvenance copies the envelope onto the outgoing normalized event's
// Detail map when — and only when — applyFrameEvent granted one. Nil is the
// fail-safe: every return path that did not confirm ownership leaves
// FrameTraceMeta.Provenance at its zero value, so no field is written.
func attachProvenance(normalized *agentpkg.NormalizedEvent, meta FrameTraceMeta) {
	if normalized == nil || meta.Provenance == nil {
		return
	}
	if normalized.Detail == nil {
		normalized.Detail = map[string]any{}
	}
	normalized.Detail["pdx_provenance"] = *meta.Provenance
}

// sessionTmuxInstance reports the tmux generation this daemon is currently
// serving, or "" when the session provider is not wired (test setups, a
// half-initialized module). "" is rejected by the SPA's envelope parser, so a
// half-wired daemon writes no rebuild record rather than a wrong one.
func (m *Module) sessionTmuxInstance() string {
	if m == nil || m.sessions == nil {
		return ""
	}
	return m.sessions.TmuxInstance()
}
