package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// HistoryPruneResult reports rows removed by PruneHistory. It deliberately
// excludes current cache data such as teams, games, and game xG values.
type HistoryPruneResult struct {
	SyncRuns              int64
	XGSyncRuns            int64
	QualificationRuns     int64
	QualificationStatuses int64
	ScenarioRuns          int64
	ScenarioResults       int64
	ExpiredSyncLeases     int64
}

// PruneHistory removes superseded operational history that finished before
// before. It is intentionally an operator action, rather than part of normal
// refreshes.
//
// For each identity, pruning always retains the latest attempt and the latest
// successful or complete result, regardless of age. It also retains every
// source row required by a retained derived batch. Current fixture, team, and
// xG cache records are never considered for deletion.
func (c *DB) PruneHistory(ctx context.Context, before time.Time) (HistoryPruneResult, error) {
	if before.IsZero() {
		return HistoryPruneResult{}, errors.New("history prune cutoff is required")
	}
	before = before.UTC()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return HistoryPruneResult{}, fmt.Errorf("begin history prune: %w", err)
	}
	defer rollback(tx)

	var result HistoryPruneResult
	// Delete descendants first. This lets the parent pruning below retain only
	// lineage still needed by the latest/in-retention scenario batches.
	if result.ScenarioRuns, result.ScenarioResults, err = deleteScenarioHistory(ctx, tx, before); err != nil {
		return HistoryPruneResult{}, err
	}
	if result.QualificationRuns, result.QualificationStatuses, err = deleteQualificationHistory(ctx, tx, before); err != nil {
		return HistoryPruneResult{}, err
	}
	if result.SyncRuns, err = deleteSyncHistory(ctx, tx, before); err != nil {
		return HistoryPruneResult{}, err
	}
	if result.XGSyncRuns, err = deleteXGSyncHistory(ctx, tx, before); err != nil {
		return HistoryPruneResult{}, err
	}
	if result.ExpiredSyncLeases, err = deleteExpiredSyncLeases(ctx, tx, time.Now().UTC()); err != nil {
		return HistoryPruneResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return HistoryPruneResult{}, fmt.Errorf("commit history prune: %w", err)
	}
	return result, nil
}

const obsoleteScenarioRuns = `
	finished_at < ?
	AND EXISTS (
		SELECT 1 FROM scenario_runs newer
		WHERE newer.fixture_snapshot_id = scenario_runs.fixture_snapshot_id
			AND newer.rules_version = scenario_runs.rules_version
			AND newer.definition_version = scenario_runs.definition_version
			AND (newer.finished_at > scenario_runs.finished_at
				OR (newer.finished_at = scenario_runs.finished_at AND newer.id > scenario_runs.id))
	)
	AND (outcome <> 'complete' OR EXISTS (
		SELECT 1 FROM scenario_runs newer_complete
		WHERE newer_complete.fixture_snapshot_id = scenario_runs.fixture_snapshot_id
			AND newer_complete.rules_version = scenario_runs.rules_version
			AND newer_complete.definition_version = scenario_runs.definition_version
			AND newer_complete.outcome = 'complete'
			AND (newer_complete.finished_at > scenario_runs.finished_at
				OR (newer_complete.finished_at = scenario_runs.finished_at AND newer_complete.id > scenario_runs.id))
	))`

