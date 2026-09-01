package cache

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func xgCheckMetadata(hour int) TargetedRefreshMetadata {
	return TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: time.Date(2031, 1, 1, hour, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, hour, 1, 0, 0, time.UTC)}
}

func xgCheckRequest(id string, hour int) CheckedXGRequest {
	due := time.Date(2031, 1, 2, hour, 0, 0, 0, time.UTC)
	return CheckedXGRequest{GameID: id, NextDueAt: &due}
}

func checkedXGFixture(t *testing.T) (context.Context, *DB, []Game, string) {
	t.Helper()
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	teams := []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}
	one := cachedGame("one", "2031", "Checks", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	two := cachedGame("two", "2031", "Checks", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	two.KickoffUTC = "2031-01-02T00:00:00Z"
	if _, err := db.ReplaceSeason(ctx, "2031", "Checks", teams, []Game{one, two}, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close failed fixture database: %v", closeErr)
		}
		t.Fatal(err)
	}
	return ctx, db, []Game{one, two}, path
}

func TestMigrationTwelveBackfillsXGChecks(t *testing.T) {
	ctx, db, _, path := checkedXGFixture(t)
	available := xgValue("one")
	if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{available}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close failed fixture database: %v", closeErr)
		}
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DELETE FROM schema_migrations WHERE version=14", "DELETE FROM schema_migrations WHERE version=13", "DROP INDEX game_xg_checks_due_idx", "DROP TABLE game_xg_checks", "DELETE FROM schema_migrations WHERE version=12"} {
		if _, err := legacy.ExecContext(ctx, q); err != nil {
			if closeErr := legacy.Close(); closeErr != nil {
				t.Errorf("close legacy database: %v", closeErr)
			}
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	states, err := db.GameXGCheckStates(ctx, "2031", "Checks")
	stamp := time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)
	if err != nil || len(states) != 2 || states[0].GameID != "one" || !states[0].LastCheckedAt.Equal(stamp) || states[0].FirstAvailableObservedAt == nil || !states[0].FirstAvailableObservedAt.Equal(stamp) || states[1].GameID != "two" || !states[1].LastCheckedAt.Equal(stamp) || states[1].FirstAvailableObservedAt != nil || states[0].LastMaterialChangeAt != nil || states[0].NextDueAt != nil || states[1].NextDueAt != nil {
		t.Fatalf("backfill=%+v,%v", states, err)
	}
	var version, audits, runs int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 14 {
		t.Fatalf("version=%d,%v", version, err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_refresh_audits WHERE resource='game_xg'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM xg_sync_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if audits != 1 || runs != 1 {
		t.Fatalf("migration fabricated lineage audits=%d runs=%d", audits, runs)
	}
}

func TestUpsertCheckedXGOmissionsOnlyAdvanceCheckState(t *testing.T) {
	ctx, db, _, _ := checkedXGFixture(t)
	defer db.Close()
	if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{xgValue("one")}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	beforeValues, _ := db.GameXGStates(ctx, "2031", "Checks")
	beforeVenue, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	beforeFull, beforeFullOK, _ := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2031", "Checks")
	result, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("two", 2), xgCheckRequest("one", 2)}, []GameXG{}, xgCheckMetadata(2))
	afterValues, _ := db.GameXGStates(ctx, "2031", "Checks")
	afterVenue, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	afterFull, afterFullOK, _ := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2031", "Checks")
	if err != nil || result.XGRun == nil || result.Audit.Mode != SourceRefreshTargeted || result.Audit.RequestedRows != 2 || result.Audit.ReturnedRows != 0 || result.Audit.RowsInserted != 0 || result.Audit.RowsUpdated != 0 || result.Audit.RowsUnchanged != 0 || result.Audit.RowsDeleted != 0 || result.XGRun.RowsSeen != 0 || result.XGRun.AvailableGames != 0 || result.XGRun.UnavailableGames != 0 || !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeVenue, afterVenue) || beforeFullOK != afterFullOK || !reflect.DeepEqual(beforeFull, afterFull) || len(result.Values) != 2 || result.Values[0].GameID != "one" || result.Values[1].GameID != "two" {
		t.Fatalf("omission=%+v,%v", result, err)
	}
	states, err := db.GameXGCheckStates(ctx, "2031", "Checks")
	if err != nil || len(states) != 2 || !states[0].LastCheckedAt.Equal(xgCheckMetadata(2).FinishedAt) || states[0].NextDueAt == nil || !states[0].NextDueAt.Equal(*xgCheckRequest("one", 2).NextDueAt) || states[0].FirstAvailableObservedAt == nil || states[1].NextDueAt == nil {
		t.Fatalf("omission states=%+v,%v", states, err)
	}
}

