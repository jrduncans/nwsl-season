package app

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
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

func TestHistoryMetricStateAndURLRoundTrip(t *testing.T) {
	summaries := []history.SeasonScoring{
		{Season: "2024", Lifecycle: cache.SourceScopeCompleted, PlotEligible: true, Played: 20},
	}
	for _, test := range []struct {
		query      string
		wantSeason string
		wantMetric historyMetric
		wantError  bool
	}{
		{query: "", wantSeason: "2024", wantMetric: historyMetricGoals},
		{query: "metric=goals", wantSeason: "2024", wantMetric: historyMetricGoals},
		{query: "metric=xg", wantSeason: "2024", wantMetric: historyMetricXG},
		{query: "metric=compare", wantSeason: "2024", wantMetric: historyMetricCompare},
		{query: "metric=", wantError: true},
		{query: "metric=other", wantError: true},
		{query: "metric=xg&metric=goals", wantError: true},
	} {
		season, metric, err := historySelectionState(historyTestURL(t, test.query), summaries)
		if test.wantError {
			if err == nil {
				t.Errorf("query %q returned no error", test.query)
			}
			continue
		}
		if err != nil || season != test.wantSeason || metric != test.wantMetric {
			t.Errorf("query %q = season %q metric %q err=%v, want %q/%q", test.query, season, metric, err, test.wantSeason, test.wantMetric)
		}
	}

	page := historyPageForMetric("/history/scoring", summaries, "2024", historyMetricXG)
	if page.FormPath != "scoring?metric=xg" {
		t.Fatalf("xG form path = %q", page.FormPath)
	}
	if len(page.MetricLinks) != 3 || page.MetricLinks[0].Path != "scoring?season=2024" || page.MetricLinks[1].Path != "scoring?metric=xg&season=2024" || page.MetricLinks[2].Path != "scoring?metric=compare&season=2024" {
		t.Fatalf("metric links = %+v", page.MetricLinks)
	}
	if page.Rows[0].Path != "scoring?metric=xg&season=2024" {
		t.Fatalf("xG row path = %q", page.Rows[0].Path)
	}
}

func TestHistoryChartScaleIsSharedAndExpandsForEligibleExtrema(t *testing.T) {
	defaultRange := []history.SeasonScoring{
		historyChartSummaryWithXG("2019", 2.2, 2.8, true),
	}
	if low, high, step := historyChartScale(defaultRange); math.Abs(low-2) > 1e-9 || math.Abs(high-3.2) > 1e-9 || math.Abs(step-0.2) > 1e-9 {
		t.Fatalf("default chart scale = (%v, %v, %v), want (2, 3.2, .2)", low, high, step)
	}

	extreme := []history.SeasonScoring{
		historyChartSummaryWithXG("2018", 0, 4, true),
		historyChartSummaryWithXG("2019", 4, 0, true),
	}
	low, high, step := historyChartScale(extreme)
	if math.Abs(low) > 1e-9 || math.Abs(high-4.8) > 1e-9 || math.Abs(step-0.8) > 1e-9 {
		t.Fatalf("expanded chart scale = (%v, %v, %v), want (0, 4.8, .8)", low, high, step)
	}
	goals := historyChartForMetric("/history/scoring", extreme, "", historyMetricGoals)
	xg := historyChartForMetric("/history/scoring", extreme, "", historyMetricXG)
	if !reflect.DeepEqual(goals.Ticks, xg.Ticks) {
		t.Fatalf("goals and xG charts do not share scale: goals=%+v xG=%+v", goals.Ticks, xg.Ticks)
	}
	if len(goals.Marks) != 2 || len(xg.Marks) != 2 || goals.Marks[0].Y == goals.Marks[1].Y || xg.Marks[0].Y == xg.Marks[1].Y {
		t.Fatalf("extreme values were clipped or collapsed: goals=%+v xG=%+v", goals.Marks, xg.Marks)
	}
}

