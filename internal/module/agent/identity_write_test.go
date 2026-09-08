package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// fakeIdentifyingAgentProvider is fakeAgentProvider plus the optional
// SessionIdentifier capability. It exists as its own fixture on purpose:
// fakeAgentProvider deliberately does NOT implement SessionIdentifier, so
// every pre-existing frame test (provenance_test.go included) keeps its
// current no-identity-write behaviour and cannot vouch for this wiring.
type fakeIdentifyingAgentProvider struct {
	fakeAgentProvider
}

func (f *fakeIdentifyingAgentProvider) IdentifyEvent(_ string, raw json.RawMessage) (string, string) {
	return agentpkg.ExtractSessionIdentity(raw)
}

// newIdentityTestModule mirrors newProxyTestModule but registers providers
// that implement SessionIdentifier.
func newIdentityTestModule(t *testing.T) *Module {
	t.Helper()
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "work-code", Name: "work"}}}
	for _, typeName := range []string{"cc", "codex"} {
		p := &fakeIdentifyingAgentProvider{}
		p.typeName = typeName
		p.derive = func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		}
		m.registry.Register(p)
	}
	return m
}

// identityPayload builds a raw hook payload carrying only the fields the
// extractor reads. An empty string omits the key entirely, which is how a
// real payload that carries neither field looks.
func identityPayload(sessionID, cwd string) json.RawMessage {
	obj := map[string]any{"hook_event_name": "Stop"}
	if sessionID != "" {
		obj["session_id"] = sessionID
	}
	if cwd != "" {
		obj["cwd"] = cwd
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return raw
}

func loadFrame(t *testing.T, m *Module, paneID string, pid int, startTime string) store.Frame {
	t.Helper()
	got, err := m.frames.GetByIdentity(paneID, pid, startTime)
	if err != nil {
		t.Fatalf("GetByIdentity(%s,%d,%s): %v", paneID, pid, startTime, err)
	}
	if got == nil {
		t.Fatalf("GetByIdentity(%s,%d,%s) = nil, want a frame", paneID, pid, startTime)
	}
	return *got
}

func assertIdentity(t *testing.T, frame store.Frame, wantSession, wantCwd string) {
	t.Helper()
	if frame.SessionID != wantSession || frame.Cwd != wantCwd {
		t.Fatalf("identity = (%q, %q), want (%q, %q)", frame.SessionID, frame.Cwd, wantSession, wantCwd)
	}
}

// ordinaryEventReq is a non-SessionStart hook event from a process that owns
// its own frame — the path a pre-deploy session's frame takes to acquire an
// identity at all (spec §5.2: UpdateHookPath, not Upsert).
func ordinaryEventReq(paneID string, pid int, startTime string, raw json.RawMessage) (EventRequest, agentpkg.DeriveResult) {
	return EventRequest{
			TmuxSession:     "work",
			TmuxPaneID:      paneID,
			PurdexName:      "PdxStop",
			AgentType:       "cc",
			SenderPID:       pid,
			SenderStartTime: startTime,
			RawEvent:        raw,
		}, agentpkg.DeriveResult{
			Valid:  true,
			Status: agentpkg.StatusIdle,
		}
}

// --- own-frame return #4: the general created_frame / updated_frame return ---

func TestSessionIdentity_OrdinaryEventWritesIdentity(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	req, result := ordinaryEventReq("%5", 100, "t100", identityPayload("sess-A", "/w/a"))
	if _, _, err := m.applyFrameEvent(req, result, 100); err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-A", "/w/a")
}

func TestSessionIdentity_LaterEventWithoutIdentityKeepsIt(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	req, result := ordinaryEventReq("%5", 100, "t100", identityPayload("sess-A", "/w/a"))
	if _, _, err := m.applyFrameEvent(req, result, 100); err != nil {
		t.Fatalf("applyFrameEvent #1: %v", err)
	}
	req2, result2 := ordinaryEventReq("%5", 100, "t100", identityPayload("", ""))
	if _, _, err := m.applyFrameEvent(req2, result2, 200); err != nil {
		t.Fatalf("applyFrameEvent #2: %v", err)
	}

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-A", "/w/a")
}

// OpenCode switches back to an existing session inside one process without
// emitting a SessionStart (spec §3.3), so a later ordinary event carrying a
// different id must replace the stored one — no lifecycle gate.
func TestSessionIdentity_DifferentIDOnLaterEventReplacesIt(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	req, result := ordinaryEventReq("%5", 100, "t100", identityPayload("sess-A", "/w/a"))
	if _, _, err := m.applyFrameEvent(req, result, 100); err != nil {
		t.Fatalf("applyFrameEvent #1: %v", err)
	}
	req2, result2 := ordinaryEventReq("%5", 100, "t100", identityPayload("sess-B", ""))
	if _, _, err := m.applyFrameEvent(req2, result2, 200); err != nil {
		t.Fatalf("applyFrameEvent #2: %v", err)
	}

	// cwd is untouched: only non-empty values are written.
	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-B", "/w/a")
}

func TestSessionIdentity_CwdArrivingLaterFillsIn(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	req, result := ordinaryEventReq("%5", 100, "t100", identityPayload("sess-A", ""))
	if _, _, err := m.applyFrameEvent(req, result, 100); err != nil {
		t.Fatalf("applyFrameEvent #1: %v", err)
	}
	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-A", "")

	req2, result2 := ordinaryEventReq("%5", 100, "t100", identityPayload("sess-A", "/w/a"))
	if _, _, err := m.applyFrameEvent(req2, result2, 200); err != nil {
		t.Fatalf("applyFrameEvent #2: %v", err)
	}

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-A", "/w/a")
}

// A SessionStart whose payload carries source == "compact" is rejected
// upstream by deriveCCStatus; at this boundary the rejection shows up as
// result.Valid == false, which returns before any frame mutation. Nothing
// may be written, or a /compact would overwrite the real session id.
func TestSessionIdentity_DeriveInvalidWritesNothing(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	req, result := ordinaryEventReq("%5", 100, "t100", identityPayload("sess-A", "/w/a"))
	if _, _, err := m.applyFrameEvent(req, result, 100); err != nil {
		t.Fatalf("applyFrameEvent #1: %v", err)
	}

	compact := EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		PurdexName:      "PdxSessionStart",
		AgentType:       "cc",
		SenderPID:       100,
		SenderStartTime: "t100",
		RawEvent:        json.RawMessage(`{"session_id":"sess-COMPACT","cwd":"/w/compact","source":"compact"}`),
	}
	_, meta, err := m.applyFrameEvent(compact, agentpkg.DeriveResult{Valid: false}, 200)
	if err != nil {
		t.Fatalf("applyFrameEvent compact: %v", err)
	}
	if meta.Reason != "derive_invalid" {
		t.Fatalf("Reason = %q, want derive_invalid", meta.Reason)
	}

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-A", "/w/a")
}

