package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	"github.com/jrduncans/nwsl-season/internal/scheduler"
	"github.com/jrduncans/nwsl-season/internal/syncer"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
)

var ensureSourceScopeRegistry = func(ctx context.Context, db *cache.DB, season, stage string, now time.Time) error {
	_, err := db.EnsureSourceScopes(ctx, season, stage, now)
	return err
}

func main() {
	os.Exit(run())
}

func run() (exitCode int) {
	if err := config.LoadEnvironmentFile(); err != nil {
		fmt.Fprintf(os.Stderr, "sync: load configuration environment file: %v\n", err)
		return 1
	}
	cfg, err := config.FromEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync: configuration: %v\n", err)
		return 1
	}
	season := flag.String("season", cfg.SyncSeason, "NWSL season year to fetch")
	stage := flag.String("stage", cfg.SyncStage, "NWSL competition stage to fetch")
	baseURL := flag.String("base-url", asa.DefaultBaseURL, "ASA API base URL")
	dbPath := flag.String("db", cfg.DBPath, "SQLite cache database path")
	recalculate := flag.Bool("recalculate", false, "recalculate qualification and clinching scenarios from cached fixtures without syncing ASA data")
	force := flag.Bool("force", false, "force all clinching calculations after synchronizing source data")
	requireXG := flag.Bool("require-xg", false, "exit nonzero when fixtures sync but xG refresh fails")
	backfillHistorical := flag.Bool("backfill-historical", false, "sequentially refresh every supported historical regular season")
	sweepDueArchived := flag.Bool("sweep-due-archived", false, "sequentially refresh currently due archived correction resources")
	pruneHistoryBefore := flag.String("prune-history-before", "", "delete superseded run history finished before this RFC 3339 timestamp, then exit")
	flag.Parse()
	maintenanceModes := 0
	for _, enabled := range []bool{*recalculate, *backfillHistorical, *sweepDueArchived, *pruneHistoryBefore != ""} {
		if enabled {
			maintenanceModes++
		}
	}
	if maintenanceModes > 1 {
		fmt.Fprintln(os.Stderr, "sync: maintenance modes are mutually exclusive")
		return 1
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	providers, err := telemetry.Configure(context.Background(), logger, "nwsl-season-sync")
	if err != nil {
		logger.Error("configure OpenTelemetry", "error", err)
		return 1
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := providers.Shutdown(shutdownCtx); err != nil {
			logger.Error("flush OpenTelemetry telemetry", "error", err)
			exitCode = 1
		}
	}()
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
		return 1
	}
	defer func() { _ = db.Close() }()
	if err := ensureSourceScopeRegistry(context.Background(), db, cfg.SyncSeason, cfg.SyncStage, time.Now().UTC()); err != nil {
		logger.Error("seed source scope registry", "error", err)
		return 1
	}
	if *pruneHistoryBefore != "" {
		cutoff, err := time.Parse(time.RFC3339, *pruneHistoryBefore)
		if err != nil {
			logger.Error("parse history prune cutoff", "error", err, "value", *pruneHistoryBefore)
			return 1
		}
		result, err := db.PruneHistory(context.Background(), cutoff)
		if err != nil {
			logger.Error("prune cache history", "error", err)
			return 1
		}
		fmt.Printf("Pruned history before %s: %d sync runs, %d xG sync runs, %d qualification runs (%d statuses), %d scenario runs (%d results), and %d expired sync leases.\n", cutoff.UTC().Format(time.RFC3339), result.SyncRuns, result.XGSyncRuns, result.QualificationRuns, result.QualificationStatuses, result.ScenarioRuns, result.ScenarioResults, result.ExpiredSyncLeases)
		return 0
	}
	service := syncer.Service{
		ASA:                  client,
		Store:                db,
		QualificationTimeout: cfg.QualificationBudget,
		ScenarioTimeout:      cfg.ScenarioBudget,
		HistoryRetention:     cfg.HistoryRetention,
	}
	expectedTeams, gamesPerTeam := 0, 0
	if rules, ok := competition.ForSeason(*season, *stage); ok {
		expectedTeams, gamesPerTeam = rules.ExpectedTeams, rules.GamesPerTeam
		service.Qualification = qualification.Refresher{Store: db, Rules: rules, Budget: cfg.QualificationBudget, Progress: operations.QualificationTelemetry(logger)}
		service.Scenarios = scenariorefresh.Refresher{Store: db, Rules: rules, Budget: cfg.ScenarioBudget, Progress: operations.ScenarioTelemetry(logger)}
	} else {
		if *recalculate {
			logger.Error("clinching recalculation unavailable: no configured season rules", "season", *season, "stage", *stage)
			return 1
		}
		logger.Warn("qualification unavailable: no configured season rules", "season", *season, "stage", *stage)
	}
	if *recalculate {
		run, err := service.Recalculate(context.Background(), syncer.RecalculateOptions{Season: *season, Stage: *stage, Force: *force, Trigger: "cli"})
		if err != nil {
			logger.Error("recalculate clinching data", "error", err)
			return 1
		}
		if run.QualificationError != "" {
			logger.Error("qualification recalculation failed", "error", run.QualificationError)
			return 1
		}
		if run.ScenarioError != "" {
			logger.Error("scenario recalculation failed", "error", run.ScenarioError)
			return 1
		}
		fmt.Printf("Checked clinching data for cached %s %s snapshot %s.\n", *season, *stage, run.FixtureSnapshotID)
		fmt.Printf("Qualification: %s. Scenarios: %s.\n", calculationOutcome(run.QualificationRecalculated), calculationOutcome(run.ScenarioRecalculated))
		printCalculationBudgetSummary(logger, db, run)
		return 0
	}
	if *backfillHistorical {
		if err := runHistoricalBackfill(historicalBackfillEntries(cfg.SyncSeason), cfg.SyncTimeout, service.Run, os.Stdout); err != nil {
			logger.Error("backfill historical ASA cache", "error", err)
			return 1
		}
		return 0
	}
	if *sweepDueArchived {
		maintenance, err := scheduler.New(db, service, scheduler.Config{
			Season: cfg.SyncSeason, Stage: cfg.SyncStage, ExpectedTeams: expectedTeams, GamesPerTeam: gamesPerTeam,
			CheckInterval: cfg.SyncCheckInterval, CompletionGrace: cfg.SyncCompletionGrace, Timeout: cfg.SyncTimeout,
		}, logger)
		if err != nil {
			logger.Error("create archived maintenance scheduler", "error", err)
			return 1
		}
		report, err := maintenance.SweepDueArchived(context.Background())
		for _, entry := range report.Entries {
			fmt.Printf("Archived %s %s %s: %s.\n", entry.Job.Kind, entry.Job.Operation.Season, entry.Job.Operation.Stage, maintenanceOutcome(entry.Material))
		}
		fmt.Printf("Archived sweep %s after %d request(s).\n", report.Reason, report.Requests)
		if report.EvidenceDirty {
			fmt.Println("Model-evaluation evidence requires regeneration for a materially corrected historical scope.")
		}
		if err != nil {
			logger.Error("sweep due archived resources", "reason", report.Reason, "error", err)
			return 1
		}
		if report.Deferred {
			logger.Warn("archived sweep deferred", "reason", report.Reason)
		}
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.SyncTimeout)
	defer cancel()
	run, err := service.Run(ctx, syncer.RunOptions{
		Season:        *season,
		Stage:         *stage,
		ExpectedTeams: expectedTeams,
		GamesPerTeam:  gamesPerTeam,
		Trigger:       "cli",
		Force:         *force,
	})
	if err != nil {
		logger.Error("sync ASA cache", "error", err)
		return 1
	}

	fmt.Printf("Synced %d games and %d teams for %s %s into %s.\n", run.GamesUpserted, run.TeamsUpserted, *season, *stage, *dbPath)
	fmt.Printf("Deleted %d stale games. Last successful sync: %s.\n", run.GamesDeleted, run.FinishedAt.Format(time.RFC3339))
	if run.XGRun != nil {
		fmt.Printf("xG refresh: %d available, %d unavailable (%d inserted, %d updated, %d unchanged).\n", run.XGRun.AvailableGames, run.XGRun.UnavailableGames, run.XGRun.RowsInserted, run.XGRun.RowsUpdated, run.XGRun.RowsUnchanged)
	}
	if run.XGError != "" {
		logger.Warn("fixture sync succeeded but xG refresh failed", "error", run.XGError)
		if *requireXG {
			return 1
		}
	}
	if run.QualificationError != "" {
		logger.Warn("fixture sync succeeded but qualification refresh failed", "error", run.QualificationError)
	}
	if run.ScenarioError != "" {
		logger.Warn("fixture sync succeeded but scenario refresh failed", "error", run.ScenarioError)
	}
	if run.HistoryPruneError != "" {
		logger.Warn("automatic cache history prune failed", "error", run.HistoryPruneError)
	}
	if run.HistoryPrune != nil && historyPruneCount(*run.HistoryPrune) > 0 {
		fmt.Printf("Automatically pruned %d history rows using the %s retention window.\n", historyPruneCount(*run.HistoryPrune), cfg.HistoryRetention)
	}
	if run.QualificationRecalculated || run.ScenarioRecalculated {
		printCalculationBudgetSummary(logger, db, run)
	}
	return 0
}

