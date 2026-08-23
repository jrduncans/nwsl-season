package app

import (
	"math"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/forecast"
)

// fixtureOutlooks fits the recommended xG Poisson model once for a season
// snapshot, then derives its exact result distribution for each remaining
// fixture. It deliberately does not call the season simulator: match outcome
// probabilities are already available from the model's Poisson distribution.
//
// The fixtures page is still useful without forecast-ready data, so a fit or
// individual-distribution failure simply leaves that fixture without an
// outlook.
func fixtureOutlooks(data cache.SeasonData) map[string]fixtureOutlookView {
	return fixtureOutlooksFor(data, forecast.Default().Model)
}

func fixtureOutlooksFor(data cache.SeasonData, model forecast.Model) map[string]fixtureOutlookView {
	games := standingsGames(data.Games)
	predictor, err := model.Fit(forecast.FitInput{
		Teams:           data.Teams,
		Games:           games,
		XGoals:          forecastXGoals(data),
		HistoricalVenue: forecastVenueSample(data),
	})
	if err != nil {
		return nil
	}

	outlooks := make(map[string]fixtureOutlookView)
	for _, game := range games {
		if game.Status != remainingStatus {
			continue
		}
		distribution, err := predictor.Distribution(game)
		if err != nil {
			continue
		}
		outcomes := distribution.Outcomes()
		if !validFixtureOutcomes(outcomes) {
			continue
		}
		outlooks[game.ID] = fixtureOutlookView{
			ModelName:   model.Info().Name,
			HomeWin:     outcomes.HomeWin,
			Draw:        outcomes.Draw,
			AwayWin:     outcomes.AwayWin,
			HomeWinText: percent(outcomes.HomeWin),
			DrawText:    percent(outcomes.Draw),
			AwayWinText: percent(outcomes.AwayWin),
		}
	}
	return outlooks
}

func validFixtureOutcomes(outcomes forecast.OutcomeProbabilities) bool {
	values := []float64{outcomes.HomeWin, outcomes.Draw, outcomes.AwayWin}
	total := 0.0
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return false
		}
		total += value
	}
	return math.Abs(total-1) < 1e-9
}
