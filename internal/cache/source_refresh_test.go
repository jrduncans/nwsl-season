package cache

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestMigrationTenCreatesAuditStateTablesAndConstraints(t *testing.T) {
	ctx := context.Background()
	db := openSourceRefreshTestDB(t)

	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 10 || schemaVersion != 10 {
		t.Fatalf("schema version = %d / %d, want 10", version, schemaVersion)
	}
	var indexCount int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='source_refresh_audits_scope_idx'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatal("source refresh audit scope index is missing")
	}

	valid := []any{SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceTriggerCLI,
		"2026-01-01T00:00:00Z", "2026-01-01T00:01:00Z", SourceRefreshSuccess, "", 1, 1, 0, 0, 1, 0, 0}
	insert := `INSERT INTO source_refresh_audits (
		resource,season,stage,mode,trigger,started_at,finished_at,outcome,error_summary,
		requested_rows,returned_rows,rows_inserted,rows_updated,rows_unchanged,rows_deleted,downstream_inputs_changed
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	for _, mutation := range []func([]any){
		func(v []any) { v[0] = "invalid" },
		func(v []any) { v[3] = "invalid" },
		func(v []any) { v[7] = "invalid" },
		func(v []any) { v[9] = -1 },
		func(v []any) { v[1], v[2] = "", "" },
		func(v []any) { v[0], v[1], v[2] = SourceResourceTeams, "2026", "Regular Season" },
		func(v []any) { v[8] = "unexpected" },
		func(v []any) { v[7], v[8] = SourceRefreshFailure, "" },
		func(v []any) { v[15] = 2 },
		func(v []any) { v[3], v[14] = SourceRefreshTargeted, 1 },
	} {
		values := append([]any(nil), valid...)
		mutation(values)
		if _, err := db.db.ExecContext(ctx, insert, values...); err == nil {
			t.Fatalf("source_refresh_audits accepted invalid values %#v", values)
		}
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO source_resource_scope_state(resource,season,stage,updated_at) VALUES('teams','2026','Regular Season','2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("source_resource_scope_state accepted a non-global team scope")
	}
}

func TestMigrationTenBackfillsLegacySuccessStateAndPreservesRows(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO sync_runs(started_at,finished_at,season,stage,outcome,error_summary,fixture_snapshot_id,teams_upserted,games_upserted,games_deleted,games_seen,teams_inserted,teams_updated,teams_unchanged,games_inserted,games_updated,games_unchanged) VALUES
		('2026-01-01T00:00:00Z','2026-01-01T01:00:00Z','2024','Regular Season','success','','',0,0,0,0,0,0,0,0,0,0),
		('2026-01-02T00:00:00Z','2026-01-02T01:00:00Z','2024','Regular Season','failure','failed','',0,0,0,0,0,0,0,0,0,0),
		('2026-01-03T00:00:00Z','2026-01-03T01:00:00Z','2024','Regular Season','success','','',0,0,0,0,0,0,0,0,0,0),
		('2026-01-04T00:00:00Z','2026-01-04T01:00:00Z','2025','Regular Season','failure','failed','',0,0,0,0,0,0,0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO xg_sync_runs(started_at,finished_at,season,stage,outcome,error_summary,rows_seen,available_games,unavailable_games,rows_inserted,rows_updated,rows_unchanged) VALUES
		('2026-01-01T00:00:00Z','2026-01-01T02:00:00Z','2024','Regular Season','success','',0,0,0,0,0,0),
		('2026-01-03T00:00:00Z','2026-01-03T02:00:00Z','2024','Regular Season','failure','failed',0,0,0,0,0,0),
		('2026-01-05T00:00:00Z','2026-01-05T02:00:00Z','2025','Regular Season','failure','failed',0,0,0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO source_scopes(season,stage,registration,lifecycle,discovery,registered_at,updated_at) VALUES ('2024','Regular Season','observed','active','available','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO teams(asa_team_id,name,short_name,abbreviation,raw_json,updated_at) VALUES
		('cached-a','Cached A','A','A','{}','2026-01-01T00:00:00Z'),
		('cached-b','Cached B','B','B','{}','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO games(asa_game_id,season,stage,kickoff_utc,status,home_team_id,away_team_id,home_score,away_score,matchday,last_updated_utc,raw_json,synced_at) VALUES ('cached-game','2023','Cached Only','2026-01-01T00:00:00Z','PreMatch','cached-a','cached-b',NULL,NULL,NULL,'','{}','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO qualification_runs(fixture_snapshot_id,source_sync_run_id,season,stage,rules_version,started_at,finished_at,outcome,error_summary,expected_statuses,written_statuses) VALUES ('snapshot',1,'2024','Regular Season','rules','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','complete','',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO scenario_runs(fixture_snapshot_id,qualification_run_id,source_sync_run_id,season,stage,rules_version,definition_version,slate_id,slate_state,slate_source,matchday,starts_at_utc,latest_kickoff_utc,cutoff_utc,fixture_ids_json,slate_reason,started_at,finished_at,outcome,error_summary,expected_results,written_results) VALUES ('snapshot',1,1,'2024','Regular Season','rules','v1','unavailable','unavailable','',0,'','','','[]','','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','complete','',0,0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP INDEX source_refresh_audits_scope_idx`,
		`DROP TABLE source_refresh_audits`,
		`DROP TABLE source_resource_scope_state`,
		`DELETE FROM schema_migrations WHERE version = 10`,
	} {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			_ = legacy.Close()
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
	states, err := db.SourceResourceScopeStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 3 {
		t.Fatalf("backfilled states = %+v, want teams, games, and game_xg", states)
	}
	wantTimes := map[SourceResource]time.Time{
		SourceResourceTeams:  testSourceRefreshTime(2026, 1, 3, 1),
		SourceResourceGames:  testSourceRefreshTime(2026, 1, 3, 1),
		SourceResourceGameXG: testSourceRefreshTime(2026, 1, 1, 2),
	}
	for _, state := range states {
		if state.NextFullDueAt != nil || state.LastFullSuccessAt == nil || !state.LastFullSuccessAt.Equal(wantTimes[state.Resource]) || state.UpdatedAt.Location() != time.UTC {
			t.Fatalf("backfilled state = %+v", state)
		}
	}
	if state, found, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2023", "Cached Only"); err != nil || found || state != (SourceResourceScopeState{}) {
		t.Fatalf("cached rows created refresh state %+v, %t, %v", state, found, err)
	}
	if audits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2024", "Regular Season"); err != nil || len(audits) != 0 || audits == nil {
		t.Fatalf("generalized audit backfill = %#v, %v", audits, err)
	}
	for _, table := range []string{"sync_runs", "xg_sync_runs", "source_scopes", "qualification_runs", "scenario_runs"} {
		var count int
		if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count == 0 {
			t.Fatalf("preserved %s rows = %d, %v", table, count, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if db, err = Open(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if states, err := db.SourceResourceScopeStates(ctx); err != nil || len(states) != 3 {
		t.Fatalf("states after idempotent reopen = %+v, %v", states, err)
	}
}

func TestRecordSourceRefreshFullStateAndMonotonicity(t *testing.T) {
	ctx := context.Background()
	db := openSourceRefreshTestDB(t)
	finished := testSourceRefreshTime(2026, 7, 1, 12)
	due := finished.Add(6 * time.Hour)
	first, err := db.RecordSourceRefresh(ctx, testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, finished), &due)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 || first.StartedAt.Location() != time.UTC || first.FinishedAt.Location() != time.UTC {
		t.Fatalf("recorded audit = %+v", first)
	}
	audits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2026", "Regular Season")
	if err != nil || len(audits) != 1 || !reflect.DeepEqual(audits[0], first) {
		t.Fatalf("audit round trip = %+v, recorded %+v, %v", audits, first, err)
	}
	state, found, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2026", "Regular Season")
	if err != nil || !found || state.LastFullSuccessAt == nil || !state.LastFullSuccessAt.Equal(finished) || state.NextFullDueAt == nil || !state.NextFullDueAt.Equal(due) || !state.UpdatedAt.Equal(finished) {
		t.Fatalf("first state = %+v, %t, %v", state, found, err)
	}

	olderDue := finished.Add(24 * time.Hour)
	older := testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, finished.Add(-time.Hour))
	if _, err := db.RecordSourceRefresh(ctx, older, &olderDue); err != nil {
		t.Fatal(err)
	}
	equalDue := finished.Add(48 * time.Hour)
	equal := testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, finished)
	if _, err := db.RecordSourceRefresh(ctx, equal, &equalDue); err != nil {
		t.Fatal(err)
	}
	state, found, err = db.SourceResourceScopeState(ctx, SourceResourceGames, "2026", "Regular Season")
	if err != nil || !found || !state.LastFullSuccessAt.Equal(finished) || !state.NextFullDueAt.Equal(due) || !state.UpdatedAt.Equal(finished) {
		t.Fatalf("non-monotonic state = %+v, %t, %v", state, found, err)
	}
	later := finished.Add(time.Hour)
	laterDue := later.Add(time.Hour)
	if _, err := db.RecordSourceRefresh(ctx, testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, later), &laterDue); err != nil {
		t.Fatal(err)
	}
	state, found, err = db.SourceResourceScopeState(ctx, SourceResourceGames, "2026", "Regular Season")
	if err != nil || !found || !state.LastFullSuccessAt.Equal(later) || !state.NextFullDueAt.Equal(laterDue) || !state.UpdatedAt.Equal(later) {
		t.Fatalf("advanced state = %+v, %t, %v", state, found, err)
	}
	audits, err = db.SourceRefreshAudits(ctx, SourceResourceGames, "2026", "Regular Season")
	if err != nil || len(audits) != 4 || !audits[0].FinishedAt.Equal(later) || !audits[1].FinishedAt.Equal(finished) || !audits[2].FinishedAt.Equal(finished) {
		t.Fatalf("audit history = %+v, %v", audits, err)
	}
}

