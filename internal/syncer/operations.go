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
	GameFreshness        GameFreshnessSummary
	FixtureInputsChanged bool
	XGInputsChanged      bool
}

// GameFreshnessSummary counts normalized fixture changes separately from
// source metadata changes. This lets correction polling be queried by actual
// game-value changes rather than by every newer ASA timestamp.
type GameFreshnessSummary struct {
	Checked          int
	ValueChanged     int
	ValueInitialized int
	MetadataChanged  int
	Unchanged        int
	ResponseRejected int
	StaleRejected    int
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
		filters.Status = allGameStatuses
		if operation.Mode == OperationTargeted {
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
		result.GameFreshness = recordGameFreshnessEvents(trace.SpanFromContext(ctx), operation, mapped, gameResult.PreviousGames)
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
		recordXGFreshnessEvents(trace.SpanFromContext(ctx), operation, xgResult.Freshness)
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
	attributes := []attribute.KeyValue{
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
	if result.Games != nil {
		attributes = append(attributes,
			attribute.Bool("nwsl.sync.source_value_changed", result.GameFreshness.ValueChanged > 0),
			attribute.Int("nwsl.sync.source_value_changed_count", result.GameFreshness.ValueChanged),
			attribute.Int("nwsl.sync.source_value_initialized_count", result.GameFreshness.ValueInitialized),
			attribute.Int("nwsl.sync.source_metadata_changed_count", result.GameFreshness.MetadataChanged),
			attribute.Bool("nwsl.sync.source_response_rejected", result.GameFreshness.ResponseRejected > 0),
			attribute.Int("nwsl.sync.source_response_rejected_count", result.GameFreshness.ResponseRejected),
			attribute.Int("nwsl.sync.source_stale_response_count", result.GameFreshness.StaleRejected),
		)
	}
	if result.XG != nil {
		var changed, initialized, metadata, missing, rejected, staleRejected int
		for _, freshness := range result.XG.Freshness {
			if freshness.ValueChanged {
				changed++
			}
			if freshness.ValueInitialized {
				initialized++
			}
			if freshness.MetadataChanged {
				metadata++
			}
			if freshness.Missing {
				missing++
			}
			if freshness.ResponseRejected {
				rejected++
			}
			if freshness.RejectionKind == "stale" {
				staleRejected++
			}
		}
		attributes = append(attributes,
			attribute.Bool("nwsl.sync.source_value_changed", changed > 0),
			attribute.Int("nwsl.sync.source_value_changed_count", changed),
			attribute.Int("nwsl.sync.source_value_initialized_count", initialized),
			attribute.Int("nwsl.sync.source_metadata_changed_count", metadata),
			attribute.Int("nwsl.sync.source_value_missing_count", missing),
			attribute.Bool("nwsl.sync.source_response_rejected", rejected > 0),
			attribute.Int("nwsl.sync.source_response_rejected_count", rejected),
			attribute.Int("nwsl.sync.source_stale_response_count", staleRejected),
		)
	}
	return attributes
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
func recordGameFreshnessEvents(span trace.Span, operation Operation, incoming, previous []cache.Game) GameFreshnessSummary {
	summary := GameFreshnessSummary{}
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
		return summary
	}
	for _, game := range incoming {
		summary.Checked++
		current, found := cached[game.ASAID]
		decision, reason := gameUpdateDecision(current, game, found)
		unchanged := found && sameGame(current, game)
		responseRejected := found && !unchanged && decision != "updated"
		accepted := !responseRejected
		valueChanged := found && accepted && gameValueChanged(current, game)
		valueInitialized := !found
		metadataChanged := found && accepted && !valueChanged && !unchanged
		rejectionKind, rejectionReason := "", ""
		if responseRejected {
			summary.ResponseRejected++
			rejectionKind = gameRejectionKind(reason)
			rejectionReason = reason
			if rejectionKind == "stale" {
				summary.StaleRejected++
			}
		}
		updateKind := "unchanged"
		switch {
		case valueInitialized:
			updateKind = "value_initialized"
			summary.ValueInitialized++
		case valueChanged:
			updateKind = "value_changed"
			summary.ValueChanged++
		case metadataChanged:
			updateKind = "metadata_changed"
			summary.MetadataChanged++
		default:
			summary.Unchanged++
		}
		currentUpdated := ""
		if found {
			currentUpdated = current.LastUpdatedUTC
		}
		attributes := []attribute.KeyValue{
			attribute.String("nwsl.asa.game.id", game.ASAID),
			attribute.String("nwsl.cache.game.last_updated_utc", currentUpdated),
			attribute.String("nwsl.asa.game.last_updated_utc", game.LastUpdatedUTC),
			attribute.String("nwsl.sync.decision", decision),
			attribute.String("nwsl.sync.reason", reason),
			attribute.String("nwsl.sync.update_kind", updateKind),
			attribute.Bool("nwsl.sync.source_value_changed", valueChanged),
			attribute.Bool("nwsl.sync.source_value_initialized", valueInitialized),
			attribute.Bool("nwsl.sync.source_metadata_changed", metadataChanged),
			attribute.Bool("nwsl.sync.response_accepted", accepted),
			attribute.Bool("nwsl.sync.response_rejected", responseRejected),
			attribute.String("nwsl.sync.rejection_kind", rejectionKind),
			attribute.String("nwsl.sync.rejection_reason", rejectionReason),
		}
		attributes = append(attributes, kickoffAgeAttributes(operation.FinishedAt, game.KickoffUTC)...)
		if found {
			attributes = append(attributes, gameValueAttributes(&current, game)...)
		}
		span.AddEvent("sync.game_freshness", trace.WithAttributes(attributes...))
	}
	return summary
}

func recordXGFreshnessEvents(span trace.Span, operation Operation, freshness []cache.XGFreshness) {
	for _, value := range freshness {
		kind := "unchanged"
		switch {
		case value.ValueInitialized:
			kind = "value_initialized"
		case value.ValueChanged:
			kind = "value_changed"
		case value.MetadataChanged:
			kind = "metadata_changed"
		case value.Missing:
			kind = "value_missing"
		}
		attributes := []attribute.KeyValue{
			attribute.String("nwsl.asa.game.id", value.GameID),
			attribute.String("nwsl.sync.resource", string(operation.Resource)),
			attribute.String("nwsl.sync.update_kind", kind),
			attribute.String("nwsl.sync.kickoff_utc", value.KickoffUTC),
			attribute.Bool("nwsl.sync.source_value_changed", value.ValueChanged),
			attribute.Bool("nwsl.sync.source_value_initialized", value.ValueInitialized),
			attribute.Bool("nwsl.sync.source_metadata_changed", value.MetadataChanged),
			attribute.Bool("nwsl.sync.source_value_missing", value.Missing),
			attribute.Bool("nwsl.sync.response_accepted", value.ResponseAccepted),
			attribute.Bool("nwsl.sync.response_rejected", value.ResponseRejected),
			attribute.String("nwsl.sync.rejection_kind", value.RejectionKind),
			attribute.String("nwsl.sync.rejection_reason", value.RejectionReason),
			attribute.String("nwsl.sync.observation_finished_at", operation.FinishedAt.UTC().Format(time.RFC3339)),
		}
		attributes = append(attributes, kickoffAgeAttributes(operation.FinishedAt, value.KickoffUTC)...)
		attributes = append(attributes, xGValueAttributes("nwsl.sync.old", value.Old)...)
		attributes = append(attributes, xGValueAttributes("nwsl.sync.new", &value.New)...)
		span.AddEvent("sync.xg_freshness", trace.WithAttributes(attributes...))
	}
}

func gameValueAttributes(old *cache.Game, incoming cache.Game) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String("nwsl.sync.old.status", old.Status),
		attribute.String("nwsl.sync.new.status", incoming.Status),
		attribute.String("nwsl.sync.old.kickoff_utc", old.KickoffUTC),
		attribute.String("nwsl.sync.new.kickoff_utc", incoming.KickoffUTC),
		attribute.String("nwsl.sync.old.home_team_id", old.HomeTeamID),
		attribute.String("nwsl.sync.new.home_team_id", incoming.HomeTeamID),
		attribute.String("nwsl.sync.old.away_team_id", old.AwayTeamID),
		attribute.String("nwsl.sync.new.away_team_id", incoming.AwayTeamID),
		attribute.Bool("nwsl.sync.old.home_score_present", old.HomeScore.Valid),
		attribute.Bool("nwsl.sync.new.home_score_present", incoming.HomeScore.Valid),
		attribute.Bool("nwsl.sync.old.away_score_present", old.AwayScore.Valid),
		attribute.Bool("nwsl.sync.new.away_score_present", incoming.AwayScore.Valid),
		attribute.Bool("nwsl.sync.old.matchday_present", old.Matchday.Valid),
		attribute.Bool("nwsl.sync.new.matchday_present", incoming.Matchday.Valid),
		attribute.Bool("nwsl.sync.old.expanded_minutes_present", old.ExpandedMinutes.Valid),
		attribute.Bool("nwsl.sync.new.expanded_minutes_present", incoming.ExpandedMinutes.Valid),
		attribute.Bool("nwsl.sync.old.knockout_game", old.KnockoutGame),
		attribute.Bool("nwsl.sync.new.knockout_game", incoming.KnockoutGame),
	}
	if old.HomeScore.Valid {
		attributes = append(attributes, attribute.Int64("nwsl.sync.old.home_score", old.HomeScore.Int64))
	}
	if incoming.HomeScore.Valid {
		attributes = append(attributes, attribute.Int64("nwsl.sync.new.home_score", incoming.HomeScore.Int64))
	}
	if old.AwayScore.Valid {
		attributes = append(attributes, attribute.Int64("nwsl.sync.old.away_score", old.AwayScore.Int64))
	}
	if incoming.AwayScore.Valid {
		attributes = append(attributes, attribute.Int64("nwsl.sync.new.away_score", incoming.AwayScore.Int64))
	}
	if old.Matchday.Valid {
		attributes = append(attributes, attribute.Int64("nwsl.sync.old.matchday", old.Matchday.Int64))
	}
	if incoming.Matchday.Valid {
		attributes = append(attributes, attribute.Int64("nwsl.sync.new.matchday", incoming.Matchday.Int64))
	}
	if old.ExpandedMinutes.Valid {
		attributes = append(attributes, attribute.Int64("nwsl.sync.old.expanded_minutes", old.ExpandedMinutes.Int64))
	}
	if incoming.ExpandedMinutes.Valid {
		attributes = append(attributes, attribute.Int64("nwsl.sync.new.expanded_minutes", incoming.ExpandedMinutes.Int64))
	}
	return attributes
}