func TestUpsertCheckedXGPreferenceClocksAndVenueReadiness(t *testing.T) {
	ctx, db, _, _ := checkedXGFixture(t)
	defer db.Close()
	if _, ok, err := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2031", "Checks"); err != nil || ok {
		t.Fatalf("unexpected full state before targeted xG=%t,%v", ok, err)
	}
	request := xgCheckRequest("one", 1)
	value := xgValue("one")
	value.RawJSON = `{"v":1}`
	first, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{request}, []GameXG{value}, xgCheckMetadata(1))
	if err != nil || first.Audit.RowsInserted != 1 || !first.Audit.DownstreamInputsChanged || first.XGRun.RowsInserted != 1 {
		t.Fatalf("insert=%+v,%v", first, err)
	}
	if state, ok, err := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2031", "Checks"); err != nil || ok || state != (SourceResourceScopeState{}) {
		t.Fatalf("targeted xG changed full state=%+v,%t,%v", state, ok, err)
	}
	venue, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	if len(venue) != 1 || venue[0].XGReady || venue[0].XGMatches != 1 || venue[0].HomeXG != 1.2 {
		t.Fatalf("targeted venue=%+v", venue)
	}
	raw := value
	raw.RawJSON = `{"v":2}`
	rawResult, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 2)}, []GameXG{raw}, xgCheckMetadata(2))
	if err != nil || rawResult.Audit.RowsUpdated != 1 || rawResult.Audit.DownstreamInputsChanged {
		t.Fatalf("raw=%+v,%v", rawResult, err)
	}
	delayed := value
	delayed.HomeXG = sql.NullFloat64{Float64: 9, Valid: true}
	delayedResult, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 0)}, []GameXG{delayed}, xgCheckMetadata(0))
	state, ok, stateErr := db.GameXGCheckState(ctx, "one")
	stored, storedOK, storedErr := db.GameXGState(ctx, "one")
	if err != nil || delayedResult.Audit.RowsUnchanged != 1 || delayedResult.Audit.DownstreamInputsChanged || stateErr != nil || !ok || storedErr != nil || !storedOK || stored.HomeXG.Float64 != 1.2 || !state.LastCheckedAt.Equal(xgCheckMetadata(2).FinishedAt) || state.FirstAvailableObservedAt == nil || !state.FirstAvailableObservedAt.Equal(xgCheckMetadata(0).FinishedAt) || state.LastMaterialChangeAt == nil || !state.LastMaterialChangeAt.Equal(xgCheckMetadata(1).FinishedAt) {
		t.Fatalf("delayed=%+v,%v state=%+v,%v stored=%+v,%v", delayedResult, err, state, stateErr, stored, storedErr)
	}
}

func TestGameXGCheckReadsAndTargetedValidationAreWriteFree(t *testing.T) {
	ctx, db, games, _ := checkedXGFixture(t)
	defer db.Close()
	if state, ok, err := db.GameXGCheckState(ctx, "one"); err != nil || ok || state != (GameXGCheckState{}) {
		t.Fatalf("missing state=%+v,%t,%v", state, ok, err)
	}
	if states, err := db.GameXGCheckStates(ctx, "2031", "Checks"); err != nil || states == nil || len(states) != 0 {
		t.Fatalf("empty states=%+v,%v", states, err)
	}
	if _, _, err := db.GameXGCheckState(ctx, " one"); err == nil {
		t.Fatal("padded ID accepted")
	}
	if _, err := db.GameXGCheckStates(ctx, "2031 ", "Checks"); err == nil {
		t.Fatal("padded scope accepted")
	}
	beforeValues, _ := db.GameXGStates(ctx, "2031", "Checks")
	beforeVenue, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	beforeRuns := xgRunHistory(t, ctx, db, "2031", "Checks")
	beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
	beforeStates, _ := db.GameXGCheckStates(ctx, "2031", "Checks")
	for _, tc := range []struct {
		name string
		req  []CheckedXGRequest
		out  []GameXG
	}{
		{"nil request", nil, []GameXG{}},
		{"empty request", []CheckedXGRequest{}, []GameXG{}},
		{"padded request", []CheckedXGRequest{{GameID: " one"}}, []GameXG{}},
		{"duplicate request", []CheckedXGRequest{{GameID: "one"}, {GameID: "one"}}, []GameXG{}},
		{"nil response", []CheckedXGRequest{{GameID: "one"}}, nil},
		{"explicit unavailable", []CheckedXGRequest{{GameID: "one"}}, []GameXG{{GameID: "one", Availability: XGUnavailable, HomeTeamID: "alpha", AwayTeamID: "bravo"}}},
		{"unrequested", []CheckedXGRequest{{GameID: "one"}}, []GameXG{xgValue("two")}},
		{"negative xG", []CheckedXGRequest{{GameID: "one"}}, []GameXG{func() GameXG { v := xgValue("one"); v.HomeXG.Float64 = -1; return v }()}},
		{"infinite xG", []CheckedXGRequest{{GameID: "one"}}, []GameXG{func() GameXG { v := xgValue("one"); v.HomeXG.Float64 = math.Inf(1); return v }()}},
		{"one sided xPoints", []CheckedXGRequest{{GameID: "one"}}, []GameXG{func() GameXG { v := xgValue("one"); v.HomeXPoints = sql.NullFloat64{Float64: 1, Valid: true}; return v }()}},
		{"out of range xPoints", []CheckedXGRequest{{GameID: "one"}}, []GameXG{func() GameXG {
			v := xgValue("one")
			v.HomeXPoints, v.AwayXPoints = sql.NullFloat64{Float64: 4, Valid: true}, sql.NullFloat64{Float64: 4, Valid: true}
			return v
		}()}},
		{"caller observation", []CheckedXGRequest{{GameID: "one"}}, []GameXG{func() GameXG {
			v := xgValue("one")
			at := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
			v.FirstObservedAt = &at
			return v
		}()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", tc.req, tc.out, xgCheckMetadata(1)); err == nil {
				t.Fatal("invalid caller data accepted")
			}
		})
	}
	tooEarlyDue := time.Date(2031, 1, 1, 1, 0, 0, 0, time.UTC)
	tooEarly := CheckedXGRequest{GameID: "one", NextDueAt: &tooEarlyDue}
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{tooEarly}, []GameXG{}, xgCheckMetadata(1)); err == nil {
		t.Fatal("due before raw finish accepted")
	}
	bad := xgValue("one")
	bad.HomeTeamID = "charlie"
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{{GameID: "missing"}, {GameID: "one"}, {GameID: "two"}}, []GameXG{bad}, xgCheckMetadata(1)); err == nil || !strings.Contains(err.Error(), "[missing one]") {
		t.Fatalf("sorted database identities=%v", err)
	}
	afterValues, _ := db.GameXGStates(ctx, "2031", "Checks")
	afterVenue, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	afterRuns := xgRunHistory(t, ctx, db, "2031", "Checks")
	afterAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
	afterStates, _ := db.GameXGCheckStates(ctx, "2031", "Checks")
	if !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeVenue, afterVenue) || !reflect.DeepEqual(beforeRuns, afterRuns) || !reflect.DeepEqual(beforeAudits, afterAudits) || !reflect.DeepEqual(beforeStates, afterStates) || len(games) != 2 {
		t.Fatal("invalid targeted xG mutated cache")
	}
}

