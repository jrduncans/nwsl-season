// Package scheduler decides when the server should refresh its local ASA cache.
package scheduler

import (
	"context"
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
	Season                 string
	Stage                  string
	CheckInterval          time.Duration
	CompletionGrace        time.Duration
	MinimumAttemptInterval time.Duration
	Timeout                time.Duration
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
	for _, value := range []struct {
		name  string
		value time.Duration
	}{
		{"check interval", config.CheckInterval},
		{"completion grace", config.CompletionGrace},
		{"minimum attempt interval", config.MinimumAttemptInterval},
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
			attribute.String("sync.season", s.config.Season),
			attribute.String("sync.stage", s.config.Stage),
		),
	)
	defer span.End()

	snapshotCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	snapshot, err := s.store.RefreshSnapshot(snapshotCtx, s.config.Season, s.config.Stage)
	cancel()
	if err != nil {
		span.SetAttributes(
			attribute.String("scheduler.action", "read_snapshot"),
			attribute.String("scheduler.outcome", "failure"),
		)
		telemetry.RecordErrorWithCode(ctx, span, err, "scheduler.refresh_snapshot")
		s.logger.Error("cache refresh decision", "decision", "check_failed", "season", s.config.Season, "stage", s.config.Stage, "error", err)
		return
	}

	decision := Assess(snapshot, s.now().UTC(), s.config.CompletionGrace)
	span.SetAttributes(
		attribute.String("sync.decision", decision.Name),
		attribute.String("sync.decision_reason", decision.Reason),
		attribute.String("sync.fixture_id", decision.FixtureID),
	)
	s.logger.Info("cache refresh decision", "decision", decision.Name, "reason", decision.Reason,
		"season", s.config.Season, "stage", s.config.Stage, "fixture_id", decision.FixtureID)
	if decision.Name != decisionEligible {
		span.SetAttributes(
			attribute.String("scheduler.action", "recalculate"),
			attribute.String("scheduler.sync.outcome", "not_requested"),
			attribute.String("scheduler.forecast_warm.outcome", "not_needed"),
		)
		span.SetAttributes(attribute.String("scheduler.outcome", s.recalculateCachedClinching(ctx, span)))
		return
	}

	span.SetAttributes(attribute.String("scheduler.action", "sync"))
	runCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	run, err := s.runner.Run(runCtx, syncer.RunOptions{
		Season: s.config.Season, Stage: s.config.Stage, Trigger: "scheduler", MinimumAttemptInterval: s.config.MinimumAttemptInterval,
	})
	cancel()
	if err != nil {
		span.SetAttributes(
			attribute.String("scheduler.sync.outcome", "failure"),
			attribute.String("scheduler.outcome", "failure"),
		)
		telemetry.RecordErrorWithCode(ctx, span, err, "scheduler.refresh_run")
		s.logger.Error("cache refresh failed", "season", s.config.Season, "stage", s.config.Stage, "error", err)
		return
	}
	span.SetAttributes(schedulerRunAttributes(run)...)
	if run.Skipped {
		s.logger.Info("cache refresh decision", "decision", "rate_limited", "season", s.config.Season, "stage", s.config.Stage,
			"last_attempt", run.FinishedAt.UTC().Format(time.RFC3339), "last_outcome", run.Outcome)
		recalculateOutcome := s.recalculateCachedClinching(ctx, span)
		outcome := "rate_limited"
		if recalculateOutcome == "failure" || recalculateOutcome == "partial_failure" {
			outcome = "partial_failure"
		}
		span.SetAttributes(attribute.String("scheduler.outcome", outcome))
		return
	}
	if run.HistoryPruneError != "" {
		s.logger.Warn("automatic cache history prune failed", "season", s.config.Season, "stage", s.config.Stage, "error", run.HistoryPruneError)
	}
	s.logger.Info("cache refresh succeeded", "season", s.config.Season, "stage", s.config.Stage,
		"duration_ms", run.FinishedAt.Sub(run.StartedAt).Milliseconds(), "games_seen", run.GamesSeen,
		"games_inserted", run.GamesInserted, "games_updated", run.GamesUpdated, "games_unchanged", run.GamesUnchanged)
	if run.XGError != "" || run.QualificationError != "" || run.ScenarioError != "" {
		span.SetAttributes(attribute.String("scheduler.outcome", "partial_failure"))
		return
	}
	span.SetAttributes(attribute.String("scheduler.outcome", "synced"))
}