func TestRecordSourceRefreshSameSecondSuccessDoesNotRegressState(t *testing.T) {
	ctx := context.Background()
	db := openSourceRefreshTestDB(t)
	firstFinished := testSourceRefreshTime(2026, 7, 1, 12).Add(900 * time.Millisecond)
	firstDue := firstFinished.Add(time.Hour)
	normalizedDue := firstDue.UTC().Truncate(time.Second)
	first, err := db.RecordSourceRefresh(ctx, testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, firstFinished), &firstDue)
	if err != nil {
		t.Fatal(err)
	}
	olderFinished := testSourceRefreshTime(2026, 7, 1, 12).Add(100 * time.Millisecond)
	olderDue := olderFinished.Add(24 * time.Hour)
	older, err := db.RecordSourceRefresh(ctx, testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, olderFinished), &olderDue)
	if err != nil {
		t.Fatal(err)
	}
	if !first.FinishedAt.Equal(testSourceRefreshTime(2026, 7, 1, 12)) || !older.FinishedAt.Equal(first.FinishedAt) {
		t.Fatalf("normalized audits = %+v / %+v", first, older)
	}
	state, found, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2026", "Regular Season")
	if err != nil || !found || !state.LastFullSuccessAt.Equal(first.FinishedAt) || !state.NextFullDueAt.Equal(normalizedDue) || !state.UpdatedAt.Equal(first.FinishedAt) {
		t.Fatalf("same-second state = %+v, %t, %v", state, found, err)
	}
	audits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2026", "Regular Season")
	if err != nil || len(audits) != 2 || !reflect.DeepEqual(audits[0], older) || !reflect.DeepEqual(audits[1], first) {
		t.Fatalf("same-second audit history = %+v, %v", audits, err)
	}
}

