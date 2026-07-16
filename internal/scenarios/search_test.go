package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestGenerateFindsMinimalOutcomeOnlyClause(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}}
	game := standings.Game{ID: "g1", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"}
	slate, err := DefineSlate([]ScheduledGame{{ID: "g1", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC), Matchday: intPtr(4)}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, []standings.Game{game}, []string{"g1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(context.Background(), Request{
		Evaluator: evaluator, Teams: teams, Games: []standings.Game{game}, Slate: slate,
		TargetTeamID: "a", Achievement: competition.Achievement{ID: competition.AchievementPlayoffs, TopK: 1},
		Baseline: clinching.AchievementResult{TeamID: "a", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.NotClinched},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != OpportunityCanClinch || !result.CanClinch {
		t.Fatalf("result = %+v, want can clinch", result)
	}
	if len(result.Clauses) != 1 || len(result.Clauses[0].Conditions) != 1 {
		t.Fatalf("clauses = %+v, want one single-fixture clause", result.Clauses)
	}
	condition := result.Clauses[0].Conditions[0]
	if condition.GameID != "g1" || len(condition.AllowedOutcomes) != 1 || condition.AllowedOutcomes[0] != clinching.HomeWin {
		t.Fatalf("condition = %+v, want g1 home win", condition)
	}
	if result.TotalAssignments != 3 || result.CertifiedAssignments != 1 || result.Diagnostics.ElapsedMicroseconds <= 0 {
		t.Fatalf("result diagnostics = %+v, want completed timing and one certified assignment", result)
	}
}

func TestDefineSlateUsesKickoffWindowWhenMatchdayIsUnavailable(t *testing.T) {
	start := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	slate, err := DefineSlate([]ScheduledGame{
		{ID: "near", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: start},
		{ID: "inside", Status: "PreMatch", HomeTeamID: "c", AwayTeamID: "d", KickoffUTC: start.Add(119 * time.Hour)},
		{ID: "outside", Status: "PreMatch", HomeTeamID: "e", AwayTeamID: "f", KickoffUTC: start.Add(120 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if slate.Source != SourceKickoffWindow || len(slate.FixtureIDs) != 2 || slate.FixtureIDs[0] != "near" || slate.FixtureIDs[1] != "inside" {
		t.Fatalf("slate = %+v, want deterministic 120-hour kickoff window", slate)
	}
}

func TestResultValidateRejectsEmptyConditionOutcomes(t *testing.T) {
	slate := Slate{ID: "s", DefinitionVersion: DefinitionVersion, State: SlateReady, Source: SourceKickoffWindow, StartsAtUTC: time.Unix(1, 0), LatestKickoffUTC: time.Unix(1, 0), CutoffUTC: time.Unix(2, 0), FixtureIDs: []string{"g"}}
	result := Result{TeamID: "a", Achievement: competition.AchievementPlayoffs, TopK: 1, State: OpportunityCanClinch, CanClinch: true, TotalAssignments: 3, CertifiedAssignments: 1, Clauses: []Clause{{Conditions: []FixtureCondition{{GameID: "g", AllowedOutcomes: []clinching.Outcome{}}}, RepresentedAssignments: 1, ProofMethods: []clinching.ProofMethod{clinching.ProofCheapBound}}}, Necessary: []FixtureCondition{}, ProofMethods: []clinching.ProofMethod{}}
	if err := result.Validate(slate); err == nil {
		t.Fatal("empty allowed outcomes were accepted")
	}
}

func intPtr(value int) *int { return &value }
