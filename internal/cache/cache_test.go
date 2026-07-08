package cache

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestMigrationsCreateFreshDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastAttempt != nil {
		t.Fatalf("last attempt = %+v, want nil", status.LastAttempt)
	}
	if status.LastSuccess != nil {
		t.Fatalf("last success = %+v, want nil", status.LastSuccess)
	}
}

func TestStandingsInputsLoadSeasonStageValues(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	teams := []Team{
		{ASAID: "alpha", Name: "Alpha FC", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"},
		{ASAID: "bravo", Name: "Bravo FC", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"},
		{ASAID: "charlie", Name: "Charlie FC", ShortName: "Charlie", Abbreviation: "CHR", RawJSON: "{}"},
	}
	regularGames := []Game{
		cachedGame("regular-1", "2024", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 2, Valid: true}, sql.NullInt64{Int64: 0, Valid: true}),
		cachedGame("regular-2", "2024", "Regular Season", "PreMatch", "bravo", "charlie", sql.NullInt64{}, sql.NullInt64{}),
	}
	playoffGames := []Game{
		cachedGame("playoff-1", "2024", "Playoffs", "FullTime", "charlie", "alpha", sql.NullInt64{Int64: 3, Valid: true}, sql.NullInt64{Int64: 0, Valid: true}),
	}

	if _, err := db.ReplaceSeason(ctx, "2024", "Regular Season", teams, regularGames, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceSeason(ctx, "2024", "Playoffs", teams, playoffGames, time.Now()); err != nil {
		t.Fatal(err)
	}

	loadedTeams, loadedGames, err := db.StandingsInputs(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}

	if len(loadedTeams) != 3 {
		t.Fatalf("teams loaded = %d, want 3", len(loadedTeams))
	}
	if len(loadedGames) != 2 {
		t.Fatalf("games loaded = %d, want 2", len(loadedGames))
	}
	for _, game := range loadedGames {
		if game.ID == "playoff-1" {
			t.Fatal("loaded playoff game for regular-season standings")
		}
	}

	table := standings.Calculate(loadedTeams, loadedGames, standings.DefaultRules())
	assertStandingsRecord(t, table, "alpha", standings.Record{
		Played: 1, Wins: 1, GoalsFor: 2, Points: 3,
	})
	assertStandingsRecord(t, table, "bravo", standings.Record{
		Played: 1, Losses: 1, GoalsAgainst: 2,
	})
	assertStandingsRecord(t, table, "charlie", standings.Record{})
}

func cachedGame(id, season, stage, status, homeID, awayID string, homeScore, awayScore sql.NullInt64) Game {
	return Game{
		ASAID:          id,
		Season:         season,
		Stage:          stage,
		KickoffUTC:     "2024-11-03 22:30:00 UTC",
		Status:         status,
		HomeTeamID:     homeID,
		AwayTeamID:     awayID,
		HomeScore:      homeScore,
		AwayScore:      awayScore,
		LastUpdatedUTC: "2024-11-07 15:57:43 UTC",
		RawJSON:        "{}",
	}
}

func assertStandingsRecord(t *testing.T, table []standings.TableRow, teamID string, want standings.Record) {
	t.Helper()
	for _, row := range table {
		if row.Team.ID == teamID {
			if row.Record != want {
				t.Fatalf("%s record = %+v, want %+v", teamID, row.Record, want)
			}
			return
		}
	}
	t.Fatalf("team %q not found in table %+v", teamID, table)
}
