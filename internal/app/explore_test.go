package app

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestExploreRegularSeasonFinalityWhileSourceScopeIsActive(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inventory cache.InventoryCompleteness
		pending   bool
		active    bool
	}{
		{name: "final before playoffs", inventory: cache.InventoryCompletenessComplete},
		{name: "pending fixture", inventory: cache.InventoryCompletenessComplete, pending: true, active: true},
		{name: "unverified inventory", inventory: cache.InventoryCompletenessUnknown, active: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := testArchive(t, map[string]archiveState{
				"2026": {Lifecycle: cache.SourceScopeActive, Inventory: tc.inventory},
			})
			archive[0].Data = completeCatalogSeasonData()
			// Equal points, with distinct goal differences from best at
			// team-14 down to last at alpha.
			for i := range archive[0].Data.Games {
				home := i % len(archive[0].Data.Teams)
				archive[0].Data.Games[i].HomeScore.Int64 = int64(1 + home*(home+1)/2)
			}
			teamPlayed := 30
			if tc.pending {
				archive[0].Data.Games[0].Status = fixtures.PreMatchStatus
				teamPlayed--
			}
			code, body := getPage(t, archive, "/explore/match-by-match/alpha-co-script-alert-1-script?season=2026")
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			status := "2026 regular season"
			if tc.active {
				status += " · In progress"
			}
			requireExact(t, body, "[data-trend-season-summary]", fmt.Sprintf("%s · %d played", status, teamPlayed))

			for _, id := range []string{"explore-data", "explore-team-data", "explore-home-data", "explore-parity-data"} {
				var records []struct {
					Season string
					Active bool
				}
				payload := archivePayload(t, archive, "/explore/match-by-match/alpha-co-script-alert-1-script", body, id)
				if err := json.Unmarshal([]byte(payload), &records); err != nil {
					t.Fatalf("%s: %v", id, err)
				}
				if len(records) != 1 || records[0].Season != "2026" || records[0].Active != tc.active {
					t.Errorf("%s = %+v, want 2026 with active=%v", id, records, tc.active)
				}
			}
			if tc.active {
				if payload := archivePayload(t, archive, "/explore/match-by-match/alpha-co-script-alert-1-script", body, "explore-threshold-data"); strings.Contains(payload, `"2026"`) {
					t.Error("2026 thresholds are available before the regular season is verified complete")
				}
				return
			}

			code, body = getPage(t, archive, "/explore/playoff-thresholds?threshold-season=2026")
			if code != http.StatusOK {
				t.Fatalf("2026 thresholds status = %d", code)
			}
			requireElements(t, body, `[data-threshold-season] option[value="2026"][selected]`)
			requireText(t, body, `[data-threshold-detail="2026"] h3`, "8 playoff teams", "no byes")
			for outcome, team := range map[string]string{"shield": "Team 14", "home": "Team 11", "playoffs": "Team 07"} {
				selector := fmt.Sprintf(`[data-threshold-table-rows] tr[data-season="2026"][data-outcome="%s"]`, outcome)
				requireAttr(t, body, selector, "data-team", team)
				requireAttr(t, body, selector, "data-ppg", "1.5")
			}
		})
	}
}

