package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

// statuslineMutex serializes concurrent /statusline/setup requests.
// CC settings.json is a shared resource; atomic rename doesn't protect
// read-modify-write ordering across simultaneous install/remove calls.
var statuslineMutex sync.Mutex

var getenvFn = os.Getenv

const (
	titleMarkerStart = "# >>> purdex agent-title >>>"
	titleMarkerLine  = "set -gw allow-set-title on"
	titleMarkerEnd   = "# <<< purdex agent-title <<<"
)

type titleStatusResponse struct {
	AllowSetTitle     bool   `json:"allow_set_title"`
	Installed         bool   `json:"installed"`
	RuntimeApplied    bool   `json:"runtime_applied"`
	ManagedConfigPath string `json:"managed_config_path"`
	Error             string `json:"error"`
}

type titleCapability struct {
	State string `json:"state"`
	Note  string `json:"note"`
}

// testNoncePrefix identifies statusline self-test POSTs to /api/agent/status.
// Real tmux session names cannot start with this prefix; the SPA self-test
// panel generates nonces like "__pdx_test_<random>" so we can route them down
// a dedicated path that signals the test observer and broadcasts keyed by the
// nonce, without touching the production snapshot map / session lookup.
const testNoncePrefix = "__pdx_test_"

// resolveStatuslineInstaller returns the StatuslineInstaller for the agent
// named by the request path variable "agent", or writes a 404 JSON error and
// returns (nil, false). Used by both /statusline/status and /statusline/setup.
func (m *Module) resolveStatuslineInstaller(w http.ResponseWriter, r *http.Request) (agentpkg.StatuslineInstaller, bool) {
	agentType := r.PathValue("agent")
	if agentType != "cc" {
		http.Error(w, `{"error":"unsupported agent"}`, http.StatusNotFound)
		return nil, false
	}
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent"}`, http.StatusNotFound)
		return nil, false
	}
	installer, ok := provider.(agentpkg.StatuslineInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support statusline"}`, http.StatusNotFound)
		return nil, false
	}
	return installer, true
}

// EventRequest is the JSON body expected by POST /api/agent/event.
//
// PurdexName is the daemon-internal stable identifier carried in the JSON
// payload as `purdex_name`. The pre-W2 `event_name` key was retained as an
// unmarshal-only alias during Phase 1/2 of the W2 rollout while the
// cc/codex/opencode CLIs and plugins migrated to the new field name; Phase 3
// (P3-T5) removed that alias so standard struct unmarshal with the
// `purdex_name` tag is the only shape now accepted.
type EventRequest struct {
	TmuxSession string `json:"tmux_session"`
	// TmuxSessionID is the immutable tmux session identifier in `$N` format.
	// When present, the daemon resolves the session code via a pure
	// EncodeSessionID call, completely bypassing the name→code cache and
	// closing the rename-race window where an external
	// `tmux kill-session foo && tmux new-session -s foo` could otherwise
	// alias the old code via a stale cache entry. Empty string falls
	// through to the existing name-based path for backward compat with
	// older pdx hook binaries.
	TmuxSessionID   string          `json:"tmux_session_id,omitempty"`
	TmuxPaneID      string          `json:"tmux_pane_id"`
	PurdexName      string          `json:"purdex_name"`
	RawEvent        json.RawMessage `json:"raw_event"`
	AgentType       string          `json:"agent_type"`
	SenderPID       int             `json:"sender_pid"`
	SenderStartTime string          `json:"sender_start_time"`
	SenderUncertain bool            `json:"sender_uncertain"`

	// identitySeq versions this event's identity write against the other
	// events in flight for the same frame. Unexported on purpose: it is not
	// part of the wire shape and no sender may supply it — handleEvent stamps
	// it on arrival, and that arrival order is the whole meaning of the
	// number. See FramesStore.NextIdentitySeq.
	identitySeq int64
}

// classifyLifecycle resolves an EventRequest to its LifecycleEventKind via
// the post-W2 two-branch decision tree (spec §3.4.2):
//
//  1. Catalog hit (provider implements HookInstaller and Events() lookup
//     by PurdexName succeeds) — use spec.Lifecycle. LifecycleNone is a
//     legitimate hit value for events with no frame-mutation effect
//     (PdxNotification, PdxPermissionRequest, etc.).
//  2. Catalog miss — return LifecycleNone. The handler surfaces a catalog
//     miss as event_not_in_catalog via DeriveStatus's Valid=false branch
//     before this lifecycle classification is consulted; LifecycleNone
//     therefore behaves as a no-op for any code that did reach this point.
//
// The pre-W2 third branch — `isLegacyHookForUnmigrated` falling through to a
// hardcoded per-agent literal-string switch — was removed in P3-T6 once all
// three agents (cc Phase 1, codex Phase 2, opencode Phase 3) populated their
// catalogs with PurdexName + Lifecycle. provider may be nil; branch 1 is
// then skipped.
func classifyLifecycle(provider agentpkg.AgentProvider, req EventRequest) agentpkg.LifecycleEventKind {
	if installer, ok := provider.(agentpkg.HookInstaller); ok {
		if spec, found := agentpkg.LookupByPurdexName(installer.Events(), req.PurdexName); found {
			return spec.Lifecycle
		}
	}
	return agentpkg.LifecycleNone
}

// classifyLifecycleForReq is the Module-bound counterpart to
// classifyLifecycle: it resolves the request's provider via m.registry and
// then runs the lookup. Returns LifecycleNone when the registry is missing
// or the agent_type is unknown — same effect as a catalog miss.
// frame_ops.go's hot path uses this so callers don't replicate the registry /
// type-assert lookup at every dispatch site.
func (m *Module) classifyLifecycleForReq(req EventRequest) agentpkg.LifecycleEventKind {
	if m == nil || m.registry == nil {
		return agentpkg.LifecycleNone
	}
	provider, _ := m.registry.Get(req.AgentType)
	return classifyLifecycle(provider, req)
}

