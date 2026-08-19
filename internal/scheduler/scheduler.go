// Package scheduler plans and executes bounded, due ASA source operations.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/syncer"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type SnapshotStore interface {
	PlanningSnapshot(context.Context) (cache.PlanningSnapshot, error)
	TryAcquireSyncLease(context.Context, string, string, time.Time) (bool, error)
	ReleaseSyncLease(context.Context, string, string) error
}

type Runner interface {
	Execute(context.Context, syncer.Operation) (syncer.OperationResult, error)
}

type calculationRunner interface {
	Recalculate(context.Context, syncer.RecalculateOptions) (cache.SyncRun, error)
}

type Config struct {
	Season                   string
	Stage                    string
	ExpectedTeams            int
	GamesPerTeam             int
	CheckInterval            time.Duration
	CompletionGrace          time.Duration
	Timeout                  time.Duration
	SourceRequestBudget      int
	ResultCorrectionInterval time.Duration
	MissingXGInterval        time.Duration
	XGCorrectionInterval     time.Duration
	GameCorrectionWindow     time.Duration
	MissingXGWindow          time.Duration
	XGCorrectionWindow       time.Duration
	InventoryInterval        time.Duration
	ColdSweepInterval        time.Duration
}

type Scheduler struct {
	store    SnapshotStore
	runner   Runner
	config   Config
	logger   *slog.Logger
	now      func() time.Time
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

const coldSweepLeaseKey = "cold-sweep"

type SweepEntry struct {
	Job      Job
	Result   syncer.OperationResult
	Material bool
}

// SweepReport describes one explicit, sequential archived maintenance pass.
// It never retains a lease between source requests.
type SweepReport struct {
	Entries       []SweepEntry
	Requests      int
	Reason        string
	Deferred      bool
	EvidenceDirty bool
}

func New(store SnapshotStore, runner Runner, config Config, logger *slog.Logger) (*Scheduler, error) {
	if store == nil {
		return nil, errors.New("scheduler store is required")
	}
	if runner == nil {
		return nil, errors.New("scheduler runner is required")
	}
	if config.Season == "" || config.Stage == "" {
		return nil, errors.New("scheduler season and stage are required")
	}
	if err := validateScheduleConfig(config); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		name  string
		value time.Duration
	}{{"check interval", config.CheckInterval}, {"completion grace", config.CompletionGrace}, {"timeout", config.Timeout}} {
		if field.value <= 0 {
			return nil, fmt.Errorf("scheduler %s must be positive", field.name)
		}
	}
	if config.SourceRequestBudget < 0 {
		return nil, errors.New("scheduler source request budget must not be negative")
	}
	for _, field := range []struct {
		name  string
		value time.Duration
	}{{"result correction interval", config.ResultCorrectionInterval}, {"missing xG interval", config.MissingXGInterval}, {"xG correction interval", config.XGCorrectionInterval}, {"game correction window", config.GameCorrectionWindow}, {"missing xG window", config.MissingXGWindow}, {"xG correction window", config.XGCorrectionWindow}, {"inventory interval", config.InventoryInterval}, {"cold sweep interval", config.ColdSweepInterval}} {
		if field.value < 0 {
			return nil, fmt.Errorf("scheduler %s must not be negative", field.name)
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{store: store, runner: runner, config: config, logger: logger, now: time.Now, stop: make(chan struct{}), done: make(chan struct{})}, nil
}

func (s *Scheduler) Start() {
	go func() {
		defer close(s.done)
		s.check()
		ticker := time.NewTicker(s.config.CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				select {
				case <-s.stop:
					return
				default:
				}
				s.check()
			}
		}
	}()
}
func (s *Scheduler) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }
func (s *Scheduler) Wait() { <-s.done }

