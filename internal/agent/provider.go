package agent

import "encoding/json"

// AgentProvider is the core interface that all agent providers must implement.
type AgentProvider interface {
	Type() string
	DisplayName() string
	IconHint() string
	Claim(ctx ClaimContext) bool
	Identify(proc ProcessInfo) bool
	DeriveStatus(eventName string, rawEvent json.RawMessage) DeriveResult
	IsAlive(tmuxTarget string) bool
}

// ClaimContext provides information for agent detection.
type ClaimContext struct {
	HookEvent   *HookEvent
	ProcessName string // Legacy foreground-command hint; avoid new dependencies.
	TmuxTarget  string // tmux target for detailed detection (e.g. "mySession:")
}

// HookEvent is the raw hook event received from pdx hook CLI.
type HookEvent struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
}

// --- Optional capabilities ---

// HookInstaller can install/remove/check hook configurations for a specific agent.
//
// Events() is the single source of truth (SSoT) for the provider's classified
// hook event catalog. It supersedes any package-local *HookEvents slice; do not
// introduce a parallel string list. Installer iteration, CheckHooks reporting,
// and template parity must derive their installable subset with
// IsInstallableHookSpec rather than assuming every catalog entry is wired.
// SupportedStatuses derivation still reads the same declaration. Any new
// HookInstaller implementation is required to implement Events().
type HookInstaller interface {
	InstallHooks(pdxPath string) error
	RemoveHooks(pdxPath string) error
	CheckHooks() (HookStatus, error)
	Events() []HookEventSpec
}

// HookEventSpec declares one upstream hook event, the Status set DeriveStatus
// may emit from that event when it is installable, and a short human-readable
// blurb for the Inspector UI. It is the build-time declaration contract;
// runtime hook handling stays per-agent (policy dispersal, plumbing shared).
//
// Fields:
//   - EmitsStatus: non-empty Status values (Status != "") that DeriveStatus
//     may return for this event across all sub-branches. Empty slice (not nil)
//     means the event is detail-only (DeriveStatus returns Valid=true with
//     Status=""); SubagentStart/Stop are the canonical examples. Polymorphic
//     events (e.g. cc Notification) list the union of every sub-branch.
//   - Description: short English sentence for Inspector display. No trailing
//     period, no emoji; keep it under roughly 70 characters.
//   - FutureOnly: installer/checker-facet flag ONLY. When true, the hook is
//     declared (installer writes it, DeriveStatus parses it) but the current
//     CLI path is not guaranteed to emit it; CheckHooks therefore tolerates
//     an absent hooks.json entry (legacy user who installed before the
//     expansion) while still surfacing present-but-broken entries. The flag
//     does NOT affect DeriveStatus-capability declarations:
//     DeriveSupportedStatuses (and thus StatusSupporter.SupportedStatuses)
//     unions every spec's EmitsStatus regardless of FutureOnly — proxy paths
//     and future CLI versions may legitimately emit the event. Default false
//     means "installer-required; missing is an issue". See fix plan §1.1.
//   - Handling: classifies whether this catalog entry is installed/parsed.
//     Empty values preserve the legacy defaults via EffectiveHookHandling:
//     specs with EmitsStatus default to status, and empty-status specs default
//     to detail. Newly added non-installable upstream entries must set this
//     explicitly to ignored or unsupported.
type HookHandling string

const (
	HookHandlingStatus      HookHandling = "status"
	HookHandlingDetail      HookHandling = "detail"
	HookHandlingIgnored     HookHandling = "ignored"
	HookHandlingUnsupported HookHandling = "unsupported"
)

