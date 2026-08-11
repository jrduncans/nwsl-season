package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SourceResource string

const (
	SourceResourceTeams  SourceResource = "teams"
	SourceResourceGames  SourceResource = "games"
	SourceResourceGameXG SourceResource = "game_xg"
)

type SourceRefreshMode string

const (
	SourceRefreshFull        SourceRefreshMode = "full"
	SourceRefreshTargeted    SourceRefreshMode = "targeted"
	SourceRefreshRecalculate SourceRefreshMode = "recalculate"
)

type SourceRefreshOutcome string

const (
	SourceRefreshSuccess SourceRefreshOutcome = "success"
	SourceRefreshFailure SourceRefreshOutcome = "failure"
)

type SourceRefreshTrigger string

const (
	SourceTriggerScheduler    SourceRefreshTrigger = "scheduler"
	SourceTriggerStartup      SourceRefreshTrigger = "startup"
	SourceTriggerCLI          SourceRefreshTrigger = "cli"
	SourceTriggerBackfill     SourceRefreshTrigger = "backfill"
	SourceTriggerMaintenance  SourceRefreshTrigger = "maintenance"
	SourceTriggerVenueHistory SourceRefreshTrigger = "venue_history"
)

type SourceRefreshAudit struct {
	ID                      int64
	Resource                SourceResource
	Season                  string
	Stage                   string
	Mode                    SourceRefreshMode
	Trigger                 SourceRefreshTrigger
	StartedAt               time.Time
	FinishedAt              time.Time
	Outcome                 SourceRefreshOutcome
	ErrorSummary            string
	RequestedRows           int
	ReturnedRows            int
	RowsInserted            int
	RowsUpdated             int
	RowsUnchanged           int
	RowsDeleted             int
	DownstreamInputsChanged bool
}

type SourceResourceScopeState struct {
	Resource          SourceResource
	Season            string
	Stage             string
	LastFullSuccessAt *time.Time
	NextFullDueAt     *time.Time
	UpdatedAt         time.Time
}

// FullRefreshMetadata is caller-observed timing and cadence metadata for a
// successful authoritative source collection.
type FullRefreshMetadata struct {
	Trigger       SourceRefreshTrigger
	StartedAt     time.Time
	FinishedAt    time.Time
	NextFullDueAt *time.Time
}

