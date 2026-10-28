package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/lifetime"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

// discoverySource is a fake ASA games source. A targeted request returns only
// the requested IDs, as ASA does.
type discoverySource struct {
	games []asa.Game
	err   error
}

func (s *discoverySource) Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error) {
	return nil, nil
}
func (s *discoverySource) Games(_ context.Context, filters asa.GamesFilters) ([]asa.Game, error) {
	if s.err != nil || filters.GameID == "" {
		return s.games, s.err
	}
	requested := strings.Split(filters.GameID, ",")
	games := []asa.Game{}
	for _, game := range s.games {
		if slices.Contains(requested, game.GameID) {
			games = append(games, game)
		}
	}
	return games, nil
}

// playoffDiscovery drives planner -> operation -> persistence for the 2026
// Playoffs after a verified regular-season finale, using a real cache and a
// fake source.
type playoffDiscovery struct {
	t       *testing.T
	ctx     context.Context
	db      *cache.DB
	source  *discoverySource
	regular cache.PlanningScopeSnapshot
	config  Config
	now     time.Time
}

func newPlayoffDiscovery(t *testing.T, completion time.Time) *playoffDiscovery {
	t.Helper()
	ctx := context.Background()
	db, err := cache.Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.EnsureSourceScopes(ctx, "2026", "Regular Season", completion); err != nil {
		t.Fatal(err)
	}
	teams := []cache.Team{}
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("t%d", i)
		teams = append(teams, cache.Team{ASAID: id, Name: id})
	}
	if _, err := db.UpsertTeams(ctx, teams, cache.FullRefreshMetadata{Trigger: cache.SourceTriggerCLI, StartedAt: completion.Add(-time.Hour), FinishedAt: completion.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	finale := plannedGame("finale", fixtures.CompletedStatus, completion.Add(-2*time.Hour))
	finale.HomeScore, finale.AwayScore = score(1), score(0)
	regular := planningScope("2026", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{finale})
	regular.Readiness.ExpectedInventory = &competition.InventoryExpectation{Games: 1}
	regular.ResultChecks = []cache.GameResultCheckState{{GameID: "finale", FirstTerminalObservedAt: timePointer(completion)}}
	// The large budget keeps regular-season xG and correction work from
	// crowding out the playoff jobs under test.
	config := Config{Season: "2026", Stage: "Regular Season", CheckInterval: 5 * time.Minute, CompletionGrace: time.Hour, SourceRequestBudget: 10}
	return &playoffDiscovery{t: t, ctx: ctx, db: db, source: &discoverySource{}, regular: regular, config: config, now: completion}
}

func (d *playoffDiscovery) playoffs() cache.PlanningScopeSnapshot {
	d.t.Helper()
	snapshot, err := d.db.PlanningSnapshot(d.ctx)
	if err != nil {
		d.t.Fatal(err)
	}
	for _, scope := range snapshot.Scopes {
		if scope.Readiness.Scope.Season == "2026" && scope.Readiness.Scope.Stage == "Playoffs" {
			return scope
		}
	}
	d.t.Fatal("2026 Playoffs scope missing")
	return cache.PlanningScopeSnapshot{}
}

// tick plans at the current time and executes the planned Playoffs games jobs
// in order. Source failures are returned rather than failing the test.
func (d *playoffDiscovery) tick() ([]Job, []syncer.OperationResult, error) {
	d.t.Helper()
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{d.regular, d.playoffs()}}, d.config, d.now)
	service := syncer.Service{Store: d.db, ASA: d.source, Now: func() time.Time { return d.now }}
	executed, results := []Job{}, []syncer.OperationResult{}
	for _, job := range jobs {
		if job.Operation.Stage != "Playoffs" || job.Operation.Resource != syncer.OperationGames {
			continue
		}
		result, err := service.Execute(d.ctx, job.Operation)
		executed, results = append(executed, job), append(results, result)
		if err != nil {
			return executed, results, err
		}
	}
	return executed, results, nil
}

// fullGamesJobs returns the full-games jobs among jobs.
func fullGamesJobs(jobs []Job) []Job {
	full := []Job{}
	for _, job := range jobs {
		if job.Kind == JobFullGames {
			full = append(full, job)
		}
	}
	return full
}

func playoffGame(id, status, home, away string, kickoff time.Time) asa.Game {
	game := asa.Game{GameID: id, DateTimeUTC: kickoff.Format("2006-01-02 15:04:05 UTC"), HomeTeamID: home, AwayTeamID: away, SeasonName: "2026", KnockoutGame: true, Status: status}
	if status == fixtures.CompletedStatus {
		two, one := 2, 1
		game.HomeScore, game.AwayScore = &two, &one
		game.LastUpdatedUTC = kickoff.Add(2 * time.Hour).Format("2006-01-02 15:04:05 UTC")
	}
	return game
}

func quarterfinals(kickoff time.Time) []asa.Game {
	return []asa.Game{
		playoffGame("qf1", fixtures.PreMatchStatus, "t1", "t8", kickoff),
		playoffGame("qf2", fixtures.PreMatchStatus, "t4", "t5", kickoff.Add(3*time.Hour)),
		playoffGame("qf3", fixtures.PreMatchStatus, "t2", "t7", kickoff.Add(24*time.Hour)),
		playoffGame("qf4", fixtures.PreMatchStatus, "t3", "t6", kickoff.Add(27*time.Hour)),
	}
}

func TestPlayoffDiscoveryRetriesHourlyUntilLaterPublicationCompletesBracket(t *testing.T) {
	completion := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	d := newPlayoffDiscovery(t, completion)
	kickoff := completion.Add(6 * 24 * time.Hour)
	expectDiscovery := func(step string, want int) {
		t.Helper()
		jobs, _, err := d.tick()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		full := fullGamesJobs(jobs)
		if len(full) != want {
			t.Fatalf("%s full-games jobs = %+v", step, full)
		}
		if want == 1 && full[0].Operation.NextFullDueAfter != time.Hour {
			t.Fatalf("%s cadence = %v", step, full[0].Operation.NextFullDueAfter)
		}
	}

	expectDiscovery("empty", 1)
	d.now = completion.Add(30 * time.Minute)
	expectDiscovery("before empty retry", 0)

	d.now = completion.Add(time.Hour)
	d.source.games = quarterfinals(kickoff)
	expectDiscovery("partial", 1)
	if got := len(d.playoffs().Games); got != 4 {
		t.Fatalf("partial games = %d", got)
	}
	d.now = completion.Add(90 * time.Minute)
	expectDiscovery("before partial retry", 0)

	d.now = completion.Add(2 * time.Hour)
	d.source.games = append(quarterfinals(kickoff),
		playoffGame("sf1", fixtures.PreMatchStatus, "t1", "t4", kickoff.Add(7*24*time.Hour)),
		playoffGame("sf2", fixtures.PreMatchStatus, "t2", "t3", kickoff.Add(7*24*time.Hour+3*time.Hour)),
		playoffGame("final", fixtures.PreMatchStatus, "t1", "t2", kickoff.Add(14*24*time.Hour)),
	)
	expectDiscovery("complete", 1)
	if got := len(d.playoffs().Games); got != 7 {
		t.Fatalf("complete games = %d", got)
	}
	completed := d.now

	// The completing discovery persisted the short interval chosen before its
	// response arrived; planning resumes the normal weekly audit instead.
	d.now = completed.Add(time.Hour)
	expectDiscovery("after complete bracket", 0)
	d.now = completed.Add(defaultInventoryInterval)
	jobs, _, err := d.tick()
	if full := fullGamesJobs(jobs); err != nil || len(full) != 1 || full[0].Reason != "weekly_inventory_audit" || full[0].Operation.NextFullDueAfter != defaultInventoryInterval {
		t.Fatalf("weekly audit = %+v, %v", full, err)
	}
}

