package server

import (
	"context"
	"log/slog"

	"github.com/jrduncans/nwsl-season/internal/app"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/lifetime"
	"github.com/jrduncans/nwsl-season/internal/syncer"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/trace"
)

// forecastWarmingRunner preserves the scheduler's normal sync behavior, then
// refreshes the process-local baseline forecast cache only when an input to a
// forecast actually changed.
type forecastWarmingRunner struct {
	service       syncer.Service
	application   *app.Application
	logger        *slog.Logger
	currentSeason string
	currentStage  string
}

func (r forecastWarmingRunner) Execute(ctx context.Context, operation syncer.Operation) (syncer.OperationResult, error) {
	result, err := r.service.Execute(ctx, operation)
	if err != nil {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeNotRun)
		return result, err
	}
	if !r.forecastInputsForScope(operation.Season, operation.Stage) || (!result.FixtureInputsChanged && !result.XGInputsChanged) {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeNotNeeded)
		return result, nil
	}
	outcome, err := r.warmForecasts(ctx, "post_source_job")
	setSchedulerForecastWarmOutcome(ctx, outcome)
	if err != nil {
		r.logger.Warn("pre-cache forecasts after source job", "season", operation.Season, "stage", operation.Stage, "error", err)
	}
	return result, nil
}

// warmForecasts refreshes the baseline forecast cache. Warm-up is independent
// from the source-request deadline: the forecast executor applies its own
// per-model deadline, and a failure must not turn a successful ASA cache
// transaction into a failed sync. It still stops with the scheduler, and that
// expected interruption is reported as not run rather than as a failure.
func (r forecastWarmingRunner) warmForecasts(ctx context.Context, trigger string) (string, error) {
	warmCtx, cancel := lifetime.Detach(ctx)
	defer cancel()
	err := r.application.PrecacheForecastsWithTrigger(warmCtx, trigger)
	switch {
	case warmCtx.Err() != nil:
		return nwslconv.SchedulerForecastWarmOutcomeNotRun, nil
	case err != nil:
		return nwslconv.SchedulerForecastWarmOutcomeFailed, err
	default:
		return nwslconv.SchedulerForecastWarmOutcomeComplete, nil
	}
}

func (r forecastWarmingRunner) forecastInputsForScope(season, stage string) bool {
	if stage != r.currentStage {
		return false
	}
	if season == r.currentSeason {
		return true
	}
	previous, err := competition.PreviousRegularSeasons(r.currentSeason, 2)
	if err != nil {
		return false
	}
	for _, candidate := range previous {
		if season == candidate {
			return true
		}
	}
	return false
}

func (r forecastWarmingRunner) Run(ctx context.Context, options syncer.RunOptions) (cache.SyncRun, error) {
	run, err := r.service.Run(ctx, options)
	if err != nil {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeNotRun)
		return run, err
	}
	if !forecastInputsChanged(run) {
		setSchedulerForecastWarmOutcome(ctx, nwslconv.SchedulerForecastWarmOutcomeNotNeeded)
		return run, err
	}
	outcome, err := r.warmForecasts(ctx, "post_sync")
	setSchedulerForecastWarmOutcome(ctx, outcome)
	if err != nil {
		r.logger.Warn("pre-cache forecasts after data refresh", "season", options.Season, "stage", options.Stage, "error", err)
		return run, nil
	}
	if outcome == nwslconv.SchedulerForecastWarmOutcomeComplete {
		r.logger.Info("pre-cached forecasts after data refresh", "season", options.Season, "stage", options.Stage,
			"fixture_snapshot_id", run.FixtureSnapshotID)
	}
	return run, nil
}

func setSchedulerForecastWarmOutcome(ctx context.Context, outcome string) {
	trace.SpanFromContext(ctx).SetAttributes(nwslconv.SchedulerForecastWarmOutcome(outcome))
}

func (r forecastWarmingRunner) Recalculate(ctx context.Context, options syncer.RecalculateOptions) (cache.SyncRun, error) {
	return r.service.Recalculate(ctx, options)
}

func forecastInputsChanged(run cache.SyncRun) bool {
	if run.TeamsInserted > 0 || run.TeamsUpdated > 0 || run.GamesInserted > 0 || run.GamesUpdated > 0 || run.GamesDeleted > 0 {
		return true
	}
	if run.XGRun == nil {
		return false
	}
	return run.XGRun.RowsInserted > 0 || run.XGRun.RowsUpdated > 0
}
