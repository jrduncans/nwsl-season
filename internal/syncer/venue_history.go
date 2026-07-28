package syncer

import (
	"context"
	"fmt"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

type venueSummaryStore interface {
	VenueSummaries(context.Context, []string, string) ([]cache.VenueSummary, error)
}

// EnsureVenueHistory synchronizes only the prior regular seasons whose
// persisted fixture/xG venue summaries are not ready. Each season gets its own
// timeout so one slow upstream response does not consume the next season's
// budget.
func (s Service) EnsureVenueHistory(ctx context.Context, currentSeason, stage string, count int, timeout time.Duration) error {
	store, ok := s.Store.(venueSummaryStore)
	if !ok {
		return fmt.Errorf("sync store does not support venue summaries")
	}
	seasons, err := competition.PreviousRegularSeasons(currentSeason, count)
	if err != nil {
		return err
	}
	summaries, err := store.VenueSummaries(ctx, seasons, stage)
	if err != nil {
		return err
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
		run, runErr := s.Run(runCtx, RunOptions{Season: season, Stage: stage, SourceOnly: true})
		cancel()
		if runErr != nil {
			return fmt.Errorf("sync venue history for %s: %w", season, runErr)
		}
		if run.XGError != "" || run.XGRun == nil {
			return fmt.Errorf("sync venue history for %s: xG summary was not refreshed: %s", season, run.XGError)
		}
	}
	return nil
}
