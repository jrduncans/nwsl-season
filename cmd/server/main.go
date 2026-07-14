package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jrduncans/nwsl-season/internal/app"
	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/scheduler"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.FromEnvironment()
	if err != nil {
		logger.Error("read configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	db, err := cache.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open cache database %q: %w", cfg.DBPath, err)
	}
	defer db.Close()

	service := syncer.Service{
		ASA:   asa.Client{HTTPClient: &http.Client{Timeout: cfg.SyncTimeout}},
		Store: db,
	}
	refreshScheduler, err := scheduler.New(db, service, scheduler.Config{
		Season: cfg.SyncSeason, Stage: cfg.SyncStage, CheckInterval: cfg.SyncCheckInterval,
		CompletionGrace: cfg.SyncCompletionGrace, MinimumAttemptInterval: cfg.SyncMinAttemptInterval,
		Timeout: cfg.SyncTimeout,
	}, logger)
	if err != nil {
		return fmt.Errorf("create refresh scheduler: %w", err)
	}
	refreshScheduler.Start()

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: app.NewHandlerWithOptions(db, app.Options{
			CurrentSeason: cfg.SyncSeason,
			Stage:         cfg.SyncStage,
		}),
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()

	logger.Info("starting HTTP server", "address", cfg.HTTPAddr, "data_dir", cfg.DataDir, "db", cfg.DBPath,
		"sync_season", cfg.SyncSeason, "sync_stage", cfg.SyncStage)
	select {
	case <-ctx.Done():
		logger.Info("shutting down HTTP server", "reason", ctx.Err())
	case err := <-serverErrors:
		refreshScheduler.Stop()
		refreshScheduler.Wait()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}

	refreshScheduler.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = server.Shutdown(shutdownCtx)
	cancel()
	refreshScheduler.Wait()
	if err != nil {
		return fmt.Errorf("gracefully shut down HTTP server: %w", err)
	}
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
