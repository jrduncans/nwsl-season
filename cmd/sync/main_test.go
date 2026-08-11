package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
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
