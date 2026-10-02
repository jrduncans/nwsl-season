package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

// Forecasts use the archive's existing cache snapshot, including venue history,
// and share Forecast Lab's bounded executor, model, cache key and assumptions.
func (a *application) populateExploreForecastBenchmarks(ctx context.Context, teams *exploreTeamsView, archive []cache.HistoricalSeason) {
	for index := range teams.TeamSeasons {
		season := &teams.TeamSeasons[index]
		if !season.Active || len(season.Teams) == 0 {
			continue
		}
		for _, input := range archive {
			if input.Entry.Season != season.Season {
				continue
			}
			rules, verified := a.rulesForSeason(season.Season, input.Entry.Stage)
			scope := requestCompetition{Season: season.Season, Stage: input.Entry.Stage, Entry: input.Entry, Cataloged: true}
			if !scope.forecastAvailable(rules, verified) || !slices.ContainsFunc(input.Data.Games, func(game cache.Game) bool { return game.Status == fixtures.PreMatchStatus }) {
				break
			}
			entry := forecast.Default()
			state := forecaststate.State{Fixed: map[string]simulation.Outcome{}}
			places := playoffPlaces(rules)
			task := forecastTask{
				key:     forecastResultKey(input.Data, state, entry.Model.Info().ID, a.options.ForecastIterations, places),
				request: simulation.Request{Teams: input.Data.Teams, Games: standingsGames(input.Data.Games), XGoals: forecastXGoals(input.Data), HistoricalVenue: forecastVenueSample(input.Data), Model: entry.Model, Fixed: state.Fixed, Iterations: a.options.ForecastIterations, PlayoffPlaces: places, PlayoffBracket: forecastPlayoffBracket(season.Season, places)},
			}
			// This failure is absorbed into an unavailable comparison; the
			// executor's non-HTTP trigger preserves warning severity.
			results, err := a.forecasts.results(withForecastTrigger(ctx, "unspecified"), []forecastTask{task})
			if err != nil {
				break
			}
			season.Benchmarks = exploreTrendBenchmarks(*season, input, &results[0])
			for benchmarkIndex := range season.Benchmarks {
				benchmark := &season.Benchmarks[benchmarkIndex]
				if benchmark.Key != "playoff" && benchmark.Key != "top-four" && benchmark.Key != "shield" {
					continue
				}
				available, completed := forecastXGCoverage(input.Data)
				benchmark.Note += fmt.Sprintf(" xG coverage: %d of %d completed matches.", available, completed)
				if input.Data.LastSuccess != nil {
					benchmark.Note += " Fixture data updated " + input.Data.LastSuccess.FinishedAt.UTC().Format("2006-01-02 15:04 UTC") + "."
				}
				if input.Data.XGStatus.LastSuccess != nil {
					benchmark.Note += " xG data updated " + input.Data.XGStatus.LastSuccess.FinishedAt.UTC().Format("2006-01-02 15:04 UTC") + "."
				}
				if note := forecastScheduleNote(input.Data, input.Entry.Inventory, true, true); note != "" {
					benchmark.Note += " " + note
				}
			}
			break
		}
	}
}
