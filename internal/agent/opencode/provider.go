package opencode

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/agent"
)

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Type() string        { return "opencode" }
func (p *Provider) DisplayName() string { return "OpenCode" }
func (p *Provider) IconHint() string    { return "opencode" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "opencode"
	}
	return false
}

func (p *Provider) Identify(proc agent.ProcessInfo) bool {
	exeName := strings.ToLower(filepath.Base(proc.ExePath))
	if exeName == "opencode" {
		return true
	}
	if !agent.IsJSRuntime(exeName) {
		return false
	}
	return agent.ArgvContainsFragment(proc.Argv, "opencode-ai", "/opencode/", "/opencode-ai/", "opencode/dist")
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveOpenCodeStatus(eventName, rawEvent)
}

// IdentifyEvent implements agent.SessionIdentifier. opencode puts session_id
// at the top level of every emit; cwd is currently sent only by SessionStart
// (spec §3.3), which is fine — the extractor simply returns "" for it and the
// store leaves whatever cwd it already holds alone.
func (p *Provider) IdentifyEvent(purdexName string, rawEvent json.RawMessage) (string, string) {
	return agent.ExtractSessionIdentity(rawEvent)
}

// SupportedStatuses declares the Status values opencode.Provider's
// DeriveStatus may emit, derived from Events().EmitsStatus. Events() is the
// SSoT; this shim keeps the StatusSupporter contract (Phase 1) working.
// Return order is lexicographic for determinism.
func (p *Provider) SupportedStatuses() []agent.Status {
	return agent.DeriveSupportedStatuses(p.Events())
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	return false
}
