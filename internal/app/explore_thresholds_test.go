package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestExploreThresholdsUsesOneArchiveAndNativeDetails(t *testing.T) {
	store := &archiveHTTPStore{archive: apptest.Scenario(t, apptest.ScenarioOverview)}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=thresholds&threshold-season=2022&penalties=exclude", nil))
	if response.Code != 200 {
		t.Fatalf("%d: %s", response.Code, response.Body.String())
	}
	if store.archiveCalls != 1 || store.seasonCalls != 0 || store.statusCalls != 0 {
		t.Fatal("not one coherent archive read")
	}
	body := response.Body.String()
	requireText(t, body, "[data-panel=thresholds]", "since 2016", "no byes", "records not loaded")
	requireAttr(t, body, `[data-threshold-year="2022"]`, "aria-current", "true")
	requireAttr(t, body, `[data-threshold-detail="2025"]`, "hidden", "")
	forbidText(t, body, "[data-panel=thresholds]", "Expected points", "xPoints incomplete")
	if strings.Contains(text(find(t, body, "[data-panel=thresholds]")[0]), "Penalties Include") {
		t.Fatal("penalties control in points-only view")
	}
	for _, query := range []string{"threshold-season=", "threshold-season=2020", "threshold-season=2026", "threshold-season=2022&threshold-season=2023", "threshold-outcome=bogus", "threshold-sort=bogus", "threshold-order=up"} {
		status, _ := getPage(t, store.archive, "/explore?view=thresholds&"+query)
		if status != http.StatusBadRequest {
			t.Errorf("%s: %d", query, status)
		}
	}
}

func TestExploreThresholdChartUsesUnroundedValuesAndGaps(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioThresholds)
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	page, err := exploreThresholds(url.Values{}, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	if page.ThresholdSelected != "2025" {
		t.Fatal(page)
	}
	// Payload ratios are the very same values as the native table/details.
	if len(page.ThresholdData) != len(page.ThresholdRows) {
		t.Fatal("payload seasons drift")
	}
	for i, row := range page.ThresholdRows {
		for j, value := range row.Values {
			if value.PPG != page.ThresholdData[i].Values[j].PPG {
				t.Fatal("chart drift")
			}
		}
	}
}

func TestExploreThresholdsRecordedArchiveReconcilesAllCutoffs(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioThresholds)
	summary, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	page, err := exploreThresholds(url.Values{}, summary, archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.ThresholdRows {
		for _, value := range row.Values {
			if value.Applicable && value.PPG == nil {
				t.Errorf("%s %s: %s", row.Season, value.Label, value.Reason)
			}
		}
	}
	for _, path := range []string{"/explore?view=thresholds&threshold-season=2023", "/explore?view=thresholds&threshold-season=2023&penalties=exclude"} {
		status, body := getPage(t, archive, path)
		if status != 200 {
			t.Fatalf("%d", status)
		}
		requireText(t, body, `[data-threshold-detail="2023"]`, "Gotham", "Orlando Pride", "31 pts", "Equal-points tie across the boundary")
		requireAttr(t, body, `canvas[data-chart="thresholds"]`, "tabindex", "0")
		if len(find(t, body, `[data-panel="thresholds"] svg`)) > 0 {
			t.Fatal("Explore trend must use Chart.js")
		}
	}
}

func TestExploreThresholdTableFiltersAndSortsOneOutcome(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioThresholds)
	summary, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"shield", "home", "bye", "playoffs"} {
		for _, order := range []string{"asc", "desc"} {
			query := url.Values{"threshold-outcome": {outcome}, "threshold-sort": {"ppg"}, "threshold-order": {order}}
			page, err := exploreThresholds(query, summary, archive)
			if err != nil {
				t.Fatal(err)
			}
			var values []float64
			for _, row := range page.ThresholdTableRows {
				if row.Hidden {
					continue
				}
				if row.Outcome != outcome || row.PPG == nil {
					t.Fatal("mixed or empty outcome row")
				}
				values = append(values, *row.PPG)
				if !strings.Contains(row.URL, "threshold-outcome="+outcome) {
					t.Fatal("native season link loses outcome")
				}
			}
			want := 9
			if outcome == "bye" {
				want = 3
			}
			if len(values) != want {
				t.Fatalf("%s: got %d rows, want %d", outcome, len(values), want)
			}
			for i := 1; i < len(values); i++ {
				if order == "asc" && values[i] < values[i-1] || order == "desc" && values[i] > values[i-1] {
					t.Fatal("incorrect PPG ordering", values)
				}
			}
			status, body := getPage(t, archive, "/explore?view=thresholds&"+query.Encode())
			if status != 200 {
				t.Fatal(status)
			}
			got := 0
			for _, row := range find(t, body, "[data-threshold-table-rows] tr") {
				if _, hidden := lookupAttr(row, "hidden"); !hidden {
					got++
				}
			}
			if got != want {
				t.Fatalf("native %s rows: %d", outcome, got)
			}

			requireAttr(t, body, "[data-threshold-table]", "open", "")
		}
	}
}
