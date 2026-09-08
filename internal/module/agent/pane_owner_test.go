package agent

import (
	"context"
	"fmt"
	"sort"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// ---------------------------------------------------------------------------
// Fixtures for resolvePaneOwners (Task 6)
//
// These reuse the process-tree / liveness seams declared in ancestor_test.go
// and add the two things the ownership query needs on top: a stubbed pane PID
// and a frame seeded with the identity columns Phase 1 added.
// ---------------------------------------------------------------------------

// withPanePID makes resolvePanePIDFn answer `pid` for every pane. The real
// implementation shells out through tmux; the query only cares about the
// number.
func withPanePID(t *testing.T, pid int) {
	t.Helper()
	orig := resolvePanePIDFn
	resolvePanePIDFn = func(tmux.Executor, string) (int, error) { return pid, nil }
	t.Cleanup(func() { resolvePanePIDFn = orig })
}

// withPanePIDError makes resolvePanePIDFn fail, i.e. the pane's current
// process cannot be resolved at all.
func withPanePIDError(t *testing.T) {
	t.Helper()
	orig := resolvePanePIDFn
	resolvePanePIDFn = func(tmux.Executor, string) (int, error) {
		return 0, fmt.Errorf("pane pid unresolvable: forced test error")
	}
	t.Cleanup(func() { resolvePanePIDFn = orig })
}

// seedIdentityFrame seeds a frame that has already reported its own session id
// and cwd — the state a pane reaches after Phase 1's identity write.
func seedIdentityFrame(t *testing.T, m *Module, paneID, agentType string, pid int, startTime string, lastSeenAt int64, sessionID, cwd string) store.Frame {
	t.Helper()
	f, err := m.frames.Upsert(store.Frame{
		PaneID:           paneID,
		AgentType:        agentType,
		PID:              pid,
		PPID:             1,
		ProcessStartTime: startTime,
		Status:           agentpkg.StatusIdle,
		StartedAt:        lastSeenAt,
		LastSeenAt:       lastSeenAt,
		Verified:         true,
		SessionID:        sessionID,
		Cwd:              cwd,
	})
	if err != nil {
		t.Fatalf("seed identity frame %s pid=%d: %v", agentType, pid, err)
	}
	return f
}

// recordingReader wraps the package-level reader (whatever the test's
// withProcessTree / withProcessReadError left in place) and records the PIDs
// it is asked for, in order.
func recordingReader(seen *[]int) procReader {
	return func(pid int) (agentpkg.ProcessInfo, error) {
		*seen = append(*seen, pid)
		return readProcessInfoFn(pid)
	}
}

func ownerFrameIDs(owners []PaneOwner) []string {
	ids := make([]string, 0, len(owners))
	for _, o := range owners {
		ids = append(ids, o.FrameID)
	}
	sort.Strings(ids)
	return ids
}

// ---------------------------------------------------------------------------
// The kept case, and the shape of what is returned
// ---------------------------------------------------------------------------

// TestResolvePaneOwners_LiveRootWithSessionID_Returned is the whole point of
// the query: a live agent whose chain reaches PID 1 and passes through the
// pane's own process is a root, and its recorded identity comes back with it.
func TestResolvePaneOwners_LiveRootWithSessionID_Returned(t *testing.T) {
	m := newProxyTestModule(t)
	frame := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withPanePID(t, 200)
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 {
		t.Fatalf("owners = %+v, want exactly one", owners)
	}
	got := owners[0]
	want := PaneOwner{
		FrameID:    frame.FrameID,
		AgentType:  "cc",
		SessionID:  "sess-1",
		Cwd:        "/w/purdex",
		TmuxPaneID: "%5",
		LastSeenAt: 42,
	}
	if got != want {
		t.Fatalf("owner = %+v, want %+v", got, want)
	}
}

// TestResolvePaneOwners_RootWithEmptySessionID_StillReturned keeps the
// layering explicit: this function answers "who are the pane's root agents",
// not "who can be resumed". Dropping identity-less roots is the handler's job
// (Task 7), and doing it here would hide a root that the tie-break needs to
// know about.
func TestResolvePaneOwners_RootWithEmptySessionID_StillReturned(t *testing.T) {
	m := newProxyTestModule(t)
	frame := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "", "")
	withPanePID(t, 200)
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 || owners[0].FrameID != frame.FrameID {
		t.Fatalf("owners = %+v, want the identity-less root", owners)
	}
	if owners[0].SessionID != "" {
		t.Fatalf("session id = %q, want empty", owners[0].SessionID)
	}
}

