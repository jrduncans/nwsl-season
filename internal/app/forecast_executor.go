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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var errForecastOverloaded = errors.New("forecast capacity is currently unavailable")

const forecastResultCacheCapacity = 128

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
	spanAttributes := forecastRunAttributes(tasks)
	ctx, span := telemetry.Tracer().Start(ctx, "forecast.run",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(spanAttributes...),
	)
	cacheHits := 0
	defer func() {
		span.SetAttributes(
			attribute.Int("forecast.cache_hits", cacheHits),
			attribute.Int("forecast.calculation_count", len(tasks)-cacheHits),
		)
		if err != nil {
			telemetry.RecordError(span, err)
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
		return results, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		return nil, errForecastOverloaded
	}

	workCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	for _, index := range missing {
		request := tasks[index].request
		calculationCtx, calculationSpan := telemetry.Tracer().Start(workCtx, "forecast.simulation",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(forecastInputAttributes(request)...),
		)
		result, runErr := e.run(calculationCtx, request)
		if runErr != nil {
			telemetry.RecordError(calculationSpan, runErr)
			calculationSpan.End()
			return nil, runErr
		}
		calculationSpan.SetAttributes(
			attribute.Int("forecast.result.fixed_assumption_count", result.FixedCount),
			attribute.Int("forecast.result.remaining_fixture_count", result.Remaining),
		)
		calculationSpan.End()
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
	attributes := []attribute.KeyValue{
		attribute.Int("forecast.task_count", len(tasks)),
		attribute.StringSlice("forecast.model_ids", forecastModelIDs(tasks)),
	}
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

func forecastInputAttributes(request simulation.Request) []attribute.KeyValue {
	attributes := forecastSharedInputAttributes(request)
	if request.Model != nil {
		attributes = append(attributes, attribute.String("forecast.model_id", request.Model.Info().ID))
	}
	return attributes
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
	return []attribute.KeyValue{
		attribute.Int("forecast.iteration_count", request.Iterations),
		attribute.Int("forecast.team_count", len(request.Teams)),
		attribute.Int("forecast.fixture_count", len(request.Games)),
		attribute.Int("forecast.completed_fixture_count", completed),
		attribute.Int("forecast.remaining_fixture_count", remaining),
		attribute.Int("forecast.fixed_assumption_count", len(request.Fixed)),
		attribute.Int("forecast.xg_observation_count", len(request.XGoals)),
		attribute.Int("forecast.playoff_place_count", request.PlayoffPlaces),
	}
}