func TestExploreUsesOneSnapshotAndPreservesMissingXG(t *testing.T) {
	for _, path := range []string{"/explore", "/nwsl-season/explore/scoring-trend?metric=gap", "/nwsl-season/explore/goal-distribution", "/explore/scoring-table", "/nwsl-season/explore/rankings/alpha", "/nwsl-season/explore", "/nwsl-season/explore?display=gap", "/nwsl-season/explore?display=scatter", "/nwsl-season/explore/season-by-season/alpha"} {
		t.Run(path, func(t *testing.T) {
			store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
				"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19},
				"2026": {Lifecycle: cache.SourceScopeActive, Goals: 2, XGCovered: 20},
			})}
			response := httptest.NewRecorder()
			NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			body := response.Body.String()
			if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
				t.Fatalf("status=%d reads=%d/%d body=%s", response.Code, store.archiveCalls, store.seasonCalls, body)
			}
			for _, want := range []string{`<h1>Explore</h1>`, `data-panel="trend"`, `data-panel="distribution"`, `data-panel="table"`, `data-chart="trend"`, `data-chart="distribution"`, `data-team-scatter-logos checked`, `name="metric" value="gap"><span>Goals − xG</span>`, `static/explore.js?v=`, `static/vendor/chart.js-4.5.1/chart.umd.min.js?v=`} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			data, found := explorePayload(t, NewHandler(store), path, body, "explore-data"), true
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
			data = explorePayload(t, NewHandler(store), path, body, "explore-team-data")
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
			for _, label := range []string{"Standings", "Matches", "Schedule difficulty", "Clinching scenarios", "Forecast lab", "Explore"} {
				if !strings.Contains(body, ">"+label+"</a>") {
					t.Errorf("missing main-site navigation %q", label)
				}
			}
			internalPath, _ := stripBasePath(strings.Split(path, "?")[0])
			if !strings.Contains(body, `href="`+relativeURL(internalPath, "/explore")+`" aria-current="page">Explore</a>`) {
				t.Error("Explore is not the active navigation item")
			}
			for _, unwanted := range []string{"Current season", "data-close", "data-year-button", "data-readout", "data-bin=", "Inventory verified", "Inventory unverified", "No regular season", "selected-season-heading", "About this data"} {
				if strings.Contains(body, unwanted) {
					t.Errorf("unexpected %q", unwanted)
				}
			}
		})
	}
}

func TestExploreDistributionValuesMatchEligibleChartSeasons(t *testing.T) {
	store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2019": {Lifecycle: cache.SourceScopeCompleted, Inventory: cache.InventoryCompletenessIncomplete, Goals: 0},
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 4},
	})}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore/goal-distribution", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	_, values, found := strings.Cut(response.Body.String(), `<details class="explore-context-details" data-distribution-values>`)
	values, _, _ = strings.Cut(values, `</details>`)
	if !found || !strings.Contains(values, `<summary><span data-basis-only="all">Table: all 2 seasons</span>`) || !strings.Contains(values, `<table class="explore-table explore-distribution-table">`) || !strings.Contains(values, `<caption><span>Count (share of matches).</span></caption>`) {
		t.Fatalf("server-rendered distribution values missing: %s", values)
	}
	for _, row := range []string{
		`<tr data-matches="20" data-basis-only="all"><th scope="row" data-value="2025">2025</th><td data-value="20">20</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="20">20 (100.0%)</td><td data-value="0">0 (0.0%)</td></tr>`,
		`<tr data-matches="20" data-basis-only="all"><th scope="row" data-value="2026">2026</th><td data-value="20">20</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="0">0 (0.0%)</td><td data-value="20">20 (100.0%)</td></tr>`,
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
	rows := []exploreDistributionSeason{
		{Season: "2023", Total: 4, GoalsEligible: true, Segments: []exploreDistributionSegment{{Count: 1}}},
		{Season: "2024", Total: 3, GoalsEligible: true, Segments: []exploreDistributionSegment{{Count: 1}}},
		{Season: "2025", Total: 1000, GoalsEligible: true, Segments: []exploreDistributionSegment{{Count: 333}}},
		{Season: "2026", Total: 3, GoalsEligible: false, Segments: []exploreDistributionSegment{{Count: 3}}},
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
				if err != nil || link.Query().Get("distribution-sort") != tc.column || link.Query().Get("distribution-order") == tc.order {
					t.Fatalf("sort link did not toggle order: %s", column.URL)
				}
			}
		})
	}
}

