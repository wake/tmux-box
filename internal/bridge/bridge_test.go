// internal/bridge/bridge_test.go
package bridge

import (
	"testing"
	"time"
)

func TestBridgeFanOut(t *testing.T) {
	b := New()

	relayCh, err := b.RegisterRelay("test-session")
	if err != nil {
		t.Fatal(err)
	}
	defer b.UnregisterRelay("test-session")

	id1, sub1 := b.Subscribe("test-session")
	id2, sub2 := b.Subscribe("test-session")
	defer b.Unsubscribe("test-session", id1)
	defer b.Unsubscribe("test-session", id2)

	// Relay sends data → both subscribers receive
	b.RelayToSubscribers("test-session", []byte(`{"type":"assistant"}`))

	select {
	case msg := <-sub1:
		if string(msg) != `{"type":"assistant"}` {
			t.Fatalf("sub1 got %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("sub1 timeout")
	}

	select {
	case msg := <-sub2:
		if string(msg) != `{"type":"assistant"}` {
			t.Fatalf("sub2 got %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("sub2 timeout")
	}

	// SPA sends message → arrives at relay channel
	b.SubscriberToRelay("test-session", []byte(`{"type":"user"}`))

	select {
	case msg := <-relayCh:
		if string(msg) != `{"type":"user"}` {
			t.Fatalf("relay got %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("relay timeout")
	}
}

func TestBridgeNoRelay(t *testing.T) {
	b := New()
	id, ch := b.Subscribe("nonexistent")
	if ch != nil {
		t.Fatal("expected nil channel for nonexistent session")
	}
	if id != 0 {
		t.Fatal("expected 0 id for nonexistent session")
	}
}

func TestBridgeUnregisterClosesSubscribers(t *testing.T) {
	b := New()
	if _, err := b.RegisterRelay("test"); err != nil {
		t.Fatal(err)
	}
	_, sub := b.Subscribe("test")
	b.UnregisterRelay("test")
	_, ok := <-sub
	if ok {
		t.Fatal("expected subscriber channel to be closed")
	}
}

func TestBridgeRegisterRelayDuplicate(t *testing.T) {
	b := New()
	_, err := b.RegisterRelay("dup")
	if err != nil {
		t.Fatal(err)
	}
	defer b.UnregisterRelay("dup")

	_, err = b.RegisterRelay("dup")
	if err == nil {
		t.Fatal("expected error for duplicate relay registration")
	}
}