// RecordSourceRefresh appends one source refresh audit and advances the
// associated full-refresh state only for a newer successful full refresh.
func (c *DB) RecordSourceRefresh(ctx context.Context, audit SourceRefreshAudit, nextFullDueAt *time.Time) (SourceRefreshAudit, error) {
	var err error
	audit, nextFullDueAt, err = prepareSourceRefresh(audit, nextFullDueAt)
	if err != nil {
		return SourceRefreshAudit{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceRefreshAudit{}, fmt.Errorf("begin source refresh record: %w", err)
	}
	defer rollback(tx)
	if err := recordSourceRefresh(ctx, tx, &audit, nextFullDueAt); err != nil {
		return SourceRefreshAudit{}, err
	}
	if err := tx.Commit(); err != nil {
		return SourceRefreshAudit{}, fmt.Errorf("commit source refresh record: %w", err)
	}
	return audit, nil
}

func prepareSourceRefresh(audit SourceRefreshAudit, nextFullDueAt *time.Time) (SourceRefreshAudit, *time.Time, error) {
	if audit.ID != 0 {
		return SourceRefreshAudit{}, nil, errors.New("source refresh audit ID must be zero")
	}
	if err := validateSourceRefreshAudit(audit); err != nil {
		return SourceRefreshAudit{}, nil, err
	}
	if nextFullDueAt != nil && (audit.Outcome != SourceRefreshSuccess || audit.Mode != SourceRefreshFull) {
		return SourceRefreshAudit{}, nil, errors.New("next full due time requires a successful full source refresh")
	}
	if nextFullDueAt != nil && nextFullDueAt.Before(audit.FinishedAt) {
		return SourceRefreshAudit{}, nil, errors.New("next full due time is before source refresh finish")
	}
	audit, nextFullDueAt = normalizeSourceRefreshTimes(audit, nextFullDueAt)
	return audit, nextFullDueAt, nil
}

// normalizeSourceRefreshTimes matches the cache's existing RFC3339 storage
// precision so validation, state ordering, and returned audit values agree.
func normalizeSourceRefreshTimes(audit SourceRefreshAudit, nextFullDueAt *time.Time) (SourceRefreshAudit, *time.Time) {
	audit.StartedAt = audit.StartedAt.UTC().Truncate(time.Second)
	audit.FinishedAt = audit.FinishedAt.UTC().Truncate(time.Second)
	if nextFullDueAt == nil {
		return audit, nil
	}
	due := nextFullDueAt.UTC().Truncate(time.Second)
	return audit, &due
}

func recordSourceRefresh(ctx context.Context, tx *sql.Tx, audit *SourceRefreshAudit, nextFullDueAt *time.Time) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO source_refresh_audits (
		resource, season, stage, mode, trigger, started_at, finished_at, outcome,
		error_summary, requested_rows, returned_rows, rows_inserted, rows_updated,
		rows_unchanged, rows_deleted, downstream_inputs_changed
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		audit.Resource, audit.Season, audit.Stage, audit.Mode, audit.Trigger,
		formatTime(audit.StartedAt), formatTime(audit.FinishedAt), audit.Outcome,
		audit.ErrorSummary, audit.RequestedRows, audit.ReturnedRows,
		audit.RowsInserted, audit.RowsUpdated, audit.RowsUnchanged,
		audit.RowsDeleted, boolInt(audit.DownstreamInputsChanged))
	if err != nil {
		return fmt.Errorf("insert source refresh audit: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read source refresh audit ID: %w", err)
	}
	audit.ID = id
	if audit.Outcome != SourceRefreshSuccess || audit.Mode != SourceRefreshFull {
		return nil
	}

	var existing sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT last_full_success_at FROM source_resource_scope_state
		WHERE resource = ? AND season = ? AND stage = ?`, audit.Resource, audit.Season, audit.Stage).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return insertSourceResourceScopeState(ctx, tx, *audit, nextFullDueAt)
	}
	if err != nil {
		return fmt.Errorf("load source refresh state: %w", err)
	}
	if existing.Valid {
		last, err := parseSourceRefreshTime("source refresh state last full success", existing.String)
		if err != nil {
			return err
		}
		if !audit.FinishedAt.After(last) {
			return nil
		}
	}
	return updateSourceResourceScopeState(ctx, tx, *audit, nextFullDueAt)
}

func insertSourceResourceScopeState(ctx context.Context, tx *sql.Tx, audit SourceRefreshAudit, nextFullDueAt *time.Time) error {
	var due any
	if nextFullDueAt != nil {
		due = formatTime(*nextFullDueAt)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO source_resource_scope_state (
		resource, season, stage, last_full_success_at, next_full_due_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?)`, audit.Resource, audit.Season, audit.Stage,
		formatTime(audit.FinishedAt), due, formatTime(audit.FinishedAt)); err != nil {
		return fmt.Errorf("insert source refresh state: %w", err)
	}
	return nil
}

