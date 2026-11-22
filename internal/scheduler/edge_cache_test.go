package scheduler

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/syncer"
	"go.opentelemetry.io/otel/trace"
)

type fakePurger struct {
	mu    sync.Mutex
	calls [][]string
	errs  []error
}

func (p *fakePurger) PurgeTags(_ context.Context, tags []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, slices.Clone(tags))
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return err
	}
	return nil
}

func (p *fakePurger) Calls() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

// changingRunner reports a stored-row change for operations in the given
// seasons and none for others.
type changingRunner struct {
	mu         sync.Mutex
	changes    map[string]bool
	operations []syncer.Operation
}

func (r *changingRunner) Execute(_ context.Context, op syncer.Operation) (syncer.OperationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operations = append(r.operations, op)
	audit := cache.SourceRefreshAudit{RowsUnchanged: 1}
	if r.changes[op.Season] {
		// A venue or attendance edit: stored, but no forecast or clinching
		// input changed.
		audit = cache.SourceRefreshAudit{RowsUpdated: 1}
	}
	return syncer.OperationResult{Operation: op, Games: &cache.GameRefreshResult{Audit: audit}}, nil
}

func (r *changingRunner) seasons() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var seasons []string
	for _, op := range r.operations {
		if !slices.Contains(seasons, op.Season) {
			seasons = append(seasons, op.Season)
		}
	}
	return seasons
}

// dueCurrentSeasonSnapshot has a result check due for the configured 2033
// season.
func dueCurrentSeasonSnapshot(now time.Time) cache.PlanningSnapshot {
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("one", fixtures.PreMatchStatus, now.Add(-2*time.Hour))})
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season", NextFullDueAt: new(now.Add(24 * time.Hour))}
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "one", NextDueAt: new(now)}}
	return cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}
}

func newPurgingScheduler(t *testing.T, snapshot cache.PlanningSnapshot, runner Runner, purger *fakePurger, now *time.Time) *Scheduler {
	t.Helper()
	config := testPlannerConfig()
	config.Now = func() time.Time { return *now }
	if purger != nil {
		config.EdgeCache = purger
	}
	s, err := New(&planningStore{snapshot: snapshot}, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func checkNow(t *testing.T, s *Scheduler) {
	t.Helper()
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
}

func TestCheckNowPurgesLivePagesAfterCurrentSeasonChange(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	runner := &changingRunner{changes: map[string]bool{"2033": true}}
	purger := &fakePurger{}
	s := newPurgingScheduler(t, dueCurrentSeasonSnapshot(now), runner, purger, &now)
	checkNow(t, s)
	if got, want := purger.Calls(), [][]string{{"nwsl-live"}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("purges = %v, want %v", got, want)
	}
}

func TestCheckNowPurgesEveryPageAfterAnotherSeasonChanges(t *testing.T) {
	now := time.Date(2033, 10, 3, 0, 0, 0, 0, time.UTC)
	archive := coldPlanningScope("2025", new(now.Add(-time.Hour)))
	runner := &changingRunner{changes: map[string]bool{"2025": true}}
	purger := &fakePurger{}
	s := newPurgingScheduler(t, cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{archive}}, runner, purger, &now)
	checkNow(t, s)
	if !slices.Contains(runner.seasons(), "2025") {
		t.Fatalf("operations covered %v, want the due 2025 archive", runner.seasons())
	}
	if got, want := purger.Calls(), [][]string{{"nwsl-page"}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("purges = %v, want %v", got, want)
	}
}

func TestCheckNowDoesNotPurgeWhenNothingChanged(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	runner := &changingRunner{}
	purger := &fakePurger{}
	s := newPurgingScheduler(t, dueCurrentSeasonSnapshot(now), runner, purger, &now)
	checkNow(t, s)
	if len(runner.seasons()) == 0 {
		t.Fatal("no source job ran")
	}
	if got := purger.Calls(); len(got) != 0 {
		t.Fatalf("purges = %v, want none", got)
	}
}

func TestFailedPurgeIsRetriedOnTheNextTick(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	changing := &changingRunner{changes: map[string]bool{"2033": true}}
	purger := &fakePurger{errs: []error{errors.New("cloudflare unavailable")}}
	s := newPurgingScheduler(t, dueCurrentSeasonSnapshot(now), changing, purger, &now)
	// A failed purge doesn't fail the check: the data was synced.
	checkNow(t, s)
	// The next tick changes nothing but still owes the purge.
	changing.mu.Lock()
	changing.changes = nil
	changing.mu.Unlock()
	now = now.Add(5 * time.Minute)
	checkNow(t, s)
	// Once it succeeds, nothing more is owed.
	now = now.Add(5 * time.Minute)
	checkNow(t, s)
	if got, want := purger.Calls(), [][]string{{"nwsl-live"}, {"nwsl-live"}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("purges = %v, want %v", got, want)
	}
}

func TestPurgesAreSpacedAndPendingScopesWiden(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	purger := &fakePurger{}
	s := newPurgingScheduler(t, cache.PlanningSnapshot{}, &changingRunner{}, purger, &now)
	ctx := context.Background()
	span := trace.SpanFromContext(ctx)

	s.purgeEdgeCache(ctx, span, purgeLive)
	// Too soon after the first purge: stays pending, and a change in another
	// season widens it.
	now = now.Add(10 * time.Second)
	s.purgeEdgeCache(ctx, span, purgeAll)
	now = now.Add(10 * time.Second)
	s.purgeEdgeCache(ctx, span, purgeLive)
	if got := len(purger.Calls()); got != 1 {
		t.Fatalf("purges within a minute = %d, want 1", got)
	}
	now = now.Add(time.Minute)
	s.purgeEdgeCache(ctx, span, purgeNone)
	now = now.Add(time.Minute)
	s.purgeEdgeCache(ctx, span, purgeNone)
	if got, want := purger.Calls(), [][]string{{"nwsl-live"}, {"nwsl-page"}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("purges = %v, want %v", got, want)
	}
}

func TestCheckNowWithoutEdgeCacheConfigured(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	runner := &changingRunner{changes: map[string]bool{"2033": true}}
	s := newPurgingScheduler(t, dueCurrentSeasonSnapshot(now), runner, nil, &now)
	checkNow(t, s)
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	if s.pendingPurge != purgeNone {
		t.Errorf("pending purge = %d, want none kept without a purger", s.pendingPurge)
	}
}