func TestRecordSourceRefreshNonFullAuditsDoNotChangeState(t *testing.T) {
	ctx := context.Background()
	db := openSourceRefreshTestDB(t)
	finished := testSourceRefreshTime(2026, 7, 1, 12)
	due := finished.Add(time.Hour)
	if _, err := db.RecordSourceRefresh(ctx, testSourceRefreshAudit(SourceResourceGameXG, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, finished), &due); err != nil {
		t.Fatal(err)
	}
	for _, audit := range []SourceRefreshAudit{
		testSourceRefreshAudit(SourceResourceGameXG, "2026", "Regular Season", SourceRefreshTargeted, SourceRefreshSuccess, finished.Add(time.Hour)),
		testSourceRefreshAudit(SourceResourceGameXG, "2026", "Regular Season", SourceRefreshRecalculate, SourceRefreshSuccess, finished.Add(2*time.Hour)),
		testSourceRefreshAudit(SourceResourceGameXG, "2026", "Regular Season", SourceRefreshFull, SourceRefreshFailure, finished.Add(3*time.Hour)),
	} {
		if audit.Outcome == SourceRefreshFailure {
			audit.ErrorSummary = "upstream unavailable"
		}
		if _, err := db.RecordSourceRefresh(ctx, audit, nil); err != nil {
			t.Fatal(err)
		}
	}
	state, found, err := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2026", "Regular Season")
	if err != nil || !found || !state.LastFullSuccessAt.Equal(finished) || !state.NextFullDueAt.Equal(due) {
		t.Fatalf("state after non-full audits = %+v, %t, %v", state, found, err)
	}
	audits, err := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2026", "Regular Season")
	if err != nil || len(audits) != 4 {
		t.Fatalf("non-full audits = %+v, %v", audits, err)
	}
}