func TestPlayoffDiscoveryRetriesAfterEarlyNextRoundDiscovery(t *testing.T) {
	completion := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	d := newPlayoffDiscovery(t, completion)
	kickoff := completion.Add(6 * 24 * time.Hour)
	d.source.games = quarterfinals(kickoff)
	if jobs, _, err := d.tick(); err != nil || len(fullGamesJobs(jobs)) != 1 {
		t.Fatalf("initial discovery = %+v, %v", jobs, err)
	}

	// The hourly retry before kickoff finds nothing new.
	d.now = kickoff.Add(30 * time.Minute)
	if jobs, _, err := d.tick(); err != nil || len(fullGamesJobs(jobs)) != 1 {
		t.Fatalf("pre-kickoff retry = %+v, %v", jobs, err)
	}

	// A targeted result makes the incomplete bracket's discovery due at once.
	d.now = kickoff.Add(65 * time.Minute)
	d.source.games[0] = playoffGame("qf1", fixtures.CompletedStatus, "t1", "t8", kickoff)
	jobs, results, err := d.tick()
	if err != nil || len(jobs) != 1 || jobs[0].Kind != JobCheckedGames || !results[0].FullGamesDiscoveryAccelerated {
		t.Fatalf("targeted result = %+v, %+v, %v", jobs, results, err)
	}

	// ASA has not yet published the semifinal, so the immediate discovery
	// observes no new match.
	d.now = kickoff.Add(70 * time.Minute)
	jobs, _, err = d.tick()
	if full := fullGamesJobs(jobs); err != nil || len(full) != 1 || full[0].Reason != "targeted_bracket_discovery" || full[0].Operation.NextFullDueAfter != time.Hour {
		t.Fatalf("immediate discovery = %+v, %v", full, err)
	}
	if got := len(d.playoffs().Games); got != 4 {
		t.Fatalf("games after immediate discovery = %d", got)
	}
	d.now = kickoff.Add(100 * time.Minute)
	if jobs, _, err := d.tick(); err != nil || len(fullGamesJobs(jobs)) != 0 {
		t.Fatalf("before scheduled retry = %+v, %v", jobs, err)
	}

	// The scheduled retry discovers it without another result change.
	d.now = kickoff.Add(130 * time.Minute)
	d.source.games = append(d.source.games, playoffGame("sf1", fixtures.PreMatchStatus, "t1", "t4", kickoff.Add(7*24*time.Hour)))
	jobs, _, err = d.tick()
	if full := fullGamesJobs(jobs); err != nil || len(full) != 1 || full[0].Operation.NextFullDueAfter != time.Hour {
		t.Fatalf("scheduled retry = %+v, %v", full, err)
	}
	if got := len(d.playoffs().Games); got != 5 {
		t.Fatalf("games after scheduled retry = %d", got)
	}
}

func TestPlayoffDiscoveryPersistsEmptyObservationAndFailure(t *testing.T) {
	completion := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	d := newPlayoffDiscovery(t, completion)
	if jobs, _, err := d.tick(); err != nil || len(fullGamesJobs(jobs)) != 1 {
		t.Fatalf("empty discovery = %+v, %v", jobs, err)
	}
	playoffs := d.playoffs()
	if playoffs.GamesFull == nil || playoffs.GamesFull.NextFullDueAt == nil || !playoffs.GamesFull.NextFullDueAt.Equal(completion.Add(time.Hour)) {
		t.Fatalf("persisted cadence = %+v", playoffs.GamesFull)
	}
	d.now = completion.Add(time.Hour)
	d.source.err = errors.New("source unavailable")
	if _, _, err := d.tick(); err == nil {
		t.Fatal("expected source failure")
	}
	// A failure leaves the work due on the next tick without advancing the
	// successful observation.
	d.now = d.now.Add(5 * time.Minute)
	d.source.err = nil
	if jobs, _, err := d.tick(); err != nil || len(fullGamesJobs(jobs)) != 1 {
		t.Fatalf("retry after failure = %+v, %v", jobs, err)
	}
}

// playoffDiscoveryStore serves the harness's synthetic regular season beside
// the cached 2026 Playoffs, with the cache's real sync leases.
type playoffDiscoveryStore struct {
	*cache.DB
	d *playoffDiscovery
}

func (s playoffDiscoveryStore) PlanningSnapshot(context.Context) (cache.PlanningSnapshot, error) {
	return cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{s.d.regular, s.d.playoffs()}}, nil
}

// playoffDiscoveryRunner executes only Playoffs games operations.
type playoffDiscoveryRunner struct {
	d        *playoffDiscovery
	executed int
}

func (r *playoffDiscoveryRunner) Execute(ctx context.Context, operation syncer.Operation) (syncer.OperationResult, error) {
	if operation.Stage != "Playoffs" || operation.Resource != syncer.OperationGames {
		return syncer.OperationResult{}, nil
	}
	r.executed++
	return syncer.Service{Store: r.d.db, ASA: r.d.source, Now: func() time.Time { return r.d.now }}.Execute(ctx, operation)
}

func TestPlayoffDiscoveryLeaseDeferralLeavesWorkDue(t *testing.T) {
	completion := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	d := newPlayoffDiscovery(t, completion)
	runner := &playoffDiscoveryRunner{d: d}
	config := d.config
	config.Timeout = time.Minute
	config.Now = func() time.Time { return d.now }
	s, err := New(playoffDiscoveryStore{DB: d.db, d: d}, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	key := "2026\x00Playoffs"
	if acquired, err := d.db.TryAcquireSyncLease(d.ctx, key, "other-process", completion.Add(time.Hour)); err != nil || !acquired {
		t.Fatalf("hold lease = %v, %v", acquired, err)
	}
	if err := s.CheckNow(d.ctx); err != nil {
		t.Fatal(err)
	}
	if runner.executed != 0 || d.playoffs().GamesFull != nil {
		t.Fatalf("deferred discovery executed %d, state %+v", runner.executed, d.playoffs().GamesFull)
	}
	if err := d.db.ReleaseSyncLease(d.ctx, key, "other-process"); err != nil {
		t.Fatal(err)
	}
	d.now = completion.Add(5 * time.Minute)
	if err := s.CheckNow(d.ctx); err != nil {
		t.Fatal(err)
	}
	if state := d.playoffs().GamesFull; runner.executed != 1 || state == nil || state.NextFullDueAt == nil || !state.NextFullDueAt.Equal(d.now.Add(time.Hour)) {
		t.Fatalf("discovery after release executed %d, state %+v", runner.executed, state)
	}
}

func TestPlayoffDiscoveryAtSaturatedBudget(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	finale := plannedGame("finale", fixtures.CompletedStatus, now.Add(-7*24*time.Hour))
	finale.HomeScore, finale.AwayScore = score(1), score(0)
	regular := planningScope("2026", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{finale})
	regular.Readiness.ExpectedInventory = &competition.InventoryExpectation{Games: 1}
	regular.ResultChecks = []cache.GameResultCheckState{{GameID: "finale", FirstTerminalObservedAt: timePointer(now.Add(-7 * 24 * time.Hour))}}
	// Only the hourly retry makes discovery due; the persisted weekly due time
	// is still in the future. A routine result poll for a finished
	// quarterfinal competes with it.
	quarterfinal := plannedGame("qf1", fixtures.PreMatchStatus, now.Add(-2*time.Hour))
	playoffs := planningScope("2026", "Playoffs", cache.SourceReadinessAvailable, []cache.Game{quarterfinal})
	playoffs.GamesFull.LastFullSuccessAt = timePointer(now.Add(-time.Hour))
	playoffs.GamesFull.NextFullDueAt = timePointer(now.Add(6 * 24 * time.Hour))
	config := Config{Season: "2026", Stage: "Regular Season", CheckInterval: 5 * time.Minute, CompletionGrace: time.Hour, SourceRequestBudget: 1}
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{regular, playoffs}}, config, now)
	if len(jobs) != 1 || jobs[0].Kind != JobFullGames || jobs[0].Operation.Stage != "Playoffs" || jobs[0].Operation.NextFullDueAfter != time.Hour {
		t.Fatalf("hourly retry at saturated budget = %+v", jobs)
	}
	// Catalog bootstrap still outranks it, and planning leaves it due for
	// the next tick rather than advancing its cadence.
	missing := historicalCatalogBootstrapScope("2025")
	snapshot := cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{regular, playoffs, missing}}
	jobs = Plan(snapshot, config, now)
	if len(jobs) != 1 || jobs[0].Operation.Season != "2025" {
		t.Fatalf("bootstrap at saturated budget = %+v", jobs)
	}
	if !discoveryDue(playoffs, config, now.Add(5*time.Minute), now.Add(-7*24*time.Hour)) {
		t.Fatal("crowded-out retry no longer due")
	}
	snapshot.Scopes = snapshot.Scopes[:2]
	jobs = Plan(snapshot, config, now.Add(5*time.Minute))
	if len(jobs) != 1 || jobs[0].Kind != JobFullGames || jobs[0].Operation.Stage != "Playoffs" {
		t.Fatalf("next-tick retry = %+v", jobs)
	}
}

