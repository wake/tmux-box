package agent

import (
	"fmt"
	"log"
	"sort"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// proxyMaxDepth caps the PPID ancestor walk during proxy subagent detection.
// 5 is enough to cover observed layouts like codex → codex-companion → cc
// (2 hops) with 3 hops of buffer for shell/tmux wrappers; beyond that we
// fall back to creating a new frame rather than paying unbounded syscall
// cost on a genuinely deep chain.
const proxyMaxDepth = 5

// proxyUpsertMaxAttempts caps optimistic-concurrency retries when two
// near-simultaneous proxy attaches (or detaches) race on the same parent
// frame's subagents list. Each attempt reloads the parent row, re-merges
// the ref through updateSubagents, and re-issues UpsertIfUnchanged. After
// exhausting attempts the caller surfaces an error — production scenarios
// that hit this limit are genuine hot loops, not ordinary concurrency.
const proxyUpsertMaxAttempts = 3

// MaxDelegatingToolUseIDs caps DelegatingToolUseIDs slice growth per
// SubagentRef. Spec §10 risk table pre-acknowledged that a degenerate hook
// stream (missing PostToolUse / hook resends with new tool_use_id) could
// inflate subagents_json unboundedly — making UpsertIfUnchanged, projection
// broadcast, and SPA payload progressively slower or out-of-budget — and
// recommended "defensive cap at ~32 entries with log-and-discard if needed".
// Round-2 codex three-parallel adversarial review (PR #829, finding #2)
// triggered enforcement.
//
// On overflow, oldest IDs evict first (FIFO). The Delegating flag stays
// true so the visual signal does not flap; dev-mode log surfaces hook stream
// health for operators.
const MaxDelegatingToolUseIDs = 32

// nativeBaselineKey identifies a native (IsProxy=false) subagent ref for the
// SessionStart filter-merge baseline (Phase 3.5 plan §2.2.2 v12 T1 fix). The
// triple (Type, ID, StartedAt) survives ID collision between an old-session
// baseline ref and a concurrent SubagentStart that happens to reuse the
// same ID — the new ref's StartedAt is a fresh broadcastTs distinct from
// any baseline ref, so the new ref doesn't match baseline and is preserved.
type nativeBaselineKey struct {
	typ       string
	id        string
	startedAt int64
}

type FrameTraceMeta struct {
	FrameID       string
	ParentFrameID string
	Decision      string
	Reason        string
	Before        any
	After         any
	// MatchedAgentType is set on the daemon_restart_recovery path when
	// tryRebuildFromProcessTree confirms an alive agent in the pane process
	// tree. Empty otherwise. Carries the agent family that the live process
	// matched, which may differ from req.AgentType (the hook event's
	// declared agent_type) — divergence is the diagnostic signal Phase 3
	// rebuild path is meant to surface.
	//
	// PR #638 codex review round 2 #3 fix: previously the matched type was
	// dropped silently (`_ = matchedType`), making rebuild trace unable to
	// distinguish "rebuild confirmed hook owner" vs "rebuild only saw a
	// different agent in the same pane (e.g. cc parent of a codex hook)".
	MatchedAgentType string
}

func (m *Module) applyFrameEvent(req EventRequest, result agentpkg.DeriveResult, broadcastTs int64) (*SessionProjection, FrameTraceMeta, error) {
	if m.frames == nil {
		return nil, FrameTraceMeta{Decision: "skipped", Reason: "frame_store_unavailable", Before: map[string]any{}, After: map[string]any{}}, nil
	}
	lifecycle := m.classifyLifecycleForReq(req)
	isDetailOnly := lifecycle == agentpkg.LifecycleSubagentStart || lifecycle == agentpkg.LifecycleSubagentStop
	if !isDetailOnly && !result.Valid {
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{Decision: "skipped", Reason: "derive_invalid", Before: map[string]any{}, After: map[string]any{}}, err
	}

	frame, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
	if err != nil {
		return nil, FrameTraceMeta{}, err
	}
	before := summarizeFrame(frame)

	switch lifecycle {
	case agentpkg.LifecycleSessionEnd:
		if frame != nil {
			// Phase 3.5 §2.3 (v8 L1 fix, codex round PR-2 attack):
			// detach-first ordering. The previous delete-first +
			// best-effort detach left a permanent orphan whenever
			// removeProxyRefForSender failed (storage error, retry
			// exhaustion, daemon crash mid-handler) — the child row
			// was gone so projection_dedup could no longer hide the
			// ancestor's stale proxy ref, and PR-3.5a ships without
			// pruneDeadProxyRefs. By detaching first and propagating
			// the error before Delete, a hook handler failure leaves
			// the DB in a recoverable state (child row + parent ref
			// both still present; sweep canonicalize / next
			// SessionEnd retry can fix it).
			if _, _, _, _, derr := m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs); derr != nil {
				return nil, FrameTraceMeta{}, derr
			}
			if err := m.frames.Delete(frame.FrameID); err != nil {
				return nil, FrameTraceMeta{}, err
			}
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       frame.FrameID,
				ParentFrameID: frame.ParentFrameID,
				Decision:      "deleted_frame",
				Reason:        "session_end",
				Before:        before,
				After:         map[string]any{},
			}, err
		}
		// frame == nil: sender has no frame of its own. This is either a
		// genuine orphan SessionEnd, or the SessionEnd of a process that was
		// previously proxy-attached to another frame (Phase 2 PR-2b §1.5).
		// Probe the pane's frames for a matching proxy ref and detach it.
		removed, parentFrame, parentBefore, parentAfter, err := m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs)
		if err != nil {
			return nil, FrameTraceMeta{}, err
		}
		if removed {
			projection, perr := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       parentFrame.FrameID,
				ParentFrameID: parentFrame.ParentFrameID,
				Decision:      "updated_frame",
				Reason:        "proxy_subagent_detached",
				Before:        parentBefore,
				After:         parentAfter,
			}, perr
		}
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{
			Decision: "skipped",
			Reason:   "session_end_without_frame",
			Before:   before,
			After:    map[string]any{},
		}, err
	case agentpkg.LifecycleSubagentStart, agentpkg.LifecycleSubagentStop:
		if frame == nil {
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				Decision: "skipped",
				Reason:   "frame_missing",
				Before:   before,
				After:    map[string]any{},
			}, err
		}
		agentID, _ := result.Detail["agent_id"].(string)
		if agentID == "" {
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       frame.FrameID,
				ParentFrameID: frame.ParentFrameID,
				Decision:      "skipped",
				Reason:        "subagent_id_missing",
				Before:        before,
				After:         before,
			}, err
		}
		ref := agentpkg.SubagentRef{
			ID: agentID,
			// Type is the canonical agent family that owns this subagent (cc /
			// codex / opencode), not the payload's per-subagent sub-variant
			// (e.g. opencode's `agent_type: "Explore"`). Keeping Type aligned
			// to frame.AgentType lets the SPA use it for agent-family color
			// lookup without provider-specific special cases.
			Type:      frame.AgentType,
			StartedAt: broadcastTs,
			// SourcePID / SourceStartTime / IsProxy left zero: native SubagentStart
			// refs have no distinct source process identity. Proxy attaches (PR-2b)
			// set these explicitly.
		}
		// Optimistic-concurrency native mutation (#632 continuation):
		// two cc SubagentStarts from a concurrent Task-tool dispatch can
		// land in near-simultaneously; the same read-modify-write race
		// that motivated the proxy atomic fix applies here. Delegate to
		// the shared helper that reloads + re-merges on UpsertIfUnchanged
		// conflict.
		applied, stored, merr := m.mutateSubagentsWithRetry(*frame, lifecycle, ref, broadcastTs)
		if merr != nil {
			return nil, FrameTraceMeta{}, merr
		}
		if !applied {
			// Frame was deleted mid-flight (concurrent sweep / SessionEnd).
			// Report frame_missing so downstream treats this like the
			// frame == nil branch above.
			projection, perr := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				Decision: "skipped",
				Reason:   "frame_missing",
				Before:   before,
				After:    map[string]any{},
			}, perr
		}
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{
			FrameID:       stored.FrameID,
			ParentFrameID: stored.ParentFrameID,
			Decision:      "updated_frame",
			Reason:        "subagent_membership_changed",
			Before:        before,
			After:         summarizeFrame(&stored),
		}, err
	case agentpkg.LifecycleUserPromptSubmit:
		// L2 codex turn-aware attach/upsert (spec §3.3.B/C, plan §3 P3-T7a/T7b).
		//
		// Gate 1 — codex only. cc / opencode UserPromptSubmit + PreToolUse
		// have no per-turn identity in their hook payloads; falling through
		// to the existing generic frame path keeps their behavior unchanged
		// (spec §5 row 17 / 17b regression guards).
		if req.AgentType != "codex" {
			break
		}
		// Gate 2 — sender owns its own frame. Existing narrow column update
		// (UpdateHookPath branch above) handles status / last_seen refresh;
		// L2 turn-aware identity only exists on the parent's proxy ref,
		// never on the sender's own frame.
		if frame != nil {
			break
		}
		// Gate 3 — must have an alive cross-type proxy parent.
		parent, perr := m.findProxyParent(req)
		if perr != nil {
			return nil, FrameTraceMeta{}, perr
		}
		if parent == nil {
			// No proxy parent. PreToolUse must NOT fall through to the
			// generic frame-create path: it carries no Status (DeriveResult
			// returns Status="" for PdxPreToolUse) and would materialize a
			// standalone idle frame in the SessionStart attach branch below,
			// contradicting "broker is starting work for a turn" semantics
			// (spec §3.3.C.1 + §5 row 20). UserPromptSubmit's existing
			// Status=Running derive path stays unchanged for backward compat
			// (v4 behavior).
			if req.PurdexName == "PdxPreToolUse" {
				projection, perr2 := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					Decision: "skipped",
					Reason:   "pre_tool_without_proxy_parent",
					Before:   map[string]any{},
					After:    map[string]any{},
				}, perr2
			}
			break
		}
		// First-time vs in-place upsert — caller-side classification keeps
		// upsertProxyRefForBroker's helper signature frozen post-Phase 2.
		// findProxyRefByBroker is a side-effect-free lookup; an in-flight
		// concurrent attach between this check and the helper's own
		// findProxyRefByBroker (inside the retry loop) only changes the
		// trace reason, not the persisted shape.
		isFirstAttach := findProxyRefByBroker(parent.Subagents, req.SenderPID, req.SenderStartTime) < 0
		parentBefore := summarizeFrame(parent)
		turnID := parseCodexTurnID(req.RawEvent)
		persisted, stored, uerr := m.upsertProxyRefForBroker(*parent, req.SenderPID, req.SenderStartTime, turnID, broadcastTs)
		if uerr != nil {
			return nil, FrameTraceMeta{}, uerr
		}
		if !persisted {
			// Parent vanished mid-flight (concurrent SessionEnd / sweep).
			// Fall through to the generic frame path as a recovery.
			break
		}
		reason := "proxy_subagent_upserted_on_user_prompt"
		if isFirstAttach {
			reason = "proxy_subagent_attached_on_user_prompt"
		}
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{
			FrameID:       stored.FrameID,
			ParentFrameID: stored.ParentFrameID,
			Decision:      "updated_frame",
			Reason:        reason,
			Before:        parentBefore,
			After:         summarizeFrame(&stored),
		}, err
	case agentpkg.LifecycleStopFailure:
		// Native subagent failure path: payload's agent_id identifies
		// the failing subagent (not the main session). Empirically this
		// is how cc reports rate_limit and other subagent terminations
		// (~98% of observed Task failures in dthn telemetry, 2026-05-03,
		// spec §1.2). Without this branch native SubagentRefs accumulate
		// forever — the only GC path is LifecycleSessionEnd, which never
		// fires on a long-running cc session that retries through
		// rate-limit storms (frame_ops.go old §288-300 was a no-op for
		// frame != nil).
		//
		// Two-step gate: pre-check ref presence with the pure helper,
		// then mutate. mutateSubagentsWithRetry's `applied=true` does
		// NOT mean a ref was removed — it only means UpsertIfUnchanged
		// committed (LastSeenAt always refreshes regardless of whether
		// the target ref was present, spec §2.3). Without the pre-check
		// we'd broadcast a phantom "detached" trace step on every
		// mismatched payload.
		if frame != nil {
			agentID, _ := result.Detail["agent_id"].(string)
			if agentID == "" || findNativeRefByID(frame.Subagents, agentID) < 0 {
				// No payload agent_id, or payload references a ref we
				// don't hold. Trace as no-detach for observability;
				// preserve legacy post-switch UpdateHookPath +
				// projection refresh by breaking. We do NOT mutate
				// frame here — even though mutateSubagentsWithRetry
				// would be data-safe (returns same slice), it would
				// refresh LastSeenAt and consume an OCC attempt for
				// no benefit.
				break
			}
			ref := agentpkg.SubagentRef{
				ID: agentID,
				// Type: native ref match is by ID alone (spec §2.4 +
				// subagentRefMatches at frame_ops.go:913); Type is
				// informational. Mirror the SubagentStart construction
				// pattern at frame_ops.go:171-184 by using the parent
				// frame's AgentType.
				Type: frame.AgentType,
			}
			// PR-A round-2 codex review (threads 019ded61/62/63):
			// mutateSubagentsAndStatusWithRetry handles three concerns:
			//  1. atomic ref removal + Status=error write (R1 P1 fix)
			//  2. per-attempt ref-presence re-check (R2 phantom-detach fix)
			//  3. retry exhaustion vs frame deletion distinguished
			outcome, stored, merr := m.mutateSubagentsAndStatusWithRetry(*frame, ref, result.Status, broadcastTs)
			if merr != nil {
				// Retry exhausted (frame still exists but every
				// UpsertIfUnchanged conflicted). Surface as handler
				// error per mutateSubagentsWithRetry contract.
				return nil, FrameTraceMeta{}, merr
			}
			switch outcome {
			case detachOutcomeDetached:
				projection, perr := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					FrameID:       stored.FrameID,
					ParentFrameID: stored.ParentFrameID,
					Decision:      "updated_frame",
					Reason:        "native_subagent_detached_on_stop_failure",
					Before:        before,
					After:         summarizeFrame(&stored),
				}, perr
			case detachOutcomeFrameMissing:
				// Concurrent SessionEnd / sweep deleted the row
				// between our pre-check and the retry. Use the
				// existing frame_missing reason already used
				// elsewhere for "row disappeared between read and
				// write".
				projection, perr := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					Decision: "skipped",
					Reason:   "frame_missing",
					Before:   before,
					After:    map[string]any{},
				}, perr
			case detachOutcomeRefAlreadyAbsent:
				// Race lost — another writer detached the same ref
				// between our pre-check and a retry baseline. We
				// did NOT mutate; fall through to legacy
				// post-switch UpdateHookPath path so StopFailure
				// semantics (Status=error, LastSeenAt refresh)
				// still take effect. The trace step is whatever
				// the legacy path emits (parent_frame_found etc.),
				// honestly reflecting "didn't take detach branch".
				break
			}
		}
		// frame == nil: collapse onto the existing LifecycleStop body so
		// codex turn-aware proxy detach + cc/opencode wildcard
		// process-level detach paths are reused unchanged. The
		// `if frame != nil { break }` guard at the top of the
		// LifecycleStop case body is a no-op here because frame is
		// nil by precondition.
		fallthrough
	case agentpkg.LifecycleStop:
		// L2 codex turn-aware Stop targeted detach (spec §3.3.D + plan
		// §3 P3-T8a). All detach goes through pane-scan helpers; we never
		// call findProxyParent on the Stop path because the sender's PPID
		// chain may have already been torn down by the time the hook
		// arrives (B1/v2 fix).
		//
		// Gate 1 — sender owns its own frame. Standalone agent Stop is
		// the J3 dispatcher's territory (lights-rebuild ProbeIntent), not
		// L2 proxy detach (spec §3.3.D + §5 row 12).
		if frame != nil {
			break
		}
		// Gate 2 — non-codex providers fall back to wildcard process-level
		// detach (cc/opencode have no per-turn identity). Mirrors the
		// long-standing PR-2b SessionEnd path.
		if req.AgentType != "codex" {
			removed, ownerAfter, ownerBefore, ownerAfterMap, derr := m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs)
			if derr != nil {
				return nil, FrameTraceMeta{}, derr
			}
			if removed {
				projection, perr := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					FrameID:       ownerAfter.FrameID,
					ParentFrameID: ownerAfter.ParentFrameID,
					Decision:      "updated_frame",
					Reason:        "proxy_subagent_detached_on_stop",
					Before:        ownerBefore,
					After:         ownerAfterMap,
				}, perr
			}
			// No matching ref — fall through to generic post-switch path
			// so legacy behavior (no frame mutation, projection refresh)
			// stays observable.
			break
		}
		// Codex three sub-case dispatch (spec §3.3.D table).
		turnID := parseCodexTurnID(req.RawEvent)
		if turnID != "" {
			// (a) Targeted detach by (PID, StartTime, TurnID).
			removed, ownerAfter, ownerBefore, ownerAfterMap, derr := m.removeProxyRefForSenderTurn(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, turnID, broadcastTs)
			if derr != nil {
				return nil, FrameTraceMeta{}, derr
			}
			if removed {
				projection, perr := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					FrameID:       ownerAfter.FrameID,
					ParentFrameID: ownerAfter.ParentFrameID,
					Decision:      "updated_frame",
					Reason:        "proxy_subagent_detached_on_stop_turn",
					Before:        ownerBefore,
					After:         ownerAfterMap,
				}, perr
			}
			// Stop's turn doesn't match any live ref. Could be late Stop
			// after a turn-change upsert overwrote SourceTurnID, or a
			// stale-tombstone Stop after a previous detach (idempotent
			// no-op). Trace stop_no_match either way.
			projection, perr := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				Decision: "skipped",
				Reason:   "proxy_subagent_stop_no_match",
				Before:   map[string]any{},
				After:    map[string]any{},
			}, perr
		}
		// (b)/(c) — codex Stop with empty/parse-failed turn_id. Pane-scan
		// for the matching broker ref (PID, StartTime) to make the
		// fallback decision; any non-matching broker's SourceTurnID is
		// irrelevant (H1/v2 fix).
		paneFrames, perr := m.frames.ListByPane(req.TmuxPaneID)
		if perr != nil {
			return nil, FrameTraceMeta{}, perr
		}
		var matched *agentpkg.SubagentRef
		for i := range paneFrames {
			idx := findProxyRefByBroker(paneFrames[i].Subagents, req.SenderPID, req.SenderStartTime)
			if idx >= 0 {
				ref := paneFrames[i].Subagents[idx]
				matched = &ref
				break
			}
		}
		if matched == nil {
			// No broker ref to detach — silently drop the Stop. Trace as
			// stop_no_match so the chain remains observable; this also
			// covers idempotent re-Stop after a previous successful
			// detach (row 14 lower-confidence branch).
			projection, perr2 := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				Decision: "skipped",
				Reason:   "proxy_subagent_stop_no_match",
				Before:   map[string]any{},
				After:    map[string]any{},
			}, perr2
		}
		if matched.SourceTurnID != "" {
			// (b) Matching broker has a live SourceTurnID (a previous
			// upsert recorded a turn_id). Stop with empty turn_id is
			// almost certainly a malformed payload, NOT a legitimate
			// "broker is finished" signal — keep the ref so the
			// conservative behavior preserves the lit dot until either
			// the next upsert or governance sweep clears it.
			projection, perr2 := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				Decision: "skipped",
				Reason:   "proxy_subagent_stop_parse_failed",
				Before:   map[string]any{},
				After:    map[string]any{},
			}, perr2
		}
		// (c) Matching broker has empty SourceTurnID (SessionStart
		// attached but no UserPromptSubmit/PreToolUse upsert ever
		// recorded a turn) → empty-turn-only detach. Round-2 A1: a
		// process-level wildcard here would TOCTOU-race a concurrent
		// UserPromptSubmit that upgrades the ref to turn-aware between
		// our ListByPane scan above and the detach helper. Re-verify
		// SourceTurnID == "" inside the optimistic-concurrency loop by
		// reusing removeProxyRefForSenderTurn with turnID == "" — the
		// match condition becomes (PID, StartTime, SourceTurnID == "")
		// and any concurrent upgrade falls into the no-match branch
		// (proxy_subagent_stop_no_match below).
		removed, ownerAfter, ownerBefore, ownerAfterMap, derr := m.removeProxyRefForSenderTurn(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, "", broadcastTs)
		if derr != nil {
			return nil, FrameTraceMeta{}, derr
		}
		if removed {
			projection, perr2 := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       ownerAfter.FrameID,
				ParentFrameID: ownerAfter.ParentFrameID,
				Decision:      "updated_frame",
				Reason:        "proxy_subagent_detached_on_stop",
				Before:        ownerBefore,
				After:         ownerAfterMap,
			}, perr2
		}
		// Pane-scan saw a matching ref but the helper found nothing —
		// concurrent writer detached between scan and helper. Trace
		// stop_no_match so the chain accurately reflects "ref already
		// gone" without claiming to have detached it twice.
		projection, perr2 := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{
			Decision: "skipped",
			Reason:   "proxy_subagent_stop_no_match",
			Before:   map[string]any{},
			After:    map[string]any{},
		}, perr2
	}

	// Proxy subagent fast-path (Phase 2 PR-2b, plan §1.4): when a SessionStart
	// arrives from a sender that has no existing frame of its own and a PPID
	// ancestor walk locates an alive cross-type parent in the same pane, we
	// collapse the event into a proxy ref attached to that parent rather than
	// creating a standalone frame. Observed in practice for codex spawned from
	// inside a cc session via codex-companion: cc owns the UX, codex should
	// show as a dot on cc's tab, not as a separate lit-up frame.
	if lifecycle == agentpkg.LifecycleSessionStart && frame == nil {
		parent, perr := m.findProxyParent(req)
		if perr != nil {
			return nil, FrameTraceMeta{}, perr
		}
		if parent != nil {
			parentBefore := summarizeFrame(parent)
			ref := agentpkg.SubagentRef{
				ID:              fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime),
				Type:            req.AgentType,
				StartedAt:       broadcastTs,
				SourcePID:       req.SenderPID,
				SourceStartTime: req.SenderStartTime,
				IsProxy:         true,
			}
			// Optimistic-concurrency attach: read-modify-write on the parent
			// row is racy when two proxy SessionStarts land on the same
			// parent simultaneously (issue #632). UpsertIfUnchanged +
			// reload-on-conflict serializes the writes without a lock; the
			// retry loop merges both refs before persistence.
			attached, stored, aerr := m.attachProxyRefWithRetry(*parent, ref, broadcastTs)
			if aerr != nil {
				return nil, FrameTraceMeta{}, aerr
			}
			if attached {
				projection, err := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					FrameID:       stored.FrameID,
					ParentFrameID: stored.ParentFrameID,
					Decision:      "updated_frame",
					Reason:        "proxy_subagent_attached",
					Before:        parentBefore,
					After:         summarizeFrame(&stored),
				}, err
			}
			// Parent vanished mid-flight (swept or deleted by concurrent
			// SessionEnd). Fall through to the legacy create-new-frame path
			// so the sender still gets represented somewhere.
		}
	}

	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		return nil, FrameTraceMeta{}, err
	}

	startedAt := broadcastTs
	parentFrameID := ""
	if frame != nil {
		startedAt = frame.StartedAt
		parentFrameID = frame.ParentFrameID
	}
	if parentFrameID == "" {
		parent, err := m.frames.FindByPanePID(req.TmuxPaneID, info.PPID)
		if err != nil {
			return nil, FrameTraceMeta{}, err
		}
		if parent != nil && (frame == nil || parent.FrameID != frame.FrameID) {
			parentFrameID = parent.FrameID
		}
	}

	// Phase 3 Commit 3 — daemon-restart recovery (plan §1.2.4 / §1.4):
	// When the standard lookup chain (GetByIdentity → findProxyParent →
	// FindByPanePID) all miss on a hook event, fall back to inspecting the
	// pane's live process tree via the Prober. A hit means "some agent we
	// know is alive somewhere under this pane" — enough to recover frame
	// state lost to a daemon restart without trusting the hook event blindly
	// (the hook's AgentType remains the SOT; rebuild only marks the trace
	// reason so the Inspector can distinguish recovered frames from fresh
	// ones).
	//
	// Fail-soft: a probe error must not abort the hook. Log and fall through
	// to the降階 no-parent path (reason="no_parent_fallback", see plan §1.4).
	rebuiltMatched := false
	rebuiltAgentType := ""
	if frame == nil && parentFrameID == "" {
		matchedType, ok, rerr := m.tryRebuildFromProcessTree(req)
		if rerr != nil {
			log.Printf("[agent] rebuild_from_process_tree_failed: pane=%s err=%v", req.TmuxPaneID, rerr)
		}
		if ok {
			rebuiltMatched = true
			// req.AgentType remains the SOT for frame.AgentType (the hook
			// event is authoritative). matchedType is preserved separately
			// for trace diagnostics — divergence (e.g. cc pane with codex
			// hook) is the signal Inspector / Phase 5 reparent uses to
			// triage suspected proxy collapse failures. (PR #638 codex
			// review round 2 #3 fix.)
			rebuiltAgentType = matchedType
		}
	}

	status := result.Status
	if status == "" && frame != nil {
		status = frame.Status
	}
	if status == "" {
		status = agentpkg.StatusIdle
	}

	var stored store.Frame
	if frame != nil {
		// Existing frame: narrow column update (#632 R8). Writing the full
		// frame would round-trip a stale Subagents baseline and clobber
		// concurrent proxy/native attach/detach mutations on the same row.
		// Subagents changes flow through mutateSubagentsWithRetry /
		// attachProxyRefWithRetry / removeProxyRefForSender, not here.
		//
		// Exception: SessionStart on an existing frame must prune any
		// stale IsProxy refs (source process dead or PID-reused) while
		// preserving everything else — including concurrently-attached
		// native SubagentStart refs (IsProxy=false) that landed between
		// attempts. Phase 3.5 plan §2.2.2 (v6 J1, v8 M3) replaces the
		// old "snapshot → reset → re-attach" three-step pattern (race
		// window between steps could lose a concurrently-attached
		// proxy) with a single filter-merge-retry: each attempt
		// re-reads frame.Subagents (so any concurrent attach is
		// naturally preserved), prunes stale IsProxy via the live
		// identity gate, and writes via UpsertIfUnchanged. A conflict
		// reload picks up the newer ref before the next prune pass.
		if lifecycle == agentpkg.LifecycleSessionStart {
			var success bool
			// initialNativeBaseline is the BASELINE native ref identity
			// set captured before the retry loop. SessionStart's reset
			// semantic drops the old session's native subagent state,
			// which the cc hook saw at attempt 0 entry. Any native ref
			// appearing in the reloaded baseline on a later retry that
			// is NOT in initialNativeBaseline is — by construction — a
			// concurrent SubagentStart that landed AFTER our baseline
			// snapshot (post our SessionStart). Such refs belong to
			// the new session and must be preserved across all retries.
			//
			// Codex round 2 #O1 fix: the previous prevWrittenNativeIDs
			// approach regressed across multiple conflicts — a native
			// preserved at attempt 1 was recorded in prevWrittenNativeIDs
			// itself, so attempt 2 dropped it as a "carried-forward
			// baseline native". Snapshotting an immutable baseline
			// before the loop sidesteps the regression entirely.
			//
			// Codex round 5 #T1 fix: the v11 baseline used ID-only
			// identity. Native IDs are provider-supplied strings and
			// cross-session ID reuse is provider-dependent (cc/codex
			// tend toward UUID-like, opencode may be more deterministic),
			// so a SessionStart racing a concurrent SubagentStart whose
			// new native ref reused an old-session ID would silently
			// drop the new ref — classified as baseline by ID alone.
			// Track baseline by (Type, ID, StartedAt) instead: the new
			// SubagentStart's StartedAt will be a fresh broadcastTs
			// distinct from any baseline ref, so the new ref survives
			// the filter even when its ID collides with a baseline ref.
			initialNativeBaseline := make(map[nativeBaselineKey]struct{}, len(frame.Subagents))
			for _, ref := range frame.Subagents {
				if !ref.IsProxy {
					initialNativeBaseline[nativeBaselineKey{ref.Type, ref.ID, ref.StartedAt}] = struct{}{}
				}
			}
			for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
				pruned := make([]agentpkg.SubagentRef, 0, len(frame.Subagents))
				for _, ref := range frame.Subagents {
					if ref.IsProxy {
						// IsProxy refs survive SessionStart reset when
						// their source process is alive + identity
						// matches (PR-2b proxy-collapse semantics).
						if !isPidAliveFn(ref.SourcePID) {
							continue
						}
						actualStart, sterr := processStartTimeFn(ref.SourcePID)
						if sterr != nil || actualStart != ref.SourceStartTime {
							continue
						}
						pruned = append(pruned, ref)
						continue
					}
					// Native ref: drop if it was in the initial
					// baseline (SessionStart reset semantic for old
					// session refs). Otherwise (appeared after baseline
					// snapshot via concurrent SubagentStart) preserve.
					// Identity is (Type, ID, StartedAt) — a concurrent
					// SubagentStart reusing an old-session ID will have
					// a fresh StartedAt and will not match baseline.
					if _, baseline := initialNativeBaseline[nativeBaselineKey{ref.Type, ref.ID, ref.StartedAt}]; !baseline {
						pruned = append(pruned, ref)
					}
				}

				candidate := *frame
				candidate.AgentType = req.AgentType
				candidate.PPID = info.PPID
				candidate.ParentFrameID = parentFrameID
				candidate.Status = status
				candidate.LastSeenAt = broadcastTs
				candidate.Verified = true
				candidate.Subagents = pruned

				ok, written, uerr := m.frames.UpsertIfUnchanged(candidate, frame.LastSeenAt)
				if uerr != nil {
					return nil, FrameTraceMeta{}, uerr
				}
				if ok {
					stored = written
					success = true
					break
				}
				// Conflict — concurrent writer touched the row (proxy
				// attach, SubagentStart hook, probe status update).
				// Reload and retry; initialNativeIDs is UNCHANGED across
				// retries, so any native ID appearing in the reloaded
				// baseline that is not in initialNativeIDs is a true
				// concurrent attach and gets preserved by the next
				// filter pass.
				reloaded, rerr := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
				if rerr != nil {
					return nil, FrameTraceMeta{}, rerr
				}
				if reloaded == nil {
					// Frame deleted mid-flight. Treat as frame_missing.
					projection, perr := m.projectPane(req.TmuxPaneID)
					return projection, FrameTraceMeta{
						Decision: "skipped",
						Reason:   "frame_missing",
						Before:   before,
						After:    map[string]any{},
					}, perr
				}
				frame = reloaded
			}
			if !success {
				return nil, FrameTraceMeta{}, fmt.Errorf("session_start filter-merge: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, frame.FrameID)
			}
		} else {
			// Non-SessionStart existing-frame path: original narrow
			// column update (#632 R8 — don't round-trip subagents).
			updated := *frame
			updated.AgentType = req.AgentType
			updated.PPID = info.PPID
			updated.ParentFrameID = parentFrameID
			updated.Status = status
			updated.LastSeenAt = broadcastTs
			updated.Verified = true
			if err := m.frames.UpdateHookPath(updated); err != nil {
				return nil, FrameTraceMeta{}, err
			}
			reloaded, rerr := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
			if rerr != nil {
				return nil, FrameTraceMeta{}, rerr
			}
			if reloaded == nil {
				return nil, FrameTraceMeta{}, nil
			}
			stored = *reloaded
		}

		// Phase 3.5 §2.2.2 — descendant scan after successful filter-
		// merge. Catches any cold-start race where a child's
		// SessionStart raced this existing-frame's SessionStart and
		// landed standalone instead of being collapsed via PR-2b's
		// pre-Upsert proxy fast-path.
		if lifecycle == agentpkg.LifecycleSessionStart {
			updated, cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs)
			if cerr != nil {
				return nil, FrameTraceMeta{}, cerr
			}
			stored = updated
		}
	} else {
		// New frame: insert via Upsert. No existing subagents to clobber;
		// start the row with an empty list.
		stored, err = m.frames.Upsert(store.Frame{
			PaneID:           req.TmuxPaneID,
			AgentType:        req.AgentType,
			PID:              req.SenderPID,
			PPID:             info.PPID,
			ProcessStartTime: req.SenderStartTime,
			ParentFrameID:    parentFrameID,
			Subagents:        []agentpkg.SubagentRef{},
			Status:           status,
			StartedAt:        startedAt,
			LastSeenAt:       broadcastTs,
			Verified:         true,
		})
		if err != nil {
			return nil, FrameTraceMeta{}, err
		}

		// Phase 3.5 §2.2.1 — bidirectional canonicalization (best-effort).
		// On a SessionStart we just created a standalone frame for, try
		// once more (post-Upsert) to fold either:
		//   (a) self into an alive cross-type ancestor (reconcile, plan
		//       §2.1.1), or
		//   (b) any standalone cross-type descendants whose SessionStart
		//       raced ours and landed before our PPID-walk could see us
		//       as an ancestor (descendant scan, plan §2.1.2).
		if lifecycle == agentpkg.LifecycleSessionStart {
			canonicalized, parentStored, rerr := m.reconcileCreatedFrameAsProxy(stored, req, broadcastTs)
			if rerr != nil {
				return nil, FrameTraceMeta{}, rerr
			}
			if canonicalized {
				projection, perr := m.projectPane(req.TmuxPaneID)
				return projection, FrameTraceMeta{
					FrameID:       parentStored.FrameID,
					ParentFrameID: parentStored.ParentFrameID,
					Decision:      "updated_frame",
					Reason:        "post_upsert_canonicalization_self",
					Before:        before,
					After:         summarizeFrame(&parentStored),
				}, perr
			}
			// self stays standalone (or partial); try descendant scan.
			updated, cerr := m.canonicalizeDescendantsAfterUpsert(stored, broadcastTs)
			if cerr != nil {
				return nil, FrameTraceMeta{}, cerr
			}
			stored = updated
		}
	}
	projection, err := m.projectPane(req.TmuxPaneID)
	// Phase 3 — three-state reason (plan §1.4):
	//   parent_frame_found       → legacy lookup hit (line 220-228)
	//   daemon_restart_recovery  → rebuild via process tree hit (commit 3)
	//   no_parent_fallback       → all lookups + rebuild missed; frame still
	//                              created using req.AgentType as SOT (commit 4).
	// The Inspector uses these to distinguish recovered frames from降階 ones.
	reason := "no_parent_fallback"
	if stored.ParentFrameID != "" {
		reason = "parent_frame_found"
	} else if rebuiltMatched {
		reason = "daemon_restart_recovery"
	}
	decision := "created_frame"
	if frame != nil {
		decision = "updated_frame"
	}
	return projection, FrameTraceMeta{
		FrameID:          stored.FrameID,
		ParentFrameID:    stored.ParentFrameID,
		Decision:         decision,
		Reason:           reason,
		Before:           before,
		After:            summarizeFrame(&stored),
		MatchedAgentType: rebuiltAgentType,
	}, err
}

