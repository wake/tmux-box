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
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func main() {
	configPath := flag.String("config", "", "path to config.toml (default: ~/.config/tbox/config.toml)")
	bind := flag.String("bind", "", "override bind address")
	port := flag.Int("port", 0, "override port")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *bind != "" {
		cfg.Bind = *bind
	}
	if *port != 0 {
		cfg.Port = *port
	}

	os.MkdirAll(cfg.DataDir, 0755)

	st, err := store.Open(filepath.Join(cfg.DataDir, "state.db"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	tx := tmux.NewRealExecutor()
	s := server.New(cfg, st, tx)

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
		srv.Shutdown(context.Background())
	}()

	log.Printf("tbox daemon listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server error: %v", err)
	}

	st.Close()
}
