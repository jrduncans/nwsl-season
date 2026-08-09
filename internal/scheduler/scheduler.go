// Package scheduler decides when the server should refresh its local ASA cache.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/syncer"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	decisionEligible = "eligible"
	decisionCurrent  = "current"
)

// SnapshotStore supplies the local data needed to decide whether to refresh.
type SnapshotStore interface {
	RefreshSnapshot(context.Context, string, string) (cache.RefreshSnapshot, error)
}

// Runner performs a complete atomic sync when the scheduler makes it eligible.
// Implementations own exception logging for returned errors; the scheduler
// marks its orchestration span without recording that same exception again.
type Runner interface {
	Run(context.Context, syncer.RunOptions) (cache.SyncRun, error)
}

// calculationRunner is optional so schedulers can still be used with runners
// that only synchronize source data. syncer.Service implements it to restore
// missing derived clinching batches from an otherwise current fixture cache.
type calculationRunner interface {
	Recalculate(context.Context, syncer.RecalculateOptions) (cache.SyncRun, error)
}

// Config configures one current-season scheduler.
type Config struct {
	Season          string
	Stage           string
	ExpectedTeams   int
	GamesPerTeam    int
	CheckInterval   time.Duration
	CompletionGrace time.Duration
	Timeout         time.Duration
}

// Decision records a local refresh decision. It deliberately excludes outcomes
// of actual network attempts, which are recorded in cache sync_runs instead.
type Decision struct {
	Name      string
	Reason    string
	FixtureID string
}

