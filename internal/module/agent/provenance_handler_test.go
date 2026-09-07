package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// ---------------------------------------------------------------------------
// Fixtures for GET /api/sessions/{code}/provenance (Task 7)
// ---------------------------------------------------------------------------

// newProvenanceQueryModule wires a module with a fake tmux server and a session
// provider that only has to answer TmuxInstance — the query resolves a pane to
// its session through the tmux session *id*, never through the provider.
func newProvenanceQueryModule(t *testing.T) (*Module, *tmux.FakeExecutor, *fakeFastSessionProvider) {
	t.Helper()
	m := newTestModule(t)
	fake := tmux.NewFakeExecutor()
	m.tmux = fake
	sessions := &fakeFastSessionProvider{tmuxInstance: "4465:1788754497"}
	m.sessions = sessions
	return m, fake, sessions
}

// codeOf is EncodeSessionID with the error turned into a test failure.
func codeOf(t *testing.T, tmuxID string) string {
	t.Helper()
	code, err := session.EncodeSessionID(tmuxID)
	if err != nil {
		t.Fatalf("EncodeSessionID(%q): %v", tmuxID, err)
	}
	return code
}

// attachPane declares that `paneID` belongs to tmux session `tmuxID` and that
// its current process is `panePID` — the two facts the query needs about a
// pane.
func attachPane(fake *tmux.FakeExecutor, paneID, tmuxID string, panePID string) {
	fake.SetPaneSessionID(paneID, tmuxID)
	fake.SetPanePID(paneID, panePID)
}

// withRecordedReads records every PID the CURRENT readProcessInfoFn is asked
// for. Install it after withProcessTree so the recorded calls are the reads
// that actually reached the process table — a memo hit records nothing.
func withRecordedReads(t *testing.T, seen *[]int) {
	t.Helper()
	orig := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		*seen = append(*seen, pid)
		return orig(pid)
	}
	t.Cleanup(func() { readProcessInfoFn = orig })
}

// withSlowReads makes every process read take `d`, so a request deadline can be
// made to expire partway through a pane's frames rather than before the first
// one.
func withSlowReads(t *testing.T, d time.Duration) {
	t.Helper()
	orig := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		time.Sleep(d)
		return orig(pid)
	}
	t.Cleanup(func() { readProcessInfoFn = orig })
}

// shiftingInstanceProvider answers TmuxInstance with a different value on each
// call, modelling a tmux server that restarted mid-request. It borrows the rest
// of SessionProvider from the fast-path fake.
type shiftingInstanceProvider struct {
	*fakeFastSessionProvider
	instances []string
	calls     int
}

func (p *shiftingInstanceProvider) TmuxInstance() string {
	if p.calls >= len(p.instances) {
		if len(p.instances) == 0 {
			return ""
		}
		return p.instances[len(p.instances)-1]
	}
	v := p.instances[p.calls]
	p.calls++
	return v
}

type provenanceBody struct {
	Found        bool   `json:"found"`
	AgentType    string `json:"agent_type"`
	SessionID    string `json:"session_id"`
	Cwd          string `json:"cwd"`
	TmuxPaneID   string `json:"tmux_pane_id"`
	TmuxInstance string `json:"tmux_instance"`
	LastSeenAt   int64  `json:"last_seen_at"`
}

