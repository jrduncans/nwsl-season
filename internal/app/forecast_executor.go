package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jrduncans/nwsl-season/internal/simulation"
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

func (e *forecastExecutor) results(ctx context.Context, tasks []forecastTask) ([]simulation.Result, error) {
	results := make([]simulation.Result, len(tasks))
	missing := make([]int, 0, len(tasks))
	e.mu.Lock()
	for index, task := range tasks {
		if result, ok := e.cache[task.key]; ok {
			results[index] = result
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
		result, err := e.run(workCtx, tasks[index].request)
		if err != nil {
			return nil, err
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
