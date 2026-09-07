package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/wake/purdex/internal/store"
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// --- HTTP Handlers ---

func (m *SessionModule) handleList(w http.ResponseWriter, r *http.Request) {
	sessions, err := m.cachedListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Return empty array, not null
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (m *SessionModule) cachedListSessions() ([]SessionInfo, error) {
	m.listCacheMu.Lock()
	defer m.listCacheMu.Unlock()
	if time.Since(m.listCacheAt) < listCacheTTL && m.listCacheData != nil {
		return m.listCacheData, nil
	}
	sessions, err := m.ListSessions()
	if err != nil {
		return nil, err
	}
	m.listCacheData = sessions
	m.listCacheAt = time.Now()
	return sessions, nil
}

func (m *SessionModule) handleGet(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

type createRequest struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
	Mode string `json:"mode"`
}

func (m *SessionModule) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || !nameRegex.MatchString(req.Name) {
		http.Error(w, "invalid session name: must match ^[a-zA-Z0-9_-]+$", http.StatusBadRequest)
		return
	}

	if req.Cwd == "" {
		req.Cwd = "/"
	}

	// Default and validate mode
	if req.Mode == "" {
		req.Mode = "terminal"
	}
	switch req.Mode {
	case "terminal", "stream":
		// valid
	default:
		http.Error(w, "invalid mode: must be terminal or stream", http.StatusBadRequest)
		return
	}

	// Serialize the HasSession→NewSession→SetMeta critical section so two
	// concurrent POSTs with the same name can't both slip past the duplicate
	// check. Input validation stays outside the lock.
	m.createMu.Lock()
	defer m.createMu.Unlock()

	// Check for duplicate session name
	if m.tmux.HasSession(req.Name) {
		http.Error(w, "session already exists: "+req.Name, http.StatusConflict)
		return
	}

	// Create the tmux session
	if err := m.tmux.NewSession(req.Name, req.Cwd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Find the newly created session to get its tmux ID
	sessions, err := m.tmux.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var info *SessionInfo
	for _, s := range sessions {
		if s.Name == req.Name {
			code, err := EncodeSessionID(s.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// Set initial meta
			if err := m.meta.SetMeta(s.ID, store.SessionMeta{
				TmuxID: s.ID,
				Mode:   req.Mode,
				Cwd:    req.Cwd,
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			info = &SessionInfo{
				Code:   code,
				TmuxID: s.ID,
				Name:   s.Name,
				Exists: true,
				Mode:   req.Mode,
				Cwd:    req.Cwd,
				// This response is built by hand rather than via ListSessions,
				// so it needs its own stamp. The rebuild engine re-points a
				// pane using the generation carried here (spec §4.8 step 4);
				// leaving it empty would give the rebuilt pane an unknown
				// generation until the next sessions broadcast.
				TmuxInstance: m.TmuxInstance(),
			}
			break
		}
	}

	if info == nil {
		http.Error(w, "session created but not found", http.StatusInternalServerError)
		return
	}

	m.invalidateNameCache()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

type renameRequest struct {
	Name string `json:"name"`
}

func (m *SessionModule) handleRename(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || !nameRegex.MatchString(req.Name) {
		http.Error(w, "invalid session name: must match ^[a-zA-Z0-9_-]+$", http.StatusBadRequest)
		return
	}

	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Check for duplicate target name
	if req.Name != info.Name && m.tmux.HasSession(req.Name) {
		http.Error(w, "session already exists: "+req.Name, http.StatusConflict)
		return
	}

	if err := m.renameSessionAtomic(info.Name, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	m.invalidateNameCache()

	// Return updated info with new name
	info.Name = req.Name
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (m *SessionModule) handleDelete(w http.ResponseWriter, r *http.Request) {
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

	// Kill tmux session by name
	if err := m.tmux.KillSession(info.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Delete meta
	_ = m.meta.DeleteMeta(info.TmuxID)

	m.invalidateNameCache()

	w.WriteHeader(http.StatusNoContent)
}

type switchModeRequest struct {
	Mode string `json:"mode"`
}

func (m *SessionModule) handleSwitchMode(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	var req switchModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate mode
	switch req.Mode {
	case "terminal", "stream":
		// valid
	default:
		http.Error(w, "invalid mode: must be terminal or stream", http.StatusBadRequest)
		return
	}

	// Verify session exists
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Ensure meta record exists before updating
	if err := m.meta.SetMeta(info.TmuxID, store.SessionMeta{
		TmuxID: info.TmuxID,
		Mode:   info.Mode,
		Cwd:    info.Cwd,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mode := req.Mode
	if err := m.UpdateMeta(code, MetaUpdate{Mode: &mode}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type sendKeysRequest struct {
	Keys string `json:"keys"`

	// ExpectedTmuxInstance is the tmux generation the caller believes this
	// session code belongs to (spec §4.6.2). Optional: absent or "" means the
	// caller states no expectation and the keys go to whatever the code
	// resolves to now — which is what Quick Commands and `executeCommand`
	// do, and their behaviour is unchanged.
	//
	// When it IS stated, the daemon checks it. Codes are a reversible encoding
	// of the tmux id `$N`, so after a tmux server restart `$0` mints the same
	// code and a caller holding a recorded code can address a session it has
	// never seen. Only the daemon knows the current generation at the moment
	// it acts, so this is the only place the check can be authoritative — a
	// client-side cache comparison is a hint, not a precondition.
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
}

func (m *SessionModule) handleSendKeys(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	var req sendKeysRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Keys == "" {
		http.Error(w, "keys must not be empty", http.StatusBadRequest)
		return
	}

	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// A stated expectation must be met exactly. An unknown current generation
	// ("" — the probe failed or timed out) can never satisfy one: unknown
	// authorises nothing, the same direction §4.6 takes when it refuses to
	// declare a pane dead without evidence.
	if req.ExpectedTmuxInstance != "" && req.ExpectedTmuxInstance != info.TmuxInstance {
		http.Error(w, "session "+code+" belongs to another tmux generation", http.StatusConflict)
		return
	}

	if err := m.tmux.SendKeysRaw("="+info.Name+":", req.Keys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (m *SessionModule) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	m.HandleTerminalWS(w, r, code)
}

// agentEventsRenamer is the optional interface implemented by the agent
// events store, used to rename stored events when a tmux session is renamed.
type agentEventsRenamer interface {
	Rename(oldName, newName string) error
}

// atomicRenamer is the optional interface implemented by the agent module,
// used to perform tmux + DB + in-memory rename atomically under the agent
// module's lock.
type atomicRenamer interface {
	RenameSessionAtomic(oldName, newName string, doRename func() error) error
}

// renameSessionAtomic runs the complete rename flow (tmux + agent events DB
// + agent module in-memory state) atomically under the agent module's lock.
//
// Ordering: DB rename first, then tmux rename.  This ordering allows tmux
// rename failure (the most likely failure mode — e.g. tmux server unavailable)
// to trigger a best-effort DB rollback, since the DB UPDATE is trivially
// reversible.  If the initial DB rename fails, no tmux state is mutated.
//
// The agent module MUST be registered at "agent.module" and implement
// atomicRenamer — this is enforced at daemon startup, not a runtime fallback.
func (m *SessionModule) renameSessionAtomic(oldName, newName string) error {
	// Hard assert: agent module must be registered with the atomic rename API.
	// Silent fallback would mask module initialization bugs.
	svc, ok := m.core.Registry.Get("agent.module")
	if !ok {
		return errors.New("rename: agent.module not registered in service registry")
	}
	renamer, ok := svc.(atomicRenamer)
	if !ok {
		return errors.New("rename: agent.module does not implement atomicRenamer")
	}

	// Look up the optional DB renamer once, before entering the critical section.
	var dbRenamer agentEventsRenamer
	if svc, ok := m.core.Registry.Get("agent.events"); ok {
		if r, ok := svc.(agentEventsRenamer); ok {
			dbRenamer = r
		}
	}

	doRename := func() error {
		// DB first — reversible via UPDATE back to oldName.
		if dbRenamer != nil {
			if err := dbRenamer.Rename(oldName, newName); err != nil {
				return err
			}
		}
		// Tmux rename — if this fails, best-effort roll back the DB
		// so the DB + in-memory + tmux state stay consistent.
		if err := m.tmux.RenameSession(oldName, newName); err != nil {
			if dbRenamer != nil {
				_ = dbRenamer.Rename(newName, oldName)
			}
			return err
		}
		return nil
	}
	return renamer.RenameSessionAtomic(oldName, newName, doRename)
}
