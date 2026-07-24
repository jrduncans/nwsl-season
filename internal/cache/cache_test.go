package cache

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

type refreshAfterFirstQuery struct {
	queryer
	once    sync.Once
	refresh func() error
	err     error
}

func (q *refreshAfterFirstQuery) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := q.queryer.QueryContext(ctx, query, args...)
	if err == nil {
		q.once.Do(func() { q.err = q.refresh() })
	}
	return rows, err
}

func TestOpenConfiguresEverySQLiteConnection(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	for i, conn := range []*sql.Conn{first, second} {
		if got := sqlitePragmaInt(t, ctx, conn, "foreign_keys"); got != 1 {
			t.Errorf("connection %d foreign_keys = %d, want 1", i+1, got)
		}
		if got := sqlitePragmaInt(t, ctx, conn, "busy_timeout"); got != 5000 {
			t.Errorf("connection %d busy_timeout = %d, want 5000", i+1, got)
		}
	}
}

func TestSQLiteForeignKeyCascadeOnNewPooledConnection(t *testing.T) {
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
	game := cachedGame("game-1", "2026", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{game}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceGameXG(ctx, "2026", "Regular Season", []Game{game}, []GameXG{{GameID: game.ASAID, Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 0, Valid: true}, RawJSON: "{}"}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Keep the initialization connection checked out so this delete uses a
	// newly opened pooled connection.
	initial, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer initial.Close()
	pooled, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pooled.Close()
	if _, err := pooled.ExecContext(ctx, `DELETE FROM games WHERE asa_game_id = ?`, game.ASAID); err != nil {
		t.Fatal(err)
	}

	var xgRows int
	if err := pooled.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_xg WHERE asa_game_id = ?`, game.ASAID).Scan(&xgRows); err != nil {
		t.Fatal(err)
	}
	if xgRows != 0 {
		t.Fatalf("game_xg rows after game deletion = %d, want 0", xgRows)
	}
}

func TestSQLiteBusyTimeoutWaitsForConcurrentWriter(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	tx, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_leases (lock_key, holder, expires_at_unix_nano) VALUES ('first', 'first', 1)`); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}

	started := make(chan struct{})
	writeResult := make(chan error, 1)
	go func() {
		close(started)
		_, err := second.ExecContext(ctx, `INSERT INTO sync_leases (lock_key, holder, expires_at_unix_nano) VALUES ('second', 'second', 1)`)
		writeResult <- err
	}()
	<-started

	// An unconfigured connection returns SQLITE_BUSY immediately. Releasing the
	// write lock while the second connection is waiting proves its timeout is in
	// effect without making this test wait for the full five seconds.
	time.Sleep(100 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-writeResult; err != nil {
		t.Fatalf("concurrent writer did not wait for lock: %v", err)
	}
}

func sqlitePragmaInt(t *testing.T, ctx context.Context, conn *sql.Conn, name string) int {
	t.Helper()
	var value int
	if err := conn.QueryRowContext(ctx, fmt.Sprintf("PRAGMA %s", name)).Scan(&value); err != nil {
		t.Fatalf("read %s pragma: %v", name, err)
	}
	return value
}

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

func TestReplaceGameXGRejectsOutOfRangeExpectedPoints(t *testing.T) {
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
	value := GameXG{GameID: "game-1", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 2.36, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.11, Valid: true}, HomeXPoints: sql.NullFloat64{Float64: 0, Valid: true}, AwayXPoints: sql.NullFloat64{Float64: MaxGameExpectedPoints, Valid: true}, RawJSON: `{}`}
	if _, err := db.ReplaceGameXG(ctx, "2026", "Regular Season", []Game{game}, []GameXG{value}, time.Now()); err != nil {
		t.Fatalf("ReplaceGameXG rejected boundary expected points: %v", err)
	}
	value.HomeXPoints.Float64 = MaxGameExpectedPoints + .01
	if _, err := db.ReplaceGameXG(ctx, "2026", "Regular Season", []Game{game}, []GameXG{value}, time.Now()); err == nil {
		t.Fatal("ReplaceGameXG succeeded with expected points above three")
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

func TestSeasonReadUsesOneSnapshotDuringRefresh(t *testing.T) {
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
	oldGame := cachedGame("game-1", "2026", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
	oldRun, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{oldGame}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	xgRun, err := db.ReplaceGameXG(ctx, "2026", "Regular Season", []Game{oldGame}, []GameXG{{
		GameID: "game-1", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo",
		HomeXG: sql.NullFloat64{Float64: 1.2, Valid: true}, AwayXG: sql.NullFloat64{Float64: 0.8, Valid: true}, RawJSON: "{}",
	}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	readTx, err := db.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(readTx)
	newGame := oldGame
	newGame.HomeScore = sql.NullInt64{Int64: 2, Valid: true}
	reader := &refreshAfterFirstQuery{
		queryer: readTx,
		refresh: func() error {
			_, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{newGame}, time.Now())
			return err
		},
	}
	season, err := loadSeasonData(ctx, reader, "2026", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if reader.err != nil {
		t.Fatalf("concurrent refresh: %v", reader.err)
	}
	if season.FixtureSnapshotID != oldRun.FixtureSnapshotID || season.LastSuccess == nil || season.LastSuccess.FixtureSnapshotID != oldRun.FixtureSnapshotID {
		t.Fatalf("fixture snapshots = season %q, run %+v; want %q", season.FixtureSnapshotID, season.LastSuccess, oldRun.FixtureSnapshotID)
	}
	if len(season.Games) != 1 || season.Games[0].HomeScore.Int64 != 1 {
		t.Fatalf("games = %+v, want pre-refresh fixture", season.Games)
	}
	if len(season.XGoals) != 1 || season.XGStatus.LastSuccess == nil || season.XGStatus.LastSuccess.ID != xgRun.ID {
		t.Fatalf("xG data = %+v, want pre-refresh xG snapshot", season)
	}
}

func TestSeasonRejectsMismatchedFixtureSnapshot(t *testing.T) {
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
	run, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{game}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE sync_runs SET fixture_snapshot_id = 'mismatch' WHERE id = ?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Season(ctx, "2026", "Regular Season"); err == nil {
		t.Fatal("Season succeeded with a mismatched fixture snapshot")
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
