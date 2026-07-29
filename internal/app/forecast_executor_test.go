package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type telemetryTestModel struct{}

func (telemetryTestModel) Info() forecast.Info { return forecast.Info{ID: "telemetry-test-v1"} }

func (telemetryTestModel) Fit(forecast.FitInput) (forecast.Predictor, error) {
	return nil, errors.New("test model must not be fitted")
}

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

func TestPrecacheForecastsCachesZeroAssumptionResultForEachModel(t *testing.T) {
	data := testSeasonData()
	options := defaultOptions(Options{CurrentSeason: "2026", Rules: testRules(30), ForecastIterations: 20, Location: time.UTC})
	application := newApplicationWithForecastExecutor(fakeStore{season: data}, options, newForecastExecutor(1, time.Second))
	var calls atomic.Int32
	application.app.forecasts.run = func(_ context.Context, request simulation.Request) (simulation.Result, error) {
		if len(request.Fixed) != 0 {
			t.Fatalf("fixed assumptions = %#v, want none", request.Fixed)
		}
		calls.Add(1)
		return simulation.Result{Model: request.Model.Info(), Iterations: request.Iterations}, nil
	}

	if err := application.PrecacheForecasts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := calls.Load(), int32(len(forecast.Catalog())); got != want {
		t.Fatalf("simulation calls = %d, want %d", got, want)
	}

	state := forecaststate.State{Fixed: map[string]simulation.Outcome{}}
	places := playoffPlaces(options.Rules)
	for _, entry := range forecast.Catalog() {
		key := forecastResultKey(data, state, entry.Model.Info().ID, options.ForecastIterations, places)
		if _, found := application.app.forecasts.cache[key]; !found {
			t.Errorf("cache has no baseline result for %s", entry.Model.Info().ID)
		}
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

func TestForecastExecutorRecordsCalculationInputs(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	executor := newForecastExecutor(1, time.Second)
	executor.run = func(context.Context, simulation.Request) (simulation.Result, error) {
		return simulation.Result{FixedCount: 2, Remaining: 3}, nil
	}
	request := simulation.Request{
		Model:         telemetryTestModel{},
		Teams:         []standings.Team{{ID: "a"}, {ID: "b"}},
		Games:         []standings.Game{{ID: "finished", Status: standings.CompletedStatus}, {ID: "future-1", Status: simulation.RemainingStatus}, {ID: "future-2", Status: simulation.RemainingStatus}, {ID: "future-3", Status: simulation.RemainingStatus}},
		XGoals:        map[string]forecast.ExpectedGoals{"finished": {}},
		Fixed:         map[string]simulation.Outcome{"future-1": simulation.HomeWin, "future-2": simulation.Draw},
		Iterations:    50000,
		PlayoffPlaces: 8,
	}
	if _, err := executor.results(context.Background(), []forecastTask{{key: "telemetry", request: request}}); err != nil {
		t.Fatal(err)
	}

	spans := exporter.GetSpans()
	calculation := findSpan(t, spans, "forecast.simulation")
	attributes := spanAttributes(calculation)
	if got := attributes["forecast.model_id"].AsString(); got != "telemetry-test-v1" {
		t.Errorf("forecast.model_id = %q, want telemetry-test-v1", got)
	}
	for key, want := range map[string]int{
		"forecast.iteration_count":         50000,
		"forecast.team_count":              2,
		"forecast.fixture_count":           4,
		"forecast.completed_fixture_count": 1,
		"forecast.remaining_fixture_count": 3,
		"forecast.fixed_assumption_count":  2,
		"forecast.xg_observation_count":    1,
		"forecast.playoff_place_count":     8,
	} {
		if got := int(attributes[key].AsInt64()); got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}
	parent := findSpan(t, spans, "forecast.run")
	parentAttributes := spanAttributes(parent)
	modelIDs := parentAttributes["forecast.model_ids"].AsStringSlice()
	if len(modelIDs) != 1 || modelIDs[0] != "telemetry-test-v1" {
		t.Errorf("forecast.model_ids = %q, want [telemetry-test-v1]", modelIDs)
	}
	if got := parentAttributes["forecast.fixed_assumption_count"].AsInt64(); got != 2 {
		t.Errorf("parent forecast.fixed_assumption_count = %d, want 2", got)
	}
}

func findSpan(t *testing.T, spans []tracetest.SpanStub, name string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q was not recorded; spans = %#v", name, spans)
	return tracetest.SpanStub{}
}

func spanAttributes(span tracetest.SpanStub) map[string]attribute.Value {
	values := make(map[string]attribute.Value, len(span.Attributes))
	for _, value := range span.Attributes {
		values[string(value.Key)] = value.Value
	}
	return values
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
