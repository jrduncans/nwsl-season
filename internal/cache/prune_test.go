package cache

import (
	"context"
	"testing"
	"time"
)

func TestPruneHistoryRetainsCurrentRowsAndRequiredLineage(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	teams := []Team{{ASAID: "a", Name: "A", ShortName: "A", Abbreviation: "A", RawJSON: "{}"}, {ASAID: "b", Name: "B", ShortName: "B", Abbreviation: "B", RawJSON: "{}"}}
	seed, err := db.ReplaceSeason(ctx, "seed", "seed", teams, []Game{{ASAID: "seed", Season: "seed", Stage: "seed", KickoffUTC: "2026-01-01T00:00:00Z", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	current := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	oldSync := insertPruneSyncRun(t, ctx, db, "2026", "Regular Season", "success", old)
	insertPruneSyncRun(t, ctx, db, "2026", "Regular Season", "failure", old.Add(time.Hour))
	currentSync := insertPruneSyncRun(t, ctx, db, "2026", "Regular Season", "success", current)
	legacySync := insertPruneSyncRun(t, ctx, db, "2024", "Regular Season", "success", old)

	oldQualification := insertPruneQualificationRun(t, ctx, db, "snapshot", "rules", oldSync, old)
	currentQualification := insertPruneQualificationRun(t, ctx, db, "snapshot", "rules", currentSync, current)
	insertPruneQualificationStatus(t, ctx, db, oldQualification)
	insertPruneQualificationStatus(t, ctx, db, currentQualification)
	oldScenario := insertPruneScenarioRun(t, ctx, db, "snapshot", "rules", "definition", oldQualification, oldSync, old)
	currentScenario := insertPruneScenarioRun(t, ctx, db, "snapshot", "rules", "definition", currentQualification, currentSync, current)
	insertPruneScenarioResult(t, ctx, db, oldScenario)
	insertPruneScenarioResult(t, ctx, db, currentScenario)

	insertPruneXGRun(t, ctx, db, "2026", "Regular Season", "success", old)
	insertPruneXGRun(t, ctx, db, "2026", "Regular Season", "failure", old.Add(time.Hour))
	insertPruneXGRun(t, ctx, db, "2026", "Regular Season", "success", current)
	if _, err := db.db.ExecContext(ctx, `INSERT INTO sync_leases(lock_key,holder,expires_at_unix_nano) VALUES ('expired','holder',1),('active','holder',9223372036854775807)`); err != nil {
		t.Fatal(err)
	}

	result, err := db.PruneHistory(ctx, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := (HistoryPruneResult{SyncRuns: 2, XGSyncRuns: 2, QualificationRuns: 1, QualificationStatuses: 1, ScenarioRuns: 1, ScenarioResults: 1, ExpiredSyncLeases: 1}); result != want {
		t.Fatalf("prune result = %+v, want %+v", result, want)
	}

	assertPruneRowCount(t, ctx, db, "sync_runs", 3) // latest success, retained source, and legacy-only success
	assertPruneRowCount(t, ctx, db, "xg_sync_runs", 1)
	assertPruneRowCount(t, ctx, db, "qualification_runs", 1)
	assertPruneRowCount(t, ctx, db, "qualification_statuses", 1)
	assertPruneRowCount(t, ctx, db, "scenario_runs", 1)
	assertPruneRowCount(t, ctx, db, "scenario_results", 1)
	assertPruneRowCount(t, ctx, db, "sync_leases", 1)

	qualification, found, err := db.QualificationForSnapshot(ctx, "snapshot", "rules")
	if err != nil || !found || qualification.Run.ID != currentQualification {
		t.Fatalf("current qualification = %+v found=%v err=%v", qualification, found, err)
	}
	scenario, found, err := db.ScenarioForSnapshot(ctx, "snapshot", "rules", "definition")
	if err != nil || !found || scenario.Run.ID != currentScenario {
		t.Fatalf("current scenario = %+v found=%v err=%v", scenario, found, err)
	}
	if seed.ID == 0 || legacySync == 0 {
		t.Fatal("expected retained seed IDs")
	}
}

func insertPruneSyncRun(t *testing.T, ctx context.Context, db *DB, season, stage, outcome string, finished time.Time) int64 {
	t.Helper()
	result, err := db.db.ExecContext(ctx, `INSERT INTO sync_runs(started_at,finished_at,season,stage,outcome,error_summary,fixture_snapshot_id,teams_upserted,games_upserted,games_deleted,games_seen,teams_inserted,teams_updated,teams_unchanged,games_inserted,games_updated,games_unchanged) VALUES(?,?,?,?,?,'','',0,0,0,0,0,0,0,0,0,0)`, formatTime(finished), formatTime(finished), season, stage, outcome)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertPruneXGRun(t *testing.T, ctx context.Context, db *DB, season, stage, outcome string, finished time.Time) {
	t.Helper()
	if _, err := db.db.ExecContext(ctx, `INSERT INTO xg_sync_runs(started_at,finished_at,season,stage,outcome,error_summary,rows_seen,available_games,unavailable_games,rows_inserted,rows_updated,rows_unchanged) VALUES(?,?,?,?,?,'',0,0,0,0,0,0)`, formatTime(finished), formatTime(finished), season, stage, outcome); err != nil {
		t.Fatal(err)
	}
}

func insertPruneQualificationRun(t *testing.T, ctx context.Context, db *DB, snapshot, rules string, syncRunID int64, finished time.Time) int64 {
	t.Helper()
	result, err := db.db.ExecContext(ctx, `INSERT INTO qualification_runs(fixture_snapshot_id,source_sync_run_id,season,stage,rules_version,started_at,finished_at,outcome,error_summary,expected_statuses,written_statuses) VALUES(?,?, '2026','Regular Season',?,?,?,'complete','',1,1)`, snapshot, syncRunID, rules, formatTime(finished), formatTime(finished))
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertPruneQualificationStatus(t *testing.T, ctx context.Context, db *DB, runID int64) {
	t.Helper()
	if _, err := db.db.ExecContext(ctx, `INSERT INTO qualification_statuses VALUES(?, 'a','playoffs',1,'clinched','cheap_bound','',0,'upper_bound',0,'upper_bound','[]','[]','not_applicable','[]','', '{}')`, runID); err != nil {
		t.Fatal(err)
	}
}

func insertPruneScenarioRun(t *testing.T, ctx context.Context, db *DB, snapshot, rules, definition string, qualificationRunID, syncRunID int64, finished time.Time) int64 {
	t.Helper()
	result, err := db.db.ExecContext(ctx, `INSERT INTO scenario_runs(fixture_snapshot_id,qualification_run_id,source_sync_run_id,season,stage,rules_version,definition_version,slate_id,slate_state,slate_source,matchday,starts_at_utc,latest_kickoff_utc,cutoff_utc,fixture_ids_json,slate_reason,started_at,finished_at,outcome,error_summary,expected_results,written_results) VALUES(?,?,?,'2026','Regular Season',?,?,'unavailable','unavailable','',0,'','','','[]','',?,?,'complete','',1,1)`, snapshot, qualificationRunID, syncRunID, rules, definition, formatTime(finished), formatTime(finished))
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertPruneScenarioResult(t *testing.T, ctx context.Context, db *DB, runID int64) {
	t.Helper()
	if _, err := db.db.ExecContext(ctx, `INSERT INTO scenario_results VALUES(?, 'a','playoffs',1,'cannot_clinch',0,0,'[]','[]','[]','',0,0,0,'{}',0,0,'[]')`, runID); err != nil {
		t.Fatal(err)
	}
}

func assertPruneRowCount(t *testing.T, ctx context.Context, db *DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}
