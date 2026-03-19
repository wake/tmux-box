// internal/server/server_test.go
package server_test

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func TestRestoreWindowSizingCallsBothMethods(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := server.New(config.Config{}, db, fakeTmux, "")
	srv.RestoreWindowSizing("test:0")

	if len(fakeTmux.AutoResizeCalls()) != 1 || fakeTmux.AutoResizeCalls()[0] != "test:0" {
		t.Errorf("expected ResizeWindowAuto called with test:0, got %v", fakeTmux.AutoResizeCalls())
	}
	calls := fakeTmux.SetWindowOptionCalls()
	if len(calls) != 1 || calls[0].Target != "test:0" || calls[0].Option != "window-size" || calls[0].Value != "latest" {
		t.Errorf("expected SetWindowOption(test:0, window-size, latest), got %v", calls)
	}
}

func TestBuildTerminalRelayWithSessionGroup(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	sgTrue := true
	cfg := config.Config{
		Terminal: config.TerminalConfig{SessionGroup: &sgTrue},
	}
	srv := server.New(cfg, db, fakeTmux, "")

	cmd, args, cleanup, err := srv.BuildTerminalRelay("myapp")
	if err != nil {
		t.Fatal(err)
	}

	if cmd != "tmux" {
		t.Errorf("expected cmd=tmux, got %s", cmd)
	}
	if args[0] != "attach-session" {
		t.Errorf("expected args[0]=attach-session, got %s", args[0])
	}
	// args[2] should be the grouped session name matching pattern
	relaySession := args[2]
	matched, _ := regexp.MatchString(`^myapp-tbox-[0-9a-f]{8}$`, relaySession)
	if !matched {
		t.Errorf("expected relay session matching myapp-tbox-{hex8}, got %s", relaySession)
	}
	// grouped session should exist in tmux
	if !fakeTmux.HasSession(relaySession) {
		t.Error("expected grouped session to be created")
	}
	// cleanup should kill it
	cleanup()
	if fakeTmux.HasSession(relaySession) {
		t.Error("expected grouped session to be killed after cleanup")
	}
}

func TestBuildTerminalRelayWithoutSessionGroup(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	srv := server.New(config.Config{}, db, fakeTmux, "")

	cmd, args, cleanup, err := srv.BuildTerminalRelay("myapp")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if cmd != "tmux" || args[0] != "attach-session" || args[1] != "-t" || args[2] != "myapp" {
		t.Errorf("expected attach-session -t myapp, got %s %v", cmd, args)
	}
}

func TestCleanupStaleRelays(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")
	fakeTmux.AddSession("myapp-tbox-1a2b3c4d", "/tmp")  // stale relay
	fakeTmux.AddSession("myapp-tbox-deadbeef", "/tmp")   // stale relay
	fakeTmux.AddSession("work", "/tmp")                   // normal session
	fakeTmux.AddSession("my-tbox-project", "/tmp")        // NOT a relay (no hex suffix)

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	srv := server.New(config.Config{}, db, fakeTmux, "")
	srv.CleanupStaleRelays()

	if fakeTmux.HasSession("myapp-tbox-1a2b3c4d") {
		t.Error("expected stale relay myapp-tbox-1a2b3c4d to be cleaned up")
	}
	if fakeTmux.HasSession("myapp-tbox-deadbeef") {
		t.Error("expected stale relay myapp-tbox-deadbeef to be cleaned up")
	}
	if !fakeTmux.HasSession("myapp") {
		t.Error("expected normal session myapp to survive cleanup")
	}
	if !fakeTmux.HasSession("work") {
		t.Error("expected normal session work to survive cleanup")
	}
	if !fakeTmux.HasSession("my-tbox-project") {
		t.Error("expected non-relay session my-tbox-project to survive cleanup")
	}
}