func (m *Module) projectPane(paneID string) (*SessionProjection, error) {
	if m.frames == nil {
		return nil, nil
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return nil, err
	}
	frames = m.filterPaneOwnedProjectionFrames(paneID, frames)
	projection := buildPaneProjection(paneID, frames)
	return &projection, nil
}

func (m *Module) filterProjectionFrames(frames []store.Frame) []store.Frame {
	if len(frames) == 0 {
		return frames
	}
	byPane := make(map[string][]store.Frame)
	for _, frame := range frames {
		byPane[frame.PaneID] = append(byPane[frame.PaneID], frame)
	}
	filtered := make([]store.Frame, 0, len(frames))
	for paneID, paneFrames := range byPane {
		filtered = append(filtered, m.filterPaneOwnedProjectionFrames(paneID, paneFrames)...)
	}
	return filtered
}

func (m *Module) filterPaneOwnedProjectionFrames(paneID string, frames []store.Frame) []store.Frame {
	if len(frames) == 0 || m == nil || m.tmux == nil {
		return frames
	}
	panePID, err := resolvePanePIDFn(m.tmux, paneID)
	if err != nil {
		return frames
	}
	filtered := make([]store.Frame, 0, len(frames))
	for _, frame := range frames {
		if projectionFrameEligibleForPane(frame, panePID) {
			filtered = append(filtered, frame)
		}
	}
	return filtered
}

