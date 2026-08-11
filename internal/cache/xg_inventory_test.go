package cache

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func xgMetadata() FullRefreshMetadata {
	due := time.Date(2030, 1, 8, 0, 1, 0, 0, time.UTC)
	return FullRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2030, 1, 1, 0, 1, 0, 0, time.UTC), NextFullDueAt: &due}
}
func xgMetadataAt(finished time.Time) FullRefreshMetadata {
	due := finished.Add(7 * 24 * time.Hour)
	return FullRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: finished.Add(-time.Minute), FinishedAt: finished, NextFullDueAt: &due}
}
func xgValue(id string) GameXG {
	return GameXG{GameID: id, Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1.2, Valid: true}, AwayXG: sql.NullFloat64{Float64: .8, Valid: true}}
}

func xgRunHistory(t *testing.T, ctx context.Context, db *DB, season, stage string) []XGSyncRun {
	t.Helper()
	rows, err := db.db.QueryContext(ctx, `SELECT id,started_at,finished_at,season,stage,outcome,error_summary,rows_seen,available_games,unavailable_games,rows_inserted,rows_updated,rows_unchanged FROM xg_sync_runs WHERE season=? AND stage=? ORDER BY id`, season, stage)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []XGSyncRun{}
	for rows.Next() {
		var value XGSyncRun
		var started, finished string
		if err := rows.Scan(&value.ID, &started, &finished, &value.Season, &value.Stage, &value.Outcome, &value.ErrorSummary, &value.RowsSeen, &value.AvailableGames, &value.UnavailableGames, &value.RowsInserted, &value.RowsUpdated, &value.RowsUnchanged); err != nil {
			t.Fatal(err)
		}
		value.StartedAt, err = time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatal(err)
		}
		value.FinishedAt, err = time.Parse(time.RFC3339, finished)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReplaceStageXGFullCandidatesAuditAndProtectedOmission(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	full := cachedGame("one", "2030", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
	pending := cachedGame("two", "2030", "Example", "PreMatch", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	pending.KickoffUTC = "2030-01-02T00:00:00Z"
	if _, err := db.ReplaceSeason(ctx, "2030", "Example", teams, []Game{full, pending}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	result, err := db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{xgValue("one")}, xgMetadata())
	if err != nil || result.XGRun == nil || len(result.Values) != 1 || result.Audit.RowsInserted != 1 || result.Audit.ReturnedRows != 1 {
		t.Fatalf("first=%+v,%v", result, err)
	}
	state, ok, err := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Example")
	if err != nil || !ok || state.LastFullSuccessAt == nil {
		t.Fatalf("state=%+v,%v,%v", state, ok, err)
	}
	result, err = db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{}, xgMetadata())
	if err != nil || result.Audit.RowsUnchanged != 1 || result.Values[0].Availability != XGAvailable {
		t.Fatalf("omission=%+v,%v", result, err)
	}
	values, err := db.GameXGStates(ctx, "2030", "Example")
	if err != nil || len(values) != 1 || values[0].GameID != "one" {
		t.Fatalf("read=%+v,%v", values, err)
	}
}

func TestReplaceStageXGEmptyAndValidation(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.ReplaceStageXG(ctx, "2030", "Empty", []GameXG{}, xgMetadata())
	if err != nil || result.XGRun == nil || len(result.Values) != 0 || result.Audit.RowsUnchanged != 0 {
		t.Fatalf("empty=%+v,%v", result, err)
	}
	if _, err := db.ReplaceStageXG(ctx, "2030", "Empty", nil, xgMetadata()); err == nil {
		t.Fatal("nil values accepted")
	}
	if v, ok, err := db.GameXGState(ctx, "missing"); err != nil || ok || !reflect.DeepEqual(v, GameXG{}) {
		t.Fatalf("missing=%+v,%t,%v", v, ok, err)
	}
}

func TestReplaceStageXGNoFullTimeCreatesReadyZeroVenue(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	g := cachedGame("pre", "2030", "No Full", "PreMatch", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	run, err := db.ReplaceSeason(ctx, "2030", "No Full", teams, []Game{g}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.ReplaceStageXG(ctx, "2030", "No Full", []GameXG{}, xgMetadata())
	if err != nil || result.XGRun == nil || len(result.Values) != 0 || result.XGRun.AvailableGames != 0 || result.XGRun.UnavailableGames != 0 {
		t.Fatalf("no full=%+v,%v", result, err)
	}
	venue, _ := db.VenueSummaries(ctx, []string{"2030"}, "No Full")
	if len(venue) != 1 || !venue[0].XGReady || venue[0].XGMatches != 0 {
		t.Fatalf("venue=%+v", venue)
	}
	season, err := db.Season(ctx, "2030", "No Full")
	if err != nil || season.LastSuccess.ID != run.ID {
		t.Fatalf("lineage=%+v,%v", season, err)
	}
}

func TestReplaceStageXGDelayedAvailableDoesNotRegressCheck(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	g := cachedGame("one", "2030", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2030", "Example", teams, []Game{g}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	newer := xgMetadata()
	newer.FinishedAt = time.Date(2030, 1, 2, 0, 1, 0, 0, time.UTC)
	d := time.Date(2030, 1, 9, 0, 1, 0, 0, time.UTC)
	newer.NextFullDueAt = &d
	if _, err := db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{}, newer); err != nil {
		t.Fatal(err)
	}
	delayed := xgMetadata()
	if _, err := db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{xgValue("one")}, delayed); err != nil {
		t.Fatal(err)
	}
	v, ok, err := db.GameXGState(ctx, "one")
	state, stateOK, stateErr := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Example")
	if err != nil || !ok || !v.LastCheckedAt.Equal(newer.FinishedAt) || v.Availability != XGAvailable || v.FirstObservedAt == nil || !v.FirstObservedAt.Equal(delayed.FinishedAt) || stateErr != nil || !stateOK || state.LastFullSuccessAt == nil || !state.LastFullSuccessAt.Equal(newer.FinishedAt) || state.NextFullDueAt == nil || !state.NextFullDueAt.Equal(*newer.NextFullDueAt) {
		t.Fatalf("delayed=%+v,%v state=%+v,%v", v, err, state, stateErr)
	}
}

func TestReplaceStageXGPreferenceAndMateriality(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	g := cachedGame("one", "2030", "Preference", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	fixture, err := db.ReplaceSeason(ctx, "2030", "Preference", teams, []Game{g}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2030, 2, 1, 0, 1, 0, 0, time.UTC)
	t2, t3, t4 := t1.Add(time.Hour), t1.Add(2*time.Hour), t1.Add(3*time.Hour)
	t5, t6 := t1.Add(4*time.Hour), t1.Add(5*time.Hour)
	first, err := db.ReplaceStageXG(ctx, "2030", "Preference", []GameXG{}, xgMetadataAt(t1))
	if err != nil || first.Audit.RowsInserted != 1 || first.XGRun.RowsInserted != 1 || first.XGRun.UnavailableGames != 1 {
		t.Fatalf("unavailable baseline=%+v,%v", first, err)
	}
	v := xgValue("one")
	v.RawJSON = `{"observation":1}`
	promoted, err := db.ReplaceStageXG(ctx, "2030", "Preference", []GameXG{v}, xgMetadataAt(t2))
	if err != nil || promoted.Audit.RowsUpdated != 1 || !promoted.Audit.DownstreamInputsChanged || promoted.XGRun.RowsUpdated != 1 {
		t.Fatalf("unavailable promotion=%+v,%v", promoted, err)
	}
	stored, ok, err := db.GameXGState(ctx, "one")
	if err != nil || !ok || stored.FirstObservedAt == nil || !stored.FirstObservedAt.Equal(t2) || !stored.LastCheckedAt.Equal(t2) || stored.Availability != XGAvailable {
		t.Fatalf("promoted state=%+v,%v", stored, err)
	}
	identical, err := db.ReplaceStageXG(ctx, "2030", "Preference", []GameXG{v}, xgMetadataAt(t3))
	if err != nil || identical.Audit.RowsUnchanged != 1 || identical.Audit.DownstreamInputsChanged || identical.XGRun.RowsUnchanged != 1 {
		t.Fatalf("identical newer=%+v,%v", identical, err)
	}
	venueBeforeRaw, _ := db.VenueSummaries(ctx, []string{"2030"}, "Preference")
	raw := v
	raw.RawJSON = `{"observation":2}`
	rawOnly, err := db.ReplaceStageXG(ctx, "2030", "Preference", []GameXG{raw}, xgMetadataAt(t4))
	venueAfterRaw, _ := db.VenueSummaries(ctx, []string{"2030"}, "Preference")
	if err != nil || rawOnly.Audit.RowsUpdated != 1 || rawOnly.Audit.DownstreamInputsChanged || rawOnly.XGRun.RowsUpdated != 1 || len(venueBeforeRaw) != 1 || len(venueAfterRaw) != 1 || venueAfterRaw[0].XGReady != venueBeforeRaw[0].XGReady || venueAfterRaw[0].XGMatches != venueBeforeRaw[0].XGMatches || venueAfterRaw[0].HomeXG != venueBeforeRaw[0].HomeXG || venueAfterRaw[0].AwayXG != venueBeforeRaw[0].AwayXG {
		t.Fatalf("raw-only=%+v,%v venue before=%+v after=%+v", rawOnly, err, venueBeforeRaw, venueAfterRaw)
	}
	numeric := raw
	numeric.HomeXG = sql.NullFloat64{Float64: 2.2, Valid: true}
	numericChange, err := db.ReplaceStageXG(ctx, "2030", "Preference", []GameXG{numeric}, xgMetadataAt(t5))
	venues, _ := db.VenueSummaries(ctx, []string{"2030"}, "Preference")
	if err != nil || numericChange.Audit.RowsUpdated != 1 || !numericChange.Audit.DownstreamInputsChanged || len(venues) != 1 || !venues[0].XGReady || venues[0].XGMatches != 1 || venues[0].HomeXG != 2.2 || venues[0].AwayXG != .8 {
		t.Fatalf("numeric=%+v,%v venue=%+v", numericChange, err, venues)
	}
	points := numeric
	points.HomeXPoints, points.AwayXPoints = sql.NullFloat64{Float64: 2.1, Valid: true}, sql.NullFloat64{Float64: .9, Valid: true}
	pointsChange, err := db.ReplaceStageXG(ctx, "2030", "Preference", []GameXG{points}, xgMetadataAt(t6))
	venuesAfterPoints, _ := db.VenueSummaries(ctx, []string{"2030"}, "Preference")
	if err != nil || pointsChange.Audit.RowsUpdated != 1 || !pointsChange.Audit.DownstreamInputsChanged || pointsChange.XGRun.RowsUpdated != 1 || len(venuesAfterPoints) != 1 || venuesAfterPoints[0].XGReady != venues[0].XGReady || venuesAfterPoints[0].XGMatches != venues[0].XGMatches || venuesAfterPoints[0].HomeXG != venues[0].HomeXG || venuesAfterPoints[0].AwayXG != venues[0].AwayXG {
		t.Fatalf("xPoints-only=%+v,%v venue before=%+v after=%+v", pointsChange, err, venues, venuesAfterPoints)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{"equal", t6}, {"older", t5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conflict := points
			conflict.HomeXG = sql.NullFloat64{Float64: 9, Valid: true}
			got, err := db.ReplaceStageXG(ctx, "2030", "Preference", []GameXG{conflict}, xgMetadataAt(tc.at))
			stored, ok, readErr := db.GameXGState(ctx, "one")
			state, stateOK, stateErr := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Preference")
			if err != nil || readErr != nil || !ok || stateErr != nil || !stateOK || got.Audit.RowsUnchanged != 1 || got.Audit.DownstreamInputsChanged || stored.HomeXG.Float64 != 2.2 || !stored.LastCheckedAt.Equal(t6) || state.LastFullSuccessAt == nil || !state.LastFullSuccessAt.Equal(t6) || state.NextFullDueAt == nil || !state.NextFullDueAt.Equal(*xgMetadataAt(t6).NextFullDueAt) {
				t.Fatalf("conflict=%+v,%v state=%+v,%v stored=%+v,%v", got, err, state, stateErr, stored, readErr)
			}
		})
	}
	season, err := db.Season(ctx, "2030", "Preference")
	if err != nil || season.LastSuccess == nil || season.LastSuccess.ID != fixture.ID {
		t.Fatalf("xG changes altered fixture lineage=%+v,%v", season, err)
	}
}

func TestReplaceStageXGRollsBackAllPersistenceSurfaces(t *testing.T) {
	for _, target := range []struct {
		name, table, event string
	}{
		{"xG insert", "game_xg", "INSERT"},
		{"xG update", "game_xg", "UPDATE"},
		{"venue", "venue_summaries", "UPDATE"},
		{"legacy run", "xg_sync_runs", "INSERT"},
		{"audit", "source_refresh_audits", "INSERT"},
		{"full state", "source_resource_scope_state", "UPDATE"},
	} {
		t.Run(target.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
			games := []Game{
				cachedGame("a-missing", "2030", "Rollback", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true}),
				cachedGame("b-promote", "2030", "Rollback", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true}),
				cachedGame("c-update", "2030", "Rollback", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true}),
			}
			if _, err := db.ReplaceSeason(ctx, "2030", "Rollback", teams, games, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			initial := xgValue("c-update")
			initial.RawJSON = `{"before":true}`
			if _, err := db.ReplaceStageXG(ctx, "2030", "Rollback", []GameXG{initial}, xgMetadataAt(time.Date(2030, 3, 1, 1, 0, 0, 0, time.UTC))); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.ExecContext(ctx, `DELETE FROM game_xg WHERE asa_game_id='a-missing'`); err != nil {
				t.Fatal(err)
			}
			beforeValues, err := db.GameXGStates(ctx, "2030", "Rollback")
			if err != nil {
				t.Fatal(err)
			}
			beforeVenue, err := db.VenueSummaries(ctx, []string{"2030"}, "Rollback")
			if err != nil {
				t.Fatal(err)
			}
			beforeRuns := xgRunHistory(t, ctx, db, "2030", "Rollback")
			beforeAudits, err := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2030", "Rollback")
			if err != nil {
				t.Fatal(err)
			}
			beforeState, beforeStateOK, err := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Rollback")
			if err != nil || !beforeStateOK {
				t.Fatalf("baseline state=%+v,%v", beforeState, err)
			}
			beforeSeason, err := db.Season(ctx, "2030", "Rollback")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.ExecContext(ctx, "CREATE TRIGGER abort_stage_xg BEFORE "+target.event+" ON "+target.table+" BEGIN SELECT RAISE(ABORT,'stop'); END"); err != nil {
				t.Fatal(err)
			}
			promotion := xgValue("b-promote")
			promotion.RawJSON = `{"promoted":true}`
			updated := xgValue("c-update")
			updated.HomeXG = sql.NullFloat64{Float64: 2.5, Valid: true}
			updated.RawJSON = `{"after":true}`
			got, err := db.ReplaceStageXG(ctx, "2030", "Rollback", []GameXG{promotion, updated}, xgMetadataAt(time.Date(2030, 3, 1, 2, 0, 0, 0, time.UTC)))
			if err == nil || got.Audit.ID != 0 || got.XGRun != nil || got.Values != nil {
				t.Fatalf("trigger result=%+v err=%v", got, err)
			}
			afterValues, valuesErr := db.GameXGStates(ctx, "2030", "Rollback")
			afterVenue, venueErr := db.VenueSummaries(ctx, []string{"2030"}, "Rollback")
			afterRuns := xgRunHistory(t, ctx, db, "2030", "Rollback")
			afterAudits, auditsErr := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2030", "Rollback")
			afterState, afterStateOK, stateErr := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Rollback")
			afterSeason, seasonErr := db.Season(ctx, "2030", "Rollback")
			if valuesErr != nil || venueErr != nil || auditsErr != nil || stateErr != nil || seasonErr != nil || !afterStateOK || !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeVenue, afterVenue) || !reflect.DeepEqual(beforeRuns, afterRuns) || !reflect.DeepEqual(beforeAudits, afterAudits) || !reflect.DeepEqual(beforeState, afterState) || beforeSeason.FixtureSnapshotID != afterSeason.FixtureSnapshotID || !reflect.DeepEqual(beforeSeason.LastSuccess, afterSeason.LastSuccess) {
				t.Fatalf("rollback incomplete: values=%v venue=%v audits=%v state=%v season=%v", valuesErr, venueErr, auditsErr, stateErr, seasonErr)
			}
		})
	}
}

