package forecast

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestResultsPoissonTwoSeasonUsesAggregateWithoutChangingTeamTotals(t *testing.T) {
	zero := 0
	input := FitInput{
		Teams:           []standings.Team{{ID: "a"}, {ID: "b"}},
		Games:           []standings.Game{{ID: "current", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "b", HomeScore: &zero, AwayScore: &zero}},
		HistoricalVenue: VenueSample{Matches: 10, HomeGoals: 30, AwayGoals: 5},
	}
	withHistory, err := NewResultsPoissonHomeTwoSeasonsV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	input.HistoricalVenue = VenueSample{}
	seasonOnly, err := NewResultsPoissonHomeTwoSeasonsV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	historical := withHistory.(resultsPredictor)
	current := seasonOnly.(resultsPredictor)
	if historical.homeRate == current.homeRate || historical.awayRate == current.awayRate {
		t.Fatalf("aggregate rates = %.3f/%.3f, current = %.3f/%.3f", historical.homeRate, historical.awayRate, current.homeRate, current.awayRate)
	}
	if historical.teams["a"] != current.teams["a"] || historical.teams["b"] != current.teams["b"] {
		t.Fatal("historical venue aggregate changed current team totals")
	}
}
