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
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/qualification"
	"github.com/jrduncans/nwsl-season/internal/scenariorefresh"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

func main() {
	cfg, err := config.FromEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync: configuration: %v\n", err)
		os.Exit(1)
	}
	season := flag.String("season", cfg.SyncSeason, "NWSL season year to fetch")
	stage := flag.String("stage", cfg.SyncStage, "NWSL competition stage to fetch")
	baseURL := flag.String("base-url", asa.DefaultBaseURL, "ASA API base URL")
	dbPath := flag.String("db", cfg.DBPath, "SQLite cache database path")
	minInterval := flag.Duration("min-interval", cfg.SyncMinAttemptInterval, "skip if the same season and stage was attempted within this duration")
	force := flag.Bool("force", false, "bypass the minimum attempt interval")
	requireXG := flag.Bool("require-xg", false, "exit nonzero when fixtures sync but xG refresh fails")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	client := asa.Client{
		BaseURL: *baseURL,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.SyncTimeout)
	defer cancel()

	db, err := cache.Open(ctx, *dbPath)
	if err != nil {
		logger.Error("open cache database", "error", err, "db", *dbPath)
		os.Exit(1)
	}
	defer db.Close()

	service := syncer.Service{
		ASA:                  client,
		Store:                db,
		QualificationTimeout: cfg.QualificationBudget,
		ScenarioTimeout:      cfg.ScenarioBudget,
	}
	if rules, ok := competition.ForSeason(*season, *stage); ok {
		service.Qualification = qualification.Refresher{Store: db, Rules: rules, Budget: cfg.QualificationBudget, Progress: qualificationTelemetry(logger)}
		service.Scenarios = scenariorefresh.Refresher{Store: db, Rules: rules, Budget: cfg.ScenarioBudget, Progress: scenarioTelemetry(logger)}
	} else {
		logger.Warn("qualification unavailable: no configured season rules", "season", *season, "stage", *stage)
	}
	run, err := service.Run(ctx, syncer.RunOptions{
		Season:                 *season,
		Stage:                  *stage,
		MinimumAttemptInterval: *minInterval,
		Force:                  *force,
	})
	if err != nil {
		logger.Error("sync ASA cache", "error", err)
		os.Exit(1)
	}

	if run.Skipped {
		fmt.Printf("Skipped sync for %s %s because the cache was attempted recently.\n", *season, *stage)
		fmt.Printf("Last attempt: %s (%s).\n", run.FinishedAt.Format(time.RFC3339), run.Outcome)
		return
	}
	fmt.Printf("Synced %d games and %d teams for %s %s into %s.\n", run.GamesUpserted, run.TeamsUpserted, *season, *stage, *dbPath)
	fmt.Printf("Deleted %d stale games. Last successful sync: %s.\n", run.GamesDeleted, run.FinishedAt.Format(time.RFC3339))
	if run.XGRun != nil {
		fmt.Printf("xG refresh: %d available, %d unavailable (%d inserted, %d updated, %d unchanged).\n", run.XGRun.AvailableGames, run.XGRun.UnavailableGames, run.XGRun.RowsInserted, run.XGRun.RowsUpdated, run.XGRun.RowsUnchanged)
	}
	if run.XGError != "" {
		logger.Warn("fixture sync succeeded but xG refresh failed", "error", run.XGError)
		if *requireXG {
			os.Exit(1)
		}
	}
	if run.QualificationError != "" {
		logger.Warn("fixture sync succeeded but qualification refresh failed", "error", run.QualificationError)
	}
	if run.ScenarioError != "" {
		logger.Warn("fixture sync succeeded but scenario refresh failed", "error", run.ScenarioError)
	}
}

func qualificationTelemetry(logger *slog.Logger) func(qualification.Progress) {
	return func(value qualification.Progress) {
		logger.Info("qualification proof "+value.Phase,
			"team_id", value.TeamID, "achievement", value.Achievement.ID, "top_k", value.Achievement.TopK,
			"completed", value.Completed, "total", value.Total, "elapsed", value.Elapsed,
			"batch_elapsed", value.BatchElapsed, "status", value.Status, "method", value.Method, "no_help_state", value.NoHelpState)
	}
}

func scenarioTelemetry(logger *slog.Logger) func(scenariorefresh.Progress) {
	return func(value scenariorefresh.Progress) {
		logger.Info("clinching scenario "+value.Phase,
			"team_id", value.TeamID, "achievement", value.Achievement.ID, "top_k", value.Achievement.TopK,
			"completed", value.Completed, "total", value.Total, "elapsed", value.Elapsed,
			"batch_elapsed", value.BatchElapsed, "state", value.State)
	}
}
