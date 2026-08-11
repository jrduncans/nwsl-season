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
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	GameID               string
	NextDueAt            *time.Time
	NextDueAfter         time.Duration
	MaterialNextDueAfter time.Duration
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

	StartedAt        time.Time
	FinishedAt       time.Time // executor-observed completion time
	NextFullDueAt    *time.Time
	NextFullDueAfter time.Duration
	Expectation      *competition.InventoryExpectation
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
	ctx, span := telemetry.Tracer().Start(ctx, "sync.source_operation",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(operationAttributes(operation)...),
	)
	result, err := s.execute(ctx, store, operation, true)
	span.SetAttributes(operationResultAttributes(result, err)...)
	if err != nil {
		// The caller owns exception emission so Run can report one failure at
		// its compatibility boundary without duplicate Honeycomb exception logs.
		telemetry.MarkError(span, err)
	}
	span.End()
	return result, err
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
		recordASAResponse(trace.SpanFromContext(ctx), operation, len(teams))
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
		recordASAResponse(trace.SpanFromContext(ctx), operation, len(games))
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
		recordGameFreshnessEvents(trace.SpanFromContext(ctx), operation, mapped, gameResult.PreviousGames)
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
		recordASAResponse(trace.SpanFromContext(ctx), operation, len(values))
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

func operationAttributes(operation Operation) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String("nwsl.sync.resource", string(operation.Resource)),
		attribute.String("nwsl.sync.mode", string(operation.Mode)),
		attribute.String("nwsl.sync.trigger", string(operation.Trigger)),
		attribute.Int("nwsl.sync.requested_rows", len(operation.Requested)),
	}
	if operation.Season != "" {
		attributes = append(attributes, attribute.String("nwsl.season", operation.Season))
	}
	if operation.Stage != "" {
		attributes = append(attributes, attribute.String("nwsl.stage", operation.Stage))
	}
	return attributes
}

func recordASAResponse(span trace.Span, operation Operation, rows int) {
	span.AddEvent("sync.asa_response", trace.WithAttributes(
		attribute.String("nwsl.sync.resource", string(operation.Resource)),
		attribute.String("nwsl.sync.mode", string(operation.Mode)),
		attribute.Int("nwsl.asa.returned_rows", rows),
	))
}

func operationResultAttributes(result OperationResult, err error) []attribute.KeyValue {
	audit := resultAudit(result)
	if audit == nil {
		if err != nil {
			return []attribute.KeyValue{attribute.String("nwsl.sync.operation.outcome", "failure")}
		}
		return []attribute.KeyValue{attribute.String("nwsl.sync.operation.outcome", "complete")}
	}
	decision, reason := "updated", "source_data_changed"
	if audit.RowsInserted == 0 && audit.RowsUpdated == 0 && audit.RowsDeleted == 0 {
		decision, reason = "not_updated", "source_data_unchanged"
	}
	return []attribute.KeyValue{
		attribute.String("nwsl.sync.operation.outcome", "complete"),
		attribute.Int("nwsl.sync.returned_rows", audit.ReturnedRows),
		attribute.Int("nwsl.sync.rows_inserted", audit.RowsInserted),
		attribute.Int("nwsl.sync.rows_updated", audit.RowsUpdated),
		attribute.Int("nwsl.sync.rows_unchanged", audit.RowsUnchanged),
		attribute.Int("nwsl.sync.rows_deleted", audit.RowsDeleted),
		attribute.Bool("nwsl.sync.downstream_inputs_changed", audit.DownstreamInputsChanged),
		attribute.String("nwsl.sync.update.decision", decision),
		attribute.String("nwsl.sync.update.reason", reason),
	}
}

func resultAudit(result OperationResult) *cache.SourceRefreshAudit {
	if result.Games != nil {
		return &result.Games.Audit
	}
	if result.XG != nil {
		return &result.XG.Audit
	}
	return result.TeamAudit
}

// recordGameFreshnessEvents puts both versions on the span created for the
// exact ASA games call. Game IDs are intentionally event attributes: they are
// useful for following a surprising source correction, but are not span-level
// grouping dimensions.
func recordGameFreshnessEvents(span trace.Span, operation Operation, incoming, previous []cache.Game) {
	cached := make(map[string]cache.Game, len(previous))
	for _, game := range previous {
		cached[game.ASAID] = game
	}
	if len(incoming) == 0 {
		span.AddEvent("sync.game_freshness", trace.WithAttributes(
			attribute.String("nwsl.sync.decision", "not_updated"),
			attribute.String("nwsl.sync.reason", "asa_returned_no_games"),
			attribute.String("nwsl.sync.resource", string(operation.Resource)),
		))
		return
	}
	for _, game := range incoming {
		current, found := cached[game.ASAID]
		decision, reason := gameUpdateDecision(current, game, found)
		currentUpdated := ""
		if found {
			currentUpdated = current.LastUpdatedUTC
		}
		span.AddEvent("sync.game_freshness", trace.WithAttributes(
			attribute.String("nwsl.asa.game.id", game.ASAID),
			attribute.String("nwsl.cache.game.last_updated_utc", currentUpdated),
			attribute.String("nwsl.asa.game.last_updated_utc", game.LastUpdatedUTC),
			attribute.String("nwsl.sync.decision", decision),
			attribute.String("nwsl.sync.reason", reason),
		))
	}
}

