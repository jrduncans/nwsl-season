package cache

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
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

func TestMigrationFourteenAddsOnlyKnockoutSourceColumns(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 14 {
		t.Fatalf("version=%d,%v", version, err)
	}
	columns := map[string]struct{}{}
	rows, err := db.db.QueryContext(ctx, `PRAGMA table_info(games)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		columns[name] = struct{}{}
	}
	if _, ok := columns["expanded_minutes"]; !ok {
		t.Fatal("expanded_minutes missing")
	}
	if _, ok := columns["knockout_game"]; !ok {
		t.Fatal("knockout_game missing")
	}
	for _, column := range []string{"extra_time", "penalties", "home_penalties", "away_penalties"} {
		if _, ok := columns[column]; !ok {
			t.Fatalf("%s missing", column)
		}
	}
}

func TestMigrationThirteenBackfillsLegacySnapshotAndPreservesCurrentRead(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	teams := []Team{{ASAID: "alpha", Name: "Alpha"}, {ASAID: "bravo", Name: "Bravo"}}
	game := cachedGame("playoff", "2040", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
	run, err := db.ReplaceSeason(ctx, "2040", "Example", teams, []Game{game}, time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	before := run.FixtureSnapshotID
	if _, err := db.db.ExecContext(ctx, `UPDATE games SET raw_json='{"expanded_minutes":120,"knockout_game":true}' WHERE asa_game_id='playoff'`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`ALTER TABLE games DROP COLUMN extra_time`, `ALTER TABLE games DROP COLUMN penalties`, `ALTER TABLE games DROP COLUMN home_penalties`, `ALTER TABLE games DROP COLUMN away_penalties`, `DELETE FROM schema_migrations WHERE version=14`, `ALTER TABLE games DROP COLUMN expanded_minutes`, `ALTER TABLE games DROP COLUMN knockout_game`, `DELETE FROM schema_migrations WHERE version=13`} {
		if _, err := db.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	data, err := db.Season(ctx, "2040", "Example")
	if err != nil || len(data.Games) != 1 || !data.Games[0].ExpandedMinutes.Valid || data.Games[0].ExpandedMinutes.Int64 != 120 || !data.Games[0].KnockoutGame {
		t.Fatalf("season=%+v,%v", data, err)
	}
	current, err := db.LastSuccess(ctx, "2040", "Example")
	if err != nil || current == nil || current.FixtureSnapshotID != data.FixtureSnapshotID || current.FixtureSnapshotID == before {
		t.Fatalf("snapshot=%+v %q before=%q err=%v", current, data.FixtureSnapshotID, before, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	again, err := db.LastSuccess(ctx, "2040", "Example")
	if err != nil || again.FixtureSnapshotID != current.FixtureSnapshotID {
		t.Fatalf("reopen=%+v,%v", again, err)
	}
	rows, err := db.db.QueryContext(ctx, `PRAGMA table_info(games)`)
	if err != nil {
		t.Fatal(err)
	}
	foundDefault := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "knockout_game" && notNull == 1 && fmt.Sprint(defaultValue) == "0" {
			foundDefault = true
		}
	}
	if err := rows.Close(); err != nil || !foundDefault {
		t.Fatalf("knockout column default/check shape found=%t err=%v", foundDefault, err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE games SET knockout_game=2 WHERE asa_game_id='playoff'`); err == nil {
		t.Fatal("knockout check accepted 2")
	}
}

