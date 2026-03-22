package session

import "net/http"

// SessionProvider is the interface registered to ServiceRegistry
// for other modules to access session data.
type SessionProvider interface {
	ListSessions() ([]SessionInfo, error)
	GetSession(code string) (*SessionInfo, error)
	UpdateMeta(code string, update MetaUpdate) error
	HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string)
}

// SessionInfo combines live tmux data with cached meta.
type SessionInfo struct {
	// Live from tmux (not stored in DB)
	Code           string `json:"code"`
	TmuxID         string `json:"-"` // internal only
	Name           string `json:"name"`
	Exists         bool   `json:"-"`
	CurrentCommand string `json:"current_command,omitempty"`

	// Meta cache (stored in DB)
	Mode        string `json:"mode"`
	CCSessionID string `json:"cc_session_id,omitempty"`
	CCModel     string `json:"cc_model,omitempty"`
	Cwd         string `json:"cwd,omitempty"`

	// Runtime state (not stored)
	HasRelay bool `json:"has_relay"`
}

// MetaUpdate supports partial meta updates. Nil = no change.
type MetaUpdate struct {
	Mode        *string
	CCSessionID *string
	CCModel     *string
	Cwd         *string
}

// RegistryKey is the service registry key for SessionProvider.
const RegistryKey = "session.provider"
