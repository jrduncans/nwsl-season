package syncer

import (
	"context"
	"fmt"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type venueSummaryStore interface {
	VenueSummaries(context.Context, []string, string) ([]cache.VenueSummary, error)
}

// EnsureVenueHistory synchronizes only the prior regular seasons whose
// persisted fixture/xG venue summaries are not ready. Each season gets its own
// timeout so one slow upstream response does not consume the next season's
// budget.
func (s Service) EnsureVenueHistory(ctx context.Context, currentSeason, stage string, count int, timeout time.Duration) (err error) {
	ctx, span := telemetry.Tracer().Start(ctx, "sync.venue_history",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("sync.season", currentSeason),
			attribute.String("sync.stage", stage),
			attribute.Int("sync.venue_history_requested_season_count", count),
		),
	)
	refreshed := 0
	recordVenueHistoryException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, span, cause, "sync.venue_history", errorType)
	}
	defer func() {
		outcome := "complete"
		if err != nil {
			outcome = "failure"
			telemetry.MarkError(span, err)
		}
		span.SetAttributes(
			attribute.Int("sync.venue_history_refreshed_season_count", refreshed),
			attribute.String("sync.venue_history.outcome", outcome),
		)
		span.End()
	}()
	store, ok := s.Store.(venueSummaryStore)
	if !ok {
		return recordVenueHistoryException(fmt.Errorf("sync store does not support venue summaries"), telemetry.ErrorTypeInvalidArgument)
	}
	seasons, err := competition.PreviousRegularSeasons(currentSeason, count)
	if err != nil {
		return recordVenueHistoryException(err, telemetry.ErrorTypeInvalidArgument)
	}
	summaries, err := store.VenueSummaries(ctx, seasons, stage)
	if err != nil {
		return recordVenueHistoryException(err, telemetry.ErrorTypeStorageFailure)
	}
	ready := make(map[string]bool, len(summaries))
	for _, summary := range summaries {
		ready[summary.Season] = summary.FixtureReady && summary.XGReady
	}
	for _, season := range seasons {
		if ready[season] {
			continue
		}
		runCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		run, runErr := s.Run(runCtx, RunOptions{Season: season, Stage: stage, Trigger: "venue_history", SourceOnly: true})
		cancel()
		if runErr != nil {
			return fmt.Errorf("sync venue history for %s: %w", season, runErr)
		}
		if run.XGError != "" || run.XGRun == nil {
			cause := fmt.Errorf("sync venue history for %s: xG summary was not refreshed: %s", season, run.XGError)
			if run.XGError == "" {
				return recordVenueHistoryException(cause, telemetry.ErrorTypeInvalidData)
			}
			return cause
		}
		refreshed++
	}
	return nil
}
