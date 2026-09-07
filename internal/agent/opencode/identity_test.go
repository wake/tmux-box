package opencode_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
)

func TestProvider_ImplementsSessionIdentifier(t *testing.T) {
	var p any = opencode.NewProvider()
	if _, ok := p.(agent.SessionIdentifier); !ok {
		t.Fatal("opencode.Provider does not implement agent.SessionIdentifier")
	}
}

func TestProvider_IdentifyEvent(t *testing.T) {
	p := opencode.NewProvider()
	raw := json.RawMessage(`{"session_id":"sess-oc","cwd":"/srv/app"}`)

	sessionID, cwd := p.IdentifyEvent("PdxStop", raw)
	if sessionID != "sess-oc" || cwd != "/srv/app" {
		t.Fatalf("IdentifyEvent = (%q, %q), want (sess-oc, /srv/app)", sessionID, cwd)
	}
}

// opencode's non-SessionStart emits carry session_id but no cwd (spec §3.3);
// the extractor must still yield the id.
func TestProvider_IdentifyEvent_SessionIDWithoutCwd(t *testing.T) {
	p := opencode.NewProvider()

	sessionID, cwd := p.IdentifyEvent("PdxStop", json.RawMessage(`{"session_id":"sess-oc"}`))
	if sessionID != "sess-oc" || cwd != "" {
		t.Fatalf("IdentifyEvent = (%q, %q), want (sess-oc, \"\")", sessionID, cwd)
	}
}