type HookEventSpec struct {
	// PurdexName is the daemon-internal stable identifier for this catalog
	// entry. Always prefixed with "Pdx". Used as:
	//   - DeriveStatus switch case label
	//   - CLI `pdx hook --agent <agent> <PurdexName>` positional argument
	//   - HTTP EventRequest.PurdexName payload value
	//   - NormalizedEvent.PurdexName / TraceStore record key
	// Daemon code MUST use PurdexName for all internal lookups and matching.
	PurdexName string

	// UpstreamKeys lists the raw event names that the agent's upstream hook
	// system fires when this catalog entry should match. Always non-empty
	// post-migration (installable / unsupported / ignored alike).
	//
	// Used at the installer/plugin boundary:
	//   - cc: written as ~/.claude/settings.json "hooks" map key
	//   - codex: written as ~/.codex/hooks.json matcher-group key
	//   - opencode: matched against Bus event name in plugin demux switch
	//
	// For cc/codex this is normally a single-element slice. For opencode
	// installable entries, multiple upstream Bus events may map to the same
	// PurdexName (e.g., permission.asked + question.asked → PdxPermissionRequest).
	// For opencode unsupported/ignored entries, UpstreamKeys is a
	// single-element slice equal to the raw upstream event name.
	UpstreamKeys []string

	// Lifecycle classifies the daemon-internal side effect kind for this
	// catalog entry. Used by frame_ops / handler so that lifecycle handling
	// (frame reset, subagent membership, frame delete, error guard whitelist)
	// can be done via catalog metadata lookup instead of hardcoded event-name
	// string comparison. See lifecycle.go for the value table.
	Lifecycle LifecycleEventKind

	EmitsStatus []Status
	Description string
	FutureOnly  bool
	Handling    HookHandling
}

// LookupByPurdexName scans specs for the catalog entry whose PurdexName
// matches purdexName. The empty string never matches even if a (legacy /
// not-yet-migrated) entry has an empty PurdexName, which keeps malformed
// payloads from accidentally landing on a real catalog row.
//
// Intended caller: daemon-internal lookups (handler routing, frame_ops
// dispatch, DeriveStatus). Slice scan is O(N) with N ≤ ~11; an index would
// add cache invalidation without measurable benefit.
func LookupByPurdexName(specs []HookEventSpec, purdexName string) (HookEventSpec, bool) {
	if purdexName == "" {
		return HookEventSpec{}, false
	}
	for _, s := range specs {
		if s.PurdexName == purdexName {
			return s, true
		}
	}
	return HookEventSpec{}, false
}

// LookupByUpstreamKey scans specs for the catalog entry whose UpstreamKeys
// contains upstreamKey. Used for installer/checker boundary inspection and
// for test assertions when verifying the cc/codex 1:1 PurdexName ↔
// UpstreamKey mapping.
//
// Caveat: NOT suitable for opencode runtime routing of filter-based events
// (`session.status`, `tool.execute.before`, `tool.execute.after`). Those
// upstream keys require a `type` / `tool` filter to determine the correct
// PurdexName, and catalog UpstreamKeys does not encode filter conditions —
// hitting `session.status` here would resolve to PdxStop even for busy /
// retry sub-states. opencode plugin demux remains the authority for that
// runtime path.
func LookupByUpstreamKey(specs []HookEventSpec, upstreamKey string) (HookEventSpec, bool) {
	if upstreamKey == "" {
		return HookEventSpec{}, false
	}
	for _, s := range specs {
		for _, k := range s.UpstreamKeys {
			if k == upstreamKey {
				return s, true
			}
		}
	}
	return HookEventSpec{}, false
}

func EffectiveHookHandling(spec HookEventSpec) HookHandling {
	if spec.Handling != "" {
		return spec.Handling
	}
	if len(spec.EmitsStatus) > 0 {
		return HookHandlingStatus
	}
	return HookHandlingDetail
}

func IsInstallableHookSpec(spec HookEventSpec) bool {
	switch EffectiveHookHandling(spec) {
	case HookHandlingStatus, HookHandlingDetail:
		return true
	default:
		return false
	}
}

// HookStatus reports the installation state of hooks for an agent.
//
// Managed and UpgradesAvailable are the Finding #2 / #4 contract
// extensions (PR #616 review): the SPA needs to distinguish (a) "pdx
// left artifacts on disk" from "all required events are installed"
// so the Remove button stays enabled on drifted-but-managed state,
// and (b) "install is valid but 6 new FutureOnly events are
// available" so the Install button / upgrade hint is not dead.
type HookStatus struct {
	Installed         bool                     `json:"installed"`
	Managed           bool                     `json:"managed"`
	UpgradesAvailable []string                 `json:"upgradesAvailable,omitempty"`
	Events            map[string]HookEventInfo `json:"events"`
	Issues            []string                 `json:"issues"`
	AgentVersion      string                   `json:"agentVersion,omitempty"`
	SupportedVersion  string                   `json:"supportedVersion,omitempty"`
	ExceedsSupport    bool                     `json:"exceedsSupport,omitempty"`
}

