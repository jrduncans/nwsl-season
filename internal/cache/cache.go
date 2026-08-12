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
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/standings"

	_ "modernc.org/sqlite"
)

const schemaVersion = 13

// MaxGameExpectedPoints is the most league points a team can expect from one
// match. ASA's game-level expected-points values estimate that allocation, so
// each team's value must be in the inclusive range [0, MaxGameExpectedPoints].
const MaxGameExpectedPoints = 3

// DB wraps the SQLite cache.
type DB struct {
	db *sql.DB
}

// queryer is the read-only subset shared by *sql.DB and *sql.Tx. Keeping the
// season queries behind this interface lets a compound read use one SQLite
// transaction and therefore one database snapshot.
type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
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
	ASAID           string
	Season          string
	Stage           string
	KickoffUTC      string
	Status          string
	HomeTeamID      string
	AwayTeamID      string
	HomeScore       sql.NullInt64
	AwayScore       sql.NullInt64
	Matchday        sql.NullInt64
	ExpandedMinutes sql.NullInt64
	KnockoutGame    bool
	LastUpdatedUTC  string
	RawJSON         string
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
	FixtureSnapshotID  string
	QualificationError string
	ScenarioError      string
	// Recalculated flags are command-local outcomes and are not persisted in
	// sync_runs. They make cache reuse visible to maintenance callers.
	QualificationRecalculated    bool
	QualificationRefreshRequired bool
	QualificationRefreshReason   string
	QualificationSnapshotChecked bool
	QualificationSnapshotFound   bool
	QualificationRulesVersion    string
	ScenarioRecalculated         bool
	ScenarioRefreshRequired      bool
	ScenarioRefreshReason        string
	ScenarioSnapshotChecked      bool
	ScenarioSnapshotFound        bool
	ScenarioRulesVersion         string
	ScenarioDefinitionVersion    string
	// XGRun/XGError describe the independent second refresh when available.
	XGRun             *XGSyncRun
	XGError           string
	HistoryPrune      *HistoryPruneResult
	HistoryPruneError string
}

// DerivedRefreshResult describes the decision made before a qualification or
// scenario refresh. It is command-local state used to keep no-op decisions on
// the parent sync span while reserving child spans for actual refresh work.
type DerivedRefreshResult struct {
	Recalculated      bool
	Required          bool
	Reason            string
	SnapshotChecked   bool
	SnapshotFound     bool
	RulesVersion      string
	DefinitionVersion string
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
	GameID                   string
	Availability             XGAvailability
	HomeTeamID, AwayTeamID   string
	HomeXG, AwayXG           sql.NullFloat64
	HomeXPoints, AwayXPoints sql.NullFloat64
	RawJSON                  string
	FirstObservedAt          *time.Time
	LastCheckedAt            time.Time
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
	VenueHistory      []VenueSummary
	FixtureSnapshotID string
}

// VenueSummary is the persisted league-wide home/away sample for one season.
// FixtureReady and XGReady distinguish a successful zero-row refresh from data
// that has never been synchronized.
type VenueSummary struct {
	Season, Stage                 string
	FixtureReady, XGReady         bool
	Matches, HomeGoals, AwayGoals int
	HomePoints, AwayPoints        int
	XGMatches                     int
	HomeXG, AwayXG                float64
	UpdatedAt                     time.Time
}

// CalculationInputs is the last successfully synced fixture snapshot in the
// cache-native shape required by qualification and scenario refreshers.
type CalculationInputs struct {
	SyncRun SyncRun
	Teams   []Team
	Games   []Game
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

	db, err := sql.Open("sqlite", sqliteDSN(path))
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
	if _, err := c.db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("configure sqlite cache: %w", err)
	}
	return nil
}

