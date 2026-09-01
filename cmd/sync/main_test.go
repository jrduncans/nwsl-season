package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

func TestHistoricalBackfillEntriesUseOnlySupportedCatalogHistory(t *testing.T) {
	entries := historicalBackfillEntries("2026")
	want := []string{"2025", "2024", "2023", "2022", "2021", "2019", "2018", "2017", "2016"}
	if len(entries) != len(want) {
		t.Fatalf("historical entries = %d, want %d", len(entries), len(want))
	}
	for i, season := range want {
		if entries[i].Season != season || entries[i].Stage != "Regular Season" || entries[i].Rules != nil {
			t.Fatalf("entry %d = %+v", i, entries[i])
		}
	}
	if got := historicalBackfillEntries("2025"); len(got) != len(want)-1 || got[0].Season != "2024" {
		t.Fatalf("configured historical exclusion = %+v", got)
	}
}

func TestRunHistoricalBackfillIsSequentialAndStopsAtFirstFailure(t *testing.T) {
	entries := historicalBackfillEntries("2026")[:3]
	var calls []syncer.RunOptions
	var output bytes.Buffer
	err := runHistoricalBackfill(entries, time.Second, func(_ context.Context, options syncer.RunOptions) (cache.SyncRun, error) {
		calls = append(calls, options)
		if len(calls) == 2 {
			return cache.SyncRun{}, errors.New("fixture request failed")
		}
		return cache.SyncRun{GamesUpserted: 12, XGRun: &cache.XGSyncRun{AvailableGames: 10, UnavailableGames: 2}}, nil
	}, &output)
	if err == nil || !strings.Contains(err.Error(), "2024 Regular Season") || len(calls) != 2 {
		t.Fatalf("backfill error/calls = %v / %+v", err, calls)
	}
	for i, call := range calls {
		if call.Season != entries[i].Season || call.Stage != entries[i].Stage || call.Trigger != "backfill" || !call.Force || !call.SourceOnly || call.ExpectedTeams != 0 || call.GamesPerTeam != 0 {
			t.Fatalf("run options %d = %+v", i, call)
		}
	}
	if got := output.String(); !strings.Contains(got, "Backfilled 2025 Regular Season: 12 games, 10 available xG and 2 unavailable xG.") || strings.Contains(got, "2024") {
		t.Fatalf("summary = %q", got)
	}
}

func TestRunHistoricalBackfillStopsOnXGFailureWithoutRequireFlag(t *testing.T) {
	entries := historicalBackfillEntries("2026")[:2]
	var calls int
	var output bytes.Buffer
	err := runHistoricalBackfill(entries, time.Second, func(context.Context, syncer.RunOptions) (cache.SyncRun, error) {
		calls++
		return cache.SyncRun{XGError: "xG unavailable"}, nil
	}, &output)
	if err == nil || !strings.Contains(err.Error(), "2025 Regular Season: xG refresh") || calls != 1 || output.Len() != 0 {
		t.Fatalf("xG error/calls/output = %v / %d / %q", err, calls, output.String())
	}
}

func TestCatalogBackfillOrdersCurrentThenHistoricalPrimaryThenSecondary(t *testing.T) {
	entries := catalogBackfillEntries("2026", "Regular Season")
	if len(entries) == 0 || entries[0].Season != "2026" || entries[0].Stage != "Regular Season" {
		t.Fatalf("configured primary is not first: %+v", entries)
	}
	firstHistoricalSecondary := -1
	for index, entry := range entries {
		if entry.Season != "2026" && !entry.Primary {
			firstHistoricalSecondary = index
			break
		}
		if firstHistoricalSecondary < 0 && entry.Season != "2026" && entry.Primary && index > 0 && entries[index-1].Season < entry.Season {
			t.Fatalf("historical primaries are not newest first: %+v", entries)
		}
	}
	if firstHistoricalSecondary < 0 {
		t.Fatal("catalog has no historical secondary stages")
	}
	for _, entry := range entries[:firstHistoricalSecondary] {
		if entry.Season != "2026" && !entry.Primary {
			t.Fatalf("historical secondary arrived before primary tier: %+v", entries)
		}
	}
	expected := make([]competition.Entry, 0)
	publicOrder := map[string]int{}
	for index, entry := range competition.PublicEntries() {
		publicOrder[entry.Season+"\x00"+entry.Stage] = index
		if entry.SourceAvailable {
			expected = append(expected, entry)
		}
	}
	sort.SliceStable(expected, func(i, j int) bool {
		priority := func(entry competition.Entry) int {
			if entry.Season == "2026" && entry.Stage == "Regular Season" {
				return 0
			}
			if entry.Season == "2026" {
				return 1
			}
			if entry.Primary {
				return 2
			}
			return 3
		}
		left, right := priority(expected[i]), priority(expected[j])
		if left != right {
			return left < right
		}
		if expected[i].Season != expected[j].Season {
			return expected[i].Season > expected[j].Season
		}
		return publicOrder[expected[i].Season+"\x00"+expected[i].Stage] < publicOrder[expected[j].Season+"\x00"+expected[j].Stage]
	})
	if len(entries) != len(expected) {
		t.Fatalf("catalog entries = %d, want every %d public source entry", len(entries), len(expected))
	}
	for index, entry := range expected {
		if entries[index].Season != entry.Season || entries[index].Stage != entry.Stage {
			t.Fatalf("catalog entry %d = %s %s, want %s %s", index, entries[index].Season, entries[index].Stage, entry.Season, entry.Stage)
		}
	}
}