func TestPlayoffDiscoveryLeavesUnrelatedStagesWeekly(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	finale := plannedGame("finale", fixtures.CompletedStatus, now.Add(-14*time.Hour))
	finale.HomeScore, finale.AwayScore = score(1), score(0)
	regular := planningScope("2026", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{finale})
	regular.Readiness.ExpectedInventory = &competition.InventoryExpectation{Games: 1}
	regular.ResultChecks = []cache.GameResultCheckState{{GameID: "finale", FirstTerminalObservedAt: timePointer(now.Add(-12 * time.Hour)), NextDueAt: timePointer(now.Add(time.Hour))}}
	regular.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2026", Stage: "Regular Season", NextFullDueAt: timePointer(now.Add(24 * time.Hour))}
	weekly := func(season, stage string, lifecycle cache.SourceScopeLifecycle) cache.PlanningScopeSnapshot {
		scope := planningScope(season, stage, cache.SourceReadinessAvailable, nil)
		scope.Readiness.Scope.Lifecycle = lifecycle
		scope.GamesFull.LastFullSuccessAt = timePointer(now.Add(-2 * time.Hour))
		scope.GamesFull.NextFullDueAt = timePointer(now.Add(7*24*time.Hour - 2*time.Hour))
		return scope
	}
	config := Config{Season: "2026", Stage: "Regular Season", CheckInterval: 5 * time.Minute, CompletionGrace: time.Hour, SourceRequestBudget: 10}
	for _, scope := range []cache.PlanningScopeSnapshot{
		weekly("2026", "NWSL Challenge Cup Final", cache.SourceScopeActive),
		weekly("2025", "Playoffs", cache.SourceScopeCompleted),
	} {
		id := scope.Readiness.Scope
		if jobs := fullGamesJobs(Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{regular, scope}}, config, now)); len(jobs) != 0 {
			t.Fatalf("%s %s accelerated: %+v", id.Season, id.Stage, jobs)
		}
	}
	// Before the regular season is complete, the current Playoffs keep the
	// weekly cadence too.
	regular.Games[0].Status = fixtures.PreMatchStatus
	regular.Games[0].HomeScore, regular.Games[0].AwayScore = sql.NullInt64{}, sql.NullInt64{}
	playoffs := weekly("2026", "Playoffs", cache.SourceScopeActive)
	if jobs := fullGamesJobs(Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{regular, playoffs}}, config, now)); len(jobs) != 0 {
		t.Fatalf("playoffs accelerated before completion: %+v", jobs)
	}
}

func TestPlanBatchesDueTargetedJobsAndHonorsBudget(t *testing.T) {
	now := time.Date(2033, 6, 10, 12, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("two", fixtures.PreMatchStatus, now.Add(-time.Hour)), plannedGame("one", fixtures.PreMatchStatus, now.Add(-time.Hour))})
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "one", NextDueAt: timePointer(now)}, {GameID: "two", NextDueAt: timePointer(now)}}
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobCheckedGames || !reflect.DeepEqual(jobIDs(jobs[0]), []string{"one", "two"}) {
		t.Fatalf("jobs = %+v", jobs)
	}
	jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, Config{Season: "2033", Stage: "Regular Season", CheckInterval: 5 * time.Minute, CompletionGrace: time.Hour, Timeout: time.Second, SourceRequestBudget: 0}, now)
	if len(jobs) != 1 {
		t.Fatalf("default budget jobs = %+v", jobs)
	}
}

func TestPlayoffDiscoveryAfterRegularSeasonCompletion(t *testing.T) {
	now := time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC)
	game := plannedGame("finale", fixtures.CompletedStatus, now.Add(-4*time.Hour))
	game.HomeScore, game.AwayScore = score(1), score(0)
	regular := planningScope("2026", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{game})
	regular.Readiness.ExpectedInventory = &competition.InventoryExpectation{Games: 1}
	regular.ResultChecks = []cache.GameResultCheckState{{GameID: "finale", FirstTerminalObservedAt: timePointer(now)}}
	playoffs := planningScope("2026", "Playoffs", cache.SourceReadinessNotPublished, nil)
	playoffs.GamesFull = &cache.SourceResourceScopeState{LastFullSuccessAt: timePointer(now.Add(-time.Minute)), NextFullDueAt: timePointer(now.Add(7 * 24 * time.Hour))}
	config := Config{Season: "2026", Stage: "Regular Season", SourceRequestBudget: 1}
	snapshot := cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{regular, playoffs}}
	jobs := Plan(snapshot, config, now)
	if len(jobs) != 1 || jobs[0].Operation.Stage != "Playoffs" || jobs[0].Operation.NextFullDueAfter != time.Hour {
		t.Fatalf("completion jobs = %+v", jobs)
	}
	// A successful empty observation suppresses a busy loop, then retries.
	playoffs.GamesFull.LastFullSuccessAt = timePointer(now)
	playoffs.GamesFull.NextFullDueAt = timePointer(now.Add(time.Hour))
	snapshot.Scopes[1] = playoffs
	if discoveryDue(playoffs, config, now.Add(59*time.Minute), now) {
		t.Fatal("early retry")
	}
	if !discoveryDue(playoffs, config, now.Add(time.Hour), now) {
		t.Fatal("hourly retry missing")
	}
	playoffs.Readiness.Readiness = cache.SourceReadinessAvailable
	playoffs.Games = []cache.Game{plannedGame("quarterfinal", fixtures.PreMatchStatus, now.Add(24*time.Hour))}
	snapshot.Scopes[1] = playoffs
	jobs = Plan(snapshot, config, now.Add(time.Hour))
	if len(jobs) != 1 || jobs[0].Operation.Stage != "Playoffs" || jobs[0].Operation.NextFullDueAfter != time.Hour {
		t.Fatalf("partial jobs = %+v", jobs)
	}
	entry, _ := competition.Lookup("2026", "Playoffs")
	playoffs.Games = make([]cache.Game, len(entry.BracketFormat.Slots))
	if acceleratedPlayoffs(playoffs, config, now) {
		t.Fatal("complete bracket accelerated")
	}
	playoffs.Readiness.Scope.Stage = "Challenge Cup"
	if acceleratedPlayoffs(playoffs, config, now) {
		t.Fatal("unrelated stage accelerated")
	}
}

func TestPlanXGCadenceAndInitialFullXG(t *testing.T) {
	now := time.Date(2033, 7, 10, 12, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("xg", fixtures.CompletedStatus, now.Add(-24*time.Hour))})
	scope.Games[0].HomeScore, scope.Games[0].AwayScore = score(1), score(0)
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "xg", LastCheckedAt: now.Add(-6 * time.Hour)}}
	// No xG full state triggers one bootstrap full request before targeted work.
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now)
	if len(jobs) == 0 || jobs[0].Kind != JobCheckedGames || jobs[1].Kind != JobFullXG {
		t.Fatalf("initial xG jobs = %+v", jobs)
	}
	state := cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.XGFull = &state
	scope.ResultChecks[0].LastCheckedAt = now
	jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobCheckedXG || jobIDs(jobs[0])[0] != "xg" || jobs[0].Operation.Requested[0].NextDueAfter != 5*time.Minute || jobs[0].Operation.Requested[0].MaterialNextDueAfter != 5*time.Minute || jobs[0].Selection.MissingEligibleCount != 1 {
		t.Fatalf("missing xG jobs = %+v", jobs)
	}
	available := cache.GameXG{GameID: "xg", Availability: cache.XGAvailable}
	scope.XG = []cache.GameXG{available}
	scope.XGChecks = []cache.GameXGCheckState{{GameID: "xg", LastCheckedAt: now.Add(-6 * time.Hour)}}
	jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Operation.Requested[0].NextDueAfter != 6*time.Hour || jobs[0].Operation.Requested[0].MaterialNextDueAfter != 6*time.Hour || jobs[0].Selection.AvailableEligibleCount != 1 {
		t.Fatalf("xG correction job = %+v", jobs)
	}
	// Result polling stops after three days, while xG polling remains eligible
	// through the fifth day, both anchored to kickoff rather than observation.
	scope.Games[0].KickoffUTC = now.Add(-4 * 24 * time.Hour).Format(time.RFC3339)
	if jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now); len(jobs) != 1 || jobs[0].Kind != JobCheckedXG {
		t.Fatalf("result-window boundary jobs = %+v", jobs)
	}
	scope.Games[0].KickoffUTC = now.Add(-6 * 24 * time.Hour).Format(time.RFC3339)
	if jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now); len(jobs) != 0 {
		t.Fatalf("post-xG-window jobs = %+v", jobs)
	}
}

