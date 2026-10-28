package simulation

import (
	"context"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/forecast"
)

func TestPostseasonSimulatesOnlyUnresolvedRounds(t *testing.T) {
	state := &Postseason{Seeds: map[int]string{}, Slots: map[string]PostseasonMatch{}}
	teams := testPlayoffTeams()
	for i, team := range teams {
		state.Seeds[i+1] = team.ID
	}
	request := Request{Teams: teams, Model: forecast.NewResultsPoissonV1(), Iterations: 100, PlayoffPlaces: 8, PlayoffBracket: testPlayoffBracket(t), Postseason: state}
	type playedSlot struct {
		slot, home, away, winner string
	}
	progress := []playedSlot{
		{},
		{"quarterfinal-1", "a", "h", "h"},
		{"quarterfinal-2", "d", "e", "d"},
		{"quarterfinal-3", "b", "g", "b"},
		{"quarterfinal-4", "c", "f", "c"},
		{"semifinal-1", "d", "h", "h"},
		{"semifinal-2", "b", "c", "c"},
		{"final", "h", "c", "h"},
	}
	eliminated := map[string]bool{}
	var lastSeed uint64
	for step, game := range progress {
		if step > 0 {
			state.Slots[game.slot] = PostseasonMatch{Home: game.home, Away: game.away, Winner: game.winner}
			loser := game.home
			if loser == game.winner {
				loser = game.away
			}
			eliminated[loser] = true
		}
		result, err := Run(context.Background(), request)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if step > 0 && result.Seed == lastSeed {
			t.Fatal("playoff advancement did not change deterministic seed")
		}
		lastSeed = result.Seed
		total := 0.0
		for _, team := range result.Teams {
			total += team.ChampionshipProbability
			if team.ChampionshipEliminated != eliminated[team.Team.ID] {
				t.Fatalf("step %d: %s elimination is not factual", step, team.Team.ID)
			}
			if eliminated[team.Team.ID] && team.ChampionshipProbability != 0 {
				t.Fatalf("eliminated team %s still has odds", team.Team.ID)
			}
			if step == len(progress)-1 && team.Team.ID == "h" && (!team.ChampionshipClinched || team.ChampionshipProbability != 1) {
				t.Fatal("confirmed champion did not retain 100%")
			}
		}
		if total < .999999 || total > 1.000001 {
			t.Fatalf("step %d odds sum to %f", step, total)
		}
	}
	state.Unavailable = "unresolved source result"
	result, err := Run(context.Background(), request)
	if err != nil || result.ChampionshipSimulated {
		t.Fatalf("unavailable championship was simulated: %+v, %v", result, err)
	}
}

func TestPostseasonRejectsProgressWithoutActualPriorWinners(t *testing.T) {
	state := &Postseason{Seeds: map[int]string{}, Slots: map[string]PostseasonMatch{"semifinal-1": {Home: "a", Away: "d", Winner: "a"}}}
	teams := testPlayoffTeams()
	for i, team := range teams {
		state.Seeds[i+1] = team.ID
	}
	_, err := Run(context.Background(), Request{Teams: teams, Model: fixedModel{score: forecast.Scoreline{Home: 1}}, Iterations: 1, PlayoffPlaces: 8, PlayoffBracket: testPlayoffBracket(t), Postseason: state})
	if err == nil {
		t.Fatal("invented prior-round winners for a published semifinal")
	}
}

func TestCheckPostseasonMatchesRunValidation(t *testing.T) {
	state := &Postseason{Seeds: map[int]string{}, Slots: map[string]PostseasonMatch{"semifinal-1": {Home: "a", Away: "d", Winner: "a"}}}
	teams := testPlayoffTeams()
	for i, team := range teams {
		state.Seeds[i+1] = team.ID
	}
	request := Request{Teams: teams, PlayoffPlaces: 8, PlayoffBracket: testPlayoffBracket(t), Postseason: state}
	if CheckPostseason(request) == nil {
		t.Fatal("check accepted progress the simulator rejects")
	}
	state.Slots = map[string]PostseasonMatch{"quarterfinal-1": {Home: "a", Away: "h", Winner: "a"}}
	if err := CheckPostseason(request); err != nil {
		t.Fatalf("check rejected valid progress: %v", err)
	}
}