func xGValueAttributes(prefix string, value *cache.GameXG) []attribute.KeyValue {
	if value == nil {
		return []attribute.KeyValue{attribute.String(prefix+".availability", "missing")}
	}
	attributes := []attribute.KeyValue{
		attribute.String(prefix+".availability", string(value.Availability)),
		attribute.String(prefix+".home_team_id", value.HomeTeamID),
		attribute.String(prefix+".away_team_id", value.AwayTeamID),
		attribute.Bool(prefix+".home_xg_present", value.HomeXG.Valid),
		attribute.Bool(prefix+".away_xg_present", value.AwayXG.Valid),
		attribute.Bool(prefix+".home_xpoints_present", value.HomeXPoints.Valid),
		attribute.Bool(prefix+".away_xpoints_present", value.AwayXPoints.Valid),
	}
	if !value.LastCheckedAt.IsZero() {
		attributes = append(attributes, attribute.String(prefix+".last_checked_at", value.LastCheckedAt.UTC().Format(time.RFC3339)))
	}
	if value.HomeXG.Valid {
		attributes = append(attributes, attribute.Float64(prefix+".home_xg", value.HomeXG.Float64))
	}
	if value.AwayXG.Valid {
		attributes = append(attributes, attribute.Float64(prefix+".away_xg", value.AwayXG.Float64))
	}
	if value.HomeXPoints.Valid {
		attributes = append(attributes, attribute.Float64(prefix+".home_xpoints", value.HomeXPoints.Float64))
	}
	if value.AwayXPoints.Valid {
		attributes = append(attributes, attribute.Float64(prefix+".away_xpoints", value.AwayXPoints.Float64))
	}
	return attributes
}

