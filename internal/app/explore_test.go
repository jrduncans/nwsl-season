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
	for _, path := range []string{"/explore", "/nwsl-season/explore?view=trend&metric=gap", "/nwsl-season/explore?view=distribution", "/explore?view=table", "/nwsl-season/explore?view=teams", "/nwsl-season/explore?view=teams&display=gap", "/nwsl-season/explore?view=teams&display=scatter", "/nwsl-season/explore?view=team-history&team=alpha"} {
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
			for _, want := range []string{`<h1>Explore</h1>`, `data-panel="trend"`, `data-panel="distribution"`, `data-panel="table"`, `data-chart="trend"`, `data-chart="distribution"`, `data-team-scatter-logos checked`, `<option value="gap">Goals − xG</option>`, `src="static/explore.js"`, `src="static/vendor/chart.js-4.5.1/chart.umd.min.js"`} {
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
			if records[0].Season != "2025" || records[0].XG != nil || records[0].Gap != nil || records[0].Active || records[0].Goals == nil || *records[0].Goals != 3 || records[1].XG == nil || records[1].Gap == nil || !records[1].Active {
				t.Fatalf("chart payload changed missing-xG or goal semantics: %+v", records)
			}
			if *records[1].Gap != *records[1].Goals-*records[1].XG {
				t.Fatalf("chart gap does not match goals minus xG: %+v", records[1])
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
				for _, value := range row.Values[:3] {
					if value.Expected == nil {
						t.Fatal("complete xG missing")
					}
				}
			}
			for _, row := range seasons[1].Teams {
				for _, value := range row.Values[:3] {
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

func TestExploreDistributionValuesMatchEligibleChartSeasons(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2019": {lifecycle: cache.SourceScopeCompleted, inventory: cache.InventoryCompletenessIncomplete, goals: 0},
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 4},
	})}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=distribution", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	_, values, found := strings.Cut(response.Body.String(), `<details class="explore-context-details" data-distribution-values>`)
	values, _, _ = strings.Cut(values, `</details>`)
	if !found || !strings.Contains(values, `<summary>Distribution values</summary>`) || !strings.Contains(values, `<table class="explore-table explore-distribution-table">`) || !strings.Contains(values, `<caption>Count (share of matches).</caption>`) {
		t.Fatalf("server-rendered distribution values missing: %s", values)
	}
	for _, row := range []string{
		`<tr data-matches="20"><th scope="row" data-value="2025">2025</th><td data-value="20">20</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="20">20 (100.0%)</td><td data-value="0">0 (0.0%)</td></tr>`,
		`<tr data-matches="20"><th scope="row" data-value="2026">2026</th><td data-value="20">20</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="20">20 (100.0%)</td></tr>`,
	} {
		if !strings.Contains(values, row) {
			t.Errorf("missing distribution row %q", row)
		}
	}
	if strings.Contains(values, `data-value="2019"`) {
		t.Error("ineligible season appears in distribution values")
	}
}

func TestExploreDistributionSortUsesExactShareAndNativeLinks(t *testing.T) {
	rows := []historyDistributionView{
		{Season: "2023", Total: 4, GoalsEligible: true, Segments: []historyDistributionSegmentView{{Count: 1}}},
		{Season: "2024", Total: 3, GoalsEligible: true, Segments: []historyDistributionSegmentView{{Count: 1}}},
		{Season: "2025", Total: 1000, GoalsEligible: true, Segments: []historyDistributionSegmentView{{Count: 333}}},
		{Season: "2026", Total: 3, GoalsEligible: false, Segments: []historyDistributionSegmentView{{Count: 3}}},
	}
	for _, tc := range []struct {
		column, order string
		want          []string
	}{
		{"bin-0", "desc", []string{"2024", "2025", "2023"}},
		{"bin-0", "asc", []string{"2023", "2025", "2024"}},
		{"matches", "desc", []string{"2025", "2023", "2024"}},
		{"season", "desc", []string{"2025", "2024", "2023"}},
	} {
		t.Run(tc.column+"/"+tc.order, func(t *testing.T) {
			page, err := exploreDistribution(url.Values{"distribution-sort": {tc.column}, "distribution-order": {tc.order}}, rows)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(page.DistributionRows))
			for _, row := range page.DistributionRows {
				got = append(got, row.Season)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("sorted seasons = %v, want %v", got, tc.want)
			}
			if !page.DistributionExpanded {
				t.Error("sorted direct URL should show the values table")
			}
			for _, column := range page.DistributionColumns {
				if column.Key != tc.column {
					continue
				}
				link, err := url.Parse(column.URL)
				if err != nil || link.Query().Get("distribution-sort") != tc.column || link.Query().Get("distribution-order") == tc.order || link.Query().Get("view") != "distribution" {
					t.Fatalf("sort link did not toggle order: %s", column.URL)
				}
			}
		})
	}
}

