package agent

import (
	"context"
	"strings"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// PaneOwner is one root agent frame of a pane: a live, identity-verified frame
// that sits inside the pane's current process tree with no other surviving
// frame of that pane above it.
//
// FrameID is carried even though nothing in this file reads it: the handler's
// multi-root tie-break sorts on it to stay deterministic when two roots share
// a last_seen_at (spec §5.3 step 6).
type PaneOwner struct {
	FrameID    string
	AgentType  string
	SessionID  string
	Cwd        string
	TmuxPaneID string
	LastSeenAt int64
}

// resolvePaneOwners returns the root agent frames of one pane.
//
// The rule, stated once: a frame is kept **iff its walk ran to completion with
// VerdictRoot and the pane's own PID was seen somewhere along the way.**
// Everything else is excluded — never promoted. An incomplete walk (depth cap,
// unreadable process, self-parent, unverifiable candidate identity) is no
// evidence, and no evidence means no action.
//
// Both questions are answered by ONE walk per surviving frame:
//
//   - inside the pane's tree — panePID appears on the chain. This is the check
//     every accepted hook event already passes (verify.go:60-65), and it is
//     what stops a surviving agent from a previous tmux generation being handed
//     back under a reused pane id;
//   - not a root — another surviving frame of this pane appears on the chain,
//     which the walker reports as SameTypeAbove / ProxyParent.
//
// `read` is shared with every other call in the same request; Task 7 passes
// newMemoProcReader(readProcessInfoFn) so that one PID costs one read for the
// whole request (a single read is four `ps` forks on darwin — spec §3.4).
//
// pidAncestorIncludesFn / probe.PidAncestorIncludes is deliberately NOT used
// for the pane-tree half: it walks with no depth cap and calls
// agentpkg.ReadProcessInfo directly, bypassing both this reader and the test
// seam. Its semantics are what the walk reproduces, including that a frame
// whose PID equals the pane PID counts as inside the tree. The projection's
// own pane filter (frame_ops.go:951-978) is not reused either: it KEEPS a
// frame when resolution fails, the opposite of the policy here.
//
// Read-only: no store writes, no envelope, no state between calls. The
// returned error is either a frames-store failure or ctx's — both mean "no
// answer", and the handler turns either into found:false. Owners decided
// before the error are returned alongside it and must not be treated as a
// result.
func (m *Module) resolvePaneOwners(ctx context.Context, paneID string, read procReader) ([]PaneOwner, error) {
	if m.frames == nil || m.tmux == nil {
		return nil, nil
	}
	// A pane whose current process cannot be resolved contributes nothing
	// (spec §5.3 step 2). That is not an error: the session's other panes may
	// still have an answer.
	panePID, err := resolvePanePIDFn(m.tmux, paneID)
	if err != nil {
		return nil, nil
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return nil, err
	}

	survivors := make([]store.Frame, 0, len(frames))
	for _, frame := range frames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !isPidAliveFn(frame.PID) {
			continue
		}
		actualStart, serr := processStartTimeFn(frame.PID)
		if serr != nil {
			continue
		}
		if strings.TrimSpace(actualStart) != strings.TrimSpace(frame.ProcessStartTime) {
			continue
		}
		survivors = append(survivors, frame)
	}

	owners := make([]PaneOwner, 0, len(survivors))
	for _, frame := range survivors {
		// ctx cannot interrupt a single process read — readProcessInfoPlatform
		// has no context (process_info_darwin.go:9) — so the deadline is
		// checked between walks instead.
		if err := ctx.Err(); err != nil {
			return owners, err
		}
		// Where SawPanePID is set, 1 of 2: the walk enters one level ABOVE
		// this frame, so it can never observe the frame's own PID. An agent
		// that IS the pane's current process is inside the pane's tree, and
		// PidAncestorIncludes counts that case too.
		sawPanePID := frame.PID == panePID

		info, rerr := read(frame.PID)
		if rerr != nil {
			// The frame's own process became unreadable: no evidence, so no
			// action for this frame. Other frames are unaffected.
			continue
		}
		res, werr := m.walkPaneAncestry(paneID, info.PPID, frame.AgentType, read, ancestryOpts{
			PanePID:   panePID,
			CheckPane: true,
		})
		if werr != nil {
			// Only a frames-store failure reaches here. It is not a verdict,
			// so it propagates rather than excluding one frame quietly.
			return owners, werr
		}
		// Where SawPanePID is set, 2 of 2 is inside the walk, at the top of
		// every iteration.
		if res.Verdict != VerdictRoot || !(sawPanePID || res.SawPanePID) {
			continue
		}
		owners = append(owners, PaneOwner{
			FrameID:    frame.FrameID,
			AgentType:  frame.AgentType,
			SessionID:  frame.SessionID,
			Cwd:        frame.Cwd,
			TmuxPaneID: frame.PaneID,
			LastSeenAt: frame.LastSeenAt,
		})
	}
	return owners, nil
}

// newMemoProcReader wraps base so that each PID costs at most one call for the
// life of the returned reader, failures included — a PID that could not be read
// is not retried within the same request.
//
// It MUST be created per request and never at package level: ancestry is
// exactly the kind of thing that goes stale, and a shared memo would serve a
// later request a process tree that no longer exists. It is used from a single
// goroutine (one HTTP request) and is not safe for concurrent use.
//
// classifyAncestor must never be given one: provenance_test.go:170 deliberately
// makes the sender's 1st/2nd/3rd read return different values to exercise the
// post-Upsert reconcile, and a memo on the hook path would break that test's
// premise while leaving it green for the wrong reason.
func newMemoProcReader(base procReader) procReader {
	type memoEntry struct {
		info agentpkg.ProcessInfo
		err  error
	}
	cache := make(map[int]memoEntry)
	return func(pid int) (agentpkg.ProcessInfo, error) {
		if entry, ok := cache[pid]; ok {
			return entry.info, entry.err
		}
		info, err := base(pid)
		cache[pid] = memoEntry{info: info, err: err}
		return info, err
	}
}
