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

	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/server"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
)

const (
	serverReadHeaderTimeout   = 5 * time.Second
	serverMinimumWriteTimeout = 30 * time.Second
	serverWriteTimeoutGrace   = 5 * time.Second
	serverIdleTimeout         = 60 * time.Second
	serverMaxHeaderBytes      = 1 << 20 // 1 MiB
	serverShutdownTimeout     = 30 * time.Second
	telemetryShutdownTimeout  = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := config.LoadEnvironmentFile(); err != nil {
		logger.Error("load configuration environment file", "error", err)
		os.Exit(1)
	}
	cfg, err := config.FromEnvironment()
	if err != nil {
		logger.Error("read configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	providers, err := telemetry.Configure(ctx, logger, "nwsl-season-server")
	if err != nil {
		logger.Error("configure OpenTelemetry", "error", err)
		os.Exit(1)
	}
	runErr := run(ctx, cfg, logger)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), telemetryShutdownTimeout)
	shutdownErr := providers.Shutdown(shutdownCtx)
	cancel()
	if runErr != nil {
		logger.Error("HTTP server stopped", "error", runErr)
		if shutdownErr != nil {
			logger.Warn("flush OpenTelemetry telemetry", "error", shutdownErr)
		}
		os.Exit(1)
	}
	if shutdownErr != nil {
		logger.Error("flush OpenTelemetry telemetry", "error", shutdownErr)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	srv, err := server.Build(ctx, cfg, server.Options{Logger: logger, StartScheduler: true})
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()
	srv.Start()
	httpServer := newHTTPServer(cfg.HTTPAddr, srv.Handler(), cfg.ForecastTimeout)
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- httpServer.ListenAndServe() }()

	logger.Info("starting HTTP server", "address", cfg.HTTPAddr, "data_dir", cfg.DataDir, "db", cfg.DBPath,
		"sync_season", cfg.SyncSeason, "sync_stage", cfg.SyncStage)
	select {
	case <-ctx.Done():
		logger.Info("shutting down HTTP server", "reason", ctx.Err())
	case err := <-serverErrors:
		srv.Stop()
		srv.Wait()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}

	srv.Stop()
	err = shutdownHTTPServer(httpServer)
	srv.Wait()
	if err != nil {
		return fmt.Errorf("gracefully shut down HTTP server: %w", err)
	}
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newHTTPServer applies the connection limits required when the listener is
// reachable without a proxy. The write deadline includes the forecast budget
// plus time to render and send its response, so both normal and comparison
// forecasts have one bounded request window.
func newHTTPServer(addr string, handler http.Handler, forecastTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		WriteTimeout:      max(serverMinimumWriteTimeout, forecastTimeout+serverWriteTimeoutGrace),
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}
}

func shutdownHTTPServer(server *http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