// sqliteDSN applies settings that SQLite scopes to an individual connection.
// The modernc driver runs every _pragma value while opening each pooled
// connection, unlike an ExecContext call on *sql.DB, which reaches only one
// connection. journal_mode is configured separately because WAL is persistent
// database state rather than a connection setting.
func sqliteDSN(path string) string {
	pragmas := url.Values{}
	pragmas.Add("_pragma", "busy_timeout(5000)")
	pragmas.Add("_pragma", "foreign_keys(ON)")
	return path + "?" + pragmas.Encode()
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
	if version < 6 {
		for _, statement := range []string{
			`ALTER TABLE scenario_results ADD COLUMN already_eliminated INTEGER NOT NULL DEFAULT 0 CHECK (already_eliminated IN (0,1))`,
			`ALTER TABLE scenario_results ADD COLUMN can_be_eliminated INTEGER NOT NULL DEFAULT 0 CHECK (can_be_eliminated IN (0,1))`,
			`ALTER TABLE scenario_results ADD COLUMN elimination_clauses_json TEXT NOT NULL DEFAULT '[]'`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 6: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 6); err != nil {
			return err
		}
	}
	if version < 7 {
		for _, statement := range []string{
			`ALTER TABLE game_xg ADD COLUMN home_xpoints REAL`,
			`ALTER TABLE game_xg ADD COLUMN away_xpoints REAL`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 7: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 7); err != nil {
			return err
		}
		version = 7
	}
	if version < 8 {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE venue_summaries (
				season TEXT NOT NULL, stage TEXT NOT NULL,
				fixture_ready INTEGER NOT NULL CHECK (fixture_ready IN (0,1)),
				xg_ready INTEGER NOT NULL CHECK (xg_ready IN (0,1)),
				matches INTEGER NOT NULL, home_goals INTEGER NOT NULL, away_goals INTEGER NOT NULL,
				home_points INTEGER NOT NULL, away_points INTEGER NOT NULL,
				xg_matches INTEGER NOT NULL, home_xg REAL NOT NULL, away_xg REAL NOT NULL,
				updated_at TEXT NOT NULL,
				PRIMARY KEY (season, stage)
			)`); err != nil {
			return fmt.Errorf("apply migration 8: %w", err)
		}
		hasGames, err := tableExists(ctx, tx, "games")
		if err != nil {
			return err
		}
		if hasGames {
			if _, err := tx.ExecContext(ctx, `INSERT INTO venue_summaries (
				season,stage,fixture_ready,xg_ready,matches,home_goals,away_goals,home_points,away_points,xg_matches,home_xg,away_xg,updated_at
			)
			SELECT g.season,g.stage,1,
				CASE WHEN EXISTS (SELECT 1 FROM xg_sync_runs xr WHERE xr.season=g.season AND xr.stage=g.stage AND xr.outcome='success')
					AND NOT EXISTS (
						SELECT 1 FROM games g2 LEFT JOIN game_xg x2 ON x2.asa_game_id=g2.asa_game_id
						WHERE g2.season=g.season AND g2.stage=g.stage AND g2.status='FullTime' AND x2.asa_game_id IS NULL
					) THEN 1 ELSE 0 END,
				SUM(CASE WHEN g.status='FullTime' AND g.home_score IS NOT NULL AND g.away_score IS NOT NULL THEN 1 ELSE 0 END),
				COALESCE(SUM(CASE WHEN g.status='FullTime' THEN g.home_score ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN g.status='FullTime' THEN g.away_score ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN g.status!='FullTime' THEN 0 WHEN g.home_score>g.away_score THEN 3 WHEN g.home_score=g.away_score THEN 1 ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN g.status!='FullTime' THEN 0 WHEN g.away_score>g.home_score THEN 3 WHEN g.home_score=g.away_score THEN 1 ELSE 0 END),0),
				SUM(CASE WHEN g.status='FullTime' AND x.availability='available' AND x.home_xg IS NOT NULL AND x.away_xg IS NOT NULL THEN 1 ELSE 0 END),
				COALESCE(SUM(CASE WHEN g.status='FullTime' AND x.availability='available' THEN x.home_xg ELSE 0 END),0),
				COALESCE(SUM(CASE WHEN g.status='FullTime' AND x.availability='available' THEN x.away_xg ELSE 0 END),0),
				?
			FROM games g LEFT JOIN game_xg x ON x.asa_game_id=g.asa_game_id
			GROUP BY g.season,g.stage`, formatTime(time.Now().UTC())); err != nil {
				return fmt.Errorf("apply migration 8: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 8); err != nil {
			return err
		}
		version = 8
	}
	if version < 9 {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE source_scopes (
			season TEXT NOT NULL,
			stage TEXT NOT NULL,
			registration TEXT NOT NULL
				CHECK (registration IN ('catalog','configured','provisional','observed')),
			lifecycle TEXT NOT NULL
				CHECK (lifecycle IN ('upcoming','active','completed')),
			discovery TEXT NOT NULL
				CHECK (discovery IN ('unknown','not_published','available')),
			registered_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (season, stage)
		)`); err != nil {
			return fmt.Errorf("apply migration 9: %w", err)
		}
		if err := backfillSourceScopes(ctx, tx, time.Now().UTC()); err != nil {
			return fmt.Errorf("apply migration 9: %w", err)
		}
		if err := recordMigration(ctx, tx, 9); err != nil {
			return err
		}
		version = 9
	}
	if version < 10 {
		for _, statement := range []string{
			`CREATE TABLE source_refresh_audits (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				resource TEXT NOT NULL
					CHECK (resource IN ('teams','games','game_xg')),
				season TEXT NOT NULL,
				stage TEXT NOT NULL,
				mode TEXT NOT NULL
					CHECK (mode IN ('full','targeted','recalculate')),
				trigger TEXT NOT NULL,
				started_at TEXT NOT NULL,
				finished_at TEXT NOT NULL,
				outcome TEXT NOT NULL
					CHECK (outcome IN ('success','failure')),
				error_summary TEXT NOT NULL,
				requested_rows INTEGER NOT NULL CHECK (requested_rows >= 0),
				returned_rows INTEGER NOT NULL CHECK (returned_rows >= 0),
				rows_inserted INTEGER NOT NULL CHECK (rows_inserted >= 0),
				rows_updated INTEGER NOT NULL CHECK (rows_updated >= 0),
				rows_unchanged INTEGER NOT NULL CHECK (rows_unchanged >= 0),
				rows_deleted INTEGER NOT NULL CHECK (rows_deleted >= 0),
				downstream_inputs_changed INTEGER NOT NULL
					CHECK (downstream_inputs_changed IN (0,1)),
				CHECK (
					(resource = 'teams' AND season = '' AND stage = '') OR
					(resource IN ('games','game_xg') AND season <> '' AND stage <> '')
				),
				CHECK (
					(outcome = 'success' AND error_summary = '') OR
					(outcome = 'failure' AND error_summary <> '')
				),
				CHECK (mode = 'full' OR rows_deleted = 0)
			)`,
			`CREATE INDEX source_refresh_audits_scope_idx
				ON source_refresh_audits (
					resource, season, stage, finished_at DESC, id DESC
				)`,
			`CREATE TABLE source_resource_scope_state (
				resource TEXT NOT NULL
					CHECK (resource IN ('teams','games','game_xg')),
				season TEXT NOT NULL,
				stage TEXT NOT NULL,
				last_full_success_at TEXT,
				next_full_due_at TEXT,
				updated_at TEXT NOT NULL,
				PRIMARY KEY (resource, season, stage),
				CHECK (
					(resource = 'teams' AND season = '' AND stage = '') OR
					(resource IN ('games','game_xg') AND season <> '' AND stage <> '')
				)
			)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 10: %w", err)
			}
		}
		if err := backfillSourceResourceScopeState(ctx, tx, time.Now().UTC()); err != nil {
			return fmt.Errorf("apply migration 10: %w", err)
		}
		if err := recordMigration(ctx, tx, 10); err != nil {
			return err
		}
		version = 10
	}
	if version < 11 {
		for _, statement := range []string{
			`CREATE TABLE game_result_checks (
				asa_game_id TEXT PRIMARY KEY REFERENCES games(asa_game_id) ON DELETE CASCADE,
				last_checked_at TEXT NOT NULL,
				first_terminal_observed_at TEXT,
				last_material_change_at TEXT,
				next_due_at TEXT
			)`,
			`CREATE INDEX game_result_checks_due_idx ON game_result_checks (next_due_at, asa_game_id)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 11: %w", err)
			}
		}
		if err := backfillGameResultChecks(ctx, tx); err != nil {
			return fmt.Errorf("apply migration 11: %w", err)
		}
		if err := recordMigration(ctx, tx, 11); err != nil {
			return err
		}
		version = 11
	}
	if version < 12 {
		for _, statement := range []string{
			`CREATE TABLE game_xg_checks (
				asa_game_id TEXT PRIMARY KEY REFERENCES games(asa_game_id) ON DELETE CASCADE,
				last_checked_at TEXT NOT NULL,
				first_available_observed_at TEXT,
				last_material_change_at TEXT,
				next_due_at TEXT
			)`,
			`CREATE INDEX game_xg_checks_due_idx ON game_xg_checks (next_due_at, asa_game_id)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply migration 12: %w", err)
			}
		}
		if err := backfillGameXGChecks(ctx, tx); err != nil {
			return fmt.Errorf("apply migration 12: %w", err)
		}
		if err := recordMigration(ctx, tx, 12); err != nil {
			return err
		}
		version = 12
	}
	if version < 13 {
		gamesOK, err := tableHasColumns(ctx, tx, "games", "asa_game_id")
		if err != nil {
			return err
		}
		if gamesOK {
			for _, change := range []struct{ column, statement string }{
				{"expanded_minutes", `ALTER TABLE games ADD COLUMN expanded_minutes INTEGER`},
				{"knockout_game", `ALTER TABLE games ADD COLUMN knockout_game INTEGER NOT NULL DEFAULT 0 CHECK (knockout_game IN (0,1))`},
			} {
				exists, err := tableHasColumns(ctx, tx, "games", change.column)
				if err != nil {
					return err
				}
				if exists {
					continue
				}
				if _, err := tx.ExecContext(ctx, change.statement); err != nil {
					return fmt.Errorf("apply migration 13: %w", err)
				}
			}
			if err := backfillPlayoffGameFields(ctx, tx); err != nil {
				return fmt.Errorf("apply migration 13: %w", err)
			}
			if err := refreshFixtureSnapshotIDsForMigration(ctx, tx); err != nil {
				return fmt.Errorf("apply migration 13: %w", err)
			}
		}
		if err := recordMigration(ctx, tx, 13); err != nil {
			return err
		}
		version = 13
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func backfillPlayoffGameFields(ctx context.Context, tx *sql.Tx) error {
	ok, err := tableHasColumns(ctx, tx, "games", "asa_game_id", "raw_json")
	if err != nil || !ok {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT asa_game_id, raw_json FROM games`)
	if err != nil {
		return err
	}
	type update struct {
		id       string
		minutes  any
		knockout bool
	}
	updates := []update{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		var source struct {
			ExpandedMinutes *int  `json:"expanded_minutes"`
			KnockoutGame    *bool `json:"knockout_game"`
		}
		if json.Unmarshal([]byte(raw), &source) != nil {
			continue
		}
		var minutes any
		if source.ExpandedMinutes != nil && *source.ExpandedMinutes >= 0 {
			minutes = *source.ExpandedMinutes
		}
		updates = append(updates, update{id: id, minutes: minutes, knockout: source.KnockoutGame != nil && *source.KnockoutGame})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, value := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE games SET expanded_minutes=?, knockout_game=? WHERE asa_game_id=?`, value.minutes, value.knockout, value.id); err != nil {
			return err
		}
	}
	return nil
}

// refreshFixtureSnapshotIDsForMigration moves pre-v13 fixture identities to
// the v3 contract after normalized playoff fields have been backfilled. Only
// each scope's current successful run is updated: older derived records stay
// attached to their original factual snapshot and are simply non-current.
func refreshFixtureSnapshotIDsForMigration(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT season, stage FROM games`)
	if err != nil {
		return err
	}
	type scope struct{ season, stage string }
	scopes := []scope{}
	for rows.Next() {
		var value scope
		if err := rows.Scan(&value.season, &value.stage); err != nil {
			_ = rows.Close()
			return err
		}
		scopes = append(scopes, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, scope := range scopes {
		run, err := latestRun(ctx, tx, "success", scope.season, scope.stage)
		if err != nil || run == nil {
			if err != nil {
				return err
			}
			continue
		}
		teams, err := inventoryTeams(ctx, tx, scope.season, scope.stage)
		if err != nil {
			return err
		}
		games, err := seasonGames(ctx, tx, scope.season, scope.stage)
		if err != nil {
			return err
		}
		snapshot, err := FixtureSnapshotID(teams, games)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sync_runs SET fixture_snapshot_id=? WHERE id=?`, snapshot, run.ID); err != nil {
			return err
		}
	}
	return nil
}

func backfillGameResultChecks(ctx context.Context, tx *sql.Tx) error {
	gamesOK, err := tableHasColumns(ctx, tx, "games", "asa_game_id", "season", "stage")
	if err != nil || !gamesOK {
		return err
	}
	runsOK, err := tableHasColumns(ctx, tx, "sync_runs", "season", "stage", "outcome", "finished_at")
	if err != nil || !runsOK {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_result_checks (asa_game_id,last_checked_at,first_terminal_observed_at,last_material_change_at,next_due_at)
		SELECT g.asa_game_id, MAX(r.finished_at), NULL, NULL, NULL
		FROM games g JOIN sync_runs r ON r.season=g.season AND r.stage=g.stage
		WHERE r.outcome='success' GROUP BY g.asa_game_id`)
	if err != nil {
		return fmt.Errorf("backfill game result checks: %w", err)
	}
	return nil
}

func backfillGameXGChecks(ctx context.Context, tx *sql.Tx) error {
	gamesOK, err := tableHasColumns(ctx, tx, "games", "asa_game_id")
	if err != nil || !gamesOK {
		return err
	}
	xgOK, err := tableHasColumns(ctx, tx, "game_xg", "asa_game_id", "last_checked_at", "first_observed_at")
	if err != nil || !xgOK {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_xg_checks (
		asa_game_id,last_checked_at,first_available_observed_at,last_material_change_at,next_due_at
	) SELECT asa_game_id,last_checked_at,first_observed_at,NULL,NULL FROM game_xg`)
	if err != nil {
		return fmt.Errorf("backfill game xG checks: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil {
		return false, fmt.Errorf("check migration table %q: %w", name, err)
	}
	return count > 0, nil
}

func tableHasColumns(ctx context.Context, tx *sql.Tx, name string, wanted ...string) (bool, error) {
	exists, err := tableExists(ctx, tx, name)
	if err != nil || !exists {
		return exists, err
	}
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+name+`)`)
	if err != nil {
		return false, fmt.Errorf("inspect columns for table %q: %w", name, err)
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var cid, notNull, primaryKey int
		var column, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &column, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan columns for table %q: %w", name, err)
		}
		columns[column] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate columns for table %q: %w", name, err)
	}
	for _, column := range wanted {
		if _, ok := columns[column]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func backfillSourceResourceScopeState(ctx context.Context, tx *sql.Tx, now time.Time) error {
	now = now.UTC()
	backfillGames, err := tableHasColumns(ctx, tx, "sync_runs", "season", "stage", "outcome", "finished_at")
	if err != nil {
		return err
	}
	if backfillGames {
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_resource_scope_state (
			resource, season, stage, last_full_success_at, next_full_due_at, updated_at
		) SELECT 'games', season, stage, MAX(finished_at), NULL, ?
		FROM sync_runs WHERE outcome = 'success' GROUP BY season, stage`, formatTime(now)); err != nil {
			return fmt.Errorf("backfill games source refresh state: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_resource_scope_state (
			resource, season, stage, last_full_success_at, next_full_due_at, updated_at
		) SELECT 'teams', '', '', MAX(finished_at), NULL, ?
		FROM sync_runs WHERE outcome = 'success' HAVING COUNT(*) > 0`, formatTime(now)); err != nil {
			return fmt.Errorf("backfill teams source refresh state: %w", err)
		}
	}
	backfillXG, err := tableHasColumns(ctx, tx, "xg_sync_runs", "season", "stage", "outcome", "finished_at")
	if err != nil {
		return err
	}
	if backfillXG {
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_resource_scope_state (
			resource, season, stage, last_full_success_at, next_full_due_at, updated_at
		) SELECT 'game_xg', season, stage, MAX(finished_at), NULL, ?
		FROM xg_sync_runs WHERE outcome = 'success' GROUP BY season, stage`, formatTime(now)); err != nil {
			return fmt.Errorf("backfill game xG source refresh state: %w", err)
		}
	}
	return nil
}

