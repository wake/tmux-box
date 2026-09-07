package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
)

// All three agents put session_id / cwd at the top level of raw_event
// (spec §3.1), so all three must answer identically for the same payload.
// The per-agent method exists so a future agent with a different payload
// shape has somewhere to differ — not to justify three implementations.
func TestSessionIdentifier_AllAgentsAgree(t *testing.T) {
	identifiers := map[string]any{
		"cc":       cc.NewProvider(nil, nil, nil, nil),
		"codex":    codex.NewProvider(),
		"opencode": opencode.NewProvider(),
	}
	raw := json.RawMessage(`{"session_id":"sess-shared","cwd":"/srv/shared","hook_event_name":"Stop"}`)

	for name, provider := range identifiers {
		t.Run(name, func(t *testing.T) {
			identifier, ok := provider.(agent.SessionIdentifier)
			if !ok {
				t.Fatalf("%s provider does not implement agent.SessionIdentifier", name)
			}
			sessionID, cwd := identifier.IdentifyEvent("PdxStop", raw)
			if sessionID != "sess-shared" || cwd != "/srv/shared" {
				t.Fatalf("%s IdentifyEvent = (%q, %q), want (sess-shared, /srv/shared)", name, sessionID, cwd)
			}
		})
	}
}
