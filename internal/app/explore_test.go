package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestExploreUsesOneSnapshotAndPreservesMissingXG(t *testing.T) {
	for _, path := range []string{"/explore", "/nwsl-season/explore?view=distribution", "/explore?view=table", "/nwsl-season/explore?view=teams", "/nwsl-season/explore?view=team-history&team=alpha"} {
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
			_, data, found = strings.Cut(body, `<script type="application/json" id="explore-team-data">`)
			data, _, _ = strings.Cut(data, "</script>")
			var seasons []exploreTeamSeason
			if !found || json.Unmarshal([]byte(data), &seasons) != nil || len(seasons) != 2 {
				t.Fatalf("invalid team payload: %s", data)
			}
			if seasons[0].Season != "2026" || len(seasons[0].Teams) != 2 || len(seasons[1].Teams) != 2 {
				t.Fatalf("unexpected team seasons: %+v", seasons)
			}
			for _, row := range seasons[0].Teams {
				if row.Played != 20 || row.XGCovered != 20 || row.Values[0].Actual != 1 || row.Values[1].Actual != 1 || row.Values[2].Actual != 0 {
					t.Fatalf("team rates: %+v", row)
				}
				for _, value := range row.Values {
					if value.Expected == nil {
						t.Fatal("complete xG missing")
					}
				}
			}
			for _, row := range seasons[1].Teams {
				for _, value := range row.Values {
					if value.Expected != nil {
						t.Fatal("partial xG presented as full-season rate")
					}
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
		{"/explore?view=teams&season=1900", http.StatusBadRequest, ""},
		{"/explore?view=teams&season=2026&season=2025", http.StatusBadRequest, ""},
		{"/explore?view=teams&season=", http.StatusBadRequest, ""},
		{"/explore?view=teams&measure=bad", http.StatusBadRequest, ""},
		{"/explore?view=teams&measure=for&measure=against", http.StatusBadRequest, ""},
		{"/explore?view=teams&display=bad", http.StatusBadRequest, ""},
		{"/explore?view=teams&team-sort=bad", http.StatusBadRequest, ""},
		{"/explore?view=teams&team-order=bad", http.StatusBadRequest, ""},
		{"/explore?view=team-history&team=unknown", http.StatusBadRequest, ""},
		{"/explore?view=team-history&team=", http.StatusBadRequest, ""},
		{"/explore?view=team-history&team=alpha&team=bravo", http.StatusBadRequest, ""},
		{"/explore?view=team-history&measure=bad", http.StatusBadRequest, ""},
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

func TestExploreTeamTableSortsUnroundedValuesAndKeepsMissingLast(t *testing.T) {
	a, b, zero := 1.0004, 1.0001, 0.0
	rows := []exploreTeamRecord{
		{ID: "a", Name: "Alpha", Played: 4, Values: [3]exploreTeamValues{{Actual: 2, Expected: &a}}},
		{ID: "c", Name: "Missing", Played: 3, Values: [3]exploreTeamValues{{Actual: 3}}},
		{ID: "b", Name: "Bravo", Played: 2, Values: [3]exploreTeamValues{{Actual: 2, Expected: &b}}},
		{ID: "d", Name: "Zero", Played: 1, Values: [3]exploreTeamValues{{Actual: 0, Expected: &zero}}},
	}
	for _, tc := range []struct{ column, order, want string }{
		{"for-gap", "desc", "badc"}, {"for-gap", "asc", "dabc"},
		{"for-expected", "asc", "dbac"}, {"for-expected", "desc", "abdc"},
		{"for-actual", "desc", "cabd"}, {"played", "desc", "acbd"},
		{"name", "asc", "abcd"}, {"name", "desc", "dcba"},
	} {
		t.Run(tc.column+"/"+tc.order, func(t *testing.T) {
			copyRows := slices.Clone(rows)
			sortExploreTeams(copyRows, tc.column, tc.order)
			var got string
			for _, row := range copyRows {
				got += row.ID
			}
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestExploreTeamViewsKeepSelectionAndWarnOnlyForMissingXG(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 3, xgCovered: 19},
	})
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, season := range []string{"2025", "2026"} {
		query := url.Values{"season": {season}, "measure": {"difference"}, "display": {"table"}}
		page, err := exploreTeams(query, summaries, archive)
		if err != nil {
			t.Fatal(err)
		}
		if page.TeamMissingXG != (season == "2026") || page.TeamSort != "difference-gap" || page.TeamOrder != "desc" || page.TeamDisplay != "table" {
			t.Fatalf("unexpected selection/warning: %+v", page)
		}
		link, err := url.Parse(page.TeamChartURL)
		if err != nil || link.Query().Get("season") != season || link.Query().Get("measure") != "difference" || link.Query().Get("display") != "chart" {
			t.Fatalf("chart link lost selection: %s", page.TeamChartURL)
		}
		if page.TeamRows[0].Team.LogoURL != clubLogoURL(page.TeamRows[0].Team.ID) {
			t.Fatal("team logo missing")
		}
		if season == "2025" && page.TeamRows[0].Team.ID != "bravo" {
			t.Fatal("table is not initially sorted by descending differential gap")
		}
		for _, column := range page.TeamColumns {
			link, err := url.Parse(column.URL)
			if err != nil || link.Query().Get("season") != season || link.Query().Get("measure") != "difference" || link.Query().Get("team-sort") != column.Key {
				t.Fatalf("sort link lost selection: %s", column.URL)
			}
		}
	}
}

func TestExploreUnifiedTableIsIndependentOfChartMeasure(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
	})
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	var baseline []exploreTeamRow
	for _, measure := range []string{"", "for", "against", "difference"} {
		query := url.Values{"display": {"table"}}
		if measure != "" {
			query.Set("measure", measure)
		}
		page, err := exploreTeams(query, summaries, archive)
		if err != nil {
			t.Fatal(err)
		}
		if measure == "" && page.TeamMeasure != "difference" {
			t.Fatal("chart does not default to goal differential")
		}
		if page.TeamSort != "difference-gap" || len(page.TeamColumns) != 11 || len(page.TeamRows) != 2 {
			t.Fatalf("unexpected unified table: %+v", page)
		}
		if baseline == nil {
			baseline = page.TeamRows
			want := []exploreTeamRowValues{
				{Actual: "1.00", Expected: "-1.00", Gap: "+2.00"},
				{Actual: "2.00", Expected: "0.00", Gap: "+2.00"},
				{Actual: "1.00", Expected: "1.00", Gap: "+0.00"},
			}
			if baseline[0].Team.ID != "bravo" || !reflect.DeepEqual(baseline[0].Values, want) {
				t.Fatalf("incorrect table group values: %+v", baseline[0])
			}
		} else if !reflect.DeepEqual(page.TeamRows, baseline) {
			t.Fatalf("chart measure %q changed table rows", measure)
		}
	}
	legacy, err := exploreTeams(url.Values{"measure": {"against"}, "team-sort": {"gap"}}, summaries, archive)
	if err != nil || legacy.TeamSort != "against-gap" || legacy.TeamRows[0].Team.ID != "alpha" {
		t.Fatalf("legacy sort lost its metric: %+v, %v", legacy, err)
	}
}