func projectionFrameEligibleForPane(frame store.Frame, panePID int) bool {
	if !isPidAliveFn(frame.PID) {
		return false
	}
	actualStart, err := processStartTimeFn(frame.PID)
	if err != nil {
		return true
	}
	if actualStart != frame.ProcessStartTime {
		return false
	}
	return pidAncestorIncludesFn(frame.PID, panePID)
}

func frameID(frame *store.Frame) string {
	if frame == nil {
		return ""
	}
	return frame.FrameID
}

func summarizeFrame(frame *store.Frame) map[string]any {
	if frame == nil {
		return map[string]any{}
	}
	return map[string]any{
		"frame_id":         frame.FrameID,
		"pane_id":          frame.PaneID,
		"agent_type":       frame.AgentType,
		"pid":              frame.PID,
		"ppid":             frame.PPID,
		"parent_frame_id":  frame.ParentFrameID,
		"process_start_at": frame.ProcessStartTime,
		"status":           string(frame.Status),
		"subagents":        append([]agentpkg.SubagentRef(nil), frame.Subagents...),
	}
}

// updateSubagents mutates a frame's subagent list in response to a
// SubagentStart / SubagentStop event. On SubagentStart the first-write wins;
// an existing ref keeps its StartedAt/SourcePID/SourceStartTime/IsProxy.
//
// Identity key is kind-aware (R2 fix): proxy refs identify by
// (SourcePID, SourceStartTime) — the sender process — while native refs
// identify by ID (the agent_id string supplied by the provider). Cross-kind
// refs (one proxy, one native) never match even if ID strings coincide.
// This isolates namespaces so a native ref whose agent_id happens to collide
// with a synthesized proxy ID cannot shadow or evict a proxy ref, and vice
// versa.
func updateSubagents(current []agentpkg.SubagentRef, lifecycle agentpkg.LifecycleEventKind, ref agentpkg.SubagentRef) []agentpkg.SubagentRef {
	if current == nil {
		current = []agentpkg.SubagentRef{}
	}
	switch lifecycle {
	case agentpkg.LifecycleSubagentStart:
		for _, existing := range current {
			if subagentRefMatches(existing, ref) {
				return current
			}
		}
		return append(current, ref)
	case agentpkg.LifecycleSubagentStop:
		filtered := make([]agentpkg.SubagentRef, 0, len(current))
		for _, existing := range current {
			if !subagentRefMatches(existing, ref) {
				filtered = append(filtered, existing)
			}
		}
		return filtered
	default:
		return current
	}
}

