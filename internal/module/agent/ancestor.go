package agent

import (
	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// AncestorVerdict distinguishes the outcomes that findProxyParent used to
// collapse into (nil, nil). See spec §4.3.
type AncestorVerdict int

const (
	// VerdictRoot — no live framed agent ancestor in the pane.
	VerdictRoot AncestorVerdict = iota
	// VerdictSameTypeAbove — live, identity-verified same-type ancestor frame.
	VerdictSameTypeAbove
	// VerdictProxyParent — live cross-type ancestor frame → existing collapse.
	VerdictProxyParent
	// VerdictIndeterminate — the walk could not complete (proc read error,
	// unverifiable identity, self-parent, depth cap).
	VerdictIndeterminate
)

// String renders the verdict for logs and test failure messages.
func (v AncestorVerdict) String() string {
	switch v {
	case VerdictRoot:
		return "root"
	case VerdictSameTypeAbove:
		return "same_type_above"
	case VerdictProxyParent:
		return "proxy_parent"
	case VerdictIndeterminate:
		return "indeterminate"
	default:
		return "unknown"
	}
}

// procReader is the seam the ancestry walk reads processes through.
//
// classifyAncestor passes readProcessInfoFn directly and must keep doing so:
// provenance_test.go:170 deliberately makes the sender's 1st/2nd/3rd process
// read return different values to exercise the post-Upsert reconcile, so a memo
// on the hook path would break that test's premise while leaving it green for
// the wrong reason. The request-scoped memo belongs to the provenance query
// alone.
type procReader func(pid int) (agentpkg.ProcessInfo, error)

// ancestryResult is what one walk reports. Frame is set for
// VerdictSameTypeAbove and VerdictProxyParent only; every other verdict reports
// nil, exactly as the pre-split return did.
type ancestryResult struct {
	Verdict AncestorVerdict
	Frame   *store.Frame
}

// walkPaneAncestry walks startPID's PPID chain, capped at proxyMaxDepth,
// looking for an alive, identity-verified frame of paneID. It is the single
// traversal behind both the proxy decision and the provenance ownership
// decision; the two results are reported separately so neither shadows the
// other.
//
// The walk starts *at* startPID — callers that begin from a process's own PID
// are responsible for reading it and handing over its PPID, which is what
// classifyAncestor does. Non-nil error is returned only when the frames store
// fails.
func (m *Module) walkPaneAncestry(paneID string, startPID int, agentType string, read procReader) (ancestryResult, error) {
	ppid := startPID
	for depth := 0; depth < proxyMaxDepth; depth++ {
		if ppid <= 1 {
			return ancestryResult{Verdict: VerdictRoot}, nil
		}
		candidate, err := m.frames.FindByPanePID(paneID, ppid)
		if err != nil {
			return ancestryResult{Verdict: VerdictIndeterminate}, err
		}
		if candidate != nil {
			// Liveness + identity gating applies to BOTH same-type and
			// cross-type candidates (R3 fix). A stale same-type frame (PID
			// reused, or process dead) is leftover data, not a real
			// "re-session of an existing live sibling"; it must not
			// hard-stop the walk or we'd strand a legitimate proxy attach
			// to a live cross-type ancestor above it.
			if isPidAliveFn(candidate.PID) {
				actualStart, serr := processStartTimeFn(candidate.PID)
				if serr != nil {
					// v5 rule: identity unverifiable → abort walk (consistent
					// with verify.go's "lookup error → don't infer" convention).
					// Prevents mis-attaching to an outer cross-type ancestor
					// when the immediate candidate's start_time is transiently
					// unreadable.
					return ancestryResult{Verdict: VerdictIndeterminate}, nil
				}
				if actualStart == candidate.ProcessStartTime {
					// Live + identity-verified candidate.
					if candidate.AgentType == agentType {
						// Same-type live ancestor: pane already owns a live
						// frame of our agent_type, so this SessionStart is a
						// re-session / update of that frame — not a cross-type
						// proxy. Hard-stop the walk here (don't continue to a
						// cross-type grandparent that would be semantically wrong).
						return ancestryResult{Verdict: VerdictSameTypeAbove, Frame: candidate}, nil
					}
					// Cross-type live ancestor: this is our proxy parent.
					return ancestryResult{Verdict: VerdictProxyParent, Frame: candidate}, nil
				}
				// Identity mismatch (PID reused) → stale frame; continue walk
				// to look for a real parent further up. Applies to both
				// same-type and cross-type.
			}
			// Dead candidate: also continue walk; sweep will clear it.
		}
		// No frame at this PID — walk one more level up.
		ancestorInfo, nerr := read(ppid)
		if nerr != nil {
			return ancestryResult{Verdict: VerdictIndeterminate}, nil
		}
		// Self-parent guard: without it the loop would re-query the same PID
		// until the depth cap.
		if ancestorInfo.PPID == ppid {
			return ancestryResult{Verdict: VerdictIndeterminate}, nil
		}
		ppid = ancestorInfo.PPID
	}
	return ancestryResult{Verdict: VerdictIndeterminate}, nil
}

// classifyAncestor walks the sender's PPID chain (capped at proxyMaxDepth)
// looking for an alive, identity-verified frame in the same pane. It is the
// first caller of walkPaneAncestry: it reads the sender's own ProcessInfo and
// enters the walk one level up, at info.PPID.
//
// The traversal itself is unchanged from the pre-split findProxyParent — only
// where it lives has moved. The frame is returned for both
// VerdictSameTypeAbove and VerdictProxyParent; every other verdict returns
// nil. Non-nil error is returned only when the frames store fails.
func (m *Module) classifyAncestor(req EventRequest) (AncestorVerdict, *store.Frame, error) {
	if m.frames == nil {
		return VerdictIndeterminate, nil, nil
	}
	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		return VerdictIndeterminate, nil, nil
	}
	res, err := m.walkPaneAncestry(req.TmuxPaneID, info.PPID, req.AgentType, readProcessInfoFn)
	return res.Verdict, res.Frame, err
}