func TestMigrationFourteenBackfillsKnockoutSourceFactsAndRejectsAmbiguity(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	teams := []Team{{ASAID: "alpha", Name: "Alpha"}, {ASAID: "bravo", Name: "Bravo"}}
	games := []Game{}
	for i, id := range []string{"missing", "false", "extra", "direct", "full", "malformed"} {
		game := cachedGame(id, "2041", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
		game.KickoffUTC = time.Date(2041, 1, i+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		games = append(games, game)
	}
	run, err := db.ReplaceSeason(ctx, "2041", "Example", teams, games, time.Date(2041, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSourceScopes(ctx, "2041", "Example", time.Date(2041, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	inventory, err := db.ReplaceGameInventory(ctx, "2041", "Example", games, nil, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2041, 1, 2, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2041, 1, 2, 0, 1, 0, 0, time.UTC)})
	if err != nil || inventory.SyncRun == nil {
		t.Fatalf("inventory=%+v,%v", inventory, err)
	}
	run = *inventory.SyncRun
	xgValues := make([]GameXG, 0, len(games))
	for _, game := range games {
		xgValues = append(xgValues, GameXG{GameID: game.ASAID, Availability: XGAvailable, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID, HomeXG: sql.NullFloat64{Float64: 1.2, Valid: true}, AwayXG: sql.NullFloat64{Float64: 0.8, Valid: true}, RawJSON: `{}`})
	}
	if _, err := db.ReplaceStageXG(ctx, "2041", "Example", xgValues, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2041, 1, 2, 0, 2, 0, 0, time.UTC), FinishedAt: time.Date(2041, 1, 2, 0, 3, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	qualification, err := db.ReplaceQualification(ctx, QualificationRun{FixtureSnapshotID: run.FixtureSnapshotID, SourceSyncRunID: run.ID, Season: "2041", Stage: "Example", RulesVersion: "test-v1", ExpectedStatuses: 1, WrittenStatuses: 1}, []QualificationStatus{{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.Clinched, Method: clinching.ProofCheapBound, StrictlyAhead: clinching.CountEvidence{Kind: "upper_bound"}, AtLeastLevel: clinching.CountEvidence{Kind: "upper_bound"}, BlockingWitness: []clinching.WitnessGame{}, FrontierWitness: []clinching.WitnessGame{}, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpNotApplicable, FixtureIDs: []string{}}}})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2041, 1, 2, 0, 4, 0, 0, time.UTC)
	slate := scenarios.Slate{ID: "migration", DefinitionVersion: scenarios.DefinitionVersion, State: scenarios.SlateReady, Source: scenarios.SourceMatchday, Matchday: 1, StartsAtUTC: stamp, LatestKickoffUTC: stamp, CutoffUTC: stamp, FixtureIDs: []string{"missing"}}
	result := ScenarioResult{Result: scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCanClinch, CanClinch: true, Clauses: []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{{GameID: "missing", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin}}}, RepresentedAssignments: 1, ProofMethods: []clinching.ProofMethod{clinching.ProofCheapBound}}}, Necessary: []scenarios.FixtureCondition{}, ProofMethods: []clinching.ProofMethod{clinching.ProofCheapBound}, TotalAssignments: 3, CertifiedAssignments: 1}}
	if _, err := db.ReplaceScenario(ctx, ScenarioRun{FixtureSnapshotID: run.FixtureSnapshotID, QualificationRunID: qualification.Run.ID, SourceSyncRunID: run.ID, Season: "2041", Stage: "Example", RulesVersion: "test-v1", DefinitionVersion: scenarios.DefinitionVersion, Slate: slate, ExpectedResults: 1, WrittenResults: 1}, []ScenarioResult{result}); err != nil {
		t.Fatal(err)
	}
	beforeXG, err := db.GameXGStates(ctx, "2041", "Example")
	if err != nil {
		t.Fatal(err)
	}
	beforeGameAudits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2041", "Example")
	if err != nil {
		t.Fatal(err)
	}
	beforeGameState, foundState, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2041", "Example")
	if err != nil || !foundState {
		t.Fatalf("game state=%+v,%t,%v", beforeGameState, foundState, err)
	}
	beforeReadiness, foundReadiness, err := db.SeasonReadiness(ctx, "2041", "Example")
	if err != nil || !foundReadiness {
		t.Fatalf("readiness=%+v,%t,%v", beforeReadiness, foundReadiness, err)
	}
	beforeVenue, err := db.VenueSummaries(ctx, []string{"2041"}, "Example")
	if err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := run.FixtureSnapshotID
	raw := map[string]string{
		"missing":   `{"home_penalties":0,"away_penalties":0}`,
		"false":     `{"extra_time":false,"penalties":false,"home_penalties":0,"away_penalties":0}`,
		"extra":     `{"extra_time":true}`,
		"direct":    `{"penalties":true,"home_penalties":4,"away_penalties":3}`,
		"full":      `{"extra_time":true,"penalties":true,"home_penalties":5,"away_penalties":4}`,
		"malformed": `{`,
	}
	for id, value := range raw {
		if _, err := db.db.ExecContext(ctx, `UPDATE games SET raw_json=? WHERE asa_game_id=?`, value, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{`ALTER TABLE games DROP COLUMN extra_time`, `ALTER TABLE games DROP COLUMN penalties`, `ALTER TABLE games DROP COLUMN home_penalties`, `ALTER TABLE games DROP COLUMN away_penalties`, `DELETE FROM schema_migrations WHERE version=14`} {
		if _, err := db.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	loaded, err := db.seasonGames(ctx, "2041", "Example")
	if err != nil || len(loaded) != len(games) {
		t.Fatalf("loaded=%+v,%v", loaded, err)
	}
	byID := map[string]Game{}
	for _, game := range loaded {
		byID[game.ASAID] = game
	}
	if byID["missing"].ExtraTime.Valid || byID["missing"].Penalties.Valid || byID["missing"].HomePenalties.Valid || byID["missing"].AwayPenalties.Valid || byID["malformed"].ExtraTime.Valid || byID["malformed"].Penalties.Valid {
		t.Fatalf("missing/malformed fields = %+v / %+v", byID["missing"], byID["malformed"])
	}
	if value := byID["false"]; !value.ExtraTime.Valid || value.ExtraTime.Bool || !value.Penalties.Valid || value.Penalties.Bool || value.HomePenalties.Valid || value.AwayPenalties.Valid {
		t.Fatalf("false fields = %+v", value)
	}
	if value := byID["extra"]; !value.ExtraTime.Valid || !value.ExtraTime.Bool || value.Penalties.Valid {
		t.Fatalf("extra fields = %+v", value)
	}
	if value := byID["direct"]; value.ExtraTime.Valid || !value.Penalties.Valid || !value.Penalties.Bool || !value.HomePenalties.Valid || value.HomePenalties.Int64 != 4 || !value.AwayPenalties.Valid || value.AwayPenalties.Int64 != 3 {
		t.Fatalf("direct fields = %+v", value)
	}
	if value := byID["full"]; !value.ExtraTime.Valid || !value.ExtraTime.Bool || !value.Penalties.Valid || !value.Penalties.Bool || !value.HomePenalties.Valid || value.HomePenalties.Int64 != 5 || !value.AwayPenalties.Valid || value.AwayPenalties.Int64 != 4 {
		t.Fatalf("full fields = %+v", value)
	}
	for id, want := range raw {
		var got string
		if err := db.db.QueryRowContext(ctx, `SELECT raw_json FROM games WHERE asa_game_id=?`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("raw_json for %s = %q, %v; want %q", id, got, err, want)
		}
	}
	current, err := db.LastSuccess(ctx, "2041", "Example")
	if err != nil || current == nil || current.FixtureSnapshotID == beforeSnapshot {
		t.Fatalf("current=%+v before=%q err=%v", current, beforeSnapshot, err)
	}
	afterXG, xgErr := db.GameXGStates(ctx, "2041", "Example")
	afterGameAudits, auditsErr := db.SourceRefreshAudits(ctx, SourceResourceGames, "2041", "Example")
	afterGameState, stateFound, stateErr := db.SourceResourceScopeState(ctx, SourceResourceGames, "2041", "Example")
	afterReadiness, readinessFound, readinessErr := db.SeasonReadiness(ctx, "2041", "Example")
	afterVenue, venueErr := db.VenueSummaries(ctx, []string{"2041"}, "Example")
	if xgErr != nil || auditsErr != nil || stateErr != nil || readinessErr != nil || venueErr != nil || !stateFound || !readinessFound || !reflect.DeepEqual(beforeXG, afterXG) || !reflect.DeepEqual(beforeGameAudits, afterGameAudits) || !reflect.DeepEqual(beforeGameState, afterGameState) || !reflect.DeepEqual(beforeReadiness, afterReadiness) || !reflect.DeepEqual(beforeVenue, afterVenue) {
		t.Fatalf("migration preservation xg=%v audits=%v state=%v readiness=%v venue=%v", xgErr, auditsErr, stateErr, readinessErr, venueErr)
	}
	if _, found, err := db.QualificationForSnapshot(ctx, beforeSnapshot, "test-v1"); err != nil || !found {
		t.Fatalf("qualification preservation found=%t err=%v", found, err)
	}
	if _, found, err := db.ScenarioForSnapshot(ctx, beforeSnapshot, "test-v1", scenarios.DefinitionVersion); err != nil || !found {
		t.Fatalf("scenario preservation found=%t err=%v", found, err)
	}
	if err := db.db.QueryRowContext(ctx, `PRAGMA foreign_key_check`).Scan(new(string)); err != sql.ErrNoRows {
		t.Fatalf("foreign key check after migration: %v", err)
	}

	for _, raw := range []string{
		`{"penalties":true}`,
		`{"penalties":true,"home_penalties":1}`,
		`{"penalties":true,"away_penalties":1}`,
		`{"penalties":false,"home_penalties":1,"away_penalties":0}`,
		`{"home_penalties":1,"away_penalties":0}`,
		`{"penalties":true,"home_penalties":-1,"away_penalties":0}`,
		`{"penalties":true,"home_penalties":0,"away_penalties":-1}`,
	} {
		invalidPath := t.TempDir() + "/cache.sqlite"
		invalidDB, err := Open(ctx, invalidPath)
		if err != nil {
			t.Fatal(err)
		}
		game := cachedGame("invalid", "2042", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
		if _, err := invalidDB.ReplaceSeason(ctx, "2042", "Example", teams, []Game{game}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := invalidDB.db.ExecContext(ctx, `UPDATE games SET raw_json=? WHERE asa_game_id='invalid'`, raw); err != nil {
			t.Fatal(err)
		}
		for _, statement := range []string{`ALTER TABLE games DROP COLUMN extra_time`, `ALTER TABLE games DROP COLUMN penalties`, `ALTER TABLE games DROP COLUMN home_penalties`, `ALTER TABLE games DROP COLUMN away_penalties`, `DELETE FROM schema_migrations WHERE version=14`} {
			if _, err := invalidDB.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := invalidDB.Close(); err != nil {
			t.Fatal(err)
		}
		if reopened, err := Open(ctx, invalidPath); err == nil {
			_ = reopened.Close()
			t.Fatalf("invalid raw %s migrated", raw)
		}
		rawDB, err := sql.Open("sqlite", sqliteDSN(invalidPath))
		if err != nil {
			t.Fatal(err)
		}
		var version int
		if err := rawDB.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 13 {
			_ = rawDB.Close()
			t.Fatalf("invalid raw %s version=%d err=%v, want v13", raw, version, err)
		}
		for _, column := range []string{"extra_time", "penalties", "home_penalties", "away_penalties"} {
			var present int
			if err := rawDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('games') WHERE name=?`, column).Scan(&present); err != nil || present != 0 {
				_ = rawDB.Close()
				t.Fatalf("invalid raw %s left column %s: present=%d err=%v", raw, column, present, err)
			}
		}
		var storedRaw string
		if err := rawDB.QueryRowContext(ctx, `SELECT raw_json FROM games WHERE asa_game_id='invalid'`).Scan(&storedRaw); err != nil || storedRaw != raw {
			_ = rawDB.Close()
			t.Fatalf("invalid raw row changed: got=%q err=%v want=%q", storedRaw, err, raw)
		}
		if _, err := rawDB.ExecContext(ctx, `UPDATE games SET raw_json='{"penalties":true,"home_penalties":1,"away_penalties":0}' WHERE asa_game_id='invalid'`); err != nil {
			_ = rawDB.Close()
			t.Fatal(err)
		}
		if err := rawDB.Close(); err != nil {
			t.Fatal(err)
		}
		corrected, err := Open(ctx, invalidPath)
		if err != nil {
			t.Fatalf("corrected retry for %s failed: %v", raw, err)
		}
		if err := corrected.db.QueryRowContext(ctx, `PRAGMA foreign_key_check`).Scan(new(string)); err != sql.ErrNoRows {
			_ = corrected.Close()
			t.Fatalf("foreign key check after corrected retry: %v", err)
		}
		if err := corrected.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationFourteenIsIdempotentWithExistingColumns(t *testing.T) {
	ctx := context.Background()
	for _, retained := range [][]string{
		{"extra_time", "penalties"},
		{"extra_time", "penalties", "home_penalties", "away_penalties"},
	} {
		t.Run(fmt.Sprintf("retained_%d", len(retained)), func(t *testing.T) {
			path := t.TempDir() + "/cache.sqlite"
			db, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			teams := []Team{{ASAID: "alpha", Name: "Alpha"}, {ASAID: "bravo", Name: "Bravo"}}
			game := cachedGame("one", "2044", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
			if _, err := db.ReplaceSeason(ctx, "2044", "Example", teams, []Game{game}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.ExecContext(ctx, `UPDATE games SET raw_json='{"extra_time":false,"penalties":true,"home_penalties":2,"away_penalties":1}' WHERE asa_game_id='one'`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=14`); err != nil {
				t.Fatal(err)
			}
			for _, column := range []string{"away_penalties", "home_penalties", "penalties", "extra_time"} {
				keep := false
				for _, value := range retained {
					keep = keep || value == column
				}
				if !keep {
					if _, err := db.db.ExecContext(ctx, `ALTER TABLE games DROP COLUMN `+column); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			loaded, err := db.seasonGames(ctx, "2044", "Example")
			if err != nil || len(loaded) != 1 || !loaded[0].ExtraTime.Valid || loaded[0].ExtraTime.Bool || !loaded[0].Penalties.Valid || !loaded[0].Penalties.Bool || loaded[0].HomePenalties.Int64 != 2 || loaded[0].AwayPenalties.Int64 != 1 {
				t.Fatalf("idempotent migration=%+v,%v", loaded, err)
			}
		})
	}
}

func TestReplaceSeasonRejectsInvalidKnockoutFactsWithoutMutation(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "Alpha"}, {ASAID: "bravo", Name: "Bravo"}}
	base := cachedGame("one", "2043", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2043", "Example", teams, []Game{base}, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := db.Season(ctx, "2043", "Example")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Game{
		func() Game {
			value := base
			value.ASAID = "two"
			value.KickoffUTC = "2043-01-02T00:00:00Z"
			value.Penalties = sql.NullBool{Bool: true, Valid: true}
			return value
		}(),
		func() Game {
			value := base
			value.ASAID = "two"
			value.KickoffUTC = "2043-01-02T00:00:00Z"
			value.HomePenalties = sql.NullInt64{Int64: 1, Valid: true}
			value.AwayPenalties = sql.NullInt64{Int64: 0, Valid: true}
			return value
		}(),
		func() Game {
			value := base
			value.ASAID = "two"
			value.KickoffUTC = "2043-01-02T00:00:00Z"
			value.Penalties = sql.NullBool{Bool: false, Valid: true}
			value.HomePenalties = sql.NullInt64{Int64: 1, Valid: true}
			value.AwayPenalties = sql.NullInt64{Int64: 0, Valid: true}
			return value
		}(),
		func() Game {
			value := base
			value.ASAID = "two"
			value.KickoffUTC = "2043-01-02T00:00:00Z"
			value.Penalties = sql.NullBool{Bool: true, Valid: true}
			value.HomePenalties = sql.NullInt64{Int64: 1, Valid: true}
			return value
		}(),
		func() Game {
			value := base
			value.ASAID = "two"
			value.KickoffUTC = "2043-01-02T00:00:00Z"
			value.Penalties = sql.NullBool{Bool: true, Valid: true}
			value.HomePenalties = sql.NullInt64{Int64: -1, Valid: true}
			value.AwayPenalties = sql.NullInt64{Int64: 0, Valid: true}
			return value
		}(),
	} {
		changed := base
		changed.HomeScore = sql.NullInt64{Int64: 2, Valid: true}
		changed.LastUpdatedUTC = "2043-01-01T13:00:00Z"
		if _, err := db.ReplaceSeason(ctx, "2043", "Example", teams, []Game{changed, invalid}, time.Now()); err == nil {
			t.Fatalf("invalid knockout facts accepted: %+v", invalid)
		}
		after, err := db.Season(ctx, "2043", "Example")
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("invalid write changed cache: before=%+v after=%+v err=%v", before, after, err)
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

func TestVenueSummaryIsPersistedAcrossFixtureAndXGWrites(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "Alpha", RawJSON: "{}"}, {ASAID: "bravo", Name: "Bravo", RawJSON: "{}"}}
	game := cachedGame("game-1", "2025", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 2, Valid: true}, sql.NullInt64{Int64: 1, Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2025", "Regular Season", teams, []Game{game}, time.Now()); err != nil {
		t.Fatal(err)
	}
	summaries, err := db.VenueSummaries(ctx, []string{"2025"}, "Regular Season")
	if err != nil || len(summaries) != 1 {
		t.Fatalf("fixture summaries = %+v, %v", summaries, err)
	}
	if got := summaries[0]; !got.FixtureReady || got.XGReady || got.Matches != 1 || got.HomeGoals != 2 || got.AwayGoals != 1 || got.HomePoints != 3 || got.AwayPoints != 0 {
		t.Fatalf("fixture summary = %+v", got)
	}
	value := GameXG{GameID: game.ASAID, Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1.7, Valid: true}, AwayXG: sql.NullFloat64{Float64: .8, Valid: true}, RawJSON: "{}"}
	if _, err := db.ReplaceGameXG(ctx, "2025", "Regular Season", []Game{game}, []GameXG{value}, time.Now()); err != nil {
		t.Fatal(err)
	}
	summaries, err = db.VenueSummaries(ctx, []string{"2025"}, "Regular Season")
	if err != nil || len(summaries) != 1 {
		t.Fatalf("xG summaries = %+v, %v", summaries, err)
	}
	if got := summaries[0]; !got.XGReady || got.XGMatches != 1 || got.HomeXG != 1.7 || got.AwayXG != .8 {
		t.Fatalf("xG summary = %+v", got)
	}
	season, err := db.Season(ctx, "2026", "Regular Season")
	if err != nil || len(season.VenueHistory) != 1 || season.VenueHistory[0].Season != "2025" {
		t.Fatalf("season venue history = %+v, %v", season.VenueHistory, err)
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
