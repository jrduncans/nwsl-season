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
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/operations"
	"github.com/jrduncans/nwsl-season/internal/qualification"
	"github.com/jrduncans/nwsl-season/internal/scenariorefresh"
	"github.com/jrduncans/nwsl-season/internal/scheduler"
	"github.com/jrduncans/nwsl-season/internal/syncer"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
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

var ensureSourceScopeRegistry = func(ctx context.Context, db *cache.DB, season, stage string, now time.Time) error {
	_, err := db.EnsureSourceScopes(ctx, season, stage, now)
	return err
}

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
	db, err := cache.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open cache database %q: %w", cfg.DBPath, err)
	}
	defer func() { _ = db.Close() }()
	if err := ensureSourceScopeRegistry(ctx, db, cfg.SyncSeason, cfg.SyncStage, time.Now().UTC()); err != nil {
		return fmt.Errorf("seed source scope registry: %w", err)
	}

	service := syncer.Service{
		ASA: asa.Client{HTTPClient: &http.Client{
			Timeout: cfg.SyncTimeout,
		}},
		Store:                db,
		QualificationTimeout: cfg.QualificationBudget,
		ScenarioTimeout:      cfg.ScenarioBudget,
		HistoryRetention:     cfg.HistoryRetention,
	}
	rules, knownRules := competition.ForSeason(cfg.SyncSeason, cfg.SyncStage)
	if knownRules {
		service.Qualification = qualification.Refresher{Store: db, Rules: rules, Budget: cfg.QualificationBudget, Progress: operations.QualificationTelemetry(logger)}
		service.Scenarios = scenariorefresh.Refresher{Store: db, Rules: rules, Budget: cfg.ScenarioBudget, Progress: operations.ScenarioTelemetry(logger)}
	} else {
		logger.Warn("qualification unavailable: no configured season rules", "season", cfg.SyncSeason, "stage", cfg.SyncStage)
	}
	application := app.NewApplication(db, app.Options{
		CurrentSeason: cfg.SyncSeason,
		Stage:         cfg.SyncStage, Rules: rules,
		ForecastConcurrency: cfg.ForecastConcurrency,
		ForecastTimeout:     cfg.ForecastTimeout,
	})
	refreshScheduler, err := scheduler.New(db, forecastWarmingRunner{service: service, application: application, logger: logger, currentSeason: cfg.SyncSeason, currentStage: cfg.SyncStage}, scheduler.Config{
		Season: cfg.SyncSeason, Stage: cfg.SyncStage, ExpectedTeams: rules.ExpectedTeams, GamesPerTeam: rules.GamesPerTeam, CheckInterval: cfg.SyncCheckInterval,
		CompletionGrace: cfg.SyncCompletionGrace, Timeout: cfg.SyncTimeout,
	}, logger)
	if err != nil {
		return fmt.Errorf("create refresh scheduler: %w", err)
	}
	if err := application.PrecacheForecastsWithTrigger(ctx, "startup"); err != nil && ctx.Err() == nil {
		logger.Warn("pre-cache baseline forecasts", "season", cfg.SyncSeason, "stage", cfg.SyncStage, "error", err)
	} else if ctx.Err() == nil {
		logger.Info("pre-cached baseline forecasts", "season", cfg.SyncSeason, "stage", cfg.SyncStage)
	}
	refreshScheduler.Start()
	server := newHTTPServer(cfg.HTTPAddr, otelhttp.NewHandler(application, "HTTP server", otelhttp.WithSpanNameFormatter(httpSpanName)), cfg.ForecastTimeout)
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
	err = shutdownHTTPServer(server)
	refreshScheduler.Wait()
	if err != nil {
		return fmt.Errorf("gracefully shut down HTTP server: %w", err)
	}
	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func httpSpanName(_ string, request *http.Request) string {
	if request.Pattern != "" {
		return request.Pattern
	}
	return request.Method + " unknown_route"
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

// forecastWarmingRunner preserves the scheduler's normal sync behavior, then
// refreshes the process-local baseline forecast cache only when an input to a
// forecast actually changed.
type forecastWarmingRunner struct {
	service       syncer.Service
	application   *app.Application
	logger        *slog.Logger
	currentSeason string
	currentStage  string
}

func (r forecastWarmingRunner) Execute(ctx context.Context, operation syncer.Operation) (syncer.OperationResult, error) {
	result, err := r.service.Execute(ctx, operation)
	if err != nil {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeNotRun)
		return result, err
	}
	if !r.forecastInputsForScope(operation.Season, operation.Stage) || (!result.FixtureInputsChanged && !result.XGInputsChanged) {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeNotNeeded)
		return result, nil
	}
	if err := r.application.PrecacheForecastsWithTrigger(context.WithoutCancel(ctx), "post_source_job"); err != nil {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeFailed)
		r.logger.Warn("pre-cache forecasts after source job", "season", operation.Season, "stage", operation.Stage, "error", err)
		return result, nil
	}
	setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeComplete)
	return result, nil
}

func (r forecastWarmingRunner) forecastInputsForScope(season, stage string) bool {
	if stage != r.currentStage {
		return false
	}
	if season == r.currentSeason {
		return true
	}
	previous, err := competition.PreviousRegularSeasons(r.currentSeason, 2)
	if err != nil {
		return false
	}
	for _, candidate := range previous {
		if season == candidate {
			return true
		}
	}
	return false
}

func (r forecastWarmingRunner) Run(ctx context.Context, options syncer.RunOptions) (cache.SyncRun, error) {
	run, err := r.service.Run(ctx, options)
	if err != nil {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeNotRun)
		return run, err
	}
	if !forecastInputsChanged(run) {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeNotNeeded)
		return run, err
	}
	// Warm-up is independent from the source-request deadline. The forecast
	// executor applies its own per-model deadline, and a failure must not turn a
	// successful ASA cache transaction into a failed sync.
	if err := r.application.PrecacheForecastsWithTrigger(context.WithoutCancel(ctx), "post_sync"); err != nil {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeFailed)
		r.logger.Warn("pre-cache forecasts after data refresh", "season", options.Season, "stage", options.Stage, "error", err)
		return run, nil
	}
	setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeComplete)
	r.logger.Info("pre-cached forecasts after data refresh", "season", options.Season, "stage", options.Stage,
		"fixture_snapshot_id", run.FixtureSnapshotID)
	return run, nil
}

func setSchedulerForecastWarmOutcome(ctx context.Context, outcome string) {
	trace.SpanFromContext(ctx).SetAttributes(nwslconv.SchedulerForecastWarmOutcome(outcome))
}

func (r forecastWarmingRunner) Recalculate(ctx context.Context, options syncer.RecalculateOptions) (cache.SyncRun, error) {
	return r.service.Recalculate(ctx, options)
}

func forecastInputsChanged(run cache.SyncRun) bool {
	if run.TeamsInserted > 0 || run.TeamsUpdated > 0 || run.GamesInserted > 0 || run.GamesUpdated > 0 || run.GamesDeleted > 0 {
		return true
	}
	if run.XGRun == nil {
		return false
	}
	return run.XGRun.RowsInserted > 0 || run.XGRun.RowsUpdated > 0
}