// handleEvent handles POST /api/agent/event.
// It stores the hook event and broadcasts normalized events to WS subscribers.
func (m *Module) handleEvent(w http.ResponseWriter, r *http.Request) {
	var req EventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Normalize sender_start_time once at the boundary. verify.go:52 already
	// TrimSpaces both sides of its compare, but downstream identity lookups
	// (findProxyRefByBroker, removeProxyRefForSender, GetByIdentity) use
	// req.SenderStartTime as a raw exact-match key. Without canonicalization
	// here, a hook payload with a padded value (e.g. " t1 ") would pass
	// verify yet miss every L2 lookup, leaking refs or splitting identity
	// (round-2 A3).
	req.SenderStartTime = strings.TrimSpace(req.SenderStartTime)

	if req.TmuxSession == "" || req.TmuxPaneID == "" || req.AgentType == "" || req.PurdexName == "" || req.SenderPID == 0 {
		http.Error(w, `{"error":"schema_invalid"}`, http.StatusBadRequest)
		return
	}

	if req.SenderStartTime == "" && !req.SenderUncertain {
		http.Error(w, `{"error":"schema_invalid"}`, http.StatusBadRequest)
		return
	}

	// The identity version is allocated HERE — before verification, which is
	// the first thing on this path that waits. Verification reads the sender's
	// process with `ps` and asks tmux about the pane, and nothing makes two
	// hooks from one process clear it in the order they arrived (opencode
	// switches session inside a single process without a SessionStart, spec
	// §3.3). A version taken after verification would therefore order identity
	// writes by which goroutine finished waiting first, and an event that
	// arrived EARLIER but verified SLOWER would carry the higher version and
	// overwrite the identity of the event that came after it —
	// UpdateSessionIdentity's guard cannot see that, it can only compare the
	// numbers it is handed.
	//
	// It is deliberately not broadcastTs, and not any other clock reading: see
	// FramesStore.NextIdentitySeq. A rejected event simply burns its number,
	// which costs nothing — the counter only has to increase.
	if m.frames != nil {
		req.identitySeq = m.frames.NextIdentitySeq()
	}

	trace := beginHookTrace(m.traceSink, req)
	traceFinished := false
	defer func() {
		if !traceFinished {
			trace.Finish("aborted", "handler_return")
		}
	}()

	if isDevMode() {
		log.Printf("[hook] trigger session=%s agent=%s purdex_name=%s chain_id=%s",
			req.TmuxSession, req.AgentType, req.PurdexName, trace.ChainID())
	}

	if decision := verifyEventFn(m, req); !decision.Accepted {
		trace.Verify(req, "rejected", decision.Reason, map[string]any{"decision": "rejected", "reason": decision.Reason})
		trace.Finish("completed", "verify_rejected")
		traceFinished = true
		writeVerifyRejected(w, req, decision.Reason)
		return
	}
	trace.Verify(req, "accepted", "verify_passed", map[string]any{"decision": "accepted"})

	// Emit PathHint for CC PreToolUse / PostToolUse before status derivation —
	// path hints are independent of status and should still seed the SPA cache
	// even when DeriveStatus returns Valid=false. Skipped when sessionCode does
	// not resolve (e.g. unknown tmux session). cwd is read from CC's raw event
	// (every CC hook payload includes `cwd`); the cached SessionInfo.Cwd acts
	// as a fallback for older / non-conforming senders.
	if req.AgentType == "cc" && (req.PurdexName == "PdxPreToolUse" || req.PurdexName == "PdxPostToolUse") &&
		m.core != nil && m.pathHintDedup != nil && m.pathHintBuffer != nil {
		// Prefer the immutable tmux session ID (matches the broadcast path
		// in emitHookToSession). resolveSessionCode (name cache) would
		// otherwise leak the kill+recreate rename race onto path hints —
		// stale code → wrong SPA session receives the path hint.
		if code, _ := m.resolveSessionCodeFromHook(req); code != "" {
			cwdFallback := ""
			if m.sessions != nil {
				if info, err := m.sessions.GetSession(code); err == nil && info != nil {
					cwdFallback = info.Cwd
				}
			}
			EmitPathHint(m.core.Events, m.pathHintDedup, m.pathHintBuffer,
				req.RawEvent, req.PurdexName, "cc", code, cwdFallback, time.Now())
		}
	}

	broadcastTs := time.Now().UnixNano()

	// Delegation flag wiring — independent of PathHint extraction. Detect cc
	// subagent invoking codex-companion via Bash and mark / unmark Delegating
	// flag on the matching SubagentRef. Spec §3.2 + §3.3. PathHint and
	// delegation are emitted from the same raw cc PreToolUse / PostToolUse /
	// PostToolUseFailure event but are extracted independently — neither
	// depends on the other's dedup state or module fields (delegation does
	// not need m.core / m.pathHintDedup / m.pathHintBuffer; spec §3.2 / plan
	// §0.3 F6). This block is therefore a sibling of the PathHint block, not
	// nested inside its conditional.
	if req.AgentType == "cc" &&
		(req.PurdexName == "PdxPreToolUse" || req.PurdexName == "PdxPostToolUse" || req.PurdexName == "PdxPostToolUseFailure") {
		if hint, ok := ExtractDelegationHint(req.RawEvent, req.PurdexName); ok {
			if hint.IsCodexMark {
				if err := m.markDelegatingRef(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, hint.AgentID, hint.ToolUseID, broadcastTs); err != nil {
					if isDevMode() {
						log.Printf("[delegation] mark error: %v", err)
					}
					// fail-soft: do not abort handler (mirrors PathHint pattern)
				}
			} else if hint.IsUnmark {
				if err := m.unmarkDelegatingRef(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, hint.AgentID, hint.ToolUseID, broadcastTs); err != nil {
					if isDevMode() {
						log.Printf("[delegation] unmark error: %v", err)
					}
					// fail-soft
				}
			}
		}
	}

	// Find provider
	var provider agentpkg.AgentProvider
	if m.registry != nil {
		provider, _ = m.registry.Get(req.AgentType)
	}

	// Derive status via provider
	var result agentpkg.DeriveResult
	if provider != nil {
		result = provider.DeriveStatus(req.PurdexName, req.RawEvent)
	}

	// W2 metadata-driven lifecycle dispatch (spec §3.4.2): catalog hit on
	// PurdexName routes the request through the lifecycle branches; a catalog
	// miss returns LifecycleNone (the request will surface as
	// event_not_in_catalog at the result.Valid=false branch above).
	// classifyLifecycle also returns LifecycleNone for known no-op events
	// (PdxNotification, PdxPermissionRequest), which simply skips lifecycle
	// branches keyed on specific kinds.
	lifecycle := classifyLifecycle(provider, req)

	// Invalid result: provider returned Valid=false. Two sub-classes:
	//   - Reason=="" → truly unknown event name → "event_not_in_catalog"
	//   - Reason!="" → known event but payload not mappable → use that reason
	//     (e.g. "compact_ignored", "notification_unknown_type")
	//
	// Both branches:
	//   - record a verify-kind trace step (decision=skipped) with the chosen reason
	//   - clear any legacy agent_events row so replay/snapshot don't surface
	//     stale state on top of an unprocessed event (matches the cleanup the
	//     valid path performs at line ~163)
	//   - return 200 OK with the reason in the body
	//   - skip frame / projection / broadcast / activity-watch
	//
	// 200 (vs verify_rejected's 202) signals "received and acknowledged, no retry".
	// Hook CLI only retries on non-2xx.
	if !result.Valid {
		reason := result.Reason
		if reason == "" {
			reason = "event_not_in_catalog"
		}
		trace.Verify(req, "skipped", reason, nil)
		if isDevMode() {
			log.Printf("[derive] skipped agent=%s purdex_name=%s reason=%s chain_id=%s",
				req.AgentType, req.PurdexName, reason, trace.ChainID())
			log.Printf("[handler] invalid_skip reason=%s chain_id=%s",
				reason, trace.ChainID())
		}
		if req.TmuxSession != "" && m.events != nil {
			if err := m.events.Delete(req.TmuxSession); err != nil {
				log.Printf("[agent] clear legacy event on invalid result: %v", err)
			}
		}
		trace.Finish("completed", reason)
		traceFinished = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"reason": reason,
		})
		return
	}
	if isDevMode() {
		log.Printf("[derive] verify_passed agent=%s purdex_name=%s status=%s reason=%s chain_id=%s",
			req.AgentType, req.PurdexName, result.Status, result.Reason, trace.ChainID())
	}

	// Error guard: when in error state, only whitelisted events can clear it
	if result.Valid && result.Status != "" && result.Status != agentpkg.StatusError {
		m.mu.Lock()
		current := m.currentStatus[req.TmuxSession]
		m.mu.Unlock()
		if current == agentpkg.StatusError {
			canClear := lifecycle == agentpkg.LifecycleUserPromptSubmit || lifecycle == agentpkg.LifecycleSessionStart
			// SessionEnd carries StatusClear and unconditionally tears down
			// session state — it must always pass the error guard or the
			// session would stay stuck red after a StopFailure followed by a
			// real session shutdown.
			canClear = canClear || lifecycle == agentpkg.LifecycleSessionEnd
			if req.AgentType != "opencode" {
				canClear = canClear || lifecycle == agentpkg.LifecycleStop
			}
			if !canClear {
				normalized := buildProjectionNormalized(nil, req.AgentType, req.PurdexName, broadcastTs, result)
				trace.Emit(normalized, normalized.AgentType, normalized.RawEventName, "skipped", "error_guard_blocked")
				trace.Finish("completed", "emit_skipped")
				traceFinished = true
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
				return
			}
		}
	}

	// Detail-only no-frame-mutation guard for cc PreToolUse / PostToolUseFailure
	// (round-2 codex three-parallel adversarial review finding #1, PR #829).
	// These events were upgraded to Valid=true in commit b121eb26 to enable the
	// Delegating mark/unmark broadcast, but applyFrameEvent's
	// LifecycleNone+Status="" path falls back to StatusIdle and CREATES a new
	// frame when none exists (frame_ops.go:530-532, generic post-switch
	// create-new-frame block). A tool-precursor / tool-failure event must NOT
	// resurrect a torn-down frame.
	//
	// The delegation mutation block above (handler.go:230) is safe on its own:
	// it goes through GetByIdentity + UpsertIfUnchanged and is a silent no-op
	// when frame is missing (spec L7). The only remaining risk is the
	// downstream applyFrameEvent invitation; this short-circuit removes it.
	//
	// Behavior:
	//   - Frame already mutated by delegation block above (when applicable).
	//   - If frame exists for this session: rebuild projection, broadcast.
	//   - If frame missing: 200 ok, no broadcast, no mutation. PreToolUse /
	//     PostToolUseFailure must not resurrect.
	//
	// Compared to the LifecycleSubagentStart/Stop short-circuit at
	// handler.go:410-441, this branch is stricter: it bypasses applyFrameEvent
	// entirely (the SubagentStart/Stop path still calls applyFrameEvent for the
	// detail-only Subagents membership update; here there is no Subagents
	// mutation to perform).
	if req.AgentType == "cc" &&
		(req.PurdexName == "PdxPreToolUse" || req.PurdexName == "PdxPostToolUseFailure") {
		if req.TmuxSession == "" {
			trace.Finish("completed", "detail_only_no_session")
			traceFinished = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		projection, perr := m.projectionForSession(req.TmuxSession)
		if perr != nil {
			log.Printf("[handler] detail-only projection: %v", perr)
			trace.Finish("aborted", "projection_failed")
			traceFinished = true
			http.Error(w, `{"error":"projection failed"}`, http.StatusInternalServerError)
			return
		}
		if projection == nil || projection.TopFrame == nil {
			// No frame to broadcast; PreToolUse / PostToolUseFailure must
			// not resurrect a torn-down frame. The delegation mutation
			// block above already silent no-op'd (spec L7).
			trace.Finish("completed", "detail_only_no_frame")
			traceFinished = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		normalized := buildProjectionNormalized(projection, req.AgentType, req.PurdexName, broadcastTs, result)
		emitDecision, emitReason := m.emitHookToSession(req, normalized)
		trace.Emit(normalized, normalized.AgentType, normalized.RawEventName, emitDecision, emitReason)
		if isDevMode() {
			log.Printf("[broadcast] session=%s has_clients=%t decision=%s reason=%s raw_event_name=%s chain_id=%s detail_only=true",
				req.TmuxSession, m.hasSubscribers(), emitDecision, emitReason, normalized.RawEventName, trace.ChainID())
		}
		if emitDecision == "broadcasted" {
			trace.Finish("completed", "detail_only_broadcasted")
		} else {
			trace.Finish("completed", "detail_only_emit_skipped")
		}
		traceFinished = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	paneProjection, frameMeta, err := m.applyFrameEvent(req, result, broadcastTs)
	if err != nil {
		log.Printf("[agent] frame event: %v", err)
		trace.Finish("aborted", "frame_apply_failed")
		traceFinished = true
		http.Error(w, `{"error":"frame update failed"}`, http.StatusInternalServerError)
		return
	}
	trace.Frame(req, frameMeta)
	if isDevMode() {
		log.Printf("[handler] frame_apply session=%s frame_id=%s lifecycle=%s decision=%s chain_id=%s",
			req.TmuxSession, frameMeta.FrameID, req.PurdexName, frameMeta.Decision, trace.ChainID())
	}
	// L2: PreToolUse no-parent path is detail-only by design (spec §3.3.C.1
	// + row 20). applyFrameEvent already returned a skipped trace reason
	// without mutating Subagents or creating a frame. Without this short
	// circuit, the rest of the handler path would still rebuild projection,
	// delete legacy events, and broadcast a normalized event with empty
	// Status (which buildProjectionNormalized maps to StatusClear),
	// contradicting "no status broadcast" semantics. Round-1 codex review
	// finding (PR #801).
	if frameMeta.Decision == "skipped" && frameMeta.Reason == "pre_tool_without_proxy_parent" {
		trace.Finish("completed", "pre_tool_without_proxy_parent_skipped")
		traceFinished = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	projection := paneProjection
	if req.TmuxSession != "" {
		projection, err = m.projectionForSession(req.TmuxSession)
		if err != nil {
			log.Printf("[agent] session projection: %v", err)
			trace.Finish("aborted", "projection_failed")
			traceFinished = true
			http.Error(w, `{"error":"frame update failed"}`, http.StatusInternalServerError)
			return
		}
	}
	trace.Projection(req, summarizeProjectionChange(paneProjection, projection))
	if isDevMode() {
		var topStatus, paneID string
		var subagentCount int
		if projection != nil {
			paneID = projection.PaneID
			subagentCount = len(projection.Subagents)
			if projection.TopFrame != nil {
				topStatus = string(projection.TopFrame.Status)
			}
		}
		log.Printf("[handler] projection_built session=%s top_status=%s subagents=%d pane_id=%s chain_id=%s",
			req.TmuxSession, topStatus, subagentCount, paneID, trace.ChainID())
	}

	if req.TmuxSession != "" && m.frames != nil && m.events != nil {
		if err := m.events.Delete(req.TmuxSession); err != nil {
			log.Printf("[agent] clear legacy event: %v", err)
			trace.Finish("aborted", "legacy_delete_failed")
			traceFinished = true
			http.Error(w, `{"error":"store failed"}`, http.StatusInternalServerError)
			return
		}
	}

	// Handle subagent events (transient — broadcast only, don't persist)
	if lifecycle == agentpkg.LifecycleSubagentStart || lifecycle == agentpkg.LifecycleSubagentStop {
		if frameMeta.Decision != "updated_frame" {
			if isDevMode() {
				log.Printf("[handler] invalid_skip decision=%s reason=%s chain_id=%s",
					frameMeta.Decision, frameMeta.Reason, trace.ChainID())
			}
			trace.Finish("completed", "emit_skipped")
			traceFinished = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		m.mu.Lock()
		syncProjectionState(m.currentStatus, m.subagents, req.TmuxSession, projection)
		m.mu.Unlock()
		normalized := buildProjectionNormalized(projection, req.AgentType, req.PurdexName, broadcastTs, result)
		emitDecision, emitReason := m.emitHookToSession(req, normalized)
		trace.Emit(normalized, normalized.AgentType, normalized.RawEventName, emitDecision, emitReason)
		if isDevMode() {
			log.Printf("[broadcast] session=%s has_clients=%t decision=%s reason=%s raw_event_name=%s chain_id=%s",
				req.TmuxSession, m.hasSubscribers(), emitDecision, emitReason, normalized.RawEventName, trace.ChainID())
		}
		if emitDecision == "broadcasted" {
			trace.Finish("completed", "emit_broadcasted")
		} else {
			trace.Finish("completed", "emit_skipped")
		}
		traceFinished = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// recordHookAt opens the probeGraceWindow so any screen-change event
	// arriving in the next probeGraceWindow interval is suppressed — the
	// hook (this code path) is the authoritative status source. Recorded
	// once per accepted hook regardless of whether activity-watching is
	// (re)started below; the orchestrator owns watcher state.
	//
	// Codex finding #3 regression: recordHookAt MUST run BEFORE the
	// currentStatus write below. Otherwise an in-flight probe callback
	// already past its early stale-check could observe the new
	// currentStatus while lastHookAt is still empty, the graceWindow check
	// passes, and the probe overwrites authoritative hook status.
	if req.TmuxSession != "" && m.probeOrch != nil && result.Valid {
		m.probeOrch.recordHookAt(req.TmuxSession)
	}

	// Update in-memory state
	if result.Valid && result.Status != "" {
		m.mu.Lock()
		if result.Status == agentpkg.StatusClear {
			delete(m.currentStatus, req.TmuxSession)
			delete(m.subagents, req.TmuxSession)
		} else {
			m.currentStatus[req.TmuxSession] = result.Status
		}
		m.mu.Unlock()
	}

	// Activity watch management:
	// 1. Any hook event stops an active watcher for this session.
	// 2. waiting/running/idle transitions restart the watcher for the top frame.
	watchAgentType := req.AgentType
	watchStatus := result.Status
	if projection != nil && projection.TopFrame != nil {
		watchAgentType = projection.TopFrame.AgentType
		watchStatus = projection.TopFrame.Status
	}
	if req.TmuxSession != "" && m.prober != nil && result.Valid {
		m.manageActivityWatch(req.TmuxSession, watchAgentType, watchStatus)
	}

	// Clear subagents on non-compact SessionStart
	if lifecycle == agentpkg.LifecycleSessionStart && result.Valid {
		m.mu.Lock()
		delete(m.subagents, req.TmuxSession)
		m.mu.Unlock()
	}

	// Build and broadcast normalized event
	normalized := buildProjectionNormalized(projection, req.AgentType, req.PurdexName, broadcastTs, result)
	// Rebuild-record envelope (spec §4.3.1). applyFrameEvent grants it only
	// when the mutation outcome confirmed the sender kept its own top-level
	// frame; nil means no envelope. The outer normalized.AgentType keeps its
	// existing meaning (the session projection winner) — the two identities
	// coexist and never mix.
	attachProvenance(&normalized, frameMeta)
	m.mu.Lock()
	syncProjectionState(m.currentStatus, m.subagents, req.TmuxSession, projection)
	m.mu.Unlock()
	emitDecision, emitReason := m.emitHookToSession(req, normalized)
	trace.Emit(normalized, normalized.AgentType, normalized.RawEventName, emitDecision, emitReason)
	if isDevMode() {
		log.Printf("[broadcast] session=%s has_clients=%t decision=%s reason=%s raw_event_name=%s chain_id=%s",
			req.TmuxSession, m.hasSubscribers(), emitDecision, emitReason, normalized.RawEventName, trace.ChainID())
	}
	if emitDecision == "broadcasted" {
		trace.Finish("completed", "emit_broadcasted")
	} else {
		trace.Finish("completed", "emit_skipped")
	}
	traceFinished = true

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// hasSubscribers reports whether the events broadcaster has any connected
// clients. Returns false when m.core or m.core.Events is nil so dev-mode
// callers can format the field unconditionally without panicking when the
// module has no event plane wired (test setup, daemon-less unit tests).
func (m *Module) hasSubscribers() bool {
	if m == nil || m.core == nil || m.core.Events == nil {
		return false
	}
	return m.core.Events.HasSubscribers()
}

// buildNormalized creates a NormalizedEvent from the derive result and current state.
func (m *Module) buildNormalized(tmuxSession, eventName, agentType string, broadcastTs int64, result agentpkg.DeriveResult) agentpkg.NormalizedEvent {
	m.mu.Lock()
	subs := make([]agentpkg.SubagentRef, len(m.subagents[tmuxSession]))
	copy(subs, m.subagents[tmuxSession])
	m.mu.Unlock()

	normalized := agentpkg.NormalizedEvent{
		AgentType:    agentType,
		Status:       string(result.Status),
		Model:        result.Model,
		Subagents:    subs,
		RawEventName: eventName,
		BroadcastTs:  broadcastTs,
		Detail:       result.Detail,
	}
	return normalized
}

// broadcastToSession resolves the tmux session name to a session code and
// broadcasts. Used by the probe orchestrator (no hook payload available, so
// no tmux_session_id) — falls through to the name-based resolveSessionCode
// path, which is still cache-backed. Hook callsites should use
// emitHookToSession instead so the immutable ID path engages.
func (m *Module) broadcastToSession(tmuxSession string, normalized agentpkg.NormalizedEvent) {
	if m.core == nil {
		return
	}
	code := m.resolveSessionCode(tmuxSession)
	if code == "" {
		return
	}
	m.emitNormalizedToCode(code, normalized)
}

// emitHookToSession routes a hook-derived normalized event to its WS code.
// Prefers req.TmuxSessionID (immutable, pure-function resolution) over
// req.TmuxSession (cache-backed, racy across kill+recreate). Returns the
// (decision, reason) tuple the trace pipeline annotates onto the chain;
// the reason value carries the resolution path label so operators can grep
// daemon logs and confirm hook clients have migrated to the ID payload.
func (m *Module) emitHookToSession(req EventRequest, normalized agentpkg.NormalizedEvent) (string, string) {
	if m.core == nil {
		return "skipped", "core_unavailable"
	}
	code, path := m.resolveSessionCodeFromHook(req)
	if code == "" {
		return "skipped", "session_code_missing"
	}
	m.emitNormalizedToCode(code, normalized)
	return "broadcasted", string(path)
}

// emitNormalizedToCode is the shared bottom half of both broadcast paths:
// marshals the normalized event JSON and pushes it onto the events bus.
// Callers MUST resolve the session code first; this helper makes no
// assumptions about how it was obtained.
func (m *Module) emitNormalizedToCode(code string, normalized agentpkg.NormalizedEvent) {
	payload, _ := json.Marshal(normalized)
	m.core.Events.Broadcast(code, "hook", string(payload))
}

// hookSessionCodePath labels which resolution branch produced the session
// code for a hook event. It is surfaced through emitHookToSession's reason
// return so the trace pipeline / broadcast log lets operators tell during
// rollout whether hook clients have migrated to the new tmux_session_id
// payload (fast path) vs still using the legacy name path (cache lookup).
type hookSessionCodePath string

const (
	// hookCodePathID — TmuxSessionID present and valid; the immutable ID
	// path resolved the code via a pure EncodeSessionID call. Migrated
	// hook clients hit this branch.
	hookCodePathID hookSessionCodePath = "id_path"
	// hookCodePathIDEmpty — TmuxSessionID missing; backward-compat
	// fallback to the name-cache path. Indicates the hook client is an
	// older pdx binary not yet updated; an operational signal that
	// rollout is incomplete.
	hookCodePathIDEmpty hookSessionCodePath = "id_empty"
	// hookCodePathMalformedID — TmuxSessionID present but rejected by
	// EncodeSessionID (corrupt payload / bug). Falls back to the name
	// path; the inline error log captures the offending value.
	hookCodePathMalformedID hookSessionCodePath = "malformed_id"
)

// resolveSessionCodeFromHook prefers the immutable tmux session ID from the
// hook payload (eliminates the name-reuse race window) and falls back to
// the cached name-path resolution when the ID is missing (older pdx hook
// binary) or malformed (unexpected payload corruption). Falling back rather
// than dropping keeps a partial-rollout daemon-vs-hook version skew safe.
//
// The returned hookSessionCodePath labels the branch that produced the
// code; see the constants for semantics. Callers that don't log the
// reason can ignore the second return.
func (m *Module) resolveSessionCodeFromHook(req EventRequest) (string, hookSessionCodePath) {
	if req.TmuxSessionID == "" {
		return m.resolveSessionCode(req.TmuxSession), hookCodePathIDEmpty
	}
	code, err := session.EncodeSessionID(req.TmuxSessionID)
	if err != nil {
		log.Printf("[agent] invalid tmux_session_id %q: %v (falling back to name path)", req.TmuxSessionID, err)
		return m.resolveSessionCode(req.TmuxSession), hookCodePathMalformedID
	}
	return code, hookCodePathID
}

// sessionCodeLookuper is the optional fast-path interface a SessionProvider
// can implement to avoid the 1+7×S tmux subprocess fan-out of ListSessions on
// every hook event. The production *session.SessionModule satisfies this
// implicitly via its 1s TTL name→code cache (see internal/module/session/
// lookup.go). Kept unexported here because it's a hot-path optimization, not
// a public contract.
type sessionCodeLookuper interface {
	LookupCodeByName(name string) (string, bool)
}

// resolveSessionCode maps a tmux session name to the pdx session code. Tries
// the cached fast path first; falls through to ListSessions on a cache miss
// so a hook fired during a rename/create race window before cache refresh
// still resolves correctly (safety net per SOT §3.1).
func (m *Module) resolveSessionCode(tmuxName string) string {
	if m.sessions == nil {
		return ""
	}
	if lookuper, ok := m.sessions.(sessionCodeLookuper); ok {
		if code, found := lookuper.LookupCodeByName(tmuxName); found {
			return code
		}
	}
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		log.Printf("[agent] list sessions: %v", err)
		return ""
	}
	for _, s := range sessions {
		if s.Name == tmuxName {
			return s.Code
		}
	}
	return ""
}

// handleHookStatus handles GET /api/hooks/{agent}/status.
func (m *Module) handleHookStatus(w http.ResponseWriter, r *http.Request) {
	agentType := r.PathValue("agent")
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent type"}`, http.StatusNotFound)
		return
	}
	installer, ok := provider.(agentpkg.HookInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support hooks"}`, http.StatusNotFound)
		return
	}
	status, err := installer.CheckHooks()
	if err != nil {
		http.Error(w, `{"error":"check failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleHookSetup handles POST /api/hooks/{agent}/setup.
func (m *Module) handleHookSetup(w http.ResponseWriter, r *http.Request) {
	agentType := r.PathValue("agent")
	provider, ok := m.registry.Get(agentType)
	if !ok {
		http.Error(w, `{"error":"unknown agent type"}`, http.StatusNotFound)
		return
	}
	installer, ok := provider.(agentpkg.HookInstaller)
	if !ok {
		http.Error(w, `{"error":"agent does not support hooks"}`, http.StatusNotFound)
		return
	}

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	pdxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find pdx binary"}`, http.StatusInternalServerError)
		return
	}
	pdxPath, _ = filepath.EvalSymlinks(pdxPath)

	switch req.Action {
	case "install":
		if err := installer.InstallHooks(pdxPath); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "setup failed", "detail": err.Error()})
			return
		}
	case "remove":
		if err := installer.RemoveHooks(pdxPath); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": "remove failed", "detail": err.Error()})
			return
		}
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	// Return updated status
	m.handleHookStatus(w, r)
}

// handleStatuslineStatus handles GET /api/agent/{agent}/statusline/status.
// Currently only "cc" is supported; other agent types return 404.
func (m *Module) handleStatuslineStatus(w http.ResponseWriter, r *http.Request) {
	installer, ok := m.resolveStatuslineInstaller(w, r)
	if !ok {
		return
	}
	state, err := installer.CheckStatusline()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleStatuslineSetup handles POST /api/agent/{agent}/statusline/setup.
// Action "install" with mode "pdx" installs the pdx-native statusLine;
// mode "wrap" installs pdx as a wrapper around the given inner command.
// Action "remove" removes a pdx-managed statusLine (unmanaged entries are
// refused with 409 Conflict).
func (m *Module) handleStatuslineSetup(w http.ResponseWriter, r *http.Request) {
	installer, ok := m.resolveStatuslineInstaller(w, r)
	if !ok {
		return
	}

	var req struct {
		Action string `json:"action"`
		Mode   string `json:"mode"`
		Inner  string `json:"inner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	pdxPath, err := os.Executable()
	if err != nil {
		http.Error(w, `{"error":"cannot find pdx binary"}`, http.StatusInternalServerError)
		return
	}
	pdxPath, _ = filepath.EvalSymlinks(pdxPath)

	// Acquire mutex only for the mutation phase. The status reply at the end
	// is a read-only CheckStatusline() call plus HTTP write; keeping it
	// outside the lock means install/remove don't block subsequent status
	// polls, and avoids holding the mutex across HTTP response writes.
	statuslineMutex.Lock()
	var (
		opErr       error
		badRequest  string
		conflictErr error
	)
	switch req.Action {
	case "install":
		switch req.Mode {
		case "pdx":
			opErr = installer.InstallStatuslinePdx(pdxPath)
		case "wrap":
			if req.Inner == "" {
				badRequest = `{"error":"wrap requires inner"}`
			} else {
				opErr = installer.InstallStatuslineWrap(pdxPath, req.Inner)
			}
		default:
			badRequest = `{"error":"mode must be pdx or wrap"}`
		}
	case "remove":
		opErr = installer.RemoveStatusline()
		if opErr != nil && strings.Contains(opErr.Error(), "refusing to remove unmanaged") {
			conflictErr = opErr
			opErr = nil
		} else if opErr == nil {
			// On successful remove: wipe cached snapshots and broadcast a
			// cleared event so the SPA can drop stale statusline state.
			// Global clear is intentional for single-host daemon (simplest-
			// possible approach); the empty session code is the existing
			// codebase convention for cross-session events (see watcher.go
			// sessions/tmux broadcasts).
			m.snapshotMu.Lock()
			m.statusSnapshots = make(map[string]statusSnapshot)
			m.snapshotMu.Unlock()
			if m.core != nil {
				m.core.Events.Broadcast("", "agent.status.cleared", `{"agent_type":"cc"}`)
			}
		}
	default:
		badRequest = `{"error":"action must be install or remove"}`
	}
	statuslineMutex.Unlock()

	switch {
	case badRequest != "":
		http.Error(w, badRequest, http.StatusBadRequest)
		return
	case conflictErr != nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": conflictErr.Error()})
		return
	case opErr != nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": opErr.Error()})
		return
	}

	// Return updated status (mutex released; CheckStatusline is a pure read).
	m.handleStatuslineStatus(w, r)
}

func (m *Module) handleTitleStatus(w http.ResponseWriter, r *http.Request) {
	state := m.titleStatus()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (m *Module) handleTitleSetup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	path, err := tmuxConfigPath()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(titleStatusResponse{Error: err.Error()})
		return
	}

	switch req.Action {
	case "install":
		if err := installTitleMarker(path); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(titleStatusResponse{ManagedConfigPath: path, Error: err.Error()})
			return
		}
		if m.tmux != nil {
			if err := m.tmux.SetWindowOptionGlobal("allow-set-title", "on"); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				state := m.titleStatus()
				state.Error = err.Error()
				_ = json.NewEncoder(w).Encode(state)
				return
			}
		}
	case "remove":
		if err := removeTitleMarker(path); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(titleStatusResponse{ManagedConfigPath: path, Error: err.Error()})
			return
		}
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	m.handleTitleStatus(w, r)
}

func (m *Module) titleStatus() titleStatusResponse {
	path, err := tmuxConfigPath()
	if err != nil {
		return titleStatusResponse{Error: err.Error()}
	}
	state := titleStatusResponse{ManagedConfigPath: path}
	data, err := os.ReadFile(path)
	if err == nil {
		if hasMalformedTitleMarker(data) {
			state.Error = "malformed purdex agent-title marker block"
		} else {
			state.Installed = bytes.Contains(data, []byte(titleMarkerStart)) && bytes.Contains(data, []byte(titleMarkerEnd))
			state.AllowSetTitle = state.Installed && bytes.Contains(data, []byte(titleMarkerLine))
		}
	} else if !os.IsNotExist(err) {
		state.Error = err.Error()
	}
	if m.tmux != nil {
		value, err := m.tmux.ShowWindowOption("allow-set-title")
		if err != nil {
			state.Error = err.Error()
		} else {
			state.RuntimeApplied = strings.TrimSpace(value) == "on"
		}
	}
	return state
}

func tmuxConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tmux.conf"), nil
}

func installTitleMarker(path string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if hasMalformedTitleMarker(data) {
		return errors.New("malformed purdex agent-title marker block")
	}
	clean := removeTitleMarkerBytes(data)
	block := []byte(titleMarkerStart + "\n" + titleMarkerLine + "\n" + titleMarkerEnd + "\n")
	if len(clean) > 0 && !bytes.HasSuffix(clean, []byte("\n")) {
		clean = append(clean, '\n')
	}
	clean = append(clean, block...)
	return os.WriteFile(path, clean, 0644)
}

func removeTitleMarker(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if hasMalformedTitleMarker(data) {
		return errors.New("malformed purdex agent-title marker block")
	}
	return os.WriteFile(path, removeTitleMarkerBytes(data), 0644)
}

func removeTitleMarkerBytes(data []byte) []byte {
	text := string(data)
	for {
		start := strings.Index(text, titleMarkerStart)
		if start == -1 {
			return []byte(text)
		}
		end := strings.Index(text[start:], titleMarkerEnd)
		if end == -1 {
			return []byte(text)
		}
		end += start + len(titleMarkerEnd)
		if end < len(text) && text[end] == '\r' {
			end++
		}
		if end < len(text) && text[end] == '\n' {
			end++
		}
		text = text[:start] + text[end:]
	}
}

func hasMalformedTitleMarker(data []byte) bool {
	text := string(data)
	start := strings.Index(text, titleMarkerStart)
	for start != -1 {
		end := strings.Index(text[start:], titleMarkerEnd)
		if end == -1 {
			return true
		}
		between := text[start+len(titleMarkerStart) : start+end]
		if strings.Contains(between, titleMarkerStart) {
			return true
		}
		nextOffset := start + end + len(titleMarkerEnd)
		remaining := text[nextOffset:]
		next := strings.Index(remaining, titleMarkerStart)
		if next == -1 {
			return false
		}
		start = nextOffset + next
	}
	return false
}

func titleCapabilities() map[string]titleCapability {
	return map[string]titleCapability{
		"cc":       claudeTitleCapability(),
		"codex":    codexTitleCapability(),
		"opencode": {State: "unknown", Note: "OpenCode has no documented persistent title toggle."},
	}
}

func claudeTitleCapability() titleCapability {
	if getenvFn("CLAUDE_CODE_DISABLE_TERMINAL_TITLE") == "1" {
		return titleCapability{State: "disabled", Note: "CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1 disables Claude terminal titles for daemon-launched sessions."}
	}
	return titleCapability{State: "enabled", Note: "Claude terminal titles are likely enabled; session-local environment overrides may differ."}
}

func codexTitleCapability() titleCapability {
	home, err := os.UserHomeDir()
	if err != nil {
		return titleCapability{State: "unknown", Note: "Codex terminal title config could not be checked."}
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if os.IsNotExist(err) {
		return titleCapability{State: "missing", Note: "Codex terminal title uses its default behavior; no config file was found."}
	}
	if err != nil {
		return titleCapability{State: "unknown", Note: "Codex terminal title config could not be read."}
	}
	return parseCodexTitleCapability(string(data))
}

func parseCodexTitleCapability(config string) titleCapability {
	idx := strings.Index(config, "terminal_title")
	if idx == -1 {
		return titleCapability{State: "missing", Note: "Codex terminal title uses its default behavior; terminal_title is not configured."}
	}
	line := strings.TrimSpace(strings.SplitN(config[idx:], "\n", 2)[0])
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return titleCapability{State: "unknown", Note: "Codex terminal title config could not be parsed."}
	}
	value := strings.TrimSpace(parts[1])
	if value == "[]" {
		return titleCapability{State: "disabled", Note: "Codex terminal title is disabled with terminal_title = []."}
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return titleCapability{State: "configured", Note: "Codex terminal title is configured in ~/.codex/config.toml."}
	}
	return titleCapability{State: "unknown", Note: "Codex terminal title config could not be parsed."}
}

// handleHistory handles GET /api/sessions/{code}/history.
func (m *Module) handleHistory(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if m.sessions == nil {
		http.Error(w, `{"error":"no session provider"}`, http.StatusInternalServerError)
		return
	}
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		http.Error(w, `{"error":"list sessions"}`, http.StatusInternalServerError)
		return
	}
	var sess *session.SessionInfo
	for _, s := range sessions {
		if s.Code == code {
			sess = &s
			break
		}
	}
	if sess == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	agentType := ""
	if projection, err := m.projectionForSession(sess.Name); err == nil && projection != nil && projection.TopFrame != nil {
		agentType = projection.TopFrame.AgentType
	}
	if agentType == "" && m.events != nil {
		ev, _ := m.events.Get(sess.Name)
		if ev != nil {
			agentType = ev.AgentType
		}
	}
	if agentType == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	provider, ok := m.registry.Get(agentType)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	histProvider, ok := provider.(agentpkg.HistoryProvider)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	history, err := histProvider.GetHistory(sess.Cwd, sess.CCSessionID)
	if err != nil {
		log.Printf("[agent] history: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// statusSnapshot is the in-memory shape cached per sessionCode and broadcast over WS.
// It is intentionally display-only and not persisted (high-frequency, agent-owned).
// Lives as a Module field (m.statusSnapshots) guarded by m.snapshotMu.
type statusSnapshot struct {
	AgentType string          `json:"agent_type"`
	Status    json.RawMessage `json:"status"`
}

// handleAgentStatus handles POST /api/agent/status.
// Receives statusline payloads from `pdx statusline-proxy` and broadcasts
// agent.status WS events to subscribers.
func (m *Module) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TmuxSession string          `json:"tmux_session"`
		AgentType   string          `json:"agent_type"`
		RawStatus   json.RawMessage `json:"raw_status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if payload.AgentType != "cc" {
		http.Error(w, `{"error":"unsupported agent_type"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))

	// Test-nonce path: only treat the request as self-test traffic when there is
	// an active observer for that nonce. This prevents legitimate sessions whose
	// names happen to start with the prefix from silently losing production
	// status updates.
	if strings.HasPrefix(payload.TmuxSession, testNoncePrefix) && m.hasTestObserver(payload.TmuxSession) {
		m.signalTestStage(payload.TmuxSession, testStageReceived)
		if m.core != nil {
			snap := statusSnapshot{AgentType: payload.AgentType, Status: payload.RawStatus}
			body, _ := json.Marshal(snap)
			m.core.Events.Broadcast(payload.TmuxSession, "agent.status", string(body))
		}
		m.signalTestStage(payload.TmuxSession, testStageBroadcast)
		return
	}

	code := m.resolveSessionCode(payload.TmuxSession)
	if code == "" {
		return
	}

	snap := statusSnapshot{AgentType: payload.AgentType, Status: payload.RawStatus}
	m.snapshotMu.Lock()
	m.statusSnapshots[code] = snap
	m.snapshotMu.Unlock()

	if m.core != nil {
		body, _ := json.Marshal(snap)
		m.core.Events.Broadcast(code, "agent.status", string(body))
	}
}

// sendStatuslineSnapshot pushes the cached statusline snapshots to a new
// WebSocket subscriber. Marshals under RLock, then releases the lock
// before calling sub.Send — a slow subscriber (full channel) would
// otherwise block every concurrent agent.status writer through snapshotMu.
func (m *Module) sendStatuslineSnapshot(sub *core.EventSubscriber) {
	if m.core == nil {
		return
	}
	m.snapshotMu.RLock()
	pending := make([][]byte, 0, len(m.statusSnapshots))
	for code, snap := range m.statusSnapshots {
		body, err := json.Marshal(snap)
		if err != nil {
			continue
		}
		event := core.HostEvent{Type: "agent.status", Session: code, Value: string(body)}
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		pending = append(pending, data)
	}
	m.snapshotMu.RUnlock()
	for _, data := range pending {
		sub.Send(data)
	}
}

// handleDetect handles GET /api/agents/detect.
// Checks if agent CLIs (claude, codex) are available on the host.
func (m *Module) handleDetect(w http.ResponseWriter, r *http.Request) {
	type agentInfo struct {
		Installed    bool            `json:"installed"`
		Path         string          `json:"path,omitempty"`
		Version      string          `json:"version,omitempty"`
		DynamicTitle titleCapability `json:"dynamic_title"`
	}

	detect := func(cmd string, versionArgs ...string) agentInfo {
		path, err := exec.LookPath(cmd)
		if err != nil {
			return agentInfo{}
		}
		info := agentInfo{Installed: true, Path: path}
		if len(versionArgs) > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, path, versionArgs...).Output()
			if err == nil {
				info.Version = strings.TrimSpace(string(out))
			}
		}
		return info
	}

	capabilities := titleCapabilities()
	result := map[string]agentInfo{
		"cc":       detect("claude", "--version"),
		"codex":    detect("codex", "--version"),
		"opencode": detect("opencode", "--version"),
	}
	for agentType, info := range result {
		info.DynamicTitle = capabilities[agentType]
		result[agentType] = info
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
