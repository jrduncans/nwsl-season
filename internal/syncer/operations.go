package syncer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

// OperationResource identifies the one ASA collection an operation owns.
type OperationResource string

const (
	OperationTeams  OperationResource = "teams"
	OperationGames  OperationResource = "games"
	OperationGameXG OperationResource = "game_xg"
)

// OperationMode selects either an authoritative collection or a checked batch.
type OperationMode string

const (
	OperationFull     OperationMode = "full"
	OperationTargeted OperationMode = "targeted"
)

// OperationGameRequest carries the independent cadence pointer for one
// targeted identity. The adapter sorts and copies these before issuing ASA.
type OperationGameRequest struct {
	GameID    string
	NextDueAt *time.Time
}

// Operation describes one sequential source request and its corresponding
// cache mutation. Targeted requests are copied, sorted, and de-duplicated
// before use; their cadence pointers are never shared with the caller.
type Operation struct {
	Resource  OperationResource
	Mode      OperationMode
	Season    string
	Stage     string
	Requested []OperationGameRequest
	Trigger   cache.SourceRefreshTrigger
	Force     bool

	StartedAt     time.Time
	FinishedAt    time.Time // executor-observed completion time
	NextFullDueAt *time.Time
	Expectation   *competition.InventoryExpectation
}

// OperationResult exposes only results committed by the owning cache API.
// FixtureInputsChanged and XGInputsChanged are suitable for the selective
// downstream work added by the scheduler workstream.
type OperationResult struct {
	Operation            Operation
	TeamAudit            *cache.SourceRefreshAudit
	Games                *cache.GameRefreshResult
	XG                   *cache.XGRefreshResult
	FixtureInputsChanged bool
	XGInputsChanged      bool
}

type operationStore interface {
	UpsertTeams(context.Context, []cache.Team, cache.FullRefreshMetadata) (cache.SourceRefreshAudit, error)
	ReplaceGameInventory(context.Context, string, string, []cache.Game, *competition.InventoryExpectation, cache.FullRefreshMetadata) (cache.GameRefreshResult, error)
	UpsertCheckedGames(context.Context, string, string, []cache.CheckedGameRequest, []cache.Game, cache.TargetedRefreshMetadata) (cache.GameRefreshResult, error)
	ReplaceStageXG(context.Context, string, string, []cache.GameXG, cache.FullRefreshMetadata) (cache.XGRefreshResult, error)
	UpsertCheckedXG(context.Context, string, string, []cache.CheckedXGRequest, []cache.GameXG, cache.TargetedRefreshMetadata) (cache.XGRefreshResult, error)
	RecordSourceRefresh(context.Context, cache.SourceRefreshAudit, *time.Time) (cache.SourceRefreshAudit, error)
}

// Execute performs exactly one ASA resource request. It deliberately does not
// acquire the compatibility Run lease; scheduler job leasing belongs to the
// next workstream.
func (s Service) Execute(ctx context.Context, operation Operation) (OperationResult, error) {
	if operation.StartedAt.IsZero() {
		operation.StartedAt = s.now()
	}
	operation, err := normalizeOperation(operation)
	if err != nil {
		return OperationResult{}, err
	}
	if s.ASA == nil {
		return OperationResult{}, errors.New("sync ASA client is required")
	}
	store, ok := s.Store.(operationStore)
	if !ok {
		return OperationResult{}, errors.New("sync store does not support split source operations")
	}
	return s.execute(ctx, store, operation, true)
}