// getProvenance drives the endpoint through the module's own route table, so a
// missing or misspelled registration fails here rather than in production.
func getProvenance(t *testing.T, m *Module, code string) (int, provenanceBody, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions/" + code + "/provenance")
	if err != nil {
		t.Fatalf("GET provenance: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body provenanceBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %q: %v", string(raw), err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode generic %q: %v", string(raw), err)
	}
	return resp.StatusCode, body, generic
}

// ---------------------------------------------------------------------------
// The answer
// ---------------------------------------------------------------------------

// TestHandleSessionProvenance_OneRoot is the whole point of the endpoint: the
// single live root agent of the session comes back with the identity it
// recorded for itself, and with the generation the reading belongs to.
func TestHandleSessionProvenance_OneRoot(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	status, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	want := provenanceBody{
		Found:        true,
		AgentType:    "cc",
		SessionID:    "sess-1",
		Cwd:          "/w/purdex",
		TmuxPaneID:   "%5",
		TmuxInstance: "4465:1788754497",
		LastSeenAt:   42,
	}
	if body != want {
		t.Fatalf("body = %+v, want %+v", body, want)
	}
}

// TestHandleSessionProvenance_NoRoots_FoundFalse — a frame that never passes
// through the pane's own process is not inside the pane's tree, so it is not an
// answer. found:false, not a guess.
func TestHandleSessionProvenance_NoRoots_FoundFalse(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	// 100 → 300 → 1: reaches PID 1 without ever passing panePID 200.
	withProcessTree(t, map[int]int{100: 300, 300: 1})
	withLivePids(t, map[int]string{100: "t100"})

	status, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body.Found {
		t.Fatalf("found = true, want false (body = %+v)", body)
	}
}

// TestHandleSessionProvenance_EmptySessionID_FoundFalse pins WHERE the filter
// lives. resolvePaneOwners deliberately returns a root that never reported a
// session id (Task 6 asserts that); the handler is what drops it, because a
// resume command cannot be composed without an id.
func TestHandleSessionProvenance_EmptySessionID_FoundFalse(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	status, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body.Found {
		t.Fatalf("found = true, want false — a root with no session id is not an answer (body = %+v)", body)
	}
}

// TestHandleSessionProvenance_TwoRootsTwoPanes_MostRecentWins — two panes of
// one tmux session, each with a root. The tie-break's first rule is recency,
// and the answer carries the winning pane's id.
func TestHandleSessionProvenance_TwoRootsTwoPanes_MostRecentWins(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%1", "$0", "200")
	attachPane(fake, "%2", "$0", "210")
	seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 10, "sess-old", "/w/a")
	seedIdentityFrame(t, m, "%2", "codex", 110, "t110", 20, "sess-new", "/w/b")
	withProcessTree(t, map[int]int{100: 200, 200: 1, 110: 210, 210: 1})
	withLivePids(t, map[int]string{100: "t100", 110: "t110"})

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if !body.Found {
		t.Fatalf("found = false, want true")
	}
	if body.SessionID != "sess-new" || body.TmuxPaneID != "%2" || body.AgentType != "codex" {
		t.Fatalf("body = %+v, want the pane %%2 / codex / sess-new root (largest last_seen_at)", body)
	}
}

// TestHandleSessionProvenance_EqualLastSeen_LowestFrameIDWins — with the same
// last_seen_at the answer must still be one specific frame, not whichever the
// store happened to hand back first.
func TestHandleSessionProvenance_EqualLastSeen_LowestFrameIDWins(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%1", "$0", "200")
	attachPane(fake, "%2", "$0", "210")
	a := seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 77, "sess-a", "/w/a")
	b := seedIdentityFrame(t, m, "%2", "codex", 110, "t110", 77, "sess-b", "/w/b")
	withProcessTree(t, map[int]int{100: 200, 200: 1, 110: 210, 210: 1})
	withLivePids(t, map[int]string{100: "t100", 110: "t110"})

	wantSession, wantPane := "sess-a", "%1"
	if b.FrameID < a.FrameID {
		wantSession, wantPane = "sess-b", "%2"
	}

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if !body.Found {
		t.Fatalf("found = false, want true")
	}
	if body.SessionID != wantSession || body.TmuxPaneID != wantPane {
		t.Fatalf("body = %+v, want %s / %s (ascending frame_id: %q vs %q)",
			body, wantSession, wantPane, a.FrameID, b.FrameID)
	}
}

// TestHandleSessionProvenance_InactivePaneInAnotherWindow_Considered — the
// session's active pane has no agent at all; the only root sits in a second
// window. An implementation that asks tmux for the session's active pane (the
// only pane the Executor interface can name) finds nothing and fails here.
func TestHandleSessionProvenance_InactivePaneInAnotherWindow_Considered(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	fake.SetActivePaneMetadata("work", tmux.TmuxPaneMetadata{
		SessionID: "$0", SessionName: "work", WindowID: "@0", PaneID: "%1",
	})
	attachPane(fake, "%1", "$0", "200") // active, framed by nothing
	attachPane(fake, "%2", "$0", "210") // second window, holds the agent
	seedIdentityFrame(t, m, "%2", "cc", 110, "t110", 20, "sess-bg", "/w/b")
	withProcessTree(t, map[int]int{110: 210, 210: 1})
	withLivePids(t, map[int]string{110: "t110"})

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if !body.Found || body.TmuxPaneID != "%2" || body.SessionID != "sess-bg" {
		t.Fatalf("body = %+v, want the root in the non-active pane %%2", body)
	}
}