func TestFullStageXGPreservesTargetedDueAndParentInvalidation(t *testing.T) {
	ctx, db, games, _ := checkedXGFixture(t)
	defer db.Close()
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 4)}, []GameXG{xgValue("one")}, xgCheckMetadata(4)); err != nil {
		t.Fatal(err)
	}
	before, ok, err := db.GameXGCheckState(ctx, "one")
	if err != nil || !ok || before.NextDueAt == nil {
		t.Fatalf("targeted state=%+v,%v", before, err)
	}
	full := FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 5, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 5, 1, 0, 0, time.UTC)}
	if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{xgValue("one")}, full); err != nil {
		t.Fatal(err)
	}
	after, ok, err := db.GameXGCheckState(ctx, "one")
	if err != nil || !ok || !after.LastCheckedAt.Equal(full.FinishedAt) || after.NextDueAt == nil || !after.NextDueAt.Equal(*before.NextDueAt) {
		t.Fatalf("full overwrote due=%+v before=%+v err=%v", after, before, err)
	}
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "charlie", Name: "C"}}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: full.FinishedAt, FinishedAt: full.FinishedAt.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	changed := games[0]
	changed.HomeTeamID = "charlie"
	changed.LastUpdatedUTC = "2031-01-01T06:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2031", "Checks", []Game{changed, games[1]}, nil, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 6, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 6, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GameXGCheckState(ctx, "one"); err != nil || ok {
		t.Fatalf("participant invalidation left state=%t,%v", ok, err)
	}
	if _, err := db.ReplaceGameInventory(ctx, "2031", "Checks", []Game{changed}, nil, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 7, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 7, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GameXGCheckState(ctx, "two"); err != nil || ok {
		t.Fatalf("parent deletion did not cascade=%t,%v", ok, err)
	}
}

