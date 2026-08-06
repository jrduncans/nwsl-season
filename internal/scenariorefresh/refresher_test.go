package scenariorefresh

import (
	"context"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCalculationTelemetrySummarizesTeamSearches(t *testing.T) {
	summary := calculationTelemetry{}
	summary.recordTeamSearch(4*time.Millisecond, "fast-team", map[competition.AchievementID]scenarios.Result{
		competition.AchievementPlayoffs: {TotalAssignments: 3, Diagnostics: scenarios.Diagnostics{SearchNodes: 2}},
	})
	summary.recordTeamSearch(telemetry.SlowOperationThreshold, "slow-team", map[competition.AchievementID]scenarios.Result{
		competition.AchievementShield: {TotalAssignments: 81, CertifiedAssignments: 2, UnresolvedAssignments: 3, Diagnostics: scenarios.Diagnostics{SearchNodes: 20, OracleCalls: 4, OracleCacheHits: 5, VisitedComplete: 6}},
	})
	attributes := scenarioAttributeMap(summary.attributes([]cache.ScenarioResult{
		{Result: scenarios.Result{State: scenarios.OpportunityCanClinch}},
		{Result: scenarios.Result{State: scenarios.OpportunityUnresolved, Limitation: scenarios.LimitationBudgetExhausted}},
	}))
	for key, want := range map[string]int64{
		"scenario.team_search_count":                 2,
		"scenario.team_search.slow_count":            1,
		"scenario.assignment_count.total":            84,
		"scenario.certified_assignment_count.total":  2,
		"scenario.unresolved_assignment_count.total": 3,
		"scenario.search_node_count.total":           22,
		"scenario.search_node_count.max":             20,
		"scenario.oracle_call_count.total":           4,
		"scenario.oracle_cache_hit_count.total":      5,
		"scenario.visited_complete_count.total":      6,
		"scenario.result.state.can_clinch_count":     1,
		"scenario.result.state.unresolved_count":     1,
		"scenario.result.budget_limited_count":       1,
	} {
		if got := attributes[key].AsInt64(); got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}
	if got := attributes["scenario.team_search.slowest_team_id"].AsString(); got != "slow-team" {
		t.Errorf("slowest team = %q, want slow-team", got)
	}
	if got := attributes["scenario.search_node_count.max_team_id"].AsString(); got != "slow-team" {
		t.Errorf("most-search-nodes team = %q, want slow-team", got)
	}
}

func TestCalculateAddsTelemetryToRefreshSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	ctx, span := telemetry.Tracer().Start(context.Background(), "scenario.refresh")
	_, _ = (Refresher{}).calculate(ctx, nil, nil, cache.QualificationSnapshot{})
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "scenario.refresh" {
		t.Fatalf("spans = %#v, want only scenario.refresh", spans)
	}
	attributes := scenarioAttributeMap(spans[0].Attributes)
	if got := attributes["scenario.input_team_count"].AsInt64(); got != 0 {
		t.Errorf("scenario.input_team_count = %d, want 0", got)
	}
	if got := attributes["scenario.team_search_count"].AsInt64(); got != 0 {
		t.Errorf("scenario.team_search_count = %d, want 0", got)
	}
}

func scenarioAttributeMap(values []attribute.KeyValue) map[string]attribute.Value {
	attributes := make(map[string]attribute.Value, len(values))
	for _, value := range values {
		attributes[string(value.Key)] = value.Value
	}
	return attributes
}

func TestParseKickoffAcceptsCacheFormats(t *testing.T) {
	for _, value := range []string{"2026-11-01T22:00:00Z", "2026-11-01 22:00:00 UTC"} {
		got, err := fixtures.ParseKickoff(value)
		if err != nil {
			t.Fatalf("parseKickoff(%q): %v", value, err)
		}
		if got.UTC().Format(time.RFC3339) != "2026-11-01T22:00:00Z" {
			t.Fatalf("parseKickoff(%q) = %s", value, got.UTC().Format(time.RFC3339))
		}
	}
}

func TestShouldRetryComputeBudget(t *testing.T) {
	if !shouldRetryComputeBudget(cache.ScenarioSnapshot{Results: []cache.ScenarioResult{{Result: scenarios.Result{State: scenarios.OpportunityUnresolved, Limitation: "scenario computation budget exhausted"}}}}) {
		t.Fatal("compute-budget result should be retried")
	}
	if !shouldRetryComputeBudget(cache.ScenarioSnapshot{Results: []cache.ScenarioResult{{Result: scenarios.Result{State: scenarios.OpportunityCanClinch, Limitation: scenarios.LimitationBudgetPartial}}}}) {
		t.Fatal("partial compute-budget result should be retried")
	}
	if shouldRetryComputeBudget(cache.ScenarioSnapshot{Results: []cache.ScenarioResult{{Result: scenarios.Result{State: scenarios.OpportunityUnresolved, Limitation: "a clinch may depend on score"}}}}) {
		t.Fatal("non-budget unresolved result should not be retried")
	}
}