func TestExploreDistributionBinSelection(t *testing.T) {
	store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
	})}
	for _, bin := range []string{"all", "0", "1", "2", "3", "4"} {
		t.Run(bin, func(t *testing.T) {
			response := httptest.NewRecorder()
			path := "/explore/goal-distribution?distribution-bin=" + bin
			NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `name="distribution-bin" value="`+bin+`" form="distribution-controls" checked`) {
				t.Errorf("selected bin %q is missing from the rendered control", bin)
			}
			if bin == "1" {
				requireExact(t, response.Body.String(), "#explore-distribution-title", "Share of regular-season matches with 1 goal")
			}
			if !strings.Contains(response.Body.String(), `<details class="explore-context-details" data-distribution-values>`) {
				t.Error("selecting a bin alone opened the distribution values table")
			}
		})
	}

	rows := []exploreDistributionSeason{{Season: "2025", Total: 20, GoalsEligible: true}}
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
	store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 4},
	})}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore/goal-distribution?distribution-sort=bin-3&distribution-order=asc", nil))
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

func TestExploreNavigationGroupsViewsByScope(t *testing.T) {
	store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 4},
	})}
	groups := map[string]string{"league": "League trends", "teams": "Compare teams", "profile": "Team profile"}
	for _, tc := range []struct{ query, group, current string }{
		{"", "teams", `data-team-display="chart" aria-current="page">Actual vs expected</a>`},
		{"view=trend", "league", `data-view-choice="trend" aria-current="page">Scoring trend</a>`},
		// Scoring trend was the default, so its metric links could omit the view.
		{"metric=gap", "league", `data-view-choice="trend" aria-current="page">Scoring trend</a>`},
		{"view=teams&metric=gap", "teams", `data-team-display="chart" aria-current="page">Actual vs expected</a>`},
		{"view=distribution", "league", `data-view-choice="distribution" aria-current="page">Goal distribution</a>`},
		{"view=home-advantage", "league", `data-view-choice="home-advantage" aria-current="page">Home advantage</a>`},
		{"view=parity", "league", `data-view-choice="parity" aria-current="page">Parity</a>`},
		{"view=table", "league", `data-view-choice="table" aria-current="page">Scoring table</a>`},
		{"view=teams", "teams", `data-team-display="chart" aria-current="page">Actual vs expected</a>`},
		{"view=teams&display=gap", "teams", `data-team-display="gap" aria-current="page">Gap to expected</a>`},
		{"view=teams&display=table", "teams", `data-team-display="table" aria-current="page">Table</a>`},
		{"view=team-rankings", "profile", `data-view-choice="team-rankings" aria-current="page">Rankings</a>`},
		{"view=season-trend", "profile", `data-view-choice="season-trend" aria-current="page">Match by match</a>`},
		{"view=team-history", "profile", `data-view-choice="team-history" aria-current="page">Season by season</a>`},
	} {
		target := exploreTarget(tc.query)
		response := httptest.NewRecorder()
		NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code == http.StatusPermanentRedirect {
			next, _ := url.Parse(target)
			next, _ = next.Parse(response.Header().Get("Location"))
			response = httptest.NewRecorder()
			NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, next.RequestURI(), nil))
		}
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.query, response.Code)
		}
		body := response.Body.String()
		if !strings.Contains(body, tc.current) {
			t.Errorf("%s: missing current view %q", tc.query, tc.current)
		}
		view, _ := exploreViewFor(attr(find(t, body, "[data-explore]")[0], "data-view"))
		if title := "<title>" + html.EscapeString(view.DocumentTitle) + "</title>"; !strings.Contains(body, title) {
			t.Errorf("%s: missing view title %q", tc.query, title)
		}
		for group, label := range groups {
			current := strings.Contains(body, `data-group-choice="`+group+`" aria-current="page">`+label+`</a>`)
			hidden := strings.Contains(body, `data-group-views="`+group+`" aria-label="`+label+` views" hidden>`)
			if current != (group == tc.group) || hidden != (group != tc.group) {
				t.Errorf("%s: group %s current=%t hidden=%t", tc.query, group, current, hidden)
			}
		}
	}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore", nil))
	body := response.Body.String()
	links := []string{
		`<a href="explore" data-group-choice="teams" aria-current="page">Compare teams</a>`,
		`<a href="explore/rankings" data-group-choice="profile">Team profile</a>`,
		`<a href="explore/scoring-trend" data-group-choice="league">League trends</a>`,
	}
	previous := -1
	for _, link := range links {
		index := strings.Index(body, link)
		if index < 0 || index < previous {
			t.Fatalf("group links must run from this season to league history; missing or out of order: %q", link)
		}
		previous = index
	}
}