func historicalBackfillEntries(configuredSeason string) []competition.Entry {
	entries := make([]competition.Entry, 0)
	for _, entry := range competition.PublicEntries() {
		if entry.Season == configuredSeason || entry.Stage != "Regular Season" || !entry.SourceAvailable || entry.Rules != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func runHistoricalBackfill(entries []competition.Entry, timeout time.Duration, run func(context.Context, syncer.RunOptions) (cache.SyncRun, error), output io.Writer) error {
	for _, entry := range entries {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		result, err := run(ctx, syncer.RunOptions{
			Season: entry.Season, Stage: entry.Stage, Trigger: "backfill", Force: true, SourceOnly: true,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("%s %s: %w", entry.Season, entry.Stage, err)
		}
		if result.XGError != "" {
			return fmt.Errorf("%s %s: xG refresh: %s", entry.Season, entry.Stage, result.XGError)
		}
		if _, err := fmt.Fprintf(output, "Backfilled %s %s: %d games", entry.Season, entry.Stage, result.GamesUpserted); err != nil {
			return fmt.Errorf("write backfill status: %w", err)
		}
		if result.XGRun != nil {
			if _, err := fmt.Fprintf(output, ", %d available xG and %d unavailable xG", result.XGRun.AvailableGames, result.XGRun.UnavailableGames); err != nil {
				return fmt.Errorf("write backfill xG status: %w", err)
			}
		}
		if _, err := fmt.Fprintln(output, "."); err != nil {
			return fmt.Errorf("write backfill completion: %w", err)
		}
	}
	return nil
}

func calculationOutcome(recalculated bool) string {
	if recalculated {
		return "recalculated"
	}
	return "already current"
}

func maintenanceOutcome(material bool) string {
	if material {
		return "material change"
	}
	return "no-op"
}

func historyPruneCount(result cache.HistoryPruneResult) int64 {
	return result.SyncRuns + result.XGSyncRuns + result.QualificationRuns + result.QualificationStatuses + result.ScenarioRuns + result.ScenarioResults + result.ExpiredSyncLeases
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
		if result.BudgetLimited() {
			summary.Scenarios++
		}
	}
	return summary
}