func TestPlanXGUsesIndependentMissingAndAvailableCadence(t *testing.T) {
	now := time.Date(2033, 7, 11, 12, 0, 0, 0, time.UTC)
	missing := plannedGame("missing", fixtures.CompletedStatus, now.Add(-24*time.Hour))
	missing.HomeScore, missing.AwayScore = score(1), score(0)
	available := plannedGame("available", fixtures.CompletedStatus, now.Add(-24*time.Hour))
	available.HomeScore, available.AwayScore = score(2), score(1)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{missing, available})
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "available", LastCheckedAt: now}, {GameID: "missing", LastCheckedAt: now}}
	scope.XG = []cache.GameXG{{GameID: "available", Availability: cache.XGAvailable}}
	scope.XGChecks = []cache.GameXGCheckState{{GameID: "available", LastCheckedAt: now.Add(-4 * time.Hour)}, {GameID: "missing", LastCheckedAt: now.Add(-4 * time.Hour)}}
	config := testPlannerConfig()
	config.MissingXGInterval = 2 * time.Minute
	config.XGCorrectionInterval = 3 * time.Hour

	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, config, now)
	if len(jobs) != 1 || jobs[0].Kind != JobCheckedXG || jobs[0].Selection.MissingEligibleCount != 1 || jobs[0].Selection.AvailableEligibleCount != 1 {
		t.Fatalf("independent xG jobs = %+v", jobs)
	}
	byID := map[string]time.Duration{}
	for _, request := range jobs[0].Operation.Requested {
		byID[request.GameID] = request.NextDueAfter
	}
	if byID["missing"] != 2*time.Minute || byID["available"] != 3*time.Hour {
		t.Fatalf("independent xG cadence = %+v", byID)
	}
}

func TestPlanAbandonedUsesTerminalCorrectionCadence(t *testing.T) {
	now := time.Date(2033, 8, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("abandoned", fixtures.AbandonedStatus, now.Add(-10*time.Hour))})
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "abandoned", LastCheckedAt: now.Add(-6 * time.Hour)}}
	state := cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.XGFull = &state
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobCheckedGames || jobs[0].Reason != "kickoff_window_result_poll" || jobs[0].Operation.Requested[0].NextDueAfter != 6*time.Hour {
		t.Fatalf("abandoned job = %+v", jobs)
	}
}

func TestPlanKeepsNonterminalMaterialChangesOnCheckCadence(t *testing.T) {
	now := time.Date(2033, 8, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("unsettled", fixtures.PreMatchStatus, now.Add(-2*time.Hour))})
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "unsettled", NextDueAt: timePointer(now)}}

	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobCheckedGames || len(jobs[0].Operation.Requested) != 1 {
		t.Fatalf("nonterminal material-change job = %+v", jobs)
	}
	request := jobs[0].Operation.Requested[0]
	if request.NextDueAfter != 5*time.Minute || request.MaterialNextDueAfter != 5*time.Minute {
		t.Fatalf("nonterminal material-change cadence = %+v", request)
	}
}

func TestPlanBootstrapUsesOneFullInventoryThenOneFullXG(t *testing.T) {
	now := time.Date(2033, 8, 2, 0, 0, 0, 0, time.UTC)
	missing := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{missing}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobFullGames {
		t.Fatalf("missing-inventory bootstrap jobs = %+v", jobs)
	}
	available := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("future", fixtures.PreMatchStatus, now.Add(time.Hour))})
	jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{available}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobFullXG {
		t.Fatalf("available bootstrap jobs = %+v", jobs)
	}
}

func TestPlanBootstrapsMissingCompletedCatalogSeason(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	historical := planningScope("2025", "Regular Season", cache.SourceReadinessUnknown, nil)
	historical.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted

	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{historical}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobFullGames || jobs[0].Operation.Season != "2025" || jobs[0].Operation.Expectation != nil {
		t.Fatalf("historical bootstrap jobs = %+v", jobs)
	}
}

func TestPlanDoesNotReloadAvailableCompletedCatalogSeasonAsBootstrap(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	historical := planningScope("2025", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("one", fixtures.CompletedStatus, now.Add(-time.Hour))})
	historical.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
	historical.GamesFull.LastFullSuccessAt = timePointer(now.Add(-31 * 24 * time.Hour))
	historical.GamesFull.NextFullDueAt = timePointer(now.Add(24 * time.Hour))
	historical.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2025", Stage: "Regular Season", LastFullSuccessAt: timePointer(now.Add(-31 * 24 * time.Hour)), NextFullDueAt: timePointer(now.Add(24 * time.Hour))}

	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{historical}}, testPlannerConfig(), now)
	if len(jobs) != 0 {
		t.Fatalf("available historical bootstrap jobs = %+v", jobs)
	}
}

func TestPlanBootstrapsInitialXGForAvailableCompletedCatalogSeason(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	historical := historicalCatalogXGBootstrapScope("2025")

	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{historical}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobFullXG || jobs[0].Operation.Season != "2025" || jobs[0].Operation.Stage != "Playoffs" {
		t.Fatalf("historical xG bootstrap jobs = %+v", jobs)
	}
}

func TestPlanIncludesActiveCurrentPlayoffsAfterRegularScope(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	regular := planningScope("2026", "Regular Season", cache.SourceReadinessNotPublished, nil)
	playoffs := planningScope("2026", "Playoffs", cache.SourceReadinessNotPublished, nil)
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{playoffs, regular}}, Config{Season: "2026", Stage: "Regular Season", SourceRequestBudget: 3}, now)
	if len(jobs) != 2 || jobs[0].Operation.Stage != "Regular Season" || jobs[1].Operation.Stage != "Playoffs" || jobs[1].Operation.Expectation != nil {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestPlanPrioritizesMissingCatalogDiscoveryBeforeRoutineChecks(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	regular := planningScope("2026", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("regular", fixtures.PreMatchStatus, now.Add(-time.Hour))})
	regular.ResultChecks = []cache.GameResultCheckState{{GameID: "regular", NextDueAt: timePointer(now)}}
	regular.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2026", Stage: "Regular Season"}
	playoffs := planningScope("2026", "Playoffs", cache.SourceReadinessNotPublished, nil)
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{playoffs, regular}}, Config{Season: "2026", Stage: "Regular Season", CheckInterval: 5 * time.Minute, CompletionGrace: time.Hour, SourceRequestBudget: 1}, now)
	if len(jobs) != 1 || jobs[0].Kind != JobFullGames || jobs[0].Operation.Stage != "Playoffs" || jobs[0].Reason != "missing_or_not_published_inventory" {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestPlanSelectsAcceleratedBracketDiscoveryBeforeRoutinePollAtSaturatedBudget(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	regular := planningScope("2026", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("regular", fixtures.PreMatchStatus, now.Add(-2*time.Hour))})
	regular.ResultChecks = []cache.GameResultCheckState{{GameID: "regular", NextDueAt: timePointer(now)}}
	regular.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2026", Stage: "Regular Season"}
	playoffGame := plannedGame("playoff", fixtures.PreMatchStatus, now.Add(time.Hour))
	playoffs := planningScope("2026", "Playoffs", cache.SourceReadinessAvailable, []cache.Game{playoffGame})
	playoffs.GamesFull.NextFullDueAt = timePointer(now)
	playoffs.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2026", Stage: "Playoffs"}

	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{regular, playoffs}}, Config{Season: "2026", Stage: "Regular Season", CheckInterval: 5 * time.Minute, CompletionGrace: time.Hour, SourceRequestBudget: 1}, now)
	if len(jobs) != 1 || jobs[0].Kind != JobFullGames || jobs[0].Operation.Stage != "Playoffs" || jobs[0].Reason != "targeted_bracket_discovery" {
		t.Fatalf("saturated next-tick jobs = %+v", jobs)
	}
}