// backfillSourceScopes also tolerates the deliberately minimal old-schema
// fixtures used by migration tests. A real version-8 database has all three
// source tables, so its query is the full union described by migration 9.
func backfillSourceScopes(ctx context.Context, tx *sql.Tx, now time.Time) error {
	hasGames, err := tableExists(ctx, tx, "games")
	if err != nil {
		return err
	}
	hasSyncRuns, err := tableExists(ctx, tx, "sync_runs")
	if err != nil {
		return err
	}
	hasXGRuns, err := tableExists(ctx, tx, "xg_sync_runs")
	if err != nil {
		return err
	}

	identities := make([]string, 0, 3)
	if hasGames {
		identities = append(identities, "SELECT season, stage FROM games")
	}
	if hasSyncRuns {
		identities = append(identities, "SELECT season, stage FROM sync_runs")
	}
	if hasXGRuns {
		identities = append(identities, "SELECT season, stage FROM xg_sync_runs")
	}
	if len(identities) == 0 {
		return nil
	}

	gameDiscovery := "0"
	if hasGames {
		gameDiscovery = "EXISTS (SELECT 1 FROM games g WHERE g.season = identities.season AND g.stage = identities.stage)"
	}
	successfulSync := "0"
	if hasSyncRuns {
		successfulSync = "EXISTS (SELECT 1 FROM sync_runs sr WHERE sr.season = identities.season AND sr.stage = identities.stage AND sr.outcome = 'success')"
	}
	statement := fmt.Sprintf(`INSERT INTO source_scopes (
		season, stage, registration, lifecycle, discovery, registered_at, updated_at
	)
	SELECT identities.season, identities.stage, 'observed', 'active',
		CASE
			WHEN %s THEN 'available'
			WHEN %s THEN 'not_published'
			ELSE 'unknown'
		END,
		?, ?
	FROM (
		%s
	) AS identities`, gameDiscovery, successfulSync, strings.Join(identities, "\nUNION\n"))
	if _, err := tx.ExecContext(ctx, statement, formatTime(now), formatTime(now)); err != nil {
		return err
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
	if err := updateVenueFixtureSummary(ctx, tx, season, stage, now); err != nil {
		return SyncRun{}, err
	}

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
		home_score, away_score, matchday, expanded_minutes, knockout_game, last_updated_utc, raw_json
		FROM games WHERE asa_game_id = ?`, game.ASAID).Scan(
		&existing.ASAID, &existing.Season, &existing.Stage, &existing.KickoffUTC, &existing.Status,
		&existing.HomeTeamID, &existing.AwayTeamID, &existing.HomeScore, &existing.AwayScore,
		&existing.Matchday, &existing.ExpandedMinutes, &existing.KnockoutGame, &existing.LastUpdatedUTC, &existing.RawJSON)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO games (
			asa_game_id, season, stage, kickoff_utc, status, home_team_id, away_team_id,
			home_score, away_score, matchday, expanded_minutes, knockout_game, last_updated_utc, raw_json, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			game.ASAID, game.Season, game.Stage, game.KickoffUTC, game.Status, game.HomeTeamID, game.AwayTeamID,
			nullableInt(game.HomeScore), nullableInt(game.AwayScore), nullableInt(game.Matchday), nullableInt(game.ExpandedMinutes), game.KnockoutGame, game.LastUpdatedUTC, game.RawJSON, formatTime(now)); err != nil {
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
		home_score = ?, away_score = ?, matchday = ?, expanded_minutes = ?, knockout_game = ?, last_updated_utc = ?, raw_json = ?, synced_at = ?
		WHERE asa_game_id = ?`,
		game.Season, game.Stage, game.KickoffUTC, game.Status, game.HomeTeamID, game.AwayTeamID,
		nullableInt(game.HomeScore), nullableInt(game.AwayScore), nullableInt(game.Matchday), nullableInt(game.ExpandedMinutes), game.KnockoutGame, game.LastUpdatedUTC, game.RawJSON, formatTime(now), game.ASAID); err != nil {
		return 0, fmt.Errorf("update game %q: %w", game.ASAID, err)
	}
	return rowUpdated, nil
}