// TestHandleSessionProvenance_OtherSessionsPaneIgnored — a framed pane of a
// different tmux session is not part of this session's answer, even though both
// live on the same tmux server.
func TestHandleSessionProvenance_OtherSessionsPaneIgnored(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")  // $0
	fake.AddSession("other", "/o") // $1
	attachPane(fake, "%1", "$0", "200")
	attachPane(fake, "%2", "$1", "210")
	seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 10, "sess-mine", "/w/a")
	seedIdentityFrame(t, m, "%2", "cc", 110, "t110", 99, "sess-theirs", "/o/b")
	withProcessTree(t, map[int]int{100: 200, 200: 1, 110: 210, 210: 1})
	withLivePids(t, map[int]string{100: "t100", 110: "t110"})

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if !body.Found {
		t.Fatalf("found = false, want true")
	}
	if body.SessionID != "sess-mine" || body.TmuxPaneID != "%1" {
		t.Fatalf("body = %+v, want sess-mine / %%1 — the other session's newer root must not be borrowed", body)
	}
}

// TestHandleSessionProvenance_UnresolvablePaneSessionID_PaneExcluded — when the
// pane's session id cannot be read, the pane is excluded outright. There is no
// fallback to the pane's session NAME: the name would have matched here, and
// the newer frame behind it would have won.
func TestHandleSessionProvenance_UnresolvablePaneSessionID_PaneExcluded(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%1", "$0", "200")
	// %9 reports a session NAME but no session id — PaneSessionID errors.
	fake.SetPaneSessionName("%9", "work")
	fake.SetPanePID("%9", "290")
	seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 10, "sess-mine", "/w/a")
	seedIdentityFrame(t, m, "%9", "cc", 190, "t190", 99, "sess-nameonly", "/w/z")
	withProcessTree(t, map[int]int{100: 200, 200: 1, 190: 290, 290: 1})
	withLivePids(t, map[int]string{100: "t100", 190: "t190"})

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if !body.Found {
		t.Fatalf("found = false, want true")
	}
	if body.SessionID != "sess-mine" || body.TmuxPaneID != "%1" {
		t.Fatalf("body = %+v, want sess-mine / %%1 — a pane whose session id is unreadable is excluded, never resolved by name", body)
	}
}

// TestHandleSessionProvenance_RenameSwap_AnswersByID is the reason this query
// goes through the tmux session ID.
//
// Two live sessions swap names. The name→code map is PINNED to the pre-swap
// mapping — that is exactly what LookupCodeByName's 250 ms cache holds for a
// moment after an external rename — and the panes report the post-swap names.
// An implementation routed through PaneSessionName + resolveSessionCode
// therefore answers a query for $0's code with $1's agent: crossed over, and
// still found:true, which is why this test asserts WHICH agent came back.
//
// The mapping is pinned with a fake rather than driven through the real
// LookupCodeByName on purpose: the real cache expires, so the outcome would be
// decided by how fast the test ran instead of by the implementation.
func TestHandleSessionProvenance_RenameSwap_AnswersByID(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("A", "/a") // $0
	fake.AddSession("B", "/b") // $1
	codeA, codeB := codeOf(t, "$0"), codeOf(t, "$1")

	// Session ids are immutable; only the names move.
	attachPane(fake, "%1", "$0", "200")
	attachPane(fake, "%2", "$1", "210")
	// Post-swap names: $0 is now called "B", $1 is now called "A".
	fake.SetPaneSessionName("%1", "B")
	fake.SetPaneSessionName("%2", "A")

	// Pre-swap name→code, pinned: what a stale lookup would still believe.
	m.sessions = &fakeFastSessionProvider{
		tmuxInstance: "4465:1788754497",
		lookup:       map[string]string{"A": codeA, "B": codeB},
		sessions: []session.SessionInfo{
			{Name: "A", Code: codeA},
			{Name: "B", Code: codeB},
		},
	}

	seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 10, "sess-zero", "/a")
	seedIdentityFrame(t, m, "%2", "cc", 110, "t110", 20, "sess-one", "/b")
	withProcessTree(t, map[int]int{100: 200, 200: 1, 110: 210, 210: 1})
	withLivePids(t, map[int]string{100: "t100", 110: "t110"})

	_, body, _ := getProvenance(t, m, codeA)
	if !body.Found {
		t.Fatalf("found = false, want true")
	}
	if body.SessionID != "sess-zero" || body.TmuxPaneID != "%1" {
		t.Fatalf("body = %+v, want sess-zero / %%1 — the query for $0's code was answered with the session that is CURRENTLY named A ($1)", body)
	}
}