// subagentRefMatches returns true when two refs identify the same subagent.
// Proxy refs compare by (SourcePID, SourceStartTime) plus a turn-aware
// SourceTurnID equality (L2 spec §3.2.A): if either side's SourceTurnID is
// empty the comparison falls back to process-level (preserves backward-compat
// with refs persisted before SourceTurnID was added and with cc/opencode
// providers that never populate it); if both sides are non-empty they must
// equal exactly. Native refs compare by ID. Cross-kind (one proxy, one
// native) is never a match — preserves the isolation between the two
// namespaces (see updateSubagents doc).
//
// This helper is used for turn-aware Stop targeted detach. Process-level
// lookup (used by attach/upsert in upsertProxyRefForBroker) goes through
// findProxyRefByBroker — DO NOT reuse this helper there: its turn-aware
// gate would treat a (PID, StartTime, turn_b) lookup against an existing
// (PID, StartTime, turn_a) ref as no-match and trigger an append-duplicate
// regression (spec §3.2 F1 fix).
func subagentRefMatches(a, b agentpkg.SubagentRef) bool {
	if a.IsProxy != b.IsProxy {
		return false
	}
	if a.IsProxy {
		if a.SourcePID != b.SourcePID || a.SourceStartTime != b.SourceStartTime {
			return false
		}
		// Process-level fallback: an unset SourceTurnID on either side
		// (legacy ref pre-L2, SessionStart attach without turn_id, or a
		// non-codex provider that never populates the field) reduces the
		// equality test to (PID, StartTime). Both sides non-empty → exact
		// turn equality (spec §3.2.A turn-level identity).
		if a.SourceTurnID == "" || b.SourceTurnID == "" {
			return true
		}
		return a.SourceTurnID == b.SourceTurnID
	}
	return a.ID == b.ID
}

// findProxyRefByBroker returns the index of the proxy ref matching this
// broker (PID + StartTime), regardless of SourceTurnID. Used for upsert/
// replace where the intent is to mutate a single broker's existing ref to
// a new turn_id (in-place) rather than appending a duplicate ref. Returns
// -1 if no proxy ref matches.
//
// Process-level only — turn-aware equality lives in subagentRefMatches and
// is intentionally NOT reused here. Reusing subagentRefMatches would make
// upsert miss an existing ref whose SourceTurnID differs from the incoming
// turnID, causing a duplicate ref to be appended (spec §3.2 F1 fix).
// Likewise, Stop targeted detach must NOT reuse this helper — that would
// drop a stale-turn ref whose owning broker has already moved on to a new
// turn (spec §3.2 F1 fix, dual direction).
func findProxyRefByBroker(refs []agentpkg.SubagentRef, pid int, startTime string) int {
	for i, ref := range refs {
		if ref.IsProxy && ref.SourcePID == pid && ref.SourceStartTime == startTime {
			return i
		}
	}
	return -1
}

// findNativeRefByID returns the index of the native (non-proxy) SubagentRef
// with the given ID, or -1 when no such ref exists. Pure read, no side
// effects.
//
// Callers use this to gate destructive native-ref operations (PdxStopFailure
// → LifecycleSubagentStop) so that a missing or non-matching agent_id is
// never reported as a successful detach. mutateSubagentsWithRetry's
// `applied=true` only signals UpsertIfUnchanged committed (LastSeenAt always
// refreshes) — it does NOT prove a ref was actually removed (spec §2.3).
// Without this pre-check we would broadcast a phantom "detached" trace step
// on every mismatched payload.
//
// Empty id short-circuits to -1 so the legacy break path fires for payloads
// whose agent_id field is absent (`raw["agent_id"] == nil` → "" via type
// assertion at the call site) or explicitly empty.
func findNativeRefByID(refs []agentpkg.SubagentRef, id string) int {
	if id == "" {
		return -1
	}
	for i, ref := range refs {
		if !ref.IsProxy && ref.ID == id {
			return i
		}
	}
	return -1
}

func syncProjectionState(currentStatus map[string]agentpkg.Status, subagents map[string][]agentpkg.SubagentRef, tmuxSession string, projection *SessionProjection) {
	if projection == nil || projection.TopFrame == nil {
		delete(currentStatus, tmuxSession)
		delete(subagents, tmuxSession)
		return
	}
	currentStatus[tmuxSession] = projection.TopFrame.Status
	subagents[tmuxSession] = append([]agentpkg.SubagentRef(nil), projection.Subagents...)
}