func deleteScenarioHistory(ctx context.Context, tx *sql.Tx, before time.Time) (int64, int64, error) {
	runs, err := countRows(ctx, tx, `SELECT COUNT(*) FROM scenario_runs WHERE `+obsoleteScenarioRuns, formatTime(before))
	if err != nil {
		return 0, 0, fmt.Errorf("count obsolete scenario runs: %w", err)
	}
	results, err := countRows(ctx, tx, `SELECT COUNT(*) FROM scenario_results WHERE scenario_run_id IN (SELECT id FROM scenario_runs WHERE `+obsoleteScenarioRuns+`)`, formatTime(before))
	if err != nil {
		return 0, 0, fmt.Errorf("count obsolete scenario results: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scenario_runs WHERE `+obsoleteScenarioRuns, formatTime(before)); err != nil {
		return 0, 0, fmt.Errorf("delete obsolete scenario runs: %w", err)
	}
	return runs, results, nil
}

const obsoleteQualificationRuns = `
	finished_at < ?
	AND NOT EXISTS (SELECT 1 FROM scenario_runs WHERE scenario_runs.qualification_run_id = qualification_runs.id)
	AND EXISTS (
		SELECT 1 FROM qualification_runs newer
		WHERE newer.fixture_snapshot_id = qualification_runs.fixture_snapshot_id
			AND newer.rules_version = qualification_runs.rules_version
			AND (newer.finished_at > qualification_runs.finished_at
				OR (newer.finished_at = qualification_runs.finished_at AND newer.id > qualification_runs.id))
	)
	AND (outcome <> 'complete' OR EXISTS (
		SELECT 1 FROM qualification_runs newer_complete
		WHERE newer_complete.fixture_snapshot_id = qualification_runs.fixture_snapshot_id
			AND newer_complete.rules_version = qualification_runs.rules_version
			AND newer_complete.outcome = 'complete'
			AND (newer_complete.finished_at > qualification_runs.finished_at
				OR (newer_complete.finished_at = qualification_runs.finished_at AND newer_complete.id > qualification_runs.id))
	))`

func deleteQualificationHistory(ctx context.Context, tx *sql.Tx, before time.Time) (int64, int64, error) {
	runs, err := countRows(ctx, tx, `SELECT COUNT(*) FROM qualification_runs WHERE `+obsoleteQualificationRuns, formatTime(before))
	if err != nil {
		return 0, 0, fmt.Errorf("count obsolete qualification runs: %w", err)
	}
	statuses, err := countRows(ctx, tx, `SELECT COUNT(*) FROM qualification_statuses WHERE qualification_run_id IN (SELECT id FROM qualification_runs WHERE `+obsoleteQualificationRuns+`)`, formatTime(before))
	if err != nil {
		return 0, 0, fmt.Errorf("count obsolete qualification statuses: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM qualification_runs WHERE `+obsoleteQualificationRuns, formatTime(before)); err != nil {
		return 0, 0, fmt.Errorf("delete obsolete qualification runs: %w", err)
	}
	return runs, statuses, nil
}

const obsoleteSyncRuns = `
	finished_at < ?
	AND NOT EXISTS (SELECT 1 FROM qualification_runs WHERE qualification_runs.source_sync_run_id = sync_runs.id)
	AND NOT EXISTS (SELECT 1 FROM scenario_runs WHERE scenario_runs.source_sync_run_id = sync_runs.id)
	AND EXISTS (
		SELECT 1 FROM sync_runs newer
		WHERE newer.season = sync_runs.season AND newer.stage = sync_runs.stage
			AND (newer.finished_at > sync_runs.finished_at
				OR (newer.finished_at = sync_runs.finished_at AND newer.id > sync_runs.id))
	)
	AND (outcome <> 'success' OR EXISTS (
		SELECT 1 FROM sync_runs newer_success
		WHERE newer_success.season = sync_runs.season AND newer_success.stage = sync_runs.stage
			AND newer_success.outcome = 'success'
			AND (newer_success.finished_at > sync_runs.finished_at
				OR (newer_success.finished_at = sync_runs.finished_at AND newer_success.id > sync_runs.id))
	))`

func deleteSyncHistory(ctx context.Context, tx *sql.Tx, before time.Time) (int64, error) {
	runs, err := countRows(ctx, tx, `SELECT COUNT(*) FROM sync_runs WHERE `+obsoleteSyncRuns, formatTime(before))
	if err != nil {
		return 0, fmt.Errorf("count obsolete sync runs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_runs WHERE `+obsoleteSyncRuns, formatTime(before)); err != nil {
		return 0, fmt.Errorf("delete obsolete sync runs: %w", err)
	}
	return runs, nil
}

const obsoleteXGSyncRuns = `
	finished_at < ?
	AND EXISTS (
		SELECT 1 FROM xg_sync_runs newer
		WHERE newer.season = xg_sync_runs.season AND newer.stage = xg_sync_runs.stage
			AND (newer.finished_at > xg_sync_runs.finished_at
				OR (newer.finished_at = xg_sync_runs.finished_at AND newer.id > xg_sync_runs.id))
	)
	AND (outcome <> 'success' OR EXISTS (
		SELECT 1 FROM xg_sync_runs newer_success
		WHERE newer_success.season = xg_sync_runs.season AND newer_success.stage = xg_sync_runs.stage
			AND newer_success.outcome = 'success'
			AND (newer_success.finished_at > xg_sync_runs.finished_at
				OR (newer_success.finished_at = xg_sync_runs.finished_at AND newer_success.id > xg_sync_runs.id))
	))`

func deleteXGSyncHistory(ctx context.Context, tx *sql.Tx, before time.Time) (int64, error) {
	runs, err := countRows(ctx, tx, `SELECT COUNT(*) FROM xg_sync_runs WHERE `+obsoleteXGSyncRuns, formatTime(before))
	if err != nil {
		return 0, fmt.Errorf("count obsolete xG sync runs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM xg_sync_runs WHERE `+obsoleteXGSyncRuns, formatTime(before)); err != nil {
		return 0, fmt.Errorf("delete obsolete xG sync runs: %w", err)
	}
	return runs, nil
}

func deleteExpiredSyncLeases(ctx context.Context, tx *sql.Tx, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM sync_leases WHERE expires_at_unix_nano <= ?`, now.UnixNano())
	if err != nil {
		return 0, fmt.Errorf("delete expired sync leases: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted sync leases: %w", err)
	}
	return deleted, nil
}

func countRows(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	var count int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
