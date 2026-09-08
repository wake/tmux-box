package codex

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

func (p *Provider) Type() string        { return "codex" }
func (p *Provider) DisplayName() string { return "Codex" }
func (p *Provider) IconHint() string    { return "codex" }

func (p *Provider) Claim(ctx agent.ClaimContext) bool {
	if ctx.HookEvent != nil {
		return ctx.HookEvent.AgentType == "codex"
	}
	return false
}

func (p *Provider) Identify(proc agent.ProcessInfo) bool {
	exeName := strings.ToLower(filepath.Base(proc.ExePath))
	if exeName == "codex" {
		return true
	}
	if !agent.IsJSRuntime(exeName) {
		return false
	}
	return agent.ArgvContainsFragment(proc.Argv, "@openai/codex", "/codex/", "/codex-cli/", "codex/dist/cli.js")
}

func (p *Provider) DeriveStatus(eventName string, rawEvent json.RawMessage) agent.DeriveResult {
	return deriveCodexStatus(eventName, rawEvent)
}

// IdentifyEvent implements agent.SessionIdentifier. codex puts session_id and
// cwd at the top level of every hook payload (spec §3.1) — alongside turn_id,
// which this feature does not use — so the shared extractor covers every event
// and purdexName is not consulted.
func (p *Provider) IdentifyEvent(purdexName string, rawEvent json.RawMessage) (string, string) {
	return agent.ExtractSessionIdentity(rawEvent)
}

// SupportedStatuses declares the Status values codex.Provider's DeriveStatus
// may emit, derived from Events().EmitsStatus. Events() is the SSoT after the
// issue #613 installer expansion; this shim keeps the StatusSupporter
// contract (Phase 1) working. Return order is lexicographic for determinism.
func (p *Provider) SupportedStatuses() []agent.Status {
	return agent.DeriveSupportedStatuses(p.Events())
}

func (p *Provider) IsAlive(tmuxTarget string) bool {
	return false // Deprecated: agent module uses prober.IsAliveFor directly
}

// ProbeIntents declares the probe-driven status transitions for the codex
// agent. Two intents:
//
//  1. ProbeIntentKindProcessDead (W6-3): recovers the missing StopFailure
//     transition codex 0.124.0 does not emit. Gates on {Running, Waiting}
//     because the inference makes sense any time codex is supposed to be
//     working. PaneAlive=true → Error, false → Clear.
//  2. ProbeIntentKindScreenChange (W6-6): recovers the missing
//     permission-approval transition. codex 0.125.0 fires
//     PdxPermissionRequest → status=waiting but emits NO hook when the
//     user approves the modal dialog, leaving lights stuck at waiting.
//     The detector watches the top 10 lines of the pane via
//     probe.Prober.Watch and emits a single Signal once Phase A
//     (ScreenStable) completes and the next ScreenChanged event arrives.
//     Gates on {Waiting} only — running is the target status (not an
//     entry point) and idle / error / clear already have hook authority.
//
// Slice order is stable (ProcessDead first, ScreenChange second) so test
// fixtures can index by position without churning on future entries.
//
// Implements agent.ProbeIntentProvider (optional capability — providers
// without ProbeIntents behave identically to pre-W6-3).
func (p *Provider) ProbeIntents() []agent.ProbeIntent {
	return []agent.ProbeIntent{
		{
			Kind:          agent.ProbeIntentKindProcessDead,
			OnEntryStatus: []agent.Status{agent.StatusRunning, agent.StatusWaiting},
			OnSignal:      onProcessDead,
		},
		{
			Kind:          agent.ProbeIntentKindScreenChange,
			OnEntryStatus: []agent.Status{agent.StatusWaiting},
			OnSignal:      onScreenChange,
		},
	}
}

// onProcessDead maps a ProcessDead signal to the recovery status, splitting
// W6-3 (PaneAlive=true → Error) and W6-4 (PaneAlive=false → Clear) on the
// pane existence observation that the detector captured. Defends against
// dispatcher misuse: a Signal with a non-ProcessDead Kind returns an empty
// Status (drop the signal) rather than mapping to Error or Clear.
func onProcessDead(sig agent.Signal) agent.Status {
	if sig.Kind != agent.ProbeIntentKindProcessDead {
		return ""
	}
	if sig.PaneAlive {
		return agent.StatusError
	}
	return agent.StatusClear
}

// onScreenChange maps a ScreenChange signal to StatusRunning. Defends
// against dispatcher misuse: a Signal carrying a non-ScreenChange Kind
// returns "" (drop the signal) rather than mapping to Running. Mirrors
// onProcessDead's defensive shape.
//
// PaneAlive is intentionally NOT inspected here: the detector contract
// (see probe_intent_screen_change.go) guarantees PaneAlive=true for every
// ScreenChange Signal — capture-pane errors are skipped at the watcher
// loop tick rather than re-emitted as PaneAlive=false. Adding a
// PaneAlive check would be dead code noise.
func onScreenChange(sig agent.Signal) agent.Status {
	if sig.Kind != agent.ProbeIntentKindScreenChange {
		return ""
	}
	return agent.StatusRunning
}
