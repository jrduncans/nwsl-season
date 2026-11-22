package scheduler

import (
	"context"
	"time"

	"github.com/jrduncans/nwsl-season/internal/edgecache"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/trace"
)

const (
	// edgeCachePurgeSpacing keeps purges well inside Cloudflare's Free plan
	// limit for tag purges (5 a minute per account, shared with deploy
	// purges). Startup catalog batches run every few seconds; a purge they
	// need waits for a later tick instead.
	edgeCachePurgeSpacing = time.Minute
	// edgeCachePurgeTimeout bounds one purge request. The purge outlives a
	// stopping scheduler so that data committed just before shutdown still
	// reaches visitors.
	edgeCachePurgeTimeout = 10 * time.Second
)

// purgeScope is which cached pages changed data affects. Larger values cover
// smaller ones.
type purgeScope int

const (
	purgeNone purgeScope = iota
	// purgeLive covers pages that current-season data can change.
	purgeLive
	// purgeAll covers every cached page. Pages can read other seasons' data
	// (forecasts read earlier regular seasons, the archive spans every
	// season), so a change outside the current season purges everything.
	purgeAll
)

func (p purgeScope) tags() []string {
	switch p {
	case purgeLive:
		return []string{edgecache.TagLive}
	case purgeAll:
		return []string{edgecache.TagPage}
	default:
		return nil
	}
}

func (p purgeScope) attribute() string {
	if p == purgeAll {
		return nwslconv.SchedulerEdgeCachePurgeScopeAll
	}
	return nwslconv.SchedulerEdgeCachePurgeScopeLive
}

// purgeScopeFor is the scope a data change in season needs.
func (s *Scheduler) purgeScopeFor(season string) purgeScope {
	if season == s.config.Season {
		return purgeLive
	}
	return purgeAll
}

// derivedDataChanged reports whether a clinching recalculation outcome may
// have written new derived data. A partial failure still wrote the component
// that succeeded.
func derivedDataChanged(outcome string) bool {
	return outcome == nwslconv.SchedulerClinchingPreflightOutcomeComplete || outcome == nwslconv.SchedulerClinchingPreflightOutcomePartialFailure
}

// purgeEdgeCache purges the pages that a tick's changed data affects, plus
// any purge an earlier tick left pending, and records the result on the tick
// span. A purge that fails, or that would come too soon after the previous
// one, stays pending for the next tick; edge expiry bounds staleness if it
// never succeeds. Scheduled ticks and CheckNow may overlap, so the pending
// state is guarded.
func (s *Scheduler) purgeEdgeCache(ctx context.Context, tickSpan trace.Span, changed purgeScope) {
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	scope := max(s.pendingPurge, changed)
	if scope == purgeNone {
		tickSpan.SetAttributes(nwslconv.SchedulerEdgeCachePurgeOutcome(nwslconv.SchedulerEdgeCachePurgeOutcomeNotNeeded))
		return
	}
	if s.config.EdgeCache == nil {
		tickSpan.SetAttributes(nwslconv.SchedulerEdgeCachePurgeOutcome(nwslconv.SchedulerEdgeCachePurgeOutcomeNotConfigured))
		return
	}
	now := s.now()
	if !s.lastPurgeAt.IsZero() && now.Sub(s.lastPurgeAt) < edgeCachePurgeSpacing {
		s.pendingPurge = scope
		tickSpan.SetAttributes(nwslconv.SchedulerEdgeCachePurgeOutcome(nwslconv.SchedulerEdgeCachePurgeOutcomeDeferred), nwslconv.SchedulerEdgeCachePurgeScope(scope.attribute()))
		return
	}
	s.lastPurgeAt = now

	purgeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), edgeCachePurgeTimeout)
	defer cancel()
	purgeCtx, span := telemetry.Tracer().Start(purgeCtx, nwslconv.SpanSchedulerEdgeCachePurge, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(nwslconv.SchedulerEdgeCachePurgeScope(scope.attribute())))
	defer span.End()
	outcome := nwslconv.SchedulerEdgeCachePurgeOutcomeComplete
	if err := s.config.EdgeCache.PurgeTags(purgeCtx, scope.tags()); err != nil {
		outcome = nwslconv.SchedulerEdgeCachePurgeOutcomeFailed
		s.pendingPurge = scope
		_ = telemetry.RecordWarningWithType(purgeCtx, span, err, nwslconv.ErrorCodeSchedulerEdgeCachePurge, telemetry.ErrorTypeUpstreamFailure)
		s.logger.Warn("purge edge cache after changed data; will retry next tick", "tags", scope.tags(), "error", err)
	} else {
		s.pendingPurge = purgeNone
		s.logger.Info("purged edge cache after changed data", "tags", scope.tags())
	}
	span.SetAttributes(nwslconv.SchedulerEdgeCachePurgeOutcome(outcome))
	tickSpan.SetAttributes(nwslconv.SchedulerEdgeCachePurgeOutcome(outcome), nwslconv.SchedulerEdgeCachePurgeScope(scope.attribute()))
}