func TestRecordSourceRefreshRejectsInvalidInputBeforeWriting(t *testing.T) {
	ctx := context.Background()
	db := openSourceRefreshTestDB(t)
	base := testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, testSourceRefreshTime(2026, 7, 1, 12))
	for _, mutate := range []func(*SourceRefreshAudit, **time.Time){
		func(a *SourceRefreshAudit, _ **time.Time) { a.ID = 1 },
		func(a *SourceRefreshAudit, _ **time.Time) { a.Resource = "invalid" },
		func(a *SourceRefreshAudit, _ **time.Time) { a.Mode = "invalid" },
		func(a *SourceRefreshAudit, _ **time.Time) { a.Outcome = "invalid" },
		func(a *SourceRefreshAudit, _ **time.Time) { a.Season = " 2026" },
		func(a *SourceRefreshAudit, _ **time.Time) { a.Stage = " " },
		func(a *SourceRefreshAudit, _ **time.Time) { a.Trigger = " " },
		func(a *SourceRefreshAudit, _ **time.Time) { a.StartedAt = time.Time{} },
		func(a *SourceRefreshAudit, _ **time.Time) { a.FinishedAt = a.StartedAt.Add(-time.Second) },
		func(a *SourceRefreshAudit, _ **time.Time) { a.RowsInserted = -1 },
		func(a *SourceRefreshAudit, _ **time.Time) { a.ErrorSummary = "unexpected" },
		func(a *SourceRefreshAudit, _ **time.Time) { a.Outcome, a.ErrorSummary = SourceRefreshFailure, " " },
		func(a *SourceRefreshAudit, _ **time.Time) { a.Mode, a.RowsDeleted = SourceRefreshTargeted, 1 },
		func(a *SourceRefreshAudit, _ **time.Time) {
			a.Mode, a.DownstreamInputsChanged = SourceRefreshRecalculate, true
		},
		func(a *SourceRefreshAudit, _ **time.Time) {
			a.Outcome, a.ErrorSummary, a.DownstreamInputsChanged = SourceRefreshFailure, "failure", true
		},
		func(a *SourceRefreshAudit, due **time.Time) {
			value := a.FinishedAt.Add(time.Hour)
			*due = &value
			a.Mode = SourceRefreshTargeted
		},
		func(a *SourceRefreshAudit, due **time.Time) { value := a.FinishedAt.Add(-time.Hour); *due = &value },
	} {
		audit := base
		var due *time.Time
		mutate(&audit, &due)
		if _, err := db.RecordSourceRefresh(ctx, audit, due); err == nil {
			t.Fatalf("RecordSourceRefresh accepted invalid audit %+v", audit)
		}
	}
	if state, found, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2026", "Regular Season"); err != nil || found || state != (SourceResourceScopeState{}) {
		t.Fatalf("invalid write created state %+v, %t, %v", state, found, err)
	}
	if audits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2026", "Regular Season"); err != nil || len(audits) != 0 || audits == nil {
		t.Fatalf("invalid write created audits %#v, %v", audits, err)
	}
}

