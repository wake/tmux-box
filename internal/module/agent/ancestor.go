package agent

import "github.com/wake/purdex/internal/store"

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

// classifyAncestor walks the sender's PPID chain (capped at proxyMaxDepth)
// looking for an alive, identity-verified frame in the same pane. It is the
// single traversal behind both the proxy decision and the provenance
// ownership decision; the two results are reported separately so neither
// shadows the other.
//
// The traversal itself is unchanged from the pre-split findProxyParent — only
// the return value carries more information. The frame is returned for both
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
	ppid := info.PPID
	for depth := 0; depth < proxyMaxDepth; depth++ {
		if ppid <= 1 {
			return VerdictRoot, nil, nil
		}
		candidate, err := m.frames.FindByPanePID(req.TmuxPaneID, ppid)
		if err != nil {
			return VerdictIndeterminate, nil, err
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
					return VerdictIndeterminate, nil, nil
				}
				if actualStart == candidate.ProcessStartTime {
					// Live + identity-verified candidate.
					if candidate.AgentType == req.AgentType {
						// Same-type live ancestor: pane already owns a live
						// frame of our agent_type, so this SessionStart is a
						// re-session / update of that frame — not a cross-type
						// proxy. Hard-stop the walk here (don't continue to a
						// cross-type grandparent that would be semantically wrong).
						return VerdictSameTypeAbove, candidate, nil
					}
					// Cross-type live ancestor: this is our proxy parent.
					return VerdictProxyParent, candidate, nil
				}
				// Identity mismatch (PID reused) → stale frame; continue walk
				// to look for a real parent further up. Applies to both
				// same-type and cross-type.
			}
			// Dead candidate: also continue walk; sweep will clear it.
		}
		// No frame at this PID — walk one more level up.
		ancestorInfo, nerr := readProcessInfoFn(ppid)
		if nerr != nil {
			return VerdictIndeterminate, nil, nil
		}
		// Self-parent guard: without it the loop would re-query the same PID
		// until the depth cap.
		if ancestorInfo.PPID == ppid {
			return VerdictIndeterminate, nil, nil
		}
		ppid = ancestorInfo.PPID
	}
	return VerdictIndeterminate, nil, nil
}
