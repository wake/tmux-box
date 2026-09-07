package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// fakeProviderWithInstance is a SessionProvider that reports a fixed tmux
// generation, so a test can assert the value the daemon stamps into the
// provenance envelope (spec §4.3.1).
func fakeProviderWithInstance(instance string) *fakeSessionProvider {
	return &fakeSessionProvider{
		sessions:     []session.SessionInfo{{Code: "work-code", Name: "work"}},
		tmuxInstance: instance,
	}
}

// deriveWithSessionDetail mirrors what Task 2 made the real cc/codex/opencode
// providers do on SessionStart: surface session_id and cwd through
// DeriveResult.Detail. newProxyTestModule's fakes drop RawEvent entirely, so
// the provenance tests need providers that do not.
func deriveWithSessionDetail(_ string, raw json.RawMessage) agentpkg.DeriveResult {
	detail := map[string]any{}
	if len(raw) > 0 {
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err == nil {
			for _, key := range []string{"session_id", "cwd"} {
				if v, ok := parsed[key].(string); ok && v != "" {
					detail[key] = v
				}
			}
		}
	}
	return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle, Detail: detail}
}

// newProvenanceTestModule is newProxyTestModule with detail-carrying providers
// and a known tmux generation. Registry.Get returns the FIRST registered
// provider for a type, so the registry is replaced rather than appended to.
func newProvenanceTestModule(t *testing.T, tmuxInstance string) *Module {
	t.Helper()
	m := newProxyTestModule(t)
	m.registry = agentpkg.NewRegistry()
	for _, typeName := range []string{"cc", "codex"} {
		m.registry.Register(&fakeAgentProvider{typeName: typeName, derive: deriveWithSessionDetail})
	}
	m.sessions = fakeProviderWithInstance(tmuxInstance)
	return m
}

// buildNormalizedForTest runs the production pair the hook handler runs —
// applyFrameEvent then buildProjectionNormalized then attachProvenance — so
// the tests exercise the real attachment condition instead of a re-implemented
// copy of it. Mirrors handler.go's derive → apply → normalize sequence for a
// request with no tmux session name (pane projection only).
func (m *Module) buildNormalizedForTest(t *testing.T, req EventRequest) agentpkg.NormalizedEvent {
	t.Helper()
	broadcastTs := time.Now().UnixNano()
	var result agentpkg.DeriveResult
	if m.registry != nil {
		if provider, ok := m.registry.Get(req.AgentType); ok {
			result = provider.DeriveStatus(req.PurdexName, req.RawEvent)
		}
	}
	projection, frameMeta, err := m.applyFrameEvent(req, result, broadcastTs)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	normalized := buildProjectionNormalized(projection, req.AgentType, req.PurdexName, broadcastTs, result)
	attachProvenance(&normalized, frameMeta)
	return normalized
}

// ---------------------------------------------------------------------------
// Provenance envelope (spec §4.3.1)
// ---------------------------------------------------------------------------

func TestProvenance_RootSessionStart_EmitsEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, "4471:1788740000")
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S1","cwd":"/w/p"}`),
	}
	withProcessTree(t, map[int]int{200: 999})

	ev := m.buildNormalizedForTest(t, req)
	raw, ok := ev.Detail["pdx_provenance"]
	if !ok {
		t.Fatalf("root SessionStart carried no provenance: detail=%+v", ev.Detail)
	}
	p, ok := raw.(Provenance)
	if !ok {
		t.Fatalf("pdx_provenance = %T, want agent.Provenance", raw)
	}
	if !p.OwnerSessionStart || p.AgentType != "codex" || p.SessionID != "S1" ||
		p.Cwd != "/w/p" || p.TmuxPaneID != "%5" || p.TmuxInstance != "4471:1788740000" {
		t.Fatalf("envelope = %+v", p)
	}
}

// A SessionStart landing on an existing frame (a /clear, say) is how a new
// session id replaces the recorded one, so it must be granted provenance too.
func TestProvenance_SessionStartOnExistingFrame_EmitsEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, "4471:1788740000")
	seedFrame(t, m, "%5", "codex", 200, "t200", 10)
	withProcessTree(t, map[int]int{200: 999})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S9","cwd":"/w/p"}`),
	}

	ev := m.buildNormalizedForTest(t, req)
	raw, ok := ev.Detail["pdx_provenance"]
	if !ok {
		t.Fatalf("SessionStart on an existing frame carried no provenance: detail=%+v", ev.Detail)
	}
	if p := raw.(Provenance); p.SessionID != "S9" || !p.OwnerSessionStart {
		t.Fatalf("envelope = %+v, want session_id S9", p)
	}
}