// recalculateCachedClinching repairs missing or retryable derived batches
// without an ASA request. In particular, it lets a restarted server recover a
// page that is pending only because the fixture cache was already current.
func (s *Scheduler) recalculateCachedClinching(parent context.Context, span trace.Span) string {
	runner, ok := s.runner.(calculationRunner)
	if !ok {
		span.SetAttributes(
			attribute.Bool("scheduler.recalculation.attempted", false),
			attribute.String("scheduler.recalculation.outcome", "unsupported"),
		)
		return "current"
	}
	span.SetAttributes(attribute.Bool("scheduler.recalculation.attempted", true))
	ctx, cancel := context.WithTimeout(parent, s.config.Timeout)
	run, err := runner.Recalculate(ctx, syncer.RecalculateOptions{Season: s.config.Season, Stage: s.config.Stage, Trigger: "scheduler"})
	cancel()
	if err != nil {
		span.SetAttributes(
			attribute.String("scheduler.recalculation.outcome", "failure"),
			attribute.String("scheduler.qualification.outcome", "not_run"),
			attribute.String("scheduler.scenario.outcome", "not_run"),
		)
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
		attribute.Bool("scheduler.sync.attempted", true),
		attribute.Bool("scheduler.sync.skipped", run.Skipped),
		attribute.String("scheduler.sync.outcome", schedulerSyncOutcome(run)),
		attribute.String("scheduler.xg.outcome", schedulerXGOutcome(run)),
		attribute.String("scheduler.qualification.outcome", schedulerComponentOutcome(run.QualificationRecalculated, run.QualificationError)),
		attribute.String("scheduler.scenario.outcome", schedulerComponentOutcome(run.ScenarioRecalculated, run.ScenarioError)),
	}
	if run.ID > 0 {
		attributes = append(attributes, attribute.Int64("scheduler.sync_run_id", run.ID))
	}
	if run.FixtureSnapshotID != "" {
		attributes = append(attributes, attribute.String("cache.fixture_snapshot_id", run.FixtureSnapshotID))
	}
	return attributes
}

func schedulerRecalculationAttributes(run cache.SyncRun) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String("scheduler.recalculation.outcome", schedulerRecalculationOutcome(run)),
		attribute.String("scheduler.qualification.outcome", schedulerComponentOutcome(run.QualificationRecalculated, run.QualificationError)),
		attribute.String("scheduler.scenario.outcome", schedulerComponentOutcome(run.ScenarioRecalculated, run.ScenarioError)),
	}
	if run.ID > 0 {
		attributes = append(attributes, attribute.Int64("scheduler.recalculation_source_sync_run_id", run.ID))
	}
	if run.FixtureSnapshotID != "" {
		attributes = append(attributes, attribute.String("cache.fixture_snapshot_id", run.FixtureSnapshotID))
	}
	return attributes
}

func schedulerSyncOutcome(run cache.SyncRun) string {
	if run.Skipped {
		return "rate_limited"
	}
	if run.XGError != "" || run.QualificationError != "" || run.ScenarioError != "" {
		return "partial_failure"
	}
	return "success"
}

func schedulerXGOutcome(run cache.SyncRun) string {
	if run.Skipped {
		return "not_run"
	}
	if run.XGError != "" {
		return "failure"
	}
	if run.XGRun != nil {
		return "complete"
	}
	return "not_requested"
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
func Assess(snapshot cache.RefreshSnapshot, now time.Time, completionGrace time.Duration) Decision {
	if snapshot.LastSuccess == nil {
		return Decision{Name: decisionEligible, Reason: "missing_successful_snapshot"}
	}
	if len(snapshot.Games) == 0 {
		return Decision{Name: decisionEligible, Reason: "empty_fixture_cache"}
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
	// publication lag can heal. The service's attempt interval still rate-limits
	// both cases.
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
