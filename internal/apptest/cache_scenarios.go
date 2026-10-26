package apptest

import (
	"fmt"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// CacheScenario supplies full historical fixture inventories for SQLite-backed
// tests and preview. Scenario keeps small, explicitly labelled snapshots for
// unit tests; these scenes instead pass the real catalog completeness checks.
func CacheScenario(t testing.TB, name string) []cache.HistoricalSeason {
	t.Helper()
	if name == ScenarioEmpty {
		return Scenario(t, name)
	}
	archive := CompleteScenarioInventories(t, Scenario(t, name))
	if name == ScenarioOverview {
		for i := range archive {
			season := &archive[i]
			indexes := map[string]int{}
			for j, team := range season.Data.Teams {
				indexes[team.ID] = j
			}
			strength := func(id string) int {
				switch season.Entry.Season {
				case "2018":
					if indexes[id] < 7 {
						return 1
					}
				case "2024":
					if indexes[id] < 2 {
						return 1
					}
				case "2025":
					if indexes[id] == 0 {
						return 1
					}
				}
				return 0
			}
			if season.Entry.Season != "2018" && season.Entry.Season != "2024" && season.Entry.Season != "2025" {
				continue
			}
			for j := range season.Data.Games {
				game := &season.Data.Games[j]
				game.HomeScore.Int64, game.AwayScore.Int64 = 1, 1
				if strength(game.HomeTeamID) > strength(game.AwayTeamID) {
					game.HomeScore.Int64 = 2
				}
				if strength(game.HomeTeamID) < strength(game.AwayTeamID) {
					game.AwayScore.Int64 = 2
				}
			}
		}
	}
	if name == ScenarioSeasonTrend {
		cacheSeasonTrendExamples(archive)
	}
	return archive
}

// Keep a pair of explicit Angel City fixtures for numeric and penalty-basis
// browser checks while the full recorded schedule supplies all match counts.
func cacheSeasonTrendExamples(archive []cache.HistoricalSeason) {
	for i := range archive {
		season := &archive[i]
		if season.Entry.Season != "2025" {
			continue
		}
		modified := map[string]int{}
		next := 0
		for j := range season.Data.Games {
			game := &season.Data.Games[j]
			if game.HomeTeamID != "kRQa8JOqKZ" && game.AwayTeamID != "kRQa8JOqKZ" {
				continue
			}
			if next == 2 {
				break
			}
			if game.AwayTeamID == "kRQa8JOqKZ" {
				game.HomeTeamID, game.AwayTeamID = game.AwayTeamID, game.HomeTeamID
			}
			game.HomeScore.Int64, game.AwayScore.Int64 = int64(2-next), 0
			modified[game.ASAID] = next
			next++
		}
		for j := range season.Data.XGoals {
			xg := &season.Data.XGoals[j]
			index, ok := modified[xg.GameID]
			if !ok {
				continue
			}
			for _, game := range season.Data.Games {
				if game.ASAID == xg.GameID {
					xg.HomeTeamID, xg.AwayTeamID = game.HomeTeamID, game.AwayTeamID
				}
			}
			xg.HomeXG.Float64, xg.AwayXG.Float64 = 1.1, .7
			xg.HomeXPoints.Float64 = 1.54
			xg.Penalties = &cache.PenaltySplit{}
			if index == 1 {
				xg.HomeXG.Float64, xg.AwayXG.Float64 = .8, .9
				xg.Penalties = &cache.PenaltySplit{HomeXG: .3, HomeGoals: 1, HomeShots: 1}
			}
		}
	}
}

// CompleteScenarioInventories replaces small synthetic historical schedules
// with recorded participant pairings, retaining the scene's synthetic score and
// expected-data patterns. Explicitly incomplete and empty scenes remain so.
// Call this before introducing a deliberately missing-fixture test case.
func CompleteScenarioInventories(t testing.TB, archive []cache.HistoricalSeason) []cache.HistoricalSeason {
	t.Helper()
	recorded := thresholdsArchive(t)
	schedules := map[string]cache.SeasonData{}
	for _, season := range recorded {
		schedules[season.Entry.Season] = season.Data
	}
	for i := range archive {
		season := &archive[i]
		source, ok := schedules[season.Entry.Season]
		if !ok || len(season.Data.Games) == 0 || (season.Readiness != nil && season.Readiness.Completeness == cache.InventoryCompletenessIncomplete) {
			continue
		}
		// Recorded threshold scenes already contain the correct schedule and awards.
		appearances := map[string]int{}
		for _, game := range season.Data.Games {
			appearances[game.HomeTeamID]++
			appearances[game.AwayTeamID]++
		}
		if _, err := competition.ReconcileInventory(season.Entry.Season, season.Entry.Stage, season.Entry.Inventory, len(season.Data.Games), appearances); err == nil {
			continue
		}
		original := season.Data
		byGame := map[string]cache.GameXG{}
		for _, xg := range original.XGoals {
			byGame[xg.GameID] = xg
		}
		names := map[string]string{}
		for _, team := range original.Teams {
			names[team.ID] = team.Name
		}
		season.Data = cache.SeasonData{Teams: append([]standings.Team(nil), source.Teams...)}
		for j := range season.Data.Teams {
			if name := names[season.Data.Teams[j].ID]; name != "" {
				season.Data.Teams[j].Name = name
			}
		}
		year, err := time.Parse("2006", season.Entry.Season)
		if err != nil {
			t.Fatal(err)
		}
		for j, fixture := range source.Games {
			template := original.Games[j%len(original.Games)]
			oldID := template.ASAID
			template.ASAID = fmt.Sprintf("cache-%s-%d", season.Entry.Season, j)
			template.HomeTeamID, template.AwayTeamID = fixture.HomeTeamID, fixture.AwayTeamID
			template.KickoffUTC = year.AddDate(0, 2, j).Format("2006-01-02 15:04:05 UTC")
			season.Data.Games = append(season.Data.Games, template)
			if observation, found := byGame[oldID]; found {
				observation.GameID, observation.HomeTeamID, observation.AwayTeamID = template.ASAID, template.HomeTeamID, template.AwayTeamID
				season.Data.XGoals = append(season.Data.XGoals, observation)
			}
		}
	}
	return archive
}
