// internal/server/server_test.go
package server_test

import (
	"path/filepath"
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

func TestBuildTerminalRelayDefault(t *testing.T) {
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

func TestBuildTerminalRelayWithIgnoreSize(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	isTrue := true
	cfg := config.Config{
		Terminal: config.TerminalConfig{IgnoreSize: &isTrue},
	}
	srv := server.New(cfg, db, fakeTmux, "")

	_, args, cleanup, err := srv.BuildTerminalRelay("myapp")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// Should have -f ignore-size in args
	foundFlag := false
	for i, a := range args {
		if a == "-f" && i+1 < len(args) && args[i+1] == "ignore-size" {
			foundFlag = true
			break
		}
	}
	if !foundFlag {
		t.Errorf("expected -f ignore-size in args, got %v", args)
	}
}