func TestPlanOrdersMissingCatalogInventoryTiers(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	scopes := []cache.PlanningScopeSnapshot{
		planningScope("2025", "Playoffs", cache.SourceReadinessNotPublished, nil),
		planningScope("2024", "Regular Season", cache.SourceReadinessUnknown, nil),
		planningScope("2025", "Regular Season", cache.SourceReadinessUnknown, nil),
		planningScope("2026", "NWSL Challenge Cup Final", cache.SourceReadinessNotPublished, nil),
		planningScope("2026", "Playoffs", cache.SourceReadinessNotPublished, nil),
		planningScope("2026", "Regular Season", cache.SourceReadinessNotPublished, nil),
	}
	for index := range scopes {
		if scopes[index].Readiness.Scope.Season != "2026" {
			scopes[index].Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
		}
	}
	jobs := Plan(cache.PlanningSnapshot{Scopes: scopes}, Config{Season: "2026", Stage: "Regular Season", SourceRequestBudget: 6}, now)
	want := []struct{ season, stage string }{
		{"2026", "Regular Season"},
		{"2026", "Playoffs"},
		{"2026", "NWSL Challenge Cup Final"},
		{"2025", "Regular Season"},
		{"2024", "Regular Season"},
		{"2025", "Playoffs"},
	}
	if len(jobs) != len(want) {
		t.Fatalf("jobs=%+v", jobs)
	}
	for index, expected := range want {
		if jobs[index].Operation.Season != expected.season || jobs[index].Operation.Stage != expected.stage {
			t.Fatalf("job %d = %s %s, want %s %s", index, jobs[index].Operation.Season, jobs[index].Operation.Stage, expected.season, expected.stage)
		}
	}
}

func TestPlanAuditsActiveCompleteInventoryWeekly(t *testing.T) {
	now := time.Date(2033, 8, 2, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("future", fixtures.PreMatchStatus, now.Add(time.Hour))})
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.GamesFull.NextFullDueAt = timePointer(now)
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Kind != JobFullGames || jobs[0].Reason != "weekly_inventory_audit" {
		t.Fatalf("weekly active inventory jobs = %+v", jobs)
	}
	scope.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
	if jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now); len(jobs) != 0 {
		t.Fatalf("completed inventory jobs = %+v", jobs)
	}
}

func TestPlanColdSweepsCompletedScopesOneAtATimeAndChainsGamesThenXG(t *testing.T) {
	now := time.Date(2033, 10, 1, 0, 0, 0, 0, time.UTC)
	archive := planningScope("2025", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("one", fixtures.CompletedStatus, now.Add(-40*24*time.Hour))})
	archive.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
	lastGames := now.Add(-31 * 24 * time.Hour)
	archive.GamesFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGames, Season: "2025", Stage: "Regular Season", LastFullSuccessAt: &lastGames}
	lastXGBeforeGames := lastGames.Add(-time.Hour)
	archive.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2025", Stage: "Regular Season", LastFullSuccessAt: &lastXGBeforeGames}
	config := testPlannerConfig()
	config.ColdSweepInterval = 30 * 24 * time.Hour
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{archive}}, config, now)
	if len(jobs) != 1 || jobs[0].Class != JobCold || jobs[0].Kind != JobFullGames || jobs[0].Operation.NextFullDueAfter != 30*24*time.Hour {
		t.Fatalf("cold games job = %+v", jobs)
	}
	gamesDue := now.Add(30 * 24 * time.Hour)
	archive.GamesFull.NextFullDueAt = &gamesDue
	jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{archive}}, config, now)
	if len(jobs) != 1 || jobs[0].Class != JobCold || jobs[0].Kind != JobFullXG || jobs[0].Reason != "archived_xg_after_games" {
		t.Fatalf("paired xG job = %+v", jobs)
	}
	lastXG := now
	archive.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2025", Stage: "Regular Season", LastFullSuccessAt: &lastXG, NextFullDueAt: &gamesDue}
	if jobs = Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{archive}}, config, now); len(jobs) != 0 {
		t.Fatalf("not-due archive jobs = %+v", jobs)
	}
}

func TestPlanColdSweepUsesPersistedDueAndHotWorkWins(t *testing.T) {
	now := time.Date(2033, 10, 1, 0, 0, 0, 0, time.UTC)
	first, second := now.Add(-time.Hour), now.Add(-2*time.Hour)
	archives := []cache.PlanningScopeSnapshot{
		coldPlanningScope("2025", &first), coldPlanningScope("2024", &second),
	}
	jobs := Plan(cache.PlanningSnapshot{Scopes: archives}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Operation.Season != "2024" {
		t.Fatalf("cold due ordering = %+v", jobs)
	}
	firstOffset := coldSweepOffset("2025", "Regular Season", 30*24*time.Hour)
	secondOffset := coldSweepOffset("2025", "Regular Season", 30*24*time.Hour)
	if firstOffset != secondOffset {
		t.Fatal("cold staggering is not deterministic")
	}
	hot := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	jobs = Plan(cache.PlanningSnapshot{Scopes: append(archives, hot)}, testPlannerConfig(), now)
	if len(jobs) != 1 || jobs[0].Class != JobHot || jobs[0].Operation.Season != "2033" {
		t.Fatalf("hot suppression jobs = %+v", jobs)
	}
}

func TestColdExecutionAcquiresGlobalThenScopeAndReleasesReverse(t *testing.T) {
	now := time.Date(2033, 10, 2, 0, 0, 0, 0, time.UTC)
	store := &planningStore{}
	runner := &operationRunner{}
	s, err := New(store, runner, testPlannerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	job := Job{Class: JobCold, Kind: JobFullGames, Operation: syncer.Operation{Resource: syncer.OperationGames, Mode: syncer.OperationFull, Season: "2025", Stage: "Regular Season"}}
	if outcome, _, attempted, err := s.executeJob(context.Background(), job); err != nil || outcome != "complete" || !attempted {
		t.Fatalf("cold execution = %q %t %v", outcome, attempted, err)
	}
	wantAcquire := []string{coldSweepLeaseKey, "2025\x00Regular Season"}
	wantRelease := []string{"2025\x00Regular Season", coldSweepLeaseKey}
	if !reflect.DeepEqual(store.keys, wantAcquire) || !reflect.DeepEqual(store.releaseKeys, wantRelease) {
		t.Fatalf("lease order = %v / %v, want %v / %v", store.keys, store.releaseKeys, wantAcquire, wantRelease)
	}
}

func TestColdExecutionCleansUpGlobalLeaseWhenScopeIsUnavailable(t *testing.T) {
	now := time.Date(2033, 10, 2, 0, 0, 0, 0, time.UTC)
	store := &planningStore{acquireResults: []bool{true, false}}
	s, err := New(store, &operationRunner{}, testPlannerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	job := Job{Class: JobCold, Kind: JobFullGames, Operation: syncer.Operation{Resource: syncer.OperationGames, Mode: syncer.OperationFull, Season: "2025", Stage: "Regular Season"}}
	if outcome, _, attempted, err := s.executeJob(context.Background(), job); err != nil || outcome != "deferred_scope_lease" || attempted {
		t.Fatalf("cold scope conflict = %q %t %v", outcome, attempted, err)
	}
	if !reflect.DeepEqual(store.releaseKeys, []string{coldSweepLeaseKey}) {
		t.Fatalf("partial acquisition releases = %v", store.releaseKeys)
	}
}

func TestColdExecutionDefersBeforeScopeWhenGlobalLeaseIsUnavailable(t *testing.T) {
	store := &planningStore{acquireResults: []bool{false}}
	s, err := New(store, &operationRunner{}, testPlannerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	job := Job{Class: JobCold, Kind: JobFullGames, Operation: syncer.Operation{Resource: syncer.OperationGames, Mode: syncer.OperationFull, Season: "2025", Stage: "Regular Season"}}
	if outcome, _, attempted, err := s.executeJob(context.Background(), job); err != nil || outcome != "deferred_global_lease" || attempted {
		t.Fatalf("cold global conflict = %q %t %v", outcome, attempted, err)
	}
	if len(store.keys) != 1 || len(store.releaseKeys) != 0 {
		t.Fatalf("global conflict leases = %v / %v", store.keys, store.releaseKeys)
	}
}

func TestSweepDueArchivedUsesMaintenanceTriggerAndFreshHotPreemption(t *testing.T) {
	now := time.Date(2033, 10, 3, 0, 0, 0, 0, time.UTC)
	archive := coldPlanningScope("2025", timePointer(now.Add(-time.Hour)))
	hot := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	store := &sequencePlanningStore{snapshots: []cache.PlanningSnapshot{{Scopes: []cache.PlanningScopeSnapshot{archive}}, {Scopes: []cache.PlanningScopeSnapshot{hot}}}}
	runner := &operationRunner{}
	s, err := New(store, runner, testPlannerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	report, err := s.SweepDueArchived(context.Background())
	if err != nil || !report.Deferred || report.Reason != "hot_work_due" || report.Requests != 1 || len(report.Entries) != 1 {
		t.Fatalf("sweep report = %+v, %v", report, err)
	}
	if len(runner.operations) != 1 || runner.operations[0].Trigger != cache.SourceTriggerMaintenance || runner.operations[0].Resource != syncer.OperationGames {
		t.Fatalf("maintenance operation = %+v", runner.operations)
	}
}

func TestSchedulerColdCorrectionDoesNotRecalculateHistoricalQualification(t *testing.T) {
	now := time.Date(2033, 10, 3, 0, 0, 0, 0, time.UTC)
	archive := coldPlanningScope("2025", timePointer(now.Add(-time.Hour)))
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{archive}}}
	runner := &historicalCorrectionRunner{}
	config := testPlannerConfig()
	config.Season = "2025"
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	s.check()
	if len(runner.operations) != 1 || runner.recalculations != 0 {
		t.Fatalf("operations=%d historical recalculations=%d", len(runner.operations), runner.recalculations)
	}
}

func TestSchedulerRefreshesCurrentCalculationsBeforeColdMaintenance(t *testing.T) {
	now := time.Date(2033, 10, 3, 0, 0, 0, 0, time.UTC)
	current := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("current", fixtures.PreMatchStatus, now.Add(time.Hour))})
	current.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season", NextFullDueAt: timePointer(now.Add(24 * time.Hour))}
	archive := coldPlanningScope("2025", timePointer(now.Add(-time.Hour)))
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{current, archive}}}
	runner := &historicalCorrectionRunner{}
	s, err := New(store, runner, testPlannerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	s.check()
	if len(runner.operations) != 1 || runner.operations[0].Season != "2025" || runner.recalculations != 1 {
		t.Fatalf("operations=%+v recalculations=%d", runner.operations, runner.recalculations)
	}
}

