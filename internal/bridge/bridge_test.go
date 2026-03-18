package bridge

import "testing"

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
