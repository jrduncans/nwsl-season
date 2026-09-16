package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestExploreUsesOneSnapshotAndPreservesMissingXG(t *testing.T) {
	for _, path := range []string{"/explore", "/nwsl-season/explore?view=distribution", "/explore?view=table"} {
		t.Run(path, func(t *testing.T) {
			store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
				"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 19},
				"2026": {lifecycle: cache.SourceScopeActive, goals: 2, xgCovered: 20},
			})}
			response := httptest.NewRecorder()
			NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			body := response.Body.String()
			if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
				t.Fatalf("status=%d reads=%d/%d body=%s", response.Code, store.archiveCalls, store.seasonCalls, body)
			}
			for _, want := range []string{`<h1>Explore</h1>`, `data-panel="trend"`, `data-panel="distribution"`, `data-panel="table"`, `data-chart="trend"`, `data-chart="distribution"`, `src="static/explore.js"`, `src="static/vendor/chart.js-4.5.1/chart.umd.min.js"`} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			_, data, found := strings.Cut(body, `<script type="application/json" id="explore-data">`)
			data, _, _ = strings.Cut(data, "</script>")
			var records []exploreChartRecord
			if !found || json.Unmarshal([]byte(data), &records) != nil || len(records) != 2 {
				t.Fatalf("invalid chart payload: %s", data)
			}
			if records[0].Season != "2025" || records[0].XG != nil || records[0].Goals == nil || *records[0].Goals != 3 || records[1].XG == nil {
				t.Fatalf("chart payload changed missing-xG or goal semantics: %+v", records)
			}
			for _, record := range records {
				count := 0
				for _, bin := range record.Bins {
					count += bin
				}
				if count != record.Played {
					t.Errorf("%s distribution counts %d != matches %d", record.Season, count, record.Played)
				}
			}
			for _, label := range []string{"Standings", "Results &amp; fixtures", "Schedule difficulty", "Clinching scenarios", "Forecast lab", "Explore"} {
				if !strings.Contains(body, ">"+label+"</a>") {
					t.Errorf("missing main-site navigation %q", label)
				}
			}
			if !strings.Contains(body, `href="explore" aria-current="page">Explore</a>`) {
				t.Error("Explore is not the active navigation item")
			}
			for _, unwanted := range []string{"Current season", "All seasons", "data-close", "data-year-button", "data-readout", "data-bin=", "Inventory verified", "Inventory unverified", "No regular season", "selected-season-heading", "About this data", "?season="} {
				if strings.Contains(body, unwanted) {
					t.Errorf("unexpected %q", unwanted)
				}
			}
		})
	}
}

func TestExploreRouteValidation(t *testing.T) {
	handler := NewHandler(&historyHTTPStore{})
	for _, tc := range []struct {
		path     string
		status   int
		location string
	}{
		{"/explore?view=bad", http.StatusBadRequest, ""},
		{"/explore/", http.StatusSeeOther, "../explore"},
		{"/nwsl-season/explore/?view=table", http.StatusSeeOther, "../explore?view=table"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.status || response.Header().Get("Location") != tc.location {
			t.Errorf("%s: status=%d location=%q", tc.path, response.Code, response.Header().Get("Location"))
		}
	}
}