func TestReplaceStageXGValidationAndIdentityAreWriteFree(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	g := cachedGame("one", "2030", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2030", "Example", teams, []Game{g}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	valid := xgValue("one")
	bad := valid
	bad.HomeXG = sql.NullFloat64{Float64: -1, Valid: true}
	pending := g
	pending.Status = "PreMatch"
	if _, err := db.db.ExecContext(ctx, `INSERT INTO games(asa_game_id,season,stage,kickoff_utc,status,home_team_id,away_team_id,last_updated_utc,raw_json,synced_at) VALUES ('pending','2030','Example','2030-01-02T00:00:00Z','PreMatch','alpha','bravo','2030-01-02T00:00:00Z','{}','2030-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	wrong := valid
	wrong.GameID = "pending"
	blank := valid
	blank.GameID = " "
	unavailable := valid
	unavailable.Availability = XGUnavailable
	same := valid
	same.AwayTeamID = "alpha"
	missingPair := valid
	missingPair.HomeXG = sql.NullFloat64{}
	nan := valid
	nan.HomeXG = sql.NullFloat64{Float64: math.NaN(), Valid: true}
	observed := valid
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	observed.FirstObservedAt = &now
	checked := valid
	checked.LastCheckedAt = now
	inf := valid
	inf.HomeXG = sql.NullFloat64{Float64: math.Inf(1), Valid: true}
	xp := valid
	xp.HomeXPoints = sql.NullFloat64{Float64: 4, Valid: true}
	xp.AwayXPoints = sql.NullFloat64{Float64: 4, Valid: true}
	xpNan := valid
	xpNan.HomeXPoints = sql.NullFloat64{Float64: math.NaN(), Valid: true}
	xpNan.AwayXPoints = sql.NullFloat64{Float64: 1, Valid: true}
	paddedTeam := valid
	paddedTeam.HomeTeamID = " alpha"
	blankTeam := valid
	blankTeam.HomeTeamID = ""
	xpPair := valid
	xpPair.HomeXPoints = sql.NullFloat64{Float64: 1, Valid: true}
	xpInf := xpPair
	xpInf.HomeXPoints.Float64 = math.Inf(1)
	xpNeg := xpPair
	xpNeg.HomeXPoints.Float64 = -1
	for _, values := range [][]GameXG{nil, {bad}, {wrong}, {valid, valid}, {blank}, {unavailable}, {same}, {missingPair}, {nan}, {observed}, {checked}, {inf}, {xp}, {xpNan}, {paddedTeam}, {blankTeam}, {xpPair}, {xpInf}, {xpNeg}} {
		if _, err := db.ReplaceStageXG(ctx, "2030", "Example", values, xgMetadata()); err == nil {
			t.Fatal("invalid xG accepted")
		}
	}
	badMeta := xgMetadata()
	badMeta.StartedAt = badMeta.FinishedAt.Add(time.Nanosecond)
	if _, err := db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{valid}, badMeta); err == nil {
		t.Fatal("raw metadata order accepted")
	}
	badDue := xgMetadata()
	early := badDue.FinishedAt.Add(-time.Nanosecond)
	badDue.NextFullDueAt = &early
	if _, err := db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{valid}, badDue); err == nil {
		t.Fatal("raw due ordering accepted")
	}
	if values, err := db.GameXGStates(ctx, "2030", "Example"); err != nil || len(values) != 0 {
		t.Fatalf("validation wrote=%+v,%v", values, err)
	}
	beforeVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	beforeValues, _ := db.GameXGStates(ctx, "2030", "Example")
	beforeSeason, _ := db.Season(ctx, "2030", "Example")
	beforeRun, _ := db.latestXGRun(ctx, "success", "2030", "Example")
	beforeAudit, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2030", "Example")
	beforeState, ok, _ := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Example")
	missing := valid
	missing.GameID = "missing"
	mismatch := valid
	mismatch.HomeTeamID = "bravo"
	mismatch.AwayTeamID = "alpha"
	other := valid
	other.GameID = "other"
	if _, err := db.db.ExecContext(ctx, `INSERT INTO games(asa_game_id,season,stage,kickoff_utc,status,home_team_id,away_team_id,last_updated_utc,raw_json,synced_at) VALUES ('other','2031','Other','2031-01-01T00:00:00Z','FullTime','alpha','bravo','2031-01-01T00:00:00Z','{}','2031-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO games(asa_game_id,season,stage,kickoff_utc,status,home_team_id,away_team_id,last_updated_utc,raw_json,synced_at) VALUES ('abandoned','2030','Example','2030-01-03T00:00:00Z','Abandoned','alpha','bravo','2030-01-03T00:00:00Z','{}','2030-01-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	pendingValue := valid
	pendingValue.GameID = "pending"
	abandonedValue := valid
	abandonedValue.GameID = "abandoned"
	for _, v := range []GameXG{missing, mismatch, other, pendingValue, abandonedValue} {
		if _, err := db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{v}, xgMetadata()); err == nil {
			t.Fatal("DB identity accepted")
		}
	}
	firstErr := func(values []GameXG) string {
		_, e := db.ReplaceStageXG(ctx, "2030", "Example", values, xgMetadata())
		if e == nil {
			t.Fatal("multi identity accepted")
		}
		return e.Error()
	}
	if got, want := firstErr([]GameXG{missing, other}), firstErr([]GameXG{other, missing}); got != want {
		t.Fatalf("identity ordering %q != %q", got, want)
	}
	afterVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	afterValues, _ := db.GameXGStates(ctx, "2030", "Example")
	afterSeason, _ := db.Season(ctx, "2030", "Example")
	afterRun, _ := db.latestXGRun(ctx, "success", "2030", "Example")
	afterAudit, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2030", "Example")
	afterState, afterOK, _ := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Example")
	if !reflect.DeepEqual(beforeVenue, afterVenue) || !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeSeason, afterSeason) || !reflect.DeepEqual(beforeRun, afterRun) || !reflect.DeepEqual(beforeAudit, afterAudit) || ok != afterOK || !reflect.DeepEqual(beforeState, afterState) {
		t.Fatal("identity failure mutated persistence")
	}
}

func TestGameXGReadsValidateAndAreScoped(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 12 {
		t.Fatalf("schema=%d,%v", version, err)
	}
	if _, ok, err := db.GameXGState(ctx, "missing"); err != nil || ok {
		t.Fatal("missing read")
	}
	if values, err := db.GameXGStates(ctx, "2030", "Example"); err != nil || values == nil || len(values) != 0 {
		t.Fatalf("empty=%+v,%v", values, err)
	}
	if _, _, err := db.GameXGState(ctx, " bad"); err == nil {
		t.Fatal("padded ID")
	}
	if _, err := db.GameXGStates(ctx, "2030 ", "Example"); err == nil {
		t.Fatal("padded scope")
	}
}

func TestGameXGReadOrderingUTCDefensiveAndMalformed(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	a := cachedGame("z", "2030", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	b := cachedGame("a", "2030", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2030", "Example", teams, []Game{a, b}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "z"} {
		if _, err := db.db.ExecContext(ctx, `INSERT INTO game_xg(asa_game_id,availability,home_team_id,away_team_id,home_xg,away_xg,raw_json,first_observed_at,last_checked_at) VALUES (?,'available','alpha','bravo',1,1,'{}','2030-01-01T01:00:00+01:00','2030-01-01T02:00:00+01:00')`, id); err != nil {
			t.Fatal(err)
		}
	}
	values, err := db.GameXGStates(ctx, "2030", "Example")
	if err != nil || len(values) != 2 || values[0].GameID != "a" || values[0].FirstObservedAt.Location() != time.UTC || values[0].LastCheckedAt.Location() != time.UTC {
		t.Fatalf("values=%+v,%v", values, err)
	}
	*values[0].FirstObservedAt = time.Time{}
	again, _, _ := db.GameXGState(ctx, "a")
	if again.FirstObservedAt.IsZero() {
		t.Fatal("pointer not defensive")
	}
	for _, q := range []string{`UPDATE game_xg SET home_xg=1,first_observed_at='bad' WHERE asa_game_id='a'`, `UPDATE game_xg SET first_observed_at='2030-01-01T00:00:00Z',last_checked_at='bad' WHERE asa_game_id='a'`} {
		if _, err := db.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
		if _, _, err := db.GameXGState(ctx, "a"); err == nil {
			t.Fatal("malformed row accepted")
		}
	}
}

func TestGameXGReadsRejectCorruptStoredCoherence(t *testing.T) {
	newFixture := func(t *testing.T) (context.Context, *DB) {
		t.Helper()
		ctx := context.Background()
		db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
		if err != nil {
			t.Fatal(err)
		}
		teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
		g := cachedGame("one", "2030", "Corrupt", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
		if _, err := db.ReplaceSeason(ctx, "2030", "Corrupt", teams, []Game{g}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ReplaceStageXG(ctx, "2030", "Corrupt", []GameXG{xgValue("one")}, xgMetadata()); err != nil {
			t.Fatal(err)
		}
		return ctx, db
	}
	for _, tc := range []struct {
		name, update, want string
	}{
		{"participant", `UPDATE game_xg SET home_team_id='bravo' WHERE asa_game_id='one'`, "team identity"},
		{"availability", `UPDATE game_xg SET availability='broken' WHERE asa_game_id='one'`, "availability"},
		{"one-sided expected points", `UPDATE game_xg SET home_xpoints=1,away_xpoints=NULL WHERE asa_game_id='one'`, "expected points"},
		{"negative expected points", `UPDATE game_xg SET home_xpoints=-1,away_xpoints=-1 WHERE asa_game_id='one'`, "expected points"},
		{"out of range expected points", `UPDATE game_xg SET home_xpoints=4,away_xpoints=4 WHERE asa_game_id='one'`, "expected points"},
		{"available no first observation", `UPDATE game_xg SET first_observed_at=NULL WHERE asa_game_id='one'`, "available values"},
		{"unavailable xG only", `UPDATE game_xg SET availability='unavailable',home_xg=1,away_xg=1,home_xpoints=NULL,away_xpoints=NULL,raw_json='',first_observed_at=NULL WHERE asa_game_id='one'`, "unavailable values"},
		{"unavailable expected points only", `UPDATE game_xg SET availability='unavailable',home_xg=NULL,away_xg=NULL,home_xpoints=1,away_xpoints=1,raw_json='',first_observed_at=NULL WHERE asa_game_id='one'`, "unavailable values"},
		{"unavailable first observation only", `UPDATE game_xg SET availability='unavailable',home_xg=NULL,away_xg=NULL,home_xpoints=NULL,away_xpoints=NULL,raw_json='',first_observed_at='2030-01-01T00:00:00Z' WHERE asa_game_id='one'`, "unavailable values"},
		{"unavailable raw JSON only", `UPDATE game_xg SET availability='unavailable',home_xg=NULL,away_xg=NULL,home_xpoints=NULL,away_xpoints=NULL,raw_json='{}',first_observed_at=NULL WHERE asa_game_id='one'`, "unavailable values"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db := newFixture(t)
			defer db.Close()
			if _, err := db.db.ExecContext(ctx, `PRAGMA ignore_check_constraints = ON`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.ExecContext(ctx, tc.update); err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.GameXGState(ctx, "one"); err == nil || !strings.Contains(err.Error(), `invalid stored xG "one"`) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("exact error=%v want context/%q", err, tc.want)
			}
			if _, err := db.GameXGStates(ctx, "2030", "Corrupt"); err == nil || !strings.Contains(err.Error(), `invalid stored xG "one"`) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("scoped error=%v want context/%q", err, tc.want)
			}
		})
	}
	t.Run("malformed stored game identity scoped", func(t *testing.T) {
		ctx, db := newFixture(t)
		defer db.Close()
		if _, err := db.db.ExecContext(ctx, `INSERT INTO games(asa_game_id,season,stage,kickoff_utc,status,home_team_id,away_team_id,home_score,away_score,matchday,last_updated_utc,raw_json,synced_at) VALUES (' bad','2030','Corrupt','2030-01-01T00:00:00Z','FullTime','alpha','bravo',1,0,NULL,'2030-01-01T00:00:00Z','{}','2030-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.ExecContext(ctx, `INSERT INTO game_xg(asa_game_id,availability,home_team_id,away_team_id,home_xg,away_xg,raw_json,first_observed_at,last_checked_at) VALUES (' bad','available','alpha','bravo',1,1,'{}','2030-01-01T00:00:00Z','2030-01-01T00:00:00Z')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.GameXGStates(ctx, "2030", "Corrupt"); err == nil || !strings.Contains(err.Error(), `invalid stored xG " bad": game identity`) {
			t.Fatalf("malformed ID error=%v", err)
		}
	})
}

func TestReplaceStageXGRollsBackCandidateCountReadFailure(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	g := cachedGame("one", "2030", "Count Failure", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2030", "Count Failure", teams, []Game{g}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceStageXG(ctx, "2030", "Count Failure", []GameXG{}, xgMetadataAt(time.Date(2030, 6, 1, 1, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	beforeValues, _ := db.GameXGStates(ctx, "2030", "Count Failure")
	beforeVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Count Failure")
	beforeRuns := xgRunHistory(t, ctx, db, "2030", "Count Failure")
	beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2030", "Count Failure")
	beforeState, beforeStateOK, _ := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Count Failure")
	if !beforeStateOK {
		t.Fatal("missing baseline full state")
	}
	if _, err := db.db.ExecContext(ctx, `CREATE TRIGGER corrupt_xg_after_venue AFTER UPDATE ON venue_summaries BEGIN UPDATE game_xg SET last_checked_at='broken' WHERE asa_game_id='one'; END`); err != nil {
		t.Fatal(err)
	}
	got, err := db.ReplaceStageXG(ctx, "2030", "Count Failure", []GameXG{xgValue("one")}, xgMetadataAt(time.Date(2030, 6, 1, 2, 0, 0, 0, time.UTC)))
	if err == nil || !strings.Contains(err.Error(), "load committed xG") || got.Audit.ID != 0 || got.XGRun != nil || got.Values != nil {
		t.Fatalf("candidate count failure=%+v,%v", got, err)
	}
	afterValues, valuesErr := db.GameXGStates(ctx, "2030", "Count Failure")
	afterVenue, venueErr := db.VenueSummaries(ctx, []string{"2030"}, "Count Failure")
	afterRuns := xgRunHistory(t, ctx, db, "2030", "Count Failure")
	afterAudits, auditsErr := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2030", "Count Failure")
	afterState, afterStateOK, stateErr := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Count Failure")
	if valuesErr != nil || venueErr != nil || auditsErr != nil || stateErr != nil || !afterStateOK || !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeVenue, afterVenue) || !reflect.DeepEqual(beforeRuns, afterRuns) || !reflect.DeepEqual(beforeAudits, afterAudits) || !reflect.DeepEqual(beforeState, afterState) {
		t.Fatalf("candidate count failure did not roll back values=%v venue=%v audits=%v state=%v", valuesErr, venueErr, auditsErr, stateErr)
	}
}

func TestReplaceStageXGOwnsCallerAndResultValues(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	g := cachedGame("one", "2030", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2030", "Example", teams, []Game{g}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	input := []GameXG{xgValue("one")}
	before := input[0]
	metadata := xgMetadata()
	ptr := metadata.NextFullDueAt
	due := *ptr
	result, err := db.ReplaceStageXG(ctx, "2030", "Example", input, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if input[0] != before || metadata.NextFullDueAt != ptr || !metadata.NextFullDueAt.Equal(due) {
		t.Fatal("caller input mutated")
	}
	result.Values[0].HomeXG.Float64 = 99
	state, ok, err := db.GameXGState(ctx, "one")
	if err != nil || !ok || state.HomeXG.Float64 == 99 {
		t.Fatalf("result mutation leaked=%+v,%v", state, err)
	}
}

func TestReplaceStageXGMixedCandidatesAndLineage(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	one := cachedGame("z", "2030", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	two := cachedGame("a", "2030", "Example", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	two.KickoffUTC = "2030-01-02T00:00:00Z"
	pre := cachedGame("pre", "2030", "Example", "PreMatch", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	pre.KickoffUTC = "2030-01-03T00:00:00Z"
	abandoned := cachedGame("ab", "2030", "Example", "Abandoned", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	abandoned.KickoffUTC = "2030-01-04T00:00:00Z"
	run, err := db.ReplaceSeason(ctx, "2030", "Example", teams, []Game{one, two, pre, abandoned}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	value := xgValue("z")
	if _, err := db.db.ExecContext(ctx, `INSERT INTO game_xg(asa_game_id,availability,home_team_id,away_team_id,home_xg,away_xg,raw_json,first_observed_at,last_checked_at) VALUES ('pre','available','alpha','bravo',1,1,'{}','2030-01-01T00:00:00Z','2030-01-01T00:00:00Z'),('ab','unavailable','alpha','bravo',NULL,NULL,'',NULL,'2030-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	beforePre, ok, err := db.GameXGState(ctx, "pre")
	if err != nil || !ok {
		t.Fatalf("preexisting PreMatch xG=%+v,%v", beforePre, err)
	}
	beforeAbandoned, ok, err := db.GameXGState(ctx, "ab")
	if err != nil || !ok {
		t.Fatalf("preexisting Abandoned xG=%+v,%v", beforeAbandoned, err)
	}
	result, err := db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{value}, xgMetadata())
	meta := xgMetadata()
	if err != nil || !reflect.DeepEqual([]string{result.Values[0].GameID, result.Values[1].GameID, result.Values[2].GameID, result.Values[3].GameID}, []string{"a", "ab", "pre", "z"}) || result.Values[0].Availability != XGUnavailable || result.Audit.ID == 0 || result.Audit.Resource != SourceResourceGameXG || result.Audit.Mode != SourceRefreshFull || result.Audit.Outcome != SourceRefreshSuccess || result.Audit.StartedAt != meta.StartedAt || result.Audit.FinishedAt != meta.FinishedAt || result.Audit.RequestedRows != 0 || result.Audit.ReturnedRows != 1 || result.Audit.RowsInserted != 2 || result.Audit.RowsUpdated != 0 || result.Audit.RowsUnchanged != 0 || result.Audit.RowsDeleted != 0 || result.XGRun == nil || result.XGRun.ID == 0 || result.XGRun.StartedAt != meta.StartedAt || result.XGRun.FinishedAt != meta.FinishedAt || result.XGRun.RowsSeen != 1 || result.XGRun.AvailableGames != 1 || result.XGRun.UnavailableGames != 1 || result.XGRun.RowsInserted != 2 || result.XGRun.RowsUpdated != 0 || result.XGRun.RowsUnchanged != 0 {
		t.Fatalf("mixed=%+v,%v", result, err)
	}
	afterPre, _, err := db.GameXGState(ctx, "pre")
	if err != nil || !reflect.DeepEqual(afterPre, beforePre) {
		t.Fatalf("PreMatch xG changed=%+v want=%+v err=%v", afterPre, beforePre, err)
	}
	afterAbandoned, _, err := db.GameXGState(ctx, "ab")
	if err != nil || !reflect.DeepEqual(afterAbandoned, beforeAbandoned) {
		t.Fatalf("Abandoned xG changed=%+v want=%+v err=%v", afterAbandoned, beforeAbandoned, err)
	}
	venues, err := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if err != nil || len(venues) != 1 || !venues[0].XGReady || venues[0].XGMatches != 1 || venues[0].HomeXG != value.HomeXG.Float64 || venues[0].AwayXG != value.AwayXG.Float64 {
		t.Fatalf("non-FullTime xG affected venue=%+v,%v", venues, err)
	}
	audits, err := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2030", "Example")
	if err != nil || len(audits) != 1 || !reflect.DeepEqual(audits[0], result.Audit) {
		t.Fatalf("persisted audit=%+v want=%+v err=%v", audits, result.Audit, err)
	}
	state, ok, err := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Example")
	if err != nil || !ok || state.LastFullSuccessAt == nil || !state.LastFullSuccessAt.Equal(meta.FinishedAt) || state.NextFullDueAt == nil || !state.NextFullDueAt.Equal(*meta.NextFullDueAt) {
		t.Fatalf("full state=%+v,%v", state, err)
	}
	season, err := db.Season(ctx, "2030", "Example")
	if err != nil || season.LastSuccess == nil || season.LastSuccess.ID != run.ID {
		t.Fatalf("fixture lineage=%+v,%v", season, err)
	}
	second, err := db.ReplaceStageXG(ctx, "2030", "Example", []GameXG{}, xgMetadata())
	if err != nil || second.Values[len(second.Values)-1].Availability != XGAvailable || second.Audit.RowsUnchanged != 2 || second.XGRun.RowsUnchanged != 2 {
		t.Fatalf("protected=%+v,%v", second, err)
	}
	state, ok, err = db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Example")
	if err != nil || !ok || state.NextFullDueAt == nil {
		t.Fatalf("full state=%+v,%v", state, err)
	}
}

func TestStageXGOmissionsDoNotDeleteAndFixtureAuthorityDoes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	fullOne := cachedGame("one", "2030", "Cascade", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	fullTwo := cachedGame("two", "2030", "Cascade", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	pre := cachedGame("pre", "2030", "Cascade", "PreMatch", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	if _, err := db.ReplaceSeason(ctx, "2030", "Cascade", teams, []Game{fullOne, fullTwo, pre}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO game_xg(asa_game_id,availability,home_team_id,away_team_id,home_xg,away_xg,raw_json,first_observed_at,last_checked_at) VALUES ('pre','unavailable','alpha','bravo',NULL,NULL,'',NULL,'2030-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceStageXG(ctx, "2030", "Cascade", []GameXG{xgValue("one")}, xgMetadataAt(time.Date(2030, 4, 1, 1, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	omitted, err := db.ReplaceStageXG(ctx, "2030", "Cascade", []GameXG{}, xgMetadataAt(time.Date(2030, 4, 1, 2, 0, 0, 0, time.UTC)))
	states, readErr := db.GameXGStates(ctx, "2030", "Cascade")
	if err != nil || readErr != nil || omitted.Audit.RowsDeleted != 0 || len(states) != 3 || states[0].GameID != "one" || states[0].Availability != XGAvailable || states[1].GameID != "pre" || states[1].Availability != XGUnavailable || states[2].GameID != "two" || states[2].Availability != XGUnavailable {
		t.Fatalf("stage omission deleted xG=%+v states=%+v errs=%v/%v", omitted, states, err, readErr)
	}
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "charlie", Name: "C"}}, xgMetadataAt(time.Date(2030, 4, 1, 3, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	changed := fullOne
	changed.HomeTeamID = "charlie"
	changed.LastUpdatedUTC = "2030-04-01T03:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Cascade", []Game{changed, pre}, nil, xgMetadataAt(time.Date(2030, 4, 1, 4, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if _, ok, err := db.GameXGState(ctx, id); err != nil || ok {
			t.Fatalf("fixture authority left incompatible xG %q: %v", id, err)
		}
	}
	if value, ok, err := db.GameXGState(ctx, "pre"); err != nil || !ok || value.Availability != XGUnavailable {
		t.Fatalf("non-FullTime xG not preserved after unrelated fixture authority=%+v,%v", value, err)
	}
}

func TestLegacyReplaceGameXGRemainsIsolatedFromStageAuditState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	g := cachedGame("one", "2030", "Legacy", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2030", "Legacy", teams, []Game{g}, time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2030, 5, 1, 1, 0, 0, 0, time.FixedZone("legacy", -7*60*60))
	before, after := time.Now().UTC(), time.Now().UTC()
	run, err := db.ReplaceGameXG(ctx, "2030", "Legacy", []Game{g}, []GameXG{xgValue("one")}, started)
	after = time.Now().UTC()
	venues, venueErr := db.VenueSummaries(ctx, []string{"2030"}, "Legacy")
	if err != nil || venueErr != nil || run.ID == 0 || !run.StartedAt.Equal(started.UTC()) || run.FinishedAt.Before(before) || run.FinishedAt.After(after) || run.RowsSeen != 1 || run.AvailableGames != 1 || run.UnavailableGames != 0 || run.RowsInserted != 1 || len(venues) != 1 || !venues[0].XGReady || venues[0].XGMatches != 1 || venues[0].HomeXG != 1.2 || venues[0].AwayXG != .8 {
		t.Fatalf("legacy success=%+v,%v venue=%+v,%v", run, err, venues, venueErr)
	}
	if _, err := db.ReplaceGameXG(ctx, "2030", "Legacy", []Game{g}, []GameXG{}, started); err == nil {
		t.Fatal("legacy available omission accepted")
	}
	if err := db.RecordXGFailure(ctx, "2030", "Legacy", started, errors.New("upstream failed")); err != nil {
		t.Fatal(err)
	}
	status, err := db.XGStatus(ctx, "2030", "Legacy")
	audits, auditErr := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2030", "Legacy")
	state, stateOK, stateErr := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2030", "Legacy")
	runs := xgRunHistory(t, ctx, db, "2030", "Legacy")
	if err != nil || auditErr != nil || stateErr != nil || status.LastAttempt == nil || status.LastAttempt.Outcome != "failure" || status.LastSuccess == nil || status.LastSuccess.ID != run.ID || len(runs) != 2 || len(audits) != 0 || stateOK || state != (SourceResourceScopeState{}) {
		t.Fatalf("legacy isolation status=%+v audits=%+v state=%+v runs=%+v errs=%v/%v/%v", status, audits, state, runs, err, auditErr, stateErr)
	}
}

func TestStageXGHasNoSchemaArtifacts(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || schemaVersion != 12 || version != 12 {
		t.Fatalf("schema version=%d const=%d err=%v", version, schemaVersion, err)
	}
	rows, err := db.db.QueryContext(ctx, `PRAGMA table_info(game_xg)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"asa_game_id", "availability", "home_team_id", "away_team_id", "home_xg", "away_xg", "raw_json", "first_observed_at", "last_checked_at", "home_xpoints", "away_xpoints"}; !reflect.DeepEqual(columns, want) {
		t.Fatalf("game_xg columns=%v want=%v", columns, want)
	}
	var artifacts int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'stage_xg_%' OR name LIKE 'game_xg_refresh_%'`).Scan(&artifacts); err != nil || artifacts != 0 {
		t.Fatalf("unexpected stage xG schema artifacts=%d,%v", artifacts, err)
	}
}
