package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

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
