// Command starstack runs the private cloud storage service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shiyi/starstack/internal/api"
	"github.com/shiyi/starstack/internal/config"
	"github.com/shiyi/starstack/internal/store"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cfg := config.FromEnv()
	log := newLogger(cfg.Log)
	log.Info("starstack starting", "version", version)
	if err := run(cfg, log); err != nil {
		log.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func newLogger(lc config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if lc.Level == "debug" {
		opts.Level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func run(cfg *config.Config, log *slog.Logger) error {
	st, err := store.Open(cfg.DataDir + "/starstack.db")
	if err != nil {
		return err
	}
	defer st.Close()

	// Background housekeeping.
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			_ = st.PruneSessions()
			_ = st.PruneShares()
		}
	}()

	svc, err := api.New(cfg, st, log)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           svc.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Info("starstack listening", "addr", cfg.Listen, "data", cfg.DataDir, "share", cfg.ShareRoot)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
