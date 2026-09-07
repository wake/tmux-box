package codex_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

func TestProvider_ImplementsSessionIdentifier(t *testing.T) {
	var p any = codex.NewProvider()
	if _, ok := p.(agent.SessionIdentifier); !ok {
		t.Fatal("codex.Provider does not implement agent.SessionIdentifier")
	}
}

func TestProvider_IdentifyEvent(t *testing.T) {
	p := codex.NewProvider()
	// codex additionally carries turn_id; it must not disturb the two fields.
	raw := json.RawMessage(`{"session_id":"sess-codex","cwd":"/srv/app","turn_id":"t-9"}`)

	sessionID, cwd := p.IdentifyEvent("PdxStop", raw)
	if sessionID != "sess-codex" || cwd != "/srv/app" {
		t.Fatalf("IdentifyEvent = (%q, %q), want (sess-codex, /srv/app)", sessionID, cwd)
	}
}

func TestProvider_IdentifyEvent_MissingFields(t *testing.T) {
	p := codex.NewProvider()

	sessionID, cwd := p.IdentifyEvent("PdxStop", json.RawMessage(`{"turn_id":"t-9"}`))
	if sessionID != "" || cwd != "" {
		t.Fatalf("IdentifyEvent = (%q, %q), want two empty strings", sessionID, cwd)
	}
}
