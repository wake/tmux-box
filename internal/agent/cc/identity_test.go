package cc_test

import (
	"encoding/json"
	"testing"

	"github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/cc"
)

func TestProvider_ImplementsSessionIdentifier(t *testing.T) {
	var p any = cc.NewProvider(nil, nil, nil, nil)
	if _, ok := p.(agent.SessionIdentifier); !ok {
		t.Fatal("cc.Provider does not implement agent.SessionIdentifier")
	}
}

func TestProvider_IdentifyEvent(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)
	raw := json.RawMessage(`{"session_id":"sess-cc","cwd":"/srv/app","hook_event_name":"Stop"}`)

	sessionID, cwd := p.IdentifyEvent("PdxStop", raw)
	if sessionID != "sess-cc" || cwd != "/srv/app" {
		t.Fatalf("IdentifyEvent = (%q, %q), want (sess-cc, /srv/app)", sessionID, cwd)
	}
}

func TestProvider_IdentifyEvent_MissingFields(t *testing.T) {
	p := cc.NewProvider(nil, nil, nil, nil)

	sessionID, cwd := p.IdentifyEvent("PdxStop", json.RawMessage(`{"hook_event_name":"Stop"}`))
	if sessionID != "" || cwd != "" {
		t.Fatalf("IdentifyEvent = (%q, %q), want two empty strings", sessionID, cwd)
	}
}
