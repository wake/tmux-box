package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/agent"
)

// TestValidateSpecsCoverEmitted_Equal is the fix-plan §2.3 PT2 assertion:
// the real rendered body and opencodeEventSpecs agree on the event set.
// Together with PT7 this is the build-time contract guard that keeps the
// JS template and Go-side specs from drifting apart (plan §1.5 — runtime
// is deliberately not involved).
func TestValidateSpecsCoverEmitted_Equal(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	if err := validateSpecsCoverEmitted(body, opencodeEventSpecs); err != nil {
		t.Fatalf("validateSpecsCoverEmitted for the real template must be nil; got %v", err)
	}
}

// TestValidateSpecsCoverEmitted_EmitNotInSpec is the fix-plan §2.3 PT3
// assertion: a template that emits an event not listed in specs must
// error. Failure mode this catches: someone adding a new emit() without
// updating opencodeEventSpecs.
func TestValidateSpecsCoverEmitted_EmitNotInSpec(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx") + "\nawait emit('Ghost', {})\n"
	if err := validateSpecsCoverEmitted(body, opencodeEventSpecs); err == nil {
		t.Fatal("expected error for emit-not-in-spec; got nil")
	}
}

// TestValidateSpecsCoverEmitted_SpecNotInEmit is the fix-plan §2.3 PT4
// assertion: a specs list that declares an event the template never
// emits must error. Failure mode this catches: removing an emit without
// updating opencodeEventSpecs.
func TestValidateSpecsCoverEmitted_SpecNotInEmit(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	extended := append([]agent.HookEventSpec(nil), opencodeEventSpecs...)
	extended = append(extended, agent.HookEventSpec{PurdexName: "PdxPhantom", UpstreamKeys: []string{"Phantom"}})
	if err := validateSpecsCoverEmitted(body, extended); err == nil {
		t.Fatal("expected error for spec-not-in-emit; got nil")
	}
}

func TestValidateSpecsCoverEmitted_IgnoresNonInstallableSpecs(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	extended := append([]agent.HookEventSpec(nil), opencodeEventSpecs...)
	extended = append(extended,
		agent.HookEventSpec{PurdexName: "PdxIgnoredSynthetic", UpstreamKeys: []string{"IgnoredSynthetic"}, Handling: agent.HookHandlingIgnored, Description: "Ignored synthetic hook"},
		agent.HookEventSpec{PurdexName: "PdxUnsupportedSynthetic", UpstreamKeys: []string{"UnsupportedSynthetic"}, Handling: agent.HookHandlingUnsupported, Description: "Unsupported synthetic hook"},
	)
	if err := validateSpecsCoverEmitted(body, extended); err != nil {
		t.Fatalf("validateSpecsCoverEmitted should ignore non-installable specs; got %v", err)
	}
}

func TestOpenCodeCheckHooks_ExcludesNonInstallableSpecs(t *testing.T) {
	original := opencodeEventSpecs
	opencodeEventSpecs = append(append([]agent.HookEventSpec(nil), opencodeEventSpecs...),
		agent.HookEventSpec{PurdexName: "PdxIgnoredSynthetic", UpstreamKeys: []string{"IgnoredSynthetic"}, Handling: agent.HookHandlingIgnored, Description: "Ignored synthetic hook"},
		agent.HookEventSpec{PurdexName: "PdxUnsupportedSynthetic", UpstreamKeys: []string{"UnsupportedSynthetic"}, Handling: agent.HookHandlingUnsupported, Description: "Unsupported synthetic hook"},
	)
	t.Cleanup(func() { opencodeEventSpecs = original })

	for _, name := range opencodeEventNames() {
		if name == "PdxIgnoredSynthetic" || name == "PdxUnsupportedSynthetic" {
			t.Fatalf("opencodeEventNames included non-installable spec %q", name)
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	SetResolveCanonicalPdxPathForTesting(t, func() (string, bool) { return "/usr/local/bin/pdx", true })
	p := NewProvider()
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatalf("CheckHooks: %v", err)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("expected managed plugin at %s: %v", pluginPath, err)
	}
	for _, name := range []string{"IgnoredSynthetic", "UnsupportedSynthetic"} {
		if _, ok := status.Events[name]; ok {
			t.Errorf("CheckHooks.Events included non-installable event %q", name)
		}
	}
}

// TestRenderManagedPlugin_ProducesValidBody is the fix-plan §2.3 PT5
// assertion: rendering does not panic and the body contains the managed
// marker that CheckHooks keys off of.
func TestRenderManagedPlugin_ProducesValidBody(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("renderManagedPlugin panicked: %v", r)
		}
	}()
	body := renderManagedPlugin("/fake/pdx")
	if !strings.Contains(body, managedMarker) {
		t.Fatalf("rendered body missing managed marker %q", managedMarker)
	}
}

