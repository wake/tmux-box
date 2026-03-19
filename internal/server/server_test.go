package server_test

import (
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

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
		t.Errorf("expected tmux attach-session -t myapp, got %s %v", cmd, args)
	}
	// Should NOT have -f ignore-size in default mode
	for _, a := range args {
		if a == "ignore-size" {
			t.Error("default mode should not have ignore-size flag")
		}
	}
}

func TestBuildTerminalRelayTerminalFirst(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	cfg := config.Config{
		Terminal: config.TerminalConfig{SizingMode: "terminal-first"},
	}
	srv := server.New(cfg, db, fakeTmux, "")

	_, args, cleanup, err := srv.BuildTerminalRelay("myapp")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	foundFlag := false
	for i, a := range args {
		if a == "-f" && i+1 < len(args) && args[i+1] == "ignore-size" {
			foundFlag = true
			break
		}
	}
	if !foundFlag {
		t.Errorf("terminal-first mode should have -f ignore-size, got %v", args)
	}
}

func TestBuildTerminalRelayMinimalFirst(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	cfg := config.Config{
		Terminal: config.TerminalConfig{SizingMode: "minimal-first"},
	}
	srv := server.New(cfg, db, fakeTmux, "")

	_, args, cleanup, err := srv.BuildTerminalRelay("myapp")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// minimal-first should NOT have -f ignore-size
	for _, a := range args {
		if a == "ignore-size" {
			t.Error("minimal-first mode should not have ignore-size flag")
		}
	}
}

func TestRestoreWindowSizingWithSmallest(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	srv := server.New(config.Config{}, db, fakeTmux, "")
	srv.RestoreWindowSizing("test:0", "smallest")

	calls := fakeTmux.SetWindowOptionCalls()
	if len(calls) != 1 || calls[0].Value != "smallest" {
		t.Errorf("expected SetWindowOption value=smallest, got %v", calls)
	}
}

func TestRestoreWindowSizingCallsBothMethods(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := server.New(config.Config{}, db, fakeTmux, "")
	srv.RestoreWindowSizing("test:0", "latest")

	if len(fakeTmux.AutoResizeCalls()) != 1 || fakeTmux.AutoResizeCalls()[0] != "test:0" {
		t.Errorf("expected ResizeWindowAuto called with test:0, got %v", fakeTmux.AutoResizeCalls())
	}
	calls := fakeTmux.SetWindowOptionCalls()
	if len(calls) != 1 || calls[0].Target != "test:0" || calls[0].Option != "window-size" || calls[0].Value != "latest" {
		t.Errorf("expected SetWindowOption(test:0, window-size, latest), got %v", calls)
	}
}