func TestExploreRouteValidation(t *testing.T) {
	handler := NewHandler(&archiveHTTPStore{})
	for _, tc := range []struct {
		path     string
		status   int
		location string
	}{
		{"/explore?view=bad", http.StatusBadRequest, ""},
		{"/explore?season=1900", http.StatusBadRequest, ""},
		{"/explore?season=2026&season=2025", http.StatusBadRequest, ""},
		{"/explore?season=", http.StatusBadRequest, ""},
		{"/explore?measure=bad", http.StatusBadRequest, ""},
		{"/explore?measure=for&measure=against", http.StatusBadRequest, ""},
		{"/explore?units=bad", http.StatusBadRequest, ""},
		{"/explore?units=", http.StatusBadRequest, ""},
		{"/explore?units=total&units=per-match", http.StatusBadRequest, ""},
		{"/explore?display=bad", http.StatusBadRequest, ""},
		{"/explore?display=gap&display=chart", http.StatusBadRequest, ""},
		{"/explore?display=scatter&display=chart", http.StatusBadRequest, ""},
		{"/explore?display=quadrant&display=chart", http.StatusBadRequest, ""},
		{"/explore?quadrant-data=", http.StatusBadRequest, ""},
		{"/explore?quadrant-data=invalid", http.StatusBadRequest, ""},
		{"/explore?quadrant-data=goals&quadrant-data=xg", http.StatusBadRequest, ""},
		{"/explore?team-sort=bad", http.StatusBadRequest, ""},
		{"/explore?team-order=bad", http.StatusBadRequest, ""},
		{"/explore/season-by-season?team=unknown", http.StatusBadRequest, ""},
		{"/explore/season-by-season?team=", http.StatusBadRequest, ""},
		{"/explore/season-by-season?team=alpha&team=bravo", http.StatusBadRequest, ""},
		{"/explore/season-by-season?measure=bad", http.StatusBadRequest, ""},
		{"/explore?distribution-sort=bad", http.StatusBadRequest, ""},
		{"/explore?distribution-sort=", http.StatusBadRequest, ""},
		{"/explore?distribution-sort=bin-0&distribution-sort=bin-1", http.StatusBadRequest, ""},
		{"/explore?distribution-order=bad", http.StatusBadRequest, ""},
		{"/explore?distribution-bin=", http.StatusBadRequest, ""},
		{"/explore?distribution-bin=5", http.StatusBadRequest, ""},
		{"/explore?distribution-bin=all&distribution-bin=1", http.StatusBadRequest, ""},
		{"/explore/home-advantage?home-sort=bad", http.StatusBadRequest, ""},
		{"/explore/home-advantage?home-sort=", http.StatusBadRequest, ""},
		{"/explore/home-advantage?home-sort=goals&home-sort=xg", http.StatusBadRequest, ""},
		{"/explore/home-advantage?home-order=bad", http.StatusBadRequest, ""},
		{"/explore/home-advantage?home-order=asc&home-order=desc", http.StatusBadRequest, ""},
		{"/explore/parity?parity-sort=bad", http.StatusBadRequest, ""},
		{"/explore/parity?parity-sort=", http.StatusBadRequest, ""},
		{"/explore/parity?parity-order=bad", http.StatusBadRequest, ""},
		{"/explore/parity?parity-series=xg", http.StatusBadRequest, ""},
		{"/explore/parity?parity-series=actual&parity-series=both", http.StatusBadRequest, ""},
		{"/explore/parity?season=1999", http.StatusBadRequest, ""},
		{"/explore/parity?season=", http.StatusBadRequest, ""},
		{"/explore/", http.StatusSeeOther, "../explore"},
		{"/nwsl-season/explore/?view=table", http.StatusSeeOther, "../explore?view=table"},
		{"/nwsl-season/explore/parity/?season=2025", http.StatusSeeOther, "../parity?season=2025"},
		{"/explore/unknown", http.StatusNotFound, ""},
		{"/explore/teams", http.StatusNotFound, ""},
		{"/explore?view=parity&season=2025", http.StatusPermanentRedirect, "explore/parity?season=2025"},
		{"/explore/parity?view=table", http.StatusPermanentRedirect, "scoring-table"},
		{"/explore/parity?view=teams&display=gap", http.StatusPermanentRedirect, "../explore?display=gap"},
		{"/explore?view=teams&metric=gap", http.StatusPermanentRedirect, "explore"},
		{"/explore?metric=gap&penalties=exclude", http.StatusPermanentRedirect, "explore/scoring-trend?metric=gap&penalties=exclude"},
		{"/nwsl-season/explore?view=parity", http.StatusPermanentRedirect, "explore/parity"},
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
	archive := testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
	})
	for index := range archive[0].Data.XGoals {
		observation := &archive[0].Data.XGoals[index]
		observation.HomeXPoints.Float64, observation.HomeXPoints.Valid = 1.4, true
		observation.AwayXPoints.Float64, observation.AwayXPoints.Valid = 1.6, true
	}
	// A missing xG observation must not remove independently covered xPoints.
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
		if page.TeamUnits != units || page.TeamActualLabel != "Points" || page.TeamXGLabel != "xPoints" || !page.TeamMissingXG || page.TeamMissingXPoints {
			t.Fatalf("points coverage or labels wrong: %+v", page)
		}
		if got := page.TeamSeasons[0].Teams[0]; len(got.Values) != 4 || got.XPointsCovered != 20 || got.Values[3].Expected == nil || got.Totals[3].Expected == nil {
			t.Fatalf("points missing from JSON record: %+v", got)
		}
		if page.TeamRows[0].Team.ID != "bravo" {
			t.Fatalf("points-expected sort did not use xPoints: %+v", page.TeamRows)
		}
		want := exploreTeamRowValues{Actual: "3.00", Expected: "1.60", Gap: "+1.40"}
		if units == "total" {
			want = exploreTeamRowValues{Actual: "60", Expected: "32.00", Gap: "+28.00"}
		}
		if got := page.TeamRows[0].Values[3]; got != want {
			t.Fatalf("%s points row = %+v, want %+v", units, got, want)
		}
		if !strings.Contains(page.TeamDisplayURLs["table"], "units="+units) {
			t.Fatalf("units lost from comparison link: %s", page.TeamDisplayURLs["table"])
		}
	}
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/explore?display=table&measure=points&units=total&team-sort=points-expected&team-order=desc", []string{`name="measure" value="points" form="team-controls" checked><span>Points</span>`, `name="units" value="total" form="team-controls" checked><span>Totals</span>`, `data-team-sort="points-expected"`, `<td>60</td><td>32.00</td><td><strong>`}},
		{"/explore/season-by-season/alpha?measure=points&series=xg&context=on&history-sort=points-expected", []string{`name="measure" value="points" form="team-history-controls" checked><span>Points</span>`, `name="series" value="xg" form="team-history-controls" checked><span>xPoints</span>`, `data-history-sort="points-expected"`, `<h4>xPoints</h4>`}},
	} {
		store := &archiveHTTPStore{archive: archive}
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
	missingPoints := testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
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
		t.Fatalf("xPoints-only missing data was not independent of xG: %+v, %v", page, err)
	}
	historyPage, err := exploreTeamHistory(url.Values{"team": {"alpha"}, "measure": {"points"}}, page.TeamSeasons)
	if err != nil || historyPage.TeamHistoryMissingXG || !historyPage.TeamHistoryMissingXPoints {
		t.Fatalf("history xPoints warning was not independent of xG: %+v, %v", historyPage, err)
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
	archive := testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3, XGCovered: 19},
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
		link, err := url.Parse(page.TeamDisplayURLs["chart"])
		if err != nil || link.Query().Get("season") != season || link.Query().Get("measure") != "difference" || link.Query().Get("display") != "chart" {
			t.Fatalf("chart link lost selection: %s", page.TeamDisplayURLs["chart"])
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
	archive := testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3, XGCovered: 19},
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
		{"paired-dot chart", page.TeamDisplayURLs["chart"], "chart"},
		{"gap chart", page.TeamDisplayURLs["gap"], "gap"},
		{"outlier plot", page.TeamDisplayURLs["scatter"], "scatter"},
		{"quadrant plot", page.TeamDisplayURLs["quadrant"], "quadrant"},
		{"table", page.TeamDisplayURLs["table"], "table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := url.Parse(tc.link)
			if err != nil {
				t.Fatal(err)
			}
			values := link.Query()
			if values.Has("view") || values.Get("display") != tc.display || values.Get("season") != "2025" || values.Get("measure") != "against" || values.Get("team-sort") != "for-expected" || values.Get("team-order") != "asc" {
				t.Fatalf("link did not preserve selection: %s", tc.link)
			}
		})
	}
}

