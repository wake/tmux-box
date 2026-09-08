package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/module/session"
)

// provenanceTimeout bounds one provenance request, checked between process
// reads (it cannot interrupt one — readProcessInfoPlatform has no context).
// It is a var only so tests can expire it; production never changes it.
var provenanceTimeout = 5 * time.Second

// provenanceResponse is the wire shape of GET /api/sessions/{code}/provenance.
//
// TmuxInstance is deliberately NOT omitempty, for the same reason as the cwd
// handler's: "" is a transmitted value meaning "the generation is unknown", and
// the caller must be able to tell it apart from a daemon that never sends the
// field. Everything else is omitted when there is no answer, so found:false is
// the two-field object the spec shows.
type provenanceResponse struct {
	Found        bool   `json:"found"`
	AgentType    string `json:"agent_type,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	TmuxPaneID   string `json:"tmux_pane_id,omitempty"`
	TmuxInstance string `json:"tmux_instance"`
	LastSeenAt   int64  `json:"last_seen_at,omitempty"`
}

// handleSessionProvenance answers which agent owns the tmux session behind
// {code}: the root agent frame of one of its panes, with the session id and cwd
// that agent reported for itself, so the SPA can compose a resume command
// (spec §5.3).
//
// The generation is sampled on BOTH sides of the frame work and reported only
// when the two samples agree, exactly as the cwd handler does. A tmux server
// that restarted mid-request would otherwise hand back the new server's agent
// stamped with the old server's generation, and the probe — which can only
// compare that stamp against the binding it asked with — would accept it.
// Disagreement reports "", and "" authorises nothing on the SPA side.
//
// An unknown session code is answered with found:false and a 200, not a 404:
// the SPA treats "no answer" uniformly and a code that just died is a normal
// race, not a client error.
func (m *Module) handleSessionProvenance(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	instance := m.tmuxInstance()
	owner, found := m.resolveSessionOwner(r.Context(), code)
	if after := m.tmuxInstance(); after != instance {
		instance = ""
	}

	resp := provenanceResponse{TmuxInstance: instance}
	if found {
		resp.Found = true
		resp.AgentType = owner.AgentType
		resp.SessionID = owner.SessionID
		resp.Cwd = owner.Cwd
		resp.TmuxPaneID = owner.TmuxPaneID
		resp.LastSeenAt = owner.LastSeenAt
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// tmuxInstance samples the tmux server generation, or "" when there is nobody
// to ask. "" means unknown and is never treated as a match.
func (m *Module) tmuxInstance() string {
	if m.sessions == nil {
		return ""
	}
	return m.sessions.TmuxInstance()
}

// resolveSessionOwner picks the one root agent frame that answers for a tmux
// session, or reports that there is none.
//
// ONE memoizing reader and ONE deadline serve the whole request: a process read
// is four `ps` forks on darwin (spec §3.4), and the frames of a session share
// almost all of their ancestry, so the memo turns O(frames × depth) reads into
// roughly O(distinct PIDs). Both are built here and handed to every
// resolvePaneOwners call — never one per pane.
//
// A non-nil error from resolvePaneOwners discards the owners it returned
// alongside it: a partial walk is not an answer, and half a pane's frames can
// name a root that the rest of the walk would have rejected. Either way the
// whole query answers "not found" rather than guessing.
func (m *Module) resolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool) {
	if m.frames == nil || m.tmux == nil || code == "" {
		return PaneOwner{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, provenanceTimeout)
	defer cancel()

	panes, err := m.panesOfSession(ctx, code)
	if err != nil {
		return PaneOwner{}, false
	}

	read := newMemoProcReader(readProcessInfoFn)
	var best PaneOwner
	found := false
	for _, paneID := range panes {
		owners, err := m.resolvePaneOwners(ctx, paneID, read)
		if err != nil {
			return PaneOwner{}, false
		}
		if len(owners) == 0 {
			continue
		}
		// The membership decided by panesOfSession is stale by the time the
		// walk finishes: resolvePaneOwners is the slow part of the request
		// (four `ps` forks per distinct PID), and a `join-pane` inside that
		// window moves the pane — and whatever agent is now running in it —
		// into a DIFFERENT session. Neither generation sample notices: both
		// sessions live on the same tmux server, so the stamp is identical
		// on both sides and the answer is reported as trustworthy.
		//
		// So the pane is asked again, at the point its answer is about to be
		// used, and an answer that can no longer be confirmed as this
		// session's is dropped rather than reported.
		if !m.paneStillInSession(paneID, code) {
			continue
		}
		for _, owner := range owners {
			// The session-id filter lives HERE, not in resolvePaneOwners: a
			// root that never reported an identity is still a root, it just
			// cannot answer this question.
			if owner.SessionID == "" {
				continue
			}
			if !found || betterOwner(owner, best) {
				best, found = owner, true
			}
		}
	}
	return best, found
}

// paneStillInSession re-reads the pane's tmux session id and reports whether it
// is still the session behind `code`. Matched by session ID and encoded with
// the same pure function panesOfSession uses, for the same reason: a name would
// reintroduce the rename window this route exists to avoid.
//
// Anything short of a confirmed match is a no: a pane that cannot be read, an
// id that cannot be encoded, and an id that encodes to another code are all
// "not confirmed as ours", and none of them may be answered with.
func (m *Module) paneStillInSession(paneID, code string) bool {
	tmuxID, err := m.tmux.PaneSessionID(paneID)
	if err != nil {
		return false
	}
	paneCode, err := session.EncodeSessionID(tmuxID)
	if err != nil {
		return false
	}
	return paneCode == code
}

// betterOwner is the multi-root tie-break: most recently seen wins, and equal
// last_seen_at is broken by ASCENDING frame id so the answer is deterministic
// and testable rather than dependent on store ordering.
func betterOwner(candidate, incumbent PaneOwner) bool {
	if candidate.LastSeenAt != incumbent.LastSeenAt {
		return candidate.LastSeenAt > incumbent.LastSeenAt
	}
	return candidate.FrameID < incumbent.FrameID
}

// panesOfSession lists the panes of the session behind `code` that could
// possibly answer — i.e. the panes that have at least one frame. The Executor
// interface cannot enumerate a session's panes (ActivePaneMetadata is the
// active pane only), and a pane with no frame has no agent to report, so
// starting from the frames costs nothing.
//
// Each pane is matched by tmux session ID, never by name. m.resolvePaneSession
// looks like exactly this function and must not be used: it goes through
// LookupCodeByName, whose cache is deliberately stale for up to 250 ms after an
// external mutation. That is fine on the hook hot path it was built for, and
// wrong here — rename session1 away and session2 into its name inside that
// window and a query for session1's code can be answered with session2's agent,
// with the generation stamp matching and the pane-tree check passing too. A
// session id is immutable for the life of the session and EncodeSessionID is a
// pure function of it, so this route has no such window. It is the same reason
// handler.go prefers TmuxSessionID over the name whenever a hook carries one.
//
// An error at either step excludes that pane, with no fallback to a name
// lookup.
//
// Enumeration is under the request deadline like everything else: it is one
// tmux round trip per distinct pane that has a frame, spent before a single
// process has been read, and a request that is already out of time must not
// spend any of them. ctx's error ends the enumeration, and the caller reads
// that the same way it reads a store failure — no answer.
func (m *Module) panesOfSession(ctx context.Context, code string) ([]string, error) {
	frames, err := m.frames.ListAll()
	if err != nil {
		return nil, err
	}
	panes := make([]string, 0, len(frames))
	seen := make(map[string]bool, len(frames))
	for _, frame := range frames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[frame.PaneID] {
			continue
		}
		seen[frame.PaneID] = true
		tmuxID, err := m.tmux.PaneSessionID(frame.PaneID)
		if err != nil {
			continue
		}
		paneCode, err := session.EncodeSessionID(tmuxID)
		if err != nil {
			continue
		}
		if paneCode != code {
			continue
		}
		panes = append(panes, frame.PaneID)
	}
	return panes, nil
}
