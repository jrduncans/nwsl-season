// Package scheduler decides when the server should refresh its local ASA cache.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/syncer"
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
	ctx, cancel := context.WithTimeout(context.Background(), s.config.Timeout)
	snapshot, err := s.store.RefreshSnapshot(ctx, s.config.Season, s.config.Stage)
	cancel()
	if err != nil {
		s.logger.Error("cache refresh decision", "decision", "check_failed", "season", s.config.Season, "stage", s.config.Stage, "error", err)
		return
	}

	decision := Assess(snapshot, s.now().UTC(), s.config.CompletionGrace)
	s.logger.Info("cache refresh decision", "decision", decision.Name, "reason", decision.Reason,
		"season", s.config.Season, "stage", s.config.Stage, "fixture_id", decision.FixtureID)
	if decision.Name != decisionEligible {
		return
	}

	runCtx, cancel := context.WithTimeout(context.Background(), s.config.Timeout)
	run, err := s.runner.Run(runCtx, syncer.RunOptions{
		Season: s.config.Season, Stage: s.config.Stage, MinimumAttemptInterval: s.config.MinimumAttemptInterval,
	})
	cancel()
	if err != nil {
		s.logger.Error("cache refresh failed", "season", s.config.Season, "stage", s.config.Stage, "error", err)
		return
	}
	if run.Skipped {
		s.logger.Info("cache refresh decision", "decision", "rate_limited", "season", s.config.Season, "stage", s.config.Stage,
			"last_attempt", run.FinishedAt.UTC().Format(time.RFC3339), "last_outcome", run.Outcome)
		return
	}
	s.logger.Info("cache refresh succeeded", "season", s.config.Season, "stage", s.config.Stage,
		"duration_ms", run.FinishedAt.Sub(run.StartedAt).Milliseconds(), "games_seen", run.GamesSeen,
		"games_inserted", run.GamesInserted, "games_updated", run.GamesUpdated, "games_unchanged", run.GamesUnchanged)
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
		kickoff, err := parseKickoff(game.KickoffUTC)
		if err != nil {
			return Decision{Name: decisionEligible, Reason: "invalid_kickoff", FixtureID: game.ASAID}
		}
		if settled(game) || now.Before(kickoff.Add(completionGrace)) {
			continue
		}
		return Decision{Name: decisionEligible, Reason: "plausibly_complete_fixture", FixtureID: game.ASAID}
	}
	return Decision{Name: decisionCurrent, Reason: "known_match_window_is_current"}
}

func knownStatus(status string) bool {
	switch status {
	case "FullTime", "PreMatch", "Abandoned":
		return true
	default:
		return false
	}
}

func settled(game cache.Game) bool {
	return game.Status == "FullTime" && game.HomeScore.Valid && game.AwayScore.Valid
}

func parseKickoff(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05 MST"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse kickoff %q", value)
}