func gameUpdateDecision(cached, incoming cache.Game, found bool) (decision, reason string) {
	if !found {
		return "updated", "new_game"
	}
	if gameIsTerminal(incoming) && !gameIsTerminal(cached) {
		return "updated", "incoming_terminal_result"
	}
	if gameIsTerminal(cached) && !gameIsTerminal(incoming) {
		return "not_updated", "incoming_reverted_terminal_status"
	}
	if cached.Status == fixtures.PreMatchStatus && incoming.Status == fixtures.PreMatchStatus && !sameGame(cached, incoming) {
		return "updated", "prematch_fixture_change"
	}
	if sameGame(cached, incoming) {
		return "not_updated", "source_data_unchanged"
	}
	incomingUpdated, incomingErr := fixtures.ParseKickoff(incoming.LastUpdatedUTC)
	cachedUpdated, cachedErr := fixtures.ParseKickoff(cached.LastUpdatedUTC)
	if incomingErr == nil && (cachedErr != nil || incomingUpdated.After(cachedUpdated)) {
		return "updated", "asa_last_updated_newer"
	}
	return "not_updated", "asa_last_updated_not_newer"
}

func gameIsTerminal(game cache.Game) bool {
	return game.Status == fixtures.AbandonedStatus || (game.Status == fixtures.CompletedStatus && game.HomeScore.Valid && game.AwayScore.Valid)
}

func sameGame(left, right cache.Game) bool {
	return left.ASAID == right.ASAID && left.Season == right.Season && left.Stage == right.Stage && left.KickoffUTC == right.KickoffUTC && left.Status == right.Status && left.HomeTeamID == right.HomeTeamID && left.AwayTeamID == right.AwayTeamID && left.HomeScore == right.HomeScore && left.AwayScore == right.AwayScore && left.Matchday == right.Matchday && left.ExpandedMinutes == right.ExpandedMinutes && left.KnockoutGame == right.KnockoutGame && left.LastUpdatedUTC == right.LastUpdatedUTC && left.RawJSON == right.RawJSON
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
			if requests[i].NextDueAfter < 0 || requests[i].MaterialNextDueAfter < 0 || (requests[i].NextDueAt != nil && (requests[i].NextDueAfter != 0 || requests[i].MaterialNextDueAfter != 0)) {
				return Operation{}, errors.New("targeted source operation has invalid due policy")
			}
		}
		operation.Requested = requests
	} else if len(operation.Requested) != 0 {
		return Operation{}, errors.New("full source operation has targeted game IDs or due time")
	}
	if operation.NextFullDueAfter < 0 || (operation.NextFullDueAt != nil && operation.NextFullDueAfter != 0) || (operation.Mode != OperationFull && operation.NextFullDueAfter != 0) {
		return Operation{}, errors.New("invalid full source operation due policy")
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
	due := operation.NextFullDueAt
	if operation.NextFullDueAfter != 0 {
		value := operation.FinishedAt.Add(operation.NextFullDueAfter)
		due = &value
	}
	return cache.FullRefreshMetadata{Trigger: operation.Trigger, StartedAt: operation.StartedAt, FinishedAt: operation.FinishedAt, NextFullDueAt: due}
}

func targetedMetadata(operation Operation) cache.TargetedRefreshMetadata {
	return cache.TargetedRefreshMetadata{Trigger: operation.Trigger, StartedAt: operation.StartedAt, FinishedAt: operation.FinishedAt}
}

func checkedGameRequests(operation Operation) []cache.CheckedGameRequest {
	requests := make([]cache.CheckedGameRequest, len(operation.Requested))
	for i, request := range operation.Requested {
		next := request.NextDueAt
		if request.NextDueAfter != 0 {
			value := operation.FinishedAt.Add(request.NextDueAfter)
			next = &value
		}
		var material *time.Time
		if request.MaterialNextDueAfter != 0 {
			value := operation.FinishedAt.Add(request.MaterialNextDueAfter)
			material = &value
		}
		requests[i] = cache.CheckedGameRequest{ASAID: request.GameID, NextDueAt: next, MaterialNextDueAt: material}
	}
	return requests
}

func checkedXGRequests(operation Operation) []cache.CheckedXGRequest {
	requests := make([]cache.CheckedXGRequest, len(operation.Requested))
	for i, request := range operation.Requested {
		next := request.NextDueAt
		if request.NextDueAfter != 0 {
			value := operation.FinishedAt.Add(request.NextDueAfter)
			next = &value
		}
		var material *time.Time
		if request.MaterialNextDueAfter != 0 {
			value := operation.FinishedAt.Add(request.MaterialNextDueAfter)
			material = &value
		}
		requests[i] = cache.CheckedXGRequest{GameID: request.GameID, NextDueAt: next, MaterialNextDueAt: material}
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
