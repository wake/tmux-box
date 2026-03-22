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
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/module/session"
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

	// 1. Load config
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

	// 2. Open MetaStore (new — session module uses this)
	meta, err := store.OpenMeta(filepath.Join(cfg.DataDir, "meta.db"))
	if err != nil {
		log.Fatalf("meta store: %v", err)
	}
	defer meta.Close()

	// 3. Open legacy Store (kept for handoff/bridge until 1.6b)
	st, err := store.Open(filepath.Join(cfg.DataDir, "state.db"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// 4. Create tmux executor
	tx := tmux.NewRealExecutor()

	// 5. Create Core with config + tmux
	c := core.New(core.CoreDeps{
		Config: &cfg,
		Tmux:   tx,
	})

	// 6. Add session module to Core
	c.AddModule(session.NewSessionModule(meta))

	// 7. Init all modules (registers SessionProvider in ServiceRegistry)
	if err := c.InitModules(); err != nil {
		log.Fatalf("core init: %v", err)
	}

	// 8. Create shared http.ServeMux
	mux := http.NewServeMux()

	// 9. Register module routes (session: GET/POST /api/sessions, GET/DELETE /api/sessions/{code}, etc.)
	c.RegisterRoutes(mux)

	// 10. Create legacy server and register its routes on same mux
	resolvedCfgPath := *cfgPath
	if resolvedCfgPath == "" {
		resolvedCfgPath = filepath.Join(cfg.DataDir, "config.toml")
	}
	legacySrv := server.NewLegacy(cfg, resolvedCfgPath, st, tx)
	legacySrv.RegisterLegacyRoutes(mux)

	// Context for background goroutines (modules + status poller).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 11. Start modules (session module resets stale modes in MetaStore)
	if err := c.StartModules(ctx); err != nil {
		log.Fatalf("core start: %v", err)
	}

	// 12. Start status poller (on legacy server — uses legacy store)
	legacySrv.StartStatusPoller(ctx)

	// 13. Apply middleware chain and start HTTP server
	handler := server.CORS(
		server.IPWhitelist(cfg.Allow)(
			server.TokenAuth(cfg.Token)(mux)))

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nshutting down...")
		cancel() // stop status poller + modules
		c.StopModules()
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
	tokenFile := fs.String("token-file", "", "path to file containing auth token (read and deleted)")
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
		TokenFile:   *tokenFile,
		Command:     cmdArgs,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := r.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "relay: %v\n", err)
		os.Exit(1)
	}
}