func TestUpsertCheckedXGRollsBackEveryBoundary(t *testing.T) {
	for _, target := range []struct {
		name, trigger string
	}{
		{"value", "CREATE TRIGGER abort_checked_xg BEFORE UPDATE ON game_xg BEGIN SELECT RAISE(ABORT,'stop'); END"},
		{"check state", "CREATE TRIGGER abort_checked_xg BEFORE UPDATE ON game_xg_checks BEGIN SELECT RAISE(ABORT,'stop'); END"},
		{"venue", "CREATE TRIGGER abort_checked_xg BEFORE UPDATE ON venue_summaries BEGIN SELECT RAISE(ABORT,'stop'); END"},
		{"legacy lineage", "CREATE TRIGGER abort_checked_xg BEFORE INSERT ON xg_sync_runs BEGIN SELECT RAISE(ABORT,'stop'); END"},
		{"audit", "CREATE TRIGGER abort_checked_xg BEFORE INSERT ON source_refresh_audits BEGIN SELECT RAISE(ABORT,'stop'); END"},
		{"complete result scan", "CREATE TRIGGER corrupt_checked_xg AFTER UPDATE ON game_xg_checks BEGIN UPDATE game_xg SET last_checked_at='bad' WHERE asa_game_id='one'; END"},
	} {
		t.Run(target.name, func(t *testing.T) {
			ctx, db, _, _ := checkedXGFixture(t)
			defer db.Close()
			if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{xgValue("one")}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
				t.Fatal(err)
			}
			beforeValues, _ := db.GameXGStates(ctx, "2031", "Checks")
			beforeVenue, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
			beforeRuns := xgRunHistory(t, ctx, db, "2031", "Checks")
			beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
			beforeStates, _ := db.GameXGCheckStates(ctx, "2031", "Checks")
			beforeFull, beforeFullOK, _ := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2031", "Checks")
			if _, err := db.db.ExecContext(ctx, target.trigger); err != nil {
				t.Fatal(err)
			}
			value := xgValue("one")
			value.HomeXG = sql.NullFloat64{Float64: 2, Valid: true}
			got, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 2), xgCheckRequest("two", 2)}, []GameXG{value}, xgCheckMetadata(2))
			if err == nil || got.Audit.ID != 0 || got.XGRun != nil || got.Values != nil {
				t.Fatalf("trigger result=%+v err=%v", got, err)
			}
			afterValues, valuesErr := db.GameXGStates(ctx, "2031", "Checks")
			afterVenue, venueErr := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
			afterRuns := xgRunHistory(t, ctx, db, "2031", "Checks")
			afterAudits, auditsErr := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
			afterStates, statesErr := db.GameXGCheckStates(ctx, "2031", "Checks")
			afterFull, afterFullOK, fullErr := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2031", "Checks")
			if valuesErr != nil || venueErr != nil || auditsErr != nil || statesErr != nil || fullErr != nil || beforeFullOK != afterFullOK || !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeVenue, afterVenue) || !reflect.DeepEqual(beforeRuns, afterRuns) || !reflect.DeepEqual(beforeAudits, afterAudits) || !reflect.DeepEqual(beforeStates, afterStates) || !reflect.DeepEqual(beforeFull, afterFull) {
				t.Fatal("rollback incomplete")
			}
		})
	}
}

