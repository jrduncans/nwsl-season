package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/jrduncans/nwsl-season/internal/app"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.FromEnvironment()

	db, err := cache.Open(context.Background(), cfg.DBPath)
	if err != nil {
		logger.Error("open cache database", "error", err, "db", cfg.DBPath)
		os.Exit(1)
	}
	defer db.Close()

	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: app.NewHandler(db),
	}

	logger.Info("starting HTTP server", "address", cfg.HTTPAddr, "data_dir", cfg.DataDir, "db", cfg.DBPath)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}