func TestExploreDistributionBinSelection(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3},
	})}
	for _, bin := range []string{"all", "0", "1", "2", "3", "4"} {
		t.Run(bin, func(t *testing.T) {
			response := httptest.NewRecorder()
			path := "/explore?view=distribution&distribution-bin=" + bin
			NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `<option value="`+bin+`" selected`) {
				t.Errorf("selected bin %q is missing from the rendered control", bin)
			}
			if bin == "1" && !strings.Contains(response.Body.String(), "Share of regular-season matches with 1 goal</h2>") {
				t.Error("one-goal chart heading is missing")
			}
			if !strings.Contains(response.Body.String(), `<details class="explore-context-details" data-distribution-values>`) {
				t.Error("selecting a bin alone opened the distribution values table")
			}
		})
	}

	rows := []historyDistributionView{{Season: "2025", Total: 20, GoalsEligible: true}}
	defaultPage, err := exploreDistribution(nil, rows)
	if err != nil || defaultPage.DistributionBin != "all" || defaultPage.DistributionExpanded {
		t.Fatalf("default selection = %+v, error = %v", defaultPage, err)
	}
	page, err := exploreDistribution(url.Values{"distribution-bin": {"3"}}, rows)
	if err != nil || page.DistributionBin != "3" || page.DistributionExpanded {
		t.Fatalf("bin selection = %+v, error = %v", page, err)
	}
	for _, column := range page.DistributionColumns {
		link, parseErr := url.Parse(column.URL)
		if parseErr != nil || link.Query().Get("distribution-bin") != "3" {
			t.Fatalf("sort link lost selected bin: %s", column.URL)
		}
	}
}