// TestHandleSessionProvenance_UnknownCode_FoundFalseWith200 — a dead or bogus
// code is a normal race, not a client error. The SPA treats "no answer"
// uniformly, so it must not have to special-case a 404.
func TestHandleSessionProvenance_UnknownCode_FoundFalseWith200(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	status, body, generic := getProvenance(t, m, "zzzzzz")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an unknown code is not a 404", status)
	}
	if body.Found {
		t.Fatalf("found = true, want false")
	}
	if _, ok := generic["tmux_instance"]; !ok {
		t.Fatalf("body %v has no tmux_instance key — found:false still carries the generation", generic)
	}
	if body.TmuxInstance != "4465:1788754497" {
		t.Fatalf("tmux_instance = %q, want the sampled generation", body.TmuxInstance)
	}
}

// TestHandleSessionProvenance_GenerationDisagrees_EmptyInstance — the tmux
// server restarted while the frames were being walked. The two samples
// disagree, so the generation is reported as unknown, and "" authorises nothing
// on the SPA side.
func TestHandleSessionProvenance_GenerationDisagrees_EmptyInstance(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	m.sessions = &shiftingInstanceProvider{
		fakeFastSessionProvider: &fakeFastSessionProvider{},
		instances:               []string{"4465:1788754497", "9999:1788800000"},
	}
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	status, body, generic := getProvenance(t, m, codeOf(t, "$0"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body.TmuxInstance != "" {
		t.Fatalf("tmux_instance = %q, want \"\" — the samples disagreed", body.TmuxInstance)
	}
	if _, ok := generic["tmux_instance"]; !ok {
		t.Fatalf("body %v dropped the tmux_instance key; \"\" is a transmitted value", generic)
	}
}

// TestHandleSessionProvenance_DeadlineExpired_FoundFalse — the request carries
// a deadline, and a walk that runs past it is not an answer. A partial owner
// list arrives together with ctx.Err(); the handler must discard it, because
// half a pane's frames can name a root that a complete walk would have rejected.
func TestHandleSessionProvenance_DeadlineExpired_FoundFalse(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	orig := provenanceTimeout
	provenanceTimeout = -1 // expired before the first frame is looked at
	t.Cleanup(func() { provenanceTimeout = orig })

	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	status, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body.Found {
		t.Fatalf("found = true, want false — an expired deadline is not an answer (body = %+v)", body)
	}
	if body.TmuxInstance != "4465:1788754497" {
		t.Fatalf("tmux_instance = %q, want the generation to still be stamped", body.TmuxInstance)
	}
}

// TestHandleSessionProvenance_PartialWalkDiscarded — the deadline expires
// PARTWAY through a pane, so resolvePaneOwners returns owners it had already
// decided together with ctx.Err(). Those owners must be thrown away: a walk
// that did not finish is not evidence, and the frames it never reached could
// have named a different root. Answering with the half it managed would be a
// guess dressed up as an answer.
//
// The fixture forces a genuine partial: two root-eligible frames in one pane
// and a process read slow enough that the first frame's walk alone overruns the
// deadline, so the second frame's ctx check fires with one owner already
// collected.
func TestHandleSessionProvenance_PartialWalkDiscarded(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	orig := provenanceTimeout
	provenanceTimeout = 20 * time.Millisecond
	t.Cleanup(func() { provenanceTimeout = orig })

	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 10, "sess-a", "/w/a")
	seedIdentityFrame(t, m, "%5", "codex", 110, "t110", 20, "sess-b", "/w/b")
	withProcessTree(t, map[int]int{100: 200, 110: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100", 110: "t110"})
	withSlowReads(t, 30*time.Millisecond)

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if body.Found {
		t.Fatalf("body = %+v, want found:false — the owners decided before the deadline are not an answer", body)
	}
}

// TestHandleSessionProvenance_DeadlineInsideOneWalk_FoundFalse is the same
// discipline as PartialWalkDiscarded, one level finer: here the session has a
// SINGLE frame, so there is no next frame whose ctx check could notice. The
// deadline expires partway up that one frame's ancestry, and the query must
// still answer found:false rather than finishing the chain and reporting the
// owner it found after time was up.
//
// The chain is long enough (100 → 200 → 300 → 400(pane) → 1) that the first
// read alone overruns a 20 ms deadline at 30 ms per read.
func TestHandleSessionProvenance_DeadlineInsideOneWalk_FoundFalse(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	orig := provenanceTimeout
	provenanceTimeout = 20 * time.Millisecond
	t.Cleanup(func() { provenanceTimeout = orig })

	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "400")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 300, 300: 400, 400: 1})
	withLivePids(t, map[int]string{100: "t100"})
	withSlowReads(t, 30*time.Millisecond)

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if body.Found {
		t.Fatalf("body = %+v, want found:false — the one walk ran past the deadline, so its owner is not an answer", body)
	}
}

