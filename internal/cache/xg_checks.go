package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

type CheckedXGRequest struct {
	GameID            string
	NextDueAt         *time.Time
	MaterialNextDueAt *time.Time
}

type GameXGCheckState struct {
	GameID, Season, Stage string
	LastCheckedAt         time.Time
	FirstAvailableObservedAt, LastMaterialChangeAt,
	NextDueAt *time.Time
}

func (c *DB) GameXGCheckState(ctx context.Context, gameID string) (GameXGCheckState, bool, error) {
	if invalidTrimmed(gameID) {
		return GameXGCheckState{}, false, errors.New("xG check has blank or untrimmed game ID")
	}
	row := c.db.QueryRowContext(ctx, `SELECT c.asa_game_id,g.season,g.stage,c.last_checked_at,c.first_available_observed_at,c.last_material_change_at,c.next_due_at
		FROM game_xg_checks c JOIN games g ON g.asa_game_id=c.asa_game_id WHERE c.asa_game_id=?`, gameID)
	state, err := scanGameXGCheckState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GameXGCheckState{}, false, nil
	}
	if err != nil {
		return GameXGCheckState{}, false, fmt.Errorf("load xG check %q: %w", gameID, err)
	}
	return state, true, nil
}

func (c *DB) GameXGCheckStates(ctx context.Context, season, stage string) ([]GameXGCheckState, error) {
	if invalidTrimmed(season) || invalidTrimmed(stage) {
		return nil, errors.New("xG checks have blank or untrimmed scope")
	}
	rows, err := c.db.QueryContext(ctx, `SELECT c.asa_game_id,g.season,g.stage,c.last_checked_at,c.first_available_observed_at,c.last_material_change_at,c.next_due_at
		FROM game_xg_checks c JOIN games g ON g.asa_game_id=c.asa_game_id
		WHERE g.season=? AND g.stage=? ORDER BY c.asa_game_id`, season, stage)
	if err != nil {
		return nil, fmt.Errorf("query xG checks: %w", err)
	}
	defer rows.Close()
	states := []GameXGCheckState{}
	for rows.Next() {
		state, err := scanGameXGCheckState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xG checks: %w", err)
	}
	return states, nil
}

type gameXGCheckScanner interface{ Scan(...any) error }

func scanGameXGCheckState(scanner gameXGCheckScanner) (GameXGCheckState, error) {
	var state GameXGCheckState
	var checked string
	var first, material, due sql.NullString
	if err := scanner.Scan(&state.GameID, &state.Season, &state.Stage, &checked, &first, &material, &due); err != nil {
		return GameXGCheckState{}, err
	}
	if invalidTrimmed(state.GameID) || invalidTrimmed(state.Season) || invalidTrimmed(state.Stage) {
		return GameXGCheckState{}, errors.New("invalid stored xG check identity")
	}
	var err error
	state.LastCheckedAt, err = time.Parse(time.RFC3339, checked)
	if err != nil {
		return GameXGCheckState{}, fmt.Errorf("parse xG check last check: %w", err)
	}
	state.LastCheckedAt = state.LastCheckedAt.UTC()
	for _, field := range []struct {
		raw  sql.NullString
		dest **time.Time
	}{{first, &state.FirstAvailableObservedAt}, {material, &state.LastMaterialChangeAt}, {due, &state.NextDueAt}} {
		if !field.raw.Valid {
			continue
		}
		v, err := time.Parse(time.RFC3339, field.raw.String)
		if err != nil {
			return GameXGCheckState{}, fmt.Errorf("parse xG check timestamp: %w", err)
		}
		v = v.UTC()
		*field.dest = &v
	}
	return state, nil
}