// --- own-frame return #1: subagent_id_missing ---

// A SubagentStart/Stop whose payload has no agent_id returns the sender's own
// frame as "skipped". It is still a real own-frame event carrying session_id,
// so the identity is written — and the membership list must not move.
func TestSessionIdentity_SubagentWithoutAgentIDWritesIdentityAndKeepsMembership(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrameWithSubagents(t, m, "%5", "cc", 100, "t100", 10, []agentpkg.SubagentRef{
		nativeSubagentRef("existing-sub", 5),
	})

	req := EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		PurdexName:      "PdxSubagentStart",
		AgentType:       "cc",
		SenderPID:       100,
		SenderStartTime: "t100",
		RawEvent:        identityPayload("sess-NOID", "/w/noid"),
	}
	// No agent_id in Detail — the payload shape that reaches the
	// subagent_id_missing return.
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Detail: map[string]any{}}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "subagent_id_missing" {
		t.Fatalf("Reason = %q, want subagent_id_missing (meta=%+v)", meta.Reason, meta)
	}

	got := loadFrame(t, m, "%5", 100, "t100")
	assertIdentity(t, got, "sess-NOID", "/w/noid")
	if len(got.Subagents) != 1 || got.Subagents[0].ID != "existing-sub" {
		t.Fatalf("Subagents = %+v, want unchanged [existing-sub]", got.Subagents)
	}
}