func TestRunCatalogBackfillKeepsConfiguredScopeDerivedAndOthersSourceOnly(t *testing.T) {
	entries := catalogBackfillEntries("2026", "Regular Season")[:3]
	var calls []syncer.RunOptions
	var output bytes.Buffer
	teams := 0
	if err := runCatalogBackfill(entries, "2026", "Regular Season", time.Second, func(context.Context) error {
		teams++
		return nil
	}, func(_ context.Context, options syncer.RunOptions) (cache.SyncRun, error) {
		calls = append(calls, options)
		return cache.SyncRun{}, nil
	}, &output); err != nil {
		t.Fatal(err)
	}
	if teams != 1 || len(calls) != len(entries) || calls[0].SourceOnly || calls[0].Force {
		t.Fatalf("configured catalog call = %+v", calls)
	}
	for index, call := range calls[1:] {
		if !call.SourceOnly || call.Force || call.Trigger != "backfill" || call.Season != entries[index+1].Season || call.Stage != entries[index+1].Stage {
			t.Fatalf("catalog call %d = %+v", index+1, call)
		}
	}
}

func TestRunCatalogBackfillStopsAfterFirstFailure(t *testing.T) {
	entries := catalogBackfillEntries("2026", "Regular Season")[:3]
	teams, calls := 0, 0
	err := runCatalogBackfill(entries, "2026", "Regular Season", time.Second, func(context.Context) error {
		teams++
		return nil
	}, func(context.Context, syncer.RunOptions) (cache.SyncRun, error) {
		calls++
		if calls == 2 {
			return cache.SyncRun{}, errors.New("fixture request failed")
		}
		return cache.SyncRun{}, nil
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), entries[1].Season+" "+entries[1].Stage) || teams != 1 || calls != 2 {
		t.Fatalf("catalog failure/teams/calls = %v / %d / %d", err, teams, calls)
	}
}

func TestIncompatibleMaintenanceModes(t *testing.T) {
	if incompatibleMaintenanceModes(false, false, false, false, false) || incompatibleMaintenanceModes(false, false, true, false, false) || !incompatibleMaintenanceModes(false, true, true, false, false) || !incompatibleMaintenanceModes(true, false, true, false, true) {
		t.Fatal("maintenance mode incompatibility did not count catalog backfill")
	}
}

func TestRunStopsWhenSourceScopeSeedingFails(t *testing.T) {
	originalArgs := os.Args
	originalCommandLine := flag.CommandLine
	os.Args = []string{"sync", "-db", t.TempDir() + "/cache.sqlite"}
	flag.CommandLine = flag.NewFlagSet("sync", flag.ContinueOnError)
	t.Cleanup(func() {
		os.Args = originalArgs
		flag.CommandLine = originalCommandLine
	})
	originalEnsure := ensureSourceScopeRegistry
	ensureSourceScopeRegistry = func(context.Context, *cache.DB, string, string, time.Time) error {
		return errors.New("source scope registry unavailable")
	}
	t.Cleanup(func() { ensureSourceScopeRegistry = originalEnsure })
	if exitCode := run(); exitCode != 1 {
		t.Fatalf("run exit code = %d, want 1 after source-scope seeding failure", exitCode)
	}
}

func TestCountCalculationBudgetTimeouts(t *testing.T) {
	qualification := cache.QualificationSnapshot{Statuses: []cache.QualificationStatus{
		{Method: clinching.ProofComputeBudget},
		{Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "calculation budget exhausted"}},
		{Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "missing tiebreak data"}},
	}}
	scenario := cache.ScenarioSnapshot{Results: []cache.ScenarioResult{
		{Result: scenarios.Result{State: scenarios.OpportunityUnresolved, Limitation: "scenario computation budget exhausted"}},
		{Result: scenarios.Result{State: scenarios.OpportunityUnresolved, Limitation: "a clinch may depend on score"}},
	}}

	got := countCalculationBudgetTimeouts(qualification, scenario)
	want := calculationBudgetSummary{Qualification: 1, NoHelp: 1, Scenarios: 1}
	if got != want {
		t.Fatalf("countCalculationBudgetTimeouts() = %+v, want %+v", got, want)
	}
}