func TestGameXGCheckReadContract(t *testing.T) {
	ctx, db, _, _ := checkedXGFixture(t)
	defer db.Close()
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 1)}, []GameXG{xgValue("one")}, xgCheckMetadata(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE game_xg_checks SET last_checked_at='2031-01-01T02:01:00+01:00',first_available_observed_at='2031-01-01T02:01:00+01:00',last_material_change_at='2031-01-01T02:01:00+01:00',next_due_at='2031-01-02T02:00:00+01:00' WHERE asa_game_id='one'`); err != nil {
		t.Fatal(err)
	}
	state, ok, err := db.GameXGCheckState(ctx, "one")
	if err != nil || !ok || state.LastCheckedAt.Location() != time.UTC || state.FirstAvailableObservedAt == nil || state.FirstAvailableObservedAt.Location() != time.UTC || state.LastMaterialChangeAt == nil || state.LastMaterialChangeAt.Location() != time.UTC || state.NextDueAt == nil || state.NextDueAt.Location() != time.UTC {
		t.Fatalf("UTC state=%+v,%v", state, err)
	}
	*state.NextDueAt = time.Time{}
	again, _, err := db.GameXGCheckState(ctx, "one")
	if err != nil || again.NextDueAt == nil || again.NextDueAt.IsZero() {
		t.Fatalf("non-defensive pointer=%+v,%v", again, err)
	}
	updates := []struct {
		column    string
		malformed string
		reset     string
	}{
		{"last_checked_at", `UPDATE game_xg_checks SET last_checked_at='bad' WHERE asa_game_id='one'`, `UPDATE game_xg_checks SET last_checked_at='2031-01-01T00:00:00Z' WHERE asa_game_id='one'`},
		{"first_available_observed_at", `UPDATE game_xg_checks SET first_available_observed_at='bad' WHERE asa_game_id='one'`, `UPDATE game_xg_checks SET first_available_observed_at='2031-01-01T00:00:00Z' WHERE asa_game_id='one'`},
		{"last_material_change_at", `UPDATE game_xg_checks SET last_material_change_at='bad' WHERE asa_game_id='one'`, `UPDATE game_xg_checks SET last_material_change_at='2031-01-01T00:00:00Z' WHERE asa_game_id='one'`},
		{"next_due_at", `UPDATE game_xg_checks SET next_due_at='bad' WHERE asa_game_id='one'`, `UPDATE game_xg_checks SET next_due_at='2031-01-01T00:00:00Z' WHERE asa_game_id='one'`},
	}
	for _, update := range updates {
		if _, err := db.db.ExecContext(ctx, update.malformed); err != nil {
			t.Fatal(err)
		}
		if _, _, err := db.GameXGCheckState(ctx, "one"); err == nil || !strings.Contains(err.Error(), "parse xG check") {
			t.Fatalf("malformed %s accepted: %v", update.column, err)
		}
		if _, err := db.db.ExecContext(ctx, update.reset); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpsertCheckedXGOwnsInputsOrdersValuesAndPreservesReadyVenue(t *testing.T) {
	ctx, db, _, _ := checkedXGFixture(t)
	defer db.Close()
	if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{xgValue("one")}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	beforeVenue, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	requests := []CheckedXGRequest{xgCheckRequest("two", 2), xgCheckRequest("one", 2)}
	duePointer, dueValue := requests[1].NextDueAt, *requests[1].NextDueAt
	values := []GameXG{xgValue("one")}
	values[0].HomeXG.Float64 = 2
	beforeValue := values[0]
	result, err := db.UpsertCheckedXG(ctx, "2031", "Checks", requests, values, xgCheckMetadata(2))
	afterVenue, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	if err != nil || requests[1].NextDueAt != duePointer || !requests[1].NextDueAt.Equal(dueValue) || values[0] != beforeValue || len(result.Values) != 2 || result.Values[0].GameID != "one" || result.Values[1].GameID != "two" || result.Audit.RowsUpdated != 1 || !result.Audit.DownstreamInputsChanged || len(beforeVenue) != 1 || len(afterVenue) != 1 || !afterVenue[0].XGReady || afterVenue[0].FixtureReady != beforeVenue[0].FixtureReady || afterVenue[0].Matches != beforeVenue[0].Matches || afterVenue[0].HomeGoals != beforeVenue[0].HomeGoals || afterVenue[0].AwayGoals != beforeVenue[0].AwayGoals || afterVenue[0].HomePoints != beforeVenue[0].HomePoints || afterVenue[0].AwayPoints != beforeVenue[0].AwayPoints || afterVenue[0].HomeXG != 2 {
		t.Fatalf("ownership/ready=%+v,%v before=%+v after=%+v", result, err, beforeVenue, afterVenue)
	}
	result.Values[0].HomeXG.Float64 = 99
	state, _, err := db.GameXGState(ctx, "one")
	if err != nil || state.HomeXG.Float64 == 99 {
		t.Fatalf("result mutation leaked=%+v,%v", state, err)
	}
}

func TestMigrationTwelveMinimalFixtureSkipsBackfill(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations VALUES (11,'2031-01-01T00:00:00Z')`,
	} {
		if _, err := legacy.ExecContext(ctx, q); err != nil {
			if closeErr := legacy.Close(); closeErr != nil {
				t.Errorf("close legacy database: %v", closeErr)
			}
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
	var n int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_xg_checks`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("minimal backfill=%d,%v", n, err)
	}
}

func TestUpsertCheckedXGPartialOmissionDoesNotFabricateOrTouchUnrequested(t *testing.T) {
	ctx, db, games, _ := checkedXGFixture(t)
	defer db.Close()
	three := cachedGame("three", "2031", "Checks", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	three.KickoffUTC = "2031-01-03T00:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2031", "Checks", append(games, three), nil, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2030, 1, 1, 1, 0, 0, 0, time.UTC), FinishedAt: time.Date(2030, 1, 1, 1, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{xgValue("one")}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM game_xg WHERE asa_game_id='three'`); err != nil {
		t.Fatal(err)
	}
	beforeOne, _, _ := db.GameXGState(ctx, "one")
	beforeTwo, _, _ := db.GameXGState(ctx, "two")
	beforeThree, threeOK, _ := db.GameXGState(ctx, "three")
	beforeUnrequested, unrequestedOK, _ := db.GameXGCheckState(ctx, "one")
	result, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("two", 2), xgCheckRequest("three", 2)}, []GameXG{}, xgCheckMetadata(2))
	afterOne, _, _ := db.GameXGState(ctx, "one")
	afterTwo, _, _ := db.GameXGState(ctx, "two")
	afterThree, afterThreeOK, _ := db.GameXGState(ctx, "three")
	afterUnrequested, afterUnrequestedOK, _ := db.GameXGCheckState(ctx, "one")
	states, _ := db.GameXGCheckStates(ctx, "2031", "Checks")
	if err != nil || result.Audit.RequestedRows != 2 || result.Audit.ReturnedRows != 0 || result.Audit.RowsInserted != 0 || result.Audit.RowsUpdated != 0 || result.Audit.RowsUnchanged != 0 || result.Audit.RowsDeleted != 0 || !reflect.DeepEqual(beforeOne, afterOne) || !reflect.DeepEqual(beforeTwo, afterTwo) || threeOK || afterThreeOK || !reflect.DeepEqual(beforeThree, afterThree) || unrequestedOK != afterUnrequestedOK || !reflect.DeepEqual(beforeUnrequested, afterUnrequested) || len(states) != 3 || states[1].GameID != "three" || states[1].NextDueAt == nil || states[2].GameID != "two" || states[2].NextDueAt == nil {
		t.Fatalf("partial omission=%+v,%v states=%+v", result, err, states)
	}
}

