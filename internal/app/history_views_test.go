package app

import (
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
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

func TestHistoryChartGeometryIsFiniteAndUsesCalendarSpacing(t *testing.T) {
	empty := historyChartFor("/history/scoring", nil, "")
	if empty.HasData || len(empty.Ticks) != 0 || len(empty.Marks) != 0 || empty.EmptyState == "" {
		t.Fatalf("empty chart = %+v", empty)
	}

	rate := 0.0
	chart := historyChartFor("/history/scoring", []history.SeasonScoring{{
		Season: "2019", GoalsPerMatch: &rate, Played: 20, PlotEligible: true,
		Lifecycle: cache.SourceScopeCompleted, Inventory: cache.InventoryCompletenessComplete,
	}}, "")
	if !chart.HasData || len(chart.Marks) != 1 || chart.Marks[0].Y != "306" {
		t.Fatalf("zero-baseline chart = %+v", chart)
	}
	if !containsHistoryChartLabel(chart, "2020") {
		t.Fatalf("chart omitted the 2020 gap label: %+v", chart.Labels)
	}

	equal := historyChartFor("/history/scoring", []history.SeasonScoring{
		historyChartSummary("2018", 2, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
		historyChartSummary("2019", 2, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
	}, "")
	if len(equal.Marks) != 2 || equal.Marks[0].Y != equal.Marks[1].Y || len(equal.Segments) != 1 {
		t.Fatalf("all-equal chart geometry = %+v", equal)
	}

	unequal := historyChartFor("/history/scoring", []history.SeasonScoring{
		historyChartSummary("2018", 1, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
		historyChartSummary("2021", 3, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
	}, "")
	if len(unequal.Segments) != 0 {
		t.Fatalf("nonconsecutive years have segments: %+v", unequal.Segments)
	}

	missing := historyChartFor("/history/scoring", []history.SeasonScoring{
		historyChartSummary("2019", 1, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
		historyChartSummary("2020", 2, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, false),
		historyChartSummary("2021", 3, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
	}, "")
	if len(missing.Marks) != 2 || len(missing.Segments) != 0 || !containsHistoryChartLabelNote(missing, "2020", "No regular season") {
		t.Fatalf("excluded intermediate year was bridged or unlabeled: marks=%+v segments=%+v labels=%+v", missing.Marks, missing.Segments, missing.Labels)
	}
	firstX, _ := strconv.ParseFloat(unequal.Marks[0].X, 64)
	secondX, _ := strconv.ParseFloat(unequal.Marks[1].X, 64)
	if !(secondX > firstX) {
		t.Fatalf("calendar x positions are not ascending: %v, %v", firstX, secondX)
	}
	for _, mark := range unequal.Marks {
		for _, coordinate := range []string{mark.X, mark.Y, mark.HitX, mark.HitY} {
			value, err := strconv.ParseFloat(coordinate, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("invalid mark coordinate %q: %v", coordinate, err)
			}
		}
	}
}

func TestHistoryChartMarkerAndSegmentRules(t *testing.T) {
	chart := historyChartFor("/nwsl-season/history/scoring", []history.SeasonScoring{
		historyChartSummary("2018", 1.5, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
		historyChartSummary("2019", 2.0, cache.SourceScopeCompleted, cache.InventoryCompletenessUnknown, true),
		historyChartSummary("2020", 2.5, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, false),
		historyChartSummary("2021", 2.5, cache.SourceScopeActive, cache.InventoryCompletenessUnknown, true),
	}, "2021")
	if len(chart.Marks) != 3 || chart.Marks[0].Unknown || chart.Marks[0].Active || !chart.Marks[1].Unknown || chart.Marks[1].Active || !chart.Marks[2].Active || !chart.Marks[2].Selected {
		t.Fatalf("marker rules = %+v", chart.Marks)
	}
	if len(chart.Segments) != 1 || !chart.Segments[0].Dashed {
		t.Fatalf("segment rules = %+v", chart.Segments)
	}
	if !strings.Contains(chart.Marks[2].Path, "season=2021") {
		t.Fatalf("active mark path = %q", chart.Marks[2].Path)
	}
}

func TestHistoryChartSelectionDoesNotChangeGeometryOrPopulation(t *testing.T) {
	summaries := []history.SeasonScoring{
		historyChartSummary("2018", 1, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
		historyChartSummary("2019", 2, cache.SourceScopeCompleted, cache.InventoryCompletenessComplete, true),
		historyChartSummary("2021", 3, cache.SourceScopeCompleted, cache.InventoryCompletenessIncomplete, false),
	}
	first := historyChartFor("/history/scoring", summaries, "2018")
	second := historyChartFor("/history/scoring", summaries, "2021")
	if first.ViewBox != second.ViewBox || !reflect.DeepEqual(first.Ticks, second.Ticks) || !reflect.DeepEqual(first.Labels, second.Labels) || !reflect.DeepEqual(first.Segments, second.Segments) {
		t.Fatalf("selection changed chart geometry: first=%+v second=%+v", first, second)
	}
	if len(first.Marks) != len(second.Marks) {
		t.Fatalf("selection changed point population: %d vs %d", len(first.Marks), len(second.Marks))
	}
	for index := range first.Marks {
		left, right := first.Marks[index], second.Marks[index]
		left.Selected, right.Selected = false, false
		if !reflect.DeepEqual(left, right) {
			t.Fatalf("selection changed mark %d: %+v vs %+v", index, first.Marks[index], second.Marks[index])
		}
	}
}

func TestHistoryChartAccessibleHTTPMarkup(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2019": {lifecycle: cache.SourceScopeCompleted, goals: 3},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 2},
	})}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/history/scoring?season=2019", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`<svg class="history-chart" viewBox="0 0 720 360" role="group" aria-labelledby="history-chart-title history-chart-description">`,
		`<title id="history-chart-title">Goals per completed match by season</title>`,
		`<desc id="history-chart-description">Server-rendered scoring chart`,
		`href="scoring?season=2019" aria-label="2019: 3.00 goals per completed match, 20 completed matches, Inventory unverified"`,
		`No regular season`, `width="60" height="60"`, `history-chart-mark-active-unverified`,
		`class="history-chart-year-label"`, `y="320" text-anchor="middle"`, `class="history-chart-gap-note"`, `y="348" text-anchor="middle"`,
		`class="history-chart-axis-title" x="380" y="22"`, `Inventory unverified`, `Active season`, `<details class="history-data">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("history response missing %q", want)
		}
	}
}

func historyChartSummary(season string, rate float64, lifecycle cache.SourceScopeLifecycle, inventory cache.InventoryCompleteness, eligible bool) history.SeasonScoring {
	return history.SeasonScoring{Season: season, GoalsPerMatch: &rate, Played: 20, Lifecycle: lifecycle, Inventory: inventory, PlotEligible: eligible}
}

func containsHistoryChartLabel(chart historyChartView, label string) bool {
	for _, item := range chart.Labels {
		if item.Label == label {
			return true
		}
	}
	return false
}

func containsHistoryChartLabelNote(chart historyChartView, label, note string) bool {
	for _, item := range chart.Labels {
		if item.Label == label && item.Note == note {
			return true
		}
	}
	return false
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
