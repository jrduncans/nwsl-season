package cache

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/standings"

	_ "modernc.org/sqlite"
)

const schemaVersion = 5

// DB wraps the SQLite cache.
type DB struct {
	db *sql.DB
}

// Team is the normalized team row stored in SQLite.
type Team struct {
	ASAID        string
	Name         string
	ShortName    string
	Abbreviation string
	RawJSON      string
}

// Game is the normalized game row stored in SQLite.
type Game struct {
	ASAID          string
	Season         string
	Stage          string
	KickoffUTC     string
	Status         string
	HomeTeamID     string
	AwayTeamID     string
	HomeScore      sql.NullInt64
	AwayScore      sql.NullInt64
	Matchday       sql.NullInt64
	LastUpdatedUTC string
	RawJSON        string
}

// SyncRun contains the audit row for a refresh attempt.
type SyncRun struct {
	ID                 int64
	StartedAt          time.Time
	FinishedAt         time.Time
	Season             string
	Stage              string
	Outcome            string
	ErrorSummary       string
	TeamsUpserted      int
	GamesUpserted      int
	GamesDeleted       int
	GamesSeen          int
	TeamsInserted      int
	TeamsUpdated       int
	TeamsUnchanged     int
	GamesInserted      int
	GamesUpdated       int
	GamesUnchanged     int
	Skipped            bool
	FixtureSnapshotID  string
	QualificationError string
	ScenarioError      string
	// XGRun/XGError describe the independent second refresh when available.
	XGRun   *XGSyncRun
	XGError string
}

// Status is the latest cache freshness summary.
type Status struct {
	LastAttempt *SyncRun
	LastSuccess *SyncRun
}

type XGAvailability string

const (
	XGAvailable   XGAvailability = "available"
	XGUnavailable XGAvailability = "unavailable"
)

type GameXG struct {
	GameID                 string
	Availability           XGAvailability
	HomeTeamID, AwayTeamID string
	HomeXG, AwayXG         sql.NullFloat64
	RawJSON                string
	FirstObservedAt        *time.Time
	LastCheckedAt          time.Time
}
type XGSyncRun struct {
	ID, RowsSeen, AvailableGames, UnavailableGames int64
	RowsInserted, RowsUpdated, RowsUnchanged       int64
	StartedAt, FinishedAt                          time.Time
	Season, Stage, Outcome, ErrorSummary           string
}
type XGStatus struct {
	LastAttempt *XGSyncRun
	LastSuccess *XGSyncRun
}

// RefreshSnapshot is the minimal cached state the background scheduler needs.
type RefreshSnapshot struct {
	Games       []Game
	LastAttempt *SyncRun
	LastSuccess *SyncRun
	XGoals      []GameXG
	XGStatus    XGStatus
}

// ErrSyncInProgress means another process holds the lease for this cache stream.
var ErrSyncInProgress = errors.New("cache sync already in progress")

// SeasonData is the cached input needed to render one season and calculate its
// standings. Games retain their presentation metadata as well as their scores.
type SeasonData struct {
	Teams             []standings.Team
	Games             []Game
	LastSuccess       *SyncRun
	XGoals            []GameXG
	XGStatus          XGStatus
	FixtureSnapshotID string
}

type QualificationRun struct {
	ID                                int64
	FixtureSnapshotID                 string
	SourceSyncRunID                   int64
	Season, Stage, RulesVersion       string
	StartedAt, FinishedAt             time.Time
	Outcome, ErrorSummary             string
	ExpectedStatuses, WrittenStatuses int
}
type QualificationStatus struct {
	TeamID                           string
	Achievement                      competition.AchievementID
	TopK                             int
	Status                           clinching.Status
	Method                           clinching.ProofMethod
	Reason                           string
	StrictlyAhead, AtLeastLevel      clinching.CountEvidence
	BlockingWitness, FrontierWitness []clinching.WitnessGame
	NoHelp                           clinching.NoHelpPath
	Diagnostics                      clinching.Diagnostics
}
type QualificationSnapshot struct {
	Run      QualificationRun
	Statuses []QualificationStatus
}
type ScenarioRun struct {
	ID, QualificationRunID, SourceSyncRunID        int64
	FixtureSnapshotID, Season, Stage, RulesVersion string
	DefinitionVersion                              string
	Slate                                          scenarios.Slate
	StartedAt, FinishedAt                          time.Time
	Outcome, ErrorSummary                          string
	ExpectedResults, WrittenResults                int
}
type ScenarioResult struct{ scenarios.Result }
type ScenarioSnapshot struct {
	Run     ScenarioRun
	Results []ScenarioResult
}

// Open opens a SQLite cache and applies migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("cache db path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create cache directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite cache: %w", err)
	}

	cache := &DB{db: db}
	if err := cache.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := cache.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return cache, nil
}

// Close closes the cache connection.
func (c *DB) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

func (c *DB) configure(ctx context.Context) error {
	statements := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	}
	for _, statement := range statements {
		if _, err := c.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite cache: %w", err)
		}
	}
	return nil
}

