package apptest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// The configured season and stage match the NWSL_SYNC_SEASON and
// NWSL_SYNC_STAGE defaults that cmd/preview and server.Build run with, so the
// source-scope registry Seed creates equals the one the server would create.
const (
	configuredSeason = "2026"
	configuredStage  = "Regular Season"
)

// Seed writes a named scenario (see ScenarioNames) into db through the
// cache's public write methods, so the pages read it exactly as they read a
// synchronized cache. It makes no ASA request.
//
// Source-scope lifecycle and inventory completeness are derived by the cache
// from the calendar year and the catalog, not copied from the scenario, so a
// seeded cache can differ from the in-memory Scenario archive: a past season
// is always completed, a season with games is always available, and a season is
// "complete" only when it matches the catalog's expected inventory. xG rows
// that the cache would reject (one side missing, or only one side's expected
// points) are seeded as unavailable xG or without expected points.
//
// Lifecycles follow the wall clock (time.Now), so they move forward with the
// calendar: the "active" 2026 season becomes completed from 2027-01-01 and a
// later year becomes active. Browser tests must not depend on a season being
// active beyond the year this was written for.
func Seed(t testing.TB, db *cache.DB, scenario string) {
	t.Helper()
	ctx := context.Background()
	archive, _ := Scenario(t, scenario)
	now := time.Now().UTC()
	// The scheduler and server seed the same registry; doing it here lets a
	// bare database serve the history pages.
	if _, err := db.EnsureSourceScopes(ctx, configuredSeason, configuredStage, now); err != nil {
		t.Fatalf("seed source scopes: %v", err)
	}
	for _, season := range archive {
		games := season.Data.Games
		if len(games) == 0 {
			continue
		}
		for i := range games {
			if games[i].RawJSON == "" {
				games[i].RawJSON = "{}"
			}
		}
		stage := season.Entry.Stage
		if _, err := db.ReplaceSeason(ctx, season.Entry.Season, stage, cacheTeams(season.Data.Teams), games, now); err != nil {
			t.Fatalf("seed %s fixtures: %v", season.Entry.Season, err)
		}
		if _, err := db.ReplaceGameXG(ctx, season.Entry.Season, stage, games, storableXG(season.Data.XGoals), now); err != nil {
			t.Fatalf("seed %s xG: %v", season.Entry.Season, err)
		}
	}
}

// cacheTeams converts scenario teams to cache rows, falling back to the
// placeholder teams that Games uses.
func cacheTeams(teams []standings.Team) []cache.Team {
	if len(teams) == 0 {
		return []cache.Team{
			{ASAID: "alpha", Name: "Alpha", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"},
			{ASAID: "bravo", Name: "Bravo", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"},
		}
	}
	rows := make([]cache.Team, 0, len(teams))
	for _, team := range teams {
		rows = append(rows, cache.Team{ASAID: team.ID, Name: team.Name, ShortName: team.Name, Abbreviation: team.ID[:3], RawJSON: "{}"})
	}
	return rows
}

// storableXG drops what ReplaceGameXG rejects: rows missing either xG value
// are omitted (the cache marks them unavailable), and expected points are kept
// only when both sides have them.
func storableXG(values []cache.GameXG) []cache.GameXG {
	stored := make([]cache.GameXG, 0, len(values))
	for _, value := range values {
		if !value.HomeXG.Valid || !value.AwayXG.Valid {
			continue
		}
		if !value.HomeXPoints.Valid || !value.AwayXPoints.Valid {
			value.HomeXPoints, value.AwayXPoints = sql.NullFloat64{}, sql.NullFloat64{}
		}
		stored = append(stored, value)
	}
	return stored
}
