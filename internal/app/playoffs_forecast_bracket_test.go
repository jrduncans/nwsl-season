package app

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func TestPlayoffForecastPositionListsConserveProbability(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	seeds := playoffSeedsFrom("2026", store.regular)
	entry, _ := competition.Lookup("2026", "Playoffs")
	pairs := [][2]int{{1, 8}, {4, 5}, {2, 7}, {3, 6}, {1, 4}, {2, 3}, {1, 2}}
	for step, pair := range append([][2]int{{}}, pairs...) {
		if step > 0 {
			game := forecastPlayoffGame(seeds, fmt.Sprintf("position-%d", step), pair[0], pair[1], true)
			game.KickoffUTC = fmt.Sprintf("2026-11-%02dT20:00:00Z", step)
			store.playoffs.Games = append(store.playoffs.Games, game)
		}
		state, err := (&application{store: store}).forecastPostseason(context.Background(), "2026", store.regular, 8)
		if err != nil || state.Unavailable != "" {
			t.Fatalf("step %d state=%+v, %v", step, state, err)
		}
		result, err := simulation.Run(context.Background(), simulation.Request{Teams: store.regular.Teams, Games: standingsGames(store.regular.Games), Model: forecast.NewResultsPoissonV1(), Iterations: 200, PlayoffPlaces: 8, PlayoffBracket: entry.BracketFormat, Postseason: state})
		if err != nil {
			t.Fatal(err)
		}
		rounds := playoffBracketForecast(*entry.BracketFormat, state, result)
		for _, round := range rounds {
			for _, slot := range round.Slots {
				for _, side := range slot.Sides {
					sum := 0.0
					for _, team := range side.Teams {
						sum += team.Probability
					}
					if math.Abs(sum-1) > 1e-10 {
						t.Fatalf("step %d %s sum=%g", step, slot.Label, sum)
					}
				}
			}
		}
		if step == 0 && (len(rounds) != 3 || len(rounds[1].Slots[0].Sides) != 2 || len(rounds[1].Slots[0].Sides[0].Teams) != 4) {
			t.Fatalf("initial positions=%+v", rounds)
		}
		if step == 7 {
			if len(rounds) != 1 {
				t.Fatal("completed matches retained future slots")
			}
			champion := rounds[0].Slots[0].Sides[0].Teams
			if champion[0].Text != "100%" {
				t.Fatal("settled champion not exact")
			}
			for _, team := range champion[1:] {
				if team.Text != "Out" {
					t.Fatal("settled loser not out")
				}
			}
		}
	}
}

func TestPlayoffForecastBracketWithholdsBarsAndOdds(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	state, err := (&application{store: store}).forecastPostseason(context.Background(), "2026", store.regular, 8)
	if err != nil {
		t.Fatal(err)
	}
	state.Unavailable = "Unresolved result"
	state.Eliminated = []string{state.Seeds[8]}
	result := simulation.Result{}
	for _, team := range store.regular.Teams {
		result.Teams = append(result.Teams, simulation.TeamResult{Team: team})
	}
	entry, _ := competition.Lookup("2026", "Playoffs")
	for _, round := range playoffBracketForecast(*entry.BracketFormat, state, result) {
		for _, slot := range round.Slots {
			for _, side := range slot.Sides {
				for _, team := range side.Teams {
					if strings.Contains(team.Text, "%") || team.Width != "" {
						t.Fatalf("withheld odds leaked: %+v", team)
					}
					if team.Team.ID == state.Seeds[8] && team.Text != "Out" {
						t.Fatal("recorded elimination lost")
					}
				}
			}
		}
	}
}
