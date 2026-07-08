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
)

func main() {
	season := flag.String("season", "2026", "NWSL season year to fetch")
	stage := flag.String("stage", "Regular Season", "NWSL competition stage to fetch")
	baseURL := flag.String("base-url", asa.DefaultBaseURL, "ASA API base URL")
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

	games, err := client.Games(ctx, asa.GamesFilters{
		SeasonName: *season,
		StageName:  *stage,
	})
	if err != nil {
		logger.Error("fetch ASA games", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Fetched %d NWSL games for %s %s.\n", len(games), *season, *stage)
}
