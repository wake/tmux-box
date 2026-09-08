package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/tmux"
)

type verifyDecision struct {
	Accepted bool
	Reason   string
}

var (
	verifyEventFn         = defaultVerifyEvent
	isPidAliveFn          = probe.IsPidAlive
	processStartTimeFn    = probe.ProcessStartTime
	pidAncestorIncludesFn = probe.PidAncestorIncludes
	readProcessInfoFn     = agentpkg.ReadProcessInfo
	resolvePanePIDFn      = resolvePanePID
)

func defaultVerifyEvent(m *Module, req EventRequest) (decision verifyDecision) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[agent][verify] warning: panic while verifying pid=%d pane=%s: %v", req.SenderPID, req.TmuxPaneID, recovered)
			decision = verifyDecision{Reason: "verify_panic"}
		}
	}()
	return m.verifyEvent(req)
}

func (m *Module) verifyEvent(req EventRequest) verifyDecision {
	if req.SenderUncertain {
		return verifyDecision{Reason: "sender_uncertain"}
	}
	if !isPidAliveFn(req.SenderPID) {
		return verifyDecision{Reason: "pid_dead"}
	}
	actualStart, err := processStartTimeFn(req.SenderPID)
	if err != nil {
		log.Printf("[agent][verify] warning: start_time lookup failed for pid=%d: %v", req.SenderPID, err)
		return verifyDecision{Reason: "start_time_unavailable"}
	}
	if strings.TrimSpace(actualStart) != strings.TrimSpace(req.SenderStartTime) {
		return verifyDecision{Reason: "pid_reused"}
	}
	if m.tmux == nil {
		log.Printf("[agent][verify] warning: tmux executor unavailable for pid=%d pane=%s", req.SenderPID, req.TmuxPaneID)
		return verifyDecision{Reason: "tmux_unavailable"}
	}
	panePID, err := resolvePanePIDFn(m.tmux, req.TmuxPaneID)
	if err != nil {
		return verifyDecision{Reason: "pane_unresolvable"}
	}
	if !pidAncestorIncludesFn(req.SenderPID, panePID) {
		return verifyDecision{Reason: "pid_not_in_pane_tree"}
	}
	provider, ok := m.registry.Get(req.AgentType)
	if !ok {
		return verifyDecision{Reason: "identify_mismatch"}
	}
	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		log.Printf("[agent][verify] warning: process lookup failed for pid=%d: %v", req.SenderPID, err)
		return verifyDecision{Reason: "process_lookup_failed"}
	}
	if !provider.Identify(info) {
		return verifyDecision{Reason: "identify_mismatch"}
	}
	return verifyDecision{Accepted: true}
}

// resolvePanePID returns the PID of the pane's own current process.
//
// ActivePanePID, never PanePID. PanePID runs `tmux list-panes -t <target>` and
// takes the first row; tmux resolves a pane id target such as `%5` to the
// WINDOW that contains it, so for any pane that is not its window's first the
// answer is a sibling's PID. ActivePanePID runs `tmux display-message -p -t
// <target> '#{pane_pid}'`, which honours a pane id target exactly.
//
// Every caller here passes a pane id (`req.TmuxPaneID`, `paneID`), so the
// distinction is not academic: with PanePID a hook from the second pane of a
// split window is checked against the first pane's process, fails the ancestor
// test, and is rejected as pid_not_in_pane_tree. probe/liveness.go:36-42 fixed
// the same bug on its own path in PR #638; this one was missed.
//
// PanePID itself is left alone — its other callers pass session/window targets,
// where "the first pane of the target" is the intended meaning.
func resolvePanePID(exec tmux.Executor, paneID string) (int, error) {
	pid, err := exec.ActivePanePID(paneID)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(pid))
	if err != nil {
		return 0, fmt.Errorf("parse pane pid %q: %w", pid, err)
	}
	return parsed, nil
}

func writeVerifyRejected(w http.ResponseWriter, req EventRequest, reason string) {
	log.Printf("[agent][verify] rejected pid=%d reason=%s pane=%s", req.SenderPID, reason, req.TmuxPaneID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "rejected",
		"reason": reason,
	})
}