// HookEventInfo describes the state of a single hook event.
//
// FutureOnly mirrors HookEventSpec.FutureOnly so the UI can render
// "tolerated absent" (grey FutureOnly badge, no red error) vs
// "missing installer-required event" (hard red).
type HookEventInfo struct {
	Installed  bool   `json:"installed"`
	Command    string `json:"command"`
	FutureOnly bool   `json:"futureOnly,omitempty"`
}

// StatuslineInstaller manages CC's statusLine.command in ~/.claude/settings.json.
type StatuslineInstaller interface {
	CheckStatusline() (StatuslineState, error)
	InstallStatuslinePdx(pdxPath string) error
	InstallStatuslineWrap(pdxPath, inner string) error
	RemoveStatusline() error
}

// StatuslineState describes the current state of an agent's statusLine config.
type StatuslineState struct {
	Mode         string `json:"mode"` // "none" | "pdx" | "wrapped" | "unmanaged"
	Installed    bool   `json:"installed"`
	Inner        string `json:"innerCommand,omitempty"`
	RawCommand   string `json:"rawCommand,omitempty"`
	SettingsPath string `json:"settingsPath"`
}

// SessionIdentifier is an optional capability, discovered by type assertion.
// It answers "which agent session sent this event, and from where" for the
// *sender's own* frame — no ownership decision is involved, it is that
// agent's own session id whoever owns the pane.
//
// Every own-frame event contributes, not just SessionStart: opencode switches
// back to an existing session inside one process without emitting a
// SessionStart (spec §3.3), so a lifecycle gate would freeze the record on the
// old conversation.
//
// Returns ("", "") when the event carries neither field; only non-empty values
// are ever written, so a payload that carries neither clears nothing. A
// provider that does not implement this interface never contributes.
type SessionIdentifier interface {
	IdentifyEvent(purdexName string, rawEvent json.RawMessage) (sessionID, cwd string)
}

// HistoryProvider can retrieve conversation history for a session.
type HistoryProvider interface {
	GetHistory(cwd string, sessionID string) ([]map[string]any, error)
}

// StreamCapable marks a provider that supports stream mode handoff.
// Reserved for future implementation.
type StreamCapable interface {
	ExtractState(tmuxTarget string) (SessionState, error)
	ExitInteractive(tmuxTarget string) error
	RelayArgs(state SessionState) []string
	ResumeCommand(state SessionState) string
}

// StatusSupporter is an optional capability for providers to declare which
// Status values they can emit. Providers that do not implement this interface
// are treated as "not declared" by the Coverage helper. Returning an empty
// slice is a valid declaration meaning "supports no Status values" and is
// distinct from not implementing the interface at all.
//
// Scope: this declares the full status set DeriveStatus is *capable* of
// returning, independent of which hook events the agent's hook installer
// currently wires. Declaration may legitimately exceed the installer's
// current event coverage (e.g. when the provider is preparing for a future
// CLI version, or when events arrive via proxy / parent agents). The drift
// test (internal/agent/drift_test.go) keeps DeriveStatus implementations
// honest against this declaration; alignment with the hook installer event
// catalog is a separate concern tracked by per-agent installer phases.
type StatusSupporter interface {
	SupportedStatuses() []Status
}

// SessionState holds agent session state for stream handoff.
type SessionState struct {
	SessionID string
	Cwd       string
}

// ProbeIntentProvider declares probe-driven status transitions for an agent.
//
// Probe is a recovery channel that complements (not replaces) hooks: when an
// agent's hook catalog has a structural gap (e.g. codex StopFailure being
// FutureOnly = ✗ 0.124.0 不發), the agent's provider declares one or more
// ProbeIntents whose detectors infer the missing transition from runtime
// observation (process liveness, tmux pane content, etc).
//
// Implementing this interface is optional. Providers without ProbeIntents
// behave identically to pre-W6-3 (no probe-driven transitions).
//
// Daemon module reads ProbeIntents() on every status change for the session's
// agent and starts/stops detectors accordingly. Detectors live in the agent's
// own package (e.g. internal/agent/codex/probe_intent_*.go) — module only
// owns the dispatcher plumbing.
type ProbeIntentProvider interface {
	ProbeIntents() []ProbeIntent
}

