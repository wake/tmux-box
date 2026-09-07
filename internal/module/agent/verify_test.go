package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func newVerifyTestModule(t *testing.T) *Module {
	t.Helper()
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { _ = events.Close() })
	m, err := New(events)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.registry = agentpkg.NewRegistry()
	m.tmux = tmux.NewFakeExecutor()
	if m.traceSink != nil {
		t.Cleanup(func() { m.traceSink.Close() })
	}
	return m
}

func stubVerifySeams(t *testing.T) {
	t.Helper()
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origAncestors := pidAncestorIncludesFn
	origRead := readProcessInfoFn
	origResolvePane := resolvePanePIDFn
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		pidAncestorIncludesFn = origAncestors
		readProcessInfoFn = origRead
		resolvePanePIDFn = origResolvePane
	})
}

func TestVerify_AcceptsPaneNativeHook(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		identify: func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/usr/local/bin/claude" },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return pid == 200 }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return pid == 200 && ancestor == 100 }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: 200, PPID: 100, ExePath: "/usr/local/bin/claude", Argv: []string{"claude"}}, nil
	}

	decision := m.verifyEvent(EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		AgentType:       "cc",
		PurdexName:      "Stop",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if !decision.Accepted {
		t.Fatalf("verify should accept native pane hook, got %+v", decision)
	}
}

func TestVerify_RejectsDetachedRuntime(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "codex",
		identify: func(agentpkg.ProcessInfo) bool { return true },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return pid == 999 }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return false }

	decision := m.verifyEvent(EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		AgentType:       "codex",
		PurdexName:      "Stop",
		SenderPID:       999,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "pid_not_in_pane_tree" {
		t.Fatalf("reason = %q, want pid_not_in_pane_tree", decision.Reason)
	}
}

func TestVerify_AcceptsNestedAgent(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "codex",
		identify: func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/opt/homebrew/bin/codex" },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return pid == 300 }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return pid == 300 && ancestor == 100 }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: 300, PPID: 200, ExePath: "/opt/homebrew/bin/codex", Argv: []string{"codex"}}, nil
	}

	decision := m.verifyEvent(EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		AgentType:       "codex",
		PurdexName:      "Stop",
		SenderPID:       300,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if !decision.Accepted {
		t.Fatalf("verify should accept nested agent, got %+v", decision)
	}
}

func TestVerify_RejectsDeadPid(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return false }

	decision := m.verifyEvent(EventRequest{SenderPID: 404})
	if decision.Reason != "pid_dead" {
		t.Fatalf("reason = %q, want pid_dead", decision.Reason)
	}
}

func TestVerify_RejectsPidReuse(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 02:00:00 2026", nil }

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%5",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "pid_reused" {
		t.Fatalf("reason = %q, want pid_reused", decision.Reason)
	}
}

func TestVerify_RejectsIdentifyMismatch(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		identify: func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/usr/local/bin/claude" },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return true }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: 200, PPID: 100, ExePath: "/opt/homebrew/bin/codex", Argv: []string{"codex"}}, nil
	}

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%5",
		AgentType:       "cc",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "identify_mismatch" {
		t.Fatalf("reason = %q, want identify_mismatch", decision.Reason)
	}
}

func TestVerify_RejectsPaneUnresolvable(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%missing",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "pane_unresolvable" {
		t.Fatalf("reason = %q, want pane_unresolvable", decision.Reason)
	}
}

func TestVerify_RejectsUncertainSender(t *testing.T) {
	m := newVerifyTestModule(t)

	decision := m.verifyEvent(EventRequest{
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
		SenderUncertain: true,
	})
	if decision.Reason != "sender_uncertain" {
		t.Fatalf("reason = %q, want sender_uncertain", decision.Reason)
	}
}

func TestVerify_RejectsWhenStartTimeLookupFails(t *testing.T) {
	m := newVerifyTestModule(t)
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "", errStub("ps failed") }

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%5",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "start_time_unavailable" {
		t.Fatalf("reason = %q, want start_time_unavailable", decision.Reason)
	}
}

func TestVerify_RejectsWhenProcessLookupFails(t *testing.T) {
	m := newVerifyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		identify: func(agentpkg.ProcessInfo) bool { return true },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return true }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{}, errStub("lookup failed")
	}

	decision := m.verifyEvent(EventRequest{
		TmuxPaneID:      "%5",
		AgentType:       "cc",
		SenderPID:       200,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if decision.Reason != "process_lookup_failed" {
		t.Fatalf("reason = %q, want process_lookup_failed", decision.Reason)
	}
}

// TestResolvePanePID_UsesActivePanePID is a regression guard for PR #977 codex
// review round 1 P2, and for the same tmux behaviour PR #638 already fixed one
// rung up the stack (probe/liveness.go:36-42).
//
// `tmux list-panes -t %5` — what RealExecutor.PanePID runs — resolves a pane id
// target to its CONTAINING WINDOW and lists that window's panes, so taking the
// first row yields the first sibling's PID, not %5's. `tmux display-message -p
// -t %5 '#{pane_pid}'` — ActivePanePID — honours a pane id target exactly.
//
// The fake stands in for that: PanePID answers with the window's first pane
// (100) and ActivePanePID with pane %5's own process (300).
func TestResolvePanePID_UsesActivePanePID(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.SetPanePID("%5", "100")       // list-panes: the window's FIRST pane
	fake.SetActivePanePID("%5", "300") // display-message: pane %5 itself

	pid, err := resolvePanePID(fake, "%5")
	if err != nil {
		t.Fatalf("resolvePanePID: %v", err)
	}
	if pid != 300 {
		t.Fatalf("pane pid = %d, want 300 (pane %%5's own process); 100 is the window's first pane, i.e. a regression to PanePID", pid)
	}
}

// TestVerify_AcceptsHookFromNonFirstPaneOfWindow is the same bug seen from the
// hook path, which is where it actually hurt: an agent in the second pane of a
// split window sends a legitimate event, verify resolves the FIRST pane's PID,
// the ancestor check runs against a process that is not on the sender's chain,
// and the event is rejected as pid_not_in_pane_tree. That pane then never gets
// a frame — and with no frame it can have no provenance owner either.
func TestVerify_AcceptsHookFromNonFirstPaneOfWindow(t *testing.T) {
	m := newVerifyTestModule(t)
	fake := m.tmux.(*tmux.FakeExecutor)
	fake.SetPanePID("%6", "100")       // sibling pane %5's shell — the wrong answer
	fake.SetActivePanePID("%6", "300") // pane %6's own shell — the right one
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		identify: func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/usr/local/bin/claude" },
	})
	stubVerifySeams(t)
	isPidAliveFn = func(pid int) bool { return pid == 400 }
	processStartTimeFn = func(pid int) (string, error) { return "Sun Apr 20 01:30:00 2026", nil }
	// 400 lives under pane %6's shell (300), nowhere near the sibling's (100).
	pidAncestorIncludesFn = func(pid int, ancestor int) bool { return pid == 400 && ancestor == 300 }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: 400, PPID: 300, ExePath: "/usr/local/bin/claude", Argv: []string{"claude"}}, nil
	}

	decision := m.verifyEvent(EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%6",
		AgentType:       "cc",
		PurdexName:      "Stop",
		SenderPID:       400,
		SenderStartTime: "Sun Apr 20 01:30:00 2026",
	})
	if !decision.Accepted {
		t.Fatalf("verify rejected a hook from a non-first pane: %+v — resolving the pane through PanePID hands back the window's first pane (100), so the sender's tree check fails", decision)
	}
}