// --- own-frame return #2: native subagent membership change ---

func TestSessionIdentity_NativeSubagentStartWritesIdentity(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	req := EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		PurdexName:      "PdxSubagentStart",
		AgentType:       "cc",
		SenderPID:       100,
		SenderStartTime: "t100",
		RawEvent:        identityPayload("sess-SUB", "/w/sub"),
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{
		Valid:  true,
		Detail: map[string]any{"agent_id": "sub-1"},
	}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "subagent_membership_changed" {
		t.Fatalf("Reason = %q, want subagent_membership_changed (meta=%+v)", meta.Reason, meta)
	}

	got := loadFrame(t, m, "%5", 100, "t100")
	assertIdentity(t, got, "sess-SUB", "/w/sub")
	if len(got.Subagents) != 1 || got.Subagents[0].ID != "sub-1" {
		t.Fatalf("Subagents = %+v, want [sub-1]", got.Subagents)
	}
}

// --- own-frame return #3: native_subagent_detached_on_stop_failure ---

func TestSessionIdentity_StopFailureNativeDetachWritesIdentity(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrameWithSubagents(t, m, "%5", "cc", 100, "t100", 10, []agentpkg.SubagentRef{
		nativeSubagentRef("match-id", 5),
	})

	raw, err := json.Marshal(map[string]any{
		"hook_event_name": "StopFailure",
		"agent_id":        "match-id",
		"error":           "rate_limit",
		"session_id":      "sess-FAIL",
		"cwd":             "/w/fail",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		PurdexName:      "PdxStopFailure",
		AgentType:       "cc",
		SenderPID:       100,
		SenderStartTime: "t100",
		RawEvent:        raw,
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{
		Valid:  true,
		Status: agentpkg.StatusError,
		Detail: map[string]any{"agent_id": "match-id"},
	}, 200)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "native_subagent_detached_on_stop_failure" {
		t.Fatalf("Reason = %q, want native_subagent_detached_on_stop_failure (meta=%+v)", meta.Reason, meta)
	}

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-FAIL", "/w/fail")
}

// --- the parent-frame returns: never write ---

// installProxyProcessTree wires the process fns so that PID 200 is a child of
// the alive cc frame at PID 100, the shape the proxy fast-path collapses.
func installProxyProcessTree(t *testing.T) {
	t.Helper()
	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "other", nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})
}

// The pre-Upsert proxy fast path hands back the PARENT's frame. Writing the
// child codex session id onto the cc parent's row would be a real
// mis-attribution.
func TestSessionIdentity_PreUpsertProxyAttachDoesNotWriteToParent(t *testing.T) {
	m := newIdentityTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	installProxyProcessTree(t)

	req := EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		PurdexName:      "PdxSessionStart",
		AgentType:       "codex",
		SenderPID:       200,
		SenderStartTime: "t200",
		RawEvent:        identityPayload("codex-sess", "/w/codex"),
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("Reason = %q, want proxy_subagent_attached (meta=%+v)", meta.Reason, meta)
	}
	if meta.FrameID != parent.FrameID {
		t.Fatalf("FrameID = %q, want parent %q", meta.FrameID, parent.FrameID)
	}

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "", "")
}

// The post-Upsert reconcileCreatedFrameAsProxy canonicalization is different
// code from the fast path above and also returns the parent's frame; one case
// cannot stand for both.
func TestSessionIdentity_PostUpsertCanonicalizationDoesNotWriteToParent(t *testing.T) {
	m := newIdentityTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	// PID 200's first two process reads (the pre-walk and the line-~610
	// parent lookup) report an unknown PPID so the fast path misses and a
	// standalone frame is created; the third (inside the post-Upsert
	// reconcile) reports the cc parent so the frame is folded.
	calls := 0
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			calls++
			if calls <= 2 {
				return agentpkg.ProcessInfo{PID: 200, PPID: 999}, nil
			}
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "other", nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})

	req := EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		PurdexName:      "PdxSessionStart",
		AgentType:       "codex",
		SenderPID:       200,
		SenderStartTime: "t200",
		RawEvent:        identityPayload("codex-sess", "/w/codex"),
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "post_upsert_canonicalization_self" {
		t.Fatalf("Reason = %q, want post_upsert_canonicalization_self (meta=%+v)", meta.Reason, meta)
	}
	if meta.FrameID != parent.FrameID {
		t.Fatalf("FrameID = %q, want parent %q", meta.FrameID, parent.FrameID)
	}

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "", "")
}

