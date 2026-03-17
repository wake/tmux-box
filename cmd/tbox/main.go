// cmd/tbox/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/relay"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: tbox <command> [flags]\n")
		fmt.Fprintf(os.Stderr, "Commands: serve, relay\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "relay":
		runRelay(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml (default: ~/.config/tbox/config.toml)")
	bindOverride := fs.String("bind", "", "override bind address")
	portOverride := fs.Int("port", 0, "override port")
	fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *bindOverride != "" {
		cfg.Bind = *bindOverride
	}
	if *portOverride != 0 {
		cfg.Port = *portOverride
	}

	os.MkdirAll(cfg.DataDir, 0755)

	st, err := store.Open(filepath.Join(cfg.DataDir, "state.db"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	tx := tmux.NewRealExecutor()

	s := server.New(cfg, st, tx)

	// Context for background goroutines (status poller).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.StartStatusPoller(ctx)

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nshutting down...")
		cancel() // stop status poller
		srv.Shutdown(context.Background())
	}()

	log.Printf("tbox daemon listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server error: %v", err)
	}
}

func runRelay(args []string) {
	fs := flag.NewFlagSet("relay", flag.ExitOnError)
	session := fs.String("session", "", "session name (required)")
	daemon := fs.String("daemon", "ws://127.0.0.1:7860", "daemon WebSocket address")
	fs.Parse(args)

	if *session == "" {
		fmt.Fprintln(os.Stderr, "relay: --session is required")
		os.Exit(1)
	}

	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "relay: no command specified after flags")
		os.Exit(1)
	}

	token := os.Getenv("TBOX_TOKEN")
	wsURL := fmt.Sprintf("%s/ws/cli-bridge/%s", *daemon, *session)

	r := &relay.Relay{
		SessionName: *session,
		DaemonURL:   wsURL,
		Token:       token,
		Command:     cmdArgs,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := r.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "relay: %v\n", err)
		os.Exit(1)
	}
}
