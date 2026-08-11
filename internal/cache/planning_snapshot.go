package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PlanningSnapshot is one consistent, read-only view of source freshness and
// cached resource state. It is deliberately a data transfer object: callers
// cannot repair or advance any persisted due pointer through this API.
type PlanningSnapshot struct {
	Scopes []PlanningScopeSnapshot
}

type PlanningScopeSnapshot struct {
	Readiness    SeasonReadinessSnapshot
	GamesFull    *SourceResourceScopeState
	XGFull       *SourceResourceScopeState
	Games        []Game
	XG           []GameXG
	ResultChecks []GameResultCheckState
	XGChecks     []GameXGCheckState
}

// PlanningSnapshot returns every registered source scope from one SQLite
// read-only transaction. Values and nullable time pointers are independent
// copies suitable for pure planning code.
func (c *DB) PlanningSnapshot(ctx context.Context) (PlanningSnapshot, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PlanningSnapshot{}, fmt.Errorf("begin planning snapshot: %w", err)
	}
	defer rollback(tx)
	scopes, err := sourceScopes(ctx, tx)
	if err != nil {
		return PlanningSnapshot{}, err
	}
	out := PlanningSnapshot{Scopes: make([]PlanningScopeSnapshot, 0, len(scopes))}
	for _, scope := range scopes {
		readiness, found, err := seasonReadinessForScope(ctx, tx, scope)
		if err != nil || !found {
			return PlanningSnapshot{}, fmt.Errorf("load planning readiness %s %s: %w", scope.Season, scope.Stage, err)
		}
		games, err := seasonGames(ctx, tx, scope.Season, scope.Stage)
		if err != nil {
			return PlanningSnapshot{}, err
		}
		xg, err := stageXGStates(ctx, tx, scope.Season, scope.Stage)
		if err != nil {
			return PlanningSnapshot{}, err
		}
		results, err := planningResultChecks(ctx, tx, scope.Season, scope.Stage)
		if err != nil {
			return PlanningSnapshot{}, err
		}
		xgChecks, err := planningXGChecks(ctx, tx, scope.Season, scope.Stage)
		if err != nil {
			return PlanningSnapshot{}, err
		}
		gamesFull, err := planningFullState(ctx, tx, SourceResourceGames, scope.Season, scope.Stage)
		if err != nil {
			return PlanningSnapshot{}, err
		}
		xgFull, err := planningFullState(ctx, tx, SourceResourceGameXG, scope.Season, scope.Stage)
		if err != nil {
			return PlanningSnapshot{}, err
		}
		out.Scopes = append(out.Scopes, PlanningScopeSnapshot{Readiness: readiness, GamesFull: gamesFull, XGFull: xgFull, Games: cloneGames(games), XG: cloneGameXG(xg), ResultChecks: cloneResultChecks(results), XGChecks: cloneXGChecks(xgChecks)})
	}
	return out, nil
}

func planningFullState(ctx context.Context, q queryer, resource SourceResource, season, stage string) (*SourceResourceScopeState, error) {
	row := q.QueryRowContext(ctx, `SELECT resource,season,stage,last_full_success_at,next_full_due_at,updated_at FROM source_resource_scope_state WHERE resource=? AND season=? AND stage=?`, resource, season, stage)
	state, err := scanSourceResourceScopeState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func planningResultChecks(ctx context.Context, q queryer, season, stage string) ([]GameResultCheckState, error) {
	rows, err := q.QueryContext(ctx, `SELECT c.asa_game_id,g.season,g.stage,c.last_checked_at,c.first_terminal_observed_at,c.last_material_change_at,c.next_due_at FROM game_result_checks c JOIN games g ON g.asa_game_id=c.asa_game_id WHERE g.season=? AND g.stage=? ORDER BY c.asa_game_id`, season, stage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GameResultCheckState{}
	for rows.Next() {
		value, err := scanGameResultCheckState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func planningXGChecks(ctx context.Context, q queryer, season, stage string) ([]GameXGCheckState, error) {
	rows, err := q.QueryContext(ctx, `SELECT c.asa_game_id,g.season,g.stage,c.last_checked_at,c.first_available_observed_at,c.last_material_change_at,c.next_due_at FROM game_xg_checks c JOIN games g ON g.asa_game_id=c.asa_game_id WHERE g.season=? AND g.stage=? ORDER BY c.asa_game_id`, season, stage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GameXGCheckState{}
	for rows.Next() {
		value, err := scanGameXGCheckState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func cloneGameXG(values []GameXG) []GameXG {
	out := append([]GameXG(nil), values...)
	for i := range out {
		if out[i].FirstObservedAt != nil {
			value := *out[i].FirstObservedAt
			out[i].FirstObservedAt = &value
		}
	}
	return out
}
func cloneResultChecks(values []GameResultCheckState) []GameResultCheckState {
	out := append([]GameResultCheckState(nil), values...)
	for i := range out {
		out[i].FirstTerminalObservedAt = copyTime(out[i].FirstTerminalObservedAt)
		out[i].LastMaterialChangeAt = copyTime(out[i].LastMaterialChangeAt)
		out[i].NextDueAt = copyTime(out[i].NextDueAt)
	}
	return out
}
func cloneXGChecks(values []GameXGCheckState) []GameXGCheckState {
	out := append([]GameXGCheckState(nil), values...)
	for i := range out {
		out[i].FirstAvailableObservedAt = copyTime(out[i].FirstAvailableObservedAt)
		out[i].LastMaterialChangeAt = copyTime(out[i].LastMaterialChangeAt)
		out[i].NextDueAt = copyTime(out[i].NextDueAt)
	}
	return out
}
func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