func TestHistoryComparePageUsesIndependentGoalsAndXGPopulations(t *testing.T) {
	goals, xg := 1.5, 2.5
	partialXG := history.SeasonScoring{Season: "2018", Lifecycle: cache.SourceScopeCompleted, PlotEligible: true, Played: 20, GoalsPerMatch: &goals, XGPerMatch: nil, XGCovered: 19}
	completeXG := history.SeasonScoring{Season: "2019", Lifecycle: cache.SourceScopeCompleted, PlotEligible: true, Played: 20, GoalsPerMatch: &goals, XGPerMatch: &xg, XGCovered: 20}
	page := historyPageForMetric("/history/scoring", []history.SeasonScoring{partialXG, completeXG}, "2018", historyMetricCompare)
	if !page.Compare || page.Selected == nil || !page.Selected.Selected {
		t.Fatalf("compare page selection state = compare=%v selected=%+v", page.Compare, page.Selected)
	}
	if len(page.Chart.Marks) != 2 || len(page.ComparisonChart.Marks) != 1 || page.ComparisonChart.Marks[0].Season != "2019" {
		t.Fatalf("compare populations = goals=%+v xG=%+v", page.Chart.Marks, page.ComparisonChart.Marks)
	}
	for _, mark := range append(page.Chart.Marks, page.ComparisonChart.Marks...) {
		if mark.Path != "scoring?metric=compare&season="+mark.Season {
			t.Errorf("compare mark %s path=%q", mark.Season, mark.Path)
		}
	}
	if len(page.MetricLinks) != 3 || page.MetricLinks[2].Label != "Compare" || !page.MetricLinks[2].Selected || page.MetricLinks[2].Path != "scoring?metric=compare&season=2018" {
		t.Fatalf("compare metric links = %+v", page.MetricLinks)
	}
	if len(page.Distributions) != 2 || !page.Distributions[0].Selected || page.Distributions[1].Selected {
		t.Fatalf("distribution selection = %+v", page.Distributions)
	}
}

func TestHistoryXGChartRequiresCompleteCoverageButKeepsGoalsPopulation(t *testing.T) {
	zero := 0.0
	full := 1.0
	partial := history.SeasonScoring{Season: "2019", Lifecycle: cache.SourceScopeCompleted, PlotEligible: true, Played: 20, GoalsPerMatch: &full, XGCovered: 19}
	completeZero := history.SeasonScoring{Season: "2021", Lifecycle: cache.SourceScopeCompleted, PlotEligible: true, Played: 20, GoalsPerMatch: &full, XGCovered: 20, XGPerMatch: &zero}
	chart := historyChartForMetric("/history/scoring", []history.SeasonScoring{partial, completeZero}, "2019", historyMetricXG)
	if len(chart.Marks) != 1 || chart.Marks[0].Season != "2021" || chart.Marks[0].Y != "306" {
		t.Fatalf("xG chart marks = %+v, want only valid zero xG point", chart.Marks)
	}
	goalsChart := historyChartFor("/history/scoring", []history.SeasonScoring{partial}, "2019")
	if len(goalsChart.Marks) != 1 || goalsChart.Marks[0].Season != "2019" {
		t.Fatalf("goals chart marks = %+v, want partial-xG goals point", goalsChart.Marks)
	}
	partial.GoalBins = [5]int{1, 2, 3, 4, 10}
	page := historyPageForMetric("/history/scoring", []history.SeasonScoring{partial}, "2019", historyMetricXG)
	if len(page.Distributions) != 1 || page.Selected == nil || page.Selected.XGStatus != "xG available for 19 of 20 completed matches; a season average requires 20 of 20." {
		t.Fatalf("partial xG page = %+v", page)
	}
}