func (s *Scheduler) check() {
	ctx, span := telemetry.Tracer().Start(context.Background(), nwslconv.SpanSchedulerTick, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(nwslconv.SeasonName(s.config.Season), nwslconv.Stage(s.config.Stage)))
	defer span.End()
	planningCtx, planningCancel := context.WithTimeout(ctx, s.config.Timeout)
	snapshot, err := s.store.PlanningSnapshot(planningCtx)
	planningCancel()
	if err != nil {
		span.SetAttributes(nwslconv.SchedulerAction("read_planning_snapshot"), nwslconv.SchedulerOutcome(nwslconv.SchedulerOutcomeFailure))
		_ = telemetry.RecordWarningWithType(ctx, span, err, nwslconv.ErrorCodeSchedulerPlanningSnapshot, telemetry.ErrorTypeStorageFailure)
		return
	}
	// Source jobs use the split-operation API, so they do not invoke
	// syncer.Service.Run's derived-data refresh. Recheck an already published
	// current inventory before any maintenance work: a slow or failed archived
	// request must not indefinitely block a missing clinching batch.
	preflightCalculation := nwslconv.SchedulerClinchingPreflightOutcomeNotNeeded
	if currentInventoryAvailable(snapshot, s.config.Season, s.config.Stage) {
		preflightCalculation = s.recalculateCachedClinching(ctx, span, s.config.Season, s.config.Stage, "preflight")
	}
	span.SetAttributes(nwslconv.SchedulerClinchingPreflightOutcome(preflightCalculation))
	now := s.now().UTC()
	jobs := Plan(snapshot, s.config, now)
	availableJobs := Plan(snapshot, unlimitedRequestBudget(s.config), now)
	span.SetAttributes(nwslconv.SchedulerJobCount(len(jobs)), nwslconv.SchedulerRequestBudget(requestBudget(s.config)))
	if len(jobs) == 0 {
		span.AddEvent(nwslconv.EventSchedulerDecision, trace.WithAttributes(nwslconv.SchedulerDecision(nwslconv.SchedulerDecisionNotCheck), nwslconv.SchedulerReason("no_source_request_due")))
		outcome := preflightCalculation
		if outcome == nwslconv.SchedulerClinchingPreflightOutcomeNotNeeded {
			outcome = s.recalculateCachedClinching(ctx, span, s.config.Season, s.config.Stage, "no_source_request_due")
		}
		span.SetAttributes(nwslconv.SchedulerAction("recalculate"), nwslconv.SchedulerRequestCount(0), nwslconv.SchedulerOutcome(outcome))
		return
	}
	for _, job := range jobs {
		decisionAttributes := []attribute.KeyValue{nwslconv.SchedulerDecision(nwslconv.SchedulerDecisionCheck), nwslconv.SchedulerReason(job.Reason), nwslconv.SchedulerJobKind(string(job.Kind)), nwslconv.SchedulerJobScope(job.Operation.Season + "/" + job.Operation.Stage), nwslconv.SchedulerRequestedRows(len(job.Operation.Requested))}
		if job.Selection.Policy != "" {
			decisionAttributes = append(decisionAttributes, selectionAttributes(job.Selection)...)
		}
		span.AddEvent(nwslconv.EventSchedulerDecision, trace.WithAttributes(decisionAttributes...))
	}
	if len(availableJobs) > len(jobs) {
		span.AddEvent(nwslconv.EventSchedulerDecision, trace.WithAttributes(nwslconv.SchedulerDecision(nwslconv.SchedulerDecisionNotCheck), nwslconv.SchedulerReason("source_request_budget_exhausted"), nwslconv.SchedulerDeferredJobCount(len(availableJobs)-len(jobs))))
	}
	requests := 0
	tickOutcome := nwslconv.SchedulerOutcomeComplete
jobsLoop:
	for _, job := range jobs {
		select {
		case <-s.stop:
			tickOutcome = nwslconv.SchedulerOutcomeStopped
			break jobsLoop
		default:
		}
		requestCtx, requestCancel := context.WithTimeout(ctx, s.config.Timeout)
		jobCtx, jobSpan := telemetry.Tracer().Start(requestCtx, nwslconv.SpanSchedulerJob, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(jobAttributes(job, syncer.OperationResult{}, nwslconv.SchedulerJobOutcomePlanned, requests)...))
		outcome, result, attempted, jobErr := s.executeJob(jobCtx, job)
		if attempted {
			requests++
		}
		jobSpan.SetAttributes(jobAttributes(job, result, outcome, requests)...)
		if strings.HasPrefix(outcome, "deferred") {
			jobSpan.AddEvent(nwslconv.EventSchedulerDecision, trace.WithAttributes(nwslconv.SchedulerDecision(nwslconv.SchedulerDecisionNotCheck), nwslconv.SchedulerReason(outcome)))
		}
		if jobErr != nil {
			telemetry.MarkError(jobSpan, jobErr)
		}
		jobSpan.End()
		requestCancel()
		if outcome == nwslconv.SchedulerJobOutcomeFailure {
			tickOutcome = nwslconv.SchedulerOutcomeFailure
			break
		}
		if strings.HasPrefix(outcome, "deferred") {
			tickOutcome = nwslconv.SchedulerOutcomeDeferred
		}
		if job.Class == JobHot && result.Games != nil && result.FixtureInputsChanged && job.Operation.Season == s.config.Season && job.Operation.Stage == s.config.Stage {
			derivedOutcome := s.recalculateCachedClinching(ctx, span, job.Operation.Season, job.Operation.Stage, "game_inputs_changed")
			if derivedOutcome == nwslconv.SchedulerClinchingPreflightOutcomeFailure || derivedOutcome == nwslconv.SchedulerClinchingPreflightOutcomePartialFailure {
				tickOutcome = nwslconv.SchedulerOutcomePartialFailure
			}
		}
		if job.Class == JobCold && (result.FixtureInputsChanged || result.XGInputsChanged) && historicalEvidenceScope(job.Operation.Season, job.Operation.Stage) {
			span.SetAttributes(nwslconv.SchedulerEvaluationEvidenceDirty(true))
			s.logger.Info("historical correction changed evaluation evidence", "season", job.Operation.Season, "stage", job.Operation.Stage, "resource", job.Kind)
		}
	}
	span.SetAttributes(nwslconv.SchedulerRequestCount(requests), nwslconv.SchedulerOutcome(tickOutcome))
}

