package cache

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestUpsertTeamsRejectsInvalidInputBeforeWriting(t *testing.T) {
	ctx := context.Background()
	finished := teamsTestTime(12)
	valid := teamsTestMetadata(finished)
	for _, test := range []struct {
		name     string
		teams    []Team
		metadata FullRefreshMetadata
	}{
		{name: "nil", teams: nil, metadata: valid},
		{name: "empty", teams: []Team{}, metadata: valid},
		{name: "blank id", teams: []Team{{ASAID: " "}}, metadata: valid},
		{name: "padded id", teams: []Team{{ASAID: " alpha"}}, metadata: valid},
		{name: "duplicate id", teams: []Team{{ASAID: "alpha"}, {ASAID: "alpha"}}, metadata: valid},
		{name: "blank trigger", teams: teamsTestCatalog(), metadata: FullRefreshMetadata{StartedAt: finished.Add(-time.Minute), FinishedAt: finished}},
		{name: "zero clock", teams: teamsTestCatalog(), metadata: FullRefreshMetadata{Trigger: SourceTriggerCLI}},
		{name: "finish before start", teams: teamsTestCatalog(), metadata: FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: finished, FinishedAt: finished.Add(-time.Second)}},
		{name: "raw finish before start within second", teams: teamsTestCatalog(), metadata: FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: finished.Add(900 * time.Millisecond), FinishedAt: finished.Add(100 * time.Millisecond)}},
		{name: "raw due before finish within second", teams: teamsTestCatalog(), metadata: FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: finished.Add(-time.Minute), FinishedAt: finished.Add(900 * time.Millisecond), NextFullDueAt: teamsTestTimePtr(finished.Add(100 * time.Millisecond))}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openTeamsTestDB(t)
			if audit, err := db.UpsertTeams(ctx, test.teams, test.metadata); err == nil || audit != (SourceRefreshAudit{}) {
				t.Fatalf("UpsertTeams() = %+v, %v; want validation error and zero audit", audit, err)
			}
			assertNoTeamCatalogWrites(t, ctx, db)
		})
	}
}