func equalGame(left, right Game) bool {
	return left.ASAID == right.ASAID && left.Season == right.Season && left.Stage == right.Stage &&
		left.KickoffUTC == right.KickoffUTC && left.Status == right.Status &&
		left.HomeTeamID == right.HomeTeamID && left.AwayTeamID == right.AwayTeamID &&
		left.HomeScore == right.HomeScore && left.AwayScore == right.AwayScore &&
		left.Matchday == right.Matchday && left.ExpandedMinutes == right.ExpandedMinutes && left.KnockoutGame == right.KnockoutGame && left.LastUpdatedUTC == right.LastUpdatedUTC && left.RawJSON == right.RawJSON
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

// ClinchingInputs loads a calculation snapshot without contacting ASA or
// changing fixture, team, xG, or sync audit data.
func (c *DB) ClinchingInputs(ctx context.Context, season, stage string) (CalculationInputs, error) {
	lastSuccess, err := c.LastSuccess(ctx, season, stage)
	if err != nil {
		return CalculationInputs{}, err
	}
	if lastSuccess == nil {
		return CalculationInputs{}, fmt.Errorf("no successful sync exists for %s %s", season, stage)
	}
	games, err := c.seasonGames(ctx, season, stage)
	if err != nil {
		return CalculationInputs{}, err
	}
	domainTeams, err := c.standingsTeams(ctx, season, stage)
	if err != nil {
		return CalculationInputs{}, err
	}
	teams := make([]Team, 0, len(domainTeams))
	for _, team := range domainTeams {
		teams = append(teams, Team{ASAID: team.ID, Name: team.Name, ShortName: team.ShortName, Abbreviation: team.Abbreviation})
	}
	snapshotID, err := FixtureSnapshotID(teams, games)
	if err != nil {
		return CalculationInputs{}, err
	}
	if snapshotID != lastSuccess.FixtureSnapshotID {
		return CalculationInputs{}, fmt.Errorf("cached fixtures do not match the last successful sync snapshot")
	}
	return CalculationInputs{SyncRun: *lastSuccess, Teams: teams, Games: games}, nil
}

// Season loads the teams, fixtures, and freshness information for a season and
// stage. It never refreshes data from the upstream source.
func (c *DB) Season(ctx context.Context, season, stage string) (SeasonData, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SeasonData{}, fmt.Errorf("begin season read: %w", err)
	}
	defer rollback(tx)

	return loadSeasonData(ctx, tx, season, stage)
}

