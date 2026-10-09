// Package apptest builds deterministic cache scenarios for application tests,
// browser tests and the cmd/preview development tool. It is a normal package
// (not a _test file) so packages outside internal/app can import it; it is
// meant only for tests and local tooling, never for production code.
package apptest

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

// ArchiveState describes one synthetic historical Regular Season.
type ArchiveState struct {
	// Lifecycle and Inventory populate the season's readiness snapshot.
	// An empty Inventory means InventoryCompletenessUnknown.
	Lifecycle cache.SourceScopeLifecycle
	Inventory cache.InventoryCompleteness
	// Goals is the total goals in each of the season's 20 games.
	Goals int64
	// XGCovered is how many of the season's first games carry xG.
	XGCovered int
}

// Archive returns the in-memory historical seasons that the app's history and
// Explore pages read, one per entry in states. Seasons must exist in the
// competition catalog. The result is in map iteration order.
func Archive(t testing.TB, states map[string]ArchiveState) []cache.HistoricalSeason {
	t.Helper()
	archive := make([]cache.HistoricalSeason, 0, len(states))
	for season, state := range states {
		entry, ok := competition.Lookup(season, "Regular Season")
		if !ok {
			t.Fatalf("catalog lacks %s regular season", season)
		}
		inventory := state.Inventory
		if inventory == "" {
			inventory = cache.InventoryCompletenessUnknown
		}
		data := cache.SeasonData{Games: Games(season, 20, state.Goals)}
		for index, game := range data.Games {
			if index >= state.XGCovered {
				break
			}
			data.XGoals = append(data.XGoals, cache.GameXG{
				GameID: game.ASAID, Availability: cache.XGAvailable, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID,
				HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 0, Valid: true},
			})
		}
		archive = append(archive, cache.HistoricalSeason{Entry: entry, Readiness: &cache.SeasonReadinessSnapshot{
			Scope:     cache.SourceScope{Season: season, Stage: "Regular Season", Lifecycle: state.Lifecycle, Discovery: cache.SourceScopeAvailable},
			Readiness: cache.SourceReadinessAvailable, Completeness: inventory,
		}, Data: data})
	}
	return archive
}

// Games returns count completed games between the placeholder teams "alpha"
// and "bravo", each with totalGoals goals split as evenly as possible.
func Games(season string, count int, totalGoals int64) []cache.Game {
	games := make([]cache.Game, 0, count)
	for index := range count {
		home := totalGoals / 2
		away := totalGoals - home
		games = append(games, cache.Game{ASAID: fmt.Sprintf("history-%s-%d", season, index), Season: season, Stage: "Regular Season", Status: fixtures.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: sql.NullInt64{Int64: home, Valid: true}, AwayScore: sql.NullInt64{Int64: away, Valid: true}})
	}
	return games
}
