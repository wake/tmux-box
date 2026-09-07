package agent

// Status represents the normalized agent status.
type Status string

const (
	StatusRunning Status = "running"
	StatusWaiting Status = "waiting"
	StatusIdle    Status = "idle"
	StatusError   Status = "error"
	StatusClear   Status = "clear"
)

// DeriveResult is the output of AgentProvider.DeriveStatus.
type DeriveResult struct {
	Status Status
	Valid  bool           // false = event should be ignored
	Model  string         // extracted model name (if any)
	Detail map[string]any // event-specific data for frontend notifications
	// Reason explains why Valid=false. Empty means truly unknown event name
	// ("event_not_in_catalog"). Non-empty signals a known event whose payload
	// cannot be mapped to a Status (e.g. "compact_ignored" for the cc
	// SessionStart compact subtype, or "notification_unknown_type" for an
	// unrecognized notification_type subtype). Handler uses this to keep
	// trace observability for known-but-unmappable events instead of folding
	// them into the generic catalog miss bucket. Ignored when Valid=true.
	Reason string
}

// DetailStrings copies only the non-empty string-valued keys present in raw
// into a new map, so an absent key stays absent rather than serializing as
// null in the WS payload's detail object.
func DetailStrings(raw map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := raw[k].(string); ok && v != "" {
			out[k] = v
		}
	}
	return out
}

// NormalizedEvent is broadcast to WS subscribers.
type NormalizedEvent struct {
	AgentType    string         `json:"agent_type"`
	Status       string         `json:"status"`
	Model        string         `json:"model,omitempty"`
	Subagents    []SubagentRef  `json:"subagents"`
	RawEventName string         `json:"raw_event_name"`
	BroadcastTs  int64          `json:"broadcast_ts"`
	Detail       map[string]any `json:"detail,omitempty"`
}
