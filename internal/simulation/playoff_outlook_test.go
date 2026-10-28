package simulation

import (
	"context"
	"math"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestPlayoffMatchOutlookSharesAdvancementAndNeutralAveraging(t *testing.T) {
	predictor, err := forecast.NewResultsPoissonV1().Fit(forecast.FitInput{Teams: testPlayoffTeams()})
	if err != nil {
		t.Fatal(err)
	}
	home, err := PlayoffMatchOutlook(predictor, "a", "b", false)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := PlayoffMatchOutlook(predictor, "b", "a", false)
	if err != nil {
		t.Fatal(err)
	}
	neutral, err := PlayoffMatchOutlook(predictor, "a", "b", true)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(home.HomeAdvance-home.HomeWin/(home.HomeWin+home.AwayWin)) > 1e-10 {
		t.Fatalf("advancement drift: %+v", home)
	}
	if math.Abs(neutral.HomeAdvance-(home.HomeAdvance+reverse.AwayAdvance)/2) > 1e-10 || math.Abs(neutral.HomeWin-(home.HomeWin+reverse.AwayWin)/2) > 1e-10 {
		t.Fatalf("neutral averaging: %+v", neutral)
	}
}

func TestPostseasonRoundAndPositionConservation(t *testing.T) {
	teams := testPlayoffTeams()
	state := &Postseason{Seeds: map[int]string{}, Slots: map[string]PostseasonMatch{"quarterfinal-1": {Home: "a", Away: "h", Winner: "a"}}}
	for i, team := range teams {
		state.Seeds[i+1] = team.ID
	}
	request := Request{Teams: teams, Model: forecast.NewResultsPoissonV1(), Iterations: 300, PlayoffPlaces: 8, PlayoffBracket: testPlayoffBracket(t), Postseason: state}
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	sums := map[string]float64{}
	for _, team := range result.Teams {
		total := 0.0
		for round, p := range team.RoundReach {
			sums[round] += p
		}
		for _, p := range team.RunEnds {
			total += p
		}
		if math.Abs(total-1) > 1e-10 {
			t.Fatalf("%s exclusive probabilities sum to %g", team.Team.ID, total)
		}
		if team.Team.ID == "a" && (!team.ReachedRounds["semifinals"] || team.RoundReach["semifinals"] != 1) {
			t.Fatalf("winner not through: %+v", team)
		}
		if team.Team.ID == "h" && (team.RunEnds["quarterfinals"] != 1 || team.EliminatedRound != "quarterfinals") {
			t.Fatalf("recorded loser: %+v", team)
		}
	}
	for round, want := range map[string]float64{"quarterfinals": 8, "semifinals": 4, "final": 2} {
		if math.Abs(sums[round]-want) > 1e-10 {
			t.Fatalf("%s reach = %g", round, sums[round])
		}
	}
	for slot, sides := range result.BracketPositions {
		for _, side := range sides {
			total := 0.0
			for _, p := range side {
				total += p
			}
			if math.Abs(total-1) > 1e-10 {
				t.Fatalf("%s side sums to %g", slot, total)
			}
		}
	}
}

func TestPostseasonScenariosValidateParticipantsAndResults(t *testing.T) {
	teams := testPlayoffTeams()
	state := &Postseason{Seeds: map[int]string{}, Slots: map[string]PostseasonMatch{}, Scenarios: map[string]PostseasonMatch{}}
	for i, team := range teams {
		state.Seeds[i+1] = team.ID
	}
	request := Request{Teams: teams, Model: forecast.NewResultsPoissonV1(), Iterations: 50, PlayoffPlaces: 8, PlayoffBracket: testPlayoffBracket(t), Postseason: state}
	baseline, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	state.Scenarios["quarterfinal-1"] = PostseasonMatch{Home: "a", Away: "h", Winner: "h"}
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Seed == baseline.Seed {
		t.Fatal("scenario omitted from random seed")
	}
	for _, row := range result.Teams {
		if row.Team.ID == "a" && !row.ChampionshipEliminated {
			t.Fatal("scenario loser still contends")
		}
	}
	for _, tc := range []struct {
		slot  string
		match PostseasonMatch
	}{
		{"quarterfinal-1", PostseasonMatch{Home: "a", Away: "g", Winner: "a"}},
		{"quarterfinal-1", PostseasonMatch{Home: "a", Away: "h", Winner: "b"}},
		{"semifinal-1", PostseasonMatch{Home: "a", Away: "d", Winner: "a"}},
		{"unknown", PostseasonMatch{Home: "a", Away: "h", Winner: "a"}},
	} {
		state.Scenarios = map[string]PostseasonMatch{tc.slot: tc.match}
		if CheckPostseason(request) == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	state.Scenarios = map[string]PostseasonMatch{"quarterfinal-1": {Home: "a", Away: "h", Winner: "h"}}
	state.Slots["quarterfinal-1"] = PostseasonMatch{Home: "a", Away: "h", Winner: "h"}
	if CheckPostseason(request) == nil {
		t.Fatal("accepted scenario on recorded result")
	}
}

func TestExclusiveRunsIncludeNonqualifiers(t *testing.T) {
	teams := append(testPlayoffTeams(), standings.Team{ID: "nonqualifier"})
	state := &Postseason{Seeds: map[int]string{}}
	for i, team := range teams[:8] {
		state.Seeds[i+1] = team.ID
	}
	result, err := Run(context.Background(), Request{Teams: teams, Model: forecast.NewResultsPoissonV1(), Iterations: 10, PlayoffPlaces: 8, PlayoffBracket: testPlayoffBracket(t), Postseason: state})
	if err != nil {
		t.Fatal(err)
	}
	for _, team := range result.Teams {
		total := 0.0
		for _, p := range team.RunEnds {
			total += p
		}
		if math.Abs(total-1) > 1e-10 {
			t.Fatalf("%s run sum=%g", team.Team.ID, total)
		}
		if team.Team.ID == "nonqualifier" && team.RunEnds["did-not-qualify"] != 1 {
			t.Fatal("nonqualifier missing")
		}
	}
}