// TestTemplateSpecsParity is the fix-plan §2.3 PT7 build-time contract
// guard. If the template emits X and specs do not declare X (or vice
// versa) this test fails and blocks the merge — so commits never ship an
// inconsistent pair. Per v4 plan §1.5 this replaces a runtime panic that
// would have punished every CheckHooks / Install call for a build-time
// drift; contract enforcement belongs at test layer.
func TestTemplateSpecsParity(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	if err := validateSpecsCoverEmitted(body, opencodeEventSpecs); err != nil {
		t.Fatalf("template/specs parity drifted: %v", err)
	}
}

// TestExtractEmittedEvents is the fix-plan §2.3 PT1 assertion: the helper
// reliably pulls event names from emit('...') and emit("...") calls in a
// synthetic body, including across multiple lines and with incidental
// whitespace. This helper is test-only (plan §1.5); it underwrites the
// template/specs parity check, not runtime health.
func TestExtractEmittedEvents(t *testing.T) {
	body := `
  await emit('SessionStart', {foo: 1})
  await emit("UserPromptSubmit",
    {bar: 2})
  await emit('Stop', {})
  // not captured: commented out. // await emit('Ignored', {})
`
	got := extractEmittedEvents(body)
	want := map[string]bool{"SessionStart": true, "UserPromptSubmit": true, "Stop": true, "Ignored": true}
	// We deliberately include 'Ignored' from a comment — regex-based extraction
	// is known to see through comments. That is fine because this helper is
	// only used against the real template body where no such comments exist,
	// and PT2-PT4 tests assert the spec-side parity that would surface a
	// mismatch if such a ghost emit ever shipped.
	gotSet := make(map[string]bool, len(got))
	for _, n := range got {
		gotSet[n] = true
	}
	for n := range want {
		if !gotSet[n] {
			t.Errorf("extractEmittedEvents missing %q; got %v", n, got)
		}
	}
}

// TestExtractPdxPath_RoundtripEscapedLiterals is the fix-plan §2.3 PT6
// assertion: renderManagedPlugin writes pdxPath with %q (complete with
// escapes for backslash/quote/unicode/…) and extractPdxPath must be able
// to round-trip it. Using a naive regex (e.g. `"([^"]+)"`) would stop at
// the first escaped quote and then double-escape when re-rendered,
// producing a byte-mismatch on perfectly managed plugins — the v4 plan
// specifically calls this out.
func TestExtractPdxPath_RoundtripEscapedLiterals(t *testing.T) {
	cases := []string{
		"/path with spaces/pdx",
		`C:\Users\foo\pdx.exe`,
		`/weird"path/pdx`,
		"/使用者/pdx",
	}
	for _, input := range cases {
		body := renderManagedPlugin(input)
		got, ok := extractPdxPath(body)
		if !ok {
			t.Errorf("extractPdxPath failed for %q", input)
			continue
		}
		if got != input {
			t.Errorf("extractPdxPath(%q) round-trip = %q", input, got)
		}
	}
}

func TestRenderManagedPlugin_UsesInputModelAndSessionScopedSubagentKeys(t *testing.T) {
	rendered := renderManagedPlugin("/usr/local/bin/pdx")
	if !strings.Contains(rendered, "const model = input.model") {
		t.Fatalf("rendered plugin should read model from input.model: %s", rendered)
	}
	if !strings.Contains(rendered, "const subagentKey = input.sessionID + ':' + input.callID") {
		t.Fatalf("rendered plugin should scope subagent keys by sessionID+callID: %s", rendered)
	}
	if !strings.Contains(rendered, "if (activeSubagents.has(subagentKey)) return") {
		t.Fatalf("rendered plugin should ignore duplicate subagent starts: %s", rendered)
	}
}