func currentInventoryAvailable(snapshot cache.PlanningSnapshot, season, stage string) bool {
	for _, scope := range snapshot.Scopes {
		if scope.Readiness.Scope.Season != season || scope.Readiness.Scope.Stage != stage {
			continue
		}
		return scope.Readiness.Scope.Lifecycle == cache.SourceScopeActive &&
			scope.Readiness.Readiness == cache.SourceReadinessAvailable &&
			len(scope.Games) > 0
	}
	return false
}

func (s *Scheduler) executeJob(parent context.Context, job Job) (string, syncer.OperationResult, bool, error) {
	now := s.now().UTC()
	holder := fmt.Sprintf("scheduler-%d", now.UnixNano())
	if job.Class == JobCold {
		acquired, err := s.store.TryAcquireSyncLease(parent, coldSweepLeaseKey, holder, now.Add(s.config.Timeout))
		if err != nil {
			s.logger.Error("acquire cold sweep lease", "job", job.Kind, "error", err)
			return nwslconv.SchedulerJobOutcomeFailure, syncer.OperationResult{}, false, err
		}
		if !acquired {
			return nwslconv.SchedulerJobOutcomeDeferredGlobalLease, syncer.OperationResult{}, false, nil
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.store.ReleaseSyncLease(releaseCtx, coldSweepLeaseKey, holder)
		}()
	}
	key := jobLeaseKey(job)
	acquired, err := s.store.TryAcquireSyncLease(parent, key, holder, now.Add(s.config.Timeout))
	if err != nil {
		s.logger.Error("acquire source job lease", "job", job.Kind, "error", err)
		return nwslconv.SchedulerJobOutcomeFailure, syncer.OperationResult{}, false, err
	}
	if !acquired {
		return nwslconv.SchedulerJobOutcomeDeferredScopeLease, syncer.OperationResult{}, false, nil
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.store.ReleaseSyncLease(releaseCtx, key, holder)
	}()
	job.Operation.StartedAt = now
	result, err := s.runner.Execute(parent, job.Operation)
	if err != nil {
		s.logger.Error("source job failed", "job", job.Kind, "season", job.Operation.Season, "stage", job.Operation.Stage, "error", err)
		return nwslconv.SchedulerJobOutcomeFailure, result, true, err
	}
	return nwslconv.SchedulerJobOutcomeComplete, result, true, nil
}

