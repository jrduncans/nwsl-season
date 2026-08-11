package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

// GameRefreshResult is the complete committed result of an authoritative game
// inventory observation.
type GameRefreshResult struct {
	Audit   SourceRefreshAudit
	SyncRun *SyncRun
	Teams   []Team
	Games   []Game
	// PreviousGames is the cached inventory immediately before this source
	// observation. It lets callers explain an update decision against the
	// exact source version that was already live, without another read.
	PreviousGames []Game
}

var ErrUnknownGameTeams = errors.New("game inventory references unknown teams")

// UnknownGameTeamsError identifies the catalog identities a caller must fetch
// before retrying an otherwise valid game inventory write.
type UnknownGameTeamsError struct {
	TeamIDs []string
}

func (e *UnknownGameTeamsError) Error() string {
	return fmt.Sprintf("%v: %s", ErrUnknownGameTeams, strings.Join(e.TeamIDs, ", "))
}

func (e *UnknownGameTeamsError) Unwrap() error { return ErrUnknownGameTeams }

// ReplaceGameInventory atomically reconciles one complete, nonempty game
// inventory. An empty first discovery is recorded but cannot replace fixtures.
func (c *DB) ReplaceGameInventory(ctx context.Context, season, stage string, games []Game, expected *competition.InventoryExpectation, metadata FullRefreshMetadata) (GameRefreshResult, error) {
	if err := validateGameInventoryInput(season, stage, games, expected); err != nil {
		return GameRefreshResult{}, err
	}
	audit, due, err := prepareSourceRefresh(SourceRefreshAudit{
		Resource: SourceResourceGames, Season: season, Stage: stage,
		Mode: SourceRefreshFull, Trigger: metadata.Trigger,
		StartedAt: metadata.StartedAt, FinishedAt: metadata.FinishedAt,
		Outcome: SourceRefreshSuccess, ReturnedRows: len(games),
	}, metadata.NextFullDueAt)
	if err != nil {
		return GameRefreshResult{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return GameRefreshResult{}, fmt.Errorf("begin game inventory refresh: %w", err)
	}
	defer rollback(tx)

	before, err := seasonGames(ctx, tx, season, stage)
	if err != nil {
		return GameRefreshResult{}, err
	}
	if len(games) == 0 {
		if len(before) != 0 {
			return GameRefreshResult{}, errors.New("refusing to replace populated game inventory with empty response")
		}
		if err := recordSourceRefresh(ctx, tx, &audit, due); err != nil {
			return GameRefreshResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return GameRefreshResult{}, fmt.Errorf("commit empty game discovery: %w", err)
		}
		return GameRefreshResult{Audit: audit, Teams: []Team{}, Games: []Game{}}, nil
	}

	if err := validateGameInventoryDatabaseIdentity(ctx, tx, season, stage, games); err != nil {
		return GameRefreshResult{}, err
	}
	beforeTeams, err := inventoryTeams(ctx, tx, season, stage)
	if err != nil {
		return GameRefreshResult{}, err
	}
	beforeSnapshot, err := FixtureSnapshotID(beforeTeams, before)
	if err != nil {
		return GameRefreshResult{}, fmt.Errorf("calculate pre-write fixture snapshot: %w", err)
	}
	old := make(map[string]Game, len(before))
	for _, game := range before {
		old[game.ASAID] = game
	}
	coverageInvalid := false
	for _, game := range games {
		existing, found := old[game.ASAID]
		if found && !preferIncomingGame(existing, game) {
			audit.RowsUnchanged++
			if err := upsertGameResultCheck(ctx, tx, game.ASAID, audit.FinishedAt, nil, true, gameTerminal(game), false); err != nil {
				return GameRefreshResult{}, err
			}
			continue
		}
		if found && (existing.HomeTeamID != game.HomeTeamID || existing.AwayTeamID != game.AwayTeamID) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM game_xg_checks WHERE asa_game_id=?`, game.ASAID); err != nil {
				return GameRefreshResult{}, fmt.Errorf("delete incompatible game xG check %q: %w", game.ASAID, err)
			}
			var hasXG int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_xg WHERE asa_game_id=?)`, game.ASAID).Scan(&hasXG); err != nil {
				return GameRefreshResult{}, fmt.Errorf("check game xG %q: %w", game.ASAID, err)
			}
			if hasXG != 0 {
				if _, err := tx.ExecContext(ctx, `DELETE FROM game_xg WHERE asa_game_id=?`, game.ASAID); err != nil {
					return GameRefreshResult{}, fmt.Errorf("delete incompatible game xG %q: %w", game.ASAID, err)
				}
				coverageInvalid = true
			}
		}
		if (!found && gameFullTime(game)) || (found && gameFullTime(existing) != gameFullTime(game)) {
			coverageInvalid = true
		}
		change, err := writeGame(ctx, tx, game, audit.FinishedAt)
		if err != nil {
			return GameRefreshResult{}, err
		}
		switch change {
		case rowInserted:
			audit.RowsInserted++
		case rowUpdated:
			audit.RowsUpdated++
		default:
			audit.RowsUnchanged++
		}
		if err := upsertGameResultCheck(ctx, tx, game.ASAID, audit.FinishedAt, nil, true, gameTerminal(game), !found || (change == rowUpdated && !equalFixtureGame(existing, game))); err != nil {
			return GameRefreshResult{}, err
		}
	}
	keep := make(map[string]struct{}, len(games))
	for _, game := range games {
		keep[game.ASAID] = struct{}{}
	}
	for _, game := range before {
		if _, ok := keep[game.ASAID]; !ok && gameFullTime(game) {
			coverageInvalid = true
		}
	}
	deleted, err := deleteMissingGames(ctx, tx, season, stage, gameIDs(games))
	if err != nil {
		return GameRefreshResult{}, err
	}
	audit.RowsDeleted = deleted

	post, err := seasonGames(ctx, tx, season, stage)
	if err != nil {
		return GameRefreshResult{}, err
	}
	if err := validateInventoryExpectation(post, expected); err != nil {
		return GameRefreshResult{}, err
	}
	teams, err := inventoryTeams(ctx, tx, season, stage)
	if err != nil {
		return GameRefreshResult{}, err
	}
	postSnapshot, err := FixtureSnapshotID(teams, post)
	if err != nil {
		return GameRefreshResult{}, fmt.Errorf("calculate post-write fixture snapshot: %w", err)
	}
	audit.DownstreamInputsChanged = beforeSnapshot != postSnapshot
	if audit.DownstreamInputsChanged {
		if err := updateSplitVenueFixtureSummary(ctx, tx, season, stage, audit.FinishedAt, coverageInvalid); err != nil {
			return GameRefreshResult{}, err
		}
	}
	run := &SyncRun{StartedAt: audit.StartedAt, FinishedAt: audit.FinishedAt, Season: season, Stage: stage, Outcome: "success", GamesUpserted: len(games), GamesSeen: len(games), GamesDeleted: deleted, GamesInserted: audit.RowsInserted, GamesUpdated: audit.RowsUpdated, GamesUnchanged: audit.RowsUnchanged, FixtureSnapshotID: postSnapshot}
	if err := insertSyncRun(ctx, tx, run); err != nil {
		return GameRefreshResult{}, err
	}
	if err := recordSourceRefresh(ctx, tx, &audit, due); err != nil {
		return GameRefreshResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return GameRefreshResult{}, fmt.Errorf("commit game inventory refresh: %w", err)
	}
	return GameRefreshResult{Audit: audit, SyncRun: run, Teams: cloneTeams(teams), Games: cloneGames(post), PreviousGames: cloneGames(before)}, nil
}