func coldPlanningScope(season string, due *time.Time) cache.PlanningScopeSnapshot {
	scope := planningScope(season, "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("one", fixtures.CompletedStatus, time.Date(2033, 1, 1, 0, 0, 0, 0, time.UTC))})
	scope.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
	last := time.Date(2033, 1, 1, 0, 0, 0, 0, time.UTC)
	scope.GamesFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGames, Season: season, Stage: "Regular Season", LastFullSuccessAt: &last, NextFullDueAt: due}
	lastXG := last
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: season, Stage: "Regular Season", LastFullSuccessAt: &lastXG, NextFullDueAt: due}
	return scope
}

func TestPlanUsesConfiguredCorrectionDefaults(t *testing.T) {
	now := time.Date(2033, 8, 3, 0, 0, 0, 0, time.UTC)
	first := now.Add(-time.Hour)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("final", fixtures.CompletedStatus, now.Add(-time.Hour))})
	scope.Games[0].HomeScore, scope.Games[0].AwayScore = score(1), score(0)
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "final", FirstTerminalObservedAt: &first, NextDueAt: timePointer(now)}}
	config := testPlannerConfig()
	config.ResultCorrectionInterval = 2 * time.Hour
	jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, config, now)
	if len(jobs) == 0 || jobs[0].Kind != JobCheckedGames || jobs[0].Operation.Requested[0].NextDueAfter != 2*time.Hour || jobs[0].Operation.Requested[0].MaterialNextDueAfter != 2*time.Hour {
		t.Fatalf("configured correction job = %+v", jobs)
	}
}

func TestSchedulerExecutesPlannedJobsSequentially(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("one", fixtures.PreMatchStatus, now.Add(-2*time.Hour))})
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "one", NextDueAt: timePointer(now)}}
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}}
	runner := &operationRunner{}
	s, err := New(store, runner, testPlannerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	s.check()
	if len(runner.operations) != 1 || runner.operations[0].Resource != syncer.OperationGames || runner.operations[0].Mode != syncer.OperationTargeted || store.acquired != 1 || store.released != 1 {
		t.Fatalf("operations=%+v leases=%d/%d", runner.operations, store.acquired, store.released)
	}
}

func TestSchedulerStartupUsesStartupSourceTrigger(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2025", "Regular Season", cache.SourceReadinessUnknown, nil)
	scope.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}}
	runner := &operationRunner{}
	config := testPlannerConfig()
	config.Season = "2026"
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	s.checkWithTrigger(cache.SourceTriggerStartup)
	if len(runner.operations) != 1 || runner.operations[0].Season != "2025" || runner.operations[0].Trigger != cache.SourceTriggerStartup {
		t.Fatalf("startup operations = %+v", runner.operations)
	}
}

func TestSchedulerStartupDrainsCatalogBootstrapInBudgetedBatches(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	current := planningScope("2026", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("current", fixtures.PreMatchStatus, now.Add(time.Hour))})
	current.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2026", Stage: "Regular Season", NextFullDueAt: timePointer(now.Add(time.Hour))}
	first := []cache.PlanningScopeSnapshot{current, historicalCatalogBootstrapScope("2025"), historicalCatalogBootstrapScope("2024"), historicalCatalogBootstrapScope("2023")}
	second := []cache.PlanningScopeSnapshot{current, historicalCatalogXGBootstrapScope("2022"), historicalCatalogXGBootstrapScope("2021")}
	store := &sequencePlanningStore{snapshots: []cache.PlanningSnapshot{
		{Scopes: first},  // first source batch
		{Scopes: second}, // decide that a second source batch is due
		{Scopes: second}, // second source batch
		{Scopes: []cache.PlanningScopeSnapshot{current}}, // end the drain
	}}
	runner := &startupDrainRunner{}
	config := testPlannerConfig()
	config.Season = "2026"
	config.StartupBootstrapInterval = time.Millisecond
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	runner.afterExecute = func() {
		if len(runner.operations) == 5 {
			s.Stop()
		}
	}
	s.Start()
	s.Wait()

	want := []struct {
		season   string
		resource syncer.OperationResource
	}{
		{"2025", syncer.OperationGames},
		{"2024", syncer.OperationGames},
		{"2023", syncer.OperationGames},
		{"2022", syncer.OperationGameXG},
		{"2021", syncer.OperationGameXG},
	}
	got := make([]struct {
		season   string
		resource syncer.OperationResource
	}, 0, len(runner.operations))
	for _, operation := range runner.operations {
		if operation.Trigger != cache.SourceTriggerStartup || operation.Mode != syncer.OperationFull {
			t.Fatalf("operation = %+v", operation)
		}
		got = append(got, struct {
			season   string
			resource syncer.OperationResource
		}{operation.Season, operation.Resource})
	}
	if !reflect.DeepEqual(got, want) || runner.overlapped || store.acquired != len(want) || store.released != len(want) || runner.recalculations != 1 {
		t.Fatalf("operations=%v overlap=%t leases=%d/%d recalculations=%d", got, runner.overlapped, store.acquired, store.released, runner.recalculations)
	}
}

func TestSchedulerStartupFailureDoesNotRapidlyRetryCatalogBootstrap(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{historicalCatalogBootstrapScope("2025")}}}
	runner := &startupDrainRunner{err: errors.New("ASA unavailable")}
	config := testPlannerConfig()
	config.Season = "2026"
	config.StartupBootstrapInterval = time.Millisecond
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	runner.afterExecute = s.Stop
	s.Start()
	s.Wait()
	if len(runner.operations) != 1 || store.acquired != 1 || store.released != 1 {
		t.Fatalf("failed startup operations=%+v leases=%d/%d", runner.operations, store.acquired, store.released)
	}
}

func TestSchedulerStartupDeferralAndNonCatalogJobsDoNotDrain(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	deferred := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{historicalCatalogBootstrapScope("2025")}}, acquireResults: []bool{false}}
	s, err := New(deferred, &startupDrainRunner{}, Config{Season: "2026", Stage: "Regular Season", CheckInterval: time.Minute, CompletionGrace: time.Hour, Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	if s.checkWithTrigger(cache.SourceTriggerStartup) {
		t.Fatal("deferred startup batch requested a rapid retry")
	}

	nonCatalog := planningScope("2033", "Invented", cache.SourceReadinessNotPublished, nil)
	nonCatalogScheduler, err := New(&planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{nonCatalog}}}, &startupDrainRunner{}, Config{Season: "2033", Stage: "Invented", CheckInterval: time.Minute, CompletionGrace: time.Hour, Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nonCatalogScheduler.now = func() time.Time { return now }
	if nonCatalogScheduler.startupCatalogBootstrapDue() {
		t.Fatal("non-catalog startup job started a bootstrap drain")
	}
}

func TestCatalogBootstrapJobIncludesOnlyCatalogInventoryAndInitialXG(t *testing.T) {
	for name, test := range map[string]struct {
		job  Job
		want bool
	}{
		"catalog inventory": {job: Job{Kind: JobFullGames, Reason: "missing_or_not_published_inventory", Operation: syncer.Operation{Season: "2025", Stage: "Playoffs"}}, want: true},
		"catalog xg":        {job: Job{Kind: JobFullXG, Reason: "initial_authoritative_xg", Operation: syncer.Operation{Season: "2025", Stage: "Playoffs"}}, want: true},
		"routine inventory": {job: Job{Kind: JobFullGames, Reason: "weekly_inventory_audit", Operation: syncer.Operation{Season: "2025", Stage: "Playoffs"}}, want: false},
		"non catalog":       {job: Job{Kind: JobFullGames, Reason: "missing_or_not_published_inventory", Operation: syncer.Operation{Season: "2033", Stage: "Invented"}}, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := catalogBootstrapJob(test.job); got != test.want {
				t.Fatalf("catalogBootstrapJob(%+v) = %t, want %t", test.job, got, test.want)
			}
		})
	}
}