// SweepDueArchived repeatedly takes fresh snapshots and executes only the one
// due cold job selected by Plan. A newly due hot job therefore ends the pass
// before another archived request starts.
func (s *Scheduler) SweepDueArchived(parent context.Context) (SweepReport, error) {
	report := SweepReport{Entries: []SweepEntry{}}
	for {
		if err := parent.Err(); err != nil {
			report.Reason = "context_expired"
			return report, err
		}
		snapshot, err := s.store.PlanningSnapshot(parent)
		if err != nil {
			report.Reason = "snapshot_failure"
			return report, err
		}
		jobs := Plan(snapshot, s.config, s.now().UTC())
		if len(jobs) == 0 {
			report.Reason = "complete"
			return report, nil
		}
		job := jobs[0]
		if job.Class != JobCold {
			report.Reason, report.Deferred = "hot_work_due", true
			return report, nil
		}
		job.Operation.Trigger = cache.SourceTriggerMaintenance
		requestCtx, cancel := context.WithTimeout(parent, s.config.Timeout)
		outcome, result, attempted, err := s.executeJob(requestCtx, job)
		cancel()
		if attempted {
			report.Requests++
		}
		if strings.HasPrefix(outcome, "deferred") {
			report.Reason, report.Deferred = strings.TrimPrefix(outcome, "deferred_"), true
			return report, nil
		}
		if err != nil {
			report.Reason = "request_failure"
			return report, err
		}
		material := result.FixtureInputsChanged || result.XGInputsChanged
		report.Entries = append(report.Entries, SweepEntry{Job: job, Result: result, Material: material})
		if material && historicalEvidenceScope(job.Operation.Season, job.Operation.Stage) {
			report.EvidenceDirty = true
		}
	}
}

func historicalEvidenceScope(season, stage string) bool {
	entry, found := competition.Lookup(season, stage)
	return found && entry.Public && entry.SourceAvailable && entry.Rules == nil
}

func (s *Scheduler) recalculateCachedClinching(parent context.Context, span trace.Span, season, stage, reason string) string {
	runner, ok := s.runner.(calculationRunner)
	if !ok {
		return nwslconv.SchedulerClinchingPreflightOutcomeCurrent
	}
	ctx, cancel := context.WithTimeout(parent, s.config.Timeout)
	run, err := runner.Recalculate(ctx, syncer.RecalculateOptions{Season: season, Stage: stage, Trigger: "scheduler", Reason: reason})
	cancel()
	if err != nil {
		telemetry.MarkError(span, err)
		return nwslconv.SchedulerClinchingPreflightOutcomeFailure
	}
	if run.QualificationError != "" || run.ScenarioError != "" {
		return nwslconv.SchedulerClinchingPreflightOutcomePartialFailure
	}
	if run.QualificationRecalculated || run.ScenarioRecalculated {
		return nwslconv.SchedulerClinchingPreflightOutcomeComplete
	}
	return nwslconv.SchedulerClinchingPreflightOutcomeCurrent
}

func jobLeaseKey(job Job) string {
	// Match Service.Run's compatibility lease while it remains a supported
	// manual path. Phase 3 intentionally serializes all source resources for
	// one scope rather than risking a full compatibility run racing a job.
	return job.Operation.Season + "\x00" + job.Operation.Stage
}
func requestBudget(config Config) int {
	if config.SourceRequestBudget > 0 {
		return config.SourceRequestBudget
	}
	return defaultSourceRequestBudget
}