func TestProvenance_ProxyCollapsed_NoEnvelope_OuterTypeIsOwner(t *testing.T) {
	// codex started inside a live cc pane: the broadcast must say cc and must
	// NOT carry codex's session id. Spec §4.3.1.
	m := newProvenanceTestModule(t, "4471:1788740000")
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100"})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S1","cwd":"/w/p"}`),
	}

	ev := m.buildNormalizedForTest(t, req)
	if _, ok := ev.Detail["pdx_provenance"]; ok {
		t.Fatalf("proxy-collapsed event must not carry provenance")
	}
	if ev.AgentType != "cc" {
		t.Fatalf("outer agent_type = %q, want cc", ev.AgentType)
	}
}

func TestProvenance_SameTypeAbove_NoEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, "4471:1788740000")
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100"})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S2"}`),
	}

	if _, ok := m.buildNormalizedForTest(t, req).Detail["pdx_provenance"]; ok {
		t.Fatalf("cc-inside-cc must not carry provenance")
	}
}

func TestProvenance_PostUpsertReconcile_NoEnvelope(t *testing.T) {
	// Pre-walk says root, reconcileCreatedFrameAsProxy folds it afterwards.
	// Mirrors TestPhase35_IT3_PreWalkMiss_PostReconcileHit's fixture: only
	// reads for PID 200 advance the sequence, so call 1 is classifyAncestor,
	// call 2 the parentFrameID lookup, call 3 the post-Upsert reconcile.
	m := newProvenanceTestModule(t, "4471:1788740000")
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	withProcessTreeSequence(t, 200, []int{999, 999, 100}) // miss, miss, hit
	withLivePids(t, map[int]string{100: "t100"})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S3"}`),
	}

	ev := m.buildNormalizedForTest(t, req)

	// Assert the fixture actually reached the reconcile before asserting the
	// absence of an envelope — otherwise a pass could just mean the walk
	// never folded anything.
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 || frames[0].FrameID != parent.FrameID {
		t.Fatalf("frames = %+v, want only the cc parent %q (codex folded)", frames, parent.FrameID)
	}
	foldedIn := false
	for _, ref := range frames[0].Subagents {
		if ref.IsProxy && ref.Type == "codex" && ref.SourcePID == 200 {
			foldedIn = true
		}
	}
	if !foldedIn {
		t.Fatalf("cc parent gained no proxy:codex ref: %+v", frames[0].Subagents)
	}

	if _, ok := ev.Detail["pdx_provenance"]; ok {
		t.Fatalf("post-Upsert reconcile must revoke the envelope")
	}
}

func TestProvenance_SenderUncertain_NoEnvelope(t *testing.T) {
	m := newProvenanceTestModule(t, "4471:1788740000")
	withProcessTree(t, map[int]int{200: 999})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		SenderUncertain: true, RawEvent: []byte(`{"session_id":"S4"}`),
	}

	if _, ok := m.buildNormalizedForTest(t, req).Detail["pdx_provenance"]; ok {
		t.Fatalf("uncertain sender must not carry provenance")
	}
}

func TestProvenance_NonSessionStart_NoEnvelope(t *testing.T) {
	// VerdictRoot is the zero value of AncestorVerdict, so a lifecycle that
	// never runs the walk must still be excluded by the explicit
	// SessionStart clause.
	m := newProvenanceTestModule(t, "4471:1788740000")
	seedFrame(t, m, "%5", "codex", 200, "t200", 10)
	withProcessTree(t, map[int]int{200: 999})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxUserPromptSubmit",
		RawEvent: []byte(`{"session_id":"S5"}`),
	}

	if _, ok := m.buildNormalizedForTest(t, req).Detail["pdx_provenance"]; ok {
		t.Fatalf("non-SessionStart must not carry provenance")
	}
}

func TestProvenance_NilSessionProvider_EmptyInstance(t *testing.T) {
	m := newProvenanceTestModule(t, "4471:1788740000")
	m.sessions = nil
	withProcessTree(t, map[int]int{200: 999})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S6"}`),
	}

	ev := m.buildNormalizedForTest(t, req)
	p, ok := ev.Detail["pdx_provenance"].(Provenance)
	if !ok {
		t.Fatalf("expected an envelope, detail=%+v", ev.Detail)
	}
	if p.TmuxInstance != "" {
		t.Fatalf("TmuxInstance = %q, want empty for a half-wired daemon", p.TmuxInstance)
	}
}