// The codex broker UserPromptSubmit upsert also returns the parent's frame.
func TestSessionIdentity_CodexBrokerParentUpsertDoesNotWriteToParent(t *testing.T) {
	m := newIdentityTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	installProxyProcessTree(t)

	req := EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		PurdexName:      "PdxUserPromptSubmit",
		AgentType:       "codex",
		SenderPID:       200,
		SenderStartTime: "t200",
		RawEvent:        identityPayload("codex-sess", "/w/codex"),
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "proxy_subagent_attached_on_user_prompt" {
		t.Fatalf("Reason = %q, want proxy_subagent_attached_on_user_prompt (meta=%+v)", meta.Reason, meta)
	}
	if meta.FrameID != parent.FrameID {
		t.Fatalf("FrameID = %q, want parent %q", meta.FrameID, parent.FrameID)
	}

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "", "")
}

// --- interleaving ---

// An identity write that lands between a proxy attach's read of the parent
// and its UpsertIfUnchanged must leave both intact: the identity is its own
// two-column UPDATE that never touches last_seen_at, so it neither trips the
// CAS nor gets clobbered by the whole-row write (spec §5.2).
func TestSessionIdentity_WriteBetweenProxyAttachReadAndUpsertSurvives(t *testing.T) {
	m := newIdentityTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	// The attach helper's baseline, read before the interleaved write.
	baseline := loadFrame(t, m, "%5", 100, "t100")

	if err := m.frames.UpdateSessionIdentity(parent.FrameID, "sess-INTERLEAVED", "/w/interleaved", 40); err != nil {
		t.Fatalf("UpdateSessionIdentity: %v", err)
	}

	ref := agentpkg.SubagentRef{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		StartedAt:       50,
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}
	attached, stored, err := m.attachProxyRefWithRetry(baseline, ref, 50)
	if err != nil {
		t.Fatalf("attachProxyRefWithRetry: %v", err)
	}
	if !attached {
		t.Fatalf("attached = false, want true")
	}
	if len(stored.Subagents) != 1 || stored.Subagents[0].SourcePID != 200 {
		t.Fatalf("Subagents = %+v, want the merged codex proxy ref", stored.Subagents)
	}
	assertIdentity(t, stored, "sess-INTERLEAVED", "/w/interleaved")
	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-INTERLEAVED", "/w/interleaved")
}

// --- ordering ---

// identityEvent is one hook payload from a `cc` sender, carrying the identity
// it wants written.
// identityEvent builds one hook carrying an identity, stamped with the version
// handleEvent would have allocated for it on arrival. The version is part of
// the event, so it is set here rather than passed alongside.
func identityEvent(sessionID, cwd string, seq int64) EventRequest {
	return EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		PurdexName:      "PdxStop",
		AgentType:       "cc",
		SenderPID:       100,
		SenderStartTime: "t100",
		RawEvent:        identityPayload(sessionID, cwd),
		identitySeq:     seq,
	}
}

// TestSessionIdentity_OlderEventDoesNotOverwriteNewer — two hooks from ONE
// process can be in flight at the same time, and nothing makes them reach the
// identity write in the order they were emitted. opencode switches session
// inside a single process (spec §3.3), so the two events carry DIFFERENT
// identities for the same frame: A is paused after its status write, B lands
// and records session B, and A then resumes and writes session A back.
//
// The row is left describing neither event — last_seen_at from B, session_id
// from A — and because a frame's identity is only written when a hook carries
// one, that combination is what backfill then fixes in place.
//
// last_seen_at cannot be the version that settles this. A proxy attach moves
// last_seen_at without touching the identity at all, so ordering identity
// writes by it would make an unrelated write reorder them.
func TestSessionIdentity_OlderEventDoesNotOverwriteNewer(t *testing.T) {
	m := newIdentityTestModule(t)
	frame := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	// B is the newer event and lands first.
	m.recordSessionIdentity(identityEvent("sess-B", "/w/b", 300), frame.FrameID)
	// A was emitted earlier and arrives late.
	m.recordSessionIdentity(identityEvent("sess-A", "/w/a", 200), frame.FrameID)

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-B", "/w/b")
}

