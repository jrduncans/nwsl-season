package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

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