func TestExploreDistributionSortedURLRendersOpenSortableTable(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 4},
	})}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=distribution&distribution-sort=bin-3&distribution-order=asc", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	_, values, found := strings.Cut(response.Body.String(), `<details class="explore-context-details" data-distribution-values open>`)
	values, _, _ = strings.Cut(values, `</details>`)
	if !found || !strings.Contains(values, `data-distribution-sort="bin-3" aria-label="Sort by share of matches with 3 goals"`) ||
		!strings.Contains(values, `aria-sort="ascending"><a href=`) {
		t.Fatalf("sorted table is not open with sortable headers: %s", values)
	}
	newer := strings.Index(values, `data-value="2026"`)
	older := strings.Index(values, `data-value="2025"`)
	if newer < 0 || older < 0 || newer > older {
		t.Fatalf("ascending 3-goal share did not place zero-share season first: %s", values)
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
		{"/explore?view=teams&units=bad", http.StatusBadRequest, ""},
		{"/explore?view=teams&units=", http.StatusBadRequest, ""},
		{"/explore?view=teams&units=total&units=per-match", http.StatusBadRequest, ""},
		{"/explore?view=teams&display=bad", http.StatusBadRequest, ""},
		{"/explore?view=teams&display=gap&display=chart", http.StatusBadRequest, ""},
		{"/explore?view=teams&display=scatter&display=chart", http.StatusBadRequest, ""},
		{"/explore?view=teams&team-sort=bad", http.StatusBadRequest, ""},
		{"/explore?view=teams&team-order=bad", http.StatusBadRequest, ""},
		{"/explore?view=team-history&team=unknown", http.StatusBadRequest, ""},
		{"/explore?view=team-history&team=", http.StatusBadRequest, ""},
		{"/explore?view=team-history&team=alpha&team=bravo", http.StatusBadRequest, ""},
		{"/explore?view=team-history&measure=bad", http.StatusBadRequest, ""},
		{"/explore?distribution-sort=bad", http.StatusBadRequest, ""},
		{"/explore?distribution-sort=", http.StatusBadRequest, ""},
		{"/explore?distribution-sort=bin-0&distribution-sort=bin-1", http.StatusBadRequest, ""},
		{"/explore?distribution-order=bad", http.StatusBadRequest, ""},
		{"/explore?distribution-bin=", http.StatusBadRequest, ""},
		{"/explore?distribution-bin=5", http.StatusBadRequest, ""},
		{"/explore?distribution-bin=all&distribution-bin=1", http.StatusBadRequest, ""},
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
		{ID: "a", Name: "Alpha", Played: 4, Values: [4]exploreTeamValues{{Actual: 2, Expected: &a}}},
		{ID: "c", Name: "Missing", Played: 3, Values: [4]exploreTeamValues{{Actual: 3}}},
		{ID: "b", Name: "Bravo", Played: 2, Values: [4]exploreTeamValues{{Actual: 2, Expected: &b}}},
		{ID: "d", Name: "Zero", Played: 1, Values: [4]exploreTeamValues{{Actual: 0, Expected: &zero}}},
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

func TestExplorePointsUseIndependentCoverageAndSelectedUnits(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
	})
	for index := range archive[0].Data.XGoals {
		observation := &archive[0].Data.XGoals[index]
		observation.HomeXPoints.Float64, observation.HomeXPoints.Valid = 1.4, true
		observation.AwayXPoints.Float64, observation.AwayXPoints.Valid = 1.6, true
	}
	// A missing xG observation must not remove independently covered xPts.
	archive[0].Data.XGoals[0].HomeXG.Valid = false
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, units := range []string{"per-match", "total"} {
		page, err := exploreTeams(url.Values{"measure": {"points"}, "units": {units}, "display": {"table"}, "team-sort": {"points-expected"}}, summaries, archive)
		if err != nil {
			t.Fatal(err)
		}
		if page.TeamUnits != units || page.TeamActualLabel != "Points" || page.TeamXGLabel != "xPts" || !page.TeamMissingXG || page.TeamMissingXPoints {
			t.Fatalf("points coverage or labels wrong: %+v", page)
		}
		if got := page.TeamSeasons[0].Teams[0]; len(got.Values) != 4 || got.XPointsCovered != 20 || got.Values[3].Expected == nil || got.Totals[3].Expected == nil {
			t.Fatalf("points missing from JSON record: %+v", got)
		}
		if page.TeamRows[0].Team.ID != "bravo" {
			t.Fatalf("points-expected sort did not use xPts: %+v", page.TeamRows)
		}
		want := exploreTeamRowValues{Actual: "3.00", Expected: "1.60", Gap: "+1.40"}
		if units == "total" {
			want = exploreTeamRowValues{Actual: "60", Expected: "32.00", Gap: "+28.00"}
		}
		if got := page.TeamRows[0].Values[3]; got != want {
			t.Fatalf("%s points row = %+v, want %+v", units, got, want)
		}
		if !strings.Contains(page.TeamTableURL, "units="+units) {
			t.Fatalf("units lost from comparison link: %s", page.TeamTableURL)
		}
	}
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/explore?view=teams&display=table&measure=points&units=total&team-sort=points-expected&team-order=desc", []string{`<option value="points" selected>Points</option>`, `<option value="total" selected>Totals</option>`, `data-team-sort="points-expected"`, `<td>60</td><td>32.00</td><td><strong>`}},
		{"/explore?view=team-history&team=alpha&measure=points&series=xg&context=on&history-sort=points-expected", []string{`<option value="points" selected>Points</option>`, `<option value="xg" selected>xPts</option>`, `data-history-sort="points-expected"`, `<h4>xPts</h4>`}},
	} {
		store := &historyHTTPStore{archive: archive}
		response := httptest.NewRecorder()
		NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
			t.Fatalf("%s: status=%d archive=%d season=%d", tc.path, response.Code, store.archiveCalls, store.seasonCalls)
		}
		for _, fragment := range tc.want {
			if !strings.Contains(response.Body.String(), fragment) {
				t.Errorf("%s: missing %q", tc.path, fragment)
			}
		}
	}
	missingPoints := historyArchive(t, map[string]historyArchiveState{
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
	})
	for index := range missingPoints[0].Data.XGoals {
		observation := &missingPoints[0].Data.XGoals[index]
		observation.HomeXPoints.Float64, observation.HomeXPoints.Valid = 1.4, true
		observation.AwayXPoints.Float64, observation.AwayXPoints.Valid = 1.6, true
	}
	missingPoints[0].Data.XGoals[0].HomeXPoints.Valid = false
	missingSummaries, err := history.SummarizeScoring(missingPoints)
	if err != nil {
		t.Fatal(err)
	}
	page, err := exploreTeams(url.Values{"measure": {"points"}}, missingSummaries, missingPoints)
	if err != nil || page.TeamMissingXG || !page.TeamMissingXPoints {
		t.Fatalf("xPts-only missing data was not independent of xG: %+v, %v", page, err)
	}
	historyPage, err := exploreTeamHistory(url.Values{"team": {"alpha"}, "measure": {"points"}}, page.TeamSeasons)
	if err != nil || historyPage.TeamHistoryMissingXG || !historyPage.TeamHistoryMissingXPoints {
		t.Fatalf("history xPts warning was not independent of xG: %+v, %v", historyPage, err)
	}
}

