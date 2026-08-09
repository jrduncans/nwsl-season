package scheduler

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

func TestAssess(t *testing.T) {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	success := &cache.SyncRun{FinishedAt: now.Add(-time.Hour)}
	xgSuccess := &cache.XGSyncRun{FinishedAt: now.Add(-time.Hour), Outcome: "success"}
	final := schedulerGame("final", "2026-07-13 12:00:00 UTC", "FullTime", true, true)

	for _, test := range []struct {
		name       string
		snapshot   cache.RefreshSnapshot
		wantName   string
		wantReason string
		wantID     string
	}{
		{
			name:       "missing successful snapshot",
			snapshot:   cache.RefreshSnapshot{Games: []cache.Game{final}},
			wantName:   decisionEligible,
			wantReason: "missing_successful_snapshot",
		},
		{
			name:       "empty fixtures",
			snapshot:   cache.RefreshSnapshot{LastSuccess: success},
			wantName:   decisionEligible,
			wantReason: "empty_fixture_cache",
		},
		{
			name: "stale pre-match fixture",
			snapshot: cache.RefreshSnapshot{LastSuccess: success, Games: []cache.Game{
				schedulerGame("stale", "2026-07-13 15:00:00 UTC", "PreMatch", false, false),
			}},
			wantName: decisionEligible, wantReason: "plausibly_complete_fixture", wantID: "stale",
		},
		{
			name: "full time without scores",
			snapshot: cache.RefreshSnapshot{LastSuccess: success, Games: []cache.Game{
				schedulerGame("missing-score", "2026-07-13 15:00:00 UTC", "FullTime", false, false),
			}},
			wantName: decisionEligible, wantReason: "plausibly_complete_fixture", wantID: "missing-score",
		},
		{
			name: "invalid kickoff",
			snapshot: cache.RefreshSnapshot{LastSuccess: success, Games: []cache.Game{
				schedulerGame("invalid-time", "not-a-time", "PreMatch", false, false),
			}},
			wantName: decisionEligible, wantReason: "invalid_kickoff", wantID: "invalid-time",
		},
		{
			name: "unsupported status",
			snapshot: cache.RefreshSnapshot{LastSuccess: success, Games: []cache.Game{
				schedulerGame("unknown", "2026-07-20 15:00:00 UTC", "Delayed", false, false),
			}},
			wantName: decisionEligible, wantReason: "unsupported_status", wantID: "unknown",
		},
		{
			name:     "missing successful xg snapshot",
			snapshot: cache.RefreshSnapshot{LastSuccess: success, Games: []cache.Game{final}},
			wantName: decisionEligible, wantReason: "missing_successful_xg_snapshot",
		},
		{
			name: "current known window",
			snapshot: cache.RefreshSnapshot{LastSuccess: success, XGStatus: cache.XGStatus{LastSuccess: xgSuccess}, Games: []cache.Game{
				final,
				schedulerGame("future", "2026-07-20 15:00:00 UTC", "PreMatch", false, false),
			}},
			wantName: decisionCurrent, wantReason: "known_match_window_is_current",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := Assess(test.snapshot, now, 3*time.Hour, 0, 0)
			if got.Name != test.wantName || got.Reason != test.wantReason || got.FixtureID != test.wantID {
				t.Fatalf("Assess() = %+v, want name=%q reason=%q fixture=%q", got, test.wantName, test.wantReason, test.wantID)
			}
		})
	}
}

func TestAssessTreatsAnIncompleteKnownScheduleAsEligible(t *testing.T) {
	now := time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC)
	snapshot := cache.RefreshSnapshot{
		LastSuccess: &cache.SyncRun{FinishedAt: now.Add(-time.Hour)},
		Games: []cache.Game{
			{ASAID: "only-fixture", HomeTeamID: "home", AwayTeamID: "away", KickoffUTC: now.Add(24 * time.Hour).Format(time.RFC3339), Status: "PreMatch"},
		},
	}

	got := Assess(snapshot, now, 2*time.Hour, 2, 2)
	if got.Name != decisionEligible || got.Reason != "incomplete_fixture_cache" {
		t.Fatalf("Assess() = %+v, want incomplete fixture cache eligibility", got)
	}
}