// Scheduler runs cache checks serially in one server process.
type Scheduler struct {
	store  SnapshotStore
	runner Runner
	config Config
	logger *slog.Logger
	now    func() time.Time

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// New constructs a scheduler. Start must be called once before Stop or Wait.
func New(store SnapshotStore, runner Runner, config Config, logger *slog.Logger) (*Scheduler, error) {
	if store == nil {
		return nil, fmt.Errorf("scheduler store is required")
	}
	if runner == nil {
		return nil, fmt.Errorf("scheduler runner is required")
	}
	if config.Season == "" || config.Stage == "" {
		return nil, fmt.Errorf("scheduler season and stage are required")
	}
	if err := validateScheduleConfig(config); err != nil {
		return nil, err
	}
	for _, value := range []struct {
		name  string
		value time.Duration
	}{
		{"check interval", config.CheckInterval},
		{"completion grace", config.CompletionGrace},
		{"timeout", config.Timeout},
	} {
		if value.value <= 0 {
			return nil, fmt.Errorf("scheduler %s must be positive", value.name)
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		store: store, runner: runner, config: config, logger: logger, now: time.Now,
		stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

// Start makes an immediate local decision, then checks on the configured ticker.
func (s *Scheduler) Start() {
	go func() {
		defer close(s.done)
		s.check()
		ticker := time.NewTicker(s.config.CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				select {
				case <-s.stop:
					return
				default:
				}
				s.check()
			}
		}
	}()
}

// Stop prevents future checks. It does not cancel an active bounded refresh.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// Wait blocks until the active check, if any, has finished.
func (s *Scheduler) Wait() { <-s.done }

func (s *Scheduler) check() {
	ctx, span := telemetry.Tracer().Start(context.Background(), "scheduler.check",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nwsl.season", s.config.Season),
			attribute.String("nwsl.stage", s.config.Stage),
		),
	)
	defer span.End()

	snapshotCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	snapshot, err := s.store.RefreshSnapshot(snapshotCtx, s.config.Season, s.config.Stage)
	cancel()
	if err != nil {
		span.SetAttributes(
			attribute.String("nwsl.scheduler.action", "read_snapshot"),
			attribute.String("nwsl.scheduler.outcome", "failure"),
		)
		telemetry.RecordWarningWithType(ctx, span, err, "scheduler.refresh_snapshot", telemetry.ErrorTypeStorageFailure)
		s.logger.Error("cache refresh decision", "decision", "check_failed", "season", s.config.Season, "stage", s.config.Stage, "error", err)
		return
	}

	decision := Assess(snapshot, s.now().UTC(), s.config.CompletionGrace, s.config.ExpectedTeams, s.config.GamesPerTeam)
	span.SetAttributes(
		attribute.String("nwsl.sync.decision", decision.Name),
		attribute.String("nwsl.sync.decision_reason", decision.Reason),
		attribute.String("nwsl.sync.fixture_id", decision.FixtureID),
		attribute.Int("nwsl.scheduler.cached_fixture_count", len(snapshot.Games)),
		attribute.Int("nwsl.scheduler.expected_fixture_count", expectedFixtureCount(s.config.ExpectedTeams, s.config.GamesPerTeam)),
	)
	s.logger.Info("cache refresh decision", "decision", decision.Name, "reason", decision.Reason,
		"season", s.config.Season, "stage", s.config.Stage, "fixture_id", decision.FixtureID)
	if decision.Name != decisionEligible {
		span.SetAttributes(
			attribute.String("nwsl.scheduler.action", "recalculate"),
			attribute.String("nwsl.scheduler.sync.outcome", "not_requested"),
			attribute.String("nwsl.scheduler.forecast_warm.outcome", "not_needed"),
		)
		span.SetAttributes(attribute.String("nwsl.scheduler.outcome", s.recalculateCachedClinching(ctx, span)))
		return
	}

	span.SetAttributes(attribute.String("nwsl.scheduler.action", "sync"))
	runCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	run, err := s.runner.Run(runCtx, syncer.RunOptions{
		Season: s.config.Season, Stage: s.config.Stage, ExpectedTeams: s.config.ExpectedTeams, GamesPerTeam: s.config.GamesPerTeam,
		TargetFixtureID: targetFixtureID(decision), Trigger: "scheduler",
	})
	cancel()
	if errors.Is(err, cache.ErrSyncInProgress) {
		span.SetAttributes(
			attribute.Bool("nwsl.error.expected", true),
			attribute.String("nwsl.scheduler.sync.outcome", "in_progress"),
			attribute.String("nwsl.scheduler.outcome", "deferred"),
		)
		s.logger.Info("cache refresh deferred", "reason", "sync_in_progress", "season", s.config.Season, "stage", s.config.Stage)
		return
	}
	if err != nil {
		span.SetAttributes(
			attribute.String("nwsl.scheduler.sync.outcome", "failure"),
			attribute.String("nwsl.scheduler.outcome", "failure"),
		)
		telemetry.MarkError(span, err)
		s.logger.Error("cache refresh failed", "season", s.config.Season, "stage", s.config.Stage, "error", err)
		return
	}
	span.SetAttributes(schedulerRunAttributes(run)...)
	if run.HistoryPruneError != "" {
		s.logger.Warn("automatic cache history prune failed", "season", s.config.Season, "stage", s.config.Stage, "error", run.HistoryPruneError)
	}
	s.logger.Info("cache refresh succeeded", "season", s.config.Season, "stage", s.config.Stage,
		"duration_ms", run.FinishedAt.Sub(run.StartedAt).Milliseconds(), "games_seen", run.GamesSeen,
		"games_inserted", run.GamesInserted, "games_updated", run.GamesUpdated, "games_unchanged", run.GamesUnchanged)
	if run.XGError != "" || run.QualificationError != "" || run.ScenarioError != "" {
		span.SetAttributes(attribute.String("nwsl.scheduler.outcome", "partial_failure"))
		return
	}
	span.SetAttributes(attribute.String("nwsl.scheduler.outcome", "synced"))
}

// recalculateCachedClinching repairs missing or retryable derived batches
// without an ASA request. In particular, it lets a restarted server recover a
// page that is pending only because the fixture cache was already current.
func (s *Scheduler) recalculateCachedClinching(parent context.Context, span trace.Span) string {
	runner, ok := s.runner.(calculationRunner)
	if !ok {
		span.SetAttributes(
			attribute.Bool("nwsl.scheduler.recalculation.attempted", false),
			attribute.String("nwsl.scheduler.recalculation.outcome", "unsupported"),
		)
		return "current"
	}
	span.SetAttributes(attribute.Bool("nwsl.scheduler.recalculation.attempted", true))
	ctx, cancel := context.WithTimeout(parent, s.config.Timeout)
	run, err := runner.Recalculate(ctx, syncer.RecalculateOptions{Season: s.config.Season, Stage: s.config.Stage, Trigger: "scheduler"})
	cancel()
	if err != nil {
		span.SetAttributes(
			attribute.String("nwsl.scheduler.recalculation.outcome", "failure"),
			attribute.String("nwsl.scheduler.qualification.outcome", "not_run"),
			attribute.String("nwsl.scheduler.scenario.outcome", "not_run"),
		)
		telemetry.MarkError(span, err)
		s.logger.Error("cached clinching recalculation failed", "season", s.config.Season, "stage", s.config.Stage, "error", err)
		return "failure"
	}
	span.SetAttributes(schedulerRecalculationAttributes(run)...)
	if run.QualificationError != "" || run.ScenarioError != "" {
		s.logger.Error("cached clinching recalculation failed", "season", s.config.Season, "stage", s.config.Stage,
			"qualification_error", run.QualificationError, "scenario_error", run.ScenarioError)
		return "partial_failure"
	}
	if run.QualificationRecalculated || run.ScenarioRecalculated {
		s.logger.Info("cached clinching recalculated", "season", s.config.Season, "stage", s.config.Stage,
			"qualification_recalculated", run.QualificationRecalculated, "scenario_recalculated", run.ScenarioRecalculated)
	}
	if run.QualificationRecalculated || run.ScenarioRecalculated {
		return "complete"
	}
	return "current"
}

func schedulerRunAttributes(run cache.SyncRun) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.Bool("nwsl.scheduler.sync.attempted", true),
		attribute.String("nwsl.scheduler.sync.outcome", schedulerSyncOutcome(run)),
		attribute.String("nwsl.scheduler.xg.outcome", schedulerXGOutcome(run)),
		attribute.String("nwsl.scheduler.qualification.outcome", schedulerComponentOutcome(run.QualificationRecalculated, run.QualificationError)),
		attribute.String("nwsl.scheduler.scenario.outcome", schedulerComponentOutcome(run.ScenarioRecalculated, run.ScenarioError)),
	}
	if run.ID > 0 {
		attributes = append(attributes, attribute.Int64("nwsl.scheduler.sync_run_id", run.ID))
	}
	if run.FixtureSnapshotID != "" {
		attributes = append(attributes, attribute.String("nwsl.cache.fixture_snapshot_id", run.FixtureSnapshotID))
	}
	return attributes
}

