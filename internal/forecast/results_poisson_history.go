package forecast

import (
	"fmt"
)

const (
	resultsPoissonHomeTwoSeasonsID = "results-poisson-home-two-seasons-v1"
	resultsPoissonHomeHistoryID    = "results-poisson-home-history-v1"
)

// NewResultsPoissonHomeHistoryV1 keeps team attack and defence season-specific
// while estimating the league home and away scoring rates from all completed
// earlier seasons plus the current season to date.
func NewResultsPoissonHomeHistoryV1() Model { return resultsPoissonHomeHistoryV1{} }

// NewResultsPoissonHomeTwoSeasonsV1 uses the two most recent completed
// regular seasons plus current-season results for league venue rates.
func NewResultsPoissonHomeTwoSeasonsV1() Model { return resultsPoissonHomeHistoryV1{seasons: 2} }

type resultsPoissonHomeHistoryV1 struct{ seasons int }

func (p resultsPoissonHomeHistoryV1) Info() Info {
	if p.seasons == 2 {
		return resultsHomeHistoryInfo(resultsPoissonHomeTwoSeasonsID, "Results Poisson", "the two most recent completed regular seasons")
	}
	return resultsHomeHistoryInfo(resultsPoissonHomeHistoryID, "Results Poisson (all home/away history)", "all earlier completed seasons")
}

func resultsHomeHistoryInfo(id, name, scope string) Info {
	return Info{
		ID:          id,
		Name:        name,
		Description: "Simulates remaining games from current-season team scoring and defence, with the league home-field estimate pooled across " + scope + ".",
		Inputs:      "Completed match scores from " + scope + ", current-season completed match scores, and the remaining schedule.",
		Assumptions: "Home-field scoring rates are more stable across seasons than team attack and defence. Historical games are limited to those available before the forecast date.",
	}
}

func (p resultsPoissonHomeHistoryV1) Fit(input FitInput) (Predictor, error) {
	if p.seasons == 2 && input.HistoricalVenue.Matches > 0 {
		return fitResultsPoisson(input, input.Games, input.HistoricalVenue)
	}
	history, _ := historyPool(input, p.seasons)
	leagueGames := append(history, input.Games...)
	predictor, err := fitResultsPoisson(input, leagueGames, VenueSample{})
	if err != nil {
		return nil, err
	}
	if _, ok := predictor.(resultsPredictor); !ok {
		return nil, fmt.Errorf("results Poisson predictor has unexpected type %T", predictor)
	}
	return predictor, nil
}
