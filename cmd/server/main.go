package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/jrduncans/nwsl-season/internal/app"
	"github.com/jrduncans/nwsl-season/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.FromEnvironment()

	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: app.NewHandler(),
	}

	logger.Info("starting HTTP server", "address", cfg.HTTPAddr, "data_dir", cfg.DataDir)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}
