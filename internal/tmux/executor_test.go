// internal/tmux/executor_test.go
package tmux_test

import (
	"testing"

	"github.com/wake/tmux-box/internal/tmux"
)

func TestListSessions(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("dev", "/home/user/dev")
	fake.AddSession("prod", "/home/user/prod")

	sessions, err := fake.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(sessions))
	}
}

func TestNewSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()

	err := fake.NewSession("test", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !fake.HasSession("test") {
		t.Error("session should exist after creation")
	}
}

func TestKillSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("doomed", "/tmp")

	fake.KillSession("doomed")

	if fake.HasSession("doomed") {
		t.Error("session should not exist after kill")
	}
}

func TestKillNonexistent(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	err := fake.KillSession("nope")
	if err != tmux.ErrNoSession {
		t.Errorf("want ErrNoSession, got %v", err)
	}
}

func TestHasSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("exists", "/tmp")

	if !fake.HasSession("exists") {
		t.Error("want true")
	}
	if fake.HasSession("nope") {
		t.Error("want false")
	}
}

func TestSendKeysRaw(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("test", "/tmp")

	err := fake.SendKeysRaw("test", "C-u")
	if err != nil {
		t.Fatal(err)
	}

	keys := fake.RawKeysSent()
	if len(keys) != 1 || keys[0].Target != "test" || keys[0].Keys[0] != "C-u" {
		t.Errorf("unexpected raw keys: %+v", keys)
	}
}

func TestSendKeysRawMultiple(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("test", "/tmp")

	err := fake.SendKeysRaw("test", "C-u", "C-c")
	if err != nil {
		t.Fatal(err)
	}

	keys := fake.RawKeysSent()
	if len(keys) != 1 || len(keys[0].Keys) != 2 {
		t.Errorf("want 1 call with 2 keys, got %+v", keys)
	}
}