func loadSeasonData(ctx context.Context, dbq queryer, season, stage string) (SeasonData, error) {
	teams, err := standingsTeams(ctx, dbq, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	games, err := seasonGames(ctx, dbq, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	lastSuccess, err := latestRun(ctx, dbq, "success", season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	xgoals, err := seasonXGoals(ctx, dbq, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	xgStatus, err := xgStatus(ctx, dbq, season, stage)
	if err != nil {
		return SeasonData{}, err
	}
	var venueHistory []VenueSummary
	if seasons, historyErr := competition.PreviousRegularSeasons(season, 2); historyErr == nil {
		venueHistory, err = venueSummaries(ctx, dbq, seasons, stage)
		if err != nil {
			return SeasonData{}, err
		}
	}
	snapshotID := ""
	if lastSuccess != nil {
		snapshotTeams := make([]Team, 0, len(teams))
		for _, team := range teams {
			snapshotTeams = append(snapshotTeams, Team{ASAID: team.ID})
		}
		snapshotID, err = FixtureSnapshotID(snapshotTeams, games)
		if err != nil {
			return SeasonData{}, fmt.Errorf("calculate fixture snapshot: %w", err)
		}
		if snapshotID != lastSuccess.FixtureSnapshotID {
			return SeasonData{}, errors.New("cached fixtures do not match the last successful sync snapshot")
		}
	}
	return SeasonData{Teams: teams, Games: games, LastSuccess: lastSuccess, XGoals: xgoals, XGStatus: xgStatus, VenueHistory: venueHistory, FixtureSnapshotID: snapshotID}, nil
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
		if value.HomeXPoints.Valid != value.AwayXPoints.Valid || (value.HomeXPoints.Valid && (!validGameExpectedPoints(value.HomeXPoints.Float64) || !validGameExpectedPoints(value.AwayXPoints.Float64))) {
			return XGSyncRun{}, fmt.Errorf("xG game %q has invalid expected points", value.GameID)
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
	if err := updateVenueXGSummary(ctx, tx, season, stage, now); err != nil {
		return XGSyncRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return XGSyncRun{}, fmt.Errorf("commit xG refresh: %w", err)
	}
	return run, nil
}

func updateVenueFixtureSummary(ctx context.Context, tx *sql.Tx, season, stage string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO venue_summaries (
		season,stage,fixture_ready,xg_ready,matches,home_goals,away_goals,home_points,away_points,xg_matches,home_xg,away_xg,updated_at
	) SELECT ?,?,1,0,
		COUNT(*),COALESCE(SUM(home_score),0),COALESCE(SUM(away_score),0),
		COALESCE(SUM(CASE WHEN home_score>away_score THEN 3 WHEN home_score=away_score THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN away_score>home_score THEN 3 WHEN home_score=away_score THEN 1 ELSE 0 END),0),
		0,0,0,?
	FROM games WHERE season=? AND stage=? AND status='FullTime' AND home_score IS NOT NULL AND away_score IS NOT NULL
	ON CONFLICT(season,stage) DO UPDATE SET
		fixture_ready=1,xg_ready=0,matches=excluded.matches,home_goals=excluded.home_goals,away_goals=excluded.away_goals,
		home_points=excluded.home_points,away_points=excluded.away_points,updated_at=excluded.updated_at`,
		season, stage, formatTime(now), season, stage)
	if err != nil {
		return fmt.Errorf("update venue fixture summary: %w", err)
	}
	return nil
}

func updateVenueXGSummary(ctx context.Context, tx *sql.Tx, season, stage string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO venue_summaries (
		season,stage,fixture_ready,xg_ready,matches,home_goals,away_goals,home_points,away_points,xg_matches,home_xg,away_xg,updated_at
	) SELECT ?,?,0,1,0,0,0,0,0,
		COUNT(*),COALESCE(SUM(x.home_xg),0),COALESCE(SUM(x.away_xg),0),?
	FROM games g JOIN game_xg x ON x.asa_game_id=g.asa_game_id
	WHERE g.season=? AND g.stage=? AND g.status='FullTime' AND x.availability='available'
	ON CONFLICT(season,stage) DO UPDATE SET
		xg_ready=1,xg_matches=excluded.xg_matches,home_xg=excluded.home_xg,away_xg=excluded.away_xg,updated_at=excluded.updated_at`,
		season, stage, formatTime(now), season, stage)
	if err != nil {
		return fmt.Errorf("update venue xG summary: %w", err)
	}
	return nil
}

// VenueSummaries loads the small persisted summaries for the requested
// seasons. Missing seasons are omitted so callers can trigger synchronization.
func (c *DB) VenueSummaries(ctx context.Context, seasons []string, stage string) ([]VenueSummary, error) {
	return venueSummaries(ctx, c.db, seasons, stage)
}

func venueSummaries(ctx context.Context, dbq queryer, seasons []string, stage string) ([]VenueSummary, error) {
	values := make([]VenueSummary, 0, len(seasons))
	for _, season := range seasons {
		var value VenueSummary
		var fixtureReady, xgReady int
		var updated string
		err := dbq.QueryRowContext(ctx, `SELECT season,stage,fixture_ready,xg_ready,matches,home_goals,away_goals,home_points,away_points,xg_matches,home_xg,away_xg,updated_at FROM venue_summaries WHERE season=? AND stage=?`, season, stage).Scan(
			&value.Season, &value.Stage, &fixtureReady, &xgReady, &value.Matches, &value.HomeGoals, &value.AwayGoals, &value.HomePoints, &value.AwayPoints, &value.XGMatches, &value.HomeXG, &value.AwayXG, &updated)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load venue summary for %s %s: %w", season, stage, err)
		}
		value.FixtureReady, value.XGReady = fixtureReady != 0, xgReady != 0
		value.UpdatedAt, err = time.Parse(time.RFC3339, updated)
		if err != nil {
			return nil, fmt.Errorf("parse venue summary timestamp: %w", err)
		}
		values = append(values, value)
	}
	return values, nil
}

func finiteNonnegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

func validGameExpectedPoints(value float64) bool {
	return finiteNonnegative(value) && value <= MaxGameExpectedPoints
}
func writeGameXG(ctx context.Context, tx *sql.Tx, value GameXG, now time.Time) (rowChange, error) {
	var old GameXG
	var first sql.NullString
	var checked string
	err := tx.QueryRowContext(ctx, `SELECT availability,home_team_id,away_team_id,home_xg,away_xg,home_xpoints,away_xpoints,raw_json,first_observed_at,last_checked_at FROM game_xg WHERE asa_game_id=?`, value.GameID).Scan(&old.Availability, &old.HomeTeamID, &old.AwayTeamID, &old.HomeXG, &old.AwayXG, &old.HomeXPoints, &old.AwayXPoints, &old.RawJSON, &first, &checked)
	if first.Valid {
		parsed, e := time.Parse(time.RFC3339, first.String)
		if e != nil {
			return 0, e
		}
		old.FirstObservedAt = &parsed
	}
	if errors.Is(err, sql.ErrNoRows) {
		firstValue := any(nil)
		home, away, homePoints, awayPoints := any(nil), any(nil), any(nil), any(nil)
		raw := ""
		if value.Availability == XGAvailable {
			firstValue = formatTime(now)
			home = value.HomeXG.Float64
			away = value.AwayXG.Float64
			homePoints = nullableFloat(value.HomeXPoints)
			awayPoints = nullableFloat(value.AwayXPoints)
			raw = value.RawJSON
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO game_xg (asa_game_id,availability,home_team_id,away_team_id,home_xg,away_xg,home_xpoints,away_xpoints,raw_json,first_observed_at,last_checked_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, value.GameID, value.Availability, value.HomeTeamID, value.AwayTeamID, home, away, homePoints, awayPoints, raw, firstValue, formatTime(now))
		if e != nil {
			return 0, fmt.Errorf("insert xG %q: %w", value.GameID, e)
		}
		return rowInserted, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load xG %q: %w", value.GameID, err)
	}
	material := old.Availability != value.Availability || old.HomeTeamID != value.HomeTeamID || old.AwayTeamID != value.AwayTeamID || old.HomeXG != value.HomeXG || old.AwayXG != value.AwayXG || old.HomeXPoints != value.HomeXPoints || old.AwayXPoints != value.AwayXPoints || (value.Availability == XGAvailable && old.RawJSON != value.RawJSON)
	firstValue := any(nil)
	home, away, homePoints, awayPoints := any(nil), any(nil), any(nil), any(nil)
	raw := ""
	if value.Availability == XGAvailable {
		if old.FirstObservedAt != nil {
			firstValue = formatTime(*old.FirstObservedAt)
		} else {
			firstValue = formatTime(now)
		}
		home = value.HomeXG.Float64
		away = value.AwayXG.Float64
		homePoints = nullableFloat(value.HomeXPoints)
		awayPoints = nullableFloat(value.AwayXPoints)
		raw = value.RawJSON
	}
	_, err = tx.ExecContext(ctx, `UPDATE game_xg SET availability=?,home_team_id=?,away_team_id=?,home_xg=?,away_xg=?,home_xpoints=?,away_xpoints=?,raw_json=?,first_observed_at=?,last_checked_at=? WHERE asa_game_id=?`, value.Availability, value.HomeTeamID, value.AwayTeamID, home, away, homePoints, awayPoints, raw, firstValue, formatTime(now), value.GameID)
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
	return xgStatus(ctx, c.db, season, stage)
}

func xgStatus(ctx context.Context, dbq queryer, season, stage string) (XGStatus, error) {
	a, e := latestXGRun(ctx, dbq, "", season, stage)
	if e != nil {
		return XGStatus{}, e
	}
	s, e := latestXGRun(ctx, dbq, "success", season, stage)
	return XGStatus{a, s}, e
}
func (c *DB) latestXGRun(ctx context.Context, outcome, season, stage string) (*XGSyncRun, error) {
	return latestXGRun(ctx, c.db, outcome, season, stage)
}

func latestXGRun(ctx context.Context, dbq queryer, outcome, season, stage string) (*XGSyncRun, error) {
	query := `SELECT id,started_at,finished_at,season,stage,outcome,error_summary,rows_seen,available_games,unavailable_games,rows_inserted,rows_updated,rows_unchanged FROM xg_sync_runs WHERE season=? AND stage=?`
	args := []any{season, stage}
	if outcome != "" {
		query += ` AND outcome=?`
		args = append(args, outcome)
	}
	query += ` ORDER BY finished_at DESC,id DESC LIMIT 1`
	var run XGSyncRun
	var st, fi string
	err := dbq.QueryRowContext(ctx, query, args...).Scan(&run.ID, &st, &fi, &run.Season, &run.Stage, &run.Outcome, &run.ErrorSummary, &run.RowsSeen, &run.AvailableGames, &run.UnavailableGames, &run.RowsInserted, &run.RowsUpdated, &run.RowsUnchanged)
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
	return seasonXGoals(ctx, c.db, season, stage)
}

func seasonXGoals(ctx context.Context, dbq queryer, season, stage string) ([]GameXG, error) {
	rows, err := dbq.QueryContext(ctx, `SELECT x.asa_game_id,x.availability,x.home_team_id,x.away_team_id,x.home_xg,x.away_xg,x.home_xpoints,x.away_xpoints,x.raw_json,x.first_observed_at,x.last_checked_at FROM game_xg x JOIN games g ON g.asa_game_id=x.asa_game_id WHERE g.season=? AND g.stage=? ORDER BY g.kickoff_utc,g.asa_game_id`, season, stage)
	if err != nil {
		return nil, fmt.Errorf("load xG: %w", err)
	}
	defer rows.Close()
	values := []GameXG{}
	for rows.Next() {
		var v GameXG
		var first sql.NullString
		var checked string
		if err := rows.Scan(&v.GameID, &v.Availability, &v.HomeTeamID, &v.AwayTeamID, &v.HomeXG, &v.AwayXG, &v.HomeXPoints, &v.AwayXPoints, &v.RawJSON, &first, &checked); err != nil {
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
	return standingsTeams(ctx, c.db, season, stage)
}

func standingsTeams(ctx context.Context, dbq queryer, season, stage string) ([]standings.Team, error) {
	rows, err := dbq.QueryContext(ctx, `SELECT DISTINCT
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
	return seasonGames(ctx, c.db, season, stage)
}

func seasonGames(ctx context.Context, dbq queryer, season, stage string) ([]Game, error) {
	rows, err := dbq.QueryContext(ctx, `SELECT
		asa_game_id, season, stage, kickoff_utc, status, home_team_id, away_team_id,
		home_score, away_score, matchday, expanded_minutes, knockout_game, last_updated_utc, raw_json
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
			&game.Matchday, &game.ExpandedMinutes, &game.KnockoutGame, &game.LastUpdatedUTC, &game.RawJSON,
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
	return latestRun(ctx, c.db, outcome, season, stage)
}

func latestRun(ctx context.Context, dbq queryer, outcome, season, stage string) (*SyncRun, error) {
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
	err := dbq.QueryRowContext(ctx, query, args...).Scan(
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

func nullableFloat(value sql.NullFloat64) any {
	if !value.Valid {
		return nil
	}
	return value.Float64
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
	writeSnapshotString("fixture-snapshot-v3")
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
		writeNull(g.ExpandedMinutes)
		writeSnapshotString(strconv.FormatBool(g.KnockoutGame))
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
func nonNilClauses(v []scenarios.Clause) []scenarios.Clause {
	if v == nil {
		return []scenarios.Clause{}
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
	rows, err := c.db.QueryContext(ctx, `SELECT team_id,achievement,top_k,opportunity_state,already_clinched,can_clinch,clauses_json,necessary_json,proof_methods_json,limitation,total_assignments,certified_assignments,unresolved_assignments,diagnostics_json,already_eliminated,can_be_eliminated,elimination_clauses_json FROM scenario_results WHERE scenario_run_id=? ORDER BY team_id,achievement`, out.Run.ID)
	if err != nil {
		return out, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var v ScenarioResult
		var achievement, state string
		var already, can, alreadyEliminated, canBeEliminated int
		var clauses, necessary, methods, diag, eliminationClauses string
		if err := rows.Scan(&v.TeamID, &achievement, &v.TopK, &state, &already, &can, &clauses, &necessary, &methods, &v.Limitation, &v.TotalAssignments, &v.CertifiedAssignments, &v.UnresolvedAssignments, &diag, &alreadyEliminated, &canBeEliminated, &eliminationClauses); err != nil {
			return out, false, err
		}
		v.Achievement = competition.AchievementID(achievement)
		v.State = scenarios.OpportunityState(state)
		v.AlreadyClinched = already != 0
		v.CanClinch = can != 0
		v.AlreadyEliminated = alreadyEliminated != 0
		v.CanBeEliminated = canBeEliminated != 0
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
		if err := json.Unmarshal([]byte(eliminationClauses), &v.EliminationClauses); err != nil {
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
		if v.EliminationClauses == nil {
			v.EliminationClauses = []scenarios.Clause{}
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
		eliminationClauses, _ := json.Marshal(nonNilClauses(v.EliminationClauses))
		if _, err := tx.ExecContext(ctx, `INSERT INTO scenario_results VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, v.TeamID, v.Achievement, v.TopK, v.State, boolInt(v.AlreadyClinched), boolInt(v.CanClinch), string(clauses), string(necessary), string(methods), v.Limitation, v.TotalAssignments, v.CertifiedAssignments, v.UnresolvedAssignments, string(diag), boolInt(v.AlreadyEliminated), boolInt(v.CanBeEliminated), string(eliminationClauses)); err != nil {
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
