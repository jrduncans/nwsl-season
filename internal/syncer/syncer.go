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
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	runMu         stdsync.Mutex
	calculationMu stdsync.Mutex
)

const allGameStatuses = fixtures.AbandonedStatus + "," + fixtures.CompletedStatus + "," + fixtures.PreMatchStatus

// ASAClient is the ASA surface required by the sync service.
type ASAClient interface {
	Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error)
	Games(context.Context, asa.GamesFilters) ([]asa.Game, error)
}
type xgASAClient interface {
	GameXGoals(context.Context, asa.XGoalsFilters) ([]asa.GameXGoals, error)
}
type xgStore interface {
	ReplaceGameXG(context.Context, string, string, []cache.Game, []cache.GameXG, time.Time) (cache.XGSyncRun, error)
	RecordXGFailure(context.Context, string, string, time.Time, error) error
}

// Store is the cache surface required by the sync service.
type Store interface {
	ReplaceSeason(context.Context, string, string, []cache.Team, []cache.Game, time.Time) (cache.SyncRun, error)
	RecordFailure(context.Context, string, string, time.Time, error) error
	LastSuccess(context.Context, string, string) (*cache.SyncRun, error)
	LastAttempt(context.Context, string, string) (*cache.SyncRun, error)
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
	// Trigger identifies the caller that started this sync (for example,
	// "scheduler", "cli", or "venue_history"). It is trace context, not
	// persisted cache state.
	Trigger                string
	MinimumAttemptInterval time.Duration
	Force                  bool
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
			attribute.String("sync.season", options.Season),
			attribute.String("sync.stage", options.Stage),
			attribute.Bool("sync.forced", options.Force),
			attribute.String("sync.trigger", syncTrigger(options.Trigger)),
		),
	)
	defer func() {
		span.SetAttributes(syncRunAttributes(run)...)
		if err != nil {
			telemetry.RecordErrorWithCode(ctx, span, err, "sync.run")
		}
		span.End()
	}()
	runMu.Lock()
	defer runMu.Unlock()

	startedAt := time.Now().UTC()
	if s.ASA == nil {
		return cache.SyncRun{}, errors.New("sync ASA client is required")
	}
	if s.Store == nil {
		return cache.SyncRun{}, errors.New("sync store is required")
	}
	if strings.TrimSpace(options.Season) == "" {
		return cache.SyncRun{}, errors.New("sync season is required")
	}
	if strings.TrimSpace(options.Stage) == "" {
		return cache.SyncRun{}, errors.New("sync stage is required")
	}

	holder := fmt.Sprintf("%d-%d", os.Getpid(), startedAt.UnixNano())
	acquired, err := s.Store.TryAcquireSyncLease(ctx, leaseKey(options), holder, leaseExpiry(ctx, startedAt))
	if err != nil {
		return cache.SyncRun{}, err
	}
	if !acquired {
		return cache.SyncRun{}, cache.ErrSyncInProgress
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Store.ReleaseSyncLease(releaseCtx, leaseKey(options), holder)
	}()

	if !options.Force && options.MinimumAttemptInterval > 0 {
		run, err := s.Store.LastAttempt(ctx, options.Season, options.Stage)
		if err != nil {
			return cache.SyncRun{}, fmt.Errorf("check recent sync: %w", err)
		}
		if run != nil && !run.FinishedAt.Add(options.MinimumAttemptInterval).Before(startedAt) {
			run.Skipped = true
			return *run, nil
		}
	}

	xgClient, hasXGClient := s.ASA.(xgASAClient)
	xgCache, hasXGCache := s.Store.(xgStore)
	fetchCtx, fetchSpan := telemetry.Tracer().Start(ctx, "sync.fetch_asa",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.Bool("sync.xg_requested", hasXGClient && hasXGCache),
		),
	)
	data := s.fetchASAData(fetchCtx, options, xgClient, hasXGClient && hasXGCache)
	fetchSpan.SetAttributes(
		attribute.Int("sync.teams_fetched", len(data.teams)),
		attribute.Int("sync.games_fetched", len(data.games)),
		attribute.Int("sync.xg_rows_fetched", len(data.xg)),
	)
	if fetchErr := errors.Join(data.teamsErr, data.gamesErr, data.xgErr); fetchErr != nil {
		telemetry.RecordErrorWithCode(fetchCtx, fetchSpan, fetchErr, "sync.fetch_asa")
	}
	fetchSpan.End()
	if data.teamsErr != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, fmt.Errorf("fetch teams: %w", data.teamsErr))
	}
	if data.gamesErr != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, fmt.Errorf("fetch games: %w", data.gamesErr))
	}
	teams, games := data.teams, data.games

	if err := validate(options, teams, games); err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, err)
	}

	cacheTeams, err := mapTeams(teams)
	if err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, err)
	}
	cacheGames, err := mapGames(options, games)
	if err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, err)
	}

	replaceCtx, replaceSpan := telemetry.Tracer().Start(ctx, "cache.season.replace",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("cache.name", "season"),
			attribute.String("sync.season", options.Season),
			attribute.String("sync.stage", options.Stage),
			attribute.Int("sync.team_count", len(cacheTeams)),
			attribute.Int("sync.fixture_count", len(cacheGames)),
		),
	)
	run, err = s.Store.ReplaceSeason(replaceCtx, options.Season, options.Stage, cacheTeams, cacheGames, startedAt)
	if err != nil {
		telemetry.RecordErrorWithCode(replaceCtx, replaceSpan, err, "cache.season.replace")
	} else {
		replaceSpan.SetAttributes(syncRunAttributes(run)...)
	}
	replaceSpan.End()
	if err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, err)
	}
	// xG is fetched concurrently with the fixture inventory, but committing it
	// still waits for the durable fixture snapshot above. The qualification and
	// scenario passes can legitimately outlive the source-sync context through
	// their independent derived budgets, so they remain after this step.
	if hasXGClient && hasXGCache {
		run = s.refreshXG(ctx, xgCache, options, startedAt, cacheGames, data.xg, data.xgErr, run)
	}
	if options.SourceOnly {
		return run, nil
	}
	run = s.refreshCalculations(context.WithoutCancel(ctx), run, cacheTeams, cacheGames, options.Force)
	return s.pruneHistory(run), nil
}