func (c *DB) UpsertCheckedXG(ctx context.Context, season, stage string, requested []CheckedXGRequest, returned []GameXG, metadata TargetedRefreshMetadata) (XGRefreshResult, error) {
	requests, audit, err := prepareCheckedXG(season, stage, requested, returned, metadata)
	if err != nil {
		return XGRefreshResult{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return XGRefreshResult{}, fmt.Errorf("begin targeted xG refresh: %w", err)
	}
	defer rollback(tx)

	games := make(map[string]Game, len(requests))
	badIDs := make(map[string]struct{})
	for _, request := range requests {
		var game Game
		err := tx.QueryRowContext(ctx, `SELECT asa_game_id,season,stage,kickoff_utc,status,home_team_id,away_team_id,home_score,away_score,matchday,last_updated_utc,raw_json FROM games WHERE asa_game_id=?`, request.GameID).Scan(
			&game.ASAID, &game.Season, &game.Stage, &game.KickoffUTC, &game.Status, &game.HomeTeamID, &game.AwayTeamID, &game.HomeScore, &game.AwayScore, &game.Matchday, &game.LastUpdatedUTC, &game.RawJSON)
		if errors.Is(err, sql.ErrNoRows) {
			badIDs[request.GameID] = struct{}{}
			continue
		}
		if err != nil {
			return XGRefreshResult{}, fmt.Errorf("load requested xG game %q: %w", request.GameID, err)
		}
		if game.Season != season || game.Stage != stage || !gameFullTime(game) {
			badIDs[request.GameID] = struct{}{}
			continue
		}
		games[request.GameID] = game
	}
	values := make(map[string]GameXG, len(returned))
	for _, value := range returned {
		values[value.GameID] = value
		if game, ok := games[value.GameID]; !ok || game.HomeTeamID != value.HomeTeamID || game.AwayTeamID != value.AwayTeamID {
			badIDs[value.GameID] = struct{}{}
		}
	}
	if len(badIDs) != 0 {
		ids := make([]string, 0, len(badIDs))
		for id := range badIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return XGRefreshResult{}, fmt.Errorf("invalid targeted xG identities %v", ids)
	}

	ids := make([]string, 0, len(requests))
	due := make(map[string]*time.Time, len(requests))
	requestsByID := make(map[string]CheckedXGRequest, len(requests))
	for _, request := range requests {
		ids = append(ids, request.GameID)
		due[request.GameID] = request.NextDueAt
		requestsByID[request.GameID] = request
	}
	sort.Strings(ids)
	material := false
	for _, id := range ids {
		value, returnedValue := values[id]
		rowMaterial := false
		if returnedValue {
			change, changed, err := writeAvailableStageXG(ctx, tx, value, audit.FinishedAt)
			if err != nil {
				return XGRefreshResult{}, err
			}
			rowMaterial = changed
			material = material || changed
			switch change {
			case rowInserted:
				audit.RowsInserted++
			case rowUpdated:
				audit.RowsUpdated++
			default:
				audit.RowsUnchanged++
			}
		}
		nextDue := due[id]
		if rowMaterial && requestsByID[id].MaterialNextDueAt != nil {
			nextDue = requestsByID[id].MaterialNextDueAt
		}
		if err := upsertGameXGCheck(ctx, tx, id, audit.FinishedAt, nextDue, false, returnedValue, rowMaterial); err != nil {
			return XGRefreshResult{}, err
		}
	}
	result, err := stageXGStates(ctx, tx, season, stage)
	if err != nil {
		return XGRefreshResult{}, err
	}
	if material {
		if err := updateTargetedVenueXGSummary(ctx, tx, season, stage, audit.FinishedAt); err != nil {
			return XGRefreshResult{}, err
		}
	}
	audit.DownstreamInputsChanged = material
	run := &XGSyncRun{StartedAt: audit.StartedAt, FinishedAt: audit.FinishedAt, Season: season, Stage: stage, Outcome: "success", RowsSeen: int64(len(returned)), AvailableGames: int64(len(returned)), RowsInserted: int64(audit.RowsInserted), RowsUpdated: int64(audit.RowsUpdated), RowsUnchanged: int64(audit.RowsUnchanged)}
	if err := insertXGSyncRun(ctx, tx, run); err != nil {
		return XGRefreshResult{}, err
	}
	if err := recordSourceRefresh(ctx, tx, &audit, nil); err != nil {
		return XGRefreshResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return XGRefreshResult{}, fmt.Errorf("commit targeted xG refresh: %w", err)
	}
	return XGRefreshResult{Audit: audit, XGRun: run, Values: append([]GameXG{}, result...)}, nil
}

func prepareCheckedXG(season, stage string, requested []CheckedXGRequest, returned []GameXG, metadata TargetedRefreshMetadata) ([]CheckedXGRequest, SourceRefreshAudit, error) {
	if invalidTrimmed(season) || invalidTrimmed(stage) {
		return nil, SourceRefreshAudit{}, errors.New("targeted xG has blank or untrimmed scope")
	}
	if requested == nil || len(requested) == 0 {
		return nil, SourceRefreshAudit{}, errors.New("targeted xG requests are empty")
	}
	if returned == nil {
		return nil, SourceRefreshAudit{}, errors.New("targeted xG response is nil")
	}
	requests := make([]CheckedXGRequest, len(requested))
	seen := make(map[string]bool, len(requested))
	for i, request := range requested {
		if invalidTrimmed(request.GameID) || seen[request.GameID] {
			return nil, SourceRefreshAudit{}, errors.New("targeted xG has invalid request IDs")
		}
		seen[request.GameID] = true
		requests[i] = request
		for _, dueValue := range []*time.Time{request.NextDueAt, request.MaterialNextDueAt} {
			if dueValue != nil && dueValue.Before(metadata.FinishedAt) {
				return nil, SourceRefreshAudit{}, errors.New("xG check due is before finish")
			}
		}
		if request.NextDueAt != nil {
			due := request.NextDueAt.UTC().Truncate(time.Second)
			requests[i].NextDueAt = &due
		}
		if request.MaterialNextDueAt != nil {
			due := request.MaterialNextDueAt.UTC().Truncate(time.Second)
			requests[i].MaterialNextDueAt = &due
		}
	}
	returnedIDs := make(map[string]bool, len(returned))
	for _, value := range returned {
		if !seen[value.GameID] || returnedIDs[value.GameID] {
			return nil, SourceRefreshAudit{}, errors.New("targeted xG contains invalid returned IDs")
		}
		returnedIDs[value.GameID] = true
		if err := validateStageXG(value); err != nil {
			return nil, SourceRefreshAudit{}, err
		}
	}
	audit, ignored, err := prepareSourceRefresh(SourceRefreshAudit{Resource: SourceResourceGameXG, Season: season, Stage: stage, Mode: SourceRefreshTargeted, Trigger: metadata.Trigger, StartedAt: metadata.StartedAt, FinishedAt: metadata.FinishedAt, Outcome: SourceRefreshSuccess, RequestedRows: len(requested), ReturnedRows: len(returned)}, nil)
	_ = ignored
	if err != nil {
		return nil, SourceRefreshAudit{}, err
	}
	return requests, audit, nil
}

func upsertGameXGCheck(ctx context.Context, tx *sql.Tx, id string, finished time.Time, due *time.Time, preserveDue, available, material bool) error {
	row := tx.QueryRowContext(ctx, `SELECT c.asa_game_id,g.season,g.stage,c.last_checked_at,c.first_available_observed_at,c.last_material_change_at,c.next_due_at
		FROM game_xg_checks c JOIN games g ON g.asa_game_id=c.asa_game_id WHERE c.asa_game_id=?`, id)
	old, err := scanGameXGCheckState(row)
	if errors.Is(err, sql.ErrNoRows) {
		var first, changed, next any
		if available {
			first = formatTime(finished)
		}
		if material {
			changed = formatTime(finished)
		}
		if due != nil {
			next = formatTime(*due)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO game_xg_checks(asa_game_id,last_checked_at,first_available_observed_at,last_material_change_at,next_due_at) VALUES(?,?,?,?,?)`, id, formatTime(finished), first, changed, next)
		return err
	}
	if err != nil {
		return err
	}
	first := old.FirstAvailableObservedAt
	if available && (first == nil || finished.Before(*first)) {
		v := finished
		first = &v
	}
	changed := old.LastMaterialChangeAt
	if material && (changed == nil || finished.After(*changed)) {
		v := finished
		changed = &v
	}
	checked, next := old.LastCheckedAt, old.NextDueAt
	if finished.After(checked) {
		checked = finished
		if !preserveDue {
			next = due
		}
	}
	var firstValue, changedValue, dueValue any
	if first != nil {
		firstValue = formatTime(*first)
	}
	if changed != nil {
		changedValue = formatTime(*changed)
	}
	if next != nil {
		dueValue = formatTime(*next)
	}
	_, err = tx.ExecContext(ctx, `UPDATE game_xg_checks SET last_checked_at=?,first_available_observed_at=?,last_material_change_at=?,next_due_at=? WHERE asa_game_id=?`, formatTime(checked), firstValue, changedValue, dueValue, id)
	return err
}

func updateTargetedVenueXGSummary(ctx context.Context, tx *sql.Tx, season, stage string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO venue_summaries (
		season,stage,fixture_ready,xg_ready,matches,home_goals,away_goals,home_points,away_points,xg_matches,home_xg,away_xg,updated_at
	) SELECT ?,?,0,0,0,0,0,0,0,COUNT(*),COALESCE(SUM(x.home_xg),0),COALESCE(SUM(x.away_xg),0),?
	FROM games g JOIN game_xg x ON x.asa_game_id=g.asa_game_id
	WHERE g.season=? AND g.stage=? AND g.status='FullTime' AND x.availability='available'
	ON CONFLICT(season,stage) DO UPDATE SET
		xg_matches=excluded.xg_matches,home_xg=excluded.home_xg,away_xg=excluded.away_xg,updated_at=excluded.updated_at`, season, stage, formatTime(now), season, stage)
	if err != nil {
		return fmt.Errorf("update targeted venue xG summary: %w", err)
	}
	return nil
}