func TestExploreTeamTotalSortUsesTotalsAndKeepsMissingLast(t *testing.T) {
	x1, x2 := 1.0, 4.5
	t1, t2 := 1.0, 45.0
	rows := []exploreTeamRecord{
		{ID: "a", Name: "Alpha", Played: 1, Values: [4]exploreTeamValues{{}, {}, {}, {Actual: 2, Expected: &x1}}, Totals: [4]exploreTeamValues{{}, {}, {}, {Actual: 2, Expected: &t1}}},
		{ID: "b", Name: "Bravo", Played: 10, Values: [4]exploreTeamValues{{}, {}, {}, {Actual: 5, Expected: &x2}}, Totals: [4]exploreTeamValues{{}, {}, {}, {Actual: 50, Expected: &t2}}},
		{ID: "c", Name: "Charlie", Played: 2, Values: [4]exploreTeamValues{{}, {}, {}, {Actual: 3}}, Totals: [4]exploreTeamValues{{}, {}, {}, {Actual: 6}}},
	}
	for _, tc := range []struct{ units, order, want string }{
		{"per-match", "desc", "abc"},
		{"total", "desc", "bac"},
		{"total", "asc", "abc"},
	} {
		copyRows := slices.Clone(rows)
		sortExploreTeams(copyRows, "points-gap", tc.order, tc.units)
		got := ""
		for _, row := range copyRows {
			got += row.ID
		}
		if got != tc.want {
			t.Errorf("%s/%s sorted %s, want %s", tc.units, tc.order, got, tc.want)
		}
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

func TestExploreTeamGapDisplayPreservesSelection(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 3, xgCovered: 19},
	})
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"season":     {"2025"},
		"measure":    {"against"},
		"display":    {"gap"},
		"team-sort":  {"for-expected"},
		"team-order": {"asc"},
	}
	page, err := exploreTeams(query, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	if page.TeamDisplay != "gap" || page.TeamSeason != "2025" || page.TeamMeasure != "against" || page.TeamSort != "for-expected" || page.TeamOrder != "asc" {
		t.Fatalf("gap view lost selection: %+v", page)
	}
	for _, tc := range []struct{ name, link, display string }{
		{"paired-dot chart", page.TeamChartURL, "chart"},
		{"gap chart", page.TeamGapURL, "gap"},
		{"outlier plot", page.TeamScatterURL, "scatter"},
		{"table", page.TeamTableURL, "table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := url.Parse(tc.link)
			if err != nil {
				t.Fatal(err)
			}
			values := link.Query()
			if values.Get("view") != "teams" || values.Get("display") != tc.display || values.Get("season") != "2025" || values.Get("measure") != "against" || values.Get("team-sort") != "for-expected" || values.Get("team-order") != "asc" {
				t.Fatalf("link did not preserve selection: %s", tc.link)
			}
		})
	}
}

func TestExploreTeamScatterDirectURLRendersSelectedPlotAndTableFallback(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 3, xgCovered: 19},
	})}
	response := httptest.NewRecorder()
	path := "/explore?view=teams&display=scatter&season=2025&measure=against"
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
		t.Fatalf("status=%d reads=%d/%d body=%s", response.Code, store.archiveCalls, store.seasonCalls, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`data-team-display="scatter" aria-current="page"`,
		`Actual vs expected by team (regular-season)</h2>`,
		`<input type="hidden" name="display" value="scatter">`,
		`<option value="2025" selected>2025</option>`,
		`<option value="against" selected>Goals allowed</option>`,
		`data-team-scatter-plot`,
		`data-team-scatter-note`,
		`data-team-scatter-legend`,
		`data-team-scatter-missing`,
		`data-team-scatter-empty`,
		`data-team-scatter-chart-wrap`,
		`data-chart="team-scatter"`,
		`data-team-scatter-hint`,
		`<div data-team-table>`,
		`<table class="explore-table explore-team-table">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scatter direct URL missing %q", want)
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
		if page.TeamSort != "difference-gap" || len(page.TeamColumns) != 14 || len(page.TeamRows) != 2 {
			t.Fatalf("unexpected unified table: %+v", page)
		}
		if baseline == nil {
			baseline = page.TeamRows
			want := []exploreTeamRowValues{
				{Actual: "1.00", Expected: "-1.00", Gap: "+2.00"},
				{Actual: "2.00", Expected: "0.00", Gap: "+2.00"},
				{Actual: "1.00", Expected: "1.00", Gap: "+0.00"},
				{Actual: "3.00", Expected: "Unavailable", Gap: "Unavailable"},
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