func buildProjectionNormalized(projection *SessionProjection, fallbackAgentType, eventName string, broadcastTs int64, result agentpkg.DeriveResult) agentpkg.NormalizedEvent {
	normalized := agentpkg.NormalizedEvent{
		AgentType:    fallbackAgentType,
		Status:       string(result.Status),
		Model:        result.Model,
		Subagents:    []agentpkg.SubagentRef{},
		RawEventName: eventName,
		BroadcastTs:  broadcastTs,
		Detail:       result.Detail,
	}
	if projection == nil {
		// Issue #717: when no projection exists for this session, "no top
		// frame" is the same semantic as the projection!=nil && TopFrame==nil
		// branch below — emit StatusClear so SPA's clear-handling path
		// triggers. Caller's empty result.Status used to leak through and
		// SPA ignored "" as a no-op. Defense in depth alongside the explicit
		// StatusClear at sweep.go:551.
		if normalized.Status == "" {
			normalized.Status = string(agentpkg.StatusClear)
		}
		return normalized
	}
	normalized.Subagents = append([]agentpkg.SubagentRef{}, projection.Subagents...)
	if projection.TopFrame == nil {
		normalized.Status = string(agentpkg.StatusClear)
		return normalized
	}
	normalized.AgentType = projection.TopFrame.AgentType
	normalized.Status = string(projection.TopFrame.Status)
	return normalized
}

// strFromDetail looks up a string field in a DeriveResult.Detail map.
// Returns "" when the key is missing or the value is not a string.
func strFromDetail(detail map[string]any, key string) string {
	if detail == nil {
		return ""
	}
	if v, ok := detail[key].(string); ok {
		return v
	}
	return ""
}

func (m *Module) projectionForSession(sessionName string) (*SessionProjection, error) {
	projections, err := m.liveFrameProjections()
	if err != nil {
		return nil, err
	}
	return m.selectSessionProjection(sessionName, projections), nil
}

func (m *Module) setProjectionTopStatus(sessionName string, status agentpkg.Status) (*SessionProjection, error) {
	projection, err := m.projectionForSession(sessionName)
	if err != nil || projection == nil || projection.TopFrame == nil {
		return projection, err
	}
	// Narrow update only status + last_seen_at (#632 R7): a whole-frame
	// Upsert from this path would round-trip a stale Subagents baseline
	// and clobber concurrent proxy/native attach/detach mutations on the
	// same row. Probe-driven status transitions have no business changing
	// Subagents — decouple the columns at the SQL layer.
	if err := m.frames.UpdateStatusAndLastSeen(projection.TopFrame.FrameID, status, time.Now().UnixNano()); err != nil {
		return nil, err
	}
	return m.projectionForSession(sessionName)
}

func (m *Module) selectSessionProjection(sessionName string, projections []SessionProjection) *SessionProjection {
	var selected *SessionProjection
	for i := range projections {
		name, _ := m.resolvePaneSession(projections[i].PaneID)
		if name != sessionName {
			continue
		}
		if selected == nil || projectionSortGreater(projections[i], *selected) {
			projection := projections[i]
			selected = &projection
		}
	}
	return selected
}

type namedProjection struct {
	SessionName string
	SessionCode string
	Projection  SessionProjection
}

func (m *Module) liveSessionProjections() ([]namedProjection, error) {
	projections, err := m.liveFrameProjections()
	if err != nil {
		return nil, err
	}
	if len(projections) == 0 {
		return nil, nil
	}
	selected := make(map[string]namedProjection)
	for _, projection := range projections {
		sessionName, sessionCode := m.resolvePaneSession(projection.PaneID)
		if sessionName == "" {
			continue
		}
		current, ok := selected[sessionName]
		if !ok || projectionSortGreater(projection, current.Projection) {
			selected[sessionName] = namedProjection{
				SessionName: sessionName,
				SessionCode: sessionCode,
				Projection:  projection,
			}
		}
	}
	sessionNames := make([]string, 0, len(selected))
	for sessionName := range selected {
		sessionNames = append(sessionNames, sessionName)
	}
	sort.Strings(sessionNames)
	out := make([]namedProjection, 0, len(sessionNames))
	for _, sessionName := range sessionNames {
		out = append(out, selected[sessionName])
	}
	return out, nil
}

func projectionSortGreater(candidate, current SessionProjection) bool {
	if candidate.TopFrame == nil {
		return false
	}
	if current.TopFrame == nil {
		return true
	}
	if candidate.TopFrame.StartedAt != current.TopFrame.StartedAt {
		return candidate.TopFrame.StartedAt > current.TopFrame.StartedAt
	}
	return candidate.TopFrame.FrameID > current.TopFrame.FrameID
}

// removeProxyRefForSender scans the pane's frames for a proxy SubagentRef
// whose SourcePID+SourceStartTime match the sender that is emitting SessionEnd,
// filters it out, refreshes the owning frame's LastSeenAt and persists via
// Upsert. Matching is by identity fields (SourcePID+SourceStartTime), not by
// ref.ID string, so the detach remains robust across potential future ID
// format changes.
//
// Returns (removed, ownerFrameAfter, ownerBefore, ownerAfter, err):
//   - removed=true with ownerFrameAfter populated when a ref was detached.
//   - removed=false, zeroFrame, nil, nil, nil when nothing matched.
func (m *Module) removeProxyRefForSender(paneID string, senderPID int, senderStartTime string, broadcastTs int64) (bool, store.Frame, any, any, error) {
	if m.frames == nil {
		return false, store.Frame{}, nil, nil, nil
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return false, store.Frame{}, nil, nil, err
	}
	for _, frame := range frames {
		if !subagentsContainProxySender(frame.Subagents, senderPID, senderStartTime) {
			continue
		}
		before := summarizeFrame(&frame)
		// Optimistic-concurrency detach: see attachProxyRefWithRetry doc.
		// #632 — the scan/list/update triplet is racy if two SessionEnds (or
		// a SessionEnd concurrent with a SessionStart) land on the same
		// parent. Reload + filter + UpsertIfUnchanged with retry.
		detached, stored, derr := m.detachProxyRefWithRetry(frame, senderPID, senderStartTime, broadcastTs)
		if derr != nil {
			return false, store.Frame{}, nil, nil, derr
		}
		if detached {
			return true, stored, before, summarizeFrame(&stored), nil
		}
		// Frame vanished or its ref was already removed by a concurrent
		// writer. Continue scanning other frames in the pane — a different
		// owner may still carry our sender's proxy ref.
	}
	return false, store.Frame{}, nil, nil, nil
}

// removeProxyRefForSenderTurn mirrors removeProxyRefForSender with a
// turn-aware identity gate (L2 spec §3.3.D). Scans the pane's frames for
// the proxy SubagentRef whose (SourcePID, SourceStartTime, SourceTurnID)
// triple matches the sender + turn that emitted Stop, then drops only that
// ref via detachProxyRefForSenderTurnWithRetry. Pane-scan (not parent-
// bound) intentionally — Stop hooks do not carry the parent FrameID and a
// SessionStart-attached ref may live on any frame in the pane.
//
// Returns (removed, ownerFrameAfter, ownerBefore, ownerAfter, err) with
// the same shape as removeProxyRefForSender so the two coexist as siblings
// rather than divergent return contracts. ownerBefore / ownerAfter are
// summarizeFrame outputs (any-typed for trace plumbing). When nothing
// matched returns (false, zeroFrame, nil, nil, nil).
//
// Process-level wildcard detach (used by Stop with empty/missing turn_id
// and by SessionEnd) goes through the existing removeProxyRefForSender;
// the two functions intentionally coexist (spec §3.2 boundary).
func (m *Module) removeProxyRefForSenderTurn(paneID string, senderPID int, senderStartTime, turnID string, broadcastTs int64) (bool, store.Frame, any, any, error) {
	if m.frames == nil {
		return false, store.Frame{}, nil, nil, nil
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return false, store.Frame{}, nil, nil, err
	}
	for _, frame := range frames {
		if !subagentsContainProxySenderTurn(frame.Subagents, senderPID, senderStartTime, turnID) {
			continue
		}
		before := summarizeFrame(&frame)
		detached, stored, derr := m.detachProxyRefForSenderTurnWithRetry(frame, senderPID, senderStartTime, turnID, broadcastTs)
		if derr != nil {
			return false, store.Frame{}, nil, nil, derr
		}
		if detached {
			return true, stored, before, summarizeFrame(&stored), nil
		}
		// Frame vanished or its ref was already removed by a concurrent
		// writer. Continue scanning other frames in the pane — a different
		// owner may still carry our sender's proxy ref for this turn.
	}
	return false, store.Frame{}, nil, nil, nil
}

// subagentsContainProxySender is a side-effect-free check used to short-
// circuit frames that don't need the read-modify-write retry loop.
func subagentsContainProxySender(refs []agentpkg.SubagentRef, senderPID int, senderStartTime string) bool {
	for _, ref := range refs {
		if ref.IsProxy && ref.SourcePID == senderPID && ref.SourceStartTime == senderStartTime {
			return true
		}
	}
	return false
}

// subagentsContainProxySenderTurn mirrors subagentsContainProxySender for
// the L2 turn-aware Stop targeted detach path (spec §3.3.D). All three
// identity fields (PID, StartTime, TurnID) must match — process-level
// matches alone are insufficient: a Stop carrying turn_b should not detach
// a ref whose owning broker has already moved on to turn_a in flight.
func subagentsContainProxySenderTurn(refs []agentpkg.SubagentRef, senderPID int, senderStartTime, turnID string) bool {
	for _, ref := range refs {
		if ref.IsProxy &&
			ref.SourcePID == senderPID &&
			ref.SourceStartTime == senderStartTime &&
			ref.SourceTurnID == turnID {
			return true
		}
	}
	return false
}

// mutateSubagentsWithRetry applies an updateSubagents mutation
// (SubagentStart add or SubagentStop remove) to frame.Subagents under
// optimistic concurrency. Each attempt re-merges via updateSubagents on top
// of the current baseline and issues UpsertIfUnchanged; on conflict it
// reloads via GetByIdentity and retries, bounded by proxyUpsertMaxAttempts.
//
// Returns (true, stored, nil) when the mutation is persisted;
// (false, zeroFrame, nil) when the frame was deleted mid-flight (caller
// decides whether to fall back, continue scanning, or report frame_missing).
// A non-nil error is only returned for storage failures or exhausted retries.
//
// Serves both native SubagentStart/Stop (from hook events) and proxy
// attach (via attachProxyRefWithRetry). All three paths share the same
// subagents_json read-modify-write cycle and would race without this helper.
func (m *Module) mutateSubagentsWithRetry(frame store.Frame, lifecycle agentpkg.LifecycleEventKind, ref agentpkg.SubagentRef, broadcastTs int64) (bool, store.Frame, error) {
	current := frame
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		expected := current.LastSeenAt
		current.Subagents = updateSubagents(current.Subagents, lifecycle, ref)
		current.LastSeenAt = broadcastTs
		ok, stored, err := m.frames.UpsertIfUnchanged(current, expected)
		if err != nil {
			return false, store.Frame{}, err
		}
		if ok {
			return true, stored, nil
		}
		reloaded, err := m.frames.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
		if err != nil {
			return false, store.Frame{}, err
		}
		if reloaded == nil {
			return false, store.Frame{}, nil
		}
		current = *reloaded
	}
	return false, store.Frame{}, fmt.Errorf("subagent mutation %s: exceeded %d retries for frame %s", lifecycle, proxyUpsertMaxAttempts, frame.FrameID)
}