func updateSourceResourceScopeState(ctx context.Context, tx *sql.Tx, audit SourceRefreshAudit, nextFullDueAt *time.Time) error {
	var due any
	if nextFullDueAt != nil {
		due = formatTime(*nextFullDueAt)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE source_resource_scope_state
		SET last_full_success_at = ?, next_full_due_at = ?, updated_at = ?
		WHERE resource = ? AND season = ? AND stage = ?`,
		formatTime(audit.FinishedAt), due, formatTime(audit.FinishedAt),
		audit.Resource, audit.Season, audit.Stage); err != nil {
		return fmt.Errorf("update source refresh state: %w", err)
	}
	return nil
}

func (c *DB) SourceResourceScopeState(ctx context.Context, resource SourceResource, season, stage string) (SourceResourceScopeState, bool, error) {
	if err := validateSourceResourceScope(resource, season, stage); err != nil {
		return SourceResourceScopeState{}, false, err
	}
	row := c.db.QueryRowContext(ctx, `SELECT resource, season, stage, last_full_success_at, next_full_due_at, updated_at
		FROM source_resource_scope_state WHERE resource = ? AND season = ? AND stage = ?`, resource, season, stage)
	state, err := scanSourceResourceScopeState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceResourceScopeState{}, false, nil
	}
	if err != nil {
		return SourceResourceScopeState{}, false, fmt.Errorf("load source refresh state: %w", err)
	}
	return state, true, nil
}

func (c *DB) SourceResourceScopeStates(ctx context.Context) ([]SourceResourceScopeState, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT resource, season, stage, last_full_success_at, next_full_due_at, updated_at
		FROM source_resource_scope_state
		ORDER BY CASE resource WHEN 'teams' THEN 1 WHEN 'games' THEN 2 WHEN 'game_xg' THEN 3 END,
			season DESC, stage ASC`)
	if err != nil {
		return nil, fmt.Errorf("query source refresh states: %w", err)
	}
	defer rows.Close()
	states := make([]SourceResourceScopeState, 0)
	for rows.Next() {
		state, err := scanSourceResourceScopeState(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source refresh state: %w", err)
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source refresh states: %w", err)
	}
	return states, nil
}

func (c *DB) SourceRefreshAudits(ctx context.Context, resource SourceResource, season, stage string) ([]SourceRefreshAudit, error) {
	if err := validateSourceResourceScope(resource, season, stage); err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT id, resource, season, stage, mode, trigger, started_at, finished_at,
		outcome, error_summary, requested_rows, returned_rows, rows_inserted, rows_updated,
		rows_unchanged, rows_deleted, downstream_inputs_changed
		FROM source_refresh_audits WHERE resource = ? AND season = ? AND stage = ?
		ORDER BY finished_at DESC, id DESC`, resource, season, stage)
	if err != nil {
		return nil, fmt.Errorf("query source refresh audits: %w", err)
	}
	defer rows.Close()
	audits := make([]SourceRefreshAudit, 0)
	for rows.Next() {
		audit, err := scanSourceRefreshAudit(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source refresh audit: %w", err)
		}
		audits = append(audits, audit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source refresh audits: %w", err)
	}
	return audits, nil
}

type sourceRefreshScanner interface {
	Scan(...any) error
}

func scanSourceRefreshAudit(scanner sourceRefreshScanner) (SourceRefreshAudit, error) {
	var audit SourceRefreshAudit
	var startedAt, finishedAt string
	var changed int
	if err := scanner.Scan(&audit.ID, &audit.Resource, &audit.Season, &audit.Stage, &audit.Mode,
		&audit.Trigger, &startedAt, &finishedAt, &audit.Outcome, &audit.ErrorSummary,
		&audit.RequestedRows, &audit.ReturnedRows, &audit.RowsInserted, &audit.RowsUpdated,
		&audit.RowsUnchanged, &audit.RowsDeleted, &changed); err != nil {
		return SourceRefreshAudit{}, err
	}
	if audit.ID <= 0 {
		return SourceRefreshAudit{}, fmt.Errorf("invalid source refresh audit ID %d", audit.ID)
	}
	var err error
	audit.StartedAt, err = parseSourceRefreshTime("source refresh audit start", startedAt)
	if err != nil {
		return SourceRefreshAudit{}, err
	}
	audit.FinishedAt, err = parseSourceRefreshTime("source refresh audit finish", finishedAt)
	if err != nil {
		return SourceRefreshAudit{}, err
	}
	if changed != 0 && changed != 1 {
		return SourceRefreshAudit{}, fmt.Errorf("invalid source refresh audit downstream inputs changed value %d", changed)
	}
	audit.DownstreamInputsChanged = changed == 1
	if err := validateSourceRefreshAudit(audit); err != nil {
		return SourceRefreshAudit{}, fmt.Errorf("invalid stored source refresh audit: %w", err)
	}
	return audit, nil
}

func scanSourceResourceScopeState(scanner sourceRefreshScanner) (SourceResourceScopeState, error) {
	var state SourceResourceScopeState
	var last, due sql.NullString
	var updated string
	if err := scanner.Scan(&state.Resource, &state.Season, &state.Stage, &last, &due, &updated); err != nil {
		return SourceResourceScopeState{}, err
	}
	if err := validateSourceResourceScope(state.Resource, state.Season, state.Stage); err != nil {
		return SourceResourceScopeState{}, fmt.Errorf("invalid stored source refresh state: %w", err)
	}
	var err error
	if last.Valid {
		value, err := parseSourceRefreshTime("source refresh state last full success", last.String)
		if err != nil {
			return SourceResourceScopeState{}, err
		}
		state.LastFullSuccessAt = &value
	}
	if due.Valid {
		value, err := parseSourceRefreshTime("source refresh state next full due", due.String)
		if err != nil {
			return SourceResourceScopeState{}, err
		}
		state.NextFullDueAt = &value
	}
	state.UpdatedAt, err = parseSourceRefreshTime("source refresh state update", updated)
	if err != nil {
		return SourceResourceScopeState{}, err
	}
	return state, nil
}

func parseSourceRefreshTime(label, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s timestamp: %w", label, err)
	}
	return parsed.UTC(), nil
}

func validateSourceRefreshAudit(audit SourceRefreshAudit) error {
	if err := validateSourceResourceScope(audit.Resource, audit.Season, audit.Stage); err != nil {
		return err
	}
	if !validSourceRefreshMode(audit.Mode) {
		return fmt.Errorf("invalid source refresh mode %q", audit.Mode)
	}
	if !validSourceRefreshOutcome(audit.Outcome) {
		return fmt.Errorf("invalid source refresh outcome %q", audit.Outcome)
	}
	if strings.TrimSpace(string(audit.Trigger)) == "" {
		return errors.New("source refresh trigger is blank")
	}
	if audit.StartedAt.IsZero() || audit.FinishedAt.IsZero() {
		return errors.New("source refresh timestamps are required")
	}
	if audit.FinishedAt.Before(audit.StartedAt) {
		return errors.New("source refresh finish is before start")
	}
	for _, count := range []int{audit.RequestedRows, audit.ReturnedRows, audit.RowsInserted, audit.RowsUpdated, audit.RowsUnchanged, audit.RowsDeleted} {
		if count < 0 {
			return errors.New("source refresh row counts must be nonnegative")
		}
	}
	if audit.Outcome == SourceRefreshSuccess && audit.ErrorSummary != "" {
		return errors.New("successful source refresh has an error summary")
	}
	if audit.Outcome == SourceRefreshFailure && strings.TrimSpace(audit.ErrorSummary) == "" {
		return errors.New("failed source refresh error summary is blank")
	}
	if audit.Mode != SourceRefreshFull && audit.RowsDeleted != 0 {
		return errors.New("only full source refreshes may delete rows")
	}
	if (audit.Outcome == SourceRefreshFailure || audit.Mode == SourceRefreshRecalculate) && audit.DownstreamInputsChanged {
		return errors.New("failed or recalculated source refresh cannot change downstream inputs")
	}
	return nil
}

func validateSourceResourceScope(resource SourceResource, season, stage string) error {
	if !validSourceResource(resource) {
		return fmt.Errorf("invalid source resource %q", resource)
	}
	if resource == SourceResourceTeams {
		if season != "" || stage != "" {
			return errors.New("team source resource requires an empty season and stage")
		}
		return nil
	}
	if strings.TrimSpace(season) == "" || season != strings.TrimSpace(season) {
		return errors.New("source resource season must be nonblank and trimmed")
	}
	if strings.TrimSpace(stage) == "" || stage != strings.TrimSpace(stage) {
		return errors.New("source resource stage must be nonblank and trimmed")
	}
	return nil
}

func validSourceResource(value SourceResource) bool {
	switch value {
	case SourceResourceTeams, SourceResourceGames, SourceResourceGameXG:
		return true
	default:
		return false
	}
}

func validSourceRefreshMode(value SourceRefreshMode) bool {
	switch value {
	case SourceRefreshFull, SourceRefreshTargeted, SourceRefreshRecalculate:
		return true
	default:
		return false
	}
}

func validSourceRefreshOutcome(value SourceRefreshOutcome) bool {
	return value == SourceRefreshSuccess || value == SourceRefreshFailure
}
