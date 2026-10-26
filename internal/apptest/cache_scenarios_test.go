package apptest_test

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestCacheSeasonTrendSupportsOpponentRatings(t *testing.T) {
	t.Parallel()
	for _, season := range apptest.CacheScenario(t, apptest.ScenarioSeasonTrend) {
		if season.Entry.Season != "2025" {
			continue
		}
		games := []standings.Game{}
		values := map[string]forecast.ExpectedGoals{}
		for _, game := range season.Data.Games {
			h, a := int(game.HomeScore.Int64), int(game.AwayScore.Int64)
			kickoff, err := fixtures.ParseKickoff(game.KickoffUTC)
			if err != nil {
				t.Fatal(err)
			}
			games = append(games, standings.Game{Kickoff: kickoff, ID: game.ASAID, Status: game.Status, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID, HomeScore: &h, AwayScore: &a})
		}
		for _, xg := range season.Data.XGoals {
			if xg.HomeXG.Valid && xg.AwayXG.Valid {
				values[xg.GameID] = forecast.ExpectedGoals{GameID: xg.GameID, Home: xg.HomeXG.Float64, Away: xg.AwayXG.Float64}
			}
		}
		adjustment, err := forecast.OpponentAdjustments(forecast.FitInput{Teams: season.Data.Teams, Games: games, XGoals: values})
		if err != nil {
			t.Fatal(err)
		}
		if len(adjustment.Fixtures) != 182 || adjustment.XGMatches != 182 {
			t.Fatalf("incomplete opponent ratings: %d fixtures, %d xG", len(adjustment.Fixtures), adjustment.XGMatches)
		}
	}
}