type asaSyncData struct {
	teams    []asa.Team
	teamsErr error
	games    []asa.Game
	gamesErr error
	xg       []asa.GameXGoals
	xgErr    error
}

// fetchASAData requests the independent ASA resources at the same time. xG
// remains an optional source: its error is handled only after the fixture
// snapshot commits successfully.
func (s Service) fetchASAData(ctx context.Context, options RunOptions, xgClient xgASAClient, fetchXG bool) asaSyncData {
	var data asaSyncData
	var group stdsync.WaitGroup

	group.Add(2)
	go func() {
		defer group.Done()
		data.teams, data.teamsErr = s.ASA.Teams(ctx, asa.TeamsFilters{})
	}()
	go func() {
		defer group.Done()
		data.games, data.gamesErr = s.ASA.Games(ctx, asa.GamesFilters{
			SeasonName: options.Season,
			StageName:  options.Stage,
			Status:     allGameStatuses,
		})
	}()
	if fetchXG {
		group.Add(1)
		go func() {
			defer group.Done()
			data.xg, data.xgErr = xgClient.GameXGoals(ctx, asa.XGoalsFilters{
				SeasonName: options.Season,
				StageName:  options.Stage,
			})
		}()
	}

	group.Wait()
	return data
}

// Recalculate reruns derived clinching calculations from the last successful
// fixture snapshot without performing a data or xG sync.
func (s Service) Recalculate(ctx context.Context, options RecalculateOptions) (run cache.SyncRun, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "sync.recalculate",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("sync.season", options.Season),
			attribute.String("sync.stage", options.Stage),
			attribute.Bool("sync.forced", options.Force),
			attribute.String("sync.trigger", syncTrigger(options.Trigger)),
		),
	)
	defer func() {
		span.SetAttributes(recalculateRunAttributes(run, err)...)
		if err != nil {
			telemetry.RecordErrorWithCode(ctx, span, err, "sync.recalculate")
		}
		span.End()
	}()
	if s.Store == nil {
		return cache.SyncRun{}, errors.New("sync store is required")
	}
	if strings.TrimSpace(options.Season) == "" {
		return cache.SyncRun{}, errors.New("recalculation season is required")
	}
	if strings.TrimSpace(options.Stage) == "" {
		return cache.SyncRun{}, errors.New("recalculation stage is required")
	}
	store, ok := s.Store.(calculationStore)
	if !ok {
		return cache.SyncRun{}, errors.New("sync store does not support cached clinching inputs")
	}
	inputs, err := store.ClinchingInputs(ctx, options.Season, options.Stage)
	if err != nil {
		return cache.SyncRun{}, fmt.Errorf("load cached clinching inputs: %w", err)
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
		attribute.String("sync.recalculate.outcome", recalculateOutcome(run, err)),
		attribute.Bool("sync.partial_failure", run.QualificationError != "" || run.ScenarioError != ""),
		attribute.Bool("sync.qualification_recalculated", run.QualificationRecalculated),
		attribute.Bool("sync.scenario_recalculated", run.ScenarioRecalculated),
		attribute.String("sync.qualification.outcome", recalculateComponentOutcome(run.QualificationRecalculated, run.QualificationError, err)),
		attribute.String("sync.scenario.outcome", recalculateComponentOutcome(run.ScenarioRecalculated, run.ScenarioError, err)),
	}
	if run.ID > 0 {
		attributes = append(attributes, attribute.Int64("sync.source_run_id", run.ID))
	}
	if run.FixtureSnapshotID != "" {
		attributes = append(attributes, attribute.String("cache.fixture_snapshot_id", run.FixtureSnapshotID))
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

func (s Service) refreshXG(ctx context.Context, store xgStore, options RunOptions, startedAt time.Time, games []cache.Game, source []asa.GameXGoals, sourceErr error, run cache.SyncRun) cache.SyncRun {
	ctx, span := telemetry.Tracer().Start(ctx, "sync.xg.refresh",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("sync.season", options.Season),
			attribute.String("sync.stage", options.Stage),
			attribute.Int("sync.fixture_count", len(games)),
			attribute.Int("sync.xg_rows_fetched", len(source)),
		),
	)
	defer func() {
		outcome := "success"
		if run.XGError != "" {
			outcome = "failure"
		}
		span.SetAttributes(attribute.String("sync.xg.outcome", outcome))
		if run.XGRun != nil {
			span.SetAttributes(
				attribute.Int64("sync.xg.available_games", run.XGRun.AvailableGames),
				attribute.Int64("sync.xg.unavailable_games", run.XGRun.UnavailableGames),
			)
		}
		span.End()
	}()
	if sourceErr != nil {
		cause := fmt.Errorf("fetch game xG: %w", sourceErr)
		telemetry.RecordErrorWithCode(ctx, span, cause, "sync.refresh_xg")
		return s.xgWarning(ctx, store, options, startedAt, run, cause)
	}
	values, err := mapXGoals(source)
	if err != nil {
		telemetry.RecordErrorWithCode(ctx, span, err, "sync.refresh_xg")
		return s.xgWarning(ctx, store, options, startedAt, run, err)
	}
	xgRun, err := store.ReplaceGameXG(ctx, options.Season, options.Stage, games, values, startedAt)
	if err != nil {
		telemetry.RecordErrorWithCode(ctx, span, err, "sync.refresh_xg")
		return s.xgWarning(ctx, store, options, startedAt, run, err)
	}
	run.XGRun = &xgRun
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
		attribute.String("sync.outcome", run.Outcome),
		attribute.Bool("sync.skipped", run.Skipped),
		attribute.Int("sync.teams_seen", run.TeamsUpserted),
		attribute.Int("sync.games_seen", run.GamesSeen),
		attribute.Int("sync.teams_inserted", run.TeamsInserted),
		attribute.Int("sync.teams_updated", run.TeamsUpdated),
		attribute.Int("sync.teams_unchanged", run.TeamsUnchanged),
		attribute.Int("sync.games_inserted", run.GamesInserted),
		attribute.Int("sync.games_updated", run.GamesUpdated),
		attribute.Int("sync.games_unchanged", run.GamesUnchanged),
		attribute.Int("sync.games_deleted", run.GamesDeleted),
		attribute.String("cache.fixture_snapshot_id", run.FixtureSnapshotID),
		attribute.Bool("sync.partial_failure", run.XGError != "" || run.QualificationError != "" || run.ScenarioError != ""),
		attribute.String("sync.xg.outcome", syncComponentOutcome(run.XGRun != nil, run.XGError)),
		attribute.String("sync.qualification.outcome", syncComponentOutcome(run.QualificationRecalculated, run.QualificationError)),
		attribute.String("sync.scenario.outcome", syncComponentOutcome(run.ScenarioRecalculated, run.ScenarioError)),
	}
	if run.XGRun != nil {
		attributes = append(attributes,
			attribute.Int64("sync.xg.available_games", run.XGRun.AvailableGames),
			attribute.Int64("sync.xg.unavailable_games", run.XGRun.UnavailableGames),
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

func (s Service) xgWarning(ctx context.Context, store xgStore, options RunOptions, startedAt time.Time, run cache.SyncRun, cause error) cache.SyncRun {
	recordCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		recordCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	if err := store.RecordXGFailure(recordCtx, options.Season, options.Stage, startedAt, cause); err != nil {
		run.XGError = cause.Error() + "; additionally failed to record xG failure: " + err.Error()
	} else {
		run.XGError = cause.Error()
	}
	return run
}

func (s Service) fail(ctx context.Context, options RunOptions, startedAt time.Time, cause error) error {
	recordCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		recordCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	if err := s.Store.RecordFailure(recordCtx, options.Season, options.Stage, startedAt, cause); err != nil {
		return fmt.Errorf("%w; additionally failed to record sync failure: %v", cause, err)
	}
	return cause
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

func validate(options RunOptions, teams []asa.Team, games []asa.Game) error {
	if len(teams) == 0 {
		return errors.New("validate ASA response: teams response is empty")
	}
	if len(games) == 0 {
		return errors.New("validate ASA response: games response is empty")
	}

	teamIDs := make(map[string]struct{}, len(teams))
	for i, team := range teams {
		if strings.TrimSpace(team.TeamID) == "" {
			return fmt.Errorf("validate ASA response: team %d is missing team_id", i)
		}
		teamIDs[team.TeamID] = struct{}{}
	}

	gameIDs := make(map[string]struct{}, len(games))
	for i, game := range games {
		label := fmt.Sprintf("game %d", i)
		if game.GameID != "" {
			label = fmt.Sprintf("game %q", game.GameID)
		}
		if strings.TrimSpace(game.GameID) == "" {
			return fmt.Errorf("validate ASA response: %s is missing game_id", label)
		}
		if _, exists := gameIDs[game.GameID]; exists {
			return fmt.Errorf("validate ASA response: duplicate game_id %q", game.GameID)
		}
		gameIDs[game.GameID] = struct{}{}
		if strings.TrimSpace(game.SeasonName) == "" {
			return fmt.Errorf("validate ASA response: %s is missing season_name", label)
		}
		if game.SeasonName != options.Season {
			return fmt.Errorf("validate ASA response: %s season_name = %q, want %q", label, game.SeasonName, options.Season)
		}
		if strings.TrimSpace(game.DateTimeUTC) == "" {
			return fmt.Errorf("validate ASA response: %s is missing date_time_utc", label)
		}
		if strings.TrimSpace(game.Status) == "" {
			return fmt.Errorf("validate ASA response: %s is missing status", label)
		}
		if _, ok := teamIDs[game.HomeTeamID]; !ok {
			return fmt.Errorf("validate ASA response: %s references unknown home team %q", label, game.HomeTeamID)
		}
		if _, ok := teamIDs[game.AwayTeamID]; !ok {
			return fmt.Errorf("validate ASA response: %s references unknown away team %q", label, game.AwayTeamID)
		}
		if game.HomeTeamID == game.AwayTeamID {
			return fmt.Errorf("validate ASA response: %s has the same home and away team %q", label, game.HomeTeamID)
		}
		if game.HomeScore != nil && *game.HomeScore < 0 {
			return fmt.Errorf("validate ASA response: %s has negative home score", label)
		}
		if game.AwayScore != nil && *game.AwayScore < 0 {
			return fmt.Errorf("validate ASA response: %s has negative away score", label)
		}
		if game.Status == fixtures.CompletedStatus && (game.HomeScore == nil || game.AwayScore == nil) {
			return fmt.Errorf("validate ASA response: %s is FullTime without both scores", label)
		}
	}
	return nil
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
