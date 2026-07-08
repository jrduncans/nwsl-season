package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

func main() {
	cfg := config.FromEnvironment()
	season := flag.String("season", "2026", "NWSL season year to fetch")
	stage := flag.String("stage", "Regular Season", "NWSL competition stage to fetch")
	baseURL := flag.String("base-url", asa.DefaultBaseURL, "ASA API base URL")
	dbPath := flag.String("db", cfg.DBPath, "SQLite cache database path")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	client := asa.Client{
		BaseURL: *baseURL,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	db, err := cache.Open(ctx, *dbPath)
	if err != nil {
		logger.Error("open cache database", "error", err, "db", *dbPath)
		os.Exit(1)
	}
	defer db.Close()

	service := syncer.Service{
		ASA:   client,
		Store: db,
	}
	run, err := service.Run(ctx, syncer.RunOptions{
		Season: *season,
		Stage:  *stage,
	})
	if err != nil {
		logger.Error("sync ASA cache", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Synced %d games and %d teams for %s %s into %s.\n", run.GamesUpserted, run.TeamsUpserted, *season, *stage, *dbPath)
	fmt.Printf("Deleted %d stale games. Last successful sync: %s.\n", run.GamesDeleted, run.FinishedAt.Format(time.RFC3339))
}