func schedulerRecalculationAttributes(run cache.SyncRun) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String("nwsl.scheduler.recalculation.outcome", schedulerRecalculationOutcome(run)),
		attribute.String("nwsl.scheduler.qualification.outcome", schedulerComponentOutcome(run.QualificationRecalculated, run.QualificationError)),
		attribute.String("nwsl.scheduler.scenario.outcome", schedulerComponentOutcome(run.ScenarioRecalculated, run.ScenarioError)),
	}
	if run.ID > 0 {
		attributes = append(attributes, attribute.Int64("nwsl.scheduler.recalculation_source_sync_run_id", run.ID))
	}
	if run.FixtureSnapshotID != "" {
		attributes = append(attributes, attribute.String("nwsl.cache.fixture_snapshot_id", run.FixtureSnapshotID))
	}
	return attributes
}

func schedulerSyncOutcome(run cache.SyncRun) string {
	if run.XGError != "" || run.QualificationError != "" || run.ScenarioError != "" {
		return "partial_failure"
	}
	return "success"
}

func schedulerXGOutcome(run cache.SyncRun) string {
	if run.XGError != "" {
		return "failure"
	}
	if run.XGRun != nil {
		return "complete"
	}
	return "not_requested"
}

func targetFixtureID(decision Decision) string {
	if decision.Reason == "plausibly_complete_fixture" {
		return decision.FixtureID
	}
	return ""
}

func schedulerRecalculationOutcome(run cache.SyncRun) string {
	if run.QualificationError != "" || run.ScenarioError != "" {
		return "partial_failure"
	}
	if run.QualificationRecalculated || run.ScenarioRecalculated {
		return "complete"
	}
	return "current"
}

