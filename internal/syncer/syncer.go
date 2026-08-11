package syncer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	stdsync "sync"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	runMu         stdsync.Mutex
	calculationMu stdsync.Mutex
)

const allGameStatuses = "Abandoned,FullTime,PreMatch"

// ASAClient is the ASA surface required by the sync service.
type ASAClient interface {
	Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error)
	Games(context.Context, asa.GamesFilters) ([]asa.Game, error)
}
type xgASAClient interface {
	GameXGoals(context.Context, asa.XGoalsFilters) ([]asa.GameXGoals, error)
}

// Store is the cache surface required by the sync service.
type Store interface {
	TryAcquireSyncLease(context.Context, string, string, time.Time) (bool, error)
	ReleaseSyncLease(context.Context, string, string) error
}

type calculationStore interface {
	ClinchingInputs(context.Context, string, string) (cache.CalculationInputs, error)
}
type historyPruningStore interface {
	PruneHistory(context.Context, time.Time) (cache.HistoryPruneResult, error)
}

// Service refreshes the persistent cache from ASA.
type Service struct {
	ASA                  ASAClient
	Store                Store
	Qualification        QualificationRefresher
	Scenarios            ScenarioRefresher
	QualificationTimeout time.Duration
	ScenarioTimeout      time.Duration
	HistoryRetention     time.Duration
	// Now is an optional operation clock used to record source observations.
	// Production uses the wall clock; tests and schedulers may supply a stable
	// clock without precomputing a request's completion time.
	Now func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// QualificationRefresher runs after the durable fixture transaction. It is
// intentionally separate from Store so a qualification failure cannot relabel
// a successful fixture refresh.
type QualificationRefresher interface {
	Refresh(context.Context, cache.SyncRun, []cache.Team, []cache.Game, bool) (bool, error)
}
type ScenarioRefresher interface {
	Refresh(context.Context, cache.SyncRun, []cache.Team, []cache.Game, bool) (bool, error)
}

// RunOptions configures one sync run.
type RunOptions struct {
	Season string
	Stage  string
	// ExpectedTeams and GamesPerTeam describe a known, fixed schedule. When
	// both are set, an incomplete or uneven ASA collection is rejected before
	// it can replace a complete local snapshot. Leave both zero for historical
	// seasons whose format is not configured here.
	ExpectedTeams int
	GamesPerTeam  int
	// TargetFixtureID is retained only for the pre-Phase-3 scheduler call
	// shape. The compatibility facade no longer performs target reconciliation;
	// Workstream B replaces this field with batched checked operations.
	TargetFixtureID string
	// Trigger identifies the caller that started this sync (for example,
	// "scheduler", "cli", or "venue_history"). It is trace context, not
	// persisted cache state.
	Trigger string
	Force   bool
	// SourceOnly stores fixtures, xG, and venue summaries without running
	// current-season qualification/scenario calculations.
	SourceOnly bool
}

// RecalculateOptions selects one already-cached season and stage. It never
// performs an ASA request or mutates synchronized source data.
type RecalculateOptions struct {
	Season  string
	Stage   string
	Force   bool
	Trigger string
}

// Run fetches one complete ASA season/stage and atomically stores it.
func (s Service) Run(ctx context.Context, options RunOptions) (run cache.SyncRun, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "sync.run",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nwsl.season", options.Season),
			attribute.String("nwsl.stage", options.Stage),
			attribute.Bool("nwsl.sync.forced", options.Force),
			attribute.String("nwsl.sync.trigger", syncTrigger(options.Trigger)),
			attribute.Int("nwsl.sync.expected_fixture_count", expectedFixtureCount(options)),
		),
	)
	recordRunException := func(cause error, errorType string) error {
		return telemetry.RecordErrorWithType(ctx, span, cause, "sync.run", errorType)
	}
	defer func() {
		span.SetAttributes(syncRunAttributes(run)...)
		if err != nil {
			if errors.Is(err, cache.ErrSyncInProgress) {
				span.SetAttributes(
					attribute.Bool("nwsl.error.expected", true),
					attribute.Bool("nwsl.sync.skipped", true),
					attribute.String("nwsl.sync.outcome", "conflict"),
				)
			} else {
				telemetry.MarkError(span, err)
			}
		}
		span.End()
	}()
	runMu.Lock()
	defer runMu.Unlock()

	startedAt := s.now()
	if s.ASA == nil {
		return cache.SyncRun{}, recordRunException(errors.New("sync ASA client is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if s.Store == nil {
		return cache.SyncRun{}, recordRunException(errors.New("sync store is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if strings.TrimSpace(options.Season) == "" {
		return cache.SyncRun{}, recordRunException(errors.New("sync season is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if strings.TrimSpace(options.Stage) == "" {
		return cache.SyncRun{}, recordRunException(errors.New("sync stage is required"), telemetry.ErrorTypeInvalidArgument)
	}

	holder := fmt.Sprintf("%d-%d", os.Getpid(), startedAt.UnixNano())
	acquired, err := s.Store.TryAcquireSyncLease(ctx, leaseKey(options), holder, leaseExpiry(ctx, startedAt))
	if err != nil {
		return cache.SyncRun{}, recordRunException(err, telemetry.ErrorTypeStorageFailure)
	}
	if !acquired {
		return cache.SyncRun{}, cache.ErrSyncInProgress
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Store.ReleaseSyncLease(releaseCtx, leaseKey(options), holder)
	}()

	if _, ok := s.Store.(operationStore); !ok {
		return cache.SyncRun{}, recordRunException(errors.New("sync store does not support split source operations"), telemetry.ErrorTypeInvalidArgument)
	}
	trigger := cache.SourceRefreshTrigger(syncTrigger(options.Trigger))
	var teamAudit *cache.SourceRefreshAudit
	if options.Force {
		teamOperation := compatibilityOperation(OperationTeams, OperationFull, options, trigger, startedAt)
		teamResult, err := s.Execute(ctx, teamOperation)
		if err != nil {
			return cache.SyncRun{}, recordRunException(err, telemetry.ErrorTypeUpstreamFailure)
		}
		teamAudit = teamResult.TeamAudit
	}
	gameOperation := compatibilityOperation(OperationGames, OperationFull, options, trigger, startedAt)
	gameResult, err := s.Execute(ctx, gameOperation)
	if err != nil {
		return cache.SyncRun{}, recordRunException(err, telemetry.ErrorTypeUpstreamFailure)
	}
	if gameResult.Games != nil && gameResult.Games.SyncRun != nil {
		run = *gameResult.Games.SyncRun
	}
	if teamAudit == nil {
		teamAudit = gameResult.TeamAudit
	}
	if teamAudit != nil {
		run.TeamsUpserted = teamAudit.RowsInserted + teamAudit.RowsUpdated + teamAudit.RowsUnchanged
		run.TeamsInserted = teamAudit.RowsInserted
		run.TeamsUpdated = teamAudit.RowsUpdated
		run.TeamsUnchanged = teamAudit.RowsUnchanged
	}
	// An empty first discovery intentionally has no fixture publication/run;
	// do not create a misleading ready xG observation for that scope.
	if gameResult.Games != nil && gameResult.Games.SyncRun != nil {
		if _, ok := s.ASA.(xgASAClient); ok {
			xgOperation := compatibilityOperation(OperationGameXG, OperationFull, options, trigger, s.now())
			xgResult, xgErr := s.Execute(ctx, xgOperation)
			if xgErr != nil {
				// xG remains a partial failure after the fixture transaction. Execute
				// already wrote its one generalized failure audit.
				run.XGError = xgErr.Error()
			} else if xgResult.XG != nil {
				run.XGRun = xgResult.XG.XGRun
			}
		}
	}
	if options.SourceOnly {
		return run, nil
	}
	if gameResult.FixtureInputsChanged && gameResult.Games != nil {
		run = s.refreshCalculations(context.WithoutCancel(ctx), run, gameResult.Games.Teams, gameResult.Games.Games, options.Force)
	}
	return s.pruneHistory(run), nil
}

// Recalculate reruns derived clinching calculations from the last successful
// fixture snapshot without performing a data or xG sync.
func (s Service) Recalculate(ctx context.Context, options RecalculateOptions) (run cache.SyncRun, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "sync.recalculate",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nwsl.season", options.Season),
			attribute.String("nwsl.stage", options.Stage),
			attribute.Bool("nwsl.sync.forced", options.Force),
			attribute.String("nwsl.sync.trigger", syncTrigger(options.Trigger)),
		),
	)
	recordRecalculationException := func(cause error, errorType string) error {
		return telemetry.RecordErrorWithType(ctx, span, cause, "sync.recalculate", errorType)
	}
	defer func() {
		span.SetAttributes(recalculateRunAttributes(run, err)...)
		if err != nil {
			telemetry.MarkError(span, err)
		}
		span.End()
	}()
	if s.Store == nil {
		return cache.SyncRun{}, recordRecalculationException(errors.New("sync store is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if strings.TrimSpace(options.Season) == "" {
		return cache.SyncRun{}, recordRecalculationException(errors.New("recalculation season is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if strings.TrimSpace(options.Stage) == "" {
		return cache.SyncRun{}, recordRecalculationException(errors.New("recalculation stage is required"), telemetry.ErrorTypeInvalidArgument)
	}
	store, ok := s.Store.(calculationStore)
	if !ok {
		return cache.SyncRun{}, recordRecalculationException(errors.New("sync store does not support cached clinching inputs"), telemetry.ErrorTypeInvalidArgument)
	}
	inputs, err := store.ClinchingInputs(ctx, options.Season, options.Stage)
	if err != nil {
		return cache.SyncRun{}, recordRecalculationException(fmt.Errorf("load cached clinching inputs: %w", err), telemetry.ErrorTypeStorageFailure)
	}
	// Keep the derived calculation budgets independent from the short caller
	// deadline used to load the cached inputs. This mirrors Run: a scheduler
	// check may have a small source-sync timeout, while the qualification and
	// scenario passes each have their own bounded budgets.
	run = s.refreshCalculations(context.WithoutCancel(ctx), inputs.SyncRun, inputs.Teams, inputs.Games, options.Force)
	return s.pruneHistory(run), nil
}

func recalculateRunAttributes(run cache.SyncRun, err error) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String("nwsl.sync.recalculate.outcome", recalculateOutcome(run, err)),
		attribute.Bool("nwsl.sync.partial_failure", run.QualificationError != "" || run.ScenarioError != ""),
		attribute.Bool("nwsl.sync.qualification_recalculated", run.QualificationRecalculated),
		attribute.Bool("nwsl.sync.scenario_recalculated", run.ScenarioRecalculated),
		attribute.String("nwsl.sync.qualification.outcome", recalculateComponentOutcome(run.QualificationRecalculated, run.QualificationError, err)),
		attribute.String("nwsl.sync.scenario.outcome", recalculateComponentOutcome(run.ScenarioRecalculated, run.ScenarioError, err)),
	}
	if run.ID > 0 {
		attributes = append(attributes, attribute.Int64("nwsl.sync.source_run_id", run.ID))
	}
	if run.FixtureSnapshotID != "" {
		attributes = append(attributes, attribute.String("nwsl.cache.fixture_snapshot_id", run.FixtureSnapshotID))
	}
	return attributes
}

func recalculateOutcome(run cache.SyncRun, err error) string {
	if err != nil {
		return "failure"
	}
	if run.QualificationError != "" || run.ScenarioError != "" {
		return "partial_failure"
	}
	if run.QualificationRecalculated || run.ScenarioRecalculated {
		return "complete"
	}
	return "current"
}

func recalculateComponentOutcome(recalculated bool, failure string, runErr error) string {
	if runErr != nil {
		return "not_run"
	}
	if failure != "" {
		return "failure"
	}
	if recalculated {
		return "complete"
	}
	return "current"
}

func (s Service) pruneHistory(run cache.SyncRun) cache.SyncRun {
	if s.HistoryRetention <= 0 {
		return run
	}
	store, ok := s.Store.(historyPruningStore)
	if !ok {
		return run
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := store.PruneHistory(ctx, time.Now().UTC().Add(-s.HistoryRetention))
	if err != nil {
		run.HistoryPruneError = err.Error()
		return run
	}
	run.HistoryPrune = &result
	return run
}

func (s Service) refreshCalculations(parent context.Context, run cache.SyncRun, teams []cache.Team, games []cache.Game, force bool) cache.SyncRun {
	calculationMu.Lock()
	defer calculationMu.Unlock()

	if s.Qualification != nil {
		budget := s.QualificationTimeout
		if budget <= 0 {
			budget = 5 * time.Second
		}
		derivedCtx, cancel := context.WithTimeout(parent, budget)
		recalculated, err := s.Qualification.Refresh(derivedCtx, run, teams, games, force)
		cancel()
		run.QualificationRecalculated = recalculated
		if err != nil {
			run.QualificationError = err.Error()
		}
	}
	if run.QualificationError == "" && s.Scenarios != nil {
		budget := s.ScenarioTimeout
		if budget <= 0 {
			budget = 30 * time.Second
		}
		derivedCtx, cancel := context.WithTimeout(parent, budget)
		recalculated, err := s.Scenarios.Refresh(derivedCtx, run, teams, games, force)
		cancel()
		run.ScenarioRecalculated = recalculated
		if err != nil {
			run.ScenarioError = err.Error()
		}
	}
	return run
}

func syncTrigger(value string) string {
	if value == "" {
		return "unspecified"
	}
	return value
}

func syncRunAttributes(run cache.SyncRun) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String("nwsl.sync.outcome", run.Outcome),
		attribute.Int("nwsl.sync.teams_seen", run.TeamsUpserted),
		attribute.Int("nwsl.sync.games_seen", run.GamesSeen),
		attribute.Int("nwsl.sync.teams_inserted", run.TeamsInserted),
		attribute.Int("nwsl.sync.teams_updated", run.TeamsUpdated),
		attribute.Int("nwsl.sync.teams_unchanged", run.TeamsUnchanged),
		attribute.Int("nwsl.sync.games_inserted", run.GamesInserted),
		attribute.Int("nwsl.sync.games_updated", run.GamesUpdated),
		attribute.Int("nwsl.sync.games_unchanged", run.GamesUnchanged),
		attribute.Int("nwsl.sync.games_deleted", run.GamesDeleted),
		attribute.String("nwsl.cache.fixture_snapshot_id", run.FixtureSnapshotID),
		attribute.Bool("nwsl.sync.partial_failure", run.XGError != "" || run.QualificationError != "" || run.ScenarioError != ""),
		attribute.String("nwsl.sync.xg.outcome", syncComponentOutcome(run.XGRun != nil, run.XGError)),
		attribute.String("nwsl.sync.qualification.outcome", syncComponentOutcome(run.QualificationRecalculated, run.QualificationError)),
		attribute.String("nwsl.sync.scenario.outcome", syncComponentOutcome(run.ScenarioRecalculated, run.ScenarioError)),
	}
	if run.XGRun != nil {
		attributes = append(attributes,
			attribute.Int64("nwsl.sync.xg.available_games", run.XGRun.AvailableGames),
			attribute.Int64("nwsl.sync.xg.unavailable_games", run.XGRun.UnavailableGames),
		)
	}
	return attributes
}

func syncComponentOutcome(completed bool, failure string) string {
	if failure != "" {
		return "failure"
	}
	if completed {
		return "complete"
	}
	return "not_run"
}

func leaseKey(options RunOptions) string {
	return options.Season + "\x00" + options.Stage
}

func leaseExpiry(ctx context.Context, startedAt time.Time) time.Time {
	if deadline, ok := ctx.Deadline(); ok && deadline.After(startedAt) {
		return deadline
	}
	return startedAt.Add(time.Minute)
}

func expectedFixtureCount(options RunOptions) int {
	if options.ExpectedTeams < 1 || options.GamesPerTeam < 1 || options.ExpectedTeams*options.GamesPerTeam%2 != 0 {
		return 0
	}
	return options.ExpectedTeams * options.GamesPerTeam / 2
}

func mapTeams(teams []asa.Team) ([]cache.Team, error) {
	cacheTeams := make([]cache.Team, 0, len(teams))
	for _, team := range teams {
		raw := team.RawJSON
		if raw == "" {
			marshaled, err := json.Marshal(team)
			if err != nil {
				return nil, fmt.Errorf("marshal raw team %q: %w", team.TeamID, err)
			}
			raw = string(marshaled)
		}
		cacheTeams = append(cacheTeams, cache.Team{
			ASAID:        team.TeamID,
			Name:         team.TeamName,
			ShortName:    team.TeamShortName,
			Abbreviation: team.TeamAbbreviation,
			RawJSON:      raw,
		})
	}
	return cacheTeams, nil
}

func mapGames(options RunOptions, games []asa.Game) ([]cache.Game, error) {
	cacheGames := make([]cache.Game, 0, len(games))
	for _, game := range games {
		raw := game.RawJSON
		if raw == "" {
			marshaled, err := json.Marshal(game)
			if err != nil {
				return nil, fmt.Errorf("marshal raw game %q: %w", game.GameID, err)
			}
			raw = string(marshaled)
		}
		cacheGames = append(cacheGames, cache.Game{
			ASAID:          game.GameID,
			Season:         options.Season,
			Stage:          options.Stage,
			KickoffUTC:     game.DateTimeUTC,
			Status:         game.Status,
			HomeTeamID:     game.HomeTeamID,
			AwayTeamID:     game.AwayTeamID,
			HomeScore:      nullInt(game.HomeScore),
			AwayScore:      nullInt(game.AwayScore),
			Matchday:       nullInt(game.Matchday),
			LastUpdatedUTC: game.LastUpdatedUTC,
			RawJSON:        raw,
		})
	}
	return cacheGames, nil
}

func mapXGoals(values []asa.GameXGoals) ([]cache.GameXG, error) {
	result := make([]cache.GameXG, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.GameID) == "" || strings.TrimSpace(value.HomeTeamID) == "" || strings.TrimSpace(value.AwayTeamID) == "" {
			return nil, fmt.Errorf("validate ASA xG response: required identity is missing")
		}
		if math.IsNaN(value.HomeTeamXGoals) || math.IsInf(value.HomeTeamXGoals, 0) || value.HomeTeamXGoals < 0 || math.IsNaN(value.AwayTeamXGoals) || math.IsInf(value.AwayTeamXGoals, 0) || value.AwayTeamXGoals < 0 {
			return nil, fmt.Errorf("validate ASA xG response: invalid xG for game %q", value.GameID)
		}
		if (value.HomeXPoints == nil) != (value.AwayXPoints == nil) || (value.HomeXPoints != nil && (!validGameExpectedPoints(*value.HomeXPoints) || !validGameExpectedPoints(*value.AwayXPoints))) {
			return nil, fmt.Errorf("validate ASA xG response: invalid expected points for game %q", value.GameID)
		}
		raw := value.RawJSON
		if raw == "" {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			raw = string(encoded)
		}
		mapped := cache.GameXG{GameID: value.GameID, Availability: cache.XGAvailable, HomeTeamID: value.HomeTeamID, AwayTeamID: value.AwayTeamID, HomeXG: sql.NullFloat64{Float64: value.HomeTeamXGoals, Valid: true}, AwayXG: sql.NullFloat64{Float64: value.AwayTeamXGoals, Valid: true}, RawJSON: raw}
		if value.HomeXPoints != nil {
			mapped.HomeXPoints = sql.NullFloat64{Float64: *value.HomeXPoints, Valid: true}
			mapped.AwayXPoints = sql.NullFloat64{Float64: *value.AwayXPoints, Valid: true}
		}
		result = append(result, mapped)
	}
	return result, nil
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func validGameExpectedPoints(value float64) bool {
	return finiteNonnegative(value) && value <= cache.MaxGameExpectedPoints
}

func nullInt(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}