func validateGameInventoryInput(season, stage string, games []Game, expected *competition.InventoryExpectation) error {
	if invalidTrimmed(season) || invalidTrimmed(stage) {
		return errors.New("game inventory has blank or untrimmed scope")
	}
	if games == nil {
		return errors.New("game inventory is nil")
	}
	if err := validateInventoryExpectation(nil, expected); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(games))
	for _, game := range games {
		if err := validateGameRow(season, stage, game); err != nil {
			return err
		}
		if _, ok := seen[game.ASAID]; ok {
			return fmt.Errorf("game inventory contains duplicate game ID %q", game.ASAID)
		}
		seen[game.ASAID] = struct{}{}
	}
	return validateInventoryExpectation(games, expected)
}

// validateGameRow is shared by authoritative and targeted game persistence.
func validateGameRow(season, stage string, game Game) error {
	if invalidTrimmed(game.ASAID) || invalidTrimmed(game.HomeTeamID) || invalidTrimmed(game.AwayTeamID) || invalidTrimmed(game.KickoffUTC) || invalidTrimmed(game.Status) || invalidTrimmed(game.LastUpdatedUTC) {
		return errors.New("game inventory contains blank or untrimmed game fields")
	}
	if game.Season != season || game.Stage != stage {
		return fmt.Errorf("game %q does not match requested scope", game.ASAID)
	}
	if game.HomeTeamID == game.AwayTeamID {
		return fmt.Errorf("game %q has identical teams", game.ASAID)
	}
	if _, err := fixtures.ParseKickoff(game.KickoffUTC); err != nil {
		return fmt.Errorf("game %q has invalid kickoff: %w", game.ASAID, err)
	}
	if _, err := fixtures.ParseKickoff(game.LastUpdatedUTC); err != nil {
		return fmt.Errorf("game %q has invalid last update: %w", game.ASAID, err)
	}
	if game.Status != fixtures.PreMatchStatus && game.Status != fixtures.CompletedStatus && game.Status != fixtures.AbandonedStatus {
		return fmt.Errorf("game %q has invalid status", game.ASAID)
	}
	if game.HomeScore.Valid != game.AwayScore.Valid || (game.HomeScore.Valid && (game.HomeScore.Int64 < 0 || game.AwayScore.Int64 < 0)) {
		return fmt.Errorf("game %q has invalid scores", game.ASAID)
	}
	if game.Status == fixtures.CompletedStatus && !game.HomeScore.Valid {
		return fmt.Errorf("game %q is FullTime without scores", game.ASAID)
	}
	if game.Matchday.Valid && game.Matchday.Int64 < 0 {
		return fmt.Errorf("game %q has invalid matchday", game.ASAID)
	}
	if game.ExpandedMinutes.Valid && game.ExpandedMinutes.Int64 < 0 {
		return fmt.Errorf("game %q has invalid expanded minutes", game.ASAID)
	}
	if entry, ok := competition.Lookup(season, stage); ok && entry.Kind == competition.StageKindKnockout && !game.KnockoutGame {
		return fmt.Errorf("game %q is missing knockout classification", game.ASAID)
	}
	return nil
}