func TestRecordSourceRefreshRejectsInvalidRawSubsecondOrdering(t *testing.T) {
	ctx := context.Background()
	db := openSourceRefreshTestDB(t)
	second := testSourceRefreshTime(2026, 7, 1, 12)
	finishBeforeStart := testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, second.Add(100*time.Millisecond))
	finishBeforeStart.StartedAt = second.Add(900 * time.Millisecond)
	if _, err := db.RecordSourceRefresh(ctx, finishBeforeStart, nil); err == nil {
		t.Fatal("accepted raw finish before start within one stored second")
	}
	dueBeforeFinish := testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, second.Add(900*time.Millisecond))
	due := second.Add(100 * time.Millisecond)
	if _, err := db.RecordSourceRefresh(ctx, dueBeforeFinish, &due); err == nil {
		t.Fatal("accepted raw next due before finish within one stored second")
	}
	if audits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2026", "Regular Season"); err != nil || len(audits) != 0 {
		t.Fatalf("invalid raw ordering wrote audits %+v, %v", audits, err)
	}
}

func TestPrepareSourceRefreshNormalizesAndCopiesDueTime(t *testing.T) {
	finished := testSourceRefreshTime(2026, 7, 1, 12).Add(900 * time.Millisecond)
	due := finished.Add(time.Hour)
	audit, copiedDue, err := prepareSourceRefresh(testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, finished), &due)
	if err != nil {
		t.Fatal(err)
	}
	if !audit.StartedAt.Equal(testSourceRefreshTime(2026, 7, 1, 11).Add(59*time.Minute)) || !audit.FinishedAt.Equal(testSourceRefreshTime(2026, 7, 1, 12)) || copiedDue == nil || !copiedDue.Equal(testSourceRefreshTime(2026, 7, 1, 13)) {
		t.Fatalf("prepared source refresh = %+v, due %v", audit, copiedDue)
	}
	due = testSourceRefreshTime(2026, 7, 2, 12)
	if !copiedDue.Equal(testSourceRefreshTime(2026, 7, 1, 13)) {
		t.Fatalf("prepared due pointer changed to %s", copiedDue)
	}
}

