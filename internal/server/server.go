// Package server composes the cache, ASA syncer, derived-data refreshers,
// refresh scheduler, and page handler into one process-local server. The
// server command and in-process tests build the same composition, so tests can
// point it at a fake ASA and drive scheduler work synchronously.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
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
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Options adjusts how Build composes the server. The zero value matches
// production except that the scheduler does not start.
type Options struct {
	// Logger receives server and scheduler logs. Nil uses slog.Default.
	Logger *slog.Logger
	// ASABaseURL overrides cfg.ASABaseURL when non-empty.
	ASABaseURL string
	// ASAHTTPClient overrides the ASA HTTP client. Nil uses a client whose
	// timeout is cfg.SyncTimeout.
	ASAHTTPClient *http.Client
	// StartScheduler makes Start run the background refresh scheduler. When
	// false, the server reads only the local cache unless CheckNow is called.
	StartScheduler bool
	// Now overrides the scheduler's planning clock. Nil uses time.Now.
	Now func() time.Time
}

// Server is a built, not yet serving, composition of the application. The
// caller owns the HTTP listener; Server owns the cache and the scheduler.
type Server struct {
	db             *cache.DB
	handler        http.Handler
	scheduler      *scheduler.Scheduler
	startScheduler bool

	mu      sync.Mutex
	started bool

	closeOnce sync.Once
	closeErr  error
}

// ensureSourceScopeRegistry is a variable so tests can simulate a seeding
// failure without corrupting a real cache.
var ensureSourceScopeRegistry = func(ctx context.Context, db *cache.DB, season, stage string, now time.Time) error {
	_, err := db.EnsureSourceScopes(ctx, season, stage, now)
	return err
}

// Build opens the cache, seeds the source-scope registry, wires the syncer,
// refreshers, application, and scheduler, and pre-caches baseline forecasts.
// It makes no ASA request. On error, it closes anything it opened.
func Build(ctx context.Context, cfg config.Config, opts Options) (*Server, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	db, err := cache.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open cache database %q: %w", cfg.DBPath, err)
	}
	server, err := build(ctx, db, cfg, opts, logger)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return server, nil
}

func build(ctx context.Context, db *cache.DB, cfg config.Config, opts Options, logger *slog.Logger) (*Server, error) {
	if err := ensureSourceScopeRegistry(ctx, db, cfg.SyncSeason, cfg.SyncStage, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("seed source scope registry: %w", err)
	}

	baseURL := cfg.ASABaseURL
	if opts.ASABaseURL != "" {
		baseURL = opts.ASABaseURL
	}
	httpClient := opts.ASAHTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.SyncTimeout}
	}
	service := syncer.Service{
		ASA:                  asa.Client{BaseURL: baseURL, HTTPClient: httpClient},
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
		CompletionGrace: cfg.SyncCompletionGrace, Timeout: cfg.SyncTimeout, Now: opts.Now,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("create refresh scheduler: %w", err)
	}
	if err := application.PrecacheForecastsWithTrigger(ctx, "startup"); err != nil && ctx.Err() == nil {
		logger.Warn("pre-cache baseline forecasts", "season", cfg.SyncSeason, "stage", cfg.SyncStage, "error", err)
	} else if ctx.Err() == nil {
		logger.Info("pre-cached baseline forecasts", "season", cfg.SyncSeason, "stage", cfg.SyncStage)
	}
	return &Server{
		db:             db,
		handler:        otelhttp.NewHandler(application, "HTTP server", otelhttp.WithSpanNameFormatter(httpSpanName)),
		scheduler:      refreshScheduler,
		startScheduler: opts.StartScheduler,
	}, nil
}

// Handler returns the instrumented application handler. It reads only the
// local cache.
func (s *Server) Handler() http.Handler { return s.handler }

// Start begins background scheduler work when Options.StartScheduler was set.
// It is safe to call more than once; only the first call has an effect.
func (s *Server) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || !s.startScheduler {
		return
	}
	s.started = true
	s.scheduler.Start()
}

// Stop cancels scheduler work, including in-flight source requests and
// detached calculations. It does not close the cache.
func (s *Server) Stop() { s.scheduler.Stop() }

// Wait blocks until a started scheduler has finished its in-flight work. It
// returns immediately when the scheduler never started.
func (s *Server) Wait() {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if started {
		s.scheduler.Wait()
	}
}

// Close releases the cache. Call it after Stop, Wait, and HTTP shutdown.
func (s *Server) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.db.Close() })
	return s.closeErr
}

// CheckNow runs one scheduler check synchronously, as a manual maintenance
// trigger, and returns after its source jobs, follow-up clinching
// calculations, and forecast warming finish. It works whether or not the
// background scheduler was started.
func (s *Server) CheckNow(ctx context.Context) error {
	return s.scheduler.CheckNow(ctx)
}

func httpSpanName(_ string, request *http.Request) string {
	if request.Pattern != "" {
		return request.Pattern
	}
	return request.Method + " unknown_route"
}
