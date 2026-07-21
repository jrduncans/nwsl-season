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
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/operations"
	"github.com/jrduncans/nwsl-season/internal/qualification"
	"github.com/jrduncans/nwsl-season/internal/scenariorefresh"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
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
	recalculate := flag.Bool("recalculate", false, "recalculate qualification and clinching scenarios from cached fixtures without syncing ASA data")
	force := flag.Bool("force", false, "bypass the sync interval and force all clinching calculations")
	requireXG := flag.Bool("require-xg", false, "exit nonzero when fixtures sync but xG refresh fails")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	client := asa.Client{
		BaseURL: *baseURL,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}

	openCtx, cancelOpen := context.WithTimeout(context.Background(), 5*time.Second)
	db, err := cache.Open(openCtx, *dbPath)
	cancelOpen()
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
		service.Qualification = qualification.Refresher{Store: db, Rules: rules, Budget: cfg.QualificationBudget, Progress: operations.QualificationTelemetry(logger)}
		service.Scenarios = scenariorefresh.Refresher{Store: db, Rules: rules, Budget: cfg.ScenarioBudget, Progress: operations.ScenarioTelemetry(logger)}
	} else {
		if *recalculate {
			logger.Error("clinching recalculation unavailable: no configured season rules", "season", *season, "stage", *stage)
			os.Exit(1)
		}
		logger.Warn("qualification unavailable: no configured season rules", "season", *season, "stage", *stage)
	}
	if *recalculate {
		run, err := service.Recalculate(context.Background(), syncer.RecalculateOptions{Season: *season, Stage: *stage, Force: *force})
		if err != nil {
			logger.Error("recalculate clinching data", "error", err)
			os.Exit(1)
		}
		if run.QualificationError != "" {
			logger.Error("qualification recalculation failed", "error", run.QualificationError)
			os.Exit(1)
		}
		if run.ScenarioError != "" {
			logger.Error("scenario recalculation failed", "error", run.ScenarioError)
			os.Exit(1)
		}
		fmt.Printf("Checked clinching data for cached %s %s snapshot %s.\n", *season, *stage, run.FixtureSnapshotID)
		fmt.Printf("Qualification: %s. Scenarios: %s.\n", calculationOutcome(run.QualificationRecalculated), calculationOutcome(run.ScenarioRecalculated))
		printCalculationBudgetSummary(logger, db, run)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.SyncTimeout)
	defer cancel()
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
	if run.QualificationRecalculated || run.ScenarioRecalculated {
		printCalculationBudgetSummary(logger, db, run)
	}
}

func calculationOutcome(recalculated bool) string {
	if recalculated {
		return "recalculated"
	}
	return "already current"
}

type calculationBudgetSummary struct {
	Qualification int
	NoHelp        int
	Scenarios     int
}

func printCalculationBudgetSummary(logger *slog.Logger, db *cache.DB, run cache.SyncRun) {
	summary, err := calculationBudgetTimeouts(context.Background(), db, run.FixtureSnapshotID, run.Season, run.Stage)
	if err != nil {
		logger.Warn("inspect clinching calculation budget", "error", err)
		return
	}
	fmt.Printf("Incomplete due to calculation budget: %d qualification proof(s), %d no-help path(s), %d scenario check(s).\n", summary.Qualification, summary.NoHelp, summary.Scenarios)
}

func calculationBudgetTimeouts(ctx context.Context, db interface {
	QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
	ScenarioForSnapshot(context.Context, string, string, string) (cache.ScenarioSnapshot, bool, error)
}, snapshotID, season, stage string) (calculationBudgetSummary, error) {
	rules, ok := competition.ForSeason(season, stage)
	if !ok {
		return calculationBudgetSummary{}, fmt.Errorf("no competition rules for %s %s", season, stage)
	}
	qualification, found, err := db.QualificationForSnapshot(ctx, snapshotID, rules.Version)
	if err != nil {
		return calculationBudgetSummary{}, err
	}
	if !found {
		return calculationBudgetSummary{}, fmt.Errorf("qualification batch is unavailable")
	}
	summary := countCalculationBudgetTimeouts(qualification, cache.ScenarioSnapshot{})
	scenario, found, err := db.ScenarioForSnapshot(ctx, snapshotID, rules.Version, scenarios.DefinitionVersion)
	if err != nil {
		return calculationBudgetSummary{}, err
	}
	if !found {
		return summary, nil
	}
	return countCalculationBudgetTimeouts(qualification, scenario), nil
}

func countCalculationBudgetTimeouts(qualification cache.QualificationSnapshot, scenario cache.ScenarioSnapshot) calculationBudgetSummary {
	summary := calculationBudgetSummary{}
	for _, status := range qualification.Statuses {
		if status.Method == clinching.ProofComputeBudget {
			summary.Qualification++
		}
		if status.NoHelp.State == clinching.NoHelpUnresolved && status.NoHelp.Reason == "calculation budget exhausted" {
			summary.NoHelp++
		}
	}
	for _, result := range scenario.Results {
		if result.State == scenarios.OpportunityUnresolved && result.Limitation == "scenario computation budget exhausted" {
			summary.Scenarios++
		}
	}
	return summary
}