// === W2 Phase 3 (P3-T3) PURDEX_EVENT const injection assertions ===

// TestRenderManagedPlugin_HasPdxEventConst asserts the rendered template
// declares the const PURDEX_EVENT object literal with all 8 installable
// PurdexName entries (per spec §2.3 — installable set is fixed for opencode
// at SessionStart / UserPromptSubmit / SubagentStart / SubagentStop /
// PermissionRequest / Stop / StopFailure / SessionEnd, all Pdx-prefixed).
func TestRenderManagedPlugin_HasPdxEventConst(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	if !strings.Contains(body, "const PURDEX_EVENT = {") {
		t.Fatal("rendered body missing `const PURDEX_EVENT = {` block")
	}
	want := []string{
		`PdxSessionStart: "PdxSessionStart"`,
		`PdxUserPromptSubmit: "PdxUserPromptSubmit"`,
		`PdxSubagentStart: "PdxSubagentStart"`,
		`PdxSubagentStop: "PdxSubagentStop"`,
		`PdxPermissionRequest: "PdxPermissionRequest"`,
		`PdxStop: "PdxStop"`,
		`PdxStopFailure: "PdxStopFailure"`,
		`PdxSessionEnd: "PdxSessionEnd"`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("PURDEX_EVENT const missing entry %q", w)
		}
	}
}

// TestRenderManagedPlugin_EmitArgsArePdxName asserts every emit() call in
// the rendered template uses PURDEX_EVENT.PdxXxx as its first argument
// (no string literals). Pairs with NoLegacyEmitLiteral to lock the post-P3
// template against accidental string-literal emit regressions.
func TestRenderManagedPlugin_EmitArgsArePdxName(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	want := []string{
		"emit(PURDEX_EVENT.PdxSessionStart,",
		"emit(PURDEX_EVENT.PdxPermissionRequest,",
		"emit(PURDEX_EVENT.PdxStopFailure,",
		"emit(PURDEX_EVENT.PdxStop,",
		"emit(PURDEX_EVENT.PdxSessionEnd,",
		"emit(PURDEX_EVENT.PdxUserPromptSubmit,",
		"emit(PURDEX_EVENT.PdxSubagentStart,",
		"emit(PURDEX_EVENT.PdxSubagentStop,",
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("rendered body missing emit() call %q", w)
		}
	}
}

// TestRenderManagedPlugin_NoLegacyEmitLiteral grep-confirms zero
// string-literal emit() calls (e.g. emit('SessionStart', ...)) survive in
// the rendered template post-P3. The whole point of the const refactor
// is to source-of-truth the names through Go catalog → JS const.
func TestRenderManagedPlugin_NoLegacyEmitLiteral(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	legacy := []string{
		"emit('SessionStart',",
		"emit('UserPromptSubmit',",
		"emit('SubagentStart',",
		"emit('SubagentStop',",
		"emit('PermissionRequest',",
		"emit('Stop',",
		"emit('StopFailure',",
		"emit('SessionEnd',",
		`emit("SessionStart",`,
		`emit("UserPromptSubmit",`,
		`emit("Stop",`,
		`emit("SessionEnd",`,
	}
	for _, lit := range legacy {
		if strings.Contains(body, lit) {
			t.Errorf("rendered body still contains legacy literal emit %q (P3-T3 should route via PURDEX_EVENT const)", lit)
		}
	}
}

// TestRenderManagedPlugin_GatesChildSessionLifecycle statically inspects
// the rendered JS body for the L1 child-session gate so a Bun-less CI
// still catches removal. It asserts the body (a) declares the
// subagentSessions map, (b) reads the authoritative child signal
// event.properties.info?.parentID, and (c) places the child-registration
// branch before the parent PdxSessionStart emit (child-check first).
func TestRenderManagedPlugin_GatesChildSessionLifecycle(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")

	if !strings.Contains(body, "subagentSessions") {
		t.Fatal("rendered body missing subagentSessions gate map")
	}
	if !strings.Contains(body, "event.properties.info?.parentID") {
		t.Fatal("rendered body missing child signal event.properties.info?.parentID")
	}

	setIdx := strings.Index(body, "subagentSessions.set(sid, parentID)")
	if setIdx < 0 {
		t.Fatal("rendered body missing child registration subagentSessions.set(sid, parentID)")
	}
	emitIdx := strings.Index(body, "emit(PURDEX_EVENT.PdxSessionStart,")
	if emitIdx < 0 {
		t.Fatal("rendered body missing PdxSessionStart emit")
	}
	if setIdx > emitIdx {
		t.Fatalf("child registration (idx %d) must precede PdxSessionStart emit (idx %d)", setIdx, emitIdx)
	}
}

