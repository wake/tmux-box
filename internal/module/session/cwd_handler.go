package session

import (
	"encoding/json"
	"net/http"
)

// cwdResponse is the wire shape of GET /api/sessions/{code}/cwd.
//
// TmuxInstance is deliberately NOT omitempty: "" is a transmitted value
// meaning "the generation is unknown", which the caller must be able to tell
// apart from an old daemon that never sends the field at all (spec §4.6).
type cwdResponse struct {
	Cwd          string `json:"cwd"`
	TmuxInstance string `json:"tmux_instance"`
}

// handleSessionCwd returns the current working directory of the tmux pane
// attached to the given session code, together with the tmux generation the
// reading belongs to. Used by the SPA terminal-link opener to resolve relative
// file paths at click time, and by the rebuild cwd probe (spec §4.6.2).
//
// The generation is sampled on BOTH sides of the pane-path read and only
// reported when the two agree. A tmux server that restarted mid-request would
// otherwise hand back the new server's cwd stamped with the old server's
// generation — and the probe, which can only compare that stamp against the
// binding it asked with, would accept it and write a stranger's directory into
// the old pane's rebuild record. Disagreement reports "" (unknown), and an
// unknown generation never authorises a write.
func (m *SessionModule) handleSessionCwd(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	cwd, err := m.tmux.PaneCurrentPath(info.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// info.TmuxInstance was sampled by GetSession, before the pane read.
	instance := info.TmuxInstance
	if after := m.TmuxInstance(); after != instance {
		instance = ""
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cwdResponse{Cwd: cwd, TmuxInstance: instance})
}