// TestResolvePaneOwners_FramePIDEqualsPanePID_InsideTree covers the first of
// the two places SawPanePID is set. The walk enters one level ABOVE the frame,
// so it can never observe the frame's own PID; without the pre-walk check an
// agent that is itself the pane's current process would be reported as outside
// its own pane. PidAncestorIncludes counts pid == ancestor as inside the tree
// and so must this.
func TestResolvePaneOwners_FramePIDEqualsPanePID_InsideTree(t *testing.T) {
	m := newProxyTestModule(t)
	frame := seedIdentityFrame(t, m, "%5", "cc", 200, "t200", 7, "sess-self", "/w")
	withPanePID(t, 200)
	// 200 → 1 directly: the loop never sees 200, only PID 1.
	withProcessTree(t, map[int]int{200: 1})
	withLivePids(t, map[int]string{200: "t200"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 || owners[0].FrameID != frame.FrameID {
		t.Fatalf("owners = %+v, want the frame whose PID is the pane PID", owners)
	}
}

// ---------------------------------------------------------------------------
// Boundary matrix row 1 — the walk reached PID 1, but the pane was never seen
// ---------------------------------------------------------------------------

// TestResolvePaneOwners_ReusedPaneID_SurvivingOldAgent_Excluded is the round-2
// Blocker case. tmux reused the pane id for a new shell, the previous
// generation's agent is still running, and the sweep has not yet removed its
// row: alive-plus-start-time alone would hand that agent back as the pane's
// owner. The pane-tree half of the walk is what refuses it.
func TestResolvePaneOwners_ReusedPaneID_SurvivingOldAgent_Excluded(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-old", "/w/old")
	withPanePID(t, 900) // the pane's CURRENT shell, a different generation
	// The old agent still hangs off the old shell at 200.
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners = %+v, want none — the frame is not in the pane's current tree", owners)
	}
}

// TestResolvePaneOwners_RootWithoutPanePIDOnChain_Excluded states the same
// rule without the pane-reuse story: a completed walk is necessary but never
// sufficient. No pane, no keep.
func TestResolvePaneOwners_RootWithoutPanePIDOnChain_Excluded(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withPanePID(t, 900)
	withProcessTree(t, map[int]int{100: 300, 300: 400, 400: 1})
	withLivePids(t, map[int]string{100: "t100"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners = %+v, want none", owners)
	}
}

// ---------------------------------------------------------------------------
// Boundary matrix row 2 — another surviving frame of this pane is above me
// ---------------------------------------------------------------------------

// TestResolvePaneOwners_NestedSameType_OnlyParentIsRoot — VerdictSameTypeAbove.
func TestResolvePaneOwners_NestedSameType_OnlyParentIsRoot(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedIdentityFrame(t, m, "%5", "cc", 200, "t200", 10, "sess-parent", "/w")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 20, "sess-child", "/w")
	withPanePID(t, 300)
	withProcessTree(t, map[int]int{100: 200, 200: 300, 300: 1})
	withLivePids(t, map[int]string{100: "t100", 200: "t200"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 || owners[0].FrameID != parent.FrameID {
		t.Fatalf("owners = %+v, want only the parent %s", owners, parent.FrameID)
	}
}

// TestResolvePaneOwners_CrossTypeChildFrame_OnlyParentIsRoot —
// VerdictProxyParent, the other half of row 2.
func TestResolvePaneOwners_CrossTypeChildFrame_OnlyParentIsRoot(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedIdentityFrame(t, m, "%5", "cc", 200, "t200", 10, "sess-parent", "/w")
	seedIdentityFrame(t, m, "%5", "codex", 100, "t100", 20, "sess-child", "/w")
	withPanePID(t, 300)
	withProcessTree(t, map[int]int{100: 200, 200: 300, 300: 1})
	withLivePids(t, map[int]string{100: "t100", 200: "t200"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 || owners[0].FrameID != parent.FrameID {
		t.Fatalf("owners = %+v, want only the cross-type parent %s", owners, parent.FrameID)
	}
}

// TestResolvePaneOwners_ProxyCollapsedChildHasNoFrame_ParentIsRoot is the
// shape the proxy collapse actually leaves behind: the inner agent never got a
// frame of its own, so the pane's single frame is the parent and it is the
// root.
func TestResolvePaneOwners_ProxyCollapsedChildHasNoFrame_ParentIsRoot(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedIdentityFrame(t, m, "%5", "cc", 200, "t200", 10, "sess-parent", "/w")
	withPanePID(t, 300)
	// 100 is the collapsed child process; it has no frame row at all.
	withProcessTree(t, map[int]int{100: 200, 200: 300, 300: 1})
	withLivePids(t, map[int]string{100: "t100", 200: "t200"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 || owners[0].FrameID != parent.FrameID {
		t.Fatalf("owners = %+v, want the parent %s", owners, parent.FrameID)
	}
}

// TestResolvePaneOwners_StaleFrameDoesNotShadowLiveRoot — a frame whose PID
// was reused fails the identity check both in the survivor filter and inside
// the walk, so it neither appears as an owner nor stops the live agent below
// it from being recognised as the root.
func TestResolvePaneOwners_StaleFrameDoesNotShadowLiveRoot(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 200, "t200-old", 5, "sess-stale", "/w")
	live := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 20, "sess-live", "/w")
	withPanePID(t, 300)
	withProcessTree(t, map[int]int{100: 200, 200: 300, 300: 1})
	// 200 is alive but reports a DIFFERENT start time than the row stored.
	withLivePids(t, map[int]string{100: "t100", 200: "t200-new"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 || owners[0].FrameID != live.FrameID {
		t.Fatalf("owners = %+v, want only the live root %s", owners, live.FrameID)
	}
}

// ---------------------------------------------------------------------------
// Boundary matrix rows 3-6 — every incomplete walk excludes, never promotes
// ---------------------------------------------------------------------------

// TestResolvePaneOwners_ChainLongerThanDepthCap_Excluded — row 3.
func TestResolvePaneOwners_ChainLongerThanDepthCap_Excluded(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withPanePID(t, 900)
	chain := map[int]int{100: 200}
	pid := 200
	for i := 0; i < proxyMaxDepth+3; i++ {
		chain[pid] = pid + 1
		pid++
	}
	withProcessTree(t, chain)
	withLivePids(t, map[int]string{100: "t100"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners = %+v, want none — the walk never completed", owners)
	}
}

// TestResolvePaneOwners_PanePIDSeenButCapExhausted_Excluded is the rule v2 of
// the plan got wrong. SawPanePID is observational: it proves membership and
// nothing whatever about whether a framed ancestor sits further up, so it can
// never rescue a walk that did not finish.
func TestResolvePaneOwners_PanePIDSeenButCapExhausted_Excluded(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withPanePID(t, 200) // hit at the very first iteration
	chain := map[int]int{100: 200}
	pid := 200
	for i := 0; i < proxyMaxDepth+3; i++ {
		chain[pid] = pid + 100
		pid += 100
	}
	withProcessTree(t, chain)
	withLivePids(t, map[int]string{100: "t100"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners = %+v, want none — SawPanePID must not rescue an incomplete walk", owners)
	}
}

// TestResolvePaneOwners_UnreadableProcessMidWalk_ExcludesOnlyThatFrame —
// row 4. One frame's chain becomes unreadable; the other frame is untouched.
func TestResolvePaneOwners_UnreadableProcessMidWalk_ExcludesOnlyThatFrame(t *testing.T) {
	m := newProxyTestModule(t)
	good := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 10, "sess-good", "/w")
	seedIdentityFrame(t, m, "%5", "codex", 500, "t500", 20, "sess-bad", "/w")
	withPanePID(t, 900)
	withLivePids(t, map[int]string{100: "t100", 500: "t500"})

	tree := map[int]int{100: 200, 200: 900, 900: 1, 500: 600}
	orig := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 600 {
			return agentpkg.ProcessInfo{}, fmt.Errorf("read process info 600: forced test error")
		}
		ppid, ok := tree[pid]
		if !ok {
			ppid = 1
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = orig })

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 || owners[0].FrameID != good.FrameID {
		t.Fatalf("owners = %+v, want only %s", owners, good.FrameID)
	}
}

// TestResolvePaneOwners_SelfParentGuard_Excluded — row 5.
func TestResolvePaneOwners_SelfParentGuard_Excluded(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withPanePID(t, 900)
	withProcessTree(t, map[int]int{100: 300, 300: 300})
	withLivePids(t, map[int]string{100: "t100"})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners = %+v, want none — the self-parent guard aborted the walk", owners)
	}
}

// TestResolvePaneOwners_CandidateIdentityUnverifiable_Excluded — row 6. The
// candidate frame's start time is transiently unreadable, so the walk cannot
// tell whether it is a real ancestor. Refuse rather than infer.
func TestResolvePaneOwners_CandidateIdentityUnverifiable_Excluded(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 200, "t200", 10, "sess-parent", "/w")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 20, "sess-child", "/w")
	withPanePID(t, 300)
	withProcessTree(t, map[int]int{100: 200, 200: 300, 300: 1})

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(pid int) bool { return pid == 100 || pid == 200 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "", fmt.Errorf("start time for %d: forced test error", pid)
	}
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	owners, err := m.resolvePaneOwners(context.Background(), "%5", readProcessInfoFn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners = %+v, want none — neither frame survives an unverifiable identity", owners)
	}
}

// ---------------------------------------------------------------------------
// The depth budget, exactly
// ---------------------------------------------------------------------------

// TestResolvePaneOwners_DeepestCompletingWalk_KeptWithExactReadSequence pins
// the deepest chain that can still finish, and the exact reads it costs.
//
// The root check lives at the TOP of an iteration (ancestor.go), and a loop
// that runs out of iterations falls through to Indeterminate. So the last read
// whose result can still be observed is the one taken at depth
// proxyMaxDepth-2; a PPID of 1 produced by the read at proxyMaxDepth-1 has no
// further iteration to see it. PID 1 is itself never read.
//
// If this test starts failing, do NOT move or add a root check outside the
// loop to make a deeper fixture pass: that would change classifyAncestor's
// behaviour, which Task 5 froze.
func TestResolvePaneOwners_DeepestCompletingWalk_KeptWithExactReadSequence(t *testing.T) {
	if proxyMaxDepth != 5 {
		t.Fatalf("proxyMaxDepth = %d; this fixture is written for 5", proxyMaxDepth)
	}
	m := newProxyTestModule(t)
	frame := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-deep", "/w")
	withPanePID(t, 200)
	withProcessTree(t, map[int]int{100: 200, 200: 300, 300: 400, 400: 500, 500: 1})
	withLivePids(t, map[int]string{100: "t100"})

	var seen []int
	owners, err := m.resolvePaneOwners(context.Background(), "%5", recordingReader(&seen))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(owners) != 1 || owners[0].FrameID != frame.FrameID {
		t.Fatalf("owners = %+v, want the deepest still-completing root", owners)
	}
	want := []int{100, 200, 300, 400, 500}
	if len(seen) != len(want) {
		t.Fatalf("reads = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("read sequence = %v, want %v", seen, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Pane resolution, memoization, cancellation
// ---------------------------------------------------------------------------

// TestResolvePaneOwners_PanePIDUnresolvable_EmptyResultNoError — a pane whose
// current process cannot be resolved contributes nothing (spec §5.3 step 2).
// It is not an error: the other panes of the session still have answers.
func TestResolvePaneOwners_PanePIDUnresolvable_EmptyResultNoError(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withPanePIDError(t)
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	var seen []int
	owners, err := m.resolvePaneOwners(context.Background(), "%5", recordingReader(&seen))
	if err != nil {
		t.Fatalf("err = %v, want nil — an unresolvable pane is not an error", err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners = %+v, want none", owners)
	}
	if len(seen) != 0 {
		t.Fatalf("reads = %v, want none — there is nothing to walk against", seen)
	}
}

// TestResolvePaneOwners_MemoizesEveryProcessRead asserts the cost contract
// positively. "At most once per PID" would also pass for an implementation
// that never walked at all, so this fixes one topology and asserts all four
// things at once: the owners are right; the SET of PIDs read is exactly the
// expected one; every one of them was read exactly once, including the ancestor
// two frames share; and a PID whose read FAILED is not retried inside the same
// request.
//
//	100 ┐              500 ┐
//	    ├→ 300 → 400(pane) → 1        600 (read fails)
//	200 ┘              700 ┘
func TestResolvePaneOwners_MemoizesEveryProcessRead(t *testing.T) {
	m := newProxyTestModule(t)
	a := seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 10, "sess-a", "/w")
	b := seedIdentityFrame(t, m, "%5", "codex", 200, "t200", 20, "sess-b", "/w")
	seedIdentityFrame(t, m, "%5", "cc", 500, "t500", 30, "sess-c", "/w")
	seedIdentityFrame(t, m, "%5", "codex", 700, "t700", 40, "sess-d", "/w")
	withPanePID(t, 400)
	withLivePids(t, map[int]string{100: "t100", 200: "t200", 500: "t500", 700: "t700"})

	tree := map[int]int{100: 300, 200: 300, 300: 400, 400: 1, 500: 600, 700: 600}
	counts := map[int]int{}
	base := func(pid int) (agentpkg.ProcessInfo, error) {
		counts[pid]++
		if pid == 600 {
			return agentpkg.ProcessInfo{}, fmt.Errorf("read process info 600: forced test error")
		}
		ppid, ok := tree[pid]
		if !ok {
			ppid = 1
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
	}

	owners, err := m.resolvePaneOwners(context.Background(), "%5", newMemoProcReader(base))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	gotIDs := ownerFrameIDs(owners)
	wantIDs := []string{a.FrameID, b.FrameID}
	sort.Strings(wantIDs)
	if len(gotIDs) != len(wantIDs) || gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Fatalf("owners = %+v, want exactly the two roots %v", owners, wantIDs)
	}

	// 300 is shared by two walks and 600 by two more; both must be read once.
	want := map[int]int{100: 1, 200: 1, 300: 1, 400: 1, 500: 1, 600: 1, 700: 1}
	if len(counts) != len(want) {
		t.Fatalf("read PIDs = %v, want exactly %v", counts, want)
	}
	for pid, n := range want {
		if counts[pid] != n {
			t.Fatalf("pid %d read %d time(s), want %d; all reads = %v", pid, counts[pid], n, counts)
		}
	}
}

// TestResolvePaneOwners_CancelledContext_StopsBetweenReads — the 5 s deadline
// cannot interrupt a single `ps`, but it must stop the query from starting
// another walk.
func TestResolvePaneOwners_CancelledContext_StopsBetweenReads(t *testing.T) {
	t.Run("cancelled_before_the_first_read", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 10, "sess-a", "/w")
		withPanePID(t, 200)
		withProcessTree(t, map[int]int{100: 200, 200: 1})
		withLivePids(t, map[int]string{100: "t100"})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var seen []int
		owners, err := m.resolvePaneOwners(ctx, "%5", recordingReader(&seen))
		if err == nil {
			t.Fatalf("err = nil, want the context error; owners = %+v", owners)
		}
		if len(seen) != 0 {
			t.Fatalf("reads = %v, want none after cancellation", seen)
		}
	})

	t.Run("cancelled_partway_through", func(t *testing.T) {
		m := newProxyTestModule(t)
		seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 10, "sess-a", "/w")
		seedIdentityFrame(t, m, "%5", "codex", 500, "t500", 20, "sess-b", "/w")
		withPanePID(t, 200)
		withProcessTree(t, map[int]int{100: 200, 200: 1, 500: 200})
		withLivePids(t, map[int]string{100: "t100", 500: "t500"})

		ctx, cancel := context.WithCancel(context.Background())
		var seen []int
		read := func(pid int) (agentpkg.ProcessInfo, error) {
			seen = append(seen, pid)
			// The first frame's own read cancels the request.
			cancel()
			return readProcessInfoFn(pid)
		}

		_, err := m.resolvePaneOwners(ctx, "%5", read)
		if err == nil {
			t.Fatal("err = nil, want the context error")
		}
		// Frames are listed started_at ASC, so pid 100 is walked first and
		// pid 500 must never be reached.
		for _, pid := range seen {
			if pid == 500 {
				t.Fatalf("reads = %v; the second frame must not be walked after cancellation", seen)
			}
		}
	})
}

// TestResolvePaneOwners_CancelledInsideOneWalk_IsNotAnAnswer — the deadline has
// to bite INSIDE one frame's ancestry, not only between frames.
//
// Spec §5.3 puts the contract at "checked between process reads", and one
// frame's chain is up to proxyMaxDepth reads of four `ps` forks each (§3.4), so
// a query can burn its whole budget without ever reaching a second frame. The
// test above happens to be caught by the next frame's check; this fixture has
// no next frame, which is exactly the case that used to slip through: the walk
// ran to the end, the owner was collected, and the query reported success on a
// walk that had already outrun its deadline.
//
// The cancellation lands one link into a four-link chain, so three reads are
// still ahead of it and there is no later frame to notice.
func TestResolvePaneOwners_CancelledInsideOneWalk_IsNotAnAnswer(t *testing.T) {
	m := newProxyTestModule(t)
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 10, "sess-a", "/w")
	withPanePID(t, 400)
	withProcessTree(t, map[int]int{100: 200, 200: 300, 300: 400, 400: 1})
	withLivePids(t, map[int]string{100: "t100"})

	ctx, cancel := context.WithCancel(context.Background())
	var seen []int
	read := func(pid int) (agentpkg.ProcessInfo, error) {
		seen = append(seen, pid)
		if pid == 200 {
			// One link up the chain; 300 and 400 are still to come, and the
			// pane's own PID has not been seen yet.
			cancel()
		}
		return readProcessInfoFn(pid)
	}

	owners, err := m.resolvePaneOwners(ctx, "%5", read)
	if err == nil {
		t.Fatalf("err = nil, want the context error; owners = %+v — a walk that outran the deadline is not evidence", owners)
	}
	for _, pid := range seen {
		if pid == 300 || pid == 400 {
			t.Fatalf("reads = %v; the walk must stop at the first read after cancellation, not finish the chain", seen)
		}
	}
}