// ProbeIntent is one probe-driven transition declared by an agent provider.
//
// Lifecycle (driven by daemon dispatcher):
//  1. Status changes to a value listed in OnEntryStatus → dispatcher resolves
//     pane id + senderPID from the active frame, then dispatches to the
//     per-Kind detector goroutine
//  2. Detector observes runtime state (per Kind) and emits Signal events
//  3. Dispatcher applies guards (see probe orchestrator §5.3) then
//     OnSignal(sig) → Status
//  4. Status changes to a value NOT in OnEntryStatus → dispatcher stops
//     detector (cancel ctx) and frees per-(session, kind) state
//
// W6-3 first PR finalize: one Kind = ProbeIntentKindProcessDead. Future Kind
// additions (W6-1/2/6 ScreenChange) MUST keep the existing Signal fields
// backward compatible (add fields, don't repurpose).
type ProbeIntent struct {
	// Kind classifies the detector. W6-3 finalize: only ProbeIntentKindProcessDead.
	// Subsequent W6 PRs MAY introduce additional Kind constants; existing Kind
	// semantics and Signal field semantics MUST remain stable.
	Kind ProbeIntentKind

	// OnEntryStatus is the set of currentStatus values that gate this intent
	// active. Detector starts on entry to any of these and stops on exit to any
	// status outside the set.
	//
	// Example (W6-3+W6-4): {StatusRunning, StatusWaiting} — codex process_dead
	// inference only makes sense while codex is supposed to be doing work.
	OnEntryStatus []Status

	// OnSignal maps a detector signal to the new Status. Empty Status returned
	// by OnSignal means "drop this signal" (detector observed transient state
	// that doesn't warrant a transition).
	//
	// Contract (per by2z79ouc DEF-1): dispatcher invokes OnSignal as a
	// mapping callback INSIDE applyProbeGuards, AFTER the stale-callback
	// guard + graceWindow guard pass. OnSignal therefore never sees signals
	// that should have been dropped by hook authority. ErrorGuard +
	// transition gate run AFTER OnSignal returns (they need newStatus to
	// compare against currentStatus). Provider may safely log / count in
	// OnSignal because pre-guard-survived signals are guaranteed.
	OnSignal func(Signal) Status
}

// ProbeIntentKind is the detector category. W6-3 finalize introduces one Kind;
// future PRs MAY add more. Use a string type so test fixtures can declare
// expected kinds without import cycles.
type ProbeIntentKind string

const (
	// ProbeIntentKindProcessDead — detector polls senderPID + observes pane
	// existence; emits one Signal with PaneAlive set when the agent process
	// is no longer in the pane PID tree (W6-3+W6-4 combined: PaneAlive=true
	// → caller maps to error; false → caller maps to clear).
	ProbeIntentKindProcessDead ProbeIntentKind = "process_dead"

	// ProbeIntentKindScreenChange — detector observes the top N lines of a
	// pane via probe.Prober.Watch; once ScreenStable arms the detector,
	// the next ScreenChanged event emits a Signal with PaneAlive=true.
	// Used by the codex provider (W6-6) to recover the missing
	// permission-approval transition: codex 0.125.0 fires PdxPermissionRequest
	// (status → waiting) but does NOT emit any hook when the user approves
	// the modal dialog, so lights stay stuck at waiting until the next
	// PdxStop arrives. The detector is dumb (single Signal, no time
	// judgement); reject-path race is handled by the dispatcher's J3
	// pre-grace + classifyAsHookRace cover. Signal.PaneAlive is always
	// true for this Kind — capture-pane errors do not fire callbacks
	// (the watch loop skips error ticks rather than re-emitting). See
	// docs/specs/2026-05-01-w6-6-codex-screen-change-probe-spec.md
	// (v6.1 final, 7 round codex spec review).
	ProbeIntentKindScreenChange ProbeIntentKind = "screen_change"
)

// Signal is the runtime observation emitted by a detector. W6-3 finalize
// includes the minimum context W6-3+W6-4 both need; future Kind additions
// MAY add fields but MUST keep existing field semantics stable.
//
// Field rationale (per b36pap7jc DEF-1 — preventing immediate signature churn
// on the next W6 PR):
//   - Kind: required for OnSignal type discrimination
//   - PaneAlive: W6-3+W6-4 binary distinction; future ScreenChange Kinds may
//     leave it unconditionally true (they observe pane content, not pane life)
//   - PaneID: explicit pane id — detector resolves & captures this when the
//     intent arms; OnSignal receives the same value (used by trace
//     observability and for downstream future-Kind detectors that act on
//     pane-scoped state)
//   - SenderPID: the codex sender pid resolved from frame state; carried on
//     Signal so OnSignal handlers can log without re-querying
type Signal struct {
	Kind      ProbeIntentKind
	PaneAlive bool
	PaneID    string
	SenderPID int
}
