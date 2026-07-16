package scenariorefresh

import (
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func TestParseKickoffAcceptsCacheFormats(t *testing.T) {
	for _, value := range []string{"2026-11-01T22:00:00Z", "2026-11-01 22:00:00 UTC"} {
		got, err := parseKickoff(value)
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
	if shouldRetryComputeBudget(cache.ScenarioSnapshot{Results: []cache.ScenarioResult{{Result: scenarios.Result{State: scenarios.OpportunityUnresolved, Limitation: "a clinch may depend on score"}}}}) {
		t.Fatal("non-budget unresolved result should not be retried")
	}
}