// detachOutcome categorizes the result of mutateSubagentsAndStatusWithRetry
// so the caller can pick the right trace reason and broadcast path. The
// four outcomes are intentionally disjoint: each represents a distinct
// observable state in the storage layer that must produce a distinct
// trace step (per PR-A round-2 adversarial review, thread 019ded6{1,2,3}).
//
// PR-A round-2 codex review (R2) found that an inline retry loop folding
// "ref already gone after reload" and "retries exhausted" into a single
// frame_missing outcome could:
//
//  1. emit a phantom native_subagent_detached_on_stop_failure trace
//     when a concurrent writer detached the same ref between our
//     pre-check and a retry attempt;
//  2. silently drop the StopFailure status update under retry exhaustion
//     without surfacing an error to the caller.
//
// This helper fixes both by re-checking ref presence on every retry
// baseline and reporting the four outcomes separately.
type detachOutcome int

const (
	// detachOutcomeDetached: this attempt's UpsertIfUnchanged succeeded and
	// the target ref was actually present in the baseline that committed.
	// Caller emits native_subagent_detached_on_stop_failure.
	detachOutcomeDetached detachOutcome = iota
	// detachOutcomeRefAlreadyAbsent: a reload showed the target ref no
	// longer present (concurrent SubagentStop or another StopFailure for
	// the same agent_id won the race). No mutation issued for this path.
	// Caller MUST fall back to legacy post-switch status update so
	// StopFailure semantics still write Status=error / refresh LastSeenAt.
	detachOutcomeRefAlreadyAbsent
	// detachOutcomeFrameMissing: a reload returned nil because the row
	// was deleted concurrently (SessionEnd / sweep). Caller emits
	// frame_missing trace.
	detachOutcomeFrameMissing
)

// mutateSubagentsAndStatusWithRetry atomically removes a native ref AND
// updates frame Status in a single UpsertIfUnchanged transaction. Mirrors
// mutateSubagentsWithRetry's OCC retry loop but adds two contracts the
// plain helper does not provide:
//
//  1. Per-attempt ref-presence re-check: if the target ref disappears
//     between attempts (concurrent writer wins the race), abort with
//     detachOutcomeRefAlreadyAbsent so caller can avoid emitting a phantom
//     detach trace.
//  2. Atomic Status field write: Status persists in the same row update
//     as the Subagents change, eliminating the race window between
//     "ref removed" and "status updated to error" that a two-step
//     (mutate then UpdateHookPath) approach would leak.
//
// Retry exhaustion (UpsertIfUnchanged keeps conflicting while the row
// still exists across all proxyUpsertMaxAttempts iterations) returns an
// error, mirroring mutateSubagentsWithRetry's contract — the caller
// surfaces this via the standard handler error path.
//
// PR-A round-2 codex review threads 019ded61 / 019ded62 / 019ded63.
func (m *Module) mutateSubagentsAndStatusWithRetry(
	frame store.Frame,
	ref agentpkg.SubagentRef,
	status agentpkg.Status,
	broadcastTs int64,
) (detachOutcome, store.Frame, error) {
	current := frame
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		// Per-attempt re-check (R2 attacker / defender / health fix).
		// On the first iteration this re-confirms the caller's
		// pre-check (cheap, O(n) scan); on later iterations after a
		// reload it catches the race where another writer already
		// detached the ref. Without this, updateSubagents would
		// no-op and we'd commit Status+LastSeenAt under the new
		// "native_subagent_detached_on_stop_failure" reason despite
		// not actually removing anything.
		if findNativeRefByID(current.Subagents, ref.ID) < 0 {
			return detachOutcomeRefAlreadyAbsent, current, nil
		}
		expected := current.LastSeenAt
		next := current
		next.Subagents = updateSubagents(current.Subagents, agentpkg.LifecycleSubagentStop, ref)
		next.Status = status
		next.LastSeenAt = broadcastTs
		ok, stored, err := m.frames.UpsertIfUnchanged(next, expected)
		if err != nil {
			return 0, store.Frame{}, err
		}
		if ok {
			return detachOutcomeDetached, stored, nil
		}
		reloaded, err := m.frames.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
		if err != nil {
			return 0, store.Frame{}, err
		}
		if reloaded == nil {
			return detachOutcomeFrameMissing, store.Frame{}, nil
		}
		current = *reloaded
	}
	return 0, store.Frame{}, fmt.Errorf("subagent+status mutation: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, frame.FrameID)
}

// attachProxyRefWithRetry is a thin wrapper over mutateSubagentsWithRetry
// specialized for the proxy-attach path. Kept as a separate helper so the
// caller at applyFrameEvent reads as "attach proxy ref" rather than
// "SubagentStart mutation on the parent".
func (m *Module) attachProxyRefWithRetry(parent store.Frame, ref agentpkg.SubagentRef, broadcastTs int64) (bool, store.Frame, error) {
	return m.mutateSubagentsWithRetry(parent, agentpkg.LifecycleSubagentStart, ref, broadcastTs)
}

// upsertProxyRefForBroker inserts or in-place mutates a single broker's
// proxy ref on a parent frame, under optimistic concurrency. Used by the
// L2 codex turn-aware attach/upsert path (UserPromptSubmit / PreToolUse,
// spec §3.3.B/C and §3.4):
//
//   - If a proxy ref matching (pid, startTime) already exists, mutate its
//     SourceTurnID to turnID and refresh StartedAt — does NOT append a
//     duplicate even when SourceTurnID differs from the incoming turnID.
//   - Otherwise, append a new proxy ref with full identity. The ID format
//     is "proxy:codex:<pid>:<startTime>" matching the SessionStart fast-
//     path's proxy attach branch so a SessionStart followed by an upsert
//     on the same broker reuses the same ID rather than diverging.
//
// Each attempt re-issues UpsertIfUnchanged; on conflict the parent is
// reloaded via GetByIdentity and findProxyRefByBroker re-runs against the
// fresh refs (a concurrent attach by a SessionStart on the same broker
// could have appended a ref between attempts, in which case we mutate the
// just-attached ref rather than appending another).
//
// Returns (true, stored, nil) on success; (false, zeroFrame, nil) when
// the parent frame was deleted mid-flight (caller decides whether to fall
// back). Errors are surfaced for storage failures or exhausted retries.
//
// Does NOT reuse mutateSubagentsWithRetry. That helper's SubagentStart
// branch routes through updateSubagents → subagentRefMatches (turn-aware),
// which would treat a (PID, StartTime, turn_b) request against an existing
// (PID, StartTime, turn_a) ref as no-match and append a duplicate (spec
// §3.4 F1 fix). The dedicated process-level lookup here keeps the upsert
// at exactly one ref per broker.
func (m *Module) upsertProxyRefForBroker(parent store.Frame, pid int, startTime string, turnID string, broadcastTs int64) (bool, store.Frame, error) {
	current := parent
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		expected := current.LastSeenAt
		idx := findProxyRefByBroker(current.Subagents, pid, startTime)
		// Build the next Subagents slice. Allocate a fresh backing array so
		// an in-place mutation does not surprise the caller's snapshot or
		// the store layer (Upsert marshals the slice into JSON).
		next := make([]agentpkg.SubagentRef, len(current.Subagents))
		copy(next, current.Subagents)
		if idx >= 0 {
			// Unconditional overwrite. A parse-failed upsert (turnID == "")
			// downgrades the ref's SourceTurnID to empty rather than
			// preserving the previous turn — see the round-2/round-3
			// trade-off documented in spec §3.4 (parse-failure semantics):
			// preserving t_old here would let a legitimate late Stop(t_old)
			// targeted-detach the still-active ref of the next turn (the
			// more common race), so we accept the rarer double-malformed
			// race (parse-failed upsert + parse-failed empty-turn Stop) as
			// a known limitation instead.
			next[idx].SourceTurnID = turnID
			next[idx].StartedAt = broadcastTs
		} else {
			next = append(next, agentpkg.SubagentRef{
				ID:              fmt.Sprintf("proxy:codex:%d:%s", pid, startTime),
				Type:            "codex",
				StartedAt:       broadcastTs,
				SourcePID:       pid,
				SourceStartTime: startTime,
				IsProxy:         true,
				SourceTurnID:    turnID,
			})
		}
		current.Subagents = next
		current.LastSeenAt = broadcastTs
		ok, stored, err := m.frames.UpsertIfUnchanged(current, expected)
		if err != nil {
			return false, store.Frame{}, err
		}
		if ok {
			return true, stored, nil
		}
		reloaded, err := m.frames.GetByIdentity(parent.PaneID, parent.PID, parent.ProcessStartTime)
		if err != nil {
			return false, store.Frame{}, err
		}
		if reloaded == nil {
			return false, store.Frame{}, nil
		}
		current = *reloaded
	}
	return false, store.Frame{}, fmt.Errorf("upsertProxyRefForBroker: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, parent.FrameID)
}

// detachProxyRefWithRetry mirrors attachProxyRefWithRetry for the SessionEnd
// cleanup path: reload parent, filter out the matching proxy ref, persist
// via UpsertIfUnchanged, retry on conflict. Returns (false, zeroFrame, nil)
// when the frame was already gone or its ref was already cleaned up by a
// concurrent writer (caller continues scanning other frames in the pane).
func (m *Module) detachProxyRefWithRetry(owner store.Frame, senderPID int, senderStartTime string, broadcastTs int64) (bool, store.Frame, error) {
	current := owner
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		hit := -1
		for i, ref := range current.Subagents {
			if ref.IsProxy && ref.SourcePID == senderPID && ref.SourceStartTime == senderStartTime {
				hit = i
				break
			}
		}
		if hit < 0 {
			// Ref already removed by a concurrent writer.
			return false, store.Frame{}, nil
		}
		expected := current.LastSeenAt
		filtered := make([]agentpkg.SubagentRef, 0, len(current.Subagents)-1)
		filtered = append(filtered, current.Subagents[:hit]...)
		filtered = append(filtered, current.Subagents[hit+1:]...)
		current.Subagents = filtered
		current.LastSeenAt = broadcastTs
		ok, stored, err := m.frames.UpsertIfUnchanged(current, expected)
		if err != nil {
			return false, store.Frame{}, err
		}
		if ok {
			return true, stored, nil
		}
		reloaded, err := m.frames.GetByIdentity(owner.PaneID, owner.PID, owner.ProcessStartTime)
		if err != nil {
			return false, store.Frame{}, err
		}
		if reloaded == nil {
			return false, store.Frame{}, nil
		}
		current = *reloaded
	}
	return false, store.Frame{}, fmt.Errorf("proxy detach: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, owner.FrameID)
}

