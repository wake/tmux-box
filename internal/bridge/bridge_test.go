package bridge

import "testing"

func TestBridgeFanOut(t *testing.T) {
	b := New()
	relayCh, _ := b.RegisterRelay("sess")
	_, subCh := b.Subscribe("sess")
	if subCh == nil {
		t.Fatal("subscribe returned nil")
	}

	// Relay → subscribers
	b.RelayToSubscribers("sess", []byte("hello"))
	msg := <-subCh
	if string(msg) != "hello" {
		t.Fatalf("got %q", msg)
	}

	// Subscriber → relay
	b.SubscriberToRelay("sess", []byte("world"))
	msg = <-relayCh
	if string(msg) != "world" {
		t.Fatalf("got %q", msg)
	}
}

func TestBridgeNoRelay(t *testing.T) {
	b := New()
	_, ch := b.Subscribe("nonexistent")
	if ch != nil {
		t.Fatal("subscribe without relay should return nil")
	}
}

func TestBridgeUnregisterClosesSubscribers(t *testing.T) {
	b := New()
	b.RegisterRelay("sess")
	_, subCh := b.Subscribe("sess")
	b.UnregisterRelay("sess")
	_, ok := <-subCh
	if ok {
		t.Fatal("subscriber channel should be closed")
	}
}

func TestBridgeRegisterRelayDuplicate(t *testing.T) {
	b := New()
	b.RegisterRelay("sess")
	_, err := b.RegisterRelay("sess")
	if err == nil {
		t.Fatal("duplicate register should error")
	}
}

func TestRelaySessionNames(t *testing.T) {
	b := New()

	// No relays
	names := b.RelaySessionNames()
	if len(names) != 0 {
		t.Fatalf("want 0, got %d", len(names))
	}

	// Register two relays
	b.RegisterRelay("alpha")
	b.RegisterRelay("beta")

	names = b.RelaySessionNames()
	if len(names) != 2 {
		t.Fatalf("want 2, got %d", len(names))
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["alpha"] || !found["beta"] {
		t.Fatalf("missing names: %v", names)
	}

	// Unregister one
	b.UnregisterRelay("alpha")
	names = b.RelaySessionNames()
	if len(names) != 1 || names[0] != "beta" {
		t.Fatalf("after unregister: %v", names)
	}
}
