package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"

	_ "modernc.org/sqlite"
)

const schemaVersion = 2

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
	ID             int64
	StartedAt      time.Time
	FinishedAt     time.Time
	Season         string
	Stage          string
	Outcome        string
	ErrorSummary   string
	TeamsUpserted  int
	GamesUpserted  int
	GamesDeleted   int
	GamesSeen      int
	TeamsInserted  int
	TeamsUpdated   int
	TeamsUnchanged int
	GamesInserted  int
	GamesUpdated   int
	GamesUnchanged int
	Skipped        bool
}

// Status is the latest cache freshness summary.
type Status struct {
	LastAttempt *SyncRun
	LastSuccess *SyncRun
}

// RefreshSnapshot is the minimal cached state the background scheduler needs.
type RefreshSnapshot struct {
	Games       []Game
	LastAttempt *SyncRun
	LastSuccess *SyncRun
}

// ErrSyncInProgress means another process holds the lease for this cache stream.
var ErrSyncInProgress = errors.New("cache sync already in progress")

// SeasonData is the cached input needed to render one season and calculate its
// standings. Games retain their presentation metadata as well as their scores.
type SeasonData struct {
	Teams       []standings.Team
	Games       []Game
	LastSuccess *SyncRun
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
	if version < schemaVersion {
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
	return RefreshSnapshot{Games: games, LastAttempt: status.LastAttempt, LastSuccess: status.LastSuccess}, nil
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
	return SeasonData{Teams: teams, Games: games, LastSuccess: lastSuccess}, nil
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
	query := `SELECT id, started_at, finished_at, season, stage, outcome, error_summary,
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
		&run.ID, &startedAt, &finishedAt, &run.Season, &run.Stage, &run.Outcome, &run.ErrorSummary,
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
		started_at, finished_at, season, stage, outcome, error_summary,
		teams_upserted, games_upserted, games_deleted, games_seen,
		teams_inserted, teams_updated, teams_unchanged, games_inserted, games_updated, games_unchanged
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(run.StartedAt), formatTime(run.FinishedAt), run.Season, run.Stage, run.Outcome, run.ErrorSummary,
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