func validateInventoryExpectation(games []Game, expected *competition.InventoryExpectation) error {
	if expected == nil {
		return nil
	}
	if expected.Teams < 0 || expected.GamesPerTeam < 0 || expected.Games < 0 || (expected.Teams == 0 && expected.GamesPerTeam == 0 && expected.Games == 0) || (expected.Teams == 0) != (expected.GamesPerTeam == 0) {
		return errors.New("invalid inventory expectation")
	}
	derived := 0
	if expected.Teams > 0 {
		if expected.Teams*expected.GamesPerTeam%2 != 0 {
			return errors.New("inventory expectation has odd game product")
		}
		derived = expected.Teams * expected.GamesPerTeam / 2
		if expected.Games != 0 && expected.Games != derived {
			return errors.New("inventory expectation game count disagrees with team appearances")
		}
	}
	if len(games) == 0 {
		return nil
	}
	want := expected.Games
	if want == 0 {
		want = derived
	}
	if want != 0 && len(games) != want {
		return fmt.Errorf("inventory has %d games, want %d", len(games), want)
	}
	if expected.Teams == 0 {
		return nil
	}
	appearances := map[string]int{}
	for _, game := range games {
		appearances[game.HomeTeamID]++
		appearances[game.AwayTeamID]++
	}
	if len(appearances) != expected.Teams {
		return fmt.Errorf("inventory has %d teams, want %d", len(appearances), expected.Teams)
	}
	for id, count := range appearances {
		if count != expected.GamesPerTeam {
			return fmt.Errorf("team %q has %d appearances, want %d", id, count, expected.GamesPerTeam)
		}
	}
	return nil
}

func invalidTrimmed(value string) bool {
	return strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value
}