// TestRenderManagedPlugin_ChildSessionGatesAllLifecycleEmits asserts the
// gate exists in every parent-level lifecycle branch (created / status /
// error / deleted), so a partial revert of the fix is caught statically.
func TestRenderManagedPlugin_ChildSessionGatesAllLifecycleEmits(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	for _, want := range []string{
		"subagentSessions.set(sid, parentID)",
		"if (sid && subagentSessions.has(sid)) return",
		"subagentSessions.delete(sid)",
		"if (pid === sid) subagentSessions.delete(childID)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered body missing child-gating construct %q", want)
		}
	}
}

// TestSubagentSessionCreatedFixtureCarriesParentID guards the fixture used
// by the child-gating contract cases: it must be a session.created bus
// event whose info.parentID is set (the authoritative child signal).
func TestSubagentSessionCreatedFixtureCarriesParentID(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(payloadFixturesDir, "session.created.subagent.json"))
	if err != nil {
		t.Fatalf("read subagent fixture: %v", err)
	}
	var env struct {
		Kind       string                 `json:"kind"`
		EventType  string                 `json:"eventType"`
		Properties map[string]interface{} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("parse subagent fixture: %v", err)
	}
	if env.Kind != "bus-event" || env.EventType != "session.created" {
		t.Fatalf("fixture kind/eventType = %q/%q, want bus-event/session.created", env.Kind, env.EventType)
	}
	info, ok := env.Properties["info"].(map[string]any)
	if !ok {
		t.Fatal("fixture missing properties.info")
	}
	if pid, _ := info["parentID"].(string); pid == "" {
		t.Fatal("fixture properties.info.parentID must be set for a subagent session.created")
	}
}

// TestRenderManagedPlugin_MagicMarkerUnchanged guards against accidental
// marker bumps during P3-T3 — the managed marker is the version stamp
// CheckHooks keys off of, and bumping it would invalidate every existing
// installed plugin without a migration story. (The TemplateSpecsParity
// test only covers spec/emit drift, not marker drift.)
func TestRenderManagedPlugin_MagicMarkerUnchanged(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")
	if !strings.Contains(body, "pdx-managed:opencode-hooks:v1") {
		t.Fatalf("rendered body missing magic marker `pdx-managed:opencode-hooks:v1`")
	}
}

// TestRenderManagedPlugin_StopAndPromptEmitCwd pins the two emits that spec
// 2026-09-07 §3.3 measured sending session_id alone. opencode switches back
// into an existing session inside one process without a session.created, so a
// frame that only ever sees Stop / UserPromptSubmit would otherwise carry a
// session id it has no directory to resume from.
func TestRenderManagedPlugin_StopAndPromptEmitCwd(t *testing.T) {
	body := renderManagedPlugin("/fake/pdx")

	for _, tc := range []struct{ event, call string }{
		{"PdxStop", "emit(PURDEX_EVENT.PdxStop, {"},
		{"PdxUserPromptSubmit", "emit(PURDEX_EVENT.PdxUserPromptSubmit, {"},
	} {
		start := strings.Index(body, tc.call)
		if start < 0 {
			t.Fatalf("rendered body missing %s emit (%q)", tc.event, tc.call)
		}
		end := strings.Index(body[start:], "})")
		if end < 0 {
			t.Fatalf("rendered body has no closing brace for the %s emit", tc.event)
		}
		if call := body[start : start+end]; !strings.Contains(call, "cwd: pdxCwd()") {
			t.Errorf("%s emit missing `cwd: pdxCwd()`; got %q", tc.event, call)
		}
	}
}