func unlimitedRequestBudget(config Config) Config {
	config.SourceRequestBudget = int(^uint(0) >> 1)
	return config
}
func jobAttributes(job Job, result syncer.OperationResult, outcome string, requests int) []attribute.KeyValue {
	returned, material := 0, false
	if result.Games != nil {
		returned = result.Games.Audit.ReturnedRows
		material = result.FixtureInputsChanged
	}
	if result.XG != nil {
		returned = result.XG.Audit.ReturnedRows
		material = result.XGInputsChanged
	}
	attributes := []attribute.KeyValue{nwslconv.SchedulerJobKind(string(job.Kind)), nwslconv.SchedulerJobClass(string(job.Class)), nwslconv.SchedulerJobOutcome(outcome), nwslconv.SchedulerJobReason(job.Reason), nwslconv.SchedulerJobScope(job.Operation.Season + "/" + job.Operation.Stage), nwslconv.SchedulerJobRequestedRows(len(job.Operation.Requested)), nwslconv.SchedulerJobReturnedRows(returned), nwslconv.SchedulerJobMaterial(material), nwslconv.SchedulerRequestCount(requests)}
	if result.Games != nil {
		attributes = append(attributes, nwslconv.SchedulerSourceValueChanged(result.GameFreshness.ValueChanged > 0), nwslconv.SchedulerSourceValueChangedCount(result.GameFreshness.ValueChanged), nwslconv.SchedulerSourceValueInitializedCount(result.GameFreshness.ValueInitialized), nwslconv.SchedulerSourceMetadataChangedCount(result.GameFreshness.MetadataChanged))
	}
	if result.XG != nil {
		var changed, initialized, metadata, missing int
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
		}
		attributes = append(attributes, nwslconv.SchedulerSourceValueChanged(changed > 0), nwslconv.SchedulerSourceValueChangedCount(changed), nwslconv.SchedulerSourceValueInitializedCount(initialized), nwslconv.SchedulerSourceMetadataChangedCount(metadata), nwslconv.SchedulerSourceValueMissingCount(missing))
	}
	if job.Selection.Policy != "" {
		attributes = append(attributes, selectionAttributes(job.Selection)...)
	}
	return attributes
}

func selectionAttributes(selection selectionMetadata) []attribute.KeyValue {
	attributes := []attribute.KeyValue{nwslconv.SchedulerSelectionPolicy(selection.Policy), nwslconv.SchedulerCandidateCount(selection.CandidateCount), nwslconv.SchedulerEligibleCount(selection.EligibleCount), nwslconv.SchedulerExpiredCount(selection.ExpiredCount), nwslconv.SchedulerInvalidKickoffCount(selection.InvalidKickoffCount), nwslconv.SchedulerMissingXGCandidateCount(selection.MissingCandidateCount), nwslconv.SchedulerMissingXGEligibleCount(selection.MissingEligibleCount), nwslconv.SchedulerAvailableXGCandidateCount(selection.AvailableCandidateCount), nwslconv.SchedulerAvailableXGEligibleCount(selection.AvailableEligibleCount), nwslconv.SchedulerMissingXGPollIntervalSeconds(int(selection.MissingPollInterval / time.Second)), nwslconv.SchedulerMissingXGWatchWindowSeconds(int(selection.MissingWatchWindow / time.Second)), nwslconv.SchedulerXGCorrectionIntervalSeconds(int(selection.AvailablePollInterval / time.Second)), nwslconv.SchedulerXGCorrectionWatchWindowSeconds(int(selection.AvailableWatchWindow / time.Second))}
	if selection.PollInterval > 0 {
		attributes = append(attributes, nwslconv.SchedulerPollIntervalSeconds(int(selection.PollInterval/time.Second)))
	}
	if selection.WatchWindow > 0 {
		attributes = append(attributes, nwslconv.SchedulerWatchWindowSeconds(int(selection.WatchWindow/time.Second)))
	}
	if !selection.OldestKickoff.IsZero() {
		attributes = append(attributes, nwslconv.SchedulerOldestKickoffUTC(selection.OldestKickoff.UTC().Format(time.RFC3339)))
	}
	if !selection.NewestKickoff.IsZero() {
		attributes = append(attributes, nwslconv.SchedulerNewestKickoffUTC(selection.NewestKickoff.UTC().Format(time.RFC3339)))
	}
	return attributes
}
func validateScheduleConfig(config Config) error {
	if config.ExpectedTeams == 0 && config.GamesPerTeam == 0 {
		return nil
	}
	if config.ExpectedTeams < 1 || config.GamesPerTeam < 1 || config.ExpectedTeams*config.GamesPerTeam%2 != 0 {
		return errors.New("scheduler expected teams and games per team must describe an even schedule")
	}
	return nil
}
func expectedFixtureCount(teams, games int) int {
	if teams < 1 || games < 1 || teams*games%2 != 0 {
		return 0
	}
	return teams * games / 2
}
