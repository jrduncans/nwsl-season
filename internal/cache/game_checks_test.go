package cache

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func checkMetadata(hour int) TargetedRefreshMetadata {
	return TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: time.Date(2030, 1, 1, hour, 0, 0, 0, time.UTC), FinishedAt: time.Date(2030, 1, 1, hour, 1, 0, 0, time.UTC)}
}
func checkRequest(id string, hour int) CheckedGameRequest {
	due := time.Date(2030, 1, 1, hour, 2, 0, 0, time.UTC)
	return CheckedGameRequest{ASAID: id, NextDueAt: &due}
}

func TestUpsertCheckedGamesOmissionStateAuditAndLineage(t *testing.T) {
	db, ctx := inventoryDB(t)
	one := inventoryGame("one", "PreMatch", 0, 0)
	two := inventoryGame("two", "PreMatch", 0, 0)
	two.KickoffUTC = "2030-01-02T12:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{one, two}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	fullStateBefore, found, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2030", "Example")
	if err != nil || !found {
		t.Fatalf("full state before targeted check = %+v, %t, %v", fullStateBefore, found, err)
	}
	result, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("two", 2), checkRequest("one", 2)}, []Game{}, checkMetadata(2))
	if err != nil {
		t.Fatal(err)
	}
	if result.SyncRun == nil || len(result.Games) != 2 || result.Audit.ID == 0 || result.Audit.Mode != SourceRefreshTargeted || result.Audit.Outcome != SourceRefreshSuccess || result.Audit.RequestedRows != 2 || result.Audit.ReturnedRows != 0 || result.Audit.RowsInserted != 0 || result.Audit.RowsUpdated != 0 || result.Audit.RowsUnchanged != 0 || result.Audit.RowsDeleted != 0 || result.Audit.DownstreamInputsChanged {
		t.Fatalf("result=%+v", result)
	}
	wantSnapshot, err := FixtureSnapshotID(result.Teams, result.Games)
	if err != nil {
		t.Fatal(err)
	}
	if result.SyncRun.FixtureSnapshotID != wantSnapshot || result.SyncRun.TeamsUpserted != 0 || result.SyncRun.TeamsInserted != 0 || result.SyncRun.TeamsUpdated != 0 || result.SyncRun.TeamsUnchanged != 0 || result.SyncRun.GamesUpserted != 0 || result.SyncRun.GamesSeen != 0 || result.SyncRun.GamesInserted != 0 || result.SyncRun.GamesUpdated != 0 || result.SyncRun.GamesUnchanged != 0 || result.SyncRun.GamesDeleted != 0 {
		t.Fatalf("legacy lineage=%+v, want complete snapshot %q and zero returned-row counts", result.SyncRun, wantSnapshot)
	}
	audits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
	if err != nil || len(audits) < 2 || !reflect.DeepEqual(audits[0], result.Audit) {
		t.Fatalf("targeted audits=%+v, result=%+v, %v", audits, result.Audit, err)
	}
	states, err := db.GameResultCheckStates(ctx, "2030", "Example")
	if err != nil || len(states) != 2 || states[0].GameID != "one" || states[0].NextDueAt == nil {
		t.Fatalf("states=%+v,%v", states, err)
	}
	fullStateAfter, found, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2030", "Example")
	if err != nil || !found || !reflect.DeepEqual(fullStateBefore, fullStateAfter) {
		t.Fatalf("targeted changed full state: before=%+v after=%+v found=%t err=%v", fullStateBefore, fullStateAfter, found, err)
	}
}

func TestUpsertCheckedGamesPreferenceAndMonotonicState(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	raw := game
	raw.RawJSON = `{"x":1}`
	raw.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 3)}, []Game{raw}, checkMetadata(3)); err != nil {
		t.Fatal(err)
	}
	state, ok, err := db.GameResultCheckState(ctx, "one")
	if err != nil || !ok || state.LastMaterialChangeAt == nil {
		t.Fatalf("raw state=%+v,%v", state, err)
	}
	due := *state.NextDueAt
	stale := raw
	stale.Status = "FullTime"
	stale.HomeScore = sql.NullInt64{Int64: 1, Valid: true}
	stale.AwayScore = sql.NullInt64{Valid: true}
	stale.LastUpdatedUTC = "2030-01-01T11:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{{ASAID: "one", NextDueAt: nil}}, []Game{stale}, checkMetadata(2)); err != nil {
		t.Fatal(err)
	}
	state, _, _ = db.GameResultCheckState(ctx, "one")
	if !state.LastCheckedAt.Equal(time.Date(2030, 1, 1, 3, 1, 0, 0, time.UTC)) || !state.NextDueAt.Equal(due) {
		t.Fatalf("regressed state=%+v", state)
	}
	terminal := stale
	terminal.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 4)}, []Game{terminal}, checkMetadata(4)); err != nil {
		t.Fatal(err)
	}
	state, _, _ = db.GameResultCheckState(ctx, "one")
	if state.FirstTerminalObservedAt == nil || state.LastMaterialChangeAt == nil {
		t.Fatalf("terminal state=%+v", state)
	}
}