func TestSchedulerStopPreventsStartingAnotherPlannedJob(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	current := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	upcoming := planningScope("2034", "Regular Season", cache.SourceReadinessNotPublished, nil)
	upcoming.Readiness.Scope.Lifecycle = cache.SourceScopeUpcoming
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{current, upcoming}}}
	runner := &operationRunner{}
	s, err := New(store, runner, testPlannerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	runner.afterExecute = s.Stop
	s.check()
	if len(runner.operations) != 1 || store.acquired != 1 || store.released != 1 {
		t.Fatalf("operations=%d leases=%d/%d", len(runner.operations), store.acquired, store.released)
	}
}

func TestSchedulerStopCancelsInFlightSourceJob(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}}
	runner := &blockingRunner{started: make(chan struct{}, 1)}
	config := testPlannerConfig()
	config.Timeout = time.Hour
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	s.Start()
	<-runner.started
	s.Stop()
	waitForScheduler(t, s)
	if store.acquired != 1 || store.released != 1 {
		t.Fatalf("leases = %d/%d, want the interrupted job's lease released", store.acquired, store.released)
	}
}

func TestSchedulerStopCancelsDetachedCalculation(t *testing.T) {
	store := &planningStore{}
	runner := &blockingRunner{started: make(chan struct{}, 1)}
	config := testPlannerConfig()
	config.Timeout = time.Hour
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	<-runner.started
	s.Stop()
	waitForScheduler(t, s)
}

func TestConfigNowControlsPlanningClock(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("one", fixtures.PreMatchStatus, now.Add(-2*time.Hour))})
	scope.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season"}
	scope.ResultChecks = []cache.GameResultCheckState{{GameID: "one", NextDueAt: timePointer(now)}}
	snapshot := cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}

	for _, test := range []struct {
		name string
		now  time.Time
		want int
	}{
		{name: "before the result check is due", now: now.Add(-time.Minute), want: 0},
		{name: "when the result check is due", now: now, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &planningStore{snapshot: snapshot}
			runner := &operationRunner{}
			config := testPlannerConfig()
			config.Now = func() time.Time { return test.now }
			s, err := New(store, runner, config, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CheckNow(context.Background()); err != nil {
				t.Fatalf("CheckNow: %v", err)
			}
			if len(runner.operations) != test.want {
				t.Fatalf("operations = %+v, want %d", runner.operations, test.want)
			}
			if test.want > 0 && !runner.operations[0].StartedAt.Equal(test.now) {
				t.Errorf("operation StartedAt = %s, want injected clock %s", runner.operations[0].StartedAt, test.now)
			}
		})
	}
}

func TestCheckNowUsesMaintenanceTriggerAndReturnsAfterJobsAndCalculations(t *testing.T) {
	now := time.Date(2033, 10, 3, 0, 0, 0, 0, time.UTC)
	current := planningScope("2033", "Regular Season", cache.SourceReadinessAvailable, []cache.Game{plannedGame("current", fixtures.PreMatchStatus, now.Add(time.Hour))})
	current.XGFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGameXG, Season: "2033", Stage: "Regular Season", NextFullDueAt: timePointer(now.Add(24 * time.Hour))}
	archive := coldPlanningScope("2025", timePointer(now.Add(-time.Hour)))
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{current, archive}}}
	runner := &historicalCorrectionRunner{}
	config := testPlannerConfig()
	config.Now = func() time.Time { return now }
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}
	if len(runner.operations) != 1 || runner.operations[0].Trigger != cache.SourceTriggerMaintenance {
		t.Fatalf("operations = %+v, want one maintenance-triggered job", runner.operations)
	}
	if runner.recalculations != 1 {
		t.Fatalf("recalculations = %d, want the preflight calculation finished before CheckNow returned", runner.recalculations)
	}
	if store.acquired == 0 || store.acquired != store.released {
		t.Fatalf("leases = %d/%d, want every acquired lease released before CheckNow returned", store.acquired, store.released)
	}
}

func TestCheckNowReportsFailedCheck(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}}
	runner := &startupDrainRunner{err: errors.New("source unavailable")}
	config := testPlannerConfig()
	config.Now = func() time.Time { return now }
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckNow(context.Background()); err == nil {
		t.Fatal("CheckNow error = nil, want failed check")
	}
	if len(runner.operations) != 1 {
		t.Fatalf("operations = %d, want one attempted job", len(runner.operations))
	}
}

func TestCheckNowStopsWhenCallerContextIsCanceled(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}}
	runner := &blockingRunner{started: make(chan struct{}, 1)}
	config := testPlannerConfig()
	config.Timeout = time.Hour
	config.Now = func() time.Time { return now }
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.CheckNow(ctx) }()
	<-runner.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CheckNow error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CheckNow did not return after its context was canceled")
	}
	if store.acquired != 1 || store.released != 1 {
		t.Fatalf("leases = %d/%d, want the interrupted job's lease released", store.acquired, store.released)
	}
	// The scheduler itself is still usable after a caller cancels one check.
	if s.stopped() {
		t.Fatal("caller cancellation stopped the scheduler")
	}
}

func TestCheckNowCallerCancellationInterruptsDetachedWork(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	store := &planningStore{snapshot: cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}}
	runner := &detachedBlockingRunner{started: make(chan struct{}, 1)}
	config := testPlannerConfig()
	config.Timeout = time.Hour
	config.Now = func() time.Time { return now }
	s, err := New(store, runner, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.CheckNow(ctx) }()
	<-runner.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CheckNow error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CheckNow did not return after its caller canceled detached work")
	}
	if s.stopped() {
		t.Fatal("caller cancellation stopped the scheduler")
	}
}

func TestSourceJobFailureLogLevel(t *testing.T) {
	now := time.Date(2033, 9, 1, 0, 0, 0, 0, time.UTC)
	scope := planningScope("2033", "Regular Season", cache.SourceReadinessNotPublished, nil)
	snapshot := cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}
	config := testPlannerConfig()
	config.Timeout = time.Hour
	config.Now = func() time.Time { return now }

	t.Run("caller cancellation is debug", func(t *testing.T) {
		logs := &levelRecorder{}
		runner := &blockingRunner{started: make(chan struct{}, 1)}
		s, err := New(&planningStore{snapshot: snapshot}, runner, config, slog.New(logs))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Stop)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- s.CheckNow(ctx) }()
		<-runner.started
		cancel()
		<-done
		if got := logs.levels("source job failed"); !reflect.DeepEqual(got, []slog.Level{slog.LevelDebug}) {
			t.Fatalf("source job failure levels = %v, want [DEBUG]", got)
		}
	})
	t.Run("source failure is error", func(t *testing.T) {
		logs := &levelRecorder{}
		runner := &startupDrainRunner{err: errors.New("source unavailable")}
		s, err := New(&planningStore{snapshot: snapshot}, runner, config, slog.New(logs))
		if err != nil {
			t.Fatal(err)
		}
		_ = s.CheckNow(context.Background())
		if got := logs.levels("source job failed"); !reflect.DeepEqual(got, []slog.Level{slog.LevelError}) {
			t.Fatalf("source job failure levels = %v, want [ERROR]", got)
		}
	})
}

// levelRecorder is a slog handler that records the level of each message.
type levelRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *levelRecorder) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record)
	return nil
}
func (h *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelRecorder) WithGroup(string) slog.Handler      { return h }
func (h *levelRecorder) levels(message string) []slog.Level {
	h.mu.Lock()
	defer h.mu.Unlock()
	var levels []slog.Level
	for _, record := range h.records {
		if record.Message == message {
			levels = append(levels, record.Level)
		}
	}
	return levels
}

