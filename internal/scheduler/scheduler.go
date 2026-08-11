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
	Season                string
	Stage                 string
	ExpectedTeams         int
	GamesPerTeam          int
	CheckInterval         time.Duration
	CompletionGrace       time.Duration
	Timeout               time.Duration
	SourceRequestBudget   int
	CorrectionInterval    time.Duration
	CorrectionDaily       time.Duration
	CorrectionFastWindow  time.Duration
	CorrectionFinalWindow time.Duration
	InventoryInterval     time.Duration
	ColdSweepInterval     time.Duration
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
	}{{"correction interval", config.CorrectionInterval}, {"correction daily", config.CorrectionDaily}, {"correction fast window", config.CorrectionFastWindow}, {"correction final window", config.CorrectionFinalWindow}, {"inventory interval", config.InventoryInterval}, {"cold sweep interval", config.ColdSweepInterval}} {
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
	tickCtx, tickCancel := context.WithTimeout(context.Background(), s.config.Timeout)
	defer tickCancel()
	ctx, span := telemetry.Tracer().Start(tickCtx, "scheduler.check", trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(attribute.String("nwsl.season", s.config.Season), attribute.String("nwsl.stage", s.config.Stage)))
	defer span.End()
	snapshot, err := s.store.PlanningSnapshot(ctx)
	if err != nil {
		span.SetAttributes(attribute.String("nwsl.scheduler.action", "read_planning_snapshot"), attribute.String("nwsl.scheduler.outcome", "failure"))
		telemetry.RecordWarningWithType(ctx, span, err, "scheduler.planning_snapshot", telemetry.ErrorTypeStorageFailure)
		return
	}
	jobs := Plan(snapshot, s.config, s.now().UTC())
	span.SetAttributes(attribute.Int("nwsl.scheduler.job_count", len(jobs)), attribute.Int("nwsl.scheduler.request_budget", requestBudget(s.config)))
	if len(jobs) == 0 {
		span.SetAttributes(attribute.String("nwsl.scheduler.action", "recalculate"), attribute.Int("nwsl.scheduler.request_count", 0), attribute.String("nwsl.scheduler.outcome", s.recalculateCachedClinching(ctx, span, s.config.Season, s.config.Stage)))
		return
	}
	requests := 0
	tickOutcome := "complete"
jobsLoop:
	for _, job := range jobs {
		select {
		case <-s.stop:
			tickOutcome = "stopped"
			break jobsLoop
		default:
		}
		if ctx.Err() != nil {
			tickOutcome = "expired"
			break
		}
		jobCtx, jobSpan := telemetry.Tracer().Start(ctx, "scheduler.job", trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(jobAttributes(job, syncer.OperationResult{}, "planned", requests)...))
		outcome, result, attempted, jobErr := s.executeJob(jobCtx, job)
		if attempted {
			requests++
		}
		jobSpan.SetAttributes(jobAttributes(job, result, outcome, requests)...)
		if jobErr != nil {
			telemetry.MarkError(jobSpan, jobErr)
		}
		jobSpan.End()
		if outcome == "failure" {
			tickOutcome = "failure"
			break
		}
		if strings.HasPrefix(outcome, "deferred") {
			tickOutcome = "deferred"
		}
		if job.Class == JobHot && result.Games != nil && result.FixtureInputsChanged && job.Operation.Season == s.config.Season && job.Operation.Stage == s.config.Stage {
			derivedOutcome := s.recalculateCachedClinching(ctx, span, job.Operation.Season, job.Operation.Stage)
			if derivedOutcome == "failure" || derivedOutcome == "partial_failure" {
				tickOutcome = "partial_failure"
			}
		}
		if job.Class == JobCold && (result.FixtureInputsChanged || result.XGInputsChanged) && historicalEvidenceScope(job.Operation.Season, job.Operation.Stage) {
			span.SetAttributes(attribute.Bool("nwsl.scheduler.evaluation_evidence_dirty", true))
			s.logger.Info("historical correction changed evaluation evidence", "season", job.Operation.Season, "stage", job.Operation.Stage, "resource", job.Kind)
		}
	}
	span.SetAttributes(attribute.Int("nwsl.scheduler.request_count", requests), attribute.String("nwsl.scheduler.outcome", tickOutcome))
}

func (s *Scheduler) executeJob(parent context.Context, job Job) (string, syncer.OperationResult, bool, error) {
	now := s.now().UTC()
	holder := fmt.Sprintf("scheduler-%d", now.UnixNano())
	if job.Class == JobCold {
		acquired, err := s.store.TryAcquireSyncLease(parent, coldSweepLeaseKey, holder, now.Add(s.config.Timeout))
		if err != nil {
			s.logger.Error("acquire cold sweep lease", "job", job.Kind, "error", err)
			return "failure", syncer.OperationResult{}, false, err
		}
		if !acquired {
			return "deferred_global_lease", syncer.OperationResult{}, false, nil
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
		return "failure", syncer.OperationResult{}, false, err
	}
	if !acquired {
		return "deferred_scope_lease", syncer.OperationResult{}, false, nil
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
		return "failure", result, true, err
	}
	return "complete", result, true, nil
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

func (s *Scheduler) recalculateCachedClinching(parent context.Context, span trace.Span, season, stage string) string {
	runner, ok := s.runner.(calculationRunner)
	if !ok {
		return "current"
	}
	ctx, cancel := context.WithTimeout(parent, s.config.Timeout)
	run, err := runner.Recalculate(ctx, syncer.RecalculateOptions{Season: season, Stage: stage, Trigger: "scheduler"})
	cancel()
	if err != nil {
		telemetry.MarkError(span, err)
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
	return []attribute.KeyValue{attribute.String("nwsl.scheduler.job_kind", string(job.Kind)), attribute.String("nwsl.scheduler.job_class", string(job.Class)), attribute.String("nwsl.scheduler.job_outcome", outcome), attribute.String("nwsl.scheduler.job_reason", job.Reason), attribute.String("nwsl.scheduler.job_scope", job.Operation.Season+"/"+job.Operation.Stage), attribute.Int("nwsl.scheduler.job_requested_rows", len(job.Operation.Requested)), attribute.Int("nwsl.scheduler.job_returned_rows", returned), attribute.Bool("nwsl.scheduler.job_material", material), attribute.Int("nwsl.scheduler.request_count", requests)}
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