func TestExploreTeamScatterDirectURLRendersSelectedPlotAndTableFallback(t *testing.T) {
	store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3, XGCovered: 19},
	})}
	response := httptest.NewRecorder()
	path := "/explore?display=scatter&season=2025&measure=against"
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
		t.Fatalf("status=%d reads=%d/%d body=%s", response.Code, store.archiveCalls, store.seasonCalls, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`data-team-display="scatter" aria-current="page"`,
		`Actual vs expected by team</h2>`,
		`<input type="hidden" name="display" value="scatter">`,
		`<option value="2025" selected>2025</option>`,
		`name="measure" value="against" form="team-controls" checked><span>Goals allowed</span>`,
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

func TestExploreTeamQuadrantSelectionsAndCacheOnlyFallback(t *testing.T) {
	for _, mode := range []string{"goals", "xg", "both"} {
		for _, units := range []string{"per-match", "total"} {
			t.Run(mode+"/"+units, func(t *testing.T) {
				store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
					"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
					"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3, XGCovered: 19},
				})}
				response := httptest.NewRecorder()
				path := "/nwsl-season/explore?display=quadrant&season=2026&measure=points&quadrant-data=" + mode + "&units=" + units
				NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
					t.Fatalf("status=%d reads=%d/%d body=%s", response.Code, store.archiveCalls, store.seasonCalls, response.Body.String())
				}
				body := response.Body.String()
				for _, want := range []string{
					`data-team-display="quadrant" aria-current="page"`,
					`Scored vs allowed by team</h2>`,
					`<input type="hidden" name="display" value="quadrant">`,
					`name="quadrant-data" value="` + mode + `" form="team-controls" checked>`,
					`name="units" value="` + units + `" form="team-controls" checked>`,
					`data-segmented="measure" hidden`,
					`data-segmented="quadrant-data"><legend>Show</legend>`,
					`data-chart="team-quadrant"`,
					`data-team-quadrant-selection`,
					`<table class="explore-table explore-team-table">`,
					`Unavailable`,
				} {
					if !strings.Contains(body, want) {
						t.Errorf("quadrant URL missing %q", want)
					}
				}
				summaries, err := history.SummarizeScoring(store.archive)
				if err != nil {
					t.Fatal(err)
				}
				selection, err := url.Parse(path)
				if err != nil {
					t.Fatal(err)
				}
				page, err := exploreTeams(selection.Query(), summaries, store.archive)
				if err != nil {
					t.Fatal(err)
				}
				for _, target := range []string{page.TeamDisplayURLs["quadrant"], page.TeamDisplayURLs["chart"], page.TeamDisplayURLs["gap"], page.TeamDisplayURLs["scatter"], page.TeamDisplayURLs["table"], page.TeamColumns[0].URL} {
					link, err := url.Parse(target)
					if err != nil || link.Query().Get("quadrant-data") != mode || link.Query().Get("units") != units || link.Query().Get("measure") != "points" || link.Query().Get("season") != "2026" {
						t.Fatalf("selection lost in link %s: %v", target, err)
					}
				}
			})
		}
	}
	page, err := exploreTeams(nil, nil, nil)
	if err != nil || page.TeamQuadrantData != "goals" {
		t.Fatalf("default quadrant selection = %q, error = %v", page.TeamQuadrantData, err)
	}
}

