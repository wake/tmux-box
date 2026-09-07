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
	// SawPanePID reports that opts.PanePID was seen on the chain. It is
	// purely OBSERVATIONAL: it never terminates the walk and never licenses a
	// shortcut. Seeing the pane proves membership; it proves nothing about
	// whether a framed ancestor sits further up, so on its own it can never
	// justify keeping a frame — only `Verdict == VerdictRoot && SawPanePID`
	// can. It is false on every walk that passed CheckPane: false.
	SawPanePID bool
}

// ancestryOpts carries the optional pane-membership question. The walk answers
// it only when CheckPane is true; PanePID is never consulted otherwise.
//
// The opt-in is a separate boolean rather than a `PanePID == 0` sentinel on
// purpose. A PPID of 0 is representable and the reader does not exclude it
// (process_info.go:41-50), and the pane comparison runs at the top of the
// iteration — ahead of the `ppid <= 1` terminator — so a 0 arriving on the
// chain would have compared equal to a "disabled" sentinel and spuriously
// marked a walk as inside the pane. classifyAncestor passes CheckPane: false
// and is provably unaffected by any of this.
type ancestryOpts struct {
	PanePID   int
	CheckPane bool
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
//
// opts adds the pane-membership question resolvePaneOwners needs, and nothing
// else: with opts.CheckPane false — which is what classifyAncestor passes — the
// walk reads the same PIDs in the same order as before the option existed, and
// SawPanePID stays false.
func (m *Module) walkPaneAncestry(paneID string, startPID int, agentType string, read procReader, opts ancestryOpts) (ancestryResult, error) {
	sawPanePID := false
	// result closes over sawPanePID so that every exit below — including the
	// early ones — reports what the walk had already observed.
	result := func(verdict AncestorVerdict, frame *store.Frame) ancestryResult {
		return ancestryResult{Verdict: verdict, Frame: frame, SawPanePID: sawPanePID}
	}
	ppid := startPID
	for depth := 0; depth < proxyMaxDepth; depth++ {
		// The pane comparison happens HERE — at the top of the iteration,
		// against the current ppid, before the candidate lookup and before
		// every early return. Checking it after the ancestorInfo read instead
		// would miss the commonest topology there is: the first parent IS the
		// pane's shell (measured depth 1), on an iteration that may well take
		// an early exit of its own.
		if opts.CheckPane && ppid == opts.PanePID {
			sawPanePID = true
		}
		if ppid <= 1 {
			return result(VerdictRoot, nil), nil
		}
		candidate, err := m.frames.FindByPanePID(paneID, ppid)
		if err != nil {
			return result(VerdictIndeterminate, nil), err
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
					return result(VerdictIndeterminate, nil), nil
				}
				if actualStart == candidate.ProcessStartTime {
					// Live + identity-verified candidate.
					if candidate.AgentType == agentType {
						// Same-type live ancestor: pane already owns a live
						// frame of our agent_type, so this SessionStart is a
						// re-session / update of that frame — not a cross-type
						// proxy. Hard-stop the walk here (don't continue to a
						// cross-type grandparent that would be semantically wrong).
						return result(VerdictSameTypeAbove, candidate), nil
					}
					// Cross-type live ancestor: this is our proxy parent.
					return result(VerdictProxyParent, candidate), nil
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
			return result(VerdictIndeterminate, nil), nil
		}
		// Self-parent guard: without it the loop would re-query the same PID
		// until the depth cap.
		if ancestorInfo.PPID == ppid {
			return result(VerdictIndeterminate, nil), nil
		}
		ppid = ancestorInfo.PPID
	}
	return result(VerdictIndeterminate, nil), nil
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
	res, err := m.walkPaneAncestry(req.TmuxPaneID, info.PPID, req.AgentType, readProcessInfoFn, ancestryOpts{})
	return res.Verdict, res.Frame, err
}
