package clinching

import (
	"context"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"testing"
)

func newGame(id, home, away string) standings.Game {
	return standings.Game{ID: id, Status: "PreMatch", HomeTeamID: home, AwayTeamID: away}
}
func TestEvaluatePointsClassifications(t *testing.T) {
	teams := []standings.Team{{ID: "t"}, {ID: "a"}, {ID: "b"}}
	achievement := competition.Achievement{ID: "test", Label: "Test", TopK: 1}
	result, err := Evaluate(context.Background(), Request{Teams: teams, Games: []standings.Game{newGame("a-b", "a", "b")}, FixtureOrder: []string{"a-b"}, TargetTeamID: "t", Achievement: achievement})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != NotClinched || len(result.BlockingWitness) != 1 {
		t.Fatalf("not-clinched result=%+v", result)
	}
	// A target already six points clear cannot be caught by either opponent.
	six := 6
	games := []standings.Game{{ID: "t-a", Status: standings.CompletedStatus, HomeTeamID: "t", AwayTeamID: "a", HomeScore: &six, AwayScore: intp(0)}, {ID: "t-b", Status: standings.CompletedStatus, HomeTeamID: "t", AwayTeamID: "b", HomeScore: &six, AwayScore: intp(0)}}
	result, err = Evaluate(context.Background(), Request{Teams: teams, Games: games, FixtureOrder: []string{}, TargetTeamID: "t", Achievement: achievement})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Clinched {
		t.Fatalf("clinched result=%+v", result)
	}
}

func TestCompletedUndeterminedTieIsComparedWithCutoff(t *testing.T) {
	teams := []standings.Team{{ID: "target"}, {ID: "rival"}, {ID: "sink-a"}, {ID: "sink-b"}}
	zero, one := 0, 1
	games := []standings.Game{
		{ID: "target-win", Status: standings.CompletedStatus, HomeTeamID: "target", AwayTeamID: "sink-a", HomeScore: &one, AwayScore: &zero},
		{ID: "rival-win", Status: standings.CompletedStatus, HomeTeamID: "rival", AwayTeamID: "sink-b", HomeScore: &one, AwayScore: &zero},
	}

	topThree := competition.Achievement{ID: "top-three", Label: "Top three", TopK: 3}
	result, err := Evaluate(context.Background(), Request{Teams: teams, Games: games, FixtureOrder: []string{}, TargetTeamID: "target", Achievement: topThree})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Clinched || result.Method != ProofAccessibleTiebreak {
		t.Fatalf("top-three result = %+v, want accessible-tiebreak clinch", result)
	}
	if result.StrictlyAhead != (CountEvidence{Value: 0, Kind: "lower_bound"}) ||
		result.AtLeastLevel != (CountEvidence{Value: 1, Kind: "upper_bound"}) {
		t.Fatalf("top-three evidence = %+v/%+v, want possible rank range 1-2", result.StrictlyAhead, result.AtLeastLevel)
	}

	shield := competition.Achievement{ID: "shield", Label: "Shield", TopK: 1}
	result, err = Evaluate(context.Background(), Request{Teams: teams, Games: games, FixtureOrder: []string{}, TargetTeamID: "target", Achievement: shield})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Unresolved || result.Method != ProofMissingDisciplinary {
		t.Fatalf("shield result = %+v, want unresolved cutoff-crossing tie", result)
	}
}

func TestCompletedTieCountsAccessibleTeamsDefinitelyAhead(t *testing.T) {
	teams := []standings.Team{{ID: "leader"}, {ID: "target"}, {ID: "rival"}, {ID: "sink-a"}, {ID: "sink-b"}, {ID: "sink-c"}}
	zero, one, two := 0, 1, 2
	games := []standings.Game{
		{ID: "leader-win", Status: standings.CompletedStatus, HomeTeamID: "leader", AwayTeamID: "sink-a", HomeScore: &two, AwayScore: &zero},
		{ID: "target-win", Status: standings.CompletedStatus, HomeTeamID: "target", AwayTeamID: "sink-b", HomeScore: &one, AwayScore: &zero},
		{ID: "rival-win", Status: standings.CompletedStatus, HomeTeamID: "rival", AwayTeamID: "sink-c", HomeScore: &one, AwayScore: &zero},
	}
	shield := competition.Achievement{ID: "shield", Label: "Shield", TopK: 1}

	result, err := Evaluate(context.Background(), Request{Teams: teams, Games: games, FixtureOrder: []string{}, TargetTeamID: "target", Achievement: shield})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != NotClinched || result.Method != ProofAccessibleTiebreak {
		t.Fatalf("result = %+v, want leader to settle non-clinch before disciplinary tie", result)
	}
	if result.StrictlyAhead != (CountEvidence{Value: 1, Kind: "lower_bound"}) {
		t.Fatalf("strictly-ahead evidence = %+v, want one definitely-ahead team", result.StrictlyAhead)
	}
}

func intp(v int) *int { return &v }
