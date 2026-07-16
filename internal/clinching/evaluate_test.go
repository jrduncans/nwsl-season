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
func intp(v int) *int { return &v }