// countingExecutor counts the pane→session lookups the pane enumeration makes,
// which is the only externally visible thing that enumeration does.
type countingExecutor struct {
	*tmux.FakeExecutor
	sessionIDCalls int
}

func (c *countingExecutor) PaneSessionID(target string) (string, error) {
	c.sessionIDCalls++
	return c.FakeExecutor.PaneSessionID(target)
}

// TestHandleSessionProvenance_ExpiredDeadline_SkipsPaneEnumeration — the
// deadline bounds the WHOLE query, and enumerating the session's panes is part
// of it: one tmux round trip per distinct pane with a frame, before a single
// process has been read. A request that is already out of time must not spend
// any of them.
//
// found:false is not enough to tell the two implementations apart — an expired
// deadline ends in found:false either way — so this asserts the work itself.
func TestHandleSessionProvenance_ExpiredDeadline_SkipsPaneEnumeration(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	counting := &countingExecutor{FakeExecutor: fake}
	m.tmux = counting
	orig := provenanceTimeout
	provenanceTimeout = -1 // already expired when the query starts
	t.Cleanup(func() { provenanceTimeout = orig })

	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	attachPane(fake, "%6", "$0", "210")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/a")
	seedIdentityFrame(t, m, "%6", "codex", 110, "t110", 43, "sess-2", "/w/b")
	withProcessTree(t, map[int]int{100: 200, 200: 1, 110: 210, 210: 1})
	withLivePids(t, map[int]string{100: "t100", 110: "t110"})

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if body.Found {
		t.Fatalf("body = %+v, want found:false", body)
	}
	if counting.sessionIDCalls != 0 {
		t.Fatalf("pane enumeration made %d tmux lookups after the deadline had expired, want 0", counting.sessionIDCalls)
	}
}

// TestHandleSessionProvenance_DefaultDeadlineIsFiveSeconds pins the number the
// spec names (§5.3), which the test above replaces with an expired one.
func TestHandleSessionProvenance_DefaultDeadlineIsFiveSeconds(t *testing.T) {
	if provenanceTimeout != 5*time.Second {
		t.Fatalf("provenanceTimeout = %v, want 5s", provenanceTimeout)
	}
}

// TestHandleSessionProvenance_MemoizesReadsAcrossPanes — one process read is
// four `ps` forks on darwin (spec §3.4), so the whole request shares ONE
// memoizing reader. Asserted positively: the exact set of PIDs, each read
// exactly once, INCLUDING the ancestor the two panes have in common — "at most
// once per PID" would also pass for a walk that never happened.
func TestHandleSessionProvenance_MemoizesReadsAcrossPanes(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%1", "$0", "200")
	attachPane(fake, "%2", "$0", "210")
	seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 10, "sess-a", "/w/a")
	seedIdentityFrame(t, m, "%2", "codex", 110, "t110", 20, "sess-b", "/w/b")
	// Both panes hang off the same tmux server process, 900.
	withProcessTree(t, map[int]int{100: 200, 200: 900, 110: 210, 210: 900, 900: 1})
	withLivePids(t, map[int]string{100: "t100", 110: "t110"})
	var seen []int
	withRecordedReads(t, &seen)

	_, body, _ := getProvenance(t, m, codeOf(t, "$0"))
	if !body.Found || body.SessionID != "sess-b" {
		t.Fatalf("body = %+v, want the newer root sess-b — the fixture must actually be walked", body)
	}

	counts := map[int]int{}
	for _, pid := range seen {
		counts[pid]++
	}
	want := []int{100, 110, 200, 210, 900}
	got := make([]int, 0, len(counts))
	for pid := range counts {
		got = append(got, pid)
	}
	sort.Ints(got)
	if len(got) != len(want) {
		t.Fatalf("read PIDs = %v (call order %v), want exactly %v", got, seen, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("read PIDs = %v (call order %v), want exactly %v", got, seen, want)
		}
	}
	for _, pid := range want {
		if counts[pid] != 1 {
			t.Fatalf("pid %d read %d times (call order %v), want exactly 1 — the shared ancestor included",
				pid, counts[pid], seen)
		}
	}
}