func (s Service) execute(ctx context.Context, store operationStore, operation Operation, allowTeamRecovery bool) (result OperationResult, err error) {
	result = OperationResult{Operation: operation}
	defer func() {
		if operation.FinishedAt.IsZero() {
			operation.FinishedAt = s.now()
		}
		result.Operation = operation
		if operation.FinishedAt.Before(operation.StartedAt) {
			clockErr := errors.New("source operation clock moved before start")
			if err == nil {
				err = clockErr
			} else {
				err = errors.Join(err, clockErr)
			}
		}
		if err != nil {
			err = s.operationFailure(ctx, store, operation, err)
		}
	}()
	switch operation.Resource {
	case OperationTeams:
		teams, err := s.ASA.Teams(ctx, asa.TeamsFilters{})
		if err != nil {
			return result, fmt.Errorf("fetch teams: %w", err)
		}
		mapped, err := mapTeams(teams)
		if err != nil {
			return result, err
		}
		operation.FinishedAt = s.now()
		audit, err := store.UpsertTeams(ctx, mapped, fullMetadata(operation))
		if err != nil {
			return result, err
		}
		result.TeamAudit = &audit
		return result, nil

	case OperationGames:
		filters := asa.GamesFilters{SeasonName: operation.Season, StageName: operation.Stage}
		if operation.Mode == OperationFull {
			filters.Status = allGameStatuses
		} else {
			filters.GameID = strings.Join(operationGameIDs(operation), ",")
		}
		games, err := s.ASA.Games(ctx, filters)
		if err != nil {
			return result, fmt.Errorf("fetch games: %w", err)
		}
		mapped, err := mapGames(RunOptions{Season: operation.Season, Stage: operation.Stage}, games)
		if err != nil {
			return result, err
		}
		operation.FinishedAt = s.now()
		gameResult, err := s.writeGames(ctx, store, operation, mapped)
		if errors.Is(err, cache.ErrUnknownGameTeams) && allowTeamRecovery {
			// The validated response remains in memory. Refresh the independent
			// catalog once, then retry that exact cache write without refetching.
			recovery := operation
			recovery.Resource, recovery.Mode, recovery.Requested = OperationTeams, OperationFull, nil
			recovery.Season, recovery.Stage = "", ""
			recovery.Expectation = nil
			recovery.StartedAt, recovery.FinishedAt = s.now(), time.Time{}
			recoveryResult, teamErr := s.execute(ctx, store, recovery, false)
			if teamErr != nil {
				return result, teamErr
			}
			result.TeamAudit = recoveryResult.TeamAudit
			gameResult, err = s.writeGames(ctx, store, operation, mapped)
		}
		if err != nil {
			return result, err
		}
		result.Games = &gameResult
		result.FixtureInputsChanged = gameResult.Audit.DownstreamInputsChanged
		return result, nil

	case OperationGameXG:
		client, ok := s.ASA.(xgASAClient)
		if !ok {
			return result, errors.New("sync ASA client does not support game xG")
		}
		filters := asa.XGoalsFilters{SeasonName: operation.Season, StageName: operation.Stage}
		if operation.Mode == OperationTargeted {
			filters.GameID = strings.Join(operationGameIDs(operation), ",")
		}
		values, err := client.GameXGoals(ctx, filters)
		if err != nil {
			return result, fmt.Errorf("fetch game xG: %w", err)
		}
		mapped, err := mapXGoals(values)
		if err != nil {
			return result, err
		}
		operation.FinishedAt = s.now()
		var xgResult cache.XGRefreshResult
		if operation.Mode == OperationFull {
			xgResult, err = store.ReplaceStageXG(ctx, operation.Season, operation.Stage, mapped, fullMetadata(operation))
		} else {
			xgResult, err = store.UpsertCheckedXG(ctx, operation.Season, operation.Stage, checkedXGRequests(operation), mapped, targetedMetadata(operation))
		}
		if err != nil {
			return result, err
		}
		result.XG = &xgResult
		result.XGInputsChanged = xgResult.Audit.DownstreamInputsChanged
		return result, nil
	}
	return result, errors.New("unsupported source operation")
}

func (s Service) writeGames(ctx context.Context, store operationStore, operation Operation, games []cache.Game) (cache.GameRefreshResult, error) {
	if operation.Mode == OperationFull {
		return store.ReplaceGameInventory(ctx, operation.Season, operation.Stage, games, operation.Expectation, fullMetadata(operation))
	}
	return store.UpsertCheckedGames(ctx, operation.Season, operation.Stage, checkedGameRequests(operation), games, targetedMetadata(operation))
}

func (s Service) operationFailure(ctx context.Context, store operationStore, operation Operation, cause error) error {
	recordCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		recordCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	_, err := store.RecordSourceRefresh(recordCtx, cache.SourceRefreshAudit{
		Resource:     cache.SourceResource(operation.Resource),
		Season:       operation.Season,
		Stage:        operation.Stage,
		Mode:         cache.SourceRefreshMode(operation.Mode),
		Trigger:      operation.Trigger,
		StartedAt:    operation.StartedAt,
		FinishedAt:   operation.FinishedAt,
		Outcome:      cache.SourceRefreshFailure,
		ErrorSummary: cause.Error(),
	}, nil)
	if err != nil {
		return fmt.Errorf("%w; additionally failed to record source failure: %v", cause, err)
	}
	return cause
}

