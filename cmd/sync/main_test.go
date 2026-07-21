package main

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

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