func TestHistoryDistributionUsesExactCountsAndAccessiblePercentages(t *testing.T) {
	// Repeat the five-match H02 fixture pattern four times with unique IDs.
	summary := historyDistributionSummary(t, "2024", [5]int{4, 4, 4, 4, 4})
	page := historyPageForMetric("/history/scoring", []history.SeasonScoring{summary}, "2024", historyMetricGoals)
	if len(page.Distributions) != 1 {
		t.Fatalf("distributions = %+v", page.Distributions)
	}
	distribution := page.Distributions[0]
	for _, segment := range distribution.Segments {
		if segment.Percent != "20.0%" || segment.Width != "20" || !segment.HasWidth {
			t.Errorf("segment = %+v, want exact 20%%", segment)
		}
	}
	if !strings.Contains(distribution.AccessibleName, "0 goals: 4 matches (20.0%)") || !strings.Contains(distribution.AccessibleName, "4+ goals: 4 matches (20.0%)") {
		t.Fatalf("distribution accessible name = %q", distribution.AccessibleName)
	}

	thirds := history.SeasonScoring{Season: "2025", Played: 3, PlotEligible: true, GoalBins: [5]int{1, 1, 1, 0, 0}}
	thirdPage := historyPageForMetric("/history/scoring", []history.SeasonScoring{thirds}, "2025", historyMetricGoals)
	for _, segment := range thirdPage.Distributions[0].Segments[:3] {
		if segment.Percent != "33.3%" {
			t.Errorf("third segment = %+v, want 33.3%%", segment)
		}
	}
	if thirdPage.Distributions[0].Segments[3].HasWidth || thirdPage.Distributions[0].Segments[4].HasWidth {
		t.Errorf("zero bins received visible widths: %+v", thirdPage.Distributions[0].Segments)
	}
}

func TestHistoryDistributionCoversEmptyAndMiddleBins(t *testing.T) {
	tests := []struct {
		name string
		bins [5]int
	}{
		{name: "only zero goals", bins: [5]int{20, 0, 0, 0, 0}},
		{name: "only four plus goals", bins: [5]int{0, 0, 0, 0, 20}},
		{name: "zeros in middle bins", bins: [5]int{4, 0, 4, 0, 12}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := historyDistributionSummary(t, "2024", test.bins)
			page := historyPageForMetric("/history/scoring", []history.SeasonScoring{summary}, "2024", historyMetricGoals)
			segments := page.Distributions[0].Segments
			for index, count := range test.bins {
				if segments[index].Count != count {
					t.Fatalf("bin %d count=%d, want %d", index, segments[index].Count, count)
				}
				if count == 0 && segments[index].HasWidth {
					t.Errorf("zero bin %d has visible width: %+v", index, segments[index])
				}
			}
		})
	}
}

func historyDistributionSummary(t *testing.T, season string, wantBins [5]int) history.SeasonScoring {
	t.Helper()
	entry, ok := competition.Lookup(season, "Regular Season")
	if !ok {
		t.Fatalf("catalog lacks %s regular season", season)
	}
	games := make([]cache.Game, 0, 20)
	for bin, count := range wantBins {
		home, away := int64(bin/2), int64(bin-bin/2)
		for occurrence := 0; occurrence < count; occurrence++ {
			index := len(games)
			games = append(games, cache.Game{
				ASAID:      fmt.Sprintf("distribution-%s-%02d", season, index),
				Season:     season,
				Stage:      "Regular Season",
				Status:     fixtures.CompletedStatus,
				HomeTeamID: "alpha",
				AwayTeamID: "bravo",
				HomeScore:  sql.NullInt64{Int64: home, Valid: true},
				AwayScore:  sql.NullInt64{Int64: away, Valid: true},
			})
		}
	}
	if len(games) != 20 {
		t.Fatalf("fixture sample has %d games, want 20", len(games))
	}
	summary, err := history.SummarizeScoring([]cache.HistoricalSeason{{
		Entry: entry,
		Readiness: &cache.SeasonReadinessSnapshot{
			Scope:     cache.SourceScope{Season: season, Stage: "Regular Season", Lifecycle: cache.SourceScopeCompleted, Discovery: cache.SourceScopeAvailable},
			Readiness: cache.SourceReadinessAvailable, Completeness: cache.InventoryCompletenessComplete,
		},
		Data: cache.SeasonData{Games: games},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return summary[0]
}

func historyChartSummary(season string, rate float64, lifecycle cache.SourceScopeLifecycle, inventory cache.InventoryCompleteness, eligible bool) history.SeasonScoring {
	return history.SeasonScoring{Season: season, GoalsPerMatch: &rate, Played: 20, Lifecycle: lifecycle, Inventory: inventory, PlotEligible: eligible}
}

func historyChartSummaryWithXG(season string, goals, xg float64, eligible bool) history.SeasonScoring {
	return history.SeasonScoring{Season: season, GoalsPerMatch: &goals, XGPerMatch: &xg, Played: 20, PlotEligible: eligible}
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