func TestExploreUnifiedTableIsIndependentOfChartMeasure(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
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

func TestLinkExploreTeamMatchesReferencesSharedFixtures(t *testing.T) {
	data := exploreHeadToHeadPayload{Fixtures: []exploreHeadToHeadFixture{{ID: "m1"}, {ID: "m2"}}}
	seasons := []exploreTeamSeason{{Teams: []exploreTeamRecord{
		{ID: "home", Matches: []exploreMatchRecord{{ID: "m2", Venue: "Home", Opponents: &exploreOpponentFactors{Scored: 1.1, Allowed: 0.9, ExpectedPoints: 1.5}}, {ID: "m1", Venue: "Away"}}},
		{ID: "away", Matches: []exploreMatchRecord{{ID: "m2", Venue: "Away", Opponents: &exploreOpponentFactors{Scored: 0.8, Allowed: 1.2, ExpectedPoints: 1.3}}}},
	}}}
	if err := linkExploreTeamMatches(seasons, &data); err != nil {
		t.Fatal(err)
	}
	if got := seasons[0].Teams[0].MatchRefs; !slices.Equal(got, []int{1, 0}) {
		t.Errorf("home refs = %v, want [1 0]", got)
	}
	if got, want := data.Fixtures[1].Opponents, (&[2][3]float64{{1.1, 0.9, 1.5}, {0.8, 1.2, 1.3}}); got == nil || *got != *want {
		t.Errorf("fixture opponents = %v, want %v", got, want)
	}
	if data.Fixtures[0].Opponents != nil {
		t.Errorf("unadjusted fixture opponents = %v, want none", data.Fixtures[0].Opponents)
	}
	payload, err := json.Marshal(seasons[0].Teams[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"matches":[1,0]`) {
		t.Errorf("team payload = %s, want match indexes", payload)
	}

	seasons[0].Teams[0].Matches = append(seasons[0].Teams[0].Matches, exploreMatchRecord{ID: "missing"})
	if err := linkExploreTeamMatches(seasons, &data); err == nil {
		t.Error("missing fixture linked without error")
	}
}

func TestTableLabel(t *testing.T) {
	for _, test := range []struct {
		rows       int
		noun, want string
	}{
		{1, "season", "Table: 1 season"},
		{10, "season", "Table: all 10 seasons"},
		{28, "match", "Table: all 28 matches"},
		{116, "team-season", "Table: all 116 team-seasons"},
	} {
		if got := tableLabel(test.rows, test.noun); got != test.want {
			t.Errorf("tableLabel(%d, %q) = %q, want %q", test.rows, test.noun, got, test.want)
		}
	}
}

func TestExploreThresholdsLeaveGapsForTiesWhileSourceScopeIsActive(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{
		"2026": {Lifecycle: cache.SourceScopeActive, Inventory: cache.InventoryCompletenessComplete},
	})
	// Every team has the same record, so no rank is determined by results.
	archive[0].Data = completeCatalogSeasonData()
	code, body := getPage(t, archive, "/explore/playoff-thresholds?threshold-season=2026")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	requireText(t, body, `[data-threshold-detail="2026"]`, "Tied finishing order remains ambiguous")
	if rows := find(t, body, `[data-threshold-table-rows] tr[data-season="2026"]`); len(rows) != 0 {
		t.Errorf("tied season published %d cutoff rows, want gaps", len(rows))
	}
}

// Each Explore view's page is its own canonical page, and its query-string
// variants point search engines at it. Links that carry selections are
// nofollow, so crawlers aren't drawn into every combination of team, season
// and display, while the tabs link each view's page plainly.
func TestExploreQueryVariantsAreCanonicalAndNofollow(t *testing.T) {
	store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 4},
	})}
	render := func(t *testing.T, handler http.Handler, target string) string {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", target, response.Code)
		}
		return response.Body.String()
	}
	canonical := regexp.MustCompile(`<link rel="canonical" href="([^"]*)">`)

	for _, tc := range []struct{ target, canonical string }{
		{"/explore", "explore"},
		{"/explore?display=table&team-sort=name", "explore"},
		{"/explore/head-to-head", "head-to-head"},
		{"/explore/playoff-thresholds?threshold-season=2025", "playoff-thresholds"},
		{"/explore/parity?season=2025", "parity"},
		{"/explore/team-seasons", "team-seasons"},
		{"/explore/match-by-match", "match-by-match"},
	} {
		body := render(t, NewHandler(store), tc.target)
		if match := canonical.FindStringSubmatch(body); match == nil || match[1] != tc.canonical {
			t.Errorf("%s: canonical = %v, want href %q", tc.target, match, tc.canonical)
		}
		anchors := regexp.MustCompile(`<a [^>]*>`).FindAllString(body, -1)
		queryLinks := 0
		for _, anchor := range anchors {
			href := regexp.MustCompile(`href="([^"]*)"`).FindStringSubmatch(anchor)
			if href == nil || strings.Contains(href[1], "://") || strings.HasPrefix(href[1], "#") {
				continue
			}
			link, err := url.Parse(html.UnescapeString(href[1]))
			if err != nil {
				t.Fatal(err)
			}
			target, _ := url.Parse(tc.target)
			resolved := target.ResolveReference(link)
			if resolved.Path != "/explore" && !strings.HasPrefix(resolved.Path, "/explore/") {
				continue
			}
			nofollow := strings.Contains(anchor, ` rel="nofollow"`)
			if resolved.RawQuery != "" {
				queryLinks++
				if !nofollow {
					t.Errorf("%s: query-string link is followable: %s", tc.target, anchor)
				}
			} else if nofollow {
				t.Errorf("%s: link to a view's page is nofollow: %s", tc.target, anchor)
			}
		}
		if queryLinks == 0 {
			t.Errorf("%s: no query-string links rendered", tc.target)
		}
		for _, group := range exploreViewGroups {
			for _, view := range group.Views {
				if href := relativeURL(requestPath(tc.target), exploreViewPath(view)); !strings.Contains(body, `href="`+href+`"`) {
					t.Errorf("%s: no plain link to %s (%q)", tc.target, exploreViewPath(view), href)
				}
			}
		}
	}

	public := NewHandlerWithOptions(store, Options{PublicURL: "https://example.test/"})
	if match := canonical.FindStringSubmatch(render(t, public, "/explore/parity?season=2025")); match == nil || match[1] != "https://example.test/explore/parity" {
		t.Errorf("canonical with a public URL = %v, want https://example.test/explore/parity", match)
	}
}

func requestPath(requestTarget string) string {
	path, _, _ := strings.Cut(requestTarget, "?")
	return path
}

// exploreViewURL is a view's page with a query string, as a request target.
func exploreViewURL(key, query string) string {
	view, ok := exploreViewFor(key)
	if !ok {
		panic("unknown Explore view " + key)
	}
	if query == "" {
		return exploreViewPath(view)
	}
	return exploreViewPath(view) + "?" + query
}

// exploreTarget turns a selection written as a query, with any view as
// view=<key>, into the request target for that view's page.
func exploreTarget(query string) string {
	values, err := url.ParseQuery(query)
	if err != nil || len(values["view"]) != 1 {
		return "/explore?" + query
	}
	key := values.Get("view")
	if _, ok := exploreViewFor(key); !ok {
		return "/explore?" + query
	}
	rest := withoutQueryParameter(query, "view")
	if remaining, _ := url.ParseQuery(rest); key == exploreViewGroups[0].Views[0].Key && legacyTrendQuery(remaining) {
		return "/explore?" + query // Let the server resolve the ambiguity.
	}
	return exploreViewURL(key, rest)
}
