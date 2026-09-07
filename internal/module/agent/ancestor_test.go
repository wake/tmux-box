package agent

import (
	"fmt"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// ---------------------------------------------------------------------------
// Process-tree fixtures
//
// These swap the same package-level seams the proxy tests in frame_ops_test.go
// swap by hand (readProcessInfoFn / isPidAliveFn / processStartTimeFn) and
// restore them via t.Cleanup, so a test can describe a PPID chain and a
// liveness set declaratively.
// ---------------------------------------------------------------------------

// withProcessTree makes readProcessInfoFn resolve PPIDs from tree. A PID with
// no entry reports PPID 1, i.e. the walk reaches the root on the next hop.
func withProcessTree(t *testing.T, tree map[int]int) {
	t.Helper()
	orig := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		ppid, ok := tree[pid]
		if !ok {
			ppid = 1
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = orig })
}

// withLivePids declares the set of PIDs that are alive and the process start
// time each one reports. A PID outside the map is dead — matching production,
// where processStartTimeFn is only ever consulted after isPidAliveFn passes.
func withLivePids(t *testing.T, live map[int]string) {
	t.Helper()
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(pid int) bool {
		_, ok := live[pid]
		return ok
	}
	processStartTimeFn = func(pid int) (string, error) {
		start, ok := live[pid]
		if !ok {
			return "", fmt.Errorf("pid %d is not alive", pid)
		}
		return start, nil
	}
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})
}

// withProcessReadError makes readProcessInfoFn fail for the given PIDs; every
// other PID reports PPID 1.
func withProcessReadError(t *testing.T, pids ...int) {
	t.Helper()
	failing := make(map[int]bool, len(pids))
	for _, pid := range pids {
		failing[pid] = true
	}
	orig := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if failing[pid] {
			return agentpkg.ProcessInfo{}, fmt.Errorf("read process info %d: forced test error", pid)
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = orig })
}

// withProcessTreeSequence makes readProcessInfoFn report a different PPID for
// `pid` on each successive call, so a test can describe an ancestor that only
// becomes visible partway through applyFrameEvent — the pre-walk misses, the
// post-Upsert reconcile hits. Calls past the end of the sequence keep
// reporting the last entry. Every other PID reports PPID 1, and only calls for
// `pid` advance the sequence, mirroring the hand-rolled counter in
// TestPhase35_IT3_PreWalkMiss_PostReconcileHit.
func withProcessTreeSequence(t *testing.T, pid int, ppids []int) {
	t.Helper()
	if len(ppids) == 0 {
		t.Fatalf("withProcessTreeSequence: empty ppid sequence")
	}
	orig := readProcessInfoFn
	calls := 0
	readProcessInfoFn = func(queried int) (agentpkg.ProcessInfo, error) {
		if queried != pid {
			return agentpkg.ProcessInfo{PID: queried, PPID: 1}, nil
		}
		idx := calls
		if idx >= len(ppids) {
			idx = len(ppids) - 1
		}
		calls++
		return agentpkg.ProcessInfo{PID: pid, PPID: ppids[idx]}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = orig })
}

// ---------------------------------------------------------------------------
// classifyAncestor — the four verdicts (spec §4.3)
// ---------------------------------------------------------------------------

func TestClassifyAncestor_NoFramedAncestor_Root(t *testing.T) {
	m := newProxyTestModule(t)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 999}) // 999 has no frame

	verdict, parent, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictRoot {
		t.Fatalf("verdict = %v, want VerdictRoot", verdict)
	}
	if parent != nil {
		t.Fatalf("parent = %v, want nil", parent)
	}
}

func TestClassifyAncestor_LiveSameTypeAncestor_SameTypeAbove(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100"})

	verdict, _, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictSameTypeAbove {
		t.Fatalf("verdict = %v, want VerdictSameTypeAbove", verdict)
	}
}

func TestClassifyAncestor_LiveCrossTypeAncestor_ProxyParent(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 100})
	withLivePids(t, map[int]string{100: "t100"})

	verdict, got, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictProxyParent {
		t.Fatalf("verdict = %v, want VerdictProxyParent", verdict)
	}
	if got == nil || got.FrameID != parent.FrameID {
		t.Fatalf("parent = %v, want %v", got, parent.FrameID)
	}
}

func TestClassifyAncestor_StaleSameTypeBelowLiveCrossType_ProxyParent(t *testing.T) {
	// Regression guard mirroring frame_ops_test.go:1470 — a dead same-type
	// frame must not hard-stop the walk to a live cross-type grandparent.
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "codex", 150, "t150", 5) // stale: pid not alive
	grand := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 150, 150: 100})
	withLivePids(t, map[int]string{100: "t100"}) // 150 absent = dead

	verdict, got, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictProxyParent || got == nil || got.FrameID != grand.FrameID {
		t.Fatalf("verdict = %v parent = %v, want ProxyParent/%s", verdict, got, grand.FrameID)
	}
}

func TestClassifyAncestor_SelfParent_Indeterminate(t *testing.T) {
	// A process whose PPID is itself must not spin to the depth cap.
	m := newProxyTestModule(t)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessTree(t, map[int]int{200: 300, 300: 300})

	verdict, _, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictIndeterminate {
		t.Fatalf("verdict = %v, want VerdictIndeterminate", verdict)
	}
}

func TestClassifyAncestor_DepthCapExceeded_Indeterminate(t *testing.T) {
	m := newProxyTestModule(t)
	chain := map[int]int{}
	pid := 200
	for i := 0; i < proxyMaxDepth+3; i++ {
		chain[pid] = pid + 1
		pid++
	}
	withProcessTree(t, chain)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}

	verdict, _, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictIndeterminate {
		t.Fatalf("verdict = %v, want VerdictIndeterminate", verdict)
	}
}

func TestClassifyAncestor_ProcessReadError_Indeterminate(t *testing.T) {
	m := newProxyTestModule(t)
	req := EventRequest{TmuxPaneID: "%5", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	withProcessReadError(t, 200)

	verdict, _, err := m.classifyAncestor(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if verdict != VerdictIndeterminate {
		t.Fatalf("verdict = %v, want VerdictIndeterminate", verdict)
	}
}