func TestTargetedPreferenceCountersAndIndependentStateTimes(t *testing.T) {
	db, ctx := inventoryDB(t)
	g := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{g}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	due := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	req := []CheckedGameRequest{{ASAID: "one", NextDueAt: &due}}
	originalDue := due
	returned := []Game{g}
	if result, err := db.UpsertCheckedGames(ctx, "2030", "Example", req, returned, checkMetadata(2)); err != nil || result.Audit.RowsUnchanged != 1 || result.Audit.RowsUpdated != 0 || result.Audit.DownstreamInputsChanged {
		t.Fatalf("identical=%+v,%v", result, err)
	}
	if !due.Equal(originalDue) || req[0].NextDueAt != &due || returned[0] != g {
		t.Fatal("caller input mutated")
	}
	stale := g
	stale.RawJSON = `{"stale":1}`
	stale.LastUpdatedUTC = "2030-01-01T10:00:00Z"
	if result, err := db.UpsertCheckedGames(ctx, "2030", "Example", req, []Game{stale}, checkMetadata(3)); err != nil || result.Audit.RowsUnchanged != 1 {
		t.Fatalf("stale=%+v,%v", result, err)
	}
	equal := g
	equal.RawJSON = `{"equal":1}`
	if result, err := db.UpsertCheckedGames(ctx, "2030", "Example", req, []Game{equal}, checkMetadata(4)); err != nil || result.Audit.RowsUnchanged != 1 {
		t.Fatalf("equal=%+v,%v", result, err)
	}
	terminal := g
	terminal.Status = "FullTime"
	terminal.HomeScore = sql.NullInt64{Int64: 1, Valid: true}
	terminal.AwayScore = sql.NullInt64{Valid: true}
	terminal.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	if result, err := db.UpsertCheckedGames(ctx, "2030", "Example", req, []Game{terminal}, checkMetadata(5)); err != nil || result.Audit.RowsUpdated != 1 || !result.Audit.DownstreamInputsChanged {
		t.Fatalf("terminal=%+v,%v", result, err)
	}
	regress := terminal
	regress.Status = "PreMatch"
	regress.HomeScore = sql.NullInt64{}
	regress.AwayScore = sql.NullInt64{}
	regress.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	if result, err := db.UpsertCheckedGames(ctx, "2030", "Example", req, []Game{regress}, checkMetadata(6)); err != nil || result.Audit.RowsUnchanged != 1 {
		t.Fatalf("regress=%+v,%v", result, err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE games SET last_updated_utc='bad' WHERE asa_game_id='one'`); err != nil {
		t.Fatal(err)
	}
	newer := terminal
	newer.HomeScore.Int64 = 2
	newer.LastUpdatedUTC = "2030-01-01T14:00:00Z"
	if result, err := db.UpsertCheckedGames(ctx, "2030", "Example", req, []Game{newer}, checkMetadata(7)); err != nil || result.Audit.RowsUpdated != 1 {
		t.Fatalf("malformed=%+v,%v", result, err)
	}
	state, _, _ := db.GameResultCheckState(ctx, "one")
	material := *state.LastMaterialChangeAt
	raw := newer
	raw.RawJSON = `{"raw":1}`
	raw.LastUpdatedUTC = "2030-01-01T15:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", req, []Game{raw}, checkMetadata(8)); err != nil {
		t.Fatal(err)
	}
	state, _, _ = db.GameResultCheckState(ctx, "one")
	if !state.LastMaterialChangeAt.Equal(material) {
		t.Fatalf("raw changed material=%+v", state)
	}
	// A delayed terminal check can move first observation backward without regressing last checked.
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", req, []Game{terminal}, checkMetadata(1)); err != nil {
		t.Fatal(err)
	}
	state, _, _ = db.GameResultCheckState(ctx, "one")
	if state.FirstTerminalObservedAt == nil || !state.FirstTerminalObservedAt.Equal(checkMetadata(1).FinishedAt) || !state.LastCheckedAt.Equal(checkMetadata(8).FinishedAt) {
		t.Fatalf("independent state=%+v", state)
	}
}

func TestTargetedMaterialTimeCanAdvanceWithoutCheckDueRegression(t *testing.T) {
	db, ctx := inventoryDB(t)
	g := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{g}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	due := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{{ASAID: "one", NextDueAt: &due}}, []Game{}, checkMetadata(10)); err != nil {
		t.Fatal(err)
	}
	before, _, _ := db.GameResultCheckState(ctx, "one")
	material := g
	material.KickoffUTC = "2030-01-01T13:00:00Z"
	material.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{{ASAID: "one", NextDueAt: nil}}, []Game{material}, checkMetadata(5)); err != nil {
		t.Fatal(err)
	}
	after, _, _ := db.GameResultCheckState(ctx, "one")
	if !after.LastCheckedAt.Equal(before.LastCheckedAt) || !after.NextDueAt.Equal(due) || after.LastMaterialChangeAt == nil || !after.LastMaterialChangeAt.Equal(checkMetadata(5).FinishedAt) {
		t.Fatalf("independent material=%+v before=%+v", after, before)
	}
}

func TestUpsertCheckedGamesRejectsIdentityWithoutWrites(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	before, ok, err := db.GameResultCheckState(ctx, "one")
	if err != nil || !ok {
		t.Fatal(err)
	}
	bad := game
	bad.HomeTeamID = "bravo"
	bad.AwayTeamID = "alpha"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 2)}, []Game{bad}, checkMetadata(2)); err == nil {
		t.Fatal("participant mismatch accepted")
	}
	if state, ok, _ := db.GameResultCheckState(ctx, "one"); !ok || !reflect.DeepEqual(before, state) {
		t.Fatalf("failed write changed state=%+v", state)
	}
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("missing", 2)}, []Game{}, checkMetadata(2)); err == nil {
		t.Fatal("missing request accepted")
	}
}

func TestGameResultCheckReadDefensiveAndCascade(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 2)}, []Game{}, checkMetadata(2)); err != nil {
		t.Fatal(err)
	}
	state, ok, err := db.GameResultCheckState(ctx, "one")
	if err != nil || !ok {
		t.Fatal(err)
	}
	due := *state.NextDueAt
	state.NextDueAt = &time.Time{}
	again, _, _ := db.GameResultCheckState(ctx, "one")
	if !again.NextDueAt.Equal(due) {
		t.Fatal("pointer was not defensive")
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM games WHERE asa_game_id='one'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.GameResultCheckState(ctx, "one"); ok {
		t.Fatal("state did not cascade")
	}
	empty, err := db.GameResultCheckStates(ctx, "2030", "Example")
	if err != nil || empty == nil || !reflect.DeepEqual(empty, []GameResultCheckState{}) {
		t.Fatalf("empty=%+v,%v", empty, err)
	}
}

func TestUpsertCheckedGamesValidationAndPartialCounts(t *testing.T) {
	db, ctx := inventoryDB(t)
	one := inventoryGame("one", "PreMatch", 0, 0)
	two := inventoryGame("two", "PreMatch", 0, 0)
	two.KickoffUTC = "2030-01-02T12:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{one, two}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		req []CheckedGameRequest
		ret []Game
	}{{nil, []Game{}}, {[]CheckedGameRequest{}, []Game{}}, {[]CheckedGameRequest{checkRequest("one", 2), checkRequest("one", 2)}, []Game{}}, {[]CheckedGameRequest{checkRequest("one", 2)}, nil}, {[]CheckedGameRequest{checkRequest("one", 2)}, []Game{func() Game { g := one; g.ASAID = "two"; return g }()}}, {[]CheckedGameRequest{checkRequest("one", 2)}, []Game{func() Game { g := one; g.Stage = "bad"; return g }()}}}
	for _, tc := range cases {
		if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", tc.req, tc.ret, checkMetadata(2)); err == nil {
			t.Fatal("invalid targeted input accepted")
		}
	}
	raw := one
	raw.RawJSON = `{"p":1}`
	raw.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	result, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 3), checkRequest("two", 3)}, []Game{raw}, checkMetadata(3))
	if err != nil || result.Audit.RowsUpdated != 1 || result.Audit.RowsUnchanged != 0 || result.Audit.RequestedRows != 2 || result.Audit.ReturnedRows != 1 {
		t.Fatalf("partial=%+v,%v", result, err)
	}
	if s, ok, _ := db.GameResultCheckState(ctx, "two"); !ok || s.NextDueAt == nil {
		t.Fatalf("omitted state=%+v", s)
	}
}

func TestTargetedValidationFailuresLeaveAllPersistenceUntouched(t *testing.T) {
	db, ctx := inventoryDB(t)
	g := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{g}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	beforeGames, _ := db.seasonGames(ctx, "2030", "Example")
	beforeChecks, _ := db.GameResultCheckStates(ctx, "2030", "Example")
	beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
	beforeRun, _ := db.LastSuccess(ctx, "2030", "Example")
	beforeState, ok, _ := db.SourceResourceScopeState(ctx, SourceResourceGames, "2030", "Example")
	badDue := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	malformed := g
	malformed.KickoffUTC = "bad"
	other := g
	other.Season = "Else"
	cases := []struct {
		req []CheckedGameRequest
		ret []Game
	}{{[]CheckedGameRequest{{ASAID: " one"}}, []Game{}}, {[]CheckedGameRequest{{ASAID: ""}}, []Game{}}, {[]CheckedGameRequest{{ASAID: "one", NextDueAt: &badDue}}, []Game{}}, {[]CheckedGameRequest{{ASAID: "one"}}, []Game{g, g}}, {[]CheckedGameRequest{{ASAID: "one"}}, []Game{malformed}}, {[]CheckedGameRequest{{ASAID: "missing"}}, []Game{}}, {[]CheckedGameRequest{{ASAID: "one"}}, []Game{other}}}
	for _, tc := range cases {
		if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", tc.req, tc.ret, checkMetadata(2)); err == nil {
			t.Fatal("invalid targeted write accepted")
		}
	}
	afterGames, _ := db.seasonGames(ctx, "2030", "Example")
	afterChecks, _ := db.GameResultCheckStates(ctx, "2030", "Example")
	afterAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
	afterRun, _ := db.LastSuccess(ctx, "2030", "Example")
	afterState, afterOK, _ := db.SourceResourceScopeState(ctx, SourceResourceGames, "2030", "Example")
	if !reflect.DeepEqual(beforeGames, afterGames) || !reflect.DeepEqual(beforeChecks, afterChecks) || !reflect.DeepEqual(beforeAudits, afterAudits) || !reflect.DeepEqual(beforeRun, afterRun) || ok != afterOK || !reflect.DeepEqual(beforeState, afterState) {
		t.Fatal("validation mutated persistence")
	}
}

func TestGameResultCheckReadValidationScopeAndMalformedStorage(t *testing.T) {
	db, ctx := inventoryDB(t)
	if _, _, err := db.GameResultCheckState(ctx, " one"); err == nil {
		t.Fatal("padded ID accepted")
	}
	if _, err := db.GameResultCheckStates(ctx, "2030 ", "Example"); err == nil {
		t.Fatal("padded scope accepted")
	}
	g := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{g}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE game_result_checks SET last_checked_at='bad' WHERE asa_game_id='one'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GameResultCheckState(ctx, "one"); err == nil {
		t.Fatal("malformed timestamp accepted")
	}
	for _, column := range []string{"first_terminal_observed_at", "last_material_change_at", "next_due_at"} {
		if _, err := db.db.ExecContext(ctx, `UPDATE game_result_checks SET last_checked_at='2030-01-01T00:00:00Z', `+column+`='bad' WHERE asa_game_id='one'`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := db.GameResultCheckState(ctx, "one"); err == nil {
			t.Fatalf("malformed %s accepted", column)
		}
		if _, err := db.db.ExecContext(ctx, `UPDATE game_result_checks SET `+column+`=NULL WHERE asa_game_id='one'`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGameResultCheckReadsAreScopedSortedAndDefensive(t *testing.T) {
	db, ctx := inventoryDB(t)
	one := inventoryGame("z", "FullTime", 1, 0)
	two := inventoryGame("a", "PreMatch", 0, 0)
	two.KickoffUTC = "2030-01-02T00:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{one, two}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GameResultCheckState(ctx, "missing"); err != nil || ok {
		t.Fatal("missing state")
	}
	states, err := db.GameResultCheckStates(ctx, "2030", "Example")
	if err != nil || len(states) != 2 || states[0].GameID != "a" || states[1].GameID != "z" || states[1].LastMaterialChangeAt == nil {
		t.Fatalf("states=%+v,%v", states, err)
	}
	*states[1].LastMaterialChangeAt = time.Time{}
	again, _, _ := db.GameResultCheckState(ctx, "z")
	if again.LastMaterialChangeAt.IsZero() {
		t.Fatal("material pointer not defensive")
	}
	// Nullable timestamp pointers returned by a read are all owned by the result.
	if _, err := db.db.ExecContext(ctx, `UPDATE game_result_checks SET last_checked_at='2030-01-01T00:00:00-08:00',first_terminal_observed_at='2030-01-01T01:00:00-08:00',last_material_change_at='2030-01-01T02:00:00-08:00',next_due_at='2030-01-01T03:00:00-08:00' WHERE asa_game_id='z'`); err != nil {
		t.Fatal(err)
	}
	value, _, err := db.GameResultCheckState(ctx, "z")
	if err != nil {
		t.Fatal(err)
	}
	if value.LastCheckedAt.Location() != time.UTC || value.FirstTerminalObservedAt.Location() != time.UTC || value.LastMaterialChangeAt.Location() != time.UTC || value.NextDueAt.Location() != time.UTC || value.LastCheckedAt.Hour() != 8 || value.FirstTerminalObservedAt.Hour() != 9 || value.LastMaterialChangeAt.Hour() != 10 || value.NextDueAt.Hour() != 11 {
		t.Fatalf("read timestamps were not normalized to UTC: %+v", value)
	}
	*value.FirstTerminalObservedAt = time.Time{}
	*value.LastMaterialChangeAt = time.Time{}
	*value.NextDueAt = time.Time{}
	again, _, _ = db.GameResultCheckState(ctx, "z")
	if again.FirstTerminalObservedAt.IsZero() || again.LastMaterialChangeAt.IsZero() || again.NextDueAt.IsZero() {
		t.Fatal("nullable pointers not defensive")
	}
	if _, err := db.GameResultCheckStates(ctx, "2031", "Example"); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertCheckedGamesVenueAndRollback(t *testing.T) {
	for _, target := range []string{"game_result_checks", "venue_summaries", "sync_runs", "source_refresh_audits"} {
		t.Run(target, func(t *testing.T) {
			db, ctx := inventoryDB(t)
			game := inventoryGame("one", "FullTime", 1, 0)
			if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata()); err != nil {
				t.Fatal(err)
			}
			omitted := inventoryGame("two", "PreMatch", 0, 0)
			omitted.KickoffUTC = "2030-01-02T12:00:00Z"
			if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game, omitted}, nil, inventoryMetadata()); err != nil {
				t.Fatal(err)
			}
			// Exercise creation for the omitted ID, then a later failing step.
			if _, err := db.db.ExecContext(ctx, `DELETE FROM game_result_checks WHERE asa_game_id='two'`); err != nil {
				t.Fatal(err)
			}
			before, _ := db.seasonGames(ctx, "2030", "Example")
			beforeVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
			beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
			beforeRun, _ := db.LastSuccess(ctx, "2030", "Example")
			beforeChecks, _ := db.GameResultCheckStates(ctx, "2030", "Example")
			event := "INSERT"
			if target == "venue_summaries" || target == "game_result_checks" {
				event = "UPDATE"
			}
			if _, err := db.db.ExecContext(ctx, "CREATE TRIGGER abort_check BEFORE "+event+" ON "+target+" BEGIN SELECT RAISE(ABORT,'stop'); END"); err != nil {
				t.Fatal(err)
			}
			updated := game
			updated.HomeScore.Int64 = 2
			updated.LastUpdatedUTC = "2030-01-01T12:00:00Z"
			if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("two", 2), checkRequest("one", 2)}, []Game{updated}, checkMetadata(2)); err == nil {
				t.Fatal("rollback trigger accepted")
			}
			after, _ := db.seasonGames(ctx, "2030", "Example")
			afterVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
			afterAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
			afterRun, _ := db.LastSuccess(ctx, "2030", "Example")
			afterChecks, _ := db.GameResultCheckStates(ctx, "2030", "Example")
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeVenue, afterVenue) || !reflect.DeepEqual(beforeAudits, afterAudits) || !reflect.DeepEqual(beforeRun, afterRun) || !reflect.DeepEqual(beforeChecks, afterChecks) {
				t.Fatalf("rollback incomplete")
			}
		})
	}
}

func TestTargetedVenueFullTimeAndAbandonedPermutations(t *testing.T) {
	db, ctx := inventoryDB(t)
	g := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{g}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	full := g
	full.Status = "FullTime"
	full.HomeScore = sql.NullInt64{Int64: 1, Valid: true}
	full.AwayScore = sql.NullInt64{Valid: true}
	full.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 2)}, []Game{full}, checkMetadata(2)); err != nil {
		t.Fatal(err)
	}
	venue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if len(venue) != 1 || venue[0].XGReady {
		t.Fatalf("entry=%+v", venue)
	}
	if _, err := db.ReplaceGameXG(ctx, "2030", "Example", []Game{full}, []GameXG{{GameID: "one", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1, Valid: true}}}, time.Date(2030, 1, 1, 3, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	score := full
	score.HomeScore.Int64 = 2
	score.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 4)}, []Game{score}, checkMetadata(4)); err != nil {
		t.Fatal(err)
	}
	venue, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !venue[0].XGReady || venue[0].HomeGoals != 2 {
		t.Fatalf("score=%+v", venue[0])
	}
	abandoned := score
	abandoned.Status = "Abandoned"
	abandoned.HomeScore = sql.NullInt64{}
	abandoned.AwayScore = sql.NullInt64{}
	abandoned.LastUpdatedUTC = "2030-01-01T14:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 5)}, []Game{abandoned}, checkMetadata(5)); err != nil {
		t.Fatal(err)
	}
	venue, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if venue[0].XGReady || venue[0].XGMatches != 0 {
		t.Fatalf("exit=%+v", venue[0])
	}
	raw := abandoned
	raw.RawJSON = `{"raw":1}`
	raw.LastUpdatedUTC = "2030-01-01T15:00:00Z"
	before := venue[0]
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 6)}, []Game{raw}, checkMetadata(6)); err != nil {
		t.Fatal(err)
	}
	venue, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !reflect.DeepEqual(before, venue[0]) {
		t.Fatalf("abandoned raw=%+v", venue[0])
	}
}

func TestTargetedVenueOmissionNoopKickoffMatchdayAndAbandoned(t *testing.T) {
	db, ctx := inventoryDB(t)
	full := inventoryGame("one", "FullTime", 1, 0)
	pending := inventoryGame("two", "PreMatch", 0, 0)
	pending.KickoffUTC = "2030-01-02T00:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{full, pending}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceGameXG(ctx, "2030", "Example", []Game{full, pending}, []GameXG{{GameID: "one", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1, Valid: true}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 2)}, []Game{}, checkMetadata(2)); err != nil {
		t.Fatal(err)
	}
	after, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("omission changed venue")
	}
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 3)}, []Game{full}, checkMetadata(3)); err != nil {
		t.Fatal(err)
	}
	after, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("no-op changed venue")
	}
	kick := full
	kick.KickoffUTC = "2030-01-01T13:00:00Z"
	kick.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 4)}, []Game{kick}, checkMetadata(4)); err != nil {
		t.Fatal(err)
	}
	after, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !after[0].XGReady {
		t.Fatal("kickoff invalidated xg")
	}
	matchday := kick
	matchday.Matchday.Int64 = 2
	matchday.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	beforeXG := after[0]
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("one", 5)}, []Game{matchday}, checkMetadata(5)); err != nil {
		t.Fatal(err)
	}
	after, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !after[0].XGReady || after[0].XGMatches != beforeXG.XGMatches || after[0].HomeXG != beforeXG.HomeXG || after[0].AwayXG != beforeXG.AwayXG {
		t.Fatalf("matchday changed xg=%+v", after[0])
	}
	ab := pending
	ab.Status = "Abandoned"
	ab.LastUpdatedUTC = "2030-01-02T12:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("two", 6)}, []Game{ab}, checkMetadata(6)); err != nil {
		t.Fatal(err)
	}
	after, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !after[0].XGReady {
		t.Fatal("abandoned invalidated xg")
	}
}

func TestFullInventoryMaintainsGameCheckStateAndDue(t *testing.T) {
	db, ctx := inventoryDB(t)
	g := inventoryGame("one", "PreMatch", 0, 0)
	first, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{g}, nil, inventoryMetadata())
	if err != nil {
		t.Fatal(err)
	}
	state, ok, _ := db.GameResultCheckState(ctx, "one")
	if !ok || state.NextDueAt != nil || state.LastMaterialChangeAt == nil {
		t.Fatalf("initial=%+v", state)
	}
	due := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{{ASAID: "one", NextDueAt: &due}}, []Game{}, checkMetadata(8)); err != nil {
		t.Fatal(err)
	}
	g.RawJSON = `{"new":1}`
	g.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	fullMetadata := inventoryMetadata()
	fullMetadata.StartedAt = time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	fullMetadata.FinishedAt = time.Date(2030, 1, 1, 9, 1, 0, 0, time.UTC)
	fullDue := time.Date(2030, 1, 2, 9, 1, 0, 0, time.UTC)
	fullMetadata.NextFullDueAt = &fullDue
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{g}, nil, fullMetadata); err != nil {
		t.Fatal(err)
	}
	state, _, _ = db.GameResultCheckState(ctx, "one")
	if !state.LastCheckedAt.Equal(fullMetadata.FinishedAt) || state.NextDueAt == nil || !state.NextDueAt.Equal(due) {
		t.Fatalf("due not preserved=%+v", state)
	}
	terminal := g
	terminal.Status = "FullTime"
	terminal.HomeScore = sql.NullInt64{Int64: 2, Valid: true}
	terminal.AwayScore = sql.NullInt64{Int64: 1, Valid: true}
	terminal.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	terminalMetadata := fullMetadata
	terminalMetadata.StartedAt = time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	terminalMetadata.FinishedAt = time.Date(2030, 1, 1, 10, 1, 0, 0, time.UTC)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{terminal}, nil, terminalMetadata); err != nil {
		t.Fatal(err)
	}
	state, _, _ = db.GameResultCheckState(ctx, "one")
	if state.FirstTerminalObservedAt == nil || !state.FirstTerminalObservedAt.Equal(terminalMetadata.FinishedAt) || state.LastMaterialChangeAt == nil || !state.LastMaterialChangeAt.Equal(terminalMetadata.FinishedAt) || state.NextDueAt == nil || !state.NextDueAt.Equal(due) {
		t.Fatalf("terminal full observation state=%+v", state)
	}
	stale := terminal
	stale.Status = "PreMatch"
	stale.HomeScore = sql.NullInt64{}
	stale.AwayScore = sql.NullInt64{}
	stale.LastUpdatedUTC = "2030-01-01T14:00:00Z"
	staleMetadata := terminalMetadata
	staleMetadata.StartedAt = time.Date(2030, 1, 1, 11, 0, 0, 0, time.UTC)
	staleMetadata.FinishedAt = time.Date(2030, 1, 1, 11, 1, 0, 0, time.UTC)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{stale}, nil, staleMetadata); err != nil {
		t.Fatal(err)
	}
	state, _, _ = db.GameResultCheckState(ctx, "one")
	stored, _ := db.seasonGames(ctx, "2030", "Example")
	if len(stored) != 1 || stored[0].Status != "FullTime" || !state.LastCheckedAt.Equal(staleMetadata.FinishedAt) || !state.FirstTerminalObservedAt.Equal(terminalMetadata.FinishedAt) || !state.LastMaterialChangeAt.Equal(terminalMetadata.FinishedAt) || state.NextDueAt == nil || !state.NextDueAt.Equal(due) {
		t.Fatalf("stale full observation regressed fixture/state: games=%+v state=%+v", stored, state)
	}
	second := inventoryGame("two", "PreMatch", 0, 0)
	second.KickoffUTC = "2030-01-02T00:00:00Z"
	insertMetadata := staleMetadata
	insertMetadata.StartedAt = time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	insertMetadata.FinishedAt = time.Date(2030, 1, 1, 12, 1, 0, 0, time.UTC)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{terminal, second}, nil, insertMetadata); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.GameResultCheckState(ctx, "two"); !ok {
		t.Fatal("full insert did not create state")
	}
	deleteMetadata := insertMetadata
	deleteMetadata.StartedAt = time.Date(2030, 1, 1, 13, 0, 0, 0, time.UTC)
	deleteMetadata.FinishedAt = time.Date(2030, 1, 1, 13, 1, 0, 0, time.UTC)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{terminal}, nil, deleteMetadata); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.GameResultCheckState(ctx, "two"); ok {
		t.Fatal("authoritative deletion did not cascade state")
	}
	empty, err := db.ReplaceGameInventory(ctx, "2032", "Empty", []Game{}, nil, fullMetadata)
	if err != nil || empty.SyncRun != nil {
		t.Fatalf("empty discovery=%+v,%v", empty, err)
	}
	if states, err := db.GameResultCheckStates(ctx, "2032", "Empty"); err != nil || len(states) != 0 {
		t.Fatalf("empty state=%+v,%v", states, err)
	}
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{}, nil, inventoryMetadata()); err == nil {
		t.Fatal("populated empty")
	}
	_ = first
}

func TestMigrationElevenBackfillsRealV10AndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2029, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	g := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceSeason(ctx, "2030", "Example", []Team{{ASAID: "alpha", Name: "A"}, {ASAID: "bravo", Name: "B"}}, []Game{g}, time.Date(2029, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ finished, outcome, summary string }{{"2030-01-03T00:00:00Z", "success", ""}, {"2030-01-04T00:00:00Z", "failure", "failed"}} {
		if _, err := db.db.ExecContext(ctx, `INSERT INTO sync_runs(started_at,finished_at,season,stage,outcome,error_summary,fixture_snapshot_id,teams_upserted,games_upserted,games_deleted,games_seen,teams_inserted,teams_updated,teams_unchanged,games_inserted,games_updated,games_unchanged) VALUES ('2030-01-01T00:00:00Z',?,'2030','Example',?,?,'',0,0,0,0,0,0,0,0,0,0)`, row.finished, row.outcome, row.summary); err != nil {
			t.Fatal(err)
		}
	}
	wantRun, err := db.LastSuccess(ctx, "2030", "Example")
	if err != nil || wantRun == nil {
		t.Fatal(err)
	}
	want := time.Date(2030, 1, 3, 0, 0, 0, 0, time.UTC)
	_ = wantRun
	db.Close()
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DELETE FROM schema_migrations WHERE version=13", "DROP INDEX game_xg_checks_due_idx", "DROP TABLE game_xg_checks", "DELETE FROM schema_migrations WHERE version=12", "DROP INDEX game_result_checks_due_idx", "DROP TABLE game_result_checks", "DELETE FROM schema_migrations WHERE version=11"} {
		if _, err := legacy.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	legacy.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	state, ok, err := db.GameResultCheckState(ctx, "one")
	if err != nil || !ok || state.FirstTerminalObservedAt != nil || state.LastMaterialChangeAt != nil || state.NextDueAt != nil {
		t.Fatalf("backfill=%+v,%v", state, err)
	}
	if !state.LastCheckedAt.Equal(want) {
		t.Fatalf("checked=%v want %v", state.LastCheckedAt, want)
	}
	db.Close()
	if db, err = Open(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("version=%d,%v", version, err)
	}
	var index int
	_ = db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='game_result_checks_due_idx'`).Scan(&index)
	if index != 1 {
		t.Fatal("due index missing")
	}
	cols := map[string]bool{}
	rows, err := db.db.QueryContext(ctx, `PRAGMA table_info(game_result_checks)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	rows.Close()
	for _, name := range []string{"asa_game_id", "last_checked_at", "first_terminal_observed_at", "last_material_change_at", "next_due_at"} {
		if !cols[name] {
			t.Fatalf("missing column %s", name)
		}
	}
	fks, err := db.db.QueryContext(ctx, `PRAGMA foreign_key_list(game_result_checks)`)
	if err != nil {
		t.Fatal(err)
	}
	defer fks.Close()
	var cascade bool
	for fks.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := fks.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		cascade = cascade || table == "games" && from == "asa_game_id" && to == "asa_game_id" && onDelete == "CASCADE"
	}
	if !cascade {
		t.Fatal("result check FK cascade missing")
	}
	if _, ok, _ := db.GameResultCheckState(ctx, "one"); !ok {
		t.Fatal("reopen lost state")
	}
}

func TestMigrationElevenToleratesMinimalV10Fixture(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/minimal.sqlite"
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)", "INSERT INTO schema_migrations VALUES (10,'2030-01-01T00:00:00Z')"} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_result_checks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("minimal fixture fabricated state")
	}
}
