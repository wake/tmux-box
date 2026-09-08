package agent

import "encoding/json"

// ExtractSessionIdentity reads the sender's own session id and cwd out of a
// raw hook payload. All three agents put both at the top level of raw_event
// (spec §3.1), so this one implementation serves them all; the per-agent
// SessionIdentifier method exists so a future agent with a different payload
// shape has somewhere to differ.
//
// Decoding into a typed struct — not a map — is load-bearing. cc PostToolUse
// payloads embed whole tool inputs, and encoding/json skips an unknown field's
// value without materialising it, whereas map[string]json.RawMessage would
// *copy* every value's bytes. BenchmarkExtractSessionIdentity holds this:
// allocated bytes per op must not scale with the tool_input size.
//
// A payload we cannot parse is not an error condition for this feature — a
// hook event still carries its status meaning — so anything unparseable simply
// yields ("", "") and the caller writes nothing.
func ExtractSessionIdentity(rawEvent json.RawMessage) (sessionID, cwd string) {
	if len(rawEvent) == 0 {
		return "", ""
	}
	var identity struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
	}
	if err := json.Unmarshal(rawEvent, &identity); err != nil {
		return "", ""
	}
	return identity.SessionID, identity.Cwd
}