func validateGameInventoryDatabaseIdentity(ctx context.Context, tx *sql.Tx, season, stage string, games []Game) error {
	missing := map[string]struct{}{}
	for _, game := range games {
		var oldSeason, oldStage string
		err := tx.QueryRowContext(ctx, `SELECT season, stage FROM games WHERE asa_game_id=?`, game.ASAID).Scan(&oldSeason, &oldStage)
		if err == nil && (oldSeason != season || oldStage != stage) {
			return fmt.Errorf("game %q already belongs to %s %s", game.ASAID, oldSeason, oldStage)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load game identity %q: %w", game.ASAID, err)
		}
		for _, id := range []string{game.HomeTeamID, game.AwayTeamID} {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE asa_team_id=?)`, id).Scan(&exists); err != nil {
				return fmt.Errorf("check team %q: %w", id, err)
			}
			if exists == 0 {
				missing[id] = struct{}{}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	ids := make([]string, 0, len(missing))
	for id := range missing {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return &UnknownGameTeamsError{TeamIDs: ids}
}

func inventoryTeams(ctx context.Context, dbq queryer, season, stage string) ([]Team, error) {
	rows, err := dbq.QueryContext(ctx, `SELECT DISTINCT t.asa_team_id,t.name,t.short_name,t.abbreviation,t.raw_json FROM teams t JOIN games g ON g.home_team_id=t.asa_team_id OR g.away_team_id=t.asa_team_id WHERE g.season=? AND g.stage=? ORDER BY t.asa_team_id`, season, stage)
	if err != nil {
		return nil, fmt.Errorf("load inventory teams: %w", err)
	}
	defer rows.Close()
	teams := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ASAID, &t.Name, &t.ShortName, &t.Abbreviation, &t.RawJSON); err != nil {
			return nil, fmt.Errorf("scan inventory team: %w", err)
		}
		teams = append(teams, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory teams: %w", err)
	}
	return teams, nil
}

func preferIncomingGame(cached, incoming Game) bool {
	if equalGame(cached, incoming) {
		return false
	}
	if gameTerminal(incoming) && !gameTerminal(cached) {
		return true
	}
	if gameTerminal(cached) && !gameTerminal(incoming) {
		return false
	}
	// Scheduled ASA fixtures can omit last_updated_utc. In that case the
	// adapter uses kickoff as a stable fallback, so an earlier reschedule would
	// otherwise look stale. The complete inventory remains authoritative for
	// material changes to a fixture that is still pre-match.
	if cached.Status == fixtures.PreMatchStatus && incoming.Status == fixtures.PreMatchStatus && !equalFixtureGame(cached, incoming) {
		return true
	}
	incomingTime, err := fixtures.ParseKickoff(incoming.LastUpdatedUTC)
	if err != nil {
		return false
	}
	cachedTime, err := fixtures.ParseKickoff(cached.LastUpdatedUTC)
	return err != nil || incomingTime.After(cachedTime)
}

func gameTerminal(game Game) bool {
	return game.Status == fixtures.AbandonedStatus || (game.Status == fixtures.CompletedStatus && game.HomeScore.Valid && game.AwayScore.Valid)
}
func gameFullTime(game Game) bool {
	return game.Status == fixtures.CompletedStatus && game.HomeScore.Valid && game.AwayScore.Valid
}
func gameIDs(games []Game) []string {
	ids := make([]string, 0, len(games))
	for _, game := range games {
		ids = append(ids, game.ASAID)
	}
	return ids
}
func cloneTeams(teams []Team) []Team { return append([]Team{}, teams...) }
func cloneGames(games []Game) []Game { return append([]Game{}, games...) }

func updateSplitVenueFixtureSummary(ctx context.Context, tx *sql.Tx, season, stage string, now time.Time, invalidateXG bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO venue_summaries (season,stage,fixture_ready,xg_ready,matches,home_goals,away_goals,home_points,away_points,xg_matches,home_xg,away_xg,updated_at) SELECT ?,?,1,0,COUNT(*),COALESCE(SUM(home_score),0),COALESCE(SUM(away_score),0),COALESCE(SUM(CASE WHEN home_score>away_score THEN 3 WHEN home_score=away_score THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN away_score>home_score THEN 3 WHEN home_score=away_score THEN 1 ELSE 0 END),0),0,0,0,? FROM games WHERE season=? AND stage=? AND status='FullTime' AND home_score IS NOT NULL AND away_score IS NOT NULL ON CONFLICT(season,stage) DO UPDATE SET fixture_ready=1,matches=excluded.matches,home_goals=excluded.home_goals,away_goals=excluded.away_goals,home_points=excluded.home_points,away_points=excluded.away_points,updated_at=excluded.updated_at`, season, stage, formatTime(now), season, stage)
	if err != nil {
		return fmt.Errorf("update split venue fixture summary: %w", err)
	}
	if !invalidateXG {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE venue_summaries SET xg_ready=0,xg_matches=(SELECT COUNT(*) FROM games g JOIN game_xg x ON x.asa_game_id=g.asa_game_id WHERE g.season=? AND g.stage=? AND g.status='FullTime' AND x.availability='available'),home_xg=(SELECT COALESCE(SUM(x.home_xg),0) FROM games g JOIN game_xg x ON x.asa_game_id=g.asa_game_id WHERE g.season=? AND g.stage=? AND g.status='FullTime' AND x.availability='available'),away_xg=(SELECT COALESCE(SUM(x.away_xg),0) FROM games g JOIN game_xg x ON x.asa_game_id=g.asa_game_id WHERE g.season=? AND g.stage=? AND g.status='FullTime' AND x.availability='available'),updated_at=? WHERE season=? AND stage=?`, season, stage, season, stage, season, stage, formatTime(now), season, stage)
	if err != nil {
		return fmt.Errorf("invalidate split venue xG summary: %w", err)
	}
	return nil
}