func TestCheckNowAfterStopReturnsErrStopped(t *testing.T) {
	store := &planningStore{}
	runner := &operationRunner{}
	s, err := New(store, runner, testPlannerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Stop()
	if err := s.CheckNow(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("CheckNow error = %v, want ErrStopped", err)
	}
	if len(runner.operations) != 0 {
		t.Fatalf("operations = %+v, want none after Stop", runner.operations)
	}
}

func waitForScheduler(t *testing.T, s *Scheduler) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		s.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop its in-flight work")
	}
}

func planningScope(season, stage string, readiness cache.SourceReadiness, games []cache.Game) cache.PlanningScopeSnapshot {
	scope := cache.PlanningScopeSnapshot{Readiness: cache.SeasonReadinessSnapshot{Scope: cache.SourceScope{Season: season, Stage: stage, Lifecycle: cache.SourceScopeActive}, Readiness: readiness, Completeness: cache.InventoryCompletenessComplete}, Games: games}
	if readiness == cache.SourceReadinessAvailable {
		due := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
		scope.GamesFull = &cache.SourceResourceScopeState{Resource: cache.SourceResourceGames, Season: season, Stage: stage, NextFullDueAt: &due}
	}
	return scope
}

func historicalCatalogBootstrapScope(season string) cache.PlanningScopeSnapshot {
	scope := planningScope(season, "Playoffs", cache.SourceReadinessNotPublished, nil)
	scope.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
	return scope
}

func historicalCatalogXGBootstrapScope(season string) cache.PlanningScopeSnapshot {
	game := plannedGame("one", fixtures.CompletedStatus, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	game.HomeScore, game.AwayScore = score(1), score(0)
	scope := planningScope(season, "Playoffs", cache.SourceReadinessAvailable, []cache.Game{game})
	scope.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
	return scope
}

func plannedGame(id, status string, kickoff time.Time) cache.Game {
	return cache.Game{ASAID: id, Season: "2033", Stage: "Regular Season", KickoffUTC: kickoff.Format(time.RFC3339), Status: status, HomeTeamID: "home", AwayTeamID: "away"}
}
func score(value int) sql.NullInt64          { return sql.NullInt64{Int64: int64(value), Valid: true} }
func timePointer(value time.Time) *time.Time { return &value }
func jobIDs(job Job) []string {
	out := make([]string, len(job.Operation.Requested))
	for i, v := range job.Operation.Requested {
		out[i] = v.GameID
	}
	return out
}
func testPlannerConfig() Config {
	return Config{Season: "2033", Stage: "Regular Season", CheckInterval: 5 * time.Minute, CompletionGrace: time.Hour, Timeout: time.Second, SourceRequestBudget: 3}
}

type planningStore struct {
	snapshot           cache.PlanningSnapshot
	acquired, released int
	keys, releaseKeys  []string
	acquireResults     []bool
}

func (s *planningStore) PlanningSnapshot(context.Context) (cache.PlanningSnapshot, error) {
	return s.snapshot, nil
}
func (s *planningStore) TryAcquireSyncLease(_ context.Context, key, _ string, _ time.Time) (bool, error) {
	s.acquired++
	s.keys = append(s.keys, key)
	if len(s.acquireResults) > 0 {
		result := s.acquireResults[0]
		s.acquireResults = s.acquireResults[1:]
		return result, nil
	}
	return true, nil
}

type sequencePlanningStore struct {
	snapshots []cache.PlanningSnapshot
	reads     int
	planningStore
}

func (s *sequencePlanningStore) PlanningSnapshot(context.Context) (cache.PlanningSnapshot, error) {
	index := s.reads
	s.reads++
	if index >= len(s.snapshots) {
		return cache.PlanningSnapshot{}, nil
	}
	return s.snapshots[index], nil
}
func (s *planningStore) ReleaseSyncLease(_ context.Context, key, _ string) error {
	s.released++
	s.releaseKeys = append(s.releaseKeys, key)
	return nil
}

type operationRunner struct {
	operations   []syncer.Operation
	afterExecute func()
}

type startupDrainRunner struct {
	operations     []syncer.Operation
	recalculations int
	inFlight       bool
	overlapped     bool
	err            error
	afterExecute   func()
}

func (r *startupDrainRunner) Execute(_ context.Context, op syncer.Operation) (syncer.OperationResult, error) {
	if r.inFlight {
		r.overlapped = true
	}
	r.inFlight = true
	defer func() { r.inFlight = false }()
	r.operations = append(r.operations, op)
	if r.afterExecute != nil {
		r.afterExecute()
	}
	if r.err != nil {
		return syncer.OperationResult{Operation: op}, r.err
	}
	return syncer.OperationResult{Operation: op, Games: &cache.GameRefreshResult{}}, nil
}

func (r *startupDrainRunner) Recalculate(context.Context, syncer.RecalculateOptions) (cache.SyncRun, error) {
	r.recalculations++
	return cache.SyncRun{}, nil
}

type historicalCorrectionRunner struct {
	operationRunner
	recalculations int
}

func (r *historicalCorrectionRunner) Execute(_ context.Context, op syncer.Operation) (syncer.OperationResult, error) {
	r.operations = append(r.operations, op)
	return syncer.OperationResult{
		Operation:            op,
		Games:                &cache.GameRefreshResult{},
		FixtureInputsChanged: true,
	}, nil
}

func (r *historicalCorrectionRunner) Recalculate(context.Context, syncer.RecalculateOptions) (cache.SyncRun, error) {
	r.recalculations++
	return cache.SyncRun{}, nil
}

func (r *operationRunner) Execute(_ context.Context, op syncer.Operation) (syncer.OperationResult, error) {
	r.operations = append(r.operations, op)
	if r.afterExecute != nil {
		r.afterExecute()
	}
	return syncer.OperationResult{Operation: op, Games: &cache.GameRefreshResult{Audit: cache.SourceRefreshAudit{RequestedRows: len(op.Requested)}}}, nil
}

// detachedBlockingRunner detaches its source job from the request deadline,
// as derived calculations and forecast warming do, and holds it until the
// owning lifetime ends.
type detachedBlockingRunner struct {
	started chan struct{}
}

func (r *detachedBlockingRunner) Execute(ctx context.Context, op syncer.Operation) (syncer.OperationResult, error) {
	detached, cancel := lifetime.Detach(ctx)
	defer cancel()
	r.started <- struct{}{}
	<-detached.Done()
	return syncer.OperationResult{Operation: op}, detached.Err()
}

// blockingRunner holds source jobs and derived calculations until shutdown.
// Recalculate detaches like the syncer's derived calculations do.
type blockingRunner struct {
	started chan struct{}
}

func (r *blockingRunner) Execute(ctx context.Context, op syncer.Operation) (syncer.OperationResult, error) {
	r.started <- struct{}{}
	<-ctx.Done()
	return syncer.OperationResult{Operation: op}, ctx.Err()
}

func (r *blockingRunner) Recalculate(ctx context.Context, _ syncer.RecalculateOptions) (cache.SyncRun, error) {
	calculation, cancel := lifetime.Detach(ctx)
	defer cancel()
	r.started <- struct{}{}
	<-calculation.Done()
	return cache.SyncRun{QualificationError: calculation.Err().Error()}, nil
}

func TestColdGamesRefreshKeepsVerifiedCatalogInventory(t *testing.T) {
	now := time.Date(2033, 10, 3, 0, 0, 0, 0, time.UTC)
	entry, _ := competition.Lookup("2021", "Regular Season")
	for _, dueResource := range []string{"games", "xg"} {
		scope := coldPlanningScope("2021", timePointer(now.Add(-time.Hour)))
		scope.Readiness.ExpectedInventory = entry.Inventory
		if dueResource == "xg" {
			scope.GamesFull.NextFullDueAt = timePointer(now.Add(time.Hour))
			scope.XGFull.NextFullDueAt = timePointer(now.Add(-time.Hour))
			scope.XGFull.LastFullSuccessAt = scope.GamesFull.LastFullSuccessAt
		}
		jobs := Plan(cache.PlanningSnapshot{Scopes: []cache.PlanningScopeSnapshot{scope}}, testPlannerConfig(), now)
		if len(jobs) != 1 || jobs[0].Kind != JobFullGames || jobs[0].Operation.Expectation == nil || *jobs[0].Operation.Expectation != *entry.Inventory {
			t.Fatalf("%s due: %+v", dueResource, jobs)
		}
	}
}