// detachProxyRefForSenderTurnWithRetry mirrors detachProxyRefWithRetry
// for the L2 turn-aware detach path: reload owner, filter out the proxy
// ref whose (PID, StartTime, TurnID) triple matches, persist via
// UpsertIfUnchanged, retry on conflict. All three identity fields must
// match to drop — process-level matches alone are insufficient (spec
// §3.3.D, see subagentsContainProxySenderTurn doc).
//
// Returns (false, zeroFrame, nil) when the ref is already gone or the
// frame was deleted mid-flight. Caller continues scanning other frames in
// the pane (top-level loop in removeProxyRefForSenderTurn).
func (m *Module) detachProxyRefForSenderTurnWithRetry(owner store.Frame, senderPID int, senderStartTime, turnID string, broadcastTs int64) (bool, store.Frame, error) {
	current := owner
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		hit := -1
		for i, ref := range current.Subagents {
			if ref.IsProxy &&
				ref.SourcePID == senderPID &&
				ref.SourceStartTime == senderStartTime &&
				ref.SourceTurnID == turnID {
				hit = i
				break
			}
		}
		if hit < 0 {
			// Ref already removed or turn drift by a concurrent writer.
			return false, store.Frame{}, nil
		}
		expected := current.LastSeenAt
		filtered := make([]agentpkg.SubagentRef, 0, len(current.Subagents)-1)
		filtered = append(filtered, current.Subagents[:hit]...)
		filtered = append(filtered, current.Subagents[hit+1:]...)
		current.Subagents = filtered
		current.LastSeenAt = broadcastTs
		ok, stored, err := m.frames.UpsertIfUnchanged(current, expected)
		if err != nil {
			return false, store.Frame{}, err
		}
		if ok {
			return true, stored, nil
		}
		reloaded, err := m.frames.GetByIdentity(owner.PaneID, owner.PID, owner.ProcessStartTime)
		if err != nil {
			return false, store.Frame{}, err
		}
		if reloaded == nil {
			return false, store.Frame{}, nil
		}
		current = *reloaded
	}
	return false, store.Frame{}, fmt.Errorf("proxy turn detach: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, owner.FrameID)
}

// firstAliveAgentInTreeFn is the test seam that indirects
// Prober.FirstAliveAgentInTree so tryRebuildFromProcessTree can be exercised
// without a wired-up prober (m.prober is nil in newTestModule). Mirrors the
// pattern of readProcessInfoFn / isPidAliveFn / processStartTimeFn declared in
// verify.go — unit tests override the package-level variable and restore it
// via t.Cleanup.
var firstAliveAgentInTreeFn = func(m *Module, target string) (string, int, error) {
	if m == nil || m.prober == nil {
		return "", 0, nil
	}
	return m.prober.FirstAliveAgentInTree(target)
}

// tryRebuildFromProcessTree attempts to recover a frame after daemon restart
// by inspecting the pane's live process tree via the Prober. Triggered when
// the standard lookup chain (GetByIdentity → findProxyParent → FindByPanePID)
// all miss on a SessionStart (Phase 3 plan §1.2.2).
//
// Behavior:
//   - Delegates to prober.FirstAliveAgentInTree(req.TmuxPaneID)
//   - Match → returns (agentType, true, nil)
//   - No match → returns ("", false, nil)
//   - Any error → returns ("", false, err); caller is expected to fail-soft
//
// Unwired in Commit 2: applyFrameEvent does not yet call this helper — that
// wiring lands in Commit 3, which also introduces the reason_code contract
// for the rebuild path.
func (m *Module) tryRebuildFromProcessTree(req EventRequest) (string, bool, error) {
	agentType, _, err := firstAliveAgentInTreeFn(m, req.TmuxPaneID)
	if err != nil {
		return "", false, err
	}
	if agentType == "" {
		return "", false, nil
	}
	return agentType, true, nil
}

// reconcileCreatedFrameAsProxy attempts to canonicalize a freshly-created
// SessionStart frame against an alive cross-type ancestor. If
// findProxyParent locates a candidate, attaches a proxy ref to the
// ancestor + best-effort deletes self's standalone row.
//
// Best-effort delete: if DeleteIfUnchanged fails because a concurrent
// writer touched the child row (sweep tick, SubagentStart hook, etc.),
// MetricPartialCanonicalizationCreated +1 and we return — sweep
// canonicalize will retry within sweepInterval=2s and projection-layer
// dedup hides the partial state from SPA in the meantime. Plan §2.1.1.
//
// Returns:
//
//	(true, parentStored, nil)  — attach + delete both succeeded; caller
//	                             should project the pane and emit a
//	                             post_upsert_canonicalization_self trace.
//	(false, zero, nil)         — no ancestor / parent vanished
//	                             mid-attach / partial state (attach
//	                             succeeded, delete failed; counted in
//	                             metrics, repaired by sweep).
//	(false, zero, err)         — storage failure; caller propagates.
//
// Unlike v2/v3 of this design there is no rollback path: partial state
// is treated as a legal transient (see plan §0.2 evidence points).
func (m *Module) reconcileCreatedFrameAsProxy(stored store.Frame, req EventRequest, broadcastTs int64) (bool, store.Frame, error) {
	parent, err := m.findProxyParent(req)
	if err != nil {
		return false, store.Frame{}, err
	}
	if parent == nil {
		return false, store.Frame{}, nil
	}
	ref := agentpkg.SubagentRef{
		ID:              fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime),
		Type:            req.AgentType,
		StartedAt:       broadcastTs,
		SourcePID:       req.SenderPID,
		SourceStartTime: req.SenderStartTime,
		IsProxy:         true,
	}
	attached, parentStored, aerr := m.attachProxyRefWithRetry(*parent, ref, broadcastTs)
	if aerr != nil {
		return false, store.Frame{}, aerr
	}
	if !attached {
		// Parent vanished mid-flight (concurrent SessionEnd / sweep).
		// Caller falls back to leaving self standalone.
		return false, store.Frame{}, nil
	}
	deleted, derr := m.frames.DeleteIfUnchanged(stored.FrameID, stored.LastSeenAt)
	if derr != nil {
		return false, store.Frame{}, derr
	}
	if !deleted {
		// Partial state: parent has the proxy ref, self still
		// standalone. Acceptable transient — projection dedup hides
		// the orphan child from SPA, sweep canonicalize repairs
		// within 2s. v8 M4 fix (codex round PR-2 attack): we still
		// report canonicalized=true here so the caller emits an
		// updated_frame / post_upsert_canonicalization_self trace
		// that matches what the SPA actually sees (parent + proxy
		// ref). Reporting canonicalized=false would have routed the
		// caller to the standalone created_frame trace, diverging
		// from projection (trace ≠ projection). Metric still +1 to
		// surface partial-state rate via expvar.
		agentpkg.MetricPartialCanonicalizationCreated.Add(1)
		log.Printf("phase3.5: reconcile partial state child %s parent %s (sweep will repair within 2s)", stored.FrameID, parentStored.FrameID)
	}
	return true, parentStored, nil
}

// pidIsAncestorOfWithCap walks descendantPID's PPID chain (capped at
// maxDepth) looking for ancestorPID. Returns true on hit; false on miss,
// depth exhaustion, readProcessInfoFn error, or self-loop (info.PPID ==
// info.PID).
//
// Used by canonicalizeDescendantsAfterUpsert to confirm a candidate
// descendant frame's process actually descends from self.PID before
// folding it into a proxy ref. Mirrors findProxyParent's PPID walk
// semantics but starts from descendant↑ rather than self↑.
//
// Phase 3.5 plan §2.1.2.
func pidIsAncestorOfWithCap(descendantPID, ancestorPID, maxDepth int) bool {
	current := descendantPID
	for depth := 0; depth < maxDepth; depth++ {
		info, err := readProcessInfoFn(current)
		if err != nil {
			return false
		}
		if info.PPID == ancestorPID {
			return true
		}
		if info.PPID <= 1 || info.PPID == current {
			return false
		}
		current = info.PPID
	}
	return false
}

// candidateHasOwnedState reports whether a frame carries live state that
// must be preserved during canonicalization. Returns true when the frame
// has any native (IsProxy=false) subagent ref OR any live identity-
// verified IsProxy ref. Returns true conservatively on a
// processStartTime read error to avoid dropping state on uncertainty.
//
// Used by both hot-path canonicalizeDescendantsAfterUpsert and sweep
// canonicalizePane (PR-3.5b §2.2.1) to decide whether folding a
// candidate as a proxy ref of an ancestor (and deleting its row) is
// safe. Sharing one classifier ensures both call sites cannot drift on
// the owned-state semantics — any change here lands in both at once.
//
// A candidate carrying ONLY stale (dead PID / PID-reused) IsProxy refs
// is considered free of owned state — those refs would be reaped by
// pruneDeadProxyRefs anyway, so dropping them with the row is
// equivalent to letting prune detach them later.
func candidateHasOwnedState(candidate store.Frame) bool {
	for _, ref := range candidate.Subagents {
		if !ref.IsProxy {
			// Native ref → real owned state.
			return true
		}
		// IsProxy: live + identity-verified counts as owned;
		// dead/PID-reused is stale and safe to drop with the fold.
		if !isPidAliveFn(ref.SourcePID) {
			continue
		}
		actualStart, sterr := processStartTimeFn(ref.SourcePID)
		if sterr != nil {
			// Read error → defensive; treat as owned (don't drop
			// state on uncertainty). Mirrors findProxyParent's
			// "lookup error → don't infer" convention.
			return true
		}
		if actualStart != ref.SourceStartTime {
			continue // PID reuse; stale
		}
		// Live + identity-verified IsProxy → owned.
		return true
	}
	return false
}