func schedulerComponentOutcome(completed bool, failure string) string {
	if failure != "" {
		return "failure"
	}
	if completed {
		return "complete"
	}
	return "current"
}

// Assess determines whether the cached match window needs an ASA request.
func Assess(snapshot cache.RefreshSnapshot, now time.Time, completionGrace time.Duration, expectedTeams, gamesPerTeam int) Decision {
	if snapshot.LastSuccess == nil {
		return Decision{Name: decisionEligible, Reason: "missing_successful_snapshot"}
	}
	if len(snapshot.Games) == 0 {
		return Decision{Name: decisionEligible, Reason: "empty_fixture_cache"}
	}
	if !hasExpectedFixtureInventory(snapshot.Games, expectedTeams, gamesPerTeam) {
		return Decision{Name: decisionEligible, Reason: "incomplete_fixture_cache"}
	}
	for _, game := range snapshot.Games {
		if !knownStatus(game.Status) {
			return Decision{Name: decisionEligible, Reason: "unsupported_status", FixtureID: game.ASAID}
		}
		kickoff, err := fixtures.ParseKickoff(game.KickoffUTC)
		if err != nil {
			return Decision{Name: decisionEligible, Reason: "invalid_kickoff", FixtureID: game.ASAID}
		}
		if settled(game) || now.Before(kickoff.Add(completionGrace)) {
			continue
		}
		return Decision{Name: decisionEligible, Reason: "plausibly_complete_fixture", FixtureID: game.ASAID}
	}
	// A migrated fixture cache has no xG audit row yet. Treat that as an
	// incomplete snapshot, then keep retrying explicit unavailable games so ASA
	// publication lag can heal on a later scheduler check.
	if snapshot.XGStatus.LastSuccess == nil {
		return Decision{Name: decisionEligible, Reason: "missing_successful_xg_snapshot"}
	}
	if snapshot.XGStatus.LastAttempt != nil {
		byID := map[string]cache.GameXG{}
		for _, value := range snapshot.XGoals {
			byID[value.GameID] = value
		}
		for _, game := range snapshot.Games {
			if game.Status != fixtures.CompletedStatus {
				continue
			}
			value, ok := byID[game.ASAID]
			if !ok || value.Availability == cache.XGUnavailable {
				return Decision{Name: decisionEligible, Reason: "xg_unavailable", FixtureID: game.ASAID}
			}
		}
	}
	return Decision{Name: decisionCurrent, Reason: "known_match_window_is_current"}
}

func validateScheduleConfig(config Config) error {
	if config.ExpectedTeams == 0 && config.GamesPerTeam == 0 {
		return nil
	}
	if config.ExpectedTeams < 1 || config.GamesPerTeam < 1 || config.ExpectedTeams*config.GamesPerTeam%2 != 0 {
		return errors.New("scheduler expected teams and games per team must describe an even schedule")
	}
	return nil
}

func hasExpectedFixtureInventory(games []cache.Game, expectedTeams, gamesPerTeam int) bool {
	if expectedTeams == 0 && gamesPerTeam == 0 {
		return true
	}
	if len(games) != expectedTeams*gamesPerTeam/2 {
		return false
	}
	appearances := make(map[string]int, expectedTeams)
	for _, game := range games {
		appearances[game.HomeTeamID]++
		appearances[game.AwayTeamID]++
	}
	if len(appearances) != expectedTeams {
		return false
	}
	for _, count := range appearances {
		if count != gamesPerTeam {
			return false
		}
	}
	return true
}

func expectedFixtureCount(expectedTeams, gamesPerTeam int) int {
	if expectedTeams < 1 || gamesPerTeam < 1 || expectedTeams*gamesPerTeam%2 != 0 {
		return 0
	}
	return expectedTeams * gamesPerTeam / 2
}

func knownStatus(status string) bool {
	switch status {
	case fixtures.CompletedStatus, fixtures.PreMatchStatus, fixtures.AbandonedStatus:
		return true
	default:
		return false
	}
}

func settled(game cache.Game) bool {
	return game.Status == fixtures.CompletedStatus && game.HomeScore.Valid && game.AwayScore.Valid
}
