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

	status, err := db.Status(ctx, "2026", "Regular Season")
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

func TestGameXGExpectedPointsPersist(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	teams := []Team{{ASAID: "alpha", Name: "Alpha FC", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"}, {ASAID: "bravo", Name: "Bravo FC", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"}}
	game := cachedGame("game-1", "2026", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 2, Valid: true}, sql.NullInt64{Int64: 1, Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{game}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceGameXG(ctx, "2026", "Regular Season", []Game{game}, []GameXG{{GameID: "game-1", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 2.36, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.11, Valid: true}, HomeXPoints: sql.NullFloat64{Float64: 2.47, Valid: true}, AwayXPoints: sql.NullFloat64{Float64: .367, Valid: true}, RawJSON: `{}`}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	season, err := db.Season(ctx, "2026", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if len(season.XGoals) != 1 || !season.XGoals[0].HomeXPoints.Valid || !season.XGoals[0].AwayXPoints.Valid {
		t.Fatalf("xG values = %+v, want expected points", season.XGoals)
	}
	if season.XGoals[0].HomeXPoints.Float64 != 2.47 || season.XGoals[0].AwayXPoints.Float64 != .367 {
		t.Fatalf("expected points = %+v, want 2.470 / 0.367", season.XGoals[0])
	}
}

func TestMigrationSevenAddsExpectedPointsColumns(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (6, '2026-07-22T00:00:00Z')`,
		`CREATE TABLE game_xg (asa_game_id TEXT PRIMARY KEY, availability TEXT NOT NULL, home_team_id TEXT NOT NULL, away_team_id TEXT NOT NULL, home_xg REAL, away_xg REAL, raw_json TEXT NOT NULL, first_observed_at TEXT, last_checked_at TEXT NOT NULL)`,
	} {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			_ = legacy.Close()
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	columns := map[string]bool{}
	rows, err := db.db.QueryContext(ctx, `PRAGMA table_info(game_xg)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home_xpoints", "away_xpoints"} {
		if !columns[name] {
			t.Errorf("game_xg columns = %v, missing %q", columns, name)
		}
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

	table := standings.Calculate(loadedTeams, loadedGames, standings.PerGameRules())
	assertStandingsRecord(t, table, "alpha", standings.Record{
		Played: 1, Wins: 1, GoalsFor: 2, Points: 3,
	})
	assertStandingsRecord(t, table, "bravo", standings.Record{
		Played: 1, Losses: 1, GoalsAgainst: 2,
	})
	assertStandingsRecord(t, table, "charlie", standings.Record{})
}

func TestSeasonLoadsFixturesAndFreshness(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	teams := []Team{
		{ASAID: "alpha", Name: "Alpha FC", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"},
		{ASAID: "bravo", Name: "Bravo FC", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"},
	}
	game := cachedGame("game-1", "2026", "Regular Season", "PreMatch", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	game.KickoffUTC = "2026-07-11 23:30:00 UTC"
	game.Matchday = sql.NullInt64{Int64: 14, Valid: true}
	started := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	if _, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{game}, started); err != nil {
		t.Fatal(err)
	}

	season, err := db.Season(ctx, "2026", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if len(season.Teams) != 2 || len(season.Games) != 1 {
		t.Fatalf("season = %+v, want two teams and one game", season)
	}
	if season.Games[0].KickoffUTC != game.KickoffUTC || season.Games[0].Matchday.Int64 != 14 {
		t.Fatalf("game = %+v, want presentation metadata", season.Games[0])
	}
	if season.LastSuccess == nil || season.LastSuccess.Season != "2026" {
		t.Fatalf("last success = %+v, want 2026 sync", season.LastSuccess)
	}
}

func TestReplaceSeasonTracksRowChanges(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	teams := []Team{
		{ASAID: "alpha", Name: "Alpha FC", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"},
		{ASAID: "bravo", Name: "Bravo FC", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"},
	}
	game := cachedGame("game-1", "2026", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})

	first, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{game}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.TeamsInserted != 2 || first.GamesInserted != 1 || first.TeamsUpdated != 0 || first.GamesUpdated != 0 {
		t.Fatalf("first run = %+v, want inserts only", first)
	}

	second, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{game}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if second.TeamsUnchanged != 2 || second.GamesUnchanged != 1 {
		t.Fatalf("second run = %+v, want unchanged rows", second)
	}

	game.HomeScore = sql.NullInt64{Int64: 2, Valid: true}
	third, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{game}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if third.TeamsUnchanged != 2 || third.GamesUpdated != 1 || third.GamesInserted != 0 {
		t.Fatalf("third run = %+v, want one updated game", third)
	}
}

func TestSyncLeaseExcludesConcurrentHolder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	expires := time.Now().Add(time.Minute)
	acquired, err := db.TryAcquireSyncLease(ctx, "2026\x00Regular Season", "first", expires)
	if err != nil || !acquired {
		t.Fatalf("first lease = %t, %v; want acquired", acquired, err)
	}
	acquired, err = db.TryAcquireSyncLease(ctx, "2026\x00Regular Season", "second", expires)
	if err != nil || acquired {
		t.Fatalf("second lease = %t, %v; want not acquired", acquired, err)
	}
	if err := db.ReleaseSyncLease(ctx, "2026\x00Regular Season", "first"); err != nil {
		t.Fatal(err)
	}
	acquired, err = db.TryAcquireSyncLease(ctx, "2026\x00Regular Season", "second", expires)
	if err != nil || !acquired {
		t.Fatalf("lease after release = %t, %v; want acquired", acquired, err)
	}
}

func TestMigrateVersionOneDatabase(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (1, '2026-01-01T00:00:00Z')`,
		`CREATE TABLE sync_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT, started_at TEXT NOT NULL, finished_at TEXT NOT NULL,
			season TEXT NOT NULL, stage TEXT NOT NULL, outcome TEXT NOT NULL, error_summary TEXT NOT NULL,
			teams_upserted INTEGER NOT NULL, games_upserted INTEGER NOT NULL, games_deleted INTEGER NOT NULL, games_seen INTEGER NOT NULL
		)`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RecordFailure(ctx, "2026", "Regular Season", time.Now(), context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	status, err := db.Status(ctx, "2026", "Regular Season")
	if err != nil || status.LastAttempt == nil {
		t.Fatalf("status = %+v, %v; want migrated audit row", status, err)
	}
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
