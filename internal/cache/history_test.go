package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestHistoricalRegularSeasonsEmptyCatalogRows(t *testing.T) {
	db := openHistoryTestDB(t)
	seasons, err := db.HistoricalRegularSeasons(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2016", "2017", "2018", "2019", "2021", "2022", "2023", "2024", "2025", "2026"}
	if got := historySeasons(seasons); !reflect.DeepEqual(got, want) {
		t.Fatalf("seasons = %v, want %v", got, want)
	}
	for _, season := range seasons {
		if season.Entry.Stage != "Regular Season" || season.Readiness != nil || len(season.Data.Games) != 0 {
			t.Fatalf("empty historical season = %+v", season)
		}
	}
}

func TestHistoricalRegularSeasonsReturnsOnlyExactRegularSeasonData(t *testing.T) {
	ctx := context.Background()
	db := openHistoryTestDB(t)
	putHistorySeason(t, db, "2024", "Regular Season", "regular")
	putHistorySeason(t, db, "2024", "Playoffs", "playoffs")
	putHistorySeason(t, db, "2020", "NWSL Challenge Cup Group Stage", "cup")

	seasons, err := db.HistoricalRegularSeasons(ctx)
	if err != nil {
		t.Fatal(err)
	}
	regular := historySeason(t, seasons, "2024")
	if len(regular.Data.Games) != 1 || regular.Data.Games[0].ASAID != "regular" {
		t.Fatalf("2024 regular season = %+v", regular)
	}
	for _, season := range seasons {
		if season.Entry.Season == "2020" {
			t.Fatal("2020 challenge cup was returned as a regular season")
		}
	}
}

func TestHistoricalRegularSeasonsPreservesReadinessAndNullableXG(t *testing.T) {
	ctx := context.Background()
	db := openHistoryTestDB(t)
	putHistoryScope(t, db, "2016", SourceScopeCompleted, SourceScopeAvailable)
	putHistoryScope(t, db, "2017", SourceScopeActive, SourceScopeUnknown)
	putHistoryScope(t, db, "2018", SourceScopeUpcoming, SourceScopeNotPublished)
	putHistorySeason(t, db, "2019", "Regular Season", "xg")
	game, err := db.Season(ctx, "2019", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceGameXG(ctx, "2019", "Regular Season", game.Games, []GameXG{{
		GameID: "xg", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo",
		HomeXG: sql.NullFloat64{Float64: 1.2, Valid: true}, AwayXG: sql.NullFloat64{Float64: 0.8, Valid: true},
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	seasons, err := db.HistoricalRegularSeasons(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		season       string
		lifecycle    SourceScopeLifecycle
		discovery    SourceScopeDiscovery
		readiness    SourceReadiness
		completeness InventoryCompleteness
	}{
		{"2016", SourceScopeCompleted, SourceScopeAvailable, SourceReadinessAvailable, InventoryCompletenessUnknown},
		{"2017", SourceScopeActive, SourceScopeUnknown, SourceReadinessUnknown, InventoryCompletenessUnknown},
		{"2018", SourceScopeUpcoming, SourceScopeNotPublished, SourceReadinessNotPublished, InventoryCompletenessUnknown},
	} {
		got := historySeason(t, seasons, want.season).Readiness
		if got == nil || got.Scope.Season != want.season || got.Scope.Stage != "Regular Season" ||
			got.Scope.Registration != SourceScopeCatalog || got.Scope.Lifecycle != want.lifecycle ||
			got.Scope.Discovery != want.discovery || got.Readiness != want.readiness ||
			got.Completeness != want.completeness {
			t.Errorf("%s readiness = %+v, want catalog/regular-season/%q/%q/%q/%q", want.season, got, want.lifecycle, want.discovery, want.readiness, want.completeness)
		}
	}
	xg := historySeason(t, seasons, "2019").Data.XGoals
	if len(xg) != 1 || !xg[0].HomeXG.Valid || !xg[0].AwayXG.Valid || xg[0].HomeXPoints.Valid || xg[0].AwayXPoints.Valid {
		t.Fatalf("nullable xG = %+v", xg)
	}
}

func TestHistoricalRegularSeasonsReflectsCorrectionsAndDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	db := openHistoryTestDB(t)
	putHistorySeason(t, db, "2024", "Regular Season", "regular")
	before := historyTableContents(t, db)
	first, err := db.HistoricalRegularSeasons(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterRead := historyTableContents(t, db); !reflect.DeepEqual(before, afterRead) {
		t.Fatalf("historical read changed cache: %v -> %v", before, afterRead)
	}
	changed := cachedGame("regular", "2024", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 2, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2024", "Regular Season", historyTeams(), []Game{changed}, time.Now()); err != nil {
		t.Fatal(err)
	}
	second, err := db.HistoricalRegularSeasons(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if historySeason(t, first, "2024").Data.Games[0].HomeScore.Int64 != 1 || historySeason(t, second, "2024").Data.Games[0].HomeScore.Int64 != 2 {
		t.Fatalf("correction was not reflected: first=%+v second=%+v", first, second)
	}
	afterCorrection := historyTableContents(t, db)
	if len(afterCorrection["sync_runs"]) != len(before["sync_runs"])+1 {
		t.Fatalf("expected correction sync run: %v -> %v", before, afterCorrection)
	}
}

func TestHistoricalRegularSeasonsErrorsAreNotPartial(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		db := openHistoryTestDB(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := db.HistoricalRegularSeasons(ctx)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled read = %#v, %v", got, err)
		}
	})
	t.Run("closed", func(t *testing.T) {
		db := openHistoryTestDB(t)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		got, err := db.HistoricalRegularSeasons(context.Background())
		if got != nil || err == nil {
			t.Fatalf("closed read = %#v, %v", got, err)
		}
	})
	t.Run("corrupt snapshot", func(t *testing.T) {
		ctx := context.Background()
		db := openHistoryTestDB(t)
		putHistorySeason(t, db, "2024", "Regular Season", "regular")
		if _, err := db.db.ExecContext(ctx, `UPDATE sync_runs SET fixture_snapshot_id = 'wrong' WHERE season = '2024' AND stage = 'Regular Season'`); err != nil {
			t.Fatal(err)
		}
		got, err := db.HistoricalRegularSeasons(ctx)
		if got != nil || err == nil {
			t.Fatalf("corrupt read = %#v, %v", got, err)
		}
	})
}

func openHistoryTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func historyTeams() []Team {
	return []Team{{ASAID: "alpha", Name: "Alpha", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"}, {ASAID: "bravo", Name: "Bravo", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"}}
}

func putHistorySeason(t *testing.T, db *DB, season, stage, id string) {
	t.Helper()
	game := cachedGame(id, season, stage, "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
	if _, err := db.ReplaceSeason(context.Background(), season, stage, historyTeams(), []Game{game}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func putHistoryScope(t *testing.T, db *DB, season string, lifecycle SourceScopeLifecycle, discovery SourceScopeDiscovery) {
	t.Helper()
	stamp := formatTime(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	if _, err := db.db.ExecContext(context.Background(), `INSERT INTO source_scopes (season, stage, registration, lifecycle, discovery, registered_at, updated_at) VALUES (?, 'Regular Season', ?, ?, ?, ?, ?)`, season, SourceScopeCatalog, lifecycle, discovery, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func historySeasons(seasons []HistoricalSeason) []string {
	got := make([]string, 0, len(seasons))
	for _, season := range seasons {
		got = append(got, season.Entry.Season)
	}
	return got
}

func historySeason(t *testing.T, seasons []HistoricalSeason, want string) HistoricalSeason {
	t.Helper()
	for _, season := range seasons {
		if season.Entry.Season == want {
			return season
		}
	}
	t.Fatalf("season %s missing from %+v", want, seasons)
	return HistoricalSeason{}
}

func historyTableContents(t *testing.T, db *DB) map[string][]string {
	t.Helper()
	contents := make(map[string][]string)
	queries := map[string]string{
		"source_scopes":               "SELECT * FROM source_scopes",
		"games":                       "SELECT * FROM games",
		"teams":                       "SELECT * FROM teams",
		"sync_runs":                   "SELECT * FROM sync_runs",
		"xg_sync_runs":                "SELECT * FROM xg_sync_runs",
		"source_refresh_audits":       "SELECT * FROM source_refresh_audits",
		"source_resource_scope_state": "SELECT * FROM source_resource_scope_state",
	}
	for table, query := range queries {
		rows, err := db.db.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			contents[table] = append(contents[table], fmt.Sprintf("%#v", values))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return contents
}
