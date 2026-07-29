package forecast

import (
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestXGPoissonHomeHistoryChangesOnlyLeagueVenueRates(t *testing.T) {
	start := time.Date(2025, 5, 8, 19, 0, 0, 0, time.UTC)
	input := FitInput{
		Teams:            []standings.Team{{ID: "a"}, {ID: "b"}},
		Games:            []standings.Game{{ID: "current", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "b", Kickoff: start}},
		XGoals:           map[string]ExpectedGoals{"current": {GameID: "current", Home: 0, Away: 0}},
		HistoricalGames:  []standings.Game{{ID: "old/current", Status: standings.CompletedStatus, HomeTeamID: "old-a", AwayTeamID: "old-b", Kickoff: start.Add(-24 * time.Hour)}},
		HistoricalXGoals: map[string]ExpectedGoals{"old/current": {GameID: "old/current", Home: 4, Away: 0}},
	}
	seasonOnly, err := NewXGPoissonV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	homeHistory, err := NewXGPoissonHomeHistoryV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	seasonValue := seasonOnly.(xgPredictor)
	homeValue := homeHistory.(xgPredictor)
	if homeValue.homeRate == seasonValue.homeRate || homeValue.awayRate == seasonValue.awayRate {
		t.Fatalf("home-history value = %+v, want changed venue rates from %+v", homeValue, seasonValue)
	}
	if homeValue.teams["a"] != seasonValue.teams["a"] || homeValue.teams["b"] != seasonValue.teams["b"] {
		t.Fatalf("home-history team totals = %+v, want current-season totals %+v", homeValue.teams, seasonValue.teams)
	}
}

func TestXGPoissonTwoSeasonUsesPersistedVenueAggregate(t *testing.T) {
	input := FitInput{
		Teams:           []standings.Team{{ID: "a"}, {ID: "b"}},
		Games:           []standings.Game{{ID: "current", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "b"}},
		XGoals:          map[string]ExpectedGoals{"current": {GameID: "current", Home: 1, Away: 1}},
		HistoricalVenue: VenueSample{XGMatches: 10, HomeXG: 30, AwayXG: 5},
	}
	withHistory, err := NewXGPoissonHomeTwoSeasonsV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	input.HistoricalVenue = VenueSample{}
	seasonOnly, err := NewXGPoissonHomeTwoSeasonsV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	historical := withHistory.(xgPredictor)
	current := seasonOnly.(xgPredictor)
	if historical.homeRate == current.homeRate || historical.awayRate == current.awayRate {
		t.Fatalf("aggregate rates = %.3f/%.3f, current = %.3f/%.3f", historical.homeRate, historical.awayRate, current.homeRate, current.awayRate)
	}
	if historical.teams["a"] != current.teams["a"] || historical.teams["b"] != current.teams["b"] {
		t.Fatal("historical venue aggregate changed current team totals")
	}
}