func TestMigrationTwelveSchemaShapeAndIdempotence(t *testing.T) {
	ctx, db, _, _ := checkedXGFixture(t)
	defer db.Close()
	rows, err := db.db.QueryContext(ctx, `PRAGMA table_info(game_xg_checks)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if !reflect.DeepEqual(columns, []string{"asa_game_id", "last_checked_at", "first_available_observed_at", "last_material_change_at", "next_due_at"}) {
		t.Fatalf("columns=%v", columns)
	}
	var dueIndex, cascade int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='game_xg_checks_due_idx'`).Scan(&dueIndex); err != nil || dueIndex != 1 {
		t.Fatalf("due index=%d,%v", dueIndex, err)
	}
	fks, err := db.db.QueryContext(ctx, `PRAGMA foreign_key_list(game_xg_checks)`)
	if err != nil {
		t.Fatal(err)
	}
	defer fks.Close()
	for fks.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := fks.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		if table == "games" && from == "asa_game_id" && to == "asa_game_id" && onDelete == "CASCADE" {
			cascade++
		}
	}
	if cascade != 1 {
		t.Fatal("missing games cascade FK")
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertCheckedXGPromotionMaterialAndDueMatrix(t *testing.T) {
	ctx, db, _, _ := checkedXGFixture(t)
	defer db.Close()
	if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	promoted, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 1)}, []GameXG{xgValue("one")}, xgCheckMetadata(1))
	if err != nil || promoted.Audit.RowsUpdated != 1 || !promoted.Audit.DownstreamInputsChanged {
		t.Fatalf("promotion=%+v,%v", promoted, err)
	}
	numeric := xgValue("one")
	numeric.HomeXG = sql.NullFloat64{Float64: 2, Valid: true}
	updated, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 2)}, []GameXG{numeric}, xgCheckMetadata(2))
	if err != nil || updated.Audit.RowsUpdated != 1 || !updated.Audit.DownstreamInputsChanged {
		t.Fatalf("numeric=%+v,%v", updated, err)
	}
	points := numeric
	points.HomeXPoints, points.AwayXPoints = sql.NullFloat64{Float64: 2, Valid: true}, sql.NullFloat64{Float64: 1, Valid: true}
	pointResult, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 3)}, []GameXG{points}, xgCheckMetadata(3))
	if err != nil || pointResult.Audit.RowsUpdated != 1 || !pointResult.Audit.DownstreamInputsChanged {
		t.Fatalf("points=%+v,%v", pointResult, err)
	}
	for _, hour := range []int{3, 2} {
		conflict := points
		conflict.HomeXG = sql.NullFloat64{Float64: 9, Valid: true}
		result, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", hour)}, []GameXG{conflict}, xgCheckMetadata(hour))
		if err != nil || result.Audit.RowsUnchanged != 1 || result.Audit.DownstreamInputsChanged {
			t.Fatalf("conflict hour %d=%+v,%v", hour, result, err)
		}
	}
	nilDue := CheckedXGRequest{GameID: "one"}
	noChange, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{nilDue}, []GameXG{points}, xgCheckMetadata(4))
	state, ok, stateErr := db.GameXGCheckState(ctx, "one")
	stored, storedOK, storedErr := db.GameXGState(ctx, "one")
	if err != nil || noChange.Audit.RowsUnchanged != 1 || noChange.Audit.DownstreamInputsChanged || stateErr != nil || !ok || state.NextDueAt != nil || !state.LastCheckedAt.Equal(xgCheckMetadata(4).FinishedAt) || state.LastMaterialChangeAt == nil || !state.LastMaterialChangeAt.Equal(xgCheckMetadata(3).FinishedAt) || storedErr != nil || !storedOK || stored.HomeXG.Float64 != 2 || !stored.LastCheckedAt.Equal(xgCheckMetadata(4).FinishedAt) {
		t.Fatalf("due/clock=%+v,%v state=%+v,%v stored=%+v,%v", noChange, err, state, stateErr, stored, storedErr)
	}
}

