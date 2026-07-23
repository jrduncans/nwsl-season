package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func TestForecastExecutorCachesSuccessfulResults(t *testing.T) {
	executor := newForecastExecutor(1, time.Second)
	var calls atomic.Int32
	executor.run = func(context.Context, simulation.Request) (simulation.Result, error) {
		calls.Add(1)
		return simulation.Result{Iterations: 7}, nil
	}
	task := forecastTask{key: "fixture-snapshot/model/fixed"}

	for range 2 {
		results, err := executor.results(context.Background(), []forecastTask{task})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Iterations != 7 {
			t.Fatalf("results = %+v, want cached simulation result", results)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("simulation calls = %d, want 1", got)
	}
}

func TestForecastExecutorRejectsWorkWhenCapacityIsFull(t *testing.T) {
	executor := newForecastExecutor(1, time.Second)
	started := make(chan struct{})
	release := make(chan struct{})
	executor.run = func(context.Context, simulation.Request) (simulation.Result, error) {
		close(started)
		<-release
		return simulation.Result{}, nil
	}
	finished := make(chan error, 1)
	go func() {
		_, err := executor.results(context.Background(), []forecastTask{{key: "first"}})
		finished <- err
	}()
	<-started
	_, err := executor.results(context.Background(), []forecastTask{{key: "second"}})
	if !errors.Is(err, errForecastOverloaded) {
		t.Fatalf("error = %v, want overload", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestForecastExecutorUsesAComputationDeadline(t *testing.T) {
	executor := newForecastExecutor(1, 5*time.Millisecond)
	executor.run = func(ctx context.Context, _ simulation.Request) (simulation.Result, error) {
		<-ctx.Done()
		return simulation.Result{}, ctx.Err()
	}
	_, err := executor.results(context.Background(), []forecastTask{{key: "slow"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestForecastExecutorBoundsItsResultCache(t *testing.T) {
	executor := newForecastExecutor(1, time.Second)
	executor.run = func(context.Context, simulation.Request) (simulation.Result, error) { return simulation.Result{}, nil }
	for index := 0; index <= forecastResultCacheCapacity; index++ {
		if _, err := executor.results(context.Background(), []forecastTask{{key: string(rune(index))}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(executor.cache); got != forecastResultCacheCapacity {
		t.Fatalf("cache entries = %d, want %d", got, forecastResultCacheCapacity)
	}
}

func TestForecastHandlerReturns429WhenForecastCapacityIsFull(t *testing.T) {
	options := defaultOptions(Options{Rules: testRules(30), ForecastIterations: 20, ForecastConcurrency: 1, Location: time.UTC})
	application := &application{store: fakeStore{season: testSeasonData()}, options: options, forecasts: newForecastExecutor(1, time.Second)}
	application.forecasts.slots <- struct{}{}
	defer func() { <-application.forecasts.slots }()

	response := httptest.NewRecorder()
	application.forecast(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast", nil))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}

func TestForecastHandlerReturns503WhenForecastTimesOut(t *testing.T) {
	options := defaultOptions(Options{Rules: testRules(30), ForecastIterations: 20, ForecastTimeout: 5 * time.Millisecond, Location: time.UTC})
	executor := newForecastExecutor(options.ForecastConcurrency, options.ForecastTimeout)
	executor.run = func(ctx context.Context, _ simulation.Request) (simulation.Result, error) {
		<-ctx.Done()
		return simulation.Result{}, ctx.Err()
	}
	application := &application{store: fakeStore{season: testSeasonData()}, options: options, forecasts: executor}

	response := httptest.NewRecorder()
	application.forecast(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestForecastCapacityDoesNotBlockOrdinaryRoutes(t *testing.T) {
	options := Options{Rules: testRules(30), ForecastIterations: 20, ForecastConcurrency: 1, Location: time.UTC}
	executor := newForecastExecutor(1, time.Second)
	executor.slots <- struct{}{}
	defer func() { <-executor.slots }()
	handler := newHandlerWithForecastExecutor(fakeStore{season: testSeasonData()}, options, executor)

	for _, path := range []string{"/healthz", "/cache/status", "/seasons/2026"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want %d", path, response.Code, http.StatusOK)
		}
	}
}
