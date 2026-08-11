package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type TargetedRefreshMetadata struct {
	Trigger               SourceRefreshTrigger
	StartedAt, FinishedAt time.Time
}
type CheckedGameRequest struct {
	ASAID             string
	NextDueAt         *time.Time
	MaterialNextDueAt *time.Time
}
type GameResultCheckState struct {
	GameID, Season, Stage                                    string
	LastCheckedAt                                            time.Time
	FirstTerminalObservedAt, LastMaterialChangeAt, NextDueAt *time.Time
}

func (c *DB) GameResultCheckState(ctx context.Context, gameID string) (GameResultCheckState, bool, error) {
	if invalidTrimmed(gameID) {
		return GameResultCheckState{}, false, errors.New("game result check has blank or untrimmed game ID")
	}
	row := c.db.QueryRowContext(ctx, `SELECT c.asa_game_id,g.season,g.stage,c.last_checked_at,c.first_terminal_observed_at,c.last_material_change_at,c.next_due_at FROM game_result_checks c JOIN games g ON g.asa_game_id=c.asa_game_id WHERE c.asa_game_id=?`, gameID)
	s, err := scanGameResultCheckState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GameResultCheckState{}, false, nil
	}
	if err != nil {
		return GameResultCheckState{}, false, fmt.Errorf("load game result check %q: %w", gameID, err)
	}
	return s, true, nil
}
func (c *DB) GameResultCheckStates(ctx context.Context, season, stage string) ([]GameResultCheckState, error) {
	if invalidTrimmed(season) || invalidTrimmed(stage) {
		return nil, errors.New("game result checks have blank or untrimmed scope")
	}
	rows, err := c.db.QueryContext(ctx, `SELECT c.asa_game_id,g.season,g.stage,c.last_checked_at,c.first_terminal_observed_at,c.last_material_change_at,c.next_due_at FROM game_result_checks c JOIN games g ON g.asa_game_id=c.asa_game_id WHERE g.season=? AND g.stage=? ORDER BY c.asa_game_id`, season, stage)
	if err != nil {
		return nil, fmt.Errorf("query game result checks: %w", err)
	}
	defer rows.Close()
	out := []GameResultCheckState{}
	for rows.Next() {
		s, e := scanGameResultCheckState(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate game result checks: %w", err)
	}
	return out, nil
}

type gameCheckScanner interface{ Scan(...any) error }

func scanGameResultCheckState(s gameCheckScanner) (GameResultCheckState, error) {
	var out GameResultCheckState
	var checked string
	var first, material, due sql.NullString
	if err := s.Scan(&out.GameID, &out.Season, &out.Stage, &checked, &first, &material, &due); err != nil {
		return GameResultCheckState{}, err
	}
	if invalidTrimmed(out.GameID) || invalidTrimmed(out.Season) || invalidTrimmed(out.Stage) {
		return GameResultCheckState{}, errors.New("invalid stored game result check identity")
	}
	var err error
	if out.LastCheckedAt, err = time.Parse(time.RFC3339, checked); err != nil {
		return GameResultCheckState{}, fmt.Errorf("parse game result check timestamp: %w", err)
	}
	out.LastCheckedAt = out.LastCheckedAt.UTC()
	for _, v := range []struct {
		raw  sql.NullString
		dest **time.Time
	}{{first, &out.FirstTerminalObservedAt}, {material, &out.LastMaterialChangeAt}, {due, &out.NextDueAt}} {
		if v.raw.Valid {
			p, e := time.Parse(time.RFC3339, v.raw.String)
			if e != nil {
				return GameResultCheckState{}, fmt.Errorf("parse game result check timestamp: %w", e)
			}
			p = p.UTC()
			*v.dest = &p
		}
	}
	return out, nil
}

func (c *DB) UpsertCheckedGames(ctx context.Context, season, stage string, requested []CheckedGameRequest, returned []Game, metadata TargetedRefreshMetadata) (GameRefreshResult, error) {
	requests, audit, err := prepareCheckedGames(season, stage, requested, returned, metadata)
	if err != nil {
		return GameRefreshResult{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return GameRefreshResult{}, fmt.Errorf("begin targeted game refresh: %w", err)
	}
	defer rollback(tx)
	old := map[string]Game{}
	for _, r := range requests {
		var g Game
		err := tx.QueryRowContext(ctx, `SELECT asa_game_id,season,stage,kickoff_utc,status,home_team_id,away_team_id,home_score,away_score,matchday,expanded_minutes,knockout_game,last_updated_utc,raw_json FROM games WHERE asa_game_id=?`, r.ASAID).Scan(&g.ASAID, &g.Season, &g.Stage, &g.KickoffUTC, &g.Status, &g.HomeTeamID, &g.AwayTeamID, &g.HomeScore, &g.AwayScore, &g.Matchday, &g.ExpandedMinutes, &g.KnockoutGame, &g.LastUpdatedUTC, &g.RawJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return GameRefreshResult{}, fmt.Errorf("requested game %q is not cached", r.ASAID)
		}
		if err != nil {
			return GameRefreshResult{}, err
		}
		if g.Season != season || g.Stage != stage {
			return GameRefreshResult{}, fmt.Errorf("requested game %q is outside scope", r.ASAID)
		}
		old[g.ASAID] = g
	}
	for _, g := range returned {
		cached := old[g.ASAID]
		if cached.HomeTeamID != g.HomeTeamID || cached.AwayTeamID != g.AwayTeamID {
			return GameRefreshResult{}, fmt.Errorf("returned game %q participant identity mismatch", g.ASAID)
		}
	}
	before, err := seasonGames(ctx, tx, season, stage)
	if err != nil {
		return GameRefreshResult{}, err
	}
	beforeTeams, err := inventoryTeams(ctx, tx, season, stage)
	if err != nil {
		return GameRefreshResult{}, err
	}
	beforeSnapshot, err := FixtureSnapshotID(beforeTeams, before)
	if err != nil {
		return GameRefreshResult{}, err
	}
	coverage := false
	materialByID := map[string]bool{}
	terminalByID := map[string]bool{}
	for _, g := range returned {
		cached := old[g.ASAID]
		terminalByID[g.ASAID] = gameTerminal(g)
		if !preferIncomingGame(cached, g) {
			audit.RowsUnchanged++
			continue
		}
		material := !equalFixtureGame(cached, g)
		materialByID[g.ASAID] = material
		if gameFullTime(cached) != gameFullTime(g) {
			coverage = true
		}
		change, e := writeGame(ctx, tx, g, audit.FinishedAt)
		if e != nil {
			return GameRefreshResult{}, e
		}
		if change == rowUpdated {
			audit.RowsUpdated++
		} else {
			audit.RowsUnchanged++
		}
	}
	for _, r := range requests {
		due := r.NextDueAt
		if materialByID[r.ASAID] && r.MaterialNextDueAt != nil {
			due = r.MaterialNextDueAt
		}
		if err := upsertGameResultCheck(ctx, tx, r.ASAID, audit.FinishedAt, due, false, terminalByID[r.ASAID], materialByID[r.ASAID]); err != nil {
			return GameRefreshResult{}, err
		}
	}
	post, err := seasonGames(ctx, tx, season, stage)
	if err != nil {
		return GameRefreshResult{}, err
	}
	teams, err := inventoryTeams(ctx, tx, season, stage)
	if err != nil {
		return GameRefreshResult{}, err
	}
	snapshot, err := FixtureSnapshotID(teams, post)
	if err != nil {
		return GameRefreshResult{}, err
	}
	audit.DownstreamInputsChanged = beforeSnapshot != snapshot
	if audit.DownstreamInputsChanged {
		if err := updateSplitVenueFixtureSummary(ctx, tx, season, stage, audit.FinishedAt, coverage); err != nil {
			return GameRefreshResult{}, err
		}
	}
	run := &SyncRun{StartedAt: audit.StartedAt, FinishedAt: audit.FinishedAt, Season: season, Stage: stage, Outcome: "success", GamesUpserted: len(returned), GamesSeen: len(returned), GamesUpdated: audit.RowsUpdated, GamesUnchanged: audit.RowsUnchanged, FixtureSnapshotID: snapshot}
	if err := insertSyncRun(ctx, tx, run); err != nil {
		return GameRefreshResult{}, err
	}
	if err := recordSourceRefresh(ctx, tx, &audit, nil); err != nil {
		return GameRefreshResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return GameRefreshResult{}, fmt.Errorf("commit targeted game refresh: %w", err)
	}
	return GameRefreshResult{Audit: audit, SyncRun: run, Teams: cloneTeams(teams), Games: cloneGames(post), PreviousGames: cloneGames(before)}, nil
}
func prepareCheckedGames(season, stage string, requested []CheckedGameRequest, returned []Game, metadata TargetedRefreshMetadata) ([]CheckedGameRequest, SourceRefreshAudit, error) {
	if invalidTrimmed(season) || invalidTrimmed(stage) {
		return nil, SourceRefreshAudit{}, errors.New("targeted games have blank or untrimmed scope")
	}
	if requested == nil || len(requested) == 0 {
		return nil, SourceRefreshAudit{}, errors.New("targeted game requests are empty")
	}
	if returned == nil {
		return nil, SourceRefreshAudit{}, errors.New("targeted game response is nil")
	}
	seen := map[string]bool{}
	out := make([]CheckedGameRequest, len(requested))
	for i, r := range requested {
		if invalidTrimmed(r.ASAID) || seen[r.ASAID] {
			return nil, SourceRefreshAudit{}, errors.New("targeted games have invalid request IDs")
		}
		seen[r.ASAID] = true
		out[i] = r
		for _, due := range []*time.Time{r.NextDueAt, r.MaterialNextDueAt} {
			if due != nil && due.Before(metadata.FinishedAt) {
				return nil, SourceRefreshAudit{}, errors.New("game check due is before finish")
			}
		}
		if r.NextDueAt != nil {
			d := r.NextDueAt.UTC().Truncate(time.Second)
			out[i].NextDueAt = &d
		}
		if r.MaterialNextDueAt != nil {
			d := r.MaterialNextDueAt.UTC().Truncate(time.Second)
			out[i].MaterialNextDueAt = &d
		}
	}
	returnedIDs := map[string]bool{}
	for _, g := range returned {
		if !seen[g.ASAID] || returnedIDs[g.ASAID] {
			return nil, SourceRefreshAudit{}, errors.New("targeted games contain invalid returned IDs")
		}
		returnedIDs[g.ASAID] = true
		if err := validateGameRow(season, stage, g); err != nil {
			return nil, SourceRefreshAudit{}, err
		}
	}
	audit, due, err := prepareSourceRefresh(SourceRefreshAudit{Resource: SourceResourceGames, Season: season, Stage: stage, Mode: SourceRefreshTargeted, Trigger: metadata.Trigger, StartedAt: metadata.StartedAt, FinishedAt: metadata.FinishedAt, Outcome: SourceRefreshSuccess, RequestedRows: len(requested), ReturnedRows: len(returned)}, nil)
	_ = due
	if err != nil {
		return nil, SourceRefreshAudit{}, err
	}
	return out, audit, nil
}
func upsertGameResultCheck(ctx context.Context, tx *sql.Tx, id string, finished time.Time, due *time.Time, preserveDue, terminal, material bool) error {
	var old GameResultCheckState
	row := tx.QueryRowContext(ctx, `SELECT c.asa_game_id,g.season,g.stage,c.last_checked_at,c.first_terminal_observed_at,c.last_material_change_at,c.next_due_at FROM game_result_checks c JOIN games g ON g.asa_game_id=c.asa_game_id WHERE c.asa_game_id=?`, id)
	old, err := scanGameResultCheckState(row)
	if errors.Is(err, sql.ErrNoRows) {
		var first, change any
		if terminal {
			first = formatTime(finished)
		}
		if material {
			change = formatTime(finished)
		}
		var duev any
		if due != nil {
			duev = formatTime(*due)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO game_result_checks (asa_game_id,last_checked_at,first_terminal_observed_at,last_material_change_at,next_due_at) VALUES (?,?,?,?,?)`, id, formatTime(finished), first, change, duev)
		return err
	}
	if err != nil {
		return err
	}
	first := old.FirstTerminalObservedAt
	if terminal && (first == nil || finished.Before(*first)) {
		v := finished
		first = &v
	}
	change := old.LastMaterialChangeAt
	if material && (change == nil || finished.After(*change)) {
		v := finished
		change = &v
	}
	checked := old.LastCheckedAt
	storedDue := old.NextDueAt
	if finished.After(old.LastCheckedAt) {
		checked = finished
		if !preserveDue {
			storedDue = due
		}
	}
	var firstv, changev, duev any
	if first != nil {
		firstv = formatTime(*first)
	}
	if change != nil {
		changev = formatTime(*change)
	}
	if storedDue != nil {
		duev = formatTime(*storedDue)
	}
	_, err = tx.ExecContext(ctx, `UPDATE game_result_checks SET last_checked_at=?,first_terminal_observed_at=?,last_material_change_at=?,next_due_at=? WHERE asa_game_id=?`, formatTime(checked), firstv, changev, duev, id)
	return err
}
func equalFixtureGame(a, b Game) bool {
	return a.ASAID == b.ASAID && a.Status == b.Status && a.HomeTeamID == b.HomeTeamID && a.AwayTeamID == b.AwayTeamID && a.HomeScore == b.HomeScore && a.AwayScore == b.AwayScore && a.KickoffUTC == b.KickoffUTC && a.Matchday == b.Matchday && a.ExpandedMinutes == b.ExpandedMinutes && a.KnockoutGame == b.KnockoutGame
}