func TestUpsertTeamsInsertsNoopsAndUpdatesMetadata(t *testing.T) {
	ctx := context.Background()
	db := openTeamsTestDB(t)
	firstFinished := teamsTestTime(12).Add(900 * time.Millisecond)
	firstDue := firstFinished.Add(time.Hour)
	input := teamsTestCatalog()
	inputBefore := append([]Team(nil), input...)
	first, err := db.UpsertTeams(ctx, input, FullRefreshMetadata{Trigger: "maintenance", StartedAt: firstFinished.Add(-time.Minute), FinishedAt: firstFinished, NextFullDueAt: &firstDue})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, inputBefore) || !firstDue.Equal(firstFinished.Add(time.Hour)) {
		t.Fatalf("caller inputs changed: teams=%+v due=%s", input, firstDue)
	}
	normalizedFirst := teamsTestTime(12)
	normalizedDue := teamsTestTime(13)
	if first.ID == 0 || first.Resource != SourceResourceTeams || first.Season != "" || first.Stage != "" || first.Mode != SourceRefreshFull || first.Trigger != "maintenance" || !first.StartedAt.Equal(teamsTestTime(11).Add(59*time.Minute)) || !first.FinishedAt.Equal(normalizedFirst) || first.Outcome != SourceRefreshSuccess || first.ErrorSummary != "" || first.RequestedRows != 0 || first.ReturnedRows != 2 || first.RowsInserted != 2 || first.RowsUpdated != 0 || first.RowsUnchanged != 0 || first.RowsDeleted != 0 || first.DownstreamInputsChanged {
		t.Fatalf("first audit = %+v", first)
	}
	state, found, err := db.SourceResourceScopeState(ctx, SourceResourceTeams, "", "")
	if err != nil || !found || state.LastFullSuccessAt == nil || !state.LastFullSuccessAt.Equal(normalizedFirst) || state.NextFullDueAt == nil || !state.NextFullDueAt.Equal(normalizedDue) || !state.UpdatedAt.Equal(normalizedFirst) {
		t.Fatalf("first state = %+v, %t, %v", state, found, err)
	}
	if got := teamsTestStored(t, ctx, db); !reflect.DeepEqual(got, input) {
		t.Fatalf("stored teams = %+v, want %+v", got, input)
	}
	var updatedAt string
	if err := db.db.QueryRowContext(ctx, `SELECT updated_at FROM teams WHERE asa_team_id='alpha'`).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if updatedAt != formatTime(normalizedFirst) {
		t.Fatalf("first team update time = %q, want %q", updatedAt, formatTime(normalizedFirst))
	}

	secondFinished := teamsTestTime(14)
	secondDue := teamsTestTime(16)
	second, err := db.UpsertTeams(ctx, input, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: secondFinished.Add(-time.Minute), FinishedAt: secondFinished, NextFullDueAt: &secondDue})
	if err != nil {
		t.Fatal(err)
	}
	if second.RowsInserted != 0 || second.RowsUpdated != 0 || second.RowsUnchanged != 2 || second.DownstreamInputsChanged {
		t.Fatalf("no-op audit = %+v", second)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT updated_at FROM teams WHERE asa_team_id='alpha'`).Scan(&updatedAt); err != nil || updatedAt != formatTime(normalizedFirst) {
		t.Fatalf("unchanged team update time = %q, %v", updatedAt, err)
	}
	state, found, err = db.SourceResourceScopeState(ctx, SourceResourceTeams, "", "")
	if err != nil || !found || !state.LastFullSuccessAt.Equal(secondFinished) || !state.NextFullDueAt.Equal(secondDue) {
		t.Fatalf("no-op state = %+v, %t, %v", state, found, err)
	}

	updated := append([]Team(nil), input...)
	updated[0].Name = "Alpha United"
	updated[0].RawJSON = `{"team_id":"alpha","revision":2}`
	third, err := db.UpsertTeams(ctx, updated, teamsTestMetadata(teamsTestTime(17)))
	if err != nil {
		t.Fatal(err)
	}
	if third.RowsInserted != 0 || third.RowsUpdated != 1 || third.RowsUnchanged != 1 || third.DownstreamInputsChanged {
		t.Fatalf("updated audit = %+v", third)
	}
	state, found, err = db.SourceResourceScopeState(ctx, SourceResourceTeams, "", "")
	if err != nil || !found || !state.LastFullSuccessAt.Equal(teamsTestTime(17)) || state.NextFullDueAt != nil {
		t.Fatalf("nil-due state = %+v, %t, %v", state, found, err)
	}
	if got := teamsTestStored(t, ctx, db); !reflect.DeepEqual(got, updated) {
		t.Fatalf("updated stored teams = %+v, want %+v", got, updated)
	}
}

func TestUpsertTeamsRetainsOmittedTeamsAndDoesNotMutateOrder(t *testing.T) {
	ctx := context.Background()
	db := openTeamsTestDB(t)
	first := teamsTestCatalog()
	if _, err := db.UpsertTeams(ctx, first, teamsTestMetadata(teamsTestTime(12))); err != nil {
		t.Fatal(err)
	}
	game := cachedGame("retained-game", "2026", "Regular Season", "PreMatch", "alpha", "bravo", sql.NullInt64{}, sql.NullInt64{})
	if _, err := db.ReplaceSeason(ctx, "2026", "Regular Season", first, []Game{game}, teamsTestTime(12)); err != nil {
		t.Fatal(err)
	}
	beforeGames, err := db.seasonGames(ctx, "2026", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	subset := []Team{first[1]}
	audit, err := db.UpsertTeams(ctx, subset, teamsTestMetadata(teamsTestTime(13)))
	if err != nil {
		t.Fatal(err)
	}
	if audit.ReturnedRows != 1 || audit.RowsDeleted != 0 || audit.RowsUnchanged != 1 {
		t.Fatalf("subset audit = %+v", audit)
	}
	if got, want := teamsTestStored(t, ctx, db), first; !reflect.DeepEqual(got, want) {
		t.Fatalf("subset deleted a team: got %+v, want %+v", got, want)
	}
	afterGames, err := db.seasonGames(ctx, "2026", "Regular Season")
	if err != nil || !reflect.DeepEqual(afterGames, beforeGames) {
		t.Fatalf("subset changed games from %+v to %+v, %v", beforeGames, afterGames, err)
	}

	other := openTeamsTestDB(t)
	reversed := []Team{first[1], first[0]}
	reversedBefore := append([]Team(nil), reversed...)
	otherAudit, err := other.UpsertTeams(ctx, reversed, teamsTestMetadata(teamsTestTime(12)))
	if err != nil {
		t.Fatal(err)
	}
	if otherAudit.RowsInserted != 2 || otherAudit.RowsUpdated != 0 || otherAudit.RowsUnchanged != 0 || !reflect.DeepEqual(reversed, reversedBefore) {
		t.Fatalf("reversed write = %+v, teams %+v", otherAudit, reversed)
	}
	if got := teamsTestStored(t, ctx, other); !reflect.DeepEqual(got, first) {
		t.Fatalf("reversed stored teams = %+v, want %+v", got, first)
	}
}

func TestUpsertTeamsRetainsOlderAndEqualAuditsWithoutRegressingState(t *testing.T) {
	ctx := context.Background()
	db := openTeamsTestDB(t)
	firstFinished := teamsTestTime(12).Add(900 * time.Millisecond)
	firstDue := firstFinished.Add(time.Hour)
	first, err := db.UpsertTeams(ctx, teamsTestCatalog(), FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: firstFinished.Add(-time.Minute), FinishedAt: firstFinished, NextFullDueAt: &firstDue})
	if err != nil {
		t.Fatal(err)
	}
	olderFinished := teamsTestTime(11)
	olderDue := teamsTestTime(20)
	older, err := db.UpsertTeams(ctx, teamsTestCatalog(), FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: olderFinished.Add(-time.Minute), FinishedAt: olderFinished, NextFullDueAt: &olderDue})
	if err != nil {
		t.Fatal(err)
	}
	equalDue := teamsTestTime(22)
	equal, err := db.UpsertTeams(ctx, teamsTestCatalog(), FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: teamsTestTime(11), FinishedAt: teamsTestTime(12), NextFullDueAt: &equalDue})
	if err != nil {
		t.Fatal(err)
	}
	state, found, err := db.SourceResourceScopeState(ctx, SourceResourceTeams, "", "")
	if err != nil || !found || !state.LastFullSuccessAt.Equal(teamsTestTime(12)) || !state.NextFullDueAt.Equal(teamsTestTime(13)) || !state.UpdatedAt.Equal(teamsTestTime(12)) {
		t.Fatalf("monotonic state = %+v, %t, %v", state, found, err)
	}
	firstDue = teamsTestTime(99)
	state, found, err = db.SourceResourceScopeState(ctx, SourceResourceTeams, "", "")
	if err != nil || !found || !state.NextFullDueAt.Equal(teamsTestTime(13)) {
		t.Fatalf("state due pointer leaked = %+v, %t, %v", state, found, err)
	}
	audits, err := db.SourceRefreshAudits(ctx, SourceResourceTeams, "", "")
	if err != nil || len(audits) != 3 || audits[0].ID != equal.ID || audits[1].ID != first.ID || audits[2].ID != older.ID {
		t.Fatalf("monotonic audits = %+v, %v", audits, err)
	}
}

func TestUpsertTeamsRollsBackWhenAuditInsertFails(t *testing.T) {
	ctx := context.Background()
	db := openTeamsTestDB(t)
	initial := []Team{teamsTestCatalog()[0]}
	first, err := db.UpsertTeams(ctx, initial, teamsTestMetadata(teamsTestTime(12)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `CREATE TRIGGER abort_team_audit BEFORE INSERT ON source_refresh_audits BEGIN SELECT RAISE(ABORT, 'audit blocked'); END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS abort_team_audit`) })
	changed := teamsTestCatalog()
	changed[0].Name = "Alpha Failed Update"
	if audit, err := db.UpsertTeams(ctx, changed, teamsTestMetadata(teamsTestTime(13))); err == nil || audit != (SourceRefreshAudit{}) {
		t.Fatalf("UpsertTeams() = %+v, %v; want rollback error", audit, err)
	}
	if teams := teamsTestStored(t, ctx, db); !reflect.DeepEqual(teams, initial) {
		t.Fatalf("audit failure did not roll back teams: %+v", teams)
	}
	if audits, err := db.SourceRefreshAudits(ctx, SourceResourceTeams, "", ""); err != nil || len(audits) != 1 || !reflect.DeepEqual(audits[0], first) {
		t.Fatalf("audit failure did not roll back audit: %+v, %v", audits, err)
	}
	if state, found, err := db.SourceResourceScopeState(ctx, SourceResourceTeams, "", ""); err != nil || !found || !state.LastFullSuccessAt.Equal(teamsTestTime(12)) {
		t.Fatalf("audit failure did not roll back state: %+v, %t, %v", state, found, err)
	}
}

func TestUpsertTeamsPreservesLegacyCacheAndReplaceSeasonDoesNotUseIt(t *testing.T) {
	ctx := context.Background()
	db := openTeamsTestDB(t)
	teams := teamsTestCatalog()
	game := cachedGame("game-1", "2026", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true})
	run, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, []Game{game}, teamsTestTime(12))
	if err != nil {
		t.Fatal(err)
	}
	if run.TeamsInserted != 2 || run.TeamsUpdated != 0 || run.TeamsUnchanged != 0 || run.GamesInserted != 1 || run.GamesUpdated != 0 || run.GamesDeleted != 0 {
		t.Fatalf("legacy ReplaceSeason counts = %+v", run)
	}
	if _, err := db.ReplaceGameXG(ctx, "2026", "Regular Season", []Game{game}, []GameXG{{GameID: game.ASAID, Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1.1, Valid: true}, AwayXG: sql.NullFloat64{Float64: .7, Valid: true}, RawJSON: "{}"}}, teamsTestTime(13)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSourceScopes(ctx, "2026", "Regular Season", teamsTestTime(14)); err != nil {
		t.Fatal(err)
	}
	if audits, err := db.SourceRefreshAudits(ctx, SourceResourceTeams, "", ""); err != nil || len(audits) != 0 {
		t.Fatalf("ReplaceSeason wrote team audit %+v, %v", audits, err)
	}
	beforeSeason, err := db.Season(ctx, "2026", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	beforeScopes, err := db.SourceScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeCounts := teamsTestTableCounts(t, ctx, db)

	updated := append([]Team(nil), teams...)
	updated[0].Name = "Alpha Presentation Update"
	audit, err := db.UpsertTeams(ctx, updated, teamsTestMetadata(teamsTestTime(15)))
	if err != nil {
		t.Fatal(err)
	}
	if audit.RowsUpdated != 1 || audit.DownstreamInputsChanged {
		t.Fatalf("independent team audit = %+v", audit)
	}
	afterSeason, err := db.Season(ctx, "2026", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	afterScopes, err := db.SourceScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterSeason.FixtureSnapshotID != run.FixtureSnapshotID || afterSeason.LastSuccess == nil || afterSeason.LastSuccess.ID != run.ID || !reflect.DeepEqual(afterSeason.Games, beforeSeason.Games) || !reflect.DeepEqual(afterSeason.XGoals, beforeSeason.XGoals) || !reflect.DeepEqual(afterSeason.VenueHistory, beforeSeason.VenueHistory) {
		t.Fatalf("legacy cache changed: before=%+v after=%+v", beforeSeason, afterSeason)
	}
	if !reflect.DeepEqual(afterScopes, beforeScopes) {
		t.Fatalf("source scopes changed from %+v to %+v", beforeScopes, afterScopes)
	}
	if afterCounts := teamsTestTableCounts(t, ctx, db); !reflect.DeepEqual(afterCounts, beforeCounts) {
		t.Fatalf("legacy table counts changed from %v to %v", beforeCounts, afterCounts)
	}
}

func openTeamsTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func teamsTestCatalog() []Team {
	return []Team{
		{ASAID: "alpha", Name: "Alpha FC", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: `{"team_id":"alpha"}`},
		{ASAID: "bravo", Name: "Bravo FC", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: `{"team_id":"bravo"}`},
	}
}

func teamsTestMetadata(finished time.Time) FullRefreshMetadata {
	return FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: finished.Add(-time.Minute), FinishedAt: finished}
}

func teamsTestTime(hour int) time.Time {
	return time.Date(2026, time.July, 1, hour, 0, 0, 0, time.UTC)
}

func teamsTestTimePtr(value time.Time) *time.Time { return &value }

func teamsTestStored(t *testing.T, ctx context.Context, db *DB) []Team {
	t.Helper()
	rows, err := db.db.QueryContext(ctx, `SELECT asa_team_id,name,short_name,abbreviation,raw_json FROM teams ORDER BY asa_team_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	teams := make([]Team, 0)
	for rows.Next() {
		var team Team
		if err := rows.Scan(&team.ASAID, &team.Name, &team.ShortName, &team.Abbreviation, &team.RawJSON); err != nil {
			t.Fatal(err)
		}
		teams = append(teams, team)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return teams
}

func assertNoTeamCatalogWrites(t *testing.T, ctx context.Context, db *DB) {
	t.Helper()
	if teams := teamsTestStored(t, ctx, db); len(teams) != 0 {
		t.Fatalf("teams written during validation failure: %+v", teams)
	}
	if audits, err := db.SourceRefreshAudits(ctx, SourceResourceTeams, "", ""); err != nil || len(audits) != 0 {
		t.Fatalf("team audits written during validation failure: %+v, %v", audits, err)
	}
	if state, found, err := db.SourceResourceScopeState(ctx, SourceResourceTeams, "", ""); err != nil || found || state != (SourceResourceScopeState{}) {
		t.Fatalf("team state written during validation failure: %+v, %t, %v", state, found, err)
	}
}

func teamsTestTableCounts(t *testing.T, ctx context.Context, db *DB) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, table := range []string{"games", "game_xg", "sync_runs", "xg_sync_runs", "venue_summaries", "qualification_runs", "scenario_runs", "source_scopes", "sync_leases"} {
		var count int
		if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}
