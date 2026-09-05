package app

import (
	"net/url"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestHistoryViewsFormatUnavailableAndLifecycleContext(t *testing.T) {
	page := historyPageFor("/history/scoring", []history.SeasonScoring{
		{Season: "2016", Readiness: cache.SourceReadinessUnknown, Lifecycle: cache.SourceScopeActive, Inventory: cache.InventoryCompletenessUnknown, Played: 0, Exclusions: []string{"source_unavailable", "below_minimum_matches"}},
		{Season: "2026", Readiness: cache.SourceReadinessAvailable, Lifecycle: cache.SourceScopeActive, Inventory: cache.InventoryCompletenessIncomplete, Played: 7, TotalGoals: 0, GoalsPerMatch: historyRate(0), Exclusions: []string{"inventory_incomplete", "below_minimum_matches"}},
	}, "2026")
	if page.Selected == nil || page.Selected.GoalsPerMatch != "0.00" || page.Selected.Status != "Active through 7 matches" || page.Selected.Inventory != "Known fixture inventory incomplete" {
		t.Fatalf("selected history row = %+v", page.Selected)
	}
	if page.Rows[0].GoalsPerMatch != "Unavailable" || page.Rows[0].Inventory != "Inventory unavailable" || page.Rows[0].Status != "Active through 0 matches; Source data unavailable" {
		t.Fatalf("unavailable row = %+v", page.Rows[0])
	}
	if page.Rows[1].Exclusions != "known fixture inventory incomplete; fewer than 20 completed, valid matches" {
		t.Fatalf("exclusions=%q", page.Rows[1].Exclusions)
	}
	if len(page.ExcludedSeasons) != 2 || page.ExcludedSeasons[0].Season != "2016" || page.ExcludedSeasons[0].Reason != "source data unavailable; fewer than 20 completed, valid matches" {
		t.Fatalf("excluded seasons = %+v", page.ExcludedSeasons)
	}
}

func TestHistorySelectionHierarchy(t *testing.T) {
	completed, active := "2024", "2026"
	tests := []struct {
		name, query, want string
		rows              []history.SeasonScoring
	}{
		{"completed eligible", "", completed, []history.SeasonScoring{{Season: active, Lifecycle: cache.SourceScopeActive, PlotEligible: true}, {Season: completed, Lifecycle: cache.SourceScopeCompleted, PlotEligible: true}}},
		{"active eligible", "", active, []history.SeasonScoring{{Season: active, Lifecycle: cache.SourceScopeActive, PlotEligible: true}}},
		{"scored fallback", "", completed, []history.SeasonScoring{{Season: completed, Played: 1}}},
		{"no selection", "", "", []history.SeasonScoring{{Season: completed}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := historySelection(historyTestURL(t, test.query), test.rows)
			if err != nil || value != test.want {
				t.Fatalf("selection=%q err=%v, want %q", value, err, test.want)
			}
		})
	}
}

func historyRate(value float64) *float64 { return &value }

func historyTestURL(t *testing.T, query string) *url.URL {
	t.Helper()
	value, err := url.Parse("/history/scoring?" + query)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