func normalizeOperation(operation Operation) (Operation, error) {
	if operation.Resource != OperationTeams && operation.Resource != OperationGames && operation.Resource != OperationGameXG {
		return Operation{}, fmt.Errorf("invalid source operation resource %q", operation.Resource)
	}
	if operation.Mode != OperationFull && operation.Mode != OperationTargeted {
		return Operation{}, fmt.Errorf("invalid source operation mode %q", operation.Mode)
	}
	if strings.TrimSpace(string(operation.Trigger)) == "" {
		return Operation{}, errors.New("source operation trigger is blank")
	}
	if operation.StartedAt.IsZero() {
		return Operation{}, errors.New("source operation start time is required")
	}
	operation.StartedAt = operation.StartedAt.UTC()
	if operation.Resource == OperationTeams {
		if operation.Season != "" || operation.Stage != "" || operation.Mode != OperationFull || len(operation.Requested) != 0 {
			return Operation{}, errors.New("team operation must be a full catalog operation without scope or game IDs")
		}
	} else if strings.TrimSpace(operation.Season) == "" || operation.Season != strings.TrimSpace(operation.Season) || strings.TrimSpace(operation.Stage) == "" || operation.Stage != strings.TrimSpace(operation.Stage) {
		return Operation{}, errors.New("scoped source operation has blank or untrimmed scope")
	}
	if operation.Mode == OperationTargeted {
		if len(operation.Requested) == 0 {
			return Operation{}, errors.New("targeted source operation has no game IDs")
		}
		requests := append([]OperationGameRequest(nil), operation.Requested...)
		sort.Slice(requests, func(i, j int) bool { return requests[i].GameID < requests[j].GameID })
		for i := range requests {
			id := requests[i].GameID
			if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) || (i > 0 && id == requests[i-1].GameID) {
				return Operation{}, errors.New("targeted source operation has invalid game IDs")
			}
			if requests[i].NextDueAt != nil {
				if !operation.FinishedAt.IsZero() && requests[i].NextDueAt.Before(operation.FinishedAt) {
					return Operation{}, errors.New("targeted source operation due is before finish")
				}
				due := requests[i].NextDueAt.UTC()
				requests[i].NextDueAt = &due
			}
		}
		operation.Requested = requests
	} else if len(operation.Requested) != 0 {
		return Operation{}, errors.New("full source operation has targeted game IDs or due time")
	}
	if operation.NextFullDueAt != nil {
		if operation.Mode != OperationFull || (!operation.FinishedAt.IsZero() && operation.NextFullDueAt.Before(operation.FinishedAt)) {
			return Operation{}, errors.New("invalid full source operation due time")
		}
		due := operation.NextFullDueAt.UTC()
		operation.NextFullDueAt = &due
	}
	if operation.Expectation != nil {
		value := *operation.Expectation
		operation.Expectation = &value
	}
	return operation, nil
}

func fullMetadata(operation Operation) cache.FullRefreshMetadata {
	return cache.FullRefreshMetadata{Trigger: operation.Trigger, StartedAt: operation.StartedAt, FinishedAt: operation.FinishedAt, NextFullDueAt: operation.NextFullDueAt}
}

func targetedMetadata(operation Operation) cache.TargetedRefreshMetadata {
	return cache.TargetedRefreshMetadata{Trigger: operation.Trigger, StartedAt: operation.StartedAt, FinishedAt: operation.FinishedAt}
}

func checkedGameRequests(operation Operation) []cache.CheckedGameRequest {
	requests := make([]cache.CheckedGameRequest, len(operation.Requested))
	for i, request := range operation.Requested {
		requests[i] = cache.CheckedGameRequest{ASAID: request.GameID, NextDueAt: request.NextDueAt}
	}
	return requests
}

func checkedXGRequests(operation Operation) []cache.CheckedXGRequest {
	requests := make([]cache.CheckedXGRequest, len(operation.Requested))
	for i, request := range operation.Requested {
		requests[i] = cache.CheckedXGRequest{GameID: request.GameID, NextDueAt: request.NextDueAt}
	}
	return requests
}

func operationGameIDs(operation Operation) []string {
	ids := make([]string, len(operation.Requested))
	for i, request := range operation.Requested {
		ids[i] = request.GameID
	}
	return ids
}

func compatibilityOperation(resource OperationResource, mode OperationMode, options RunOptions, trigger cache.SourceRefreshTrigger, started time.Time) Operation {
	operation := Operation{Resource: resource, Mode: mode, Trigger: trigger, StartedAt: started}
	if resource == OperationTeams {
		return operation
	}
	operation.Season, operation.Stage = options.Season, options.Stage
	if resource == OperationGames && mode == OperationFull && (options.ExpectedTeams != 0 || options.GamesPerTeam != 0) {
		operation.Expectation = &competition.InventoryExpectation{Teams: options.ExpectedTeams, GamesPerTeam: options.GamesPerTeam, Games: expectedFixtureCount(options)}
	}
	return operation
}