// The guard must not become "the first write wins": an event that really is
// newer still replaces what is stored.
func TestSessionIdentity_NewerEventStillWins(t *testing.T) {
	m := newIdentityTestModule(t)
	frame := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	m.recordSessionIdentity(identityEvent("sess-A", "/w/a", 200), frame.FrameID)
	m.recordSessionIdentity(identityEvent("sess-B", "/w/b", 300), frame.FrameID)

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-B", "/w/b")
}

// The version is the EVENT's, not the row's clock: a proxy attach bumps
// last_seen_at between two identity writes and changes nothing about which of
// them is newer.
func TestSessionIdentity_LastSeenAtIsNotTheVersion(t *testing.T) {
	m := newIdentityTestModule(t)
	frame := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	m.recordSessionIdentity(identityEvent("sess-A", "/w/a", 200), frame.FrameID)
	if err := m.frames.UpdateStatusAndLastSeen(frame.FrameID, agentpkg.StatusRunning, 9999); err != nil {
		t.Fatalf("UpdateStatusAndLastSeen: %v", err)
	}
	// Still older than A by event order, however far last_seen_at has moved.
	m.recordSessionIdentity(identityEvent("sess-STALE", "/w/stale", 150), frame.FrameID)

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-A", "/w/a")
}

// --- where the version comes from -------------------------------------------

// postIdentityEvent drives one hook all the way through handleEvent, which is
// the only path that allocates an identity version. The direct
// applyFrameEvent tests above hand a version in and so cannot say anything
// about where it came from or when it was taken.
func postIdentityEvent(t *testing.T, m *Module, sessionID, cwd string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"tmux_session":      "work",
		"tmux_pane_id":      "%5",
		"sender_pid":        100,
		"sender_start_time": "t100",
		"purdex_name":       "PdxUserPromptSubmit",
		"agent_type":        "cc",
		"raw_event":         identityPayload(sessionID, cwd),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/agent/event", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", w.Code, w.Body.String())
	}
}

// TestSessionIdentity_SlowVerificationDoesNotOvertakeANewerEvent — the
// version has to be allocated when the event ARRIVES, not when it is about to
// be written, and verification sits between the two.
//
// Verification is a real wait: it reads the sender's process with `ps` and
// asks tmux about the pane. Two hooks from one process can be in flight at
// once (opencode switches session inside a single process without a
// SessionStart), and there is nothing that makes them clear verification in
// the order they arrived. A version taken AFTER verification is therefore the
// order the goroutines finished waiting, not the order the daemon received
// them — so the event that arrived first, and verified slowest, gets the
// HIGHEST version and overwrites the identity of the one that arrived after
// it. The store's ordering guard cannot help: it is being handed the wrong
// numbers.
//
// A arrives first and blocks in verification until B — which arrives second —
// has been written in full. B's identity is the newer one and must survive.
func TestSessionIdentity_SlowVerificationDoesNotOvertakeANewerEvent(t *testing.T) {
	m := newIdentityTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	aInVerify := make(chan struct{})
	bWritten := make(chan struct{})
	origVerify := verifyEventFn
	verifyEventFn = func(_ *Module, req EventRequest) verifyDecision {
		if strings.Contains(string(req.RawEvent), "sess-A") {
			close(aInVerify)
			<-bWritten // A is slow: it clears verification only after B is done
		}
		return verifyDecision{Accepted: true}
	}
	t.Cleanup(func() { verifyEventFn = origVerify })

	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		postIdentityEvent(t, m, "sess-A", "/w/a")
	}()

	<-aInVerify // A is inside the handler, past the point a version is due
	postIdentityEvent(t, m, "sess-B", "/w/b")
	close(bWritten)
	<-aDone

	assertIdentity(t, loadFrame(t, m, "%5", 100, "t100"), "sess-B", "/w/b")
}