func TestParticipantChangeDeletesCheckOnlyStateAndLegacyLeavesChecksAlone(t *testing.T) {
	ctx, db, games, _ := checkedXGFixture(t)
	defer db.Close()
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 1)}, []GameXG{}, xgCheckMetadata(1)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GameXGState(ctx, "one"); err != nil || ok {
		t.Fatalf("omission fabricated value=%t,%v", ok, err)
	}
	if _, ok, err := db.GameXGCheckState(ctx, "one"); err != nil || !ok {
		t.Fatalf("missing check-only state=%t,%v", ok, err)
	}
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "charlie", Name: "C"}}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 2, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 2, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	changed := games[0]
	changed.HomeTeamID = "charlie"
	changed.LastUpdatedUTC = "2031-01-01T03:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2031", "Checks", []Game{changed, games[1]}, nil, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 3, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 3, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GameXGCheckState(ctx, "one"); err != nil || ok {
		t.Fatalf("participant change left check-only state=%t,%v", ok, err)
	}
	legacy := GameXG{GameID: "two", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1, Valid: true}}
	if _, err := db.ReplaceGameXG(ctx, "2031", "Checks", []Game{games[1]}, []GameXG{legacy}, time.Date(2031, 1, 1, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GameXGCheckState(ctx, "two"); err != nil || ok {
		t.Fatalf("legacy write created xG check state=%t,%v", ok, err)
	}
}

func TestReplaceStageXGRollsBackXGCheckMaintenance(t *testing.T) {
	for _, tc := range []struct {
		name, event string
		seed        bool
	}{
		{"check insert", "INSERT", false},
		{"check update", "UPDATE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, _, _ := checkedXGFixture(t)
			defer db.Close()
			if tc.seed {
				if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{xgValue("one")}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
					t.Fatal(err)
				}
			}
			beforeValues, _ := db.GameXGStates(ctx, "2031", "Checks")
			beforeStates, _ := db.GameXGCheckStates(ctx, "2031", "Checks")
			beforeRuns := xgRunHistory(t, ctx, db, "2031", "Checks")
			beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
			triggerStatements := map[string]string{
				"INSERT": "CREATE TRIGGER abort_full_xg_check BEFORE INSERT ON game_xg_checks BEGIN SELECT RAISE(ABORT,'stop'); END",
				"UPDATE": "CREATE TRIGGER abort_full_xg_check BEFORE UPDATE ON game_xg_checks BEGIN SELECT RAISE(ABORT,'stop'); END",
			}
			if _, err := db.db.ExecContext(ctx, triggerStatements[tc.event]); err != nil {
				t.Fatal(err)
			}
			result, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{xgValue("one")}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 2, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 2, 1, 0, 0, time.UTC)})
			afterValues, valuesErr := db.GameXGStates(ctx, "2031", "Checks")
			afterStates, statesErr := db.GameXGCheckStates(ctx, "2031", "Checks")
			afterRuns := xgRunHistory(t, ctx, db, "2031", "Checks")
			afterAudits, auditsErr := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
			if err == nil || result.Audit.ID != 0 || result.XGRun != nil || result.Values != nil || valuesErr != nil || statesErr != nil || auditsErr != nil || !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeStates, afterStates) || !reflect.DeepEqual(beforeRuns, afterRuns) || !reflect.DeepEqual(beforeAudits, afterAudits) {
				t.Fatalf("full rollback=%+v,%v", result, err)
			}
		})
	}
}

func TestUpsertCheckedXGRollsBackValueAndCheckInsert(t *testing.T) {
	for _, target := range []string{"game_xg", "game_xg_checks"} {
		t.Run(target, func(t *testing.T) {
			ctx, db, _, _ := checkedXGFixture(t)
			defer db.Close()
			beforeValues, _ := db.GameXGStates(ctx, "2031", "Checks")
			beforeStates, _ := db.GameXGCheckStates(ctx, "2031", "Checks")
			beforeRuns := xgRunHistory(t, ctx, db, "2031", "Checks")
			beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
			triggerStatements := map[string]string{
				"game_xg":        "CREATE TRIGGER abort_checked_xg_insert BEFORE INSERT ON game_xg BEGIN SELECT RAISE(ABORT,'stop'); END",
				"game_xg_checks": "CREATE TRIGGER abort_checked_xg_insert BEFORE INSERT ON game_xg_checks BEGIN SELECT RAISE(ABORT,'stop'); END",
			}
			if _, err := db.db.ExecContext(ctx, triggerStatements[target]); err != nil {
				t.Fatal(err)
			}
			result, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 1)}, []GameXG{xgValue("one")}, xgCheckMetadata(1))
			afterValues, valuesErr := db.GameXGStates(ctx, "2031", "Checks")
			afterStates, statesErr := db.GameXGCheckStates(ctx, "2031", "Checks")
			afterRuns := xgRunHistory(t, ctx, db, "2031", "Checks")
			afterAudits, auditsErr := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
			if err == nil || result.Audit.ID != 0 || result.XGRun != nil || result.Values != nil || valuesErr != nil || statesErr != nil || auditsErr != nil || !reflect.DeepEqual(beforeValues, afterValues) || !reflect.DeepEqual(beforeStates, afterStates) || !reflect.DeepEqual(beforeRuns, afterRuns) || !reflect.DeepEqual(beforeAudits, afterAudits) {
				t.Fatalf("insert rollback=%+v,%v", result, err)
			}
		})
	}
}

