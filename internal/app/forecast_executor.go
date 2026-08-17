package app

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var errForecastOverloaded = errors.New("forecast capacity is currently unavailable")

const forecastResultCacheCapacity = 128

type forecastTriggerContextKey struct{}

func withForecastTrigger(ctx context.Context, trigger string) context.Context {
	if trigger == "" {
		trigger = "unspecified"
	}
	return context.WithValue(ctx, forecastTriggerContextKey{}, trigger)
}

func forecastTrigger(ctx context.Context) string {
	trigger, _ := ctx.Value(forecastTriggerContextKey{}).(string)
	if trigger == "" {
		return "unspecified"
	}
	return trigger
}

// forecastExecutor bounds expensive season simulations for the entire process.
// It intentionally does not queue work: waiting requests would still allow an
// unbounded request flood to retain goroutines and memory.
type forecastExecutor struct {
	slots   chan struct{}
	timeout time.Duration
	run     func(context.Context, simulation.Request) (simulation.Result, error)

	mu    sync.Mutex
	cache map[string]simulation.Result
}

type forecastTask struct {
	key     string
	request simulation.Request
}

func newForecastExecutor(concurrency int, timeout time.Duration) *forecastExecutor {
	return &forecastExecutor{
		slots: make(chan struct{}, concurrency), timeout: timeout, run: simulation.Run,
		cache: make(map[string]simulation.Result),
	}
}

func (e *forecastExecutor) results(ctx context.Context, tasks []forecastTask) (results []simulation.Result, err error) {
	trigger := forecastTrigger(ctx)
	spanAttributes := forecastRunAttributes(tasks)
	spanAttributes = append(spanAttributes, nwslconv.ForecastTrigger(trigger))
	ctx, span := telemetry.Tracer().Start(ctx, nwslconv.SpanForecastRun, trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(spanAttributes...),
	)
	cacheHits := 0
	outcome := nwslconv.ForecastOutcomeComputed
	defer func() {
		span.SetAttributes(nwslconv.ForecastCacheHits(cacheHits), nwslconv.ForecastCalculationCount(len(tasks)-cacheHits), nwslconv.ForecastOutcome(outcome))
		if err != nil {
			if errors.Is(err, errForecastOverloaded) {
				span.SetAttributes(nwslconv.ErrorExpected(true))
			} else if trigger != "http" {
				err = telemetry.RecordWarningWithType(ctx, span, err, nwslconv.SpanForecastRun, telemetry.ErrorTypeCalculationFailure)
			} else {
				err = telemetry.RecordErrorWithType(ctx, span, err, nwslconv.SpanForecastRun, telemetry.ErrorTypeCalculationFailure)
			}
		}
		span.End()
	}()

	results = make([]simulation.Result, len(tasks))
	missing := make([]int, 0, len(tasks))
	e.mu.Lock()
	for index, task := range tasks {
		if result, ok := e.cache[task.key]; ok {
			results[index] = result
			cacheHits++
			continue
		}
		missing = append(missing, index)
	}
	e.mu.Unlock()
	if len(missing) == 0 {
		outcome = nwslconv.ForecastOutcomeCacheHit
		return results, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		outcome = nwslconv.ForecastOutcomeOverloaded
		return nil, errForecastOverloaded
	}

	workCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	for _, index := range missing {
		request := tasks[index].request
		result, runErr := e.run(workCtx, request)
		if runErr != nil {
			if errors.Is(runErr, context.DeadlineExceeded) {
				outcome = nwslconv.ForecastOutcomeTimedOut
			} else {
				outcome = nwslconv.ForecastOutcomeFailure
			}
			return nil, runErr
		}
		results[index] = result
		e.mu.Lock()
		if len(e.cache) >= forecastResultCacheCapacity {
			// The cache is an availability optimization, not persistence. A
			// bounded, opportunistic eviction policy prevents user-controlled
			// scenarios from retaining unbounded result sets.
			for key := range e.cache {
				delete(e.cache, key)
				break
			}
		}
		e.cache[tasks[index].key] = result
		e.mu.Unlock()
	}
	return results, nil
}

func forecastRunAttributes(tasks []forecastTask) []attribute.KeyValue {
	attributes := []attribute.KeyValue{nwslconv.ForecastTaskCount(len(tasks)), nwslconv.ForecastModelIds(forecastModelIDs(tasks))}
	if len(tasks) > 0 {
		attributes = append(attributes, forecastSharedInputAttributes(tasks[0].request)...)
	}
	return attributes
}

func forecastModelIDs(tasks []forecastTask) []string {
	ids := make([]string, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task.request.Model == nil {
			continue
		}
		id := task.request.Model.Info().ID
		if id == "" {
			continue
		}
		if _, found := seen[id]; found {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func forecastSharedInputAttributes(request simulation.Request) []attribute.KeyValue {
	remaining := 0
	completed := 0
	for _, game := range request.Games {
		switch game.Status {
		case simulation.RemainingStatus:
			remaining++
		case standings.CompletedStatus:
			completed++
		}
	}
	return []attribute.KeyValue{nwslconv.ForecastIterationCount(request.Iterations), nwslconv.ForecastTeamCount(len(request.Teams)), nwslconv.ForecastFixtureCount(len(request.Games)), nwslconv.ForecastCompletedFixtureCount(completed), nwslconv.ForecastRemainingFixtureCount(remaining), nwslconv.ForecastFixedAssumptionCount(len(request.Fixed)), nwslconv.ForecastXGObservationCount(len(request.XGoals)), nwslconv.ForecastPlayoffPlaceCount(request.PlayoffPlaces)}
}