func TestSchedulerRunsImmediatelyAndStops(t *testing.T) {
	now := time.Now().UTC()
	store := schedulerStore{snapshot: cache.RefreshSnapshot{
		LastSuccess: &cache.SyncRun{FinishedAt: now},
		Games:       []cache.Game{schedulerGame("stale", now.Add(-4*time.Hour).Format(time.RFC3339), "PreMatch", false, false)},
	}}
	runner := &schedulerRunner{called: make(chan struct{}, 1)}
	s, err := New(store, runner, Config{
		Season: "2026", Stage: "Regular Season", CheckInterval: time.Hour,
		CompletionGrace: 3 * time.Hour, Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	select {
	case <-runner.called:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not run its startup check")
	}
	s.Stop()
	s.Wait()
	if runner.calls.Load() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls.Load())
	}
	if got := runner.lastOptions.TargetFixtureID; got != "stale" {
		t.Fatalf("target fixture = %q, want stale", got)
	}
}

func TestSchedulerRecalculatesWhenFixtureCacheIsCurrent(t *testing.T) {
	now := time.Now().UTC()
	store := schedulerStore{snapshot: cache.RefreshSnapshot{
		LastSuccess: &cache.SyncRun{FinishedAt: now},
		XGStatus:    cache.XGStatus{LastSuccess: &cache.XGSyncRun{FinishedAt: now, Outcome: "success"}},
		Games:       []cache.Game{schedulerGame("future", now.Add(time.Hour).Format(time.RFC3339), "PreMatch", false, false)},
	}}
	runner := &recalculatingSchedulerRunner{schedulerRunner: schedulerRunner{called: make(chan struct{}, 1)}, recalculated: make(chan struct{}, 1)}
	s, err := New(store, runner, Config{
		Season: "2026", Stage: "Regular Season", CheckInterval: time.Hour,
		CompletionGrace: 3 * time.Hour, Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	select {
	case <-runner.recalculated:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not recalculate the current fixture cache")
	}
	s.Stop()
	s.Wait()
	if runner.calls.Load() != 0 {
		t.Fatalf("source sync calls = %d, want 0", runner.calls.Load())
	}
}

func TestTargetFixtureIDIsLimitedToOverdueResults(t *testing.T) {
	if got := targetFixtureID(Decision{Reason: "plausibly_complete_fixture", FixtureID: "game-1"}); got != "game-1" {
		t.Fatalf("overdue target = %q, want game-1", got)
	}
	for _, reason := range []string{"xg_unavailable", "unsupported_status", "invalid_kickoff", "incomplete_fixture_cache"} {
		if got := targetFixtureID(Decision{Reason: reason, FixtureID: "game-1"}); got != "" {
			t.Errorf("reason %q target = %q, want empty", reason, got)
		}
	}
}

func schedulerGame(id, kickoff, status string, homeScore, awayScore bool) cache.Game {
	game := cache.Game{ASAID: id, KickoffUTC: kickoff, Status: status}
	if homeScore {
		game.HomeScore = sql.NullInt64{Int64: 1, Valid: true}
	}
	if awayScore {
		game.AwayScore = sql.NullInt64{Int64: 0, Valid: true}
	}
	return game
}

type schedulerStore struct{ snapshot cache.RefreshSnapshot }

func (s schedulerStore) RefreshSnapshot(context.Context, string, string) (cache.RefreshSnapshot, error) {
	return s.snapshot, nil
}

type schedulerRunner struct {
	called      chan struct{}
	calls       atomic.Int32
	lastOptions syncer.RunOptions
}

func (r *schedulerRunner) Run(_ context.Context, options syncer.RunOptions) (cache.SyncRun, error) {
	r.calls.Add(1)
	r.lastOptions = options
	r.called <- struct{}{}
	return cache.SyncRun{StartedAt: time.Now(), FinishedAt: time.Now()}, nil
}

type recalculatingSchedulerRunner struct {
	schedulerRunner
	recalculated chan struct{}
}

func (r *recalculatingSchedulerRunner) Recalculate(context.Context, syncer.RecalculateOptions) (cache.SyncRun, error) {
	r.recalculated <- struct{}{}
	return cache.SyncRun{QualificationRecalculated: true, ScenarioRecalculated: true}, nil
}