func TestUpsertCheckedXGFinalValidationAndLineageCases(t *testing.T) {
	ctx, db, games, _ := checkedXGFixture(t)
	defer db.Close()
	pre := cachedGame("pre", "2031", "Checks", "PreMatch", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	abandoned := cachedGame("abandoned", "2031", "Checks", "Abandoned", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	if _, err := db.ReplaceGameInventory(ctx, "2031", "Checks", append(games, pre, abandoned), nil, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	other := cachedGame("other", "2031", "Other", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true})
	if _, err := db.ReplaceSeason(ctx, "2031", "Other", []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}, []Game{other}, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		req  []CheckedXGRequest
		out  []GameXG
		meta TargetedRefreshMetadata
	}{
		{"blank request", []CheckedXGRequest{{GameID: ""}}, []GameXG{}, xgCheckMetadata(1)},
		{"duplicate returned", []CheckedXGRequest{{GameID: "one"}}, []GameXG{xgValue("one"), xgValue("one")}, xgCheckMetadata(1)},
		{"padded returned", []CheckedXGRequest{{GameID: "one"}}, []GameXG{func() GameXG { v := xgValue("one"); v.GameID = " one"; return v }()}, xgCheckMetadata(1)},
		{"nan", []CheckedXGRequest{{GameID: "one"}}, []GameXG{func() GameXG { v := xgValue("one"); v.HomeXG.Float64 = math.NaN(); return v }()}, xgCheckMetadata(1)},
		{"metadata order", []CheckedXGRequest{{GameID: "one"}}, []GameXG{}, TargetedRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 2, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 1, 0, 0, 0, time.UTC)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", tc.req, tc.out, tc.meta); err == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{{GameID: "other"}, {GameID: "pre"}, {GameID: "abandoned"}}, []GameXG{}, xgCheckMetadata(1)); err == nil || !strings.Contains(err.Error(), "[abandoned other pre]") {
		t.Fatalf("eligibility error=%v", err)
	}
	result, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("two", 2), xgCheckRequest("one", 2)}, []GameXG{xgValue("one")}, xgCheckMetadata(2))
	audits, _ := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2031", "Checks")
	runs := xgRunHistory(t, ctx, db, "2031", "Checks")
	if err != nil || result.Audit.RequestedRows != 2 || result.Audit.ReturnedRows != 1 || result.Audit.RowsInserted != 1 || result.Audit.RowsUpdated != 0 || result.Audit.RowsUnchanged != 0 || result.Audit.RowsDeleted != 0 || len(audits) == 0 || !reflect.DeepEqual(audits[0], result.Audit) || len(runs) == 0 || runs[len(runs)-1].RowsSeen != 1 || runs[len(runs)-1].AvailableGames != 1 || runs[len(runs)-1].UnavailableGames != 0 || runs[len(runs)-1].RowsInserted != 1 {
		t.Fatalf("partial lineage=%+v,%v audits=%+v runs=%+v", result, err, audits, runs)
	}
}

func TestUpsertCheckedXGVenueAndStateFinalCases(t *testing.T) {
	ctx, db, games, _ := checkedXGFixture(t)
	defer db.Close()
	if _, err := db.db.ExecContext(ctx, `DELETE FROM venue_summaries WHERE season='2031' AND stage='Checks'`); err != nil {
		t.Fatal(err)
	}
	value := xgValue("one")
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 1)}, []GameXG{value}, xgCheckMetadata(1)); err != nil {
		t.Fatal(err)
	}
	missingCreated, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	if len(missingCreated) != 1 || missingCreated[0].XGReady || missingCreated[0].FixtureReady || missingCreated[0].Matches != 0 {
		t.Fatalf("missing venue=%+v", missingCreated)
	}
	before := missingCreated[0]
	for _, v := range []GameXG{value, func() GameXG { v := value; v.RawJSON = `{"raw":true}`; return v }()} {
		if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 2)}, []GameXG{v}, xgCheckMetadata(2)); err != nil {
			t.Fatal(err)
		}
		got, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
		if !reflect.DeepEqual(before, got[0]) {
			t.Fatalf("nonmaterial changed venue before=%+v after=%+v", before, got[0])
		}
	}
	points := value
	points.HomeXPoints, points.AwayXPoints = sql.NullFloat64{Float64: 2, Valid: true}, sql.NullFloat64{Float64: 1, Valid: true}
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("one", 3)}, []GameXG{points}, xgCheckMetadata(3)); err != nil {
		t.Fatal(err)
	}
	afterPoints, _ := db.VenueSummaries(ctx, []string{"2031"}, "Checks")
	if len(afterPoints) != 1 || afterPoints[0].XGReady || afterPoints[0].HomeXG != before.HomeXG || afterPoints[0].AwayXG != before.AwayXG {
		t.Fatalf("xPoints venue=%+v", afterPoints)
	}
	if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{xgCheckRequest("two", 1)}, []GameXG{}, xgCheckMetadata(1)); err != nil {
		t.Fatal(err)
	}
	state, _, _ := db.GameXGCheckState(ctx, "two")
	statusOnly := games[1]
	statusOnly.HomeScore.Int64 = 2
	statusOnly.LastUpdatedUTC = "2031-01-01T04:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2031", "Checks", []Game{games[0], statusOnly}, nil, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2031, 1, 1, 4, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 1, 1, 4, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	afterStatus, ok, err := db.GameXGCheckState(ctx, "two")
	if err != nil || !ok || !reflect.DeepEqual(state, afterStatus) {
		t.Fatalf("status-only lost xG state before=%+v after=%+v err=%v", state, afterStatus, err)
	}
}
