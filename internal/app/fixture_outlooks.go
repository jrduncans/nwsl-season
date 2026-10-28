package app

import (
	"context"
	"math"

	"github.com/jrduncans/nwsl-season/internal/bracket"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/simulation"

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

// playoffOutlooks fits once on the same season's regular-season data. Both
// public match surfaces use this map; scenarios never enter this path.
func (a *application) playoffOutlooks(ctx context.Context, season string, data cache.SeasonData) map[string]fixtureOutlookView {
	regular, ok := competition.Lookup(season, "Regular Season")
	rules, verified := a.rulesForSeason(season, "Regular Season")
	scope := requestCompetition{Season: season, Stage: "Regular Season", Entry: regular, Cataloged: ok}
	if !scope.forecastAvailable(rules, verified) {
		return nil
	}
	entry, ok := competition.Lookup(season, "Playoffs")
	if !ok || entry.BracketFormat == nil {
		return nil
	}
	training, err := a.loadSeasonData(ctx, season, "Regular Season")
	if err != nil {
		return nil
	}
	seeds := playoffSeedsFrom(season, training)
	view := bracket.Build(*entry.BracketFormat, data.Games)
	if seeds.Certified {
		view = bracket.BuildWithSeeds(*entry.BracketFormat, data.Games, seeds.Seeds)
	}
	if view.State == bracket.StateFormatMismatch || view.State == bracket.StateUnresolved {
		return nil
	}
	model := forecast.Default().Model
	predictor, err := model.Fit(forecast.FitInput{Teams: training.Teams, Games: standingsGames(training.Games), XGoals: forecastXGoals(training), HistoricalVenue: forecastVenueSample(training)})
	if err != nil {
		return nil
	}
	result := map[string]fixtureOutlookView{}
	for ri, round := range view.Rounds {
		for _, slot := range round.Slots {
			if slot.Home.TeamID == bracket.TBD || slot.Away.TeamID == bracket.TBD || slot.Home.TeamID == "" || slot.Away.TeamID == "" || (slot.Game != nil && slot.Game.Status != fixtures.PreMatchStatus) {
				continue
			}
			home, away := slot.Home.TeamID, slot.Away.TeamID
			if slot.Game == nil {
				seedByID := map[string]int{}
				for seed, id := range seeds.Seeds {
					seedByID[id] = seed
				}
				if seedByID[home] > seedByID[away] {
					home, away = away, home
				}
			}
			odds, err := simulation.PlayoffMatchOutlook(predictor, home, away, ri == len(view.Rounds)-1)
			if err != nil || !validFixtureOutcomes(odds.OutcomeProbabilities) {
				continue
			}
			// Orient expected slots like the bracket even if topology and seed order differ.
			if home != slot.Home.TeamID {
				odds.HomeWin, odds.AwayWin = odds.AwayWin, odds.HomeWin
				odds.HomeAdvance, odds.AwayAdvance = odds.AwayAdvance, odds.HomeAdvance
			}
			value := fixtureOutlookView{Playoff: true, ModelName: model.Info().Name, HomeWin: odds.HomeWin, Draw: odds.Draw, AwayWin: odds.AwayWin, HomeWinText: percent(odds.HomeWin), DrawText: percent(odds.Draw), AwayWinText: percent(odds.AwayWin), HomeAdvanceText: percent(odds.HomeAdvance), AwayAdvanceText: percent(odds.AwayAdvance)}
			result[slot.ID] = value
			if slot.Game != nil {
				result[slot.Game.ASAID] = value
			}
		}
	}
	return result
}