func kickoffAgeAttributes(observedAt time.Time, kickoffUTC string) []attribute.KeyValue {
	kickoff, err := fixtures.ParseKickoff(kickoffUTC)
	if err != nil || observedAt.IsZero() {
		return nil
	}
	return []attribute.KeyValue{
		attribute.String("nwsl.sync.kickoff_utc", kickoff.UTC().Format(time.RFC3339)),
		attribute.Int64("nwsl.sync.kickoff_age_seconds", int64(observedAt.UTC().Sub(kickoff).Seconds())),
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

func gameValueChanged(left, right cache.Game) bool {
	return left.ASAID != right.ASAID || left.Season != right.Season || left.Stage != right.Stage || left.KickoffUTC != right.KickoffUTC || left.Status != right.Status || left.HomeTeamID != right.HomeTeamID || left.AwayTeamID != right.AwayTeamID || left.HomeScore != right.HomeScore || left.AwayScore != right.AwayScore || left.Matchday != right.Matchday || left.ExpandedMinutes != right.ExpandedMinutes || left.KnockoutGame != right.KnockoutGame
}

func gameRejectionKind(reason string) string {
	switch reason {
	case "asa_last_updated_not_newer":
		return "stale"
	case "incoming_reverted_terminal_status":
		return "terminal_regression"
	default:
		return "policy"
	}
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
		return fmt.Errorf("%w; additionally failed to record source failure: %w", cause, err)
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