func TestSourceRefreshReadOrderingFilteringScannersAndPointers(t *testing.T) {
	ctx := context.Background()
	db := openSourceRefreshTestDB(t)
	base := testSourceRefreshTime(2026, 7, 1, 12)
	for _, audit := range []SourceRefreshAudit{
		testSourceRefreshAudit(SourceResourceGames, "2025", "Z Stage", SourceRefreshFull, SourceRefreshSuccess, base),
		testSourceRefreshAudit(SourceResourceGames, "2026", "B Stage", SourceRefreshFull, SourceRefreshSuccess, base.Add(time.Hour)),
		testSourceRefreshAudit(SourceResourceGames, "2026", "A Stage", SourceRefreshFull, SourceRefreshSuccess, base.Add(2*time.Hour)),
		testSourceRefreshAudit(SourceResourceGameXG, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, base),
		testSourceRefreshAudit(SourceResourceTeams, "", "", SourceRefreshFull, SourceRefreshSuccess, base),
	} {
		if _, err := db.RecordSourceRefresh(ctx, audit, nil); err != nil {
			t.Fatal(err)
		}
	}
	newest := testSourceRefreshAudit(SourceResourceGames, "2026", "A Stage", SourceRefreshTargeted, SourceRefreshSuccess, base.Add(3*time.Hour))
	if _, err := db.RecordSourceRefresh(ctx, newest, nil); err != nil {
		t.Fatal(err)
	}
	states, err := db.SourceResourceScopeStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := make([]string, 0, len(states))
	for _, state := range states {
		gotOrder = append(gotOrder, string(state.Resource)+":"+state.Season+":"+state.Stage)
	}
	wantOrder := []string{"teams::", "games:2026:A Stage", "games:2026:B Stage", "games:2025:Z Stage", "game_xg:2026:Regular Season"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("state order = %v, want %v", gotOrder, wantOrder)
	}
	audits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2026", "A Stage")
	if err != nil || len(audits) != 2 || audits[0].Mode != SourceRefreshTargeted || !audits[0].FinishedAt.Equal(base.Add(3*time.Hour)) {
		t.Fatalf("filtered audits = %+v, %v", audits, err)
	}
	if missing, found, err := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2020", "Regular Season"); err != nil || found || missing != (SourceResourceScopeState{}) {
		t.Fatalf("missing state = %+v, %t, %v", missing, found, err)
	}
	if empty, err := db.SourceRefreshAudits(ctx, SourceResourceGameXG, "2020", "Regular Season"); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty audits = %#v, %v", empty, err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO source_resource_scope_state(resource,season,stage,last_full_success_at,next_full_due_at,updated_at) VALUES ('game_xg','2024','No Success',NULL,NULL,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if state, found, err := db.SourceResourceScopeState(ctx, SourceResourceGameXG, "2024", "No Success"); err != nil || !found || state.LastFullSuccessAt != nil || state.NextFullDueAt != nil {
		t.Fatalf("nullable state timestamps = %+v, %t, %v", state, found, err)
	}
	states[0].LastFullSuccessAt.Add(time.Hour)
	*states[0].LastFullSuccessAt = base.Add(99 * time.Hour)
	state, found, err := db.SourceResourceScopeState(ctx, SourceResourceTeams, "", "")
	if err != nil || !found || !state.LastFullSuccessAt.Equal(base) {
		t.Fatalf("state pointer leaked %+v, %t, %v", state, found, err)
	}

	if _, err := db.db.ExecContext(ctx, `PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE source_refresh_audits SET downstream_inputs_changed = 2 WHERE id = ?`, audits[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2026", "A Stage"); err == nil {
		t.Fatal("malformed stored audit did not return an error")
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE source_resource_scope_state SET updated_at = 'not-a-time' WHERE resource = 'teams'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SourceResourceScopeState(ctx, SourceResourceTeams, "", ""); err == nil {
		t.Fatal("malformed stored state did not return an error")
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE source_resource_scope_state SET updated_at = '2026-01-01T00:00:00Z' WHERE resource = 'teams'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE source_resource_scope_state SET resource = 'invalid' WHERE resource = 'game_xg' AND season = '2024'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SourceResourceScopeStates(ctx); err == nil {
		t.Fatal("malformed stored state enum did not return an error")
	}
	if _, err := db.db.ExecContext(ctx, `PRAGMA ignore_check_constraints = OFF`); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRefreshMetadataDoesNotChangeSourceScopes(t *testing.T) {
	ctx := context.Background()
	db := openSourceRefreshTestDB(t)
	now := testSourceRefreshTime(2026, 7, 1, 12)
	if _, err := db.EnsureSourceScopes(ctx, "2026", "Regular Season", now); err != nil {
		t.Fatal(err)
	}
	before, err := db.SourceScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordSourceRefresh(ctx, testSourceRefreshAudit(SourceResourceGames, "2026", "Regular Season", SourceRefreshFull, SourceRefreshSuccess, now.Add(time.Hour)), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SourceResourceScopeStates(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2026", "Regular Season"); err != nil {
		t.Fatal(err)
	}
	after, err := db.SourceScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("source scopes changed from %+v to %+v", before, after)
	}
}

func openSourceRefreshTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testSourceRefreshAudit(resource SourceResource, season, stage string, mode SourceRefreshMode, outcome SourceRefreshOutcome, finished time.Time) SourceRefreshAudit {
	audit := SourceRefreshAudit{
		Resource: resource, Season: season, Stage: stage, Mode: mode, Trigger: SourceTriggerCLI,
		StartedAt: finished.Add(-time.Minute), FinishedAt: finished, Outcome: outcome,
		RequestedRows: 3, ReturnedRows: 4, RowsInserted: 1, RowsUpdated: 1, RowsUnchanged: 2,
	}
	if outcome == SourceRefreshFailure {
		audit.ErrorSummary = "failed"
	}
	return audit
}

func testSourceRefreshTime(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
}
