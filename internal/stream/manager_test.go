// internal/stream/manager_test.go
package stream_test

import (
	"testing"

	"github.com/wake/tmux-box/internal/stream"
)

func TestManagerStartStop(t *testing.T) {
	mgr := stream.NewManager()
	defer mgr.StopAll()

	err := mgr.Start("test-1", "cat", []string{}, "/tmp")
	if err != nil {
		t.Fatal(err)
	}

	if !mgr.Has("test-1") {
		t.Error("should have test-1")
	}

	sess := mgr.Get("test-1")
	if sess == nil {
		t.Fatal("Get returned nil")
	}
	if !sess.Running() {
		t.Error("session should be running")
	}

	mgr.Stop("test-1")

	if mgr.Has("test-1") {
		t.Error("should not have test-1 after stop")
	}
}

func TestManagerDuplicateStart(t *testing.T) {
	mgr := stream.NewManager()
	defer mgr.StopAll()

	mgr.Start("dup", "cat", []string{}, "/tmp")
	err := mgr.Start("dup", "cat", []string{}, "/tmp")
	if err == nil {
		t.Error("want error for duplicate start")
	}
}

func TestManagerStopAll(t *testing.T) {
	mgr := stream.NewManager()

	mgr.Start("a", "cat", []string{}, "/tmp")
	mgr.Start("b", "cat", []string{}, "/tmp")

	mgr.StopAll()

	if mgr.Has("a") || mgr.Has("b") {
		t.Error("all sessions should be stopped")
	}
}