// canonicalizeDescendantsAfterUpsert scans self.PaneID for standalone
// cross-type descendants whose live PPID chain passes through self.PID and
// whose stored ProcessStartTime matches the live process's actual start
// time, folding each as a proxy ref on self. Plan §2.1.2.
//
// Best-effort delete: when DeleteIfUnchanged returns (false, nil) due to a
// concurrent writer touching the candidate row, the partial state (ref
// attached on self + standalone candidate still present) is acceptable —
// projection-layer dedup hides it from SPA, sweep canonicalizePane
// retries within 2s. Increments MetricPartialCanonicalizationCreated.
//
// Identity gate (alive + processStartTimeFn match) prevents PID-reuse
// stale rows from polluting self.Subagents (codex round 2 G2; same logic
// landed in PR-2b findProxyParent). Same-AgentType candidates are skipped
// — proxy refs are cross-type-only by definition.
//
// Returns the latest stored self frame (carrying all successful folds);
// callers replace their local copy with this value because each
// attachProxyRefWithRetry can bump self.LastSeenAt. Storage errors
// propagate as a non-nil error and abort further folds — the partial
// progress is left for sweep.
func (m *Module) canonicalizeDescendantsAfterUpsert(self store.Frame, broadcastTs int64) (store.Frame, error) {
	if m.frames == nil {
		return self, nil
	}
	frames, err := m.frames.ListByPane(self.PaneID)
	if err != nil {
		return self, err
	}
	current := self
	for _, candidate := range frames {
		if candidate.FrameID == current.FrameID {
			continue
		}
		if candidate.AgentType == current.AgentType {
			continue
		}
		if !pidIsAncestorOfWithCap(candidate.PID, current.PID, proxyMaxDepth) {
			continue
		}
		// Codex round 2 #O2 refinement: protect candidates that own
		// LIVE state (native refs or live identity-verified IsProxy
		// refs) from being folded. The original v8 M2 guard (len > 0)
		// over-protected — a candidate carrying ONLY stale dead/PID-
		// reused IsProxy refs is still a race-window artifact safe
		// to fold (the stale ref would be reaped by sweep prune
		// anyway, and dropping it along with the candidate row is
		// equivalent). Distinguishing these classes lets the hot-path
		// fold race-window standalones even when sweep prune has not
		// yet cleared their stale leftovers.
		//
		// PR-3.5b §2.2.1: classifier extracted as candidateHasOwnedState
		// so sweep canonicalizePane shares the exact same semantics —
		// any drift between the two would create different fold
		// decisions for identical candidate states.
		if candidateHasOwnedState(candidate) {
			continue
		}
		// Identity gate (PR-2b alignment + plan §2.1.2). Mirrors
		// findProxyParent's "live + start_time match" check on the
		// other end of the walk — keeps both sides of canonicalization
		// honest about which candidates count as alive descendants.
		if !isPidAliveFn(candidate.PID) {
			continue
		}
		actualStart, sterr := processStartTimeFn(candidate.PID)
		if sterr != nil || actualStart != candidate.ProcessStartTime {
			continue
		}
		ref := agentpkg.SubagentRef{
			ID:              fmt.Sprintf("proxy:%s:%d:%s", candidate.AgentType, candidate.PID, candidate.ProcessStartTime),
			Type:            candidate.AgentType,
			StartedAt:       broadcastTs,
			SourcePID:       candidate.PID,
			SourceStartTime: candidate.ProcessStartTime,
			IsProxy:         true,
		}
		attached, parentStored, aerr := m.attachProxyRefWithRetry(current, ref, broadcastTs)
		if aerr != nil {
			return current, aerr
		}
		if !attached {
			// Self frame vanished mid-flight (sweep / SessionEnd).
			// Stop folding — caller will project the pane and surface
			// frame_missing.
			return current, nil
		}
		deleted, derr := m.frames.DeleteIfUnchanged(candidate.FrameID, candidate.LastSeenAt)
		if derr != nil {
			return parentStored, derr
		}
		if !deleted {
			// Partial state: proxy attached on self + candidate row
			// still standalone. Acceptable transient — projection
			// dedup hides from SPA, sweep canonicalize repairs within
			// 2s. Continue scanning other candidates.
			agentpkg.MetricPartialCanonicalizationCreated.Add(1)
			log.Printf("phase3.5: descendant scan partial state child %s parent %s (sweep will repair within 2s)", candidate.FrameID, parentStored.FrameID)
		}
		current = parentStored
	}
	return current, nil
}

// findProxyParent reports the cross-type ancestor frame a SessionStart should
// be folded into, or nil when it should not fold. Contract unchanged; the
// traversal now lives in classifyAncestor (see plan §1.4 for the full
// contract, spec §4.3 for the verdict split).
//
// Returns (parent, nil) when a proxy candidate is found; (nil, nil) when the
// walk should not proxy-attach (no ancestor has a frame / same-type hard
// stop / all cross-type candidates stale or dead / depth exceeded / proc info
// or start_time read errors that make identity unverifiable). Non-nil error
// is returned only when the frames store fails.
func (m *Module) findProxyParent(req EventRequest) (*store.Frame, error) {
	verdict, parent, err := m.classifyAncestor(req)
	if err != nil {
		return nil, err
	}
	if verdict == VerdictProxyParent {
		return parent, nil
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Phase 2 P2-T1 — markDelegatingRef / unmarkDelegatingRef
// (spec §3.5 / plan §2 P2-T1)
// ---------------------------------------------------------------------------

// markDelegatingRef appends toolUseID to DelegatingToolUseIDs (deduped) and
// sets Delegating=true on the SubagentRef whose ID matches agentID, on the
// frame at (paneID, senderPID, senderStartTime). Idempotent — re-mark with
// same toolUseID is a no-op (slice membership check).
//
// Mirror upsertProxyRefForBroker pattern (frame_ops.go:1216-1297). Does NOT
// reuse mutateSubagentsWithRetry — that helper's SubagentStart branch routes
// through updateSubagents → subagentRefMatches (turn-aware), which only
// supports SubagentStart append-if-missing / SubagentStop remove-matching,
// not in-place mutation of an existing ref's fields (frame_ops.go:825-848 /
// plan §0.3 B1). Same UpsertIfUnchanged retry cap (proxyUpsertMaxAttempts).
//
// Race scope (spec L7): when PreToolUse arrives before SubagentStart has
// established the ref (regression #10/#12), this is a silent no-op — by
// design, since attaching a flag to a non-existent ref has no meaningful
// semantics; recovers when subagent ends or on the next tool use.
//
// Lookup excludes proxy refs (ref.IsProxy=true) even when their ID matches:
// Delegating is a cc-native subagent state by design (spec §3.1). If a proxy
// ref happens to share an ID with a real native subagent (rare namespace
// collision pinned by TestUpdateSubagents_ProxyNativeIDNamespacesAreIsolated)
// the proxy ref must not absorb the flag — otherwise the native ref stays
// blue while the proxy ref ends up orange-via-delegation, defeating the
// visual signal. Mirror filter applied in unmarkDelegatingRef. The IsProxy
// invariant rule (plan §0.3 B-rule) is preserved: this filter only reads
// IsProxy, never writes it.
//
// Invariant: Delegating == len(DelegatingToolUseIDs) > 0.
func (m *Module) markDelegatingRef(paneID string, senderPID int, senderStartTime string, agentID, toolUseID string, broadcastTs int64) error {
	parent, err := m.frames.GetByIdentity(paneID, senderPID, senderStartTime)
	if err != nil {
		return err
	}
	if parent == nil {
		// Spec L7: PreToolUse-before-SubagentStart silent no-op.
		return nil
	}

	current := *parent
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		expected := current.LastSeenAt

		idx := -1
		for i, ref := range current.Subagents {
			// Skip proxy refs even on ID match — see helper comment for
			// the namespace-collision rationale.
			if ref.ID == agentID && !ref.IsProxy {
				idx = i
				break
			}
		}
		if idx < 0 {
			// Ref not present — spec §3.5 race: PreToolUse beat SubagentStart.
			return nil
		}

		// Build a fresh slice so mutating next[idx] never aliases the caller's
		// snapshot or any other reader of current.Subagents.
		next := make([]agentpkg.SubagentRef, len(current.Subagents))
		copy(next, current.Subagents)

		// Dedupe append toolUseID into a fresh backing array (avoid sharing
		// with the existing ref's slice).
		ids := make([]string, 0, len(next[idx].DelegatingToolUseIDs)+1)
		ids = append(ids, next[idx].DelegatingToolUseIDs...)
		already := false
		for _, id := range ids {
			if id == toolUseID {
				already = true
				break
			}
		}
		if !already {
			ids = append(ids, toolUseID)
		}
		// FIFO evict-oldest enforcement (spec §10 / round-2 finding #2).
		// Cap kicks in when degenerate hook streams keep mark'ing without
		// matching PostToolUse — preserves Delegating=true so the visual
		// signal does not flap; surfaces a dev-mode log for operators.
		if len(ids) > MaxDelegatingToolUseIDs {
			if isDevMode() {
				log.Printf("[delegation] cap reached: evicting %d oldest entries (agent_id=%s pane=%s)",
					len(ids)-MaxDelegatingToolUseIDs, agentID, paneID)
			}
			ids = ids[len(ids)-MaxDelegatingToolUseIDs:]
		}
		next[idx].DelegatingToolUseIDs = ids
		next[idx].Delegating = len(ids) > 0

		current.Subagents = next
		current.LastSeenAt = broadcastTs
		ok, _, err := m.frames.UpsertIfUnchanged(current, expected)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		reloaded, rerr := m.frames.GetByIdentity(paneID, senderPID, senderStartTime)
		if rerr != nil {
			return rerr
		}
		if reloaded == nil {
			// Frame disappeared mid-flight — silent no-op (mirror
			// upsertProxyRefForBroker's reload-nil branch).
			return nil
		}
		current = *reloaded
	}
	return fmt.Errorf("markDelegatingRef: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, current.FrameID)
}

// unmarkDelegatingRef removes toolUseID from DelegatingToolUseIDs on the ref
// whose ID == agentID, then recomputes Delegating = len(remaining) > 0. No-op
// if ref not found, ID not in slice, or whole frame missing (covers
// PostToolUse arriving after SubagentStop, PostToolUseFailure for non-codex
// Bash, etc.).
//
// Mirror upsertProxyRefForBroker pattern (frame_ops.go:1216-1297). Does NOT
// reuse mutateSubagentsWithRetry — see markDelegatingRef rationale.
//
// Lookup excludes proxy refs (ref.IsProxy=true) for the same reason as
// markDelegatingRef: Delegating is a cc-native subagent state. See
// markDelegatingRef helper comment for the namespace-collision rationale.
//
// Invariant: Delegating == len(DelegatingToolUseIDs) > 0.
func (m *Module) unmarkDelegatingRef(paneID string, senderPID int, senderStartTime string, agentID, toolUseID string, broadcastTs int64) error {
	parent, err := m.frames.GetByIdentity(paneID, senderPID, senderStartTime)
	if err != nil {
		return err
	}
	if parent == nil {
		return nil
	}

	current := *parent
	for attempt := 0; attempt < proxyUpsertMaxAttempts; attempt++ {
		expected := current.LastSeenAt

		idx := -1
		for i, ref := range current.Subagents {
			// Skip proxy refs even on ID match — see markDelegatingRef.
			if ref.ID == agentID && !ref.IsProxy {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil
		}

		next := make([]agentpkg.SubagentRef, len(current.Subagents))
		copy(next, current.Subagents)

		// Filter into a fresh slice (do not mutate the caller's backing
		// array via append/in-place writes).
		filtered := make([]string, 0, len(next[idx].DelegatingToolUseIDs))
		for _, id := range next[idx].DelegatingToolUseIDs {
			if id != toolUseID {
				filtered = append(filtered, id)
			}
		}
		next[idx].DelegatingToolUseIDs = filtered
		next[idx].Delegating = len(filtered) > 0

		current.Subagents = next
		current.LastSeenAt = broadcastTs
		ok, _, err := m.frames.UpsertIfUnchanged(current, expected)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		reloaded, rerr := m.frames.GetByIdentity(paneID, senderPID, senderStartTime)
		if rerr != nil {
			return rerr
		}
		if reloaded == nil {
			return nil
		}
		current = *reloaded
	}
	return fmt.Errorf("unmarkDelegatingRef: exceeded %d retries for frame %s", proxyUpsertMaxAttempts, current.FrameID)
}