// Migrate creates the cache schema and upgrades earlier versions in place.
func (c *DB) Migrate(ctx context.Context) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer rollback(tx)

	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	version, err := migrationVersion(ctx, tx)
	if err != nil {
		return err
	}
	if version < 1 {
		for _, statement := range []string{
			`CREATE TABLE teams (
				asa_team_id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				short_name TEXT NOT NULL,
				abbreviation TEXT NOT NULL,
				raw_json TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE TABLE games (
				asa_game_id TEXT PRIMARY KEY,
				season TEXT NOT NULL,
				stage TEXT NOT NULL,
				kickoff_utc TEXT NOT NULL,
				status TEXT NOT NULL,
				home_team_id TEXT NOT NULL REFERENCES teams(asa_team_id),
				away_team_id TEXT NOT NULL REFERENCES teams(asa_team_id),
				home_score INTEGER,
				away_score INTEGER,
				matchday INTEGER,
				last_updated_utc TEXT NOT NULL,
				raw_json TEXT NOT NULL,
				synced_at TEXT NOT NULL
			)`,
			`CREATE INDEX games_season_stage_idx ON games (season, stage)`,
			`CREATE TABLE sync_runs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				started_at TEXT NOT NULL,
				finished_at TEXT NOT NULL,
				season TEXT NOT NULL,
				stage TEXT NOT NULL,
				outcome TEXT NOT NULL,
				error_summary TEXT NOT NULL,
				teams_upserted INTEGER NOT NULL,
				games_upserted INTEGER NOT NULL,
				games_deleted INTEGER NOT NULL,
				games_seen INTEGER NOT NULL
			)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 1: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 1); err != nil {
			return err
		}
		version = 1
	}
	if version < 2 {
		for _, statement := range []string{
			`ALTER TABLE sync_runs ADD COLUMN teams_inserted INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE sync_runs ADD COLUMN teams_updated INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE sync_runs ADD COLUMN teams_unchanged INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE sync_runs ADD COLUMN games_inserted INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE sync_runs ADD COLUMN games_updated INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE sync_runs ADD COLUMN games_unchanged INTEGER NOT NULL DEFAULT 0`,
			`CREATE TABLE sync_leases (
				lock_key TEXT PRIMARY KEY,
				holder TEXT NOT NULL,
				expires_at_unix_nano INTEGER NOT NULL
			)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 2: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 2); err != nil {
			return err
		}
		version = 2
	}
	if version < 3 {
		for _, statement := range []string{
			`CREATE TABLE game_xg (
				asa_game_id TEXT PRIMARY KEY REFERENCES games(asa_game_id) ON DELETE CASCADE,
				availability TEXT NOT NULL CHECK (availability IN ('available', 'unavailable')),
				home_team_id TEXT NOT NULL REFERENCES teams(asa_team_id),
				away_team_id TEXT NOT NULL REFERENCES teams(asa_team_id),
				home_xg REAL, away_xg REAL, raw_json TEXT NOT NULL,
				first_observed_at TEXT, last_checked_at TEXT NOT NULL,
				CHECK ((availability = 'available' AND home_xg IS NOT NULL AND away_xg IS NOT NULL AND first_observed_at IS NOT NULL) OR (availability = 'unavailable' AND home_xg IS NULL AND away_xg IS NULL AND first_observed_at IS NULL AND raw_json = ''))
			)`,
			`CREATE TABLE xg_sync_runs (
				id INTEGER PRIMARY KEY AUTOINCREMENT, started_at TEXT NOT NULL, finished_at TEXT NOT NULL,
				season TEXT NOT NULL, stage TEXT NOT NULL, outcome TEXT NOT NULL CHECK (outcome IN ('success','failure')),
				error_summary TEXT NOT NULL, rows_seen INTEGER NOT NULL, available_games INTEGER NOT NULL, unavailable_games INTEGER NOT NULL,
				rows_inserted INTEGER NOT NULL, rows_updated INTEGER NOT NULL, rows_unchanged INTEGER NOT NULL
			)`, `CREATE INDEX xg_sync_runs_season_stage_idx ON xg_sync_runs (season, stage, finished_at)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 3: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 3); err != nil {
			return err
		}
	}
	if version < 4 {
		for _, statement := range []string{
			`ALTER TABLE sync_runs ADD COLUMN fixture_snapshot_id TEXT NOT NULL DEFAULT ''`,
			`CREATE TABLE qualification_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, fixture_snapshot_id TEXT NOT NULL, source_sync_run_id INTEGER NOT NULL REFERENCES sync_runs(id), season TEXT NOT NULL, stage TEXT NOT NULL, rules_version TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT NOT NULL, outcome TEXT NOT NULL CHECK (outcome IN ('complete','failure')), error_summary TEXT NOT NULL, expected_statuses INTEGER NOT NULL, written_statuses INTEGER NOT NULL)`,
			`CREATE INDEX qualification_runs_lookup_idx ON qualification_runs (fixture_snapshot_id, rules_version, finished_at)`,
			`CREATE TABLE qualification_statuses (qualification_run_id INTEGER NOT NULL REFERENCES qualification_runs(id) ON DELETE CASCADE, team_id TEXT NOT NULL REFERENCES teams(asa_team_id), achievement TEXT NOT NULL, top_k INTEGER NOT NULL, status TEXT NOT NULL CHECK (status IN ('clinched','not_clinched','unresolved')), proof_method TEXT NOT NULL, reason TEXT NOT NULL, strictly_ahead_value INTEGER NOT NULL, strictly_ahead_kind TEXT NOT NULL CHECK (strictly_ahead_kind IN ('exact','lower_bound','upper_bound')), at_least_level_value INTEGER NOT NULL, at_least_level_kind TEXT NOT NULL CHECK (at_least_level_kind IN ('exact','lower_bound','upper_bound')), blocking_witness_json TEXT NOT NULL, frontier_witness_json TEXT NOT NULL, no_help_state TEXT NOT NULL, no_help_fixture_ids_json TEXT NOT NULL, no_help_reason TEXT NOT NULL, diagnostics_json TEXT NOT NULL, PRIMARY KEY (qualification_run_id,team_id,achievement))`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 4: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 4); err != nil {
			return err
		}
	}
	if version < 5 {
		for _, statement := range []string{
			`CREATE TABLE scenario_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, fixture_snapshot_id TEXT NOT NULL, qualification_run_id INTEGER NOT NULL REFERENCES qualification_runs(id), source_sync_run_id INTEGER NOT NULL REFERENCES sync_runs(id), season TEXT NOT NULL, stage TEXT NOT NULL, rules_version TEXT NOT NULL, definition_version TEXT NOT NULL, slate_id TEXT NOT NULL, slate_state TEXT NOT NULL CHECK (slate_state IN ('ready','no_upcoming_fixtures','unavailable')), slate_source TEXT NOT NULL, matchday INTEGER NOT NULL, starts_at_utc TEXT NOT NULL, latest_kickoff_utc TEXT NOT NULL, cutoff_utc TEXT NOT NULL, fixture_ids_json TEXT NOT NULL, slate_reason TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT NOT NULL, outcome TEXT NOT NULL CHECK (outcome IN ('complete','failure')), error_summary TEXT NOT NULL, expected_results INTEGER NOT NULL, written_results INTEGER NOT NULL)`,
			`CREATE INDEX scenario_runs_exact_idx ON scenario_runs (fixture_snapshot_id,rules_version,definition_version,finished_at)`,
			`CREATE INDEX scenario_runs_latest_idx ON scenario_runs (season,stage,rules_version,finished_at)`,
			`CREATE TABLE scenario_results (scenario_run_id INTEGER NOT NULL REFERENCES scenario_runs(id) ON DELETE CASCADE, team_id TEXT NOT NULL REFERENCES teams(asa_team_id), achievement TEXT NOT NULL, top_k INTEGER NOT NULL, opportunity_state TEXT NOT NULL CHECK (opportunity_state IN ('already_clinched','can_clinch','cannot_clinch','tiebreak_dependent','unresolved')), already_clinched INTEGER NOT NULL CHECK (already_clinched IN (0,1)), can_clinch INTEGER NOT NULL CHECK (can_clinch IN (0,1)), clauses_json TEXT NOT NULL, necessary_json TEXT NOT NULL, proof_methods_json TEXT NOT NULL, limitation TEXT NOT NULL, total_assignments INTEGER NOT NULL, certified_assignments INTEGER NOT NULL, unresolved_assignments INTEGER NOT NULL, diagnostics_json TEXT NOT NULL, PRIMARY KEY (scenario_run_id,team_id,achievement))`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 5: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 5); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func migrationVersion(ctx context.Context, tx *sql.Tx) (int, error) {
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	return version, nil
}

func recordMigration(ctx context.Context, tx *sql.Tx, version int) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	return nil
}

// ReplaceSeason atomically upserts teams and games, then deletes disappeared games.
func (c *DB) ReplaceSeason(ctx context.Context, season, stage string, teams []Team, games []Game, startedAt time.Time) (SyncRun, error) {
	now := time.Now().UTC()
	run := SyncRun{
		StartedAt:     startedAt.UTC(),
		FinishedAt:    now,
		Season:        season,
		Stage:         stage,
		Outcome:       "success",
		TeamsUpserted: len(teams),
		GamesUpserted: len(games),
		GamesSeen:     len(games),
	}
	snapshotID, err := FixtureSnapshotID(teams, games)
	if err != nil {
		return SyncRun{}, err
	}
	run.FixtureSnapshotID = snapshotID

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncRun{}, fmt.Errorf("begin cache refresh: %w", err)
	}
	defer rollback(tx)

	for _, team := range teams {
		change, err := writeTeam(ctx, tx, team, now)
		if err != nil {
			return SyncRun{}, err
		}
		switch change {
		case rowInserted:
			run.TeamsInserted++
		case rowUpdated:
			run.TeamsUpdated++
		case rowUnchanged:
			run.TeamsUnchanged++
		}
	}

	gameIDs := make([]string, 0, len(games))
	for _, game := range games {
		gameIDs = append(gameIDs, game.ASAID)
		change, err := writeGame(ctx, tx, game, now)
		if err != nil {
			return SyncRun{}, err
		}
		switch change {
		case rowInserted:
			run.GamesInserted++
		case rowUpdated:
			run.GamesUpdated++
		case rowUnchanged:
			run.GamesUnchanged++
		}
	}

	deleted, err := deleteMissingGames(ctx, tx, season, stage, gameIDs)
	if err != nil {
		return SyncRun{}, err
	}
	run.GamesDeleted = deleted

	if err := insertSyncRun(ctx, tx, &run); err != nil {
		return SyncRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncRun{}, fmt.Errorf("commit cache refresh: %w", err)
	}
	return run, nil
}

type rowChange int

const (
	rowInserted rowChange = iota
	rowUpdated
	rowUnchanged
)

func writeTeam(ctx context.Context, tx *sql.Tx, team Team, now time.Time) (rowChange, error) {
	var existing Team
	err := tx.QueryRowContext(ctx, `SELECT asa_team_id, name, short_name, abbreviation, raw_json FROM teams WHERE asa_team_id = ?`, team.ASAID).Scan(
		&existing.ASAID, &existing.Name, &existing.ShortName, &existing.Abbreviation, &existing.RawJSON)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO teams (
			asa_team_id, name, short_name, abbreviation, raw_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`, team.ASAID, team.Name, team.ShortName, team.Abbreviation, team.RawJSON, formatTime(now)); err != nil {
			return 0, fmt.Errorf("insert team %q: %w", team.ASAID, err)
		}
		return rowInserted, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load team %q: %w", team.ASAID, err)
	}
	if existing == team {
		return rowUnchanged, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE teams SET
		name = ?, short_name = ?, abbreviation = ?, raw_json = ?, updated_at = ?
		WHERE asa_team_id = ?`, team.Name, team.ShortName, team.Abbreviation, team.RawJSON, formatTime(now), team.ASAID); err != nil {
		return 0, fmt.Errorf("update team %q: %w", team.ASAID, err)
	}
	return rowUpdated, nil
}

func writeGame(ctx context.Context, tx *sql.Tx, game Game, now time.Time) (rowChange, error) {
	var existing Game
	err := tx.QueryRowContext(ctx, `SELECT
		asa_game_id, season, stage, kickoff_utc, status, home_team_id, away_team_id,
		home_score, away_score, matchday, last_updated_utc, raw_json
		FROM games WHERE asa_game_id = ?`, game.ASAID).Scan(
		&existing.ASAID, &existing.Season, &existing.Stage, &existing.KickoffUTC, &existing.Status,
		&existing.HomeTeamID, &existing.AwayTeamID, &existing.HomeScore, &existing.AwayScore,
		&existing.Matchday, &existing.LastUpdatedUTC, &existing.RawJSON)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO games (
			asa_game_id, season, stage, kickoff_utc, status, home_team_id, away_team_id,
			home_score, away_score, matchday, last_updated_utc, raw_json, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			game.ASAID, game.Season, game.Stage, game.KickoffUTC, game.Status, game.HomeTeamID, game.AwayTeamID,
			nullableInt(game.HomeScore), nullableInt(game.AwayScore), nullableInt(game.Matchday), game.LastUpdatedUTC, game.RawJSON, formatTime(now)); err != nil {
			return 0, fmt.Errorf("insert game %q: %w", game.ASAID, err)
		}
		return rowInserted, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load game %q: %w", game.ASAID, err)
	}
	if equalGame(existing, game) {
		return rowUnchanged, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE games SET
		season = ?, stage = ?, kickoff_utc = ?, status = ?, home_team_id = ?, away_team_id = ?,
		home_score = ?, away_score = ?, matchday = ?, last_updated_utc = ?, raw_json = ?, synced_at = ?
		WHERE asa_game_id = ?`,
		game.Season, game.Stage, game.KickoffUTC, game.Status, game.HomeTeamID, game.AwayTeamID,
		nullableInt(game.HomeScore), nullableInt(game.AwayScore), nullableInt(game.Matchday), game.LastUpdatedUTC, game.RawJSON, formatTime(now), game.ASAID); err != nil {
		return 0, fmt.Errorf("update game %q: %w", game.ASAID, err)
	}
	return rowUpdated, nil
}

func equalGame(left, right Game) bool {
	return left.ASAID == right.ASAID && left.Season == right.Season && left.Stage == right.Stage &&
		left.KickoffUTC == right.KickoffUTC && left.Status == right.Status &&
		left.HomeTeamID == right.HomeTeamID && left.AwayTeamID == right.AwayTeamID &&
		left.HomeScore == right.HomeScore && left.AwayScore == right.AwayScore &&
		left.Matchday == right.Matchday && left.LastUpdatedUTC == right.LastUpdatedUTC && left.RawJSON == right.RawJSON
}

// RecordFailure records a failed refresh attempt without mutating cached data.
func (c *DB) RecordFailure(ctx context.Context, season, stage string, startedAt time.Time, cause error) error {
	run := SyncRun{
		StartedAt:    startedAt.UTC(),
		FinishedAt:   time.Now().UTC(),
		Season:       season,
		Stage:        stage,
		Outcome:      "failure",
		ErrorSummary: summarizeError(cause),
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO sync_runs (
		started_at, finished_at, season, stage, outcome, error_summary,
		teams_upserted, games_upserted, games_deleted, games_seen,
		teams_inserted, teams_updated, teams_unchanged, games_inserted, games_updated, games_unchanged
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(run.StartedAt), formatTime(run.FinishedAt), run.Season, run.Stage, run.Outcome, run.ErrorSummary,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("record failed sync run: %w", err)
	}
	return nil
}

// Status returns the last attempted and successful sync runs for a season and stage.
func (c *DB) Status(ctx context.Context, season, stage string) (Status, error) {
	attempt, err := c.LastAttempt(ctx, season, stage)
	if err != nil {
		return Status{}, err
	}
	success, err := c.LastSuccess(ctx, season, stage)
	if err != nil {
		return Status{}, err
	}
	return Status{LastAttempt: attempt, LastSuccess: success}, nil
}

// LastAttempt returns the latest attempted sync for a season and stage.
func (c *DB) LastAttempt(ctx context.Context, season, stage string) (*SyncRun, error) {
	return c.latestRun(ctx, "", season, stage)
}

// LastSuccess returns the latest successful sync for a season and stage.
func (c *DB) LastSuccess(ctx context.Context, season, stage string) (*SyncRun, error) {
	return c.latestRun(ctx, "success", season, stage)
}

// RefreshSnapshot returns the fixtures and audit data needed for a refresh decision.
func (c *DB) RefreshSnapshot(ctx context.Context, season, stage string) (RefreshSnapshot, error) {
	games, err := c.seasonGames(ctx, season, stage)
	if err != nil {
		return RefreshSnapshot{}, err
	}
	status, err := c.Status(ctx, season, stage)
	if err != nil {
		return RefreshSnapshot{}, err
	}
	xgoals, err := c.seasonXGoals(ctx, season, stage)
	if err != nil {
		return RefreshSnapshot{}, err
	}
	xgStatus, err := c.XGStatus(ctx, season, stage)
	if err != nil {
		return RefreshSnapshot{}, err
	}
	return RefreshSnapshot{Games: games, LastAttempt: status.LastAttempt, LastSuccess: status.LastSuccess, XGoals: xgoals, XGStatus: xgStatus}, nil
}

// TryAcquireSyncLease atomically obtains a short-lived cross-process sync lease.
func (c *DB) TryAcquireSyncLease(ctx context.Context, key, holder string, expiresAt time.Time) (bool, error) {
	result, err := c.db.ExecContext(ctx, `INSERT INTO sync_leases (lock_key, holder, expires_at_unix_nano)
		VALUES (?, ?, ?)
		ON CONFLICT(lock_key) DO UPDATE SET holder = excluded.holder, expires_at_unix_nano = excluded.expires_at_unix_nano
		WHERE sync_leases.expires_at_unix_nano <= ?`, key, holder, expiresAt.UnixNano(), time.Now().UTC().UnixNano())
	if err != nil {
		return false, fmt.Errorf("acquire sync lease: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect sync lease: %w", err)
	}
	return changed == 1, nil
}

// ReleaseSyncLease releases a lease held by this run and leaves another holder untouched.
func (c *DB) ReleaseSyncLease(ctx context.Context, key, holder string) error {
	if _, err := c.db.ExecContext(ctx, `DELETE FROM sync_leases WHERE lock_key = ? AND holder = ?`, key, holder); err != nil {
		return fmt.Errorf("release sync lease: %w", err)
	}
	return nil
}

// CountGames returns cached games for tests and diagnostics.
func (c *DB) CountGames(ctx context.Context, season, stage string) (int, error) {
	var count int
	if err := c.db.QueryRowContext(ctx, `SELECT count(*) FROM games WHERE season = ? AND stage = ?`, season, stage).Scan(&count); err != nil {
		return 0, fmt.Errorf("count games: %w", err)
	}
	return count, nil
}

// GameByID returns one cached game for tests and diagnostics.
func (c *DB) GameByID(ctx context.Context, id string) (Game, error) {
	var game Game
	var homeScore, awayScore, matchday sql.NullInt64
	if err := c.db.QueryRowContext(ctx, `SELECT
		asa_game_id, season, stage, kickoff_utc, status, home_team_id, away_team_id,
		home_score, away_score, matchday, last_updated_utc, raw_json
		FROM games WHERE asa_game_id = ?`, id).Scan(
		&game.ASAID, &game.Season, &game.Stage, &game.KickoffUTC, &game.Status, &game.HomeTeamID, &game.AwayTeamID,
		&homeScore, &awayScore, &matchday, &game.LastUpdatedUTC, &game.RawJSON); err != nil {
		return Game{}, fmt.Errorf("load game %q: %w", id, err)
	}
	game.HomeScore = homeScore
	game.AwayScore = awayScore
	game.Matchday = matchday
	return game, nil
}

// StandingsInputs loads teams and games for a season and stage.
func (c *DB) StandingsInputs(ctx context.Context, season, stage string) ([]standings.Team, []standings.Game, error) {
	teams, err := c.standingsTeams(ctx, season, stage)
	if err != nil {
		return nil, nil, err
	}
	games, err := c.standingsGames(ctx, season, stage)
	if err != nil {
		return nil, nil, err
	}
	return teams, games, nil
}

// Season loads the teams, fixtures, and freshness information for a season and
// stage. It never refreshes data from the upstream source.
func (c *DB) Season(ctx context.Context, season, stage string) (SeasonData, error) {
	teams, err := c.standingsTeams(ctx, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	games, err := c.seasonGames(ctx, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	lastSuccess, err := c.LastSuccess(ctx, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	xgoals, err := c.seasonXGoals(ctx, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	xgStatus, err := c.XGStatus(ctx, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	snapshotID := ""
	if lastSuccess != nil {
		snapshotID = lastSuccess.FixtureSnapshotID
	}
	return SeasonData{Teams: teams, Games: games, LastSuccess: lastSuccess, XGoals: xgoals, XGStatus: xgStatus, FixtureSnapshotID: snapshotID}, nil
}

// ReplaceGameXG atomically replaces a complete xG response after validating it
// against the already committed fixture snapshot. Missing newly completed games
// become explicit unavailable markers; previously available values may not be
// omitted by a later response.
func (c *DB) ReplaceGameXG(ctx context.Context, season, stage string, games []Game, values []GameXG, startedAt time.Time) (XGSyncRun, error) {
	now := time.Now().UTC()
	run := XGSyncRun{StartedAt: startedAt.UTC(), FinishedAt: now, Season: season, Stage: stage, Outcome: "success", RowsSeen: int64(len(values))}
	fixtures := map[string]Game{}
	teams := map[string]struct{}{}
	for _, game := range games {
		if game.Season != season || game.Stage != stage || game.ASAID == "" {
			return XGSyncRun{}, fmt.Errorf("invalid xG fixture snapshot")
		}
		fixtures[game.ASAID] = game
		teams[game.HomeTeamID] = struct{}{}
		teams[game.AwayTeamID] = struct{}{}
	}
	seen := map[string]GameXG{}
	for _, value := range values {
		if value.GameID == "" {
			return XGSyncRun{}, errors.New("xG row has empty game ID")
		}
		if _, ok := seen[value.GameID]; ok {
			return XGSyncRun{}, fmt.Errorf("duplicate xG game %q", value.GameID)
		}
		game, ok := fixtures[value.GameID]
		if !ok {
			return XGSyncRun{}, fmt.Errorf("xG game %q is not in fixture snapshot", value.GameID)
		}
		if game.Status != "FullTime" {
			return XGSyncRun{}, fmt.Errorf("xG game %q is not completed", value.GameID)
		}
		if value.HomeTeamID != game.HomeTeamID || value.AwayTeamID != game.AwayTeamID {
			return XGSyncRun{}, fmt.Errorf("xG game %q team identity mismatch", value.GameID)
		}
		if _, ok := teams[value.HomeTeamID]; !ok {
			return XGSyncRun{}, fmt.Errorf("xG game %q has unknown team", value.GameID)
		}
		if value.Availability != XGAvailable || !value.HomeXG.Valid || !value.AwayXG.Valid || !finiteNonnegative(value.HomeXG.Float64) || !finiteNonnegative(value.AwayXG.Float64) {
			return XGSyncRun{}, fmt.Errorf("xG game %q has invalid values", value.GameID)
		}
		seen[value.GameID] = value
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return XGSyncRun{}, fmt.Errorf("begin xG refresh: %w", err)
	}
	defer rollback(tx)
	// An upstream omission must never erase a previously good value.
	for id, game := range fixtures {
		if game.Status != "FullTime" {
			continue
		}
		var availability string
		err := tx.QueryRowContext(ctx, `SELECT availability FROM game_xg WHERE asa_game_id=?`, id).Scan(&availability)
		if err == nil && availability == string(XGAvailable) {
			if _, ok := seen[id]; !ok {
				return XGSyncRun{}, fmt.Errorf("xG response omitted previously available game %q", id)
			}
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return XGSyncRun{}, fmt.Errorf("check existing xG %q: %w", id, err)
		}
	}
	for id, game := range fixtures {
		if game.Status != "FullTime" {
			continue
		}
		value, available := seen[id]
		if !available {
			value = GameXG{GameID: id, Availability: XGUnavailable, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID}
			run.UnavailableGames++
		} else {
			run.AvailableGames++
		}
		change, err := writeGameXG(ctx, tx, value, now)
		if err != nil {
			return XGSyncRun{}, err
		}
		switch change {
		case rowInserted:
			run.RowsInserted++
		case rowUpdated:
			run.RowsUpdated++
		case rowUnchanged:
			run.RowsUnchanged++
		}
	}
	if err := insertXGSyncRun(ctx, tx, &run); err != nil {
		return XGSyncRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return XGSyncRun{}, fmt.Errorf("commit xG refresh: %w", err)
	}
	return run, nil
}

func finiteNonnegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
func writeGameXG(ctx context.Context, tx *sql.Tx, value GameXG, now time.Time) (rowChange, error) {
	var old GameXG
	var first sql.NullString
	var checked string
	err := tx.QueryRowContext(ctx, `SELECT availability,home_team_id,away_team_id,home_xg,away_xg,raw_json,first_observed_at,last_checked_at FROM game_xg WHERE asa_game_id=?`, value.GameID).Scan(&old.Availability, &old.HomeTeamID, &old.AwayTeamID, &old.HomeXG, &old.AwayXG, &old.RawJSON, &first, &checked)
	if first.Valid {
		parsed, e := time.Parse(time.RFC3339, first.String)
		if e != nil {
			return 0, e
		}
		old.FirstObservedAt = &parsed
	}
	if errors.Is(err, sql.ErrNoRows) {
		firstValue := any(nil)
		home, away := any(nil), any(nil)
		raw := ""
		if value.Availability == XGAvailable {
			firstValue = formatTime(now)
			home = value.HomeXG.Float64
			away = value.AwayXG.Float64
			raw = value.RawJSON
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO game_xg (asa_game_id,availability,home_team_id,away_team_id,home_xg,away_xg,raw_json,first_observed_at,last_checked_at) VALUES (?,?,?,?,?,?,?,?,?)`, value.GameID, value.Availability, value.HomeTeamID, value.AwayTeamID, home, away, raw, firstValue, formatTime(now))
		if e != nil {
			return 0, fmt.Errorf("insert xG %q: %w", value.GameID, e)
		}
		return rowInserted, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load xG %q: %w", value.GameID, err)
	}
	material := old.Availability != value.Availability || old.HomeTeamID != value.HomeTeamID || old.AwayTeamID != value.AwayTeamID || old.HomeXG != value.HomeXG || old.AwayXG != value.AwayXG || (value.Availability == XGAvailable && old.RawJSON != value.RawJSON)
	firstValue := any(nil)
	home, away := any(nil), any(nil)
	raw := ""
	if value.Availability == XGAvailable {
		if old.FirstObservedAt != nil {
			firstValue = formatTime(*old.FirstObservedAt)
		} else {
			firstValue = formatTime(now)
		}
		home = value.HomeXG.Float64
		away = value.AwayXG.Float64
		raw = value.RawJSON
	}
	_, err = tx.ExecContext(ctx, `UPDATE game_xg SET availability=?,home_team_id=?,away_team_id=?,home_xg=?,away_xg=?,raw_json=?,first_observed_at=?,last_checked_at=? WHERE asa_game_id=?`, value.Availability, value.HomeTeamID, value.AwayTeamID, home, away, raw, firstValue, formatTime(now), value.GameID)
	if err != nil {
		return 0, fmt.Errorf("update xG %q: %w", value.GameID, err)
	}
	if material {
		return rowUpdated, nil
	}
	return rowUnchanged, nil
}

func (c *DB) RecordXGFailure(ctx context.Context, season, stage string, startedAt time.Time, cause error) error {
	run := XGSyncRun{StartedAt: startedAt.UTC(), FinishedAt: time.Now().UTC(), Season: season, Stage: stage, Outcome: "failure", ErrorSummary: summarizeError(cause)}
	if err := insertXGSyncRun(ctx, c.db, &run); err != nil {
		return err
	}
	return nil
}
func insertXGSyncRun(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, run *XGSyncRun) error {
	result, err := exec.ExecContext(ctx, `INSERT INTO xg_sync_runs (started_at,finished_at,season,stage,outcome,error_summary,rows_seen,available_games,unavailable_games,rows_inserted,rows_updated,rows_unchanged) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, formatTime(run.StartedAt), formatTime(run.FinishedAt), run.Season, run.Stage, run.Outcome, run.ErrorSummary, run.RowsSeen, run.AvailableGames, run.UnavailableGames, run.RowsInserted, run.RowsUpdated, run.RowsUnchanged)
	if err != nil {
		return fmt.Errorf("record xG sync run: %w", err)
	}
	if id, e := result.LastInsertId(); e == nil {
		run.ID = id
	}
	return nil
}
func (c *DB) XGStatus(ctx context.Context, season, stage string) (XGStatus, error) {
	a, e := c.latestXGRun(ctx, "", season, stage)
	if e != nil {
		return XGStatus{}, e
	}
	s, e := c.latestXGRun(ctx, "success", season, stage)
	return XGStatus{a, s}, e
}
func (c *DB) latestXGRun(ctx context.Context, outcome, season, stage string) (*XGSyncRun, error) {
	q := `SELECT id,started_at,finished_at,season,stage,outcome,error_summary,rows_seen,available_games,unavailable_games,rows_inserted,rows_updated,rows_unchanged FROM xg_sync_runs WHERE season=? AND stage=?`
	args := []any{season, stage}
	if outcome != "" {
		q += ` AND outcome=?`
		args = append(args, outcome)
	}
	q += ` ORDER BY finished_at DESC,id DESC LIMIT 1`
	var run XGSyncRun
	var st, fi string
	err := c.db.QueryRowContext(ctx, q, args...).Scan(&run.ID, &st, &fi, &run.Season, &run.Stage, &run.Outcome, &run.ErrorSummary, &run.RowsSeen, &run.AvailableGames, &run.UnavailableGames, &run.RowsInserted, &run.RowsUpdated, &run.RowsUnchanged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load xG status: %w", err)
	}
	run.StartedAt, err = time.Parse(time.RFC3339, st)
	if err != nil {
		return nil, err
	}
	run.FinishedAt, err = time.Parse(time.RFC3339, fi)
	if err != nil {
		return nil, err
	}
	return &run, nil
}
func (c *DB) seasonXGoals(ctx context.Context, season, stage string) ([]GameXG, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT x.asa_game_id,x.availability,x.home_team_id,x.away_team_id,x.home_xg,x.away_xg,x.raw_json,x.first_observed_at,x.last_checked_at FROM game_xg x JOIN games g ON g.asa_game_id=x.asa_game_id WHERE g.season=? AND g.stage=? ORDER BY g.kickoff_utc,g.asa_game_id`, season, stage)
	if err != nil {
		return nil, fmt.Errorf("load xG: %w", err)
	}
	defer rows.Close()
	values := []GameXG{}
	for rows.Next() {
		var v GameXG
		var first sql.NullString
		var checked string
		if err := rows.Scan(&v.GameID, &v.Availability, &v.HomeTeamID, &v.AwayTeamID, &v.HomeXG, &v.AwayXG, &v.RawJSON, &first, &checked); err != nil {
			return nil, err
		}
		if first.Valid {
			t, e := time.Parse(time.RFC3339, first.String)
			if e != nil {
				return nil, e
			}
			v.FirstObservedAt = &t
		}
		v.LastCheckedAt, err = time.Parse(time.RFC3339, checked)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func (c *DB) standingsTeams(ctx context.Context, season, stage string) ([]standings.Team, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT DISTINCT
		t.asa_team_id, t.name, t.short_name, t.abbreviation
		FROM teams t
		JOIN games g ON g.home_team_id = t.asa_team_id OR g.away_team_id = t.asa_team_id
		WHERE g.season = ? AND g.stage = ?
		ORDER BY t.name, t.asa_team_id`, season, stage)
	if err != nil {
		return nil, fmt.Errorf("load standings teams: %w", err)
	}
	defer rows.Close()

	teams := []standings.Team{}
	for rows.Next() {
		var team standings.Team
		if err := rows.Scan(&team.ID, &team.Name, &team.ShortName, &team.Abbreviation); err != nil {
			return nil, fmt.Errorf("scan standings team: %w", err)
		}
		teams = append(teams, team)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate standings teams: %w", err)
	}
	return teams, nil
}

func (c *DB) standingsGames(ctx context.Context, season, stage string) ([]standings.Game, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT
		asa_game_id, status, home_team_id, away_team_id, home_score, away_score
		FROM games
		WHERE season = ? AND stage = ?
		ORDER BY kickoff_utc, asa_game_id`, season, stage)
	if err != nil {
		return nil, fmt.Errorf("load standings games: %w", err)
	}
	defer rows.Close()

	games := []standings.Game{}
	for rows.Next() {
		var game standings.Game
		var homeScore, awayScore sql.NullInt64
		if err := rows.Scan(&game.ID, &game.Status, &game.HomeTeamID, &game.AwayTeamID, &homeScore, &awayScore); err != nil {
			return nil, fmt.Errorf("scan standings game: %w", err)
		}
		game.HomeScore = intPtrFromNull(homeScore)
		game.AwayScore = intPtrFromNull(awayScore)
		games = append(games, game)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate standings games: %w", err)
	}
	return games, nil
}

func (c *DB) seasonGames(ctx context.Context, season, stage string) ([]Game, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT
		asa_game_id, season, stage, kickoff_utc, status, home_team_id, away_team_id,
		home_score, away_score, matchday, last_updated_utc, raw_json
		FROM games
		WHERE season = ? AND stage = ?
		ORDER BY kickoff_utc, asa_game_id`, season, stage)
	if err != nil {
		return nil, fmt.Errorf("load season games: %w", err)
	}
	defer rows.Close()

	games := []Game{}
	for rows.Next() {
		var game Game
		if err := rows.Scan(
			&game.ASAID, &game.Season, &game.Stage, &game.KickoffUTC, &game.Status,
			&game.HomeTeamID, &game.AwayTeamID, &game.HomeScore, &game.AwayScore,
			&game.Matchday, &game.LastUpdatedUTC, &game.RawJSON,
		); err != nil {
			return nil, fmt.Errorf("scan season game: %w", err)
		}
		games = append(games, game)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate season games: %w", err)
	}
	return games, nil
}

func (c *DB) latestRun(ctx context.Context, outcome, season, stage string) (*SyncRun, error) {
	query := `SELECT id, started_at, finished_at, season, stage, outcome, error_summary, fixture_snapshot_id,
		teams_upserted, games_upserted, games_deleted, games_seen,
		teams_inserted, teams_updated, teams_unchanged, games_inserted, games_updated, games_unchanged FROM sync_runs`
	args := []any{}
	if outcome != "" {
		query += ` WHERE outcome = ?`
		args = append(args, outcome)
	}
	if season != "" {
		query += conjunction(args) + ` season = ?`
		args = append(args, season)
	}
	if stage != "" {
		query += conjunction(args) + ` stage = ?`
		args = append(args, stage)
	}
	query += ` ORDER BY finished_at DESC, id DESC LIMIT 1`

	var run SyncRun
	var startedAt, finishedAt string
	err := c.db.QueryRowContext(ctx, query, args...).Scan(
		&run.ID, &startedAt, &finishedAt, &run.Season, &run.Stage, &run.Outcome, &run.ErrorSummary, &run.FixtureSnapshotID,
		&run.TeamsUpserted, &run.GamesUpserted, &run.GamesDeleted, &run.GamesSeen,
		&run.TeamsInserted, &run.TeamsUpdated, &run.TeamsUnchanged,
		&run.GamesInserted, &run.GamesUpdated, &run.GamesUnchanged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load sync status: %w", err)
	}
	run.StartedAt, err = time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return nil, fmt.Errorf("parse sync start time: %w", err)
	}
	run.FinishedAt, err = time.Parse(time.RFC3339, finishedAt)
	if err != nil {
		return nil, fmt.Errorf("parse sync finish time: %w", err)
	}
	return &run, nil
}

func conjunction(args []any) string {
	if len(args) == 0 {
		return " WHERE"
	}
	return " AND"
}

func deleteMissingGames(ctx context.Context, tx *sql.Tx, season, stage string, gameIDs []string) (int, error) {
	if len(gameIDs) == 0 {
		return 0, errors.New("refusing to delete games for an empty complete-season response")
	}

	keep := make(map[string]struct{}, len(gameIDs))
	for _, id := range gameIDs {
		keep[id] = struct{}{}
	}

	rows, err := tx.QueryContext(ctx, `SELECT asa_game_id FROM games WHERE season = ? AND stage = ?`, season, stage)
	if err != nil {
		return 0, fmt.Errorf("load existing game ids: %w", err)
	}
	defer rows.Close()

	deleteIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan existing game id: %w", err)
		}
		if _, ok := keep[id]; !ok {
			deleteIDs = append(deleteIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate existing game ids: %w", err)
	}

	for _, id := range deleteIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM games WHERE asa_game_id = ?`, id); err != nil {
			return 0, fmt.Errorf("delete missing game %q: %w", id, err)
		}
	}
	return len(deleteIDs), nil
}

func insertSyncRun(ctx context.Context, tx *sql.Tx, run *SyncRun) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO sync_runs (
		started_at, finished_at, season, stage, outcome, error_summary, fixture_snapshot_id,
		teams_upserted, games_upserted, games_deleted, games_seen,
		teams_inserted, teams_updated, teams_unchanged, games_inserted, games_updated, games_unchanged
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(run.StartedAt), formatTime(run.FinishedAt), run.Season, run.Stage, run.Outcome, run.ErrorSummary, run.FixtureSnapshotID,
		run.TeamsUpserted, run.GamesUpserted, run.GamesDeleted, run.GamesSeen,
		run.TeamsInserted, run.TeamsUpdated, run.TeamsUnchanged,
		run.GamesInserted, run.GamesUpdated, run.GamesUnchanged)
	if err != nil {
		return fmt.Errorf("record successful sync run: %w", err)
	}
	id, err := result.LastInsertId()
	if err == nil {
		run.ID = id
	}
	return nil
}

func nullableInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func intPtrFromNull(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

// FixtureSnapshotID is a stable identity for the season's participating clubs
// and fixture state. It deliberately excludes presentation and fetch metadata.
func FixtureSnapshotID(teams []Team, games []Game) (string, error) {
	participants := map[string]bool{}
	for _, g := range games {
		if g.ASAID == "" || g.HomeTeamID == "" || g.AwayTeamID == "" {
			return "", errors.New("invalid fixture snapshot game")
		}
		participants[g.HomeTeamID], participants[g.AwayTeamID] = true, true
	}
	known := map[string]bool{}
	for _, t := range teams {
		if t.ASAID == "" || known[t.ASAID] {
			return "", errors.New("invalid fixture snapshot team")
		}
		known[t.ASAID] = true
	}
	ids := make([]string, 0, len(participants))
	for id := range participants {
		if !known[id] {
			return "", fmt.Errorf("fixture references unknown team %q", id)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ordered := append([]Game(nil), games...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ASAID < ordered[j].ASAID })
	h := sha256.New()
	writeSnapshotString := func(s string) {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(len(s)))
		h.Write(b[:])
		h.Write([]byte(s))
	}
	writeNull := func(v sql.NullInt64) {
		if v.Valid {
			writeSnapshotString("1")
			writeSnapshotString(strconv.FormatInt(v.Int64, 10))
		} else {
			writeSnapshotString("0")
		}
	}
	writeSnapshotString("fixture-snapshot-v2")
	for _, id := range ids {
		writeSnapshotString(id)
	}
	for _, g := range ordered {
		writeSnapshotString(g.ASAID)
		writeSnapshotString(g.Status)
		writeSnapshotString(g.HomeTeamID)
		writeSnapshotString(g.AwayTeamID)
		writeNull(g.HomeScore)
		writeNull(g.AwayScore)
		writeSnapshotString(g.KickoffUTC)
		writeNull(g.Matchday)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *DB) QualificationForSnapshot(ctx context.Context, snapshotID, rulesVersion string) (QualificationSnapshot, bool, error) {
	var out QualificationSnapshot
	var started, finished string
	err := c.db.QueryRowContext(ctx, `SELECT id,fixture_snapshot_id,source_sync_run_id,season,stage,rules_version,started_at,finished_at,outcome,error_summary,expected_statuses,written_statuses FROM qualification_runs WHERE fixture_snapshot_id=? AND rules_version=? AND outcome='complete' ORDER BY finished_at DESC,id DESC LIMIT 1`, snapshotID, rulesVersion).Scan(&out.Run.ID, &out.Run.FixtureSnapshotID, &out.Run.SourceSyncRunID, &out.Run.Season, &out.Run.Stage, &out.Run.RulesVersion, &started, &finished, &out.Run.Outcome, &out.Run.ErrorSummary, &out.Run.ExpectedStatuses, &out.Run.WrittenStatuses)
	if errors.Is(err, sql.ErrNoRows) {
		return QualificationSnapshot{}, false, nil
	}
	if err != nil {
		return out, false, fmt.Errorf("load qualification run: %w", err)
	}
	out.Run.StartedAt, err = time.Parse(time.RFC3339, started)
	if err != nil {
		return out, false, err
	}
	out.Run.FinishedAt, err = time.Parse(time.RFC3339, finished)
	if err != nil {
		return out, false, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT team_id,achievement,top_k,status,proof_method,reason,strictly_ahead_value,strictly_ahead_kind,at_least_level_value,at_least_level_kind,blocking_witness_json,frontier_witness_json,no_help_state,no_help_fixture_ids_json,no_help_reason,diagnostics_json FROM qualification_statuses WHERE qualification_run_id=? ORDER BY team_id,achievement`, out.Run.ID)
	if err != nil {
		return out, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var v QualificationStatus
		var achievement, status, method, nohelp string
		var block, frontier, fixtureIDs, diagnostics string
		if err := rows.Scan(&v.TeamID, &achievement, &v.TopK, &status, &method, &v.Reason, &v.StrictlyAhead.Value, &v.StrictlyAhead.Kind, &v.AtLeastLevel.Value, &v.AtLeastLevel.Kind, &block, &frontier, &nohelp, &fixtureIDs, &v.NoHelp.Reason, &diagnostics); err != nil {
			return out, false, err
		}
		v.Achievement = competition.AchievementID(achievement)
		v.Status = clinching.Status(status)
		v.Method = clinching.ProofMethod(method)
		v.NoHelp.State = clinching.NoHelpState(nohelp)
		if err := json.Unmarshal([]byte(block), &v.BlockingWitness); err != nil {
			return out, false, err
		}
		if err := json.Unmarshal([]byte(frontier), &v.FrontierWitness); err != nil {
			return out, false, err
		}
		if err := json.Unmarshal([]byte(fixtureIDs), &v.NoHelp.FixtureIDs); err != nil {
			return out, false, err
		}
		if err := json.Unmarshal([]byte(diagnostics), &v.Diagnostics); err != nil {
			return out, false, err
		}
		out.Statuses = append(out.Statuses, v)
	}
	if err := rows.Err(); err != nil {
		return out, false, err
	}
	return out, true, nil
}

func (c *DB) ReplaceQualification(ctx context.Context, run QualificationRun, values []QualificationStatus) (QualificationSnapshot, error) {
	if run.FixtureSnapshotID == "" || run.RulesVersion == "" || run.SourceSyncRunID == 0 || run.ExpectedStatuses != len(values) || run.WrittenStatuses != len(values) {
		return QualificationSnapshot{}, errors.New("invalid qualification batch counts")
	}
	seen := map[string]bool{}
	for _, v := range values {
		if v.TeamID == "" || v.Achievement == "" || v.TopK < 1 || !validQualification(v) {
			return QualificationSnapshot{}, errors.New("invalid qualification status")
		}
		k := v.TeamID + "\x00" + string(v.Achievement)
		if seen[k] {
			return QualificationSnapshot{}, errors.New("duplicate qualification status")
		}
		seen[k] = true
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return QualificationSnapshot{}, err
	}
	defer rollback(tx)
	run.Outcome = "complete"
	run.FinishedAt = time.Now().UTC()
	if run.StartedAt.IsZero() {
		run.StartedAt = run.FinishedAt
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO qualification_runs(fixture_snapshot_id,source_sync_run_id,season,stage,rules_version,started_at,finished_at,outcome,error_summary,expected_statuses,written_statuses) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, run.FixtureSnapshotID, run.SourceSyncRunID, run.Season, run.Stage, run.RulesVersion, formatTime(run.StartedAt), formatTime(run.FinishedAt), run.Outcome, run.ErrorSummary, run.ExpectedStatuses, run.WrittenStatuses)
	if err != nil {
		return QualificationSnapshot{}, err
	}
	run.ID, _ = res.LastInsertId()
	for _, v := range values {
		block, _ := json.Marshal(nonNilWitness(v.BlockingWitness))
		frontier, _ := json.Marshal(nonNilWitness(v.FrontierWitness))
		fixtures, _ := json.Marshal(nonNilStrings(v.NoHelp.FixtureIDs))
		diagnostics, _ := json.Marshal(v.Diagnostics)
		if _, err := tx.ExecContext(ctx, `INSERT INTO qualification_statuses VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, v.TeamID, v.Achievement, v.TopK, v.Status, v.Method, v.Reason, v.StrictlyAhead.Value, v.StrictlyAhead.Kind, v.AtLeastLevel.Value, v.AtLeastLevel.Kind, string(block), string(frontier), v.NoHelp.State, string(fixtures), v.NoHelp.Reason, string(diagnostics)); err != nil {
			return QualificationSnapshot{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return QualificationSnapshot{}, err
	}
	return QualificationSnapshot{Run: run, Statuses: append([]QualificationStatus(nil), values...)}, nil
}
func (c *DB) RecordQualificationFailure(ctx context.Context, run QualificationRun, cause error) error {
	run.Outcome = "failure"
	run.ErrorSummary = summarizeError(cause)
	run.FinishedAt = time.Now().UTC()
	if run.StartedAt.IsZero() {
		run.StartedAt = run.FinishedAt
	}
	_, err := c.db.ExecContext(ctx, `INSERT INTO qualification_runs(fixture_snapshot_id,source_sync_run_id,season,stage,rules_version,started_at,finished_at,outcome,error_summary,expected_statuses,written_statuses) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, run.FixtureSnapshotID, run.SourceSyncRunID, run.Season, run.Stage, run.RulesVersion, formatTime(run.StartedAt), formatTime(run.FinishedAt), run.Outcome, run.ErrorSummary, run.ExpectedStatuses, run.WrittenStatuses)
	return err
}
func nonNilWitness(v []clinching.WitnessGame) []clinching.WitnessGame {
	if v == nil {
		return []clinching.WitnessGame{}
	}
	return v
}
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
func validQualification(v QualificationStatus) bool {
	return clinching.ValidStatus(v.Status) && clinching.ValidMethod(v.Method) && clinching.ValidEvidence(v.StrictlyAhead) && clinching.ValidEvidence(v.AtLeastLevel) && validNoHelp(v.NoHelp.State)
}
func validNoHelp(v clinching.NoHelpState) bool {
	return v == clinching.NoHelpNotApplicable || v == clinching.NoHelpGuaranteed || v == clinching.NoHelpImpossible || v == clinching.NoHelpUnresolved
}

// ScenarioForSnapshot returns the latest completed batch for the exact input
// identity. Scenario clauses are never mixed across fixture snapshots.
func (c *DB) ScenarioForSnapshot(ctx context.Context, snapshotID, rulesVersion, definitionVersion string) (ScenarioSnapshot, bool, error) {
	// A scenario batch is current only when it was proved against the latest
	// completed qualification batch for the same snapshot and rules. This avoids
	// surfacing an older complete scenario batch after qualification was rebuilt.
	return c.loadScenario(ctx, `WHERE fixture_snapshot_id=? AND rules_version=? AND definition_version=? AND outcome='complete' AND qualification_run_id=(SELECT id FROM qualification_runs WHERE fixture_snapshot_id=? AND rules_version=? AND outcome='complete' ORDER BY finished_at DESC,id DESC LIMIT 1)`, snapshotID, rulesVersion, definitionVersion, snapshotID, rulesVersion)
}
func (c *DB) LatestScenario(ctx context.Context, season, stage, rulesVersion, definitionVersion string) (ScenarioSnapshot, bool, error) {
	return c.loadScenario(ctx, `WHERE season=? AND stage=? AND rules_version=? AND definition_version=? AND outcome='complete'`, season, stage, rulesVersion, definitionVersion)
}
func (c *DB) loadScenario(ctx context.Context, where string, args ...any) (ScenarioSnapshot, bool, error) {
	q := `SELECT id,fixture_snapshot_id,qualification_run_id,source_sync_run_id,season,stage,rules_version,definition_version,slate_id,slate_state,slate_source,matchday,starts_at_utc,latest_kickoff_utc,cutoff_utc,fixture_ids_json,slate_reason,started_at,finished_at,outcome,error_summary,expected_results,written_results FROM scenario_runs ` + where + ` ORDER BY finished_at DESC,id DESC LIMIT 1`
	var out ScenarioSnapshot
	var state, source, starts, latest, cutoff, fixtures, started, finished string
	err := c.db.QueryRowContext(ctx, q, args...).Scan(&out.Run.ID, &out.Run.FixtureSnapshotID, &out.Run.QualificationRunID, &out.Run.SourceSyncRunID, &out.Run.Season, &out.Run.Stage, &out.Run.RulesVersion, &out.Run.DefinitionVersion, &out.Run.Slate.ID, &state, &source, &out.Run.Slate.Matchday, &starts, &latest, &cutoff, &fixtures, &out.Run.Slate.Reason, &started, &finished, &out.Run.Outcome, &out.Run.ErrorSummary, &out.Run.ExpectedResults, &out.Run.WrittenResults)
	if errors.Is(err, sql.ErrNoRows) {
		return ScenarioSnapshot{}, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.Run.Slate.DefinitionVersion = out.Run.DefinitionVersion
	out.Run.Slate.State = scenarios.SlateState(state)
	out.Run.Slate.Source = scenarios.SlateSource(source)
	if err := json.Unmarshal([]byte(fixtures), &out.Run.Slate.FixtureIDs); err != nil {
		return out, false, err
	}
	if out.Run.Slate.FixtureIDs == nil {
		out.Run.Slate.FixtureIDs = []string{}
	}
	if starts != "" {
		if out.Run.Slate.StartsAtUTC, err = time.Parse(time.RFC3339, starts); err != nil {
			return out, false, err
		}
		if out.Run.Slate.LatestKickoffUTC, err = time.Parse(time.RFC3339, latest); err != nil {
			return out, false, err
		}
		if out.Run.Slate.CutoffUTC, err = time.Parse(time.RFC3339, cutoff); err != nil {
			return out, false, err
		}
	}
	if out.Run.StartedAt, err = time.Parse(time.RFC3339, started); err != nil {
		return out, false, err
	}
	if out.Run.FinishedAt, err = time.Parse(time.RFC3339, finished); err != nil {
		return out, false, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT team_id,achievement,top_k,opportunity_state,already_clinched,can_clinch,clauses_json,necessary_json,proof_methods_json,limitation,total_assignments,certified_assignments,unresolved_assignments,diagnostics_json FROM scenario_results WHERE scenario_run_id=? ORDER BY team_id,achievement`, out.Run.ID)
	if err != nil {
		return out, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var v ScenarioResult
		var achievement, state string
		var already, can int
		var clauses, necessary, methods, diag string
		if err := rows.Scan(&v.TeamID, &achievement, &v.TopK, &state, &already, &can, &clauses, &necessary, &methods, &v.Limitation, &v.TotalAssignments, &v.CertifiedAssignments, &v.UnresolvedAssignments, &diag); err != nil {
			return out, false, err
		}
		v.Achievement = competition.AchievementID(achievement)
		v.State = scenarios.OpportunityState(state)
		v.AlreadyClinched = already != 0
		v.CanClinch = can != 0
		if err := json.Unmarshal([]byte(clauses), &v.Clauses); err != nil {
			return out, false, err
		}
		if err := json.Unmarshal([]byte(necessary), &v.Necessary); err != nil {
			return out, false, err
		}
		if err := json.Unmarshal([]byte(methods), &v.ProofMethods); err != nil {
			return out, false, err
		}
		if err := json.Unmarshal([]byte(diag), &v.Diagnostics); err != nil {
			return out, false, err
		}
		if v.Clauses == nil {
			v.Clauses = []scenarios.Clause{}
		}
		if v.Necessary == nil {
			v.Necessary = []scenarios.FixtureCondition{}
		}
		if v.ProofMethods == nil {
			v.ProofMethods = []clinching.ProofMethod{}
		}
		out.Results = append(out.Results, v)
	}
	return out, true, rows.Err()
}
func (c *DB) ReplaceScenario(ctx context.Context, run ScenarioRun, values []ScenarioResult) (ScenarioSnapshot, error) {
	if run.FixtureSnapshotID == "" || run.RulesVersion == "" || run.DefinitionVersion != scenarios.DefinitionVersion || run.QualificationRunID == 0 || run.SourceSyncRunID == 0 || run.ExpectedResults != len(values) || run.WrittenResults != len(values) {
		return ScenarioSnapshot{}, errors.New("invalid scenario batch counts")
	}
	if err := run.Slate.Validate(); err != nil {
		return ScenarioSnapshot{}, err
	}
	seen := map[string]bool{}
	for _, v := range values {
		if err := v.Result.Validate(run.Slate); err != nil {
			return ScenarioSnapshot{}, err
		}
		k := v.TeamID + "\x00" + string(v.Achievement)
		if seen[k] {
			return ScenarioSnapshot{}, errors.New("duplicate scenario result")
		}
		seen[k] = true
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return ScenarioSnapshot{}, err
	}
	defer rollback(tx)
	var n int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM qualification_runs WHERE id=? AND fixture_snapshot_id=? AND rules_version=? AND outcome='complete'`, run.QualificationRunID, run.FixtureSnapshotID, run.RulesVersion).Scan(&n)
	if err != nil {
		return ScenarioSnapshot{}, err
	}
	if n != 1 {
		return ScenarioSnapshot{}, errors.New("scenario qualification prerequisite does not match")
	}
	run.Outcome = "complete"
	run.FinishedAt = time.Now().UTC()
	if run.StartedAt.IsZero() {
		run.StartedAt = run.FinishedAt
	}
	fixtures, _ := json.Marshal(nonNilStrings(run.Slate.FixtureIDs))
	starts, latest, cutoff := "", "", ""
	source := ""
	if run.Slate.State == scenarios.SlateReady {
		starts = formatTime(run.Slate.StartsAtUTC)
		latest = formatTime(run.Slate.LatestKickoffUTC)
		cutoff = formatTime(run.Slate.CutoffUTC)
		source = string(run.Slate.Source)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO scenario_runs(fixture_snapshot_id,qualification_run_id,source_sync_run_id,season,stage,rules_version,definition_version,slate_id,slate_state,slate_source,matchday,starts_at_utc,latest_kickoff_utc,cutoff_utc,fixture_ids_json,slate_reason,started_at,finished_at,outcome,error_summary,expected_results,written_results) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.FixtureSnapshotID, run.QualificationRunID, run.SourceSyncRunID, run.Season, run.Stage, run.RulesVersion, run.DefinitionVersion, run.Slate.ID, run.Slate.State, source, run.Slate.Matchday, starts, latest, cutoff, string(fixtures), run.Slate.Reason, formatTime(run.StartedAt), formatTime(run.FinishedAt), run.Outcome, run.ErrorSummary, run.ExpectedResults, run.WrittenResults)
	if err != nil {
		return ScenarioSnapshot{}, err
	}
	run.ID, _ = res.LastInsertId()
	for _, v := range values {
		clauses, _ := json.Marshal(v.Clauses)
		necessary, _ := json.Marshal(v.Necessary)
		methods, _ := json.Marshal(v.ProofMethods)
		diag, _ := json.Marshal(v.Diagnostics)
		if _, err := tx.ExecContext(ctx, `INSERT INTO scenario_results VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, v.TeamID, v.Achievement, v.TopK, v.State, boolInt(v.AlreadyClinched), boolInt(v.CanClinch), string(clauses), string(necessary), string(methods), v.Limitation, v.TotalAssignments, v.CertifiedAssignments, v.UnresolvedAssignments, string(diag)); err != nil {
			return ScenarioSnapshot{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ScenarioSnapshot{}, err
	}
	return ScenarioSnapshot{Run: run, Results: append([]ScenarioResult(nil), values...)}, nil
}
func (c *DB) RecordScenarioFailure(ctx context.Context, run ScenarioRun, cause error) error {
	run.Outcome = "failure"
	run.ErrorSummary = summarizeError(cause)
	run.FinishedAt = time.Now().UTC()
	if run.StartedAt.IsZero() {
		run.StartedAt = run.FinishedAt
	}
	fixtures, _ := json.Marshal([]string{})
	_, err := c.db.ExecContext(ctx, `INSERT INTO scenario_runs(fixture_snapshot_id,qualification_run_id,source_sync_run_id,season,stage,rules_version,definition_version,slate_id,slate_state,slate_source,matchday,starts_at_utc,latest_kickoff_utc,cutoff_utc,fixture_ids_json,slate_reason,started_at,finished_at,outcome,error_summary,expected_results,written_results) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.FixtureSnapshotID, run.QualificationRunID, run.SourceSyncRunID, run.Season, run.Stage, run.RulesVersion, run.DefinitionVersion, "", scenarios.SlateUnavailable, "", 0, "", "", "", string(fixtures), "", formatTime(run.StartedAt), formatTime(run.FinishedAt), run.Outcome, run.ErrorSummary, run.ExpectedResults, 0)
	return err
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func summarizeError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	const max = 1000
	if len(text) > max {
		return text[:max]
	}
	return text
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
