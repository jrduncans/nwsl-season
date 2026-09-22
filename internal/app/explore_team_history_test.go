package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestExploreTeamHistoryKeepsRatesCoverageAndEligibility(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2019": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
		"2021": {lifecycle: cache.SourceScopeCompleted, goals: 4, xgCovered: 19},
		"2022": {lifecycle: cache.SourceScopeCompleted, inventory: cache.InventoryCompletenessIncomplete, goals: 9},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 1, xgCovered: 20},
	})
	for i := range archive {
		if archive[i].Entry.Season == "2026" {
			// Even one played match is eligible, with its sample size explicit.
			archive[i].Data.Games = archive[i].Data.Games[:1]
			archive[i].Data.XGoals = archive[i].Data.XGoals[:1]
		}
	}
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	page, err := exploreTeamHistory(url.Values{"team": {"alpha"}}, teams.TeamSeasons)
	if err != nil {
		t.Fatal(err)
	}
	want := []exploreTeamHistoryRow{
		{Season: "2026", Active: true, Played: 1, Values: []exploreTeamRowValues{{Actual: "-1.00", Expected: "1.00"}, {Actual: "0.00", Expected: "1.00"}, {Actual: "1.00", Expected: "0.00"}}},
		{Season: "2021", Played: 20, Values: []exploreTeamRowValues{{Actual: "0.00", Expected: "Unavailable"}, {Actual: "2.00", Expected: "Unavailable"}, {Actual: "2.00", Expected: "Unavailable"}}},
		{Season: "2019", Played: 20, Values: []exploreTeamRowValues{{Actual: "-1.00", Expected: "1.00"}, {Actual: "1.00", Expected: "1.00"}, {Actual: "2.00", Expected: "0.00"}}},
	}
	if !reflect.DeepEqual(page.TeamHistoryRows, want) || !page.TeamHistoryMissingXG {
		t.Fatalf("history rates, coverage, or eligibility changed: %+v", page)
	}
	// The HTML fallback contains every measure, even when the chart shows one.
	store := &historyHTTPStore{archive: archive}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/explore?view=team-history&team=alpha&measure=against", nil))
	body := response.Body.String()
	for _, fragment := range []string{`data-panel="team-history" aria-labelledby=`, `value="alpha" selected`, `<th scope="row">2026</th>`, `<th scope="colgroup" colspan="2">Goals allowed</th>`, `<td>-1.00</td><td>1.00</td>`, `Some seasons have incomplete xG for this team`, `Show team history`} {
		if !strings.Contains(body, fragment) {
			t.Errorf("missing %q", fragment)
		}
	}
	if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
		t.Fatalf("status=%d reads=%d/%d", response.Code, store.archiveCalls, store.seasonCalls)
	}
}

func TestExploreTeamHistoryUsesIDsAcrossNameChanges(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2019": {lifecycle: cache.SourceScopeCompleted, goals: 2},
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3},
	})
	for i := range archive {
		season := &archive[i]
		name := "Current name"
		if season.Entry.Season == "2019" {
			name = "Former name"
			for j := range season.Data.Games {
				season.Data.Games[j].AwayTeamID = "former-club"
			}
		}
		season.Data.Teams = []standings.Team{{ID: "alpha", Name: name}, {ID: "bravo", Name: "Same name"}, {ID: "former-club", Name: "Same name"}}
	}
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, name string
		seasons  int
	}{{"alpha", "Current name", 2}, {"bravo", "Same name", 1}, {"former-club", "Same name", 1}} {
		page, err := exploreTeamHistory(url.Values{"team": {tc.id}}, teams.TeamSeasons)
		if err != nil || page.HistoryTeam.ID != tc.id || page.HistoryTeam.Name != tc.name || len(page.TeamHistoryRows) != tc.seasons || len(page.HistoryTeamOptions) != 3 {
			t.Fatalf("identity %s changed: %+v, %v", tc.id, page, err)
		}
	}
}

func TestExploreTeamHistoryEmptyArchive(t *testing.T) {
	page, err := exploreTeamHistory(nil, nil)
	if err != nil || page.HistoryTeam.ID != "" || len(page.TeamHistoryRows) != 0 || page.TeamHistoryMissingXG {
		t.Fatalf("empty archive: %+v, %v", page, err)
	}
	response := httptest.NewRecorder()
	NewHandler(&historyHTTPStore{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=team-history", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-team-history-empty>No team history`) {
		t.Fatalf("empty history: %d %s", response.Code, response.Body.String())
	}
}

func TestExploreTeamHistorySortsEveryColumnBeforeRounding(t *testing.T) {
	low, high, zero, one, two, negative := 1.0001, 1.0004, 0.0, 1.0, 2.0, -1.0
	seasons := []exploreTeamSeason{
		{Season: "2026", Active: true, Teams: []exploreTeamRecord{{ID: "alpha", Name: "Alpha", Played: 2, Values: [3]exploreTeamValues{{Actual: low, Expected: &low}, {Actual: 4, Expected: &two}, {Actual: -3, Expected: &negative}}}}},
		{Season: "2025", Teams: []exploreTeamRecord{{ID: "alpha", Name: "Alpha", Played: 4, Values: [3]exploreTeamValues{{Actual: high, Expected: &high}, {Actual: 2, Expected: &zero}, {Actual: -1, Expected: &one}}}}},
		{Season: "2023", Teams: []exploreTeamRecord{{ID: "alpha", Name: "Alpha", Played: 4}}},
		{Season: "2021", Teams: []exploreTeamRecord{{ID: "alpha", Name: "Alpha", Played: 1, Values: [3]exploreTeamValues{{Actual: low, Expected: &zero}, {Actual: 3, Expected: &one}, {Actual: -2, Expected: &zero}}}}},
	}
	for _, tc := range []struct{ column, ascending, descending string }{
		{"season", "2021,2023,2025,2026", "2026,2025,2023,2021"},
		{"played", "2021,2026,2025,2023", "2025,2023,2026,2021"},
		{"for-actual", "2023,2026,2021,2025", "2025,2026,2021,2023"},
		{"for-expected", "2021,2026,2025,2023", "2025,2026,2021,2023"},
		{"against-actual", "2023,2025,2021,2026", "2026,2021,2025,2023"},
		{"against-expected", "2025,2021,2026,2023", "2026,2021,2025,2023"},
		{"difference-actual", "2026,2021,2025,2023", "2023,2025,2021,2026"},
		{"difference-expected", "2026,2021,2025,2023", "2025,2021,2026,2023"},
	} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(tc.column+"/"+order, func(t *testing.T) {
				query := url.Values{"team": {"alpha"}, "measure": {"against"}, "history-sort": {tc.column}, "history-order": {order}}
				page, err := exploreTeamHistory(query, seasons)
				if err != nil {
					t.Fatal(err)
				}
				var years []string
				for _, row := range page.TeamHistoryRows {
					years = append(years, row.Season)
				}
				want := tc.ascending
				if order == "desc" {
					want = tc.descending
				}
				if got := strings.Join(years, ","); got != want {
					t.Fatalf("got %s, want %s", got, want)
				}
				for _, column := range page.HistoryColumns {
					link, err := url.Parse(column.URL)
					if err != nil {
						t.Fatal(err)
					}
					wantOrder, wantSort := "desc", "none"
					if column.Key == tc.column {
						wantSort = "ascending"
						if order == "desc" {
							wantOrder, wantSort = "asc", "descending"
						}
					}
					if link.Query().Get("view") != "team-history" || link.Query().Get("team") != "alpha" || link.Query().Get("measure") != "against" || link.Query().Get("history-sort") != column.Key || link.Query().Get("history-order") != wantOrder || column.Sort != wantSort {
						t.Fatalf("sort link or state lost selection: %+v", column)
					}
				}
			})
		}
	}
	if seasons[0].Season != "2026" || seasons[0].Teams[0].Values[0].Actual != low {
		t.Fatal("sorting changed the shared chart data")
	}
}

func TestExploreTeamHistorySortURLsAndFallback(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2019": {lifecycle: cache.SourceScopeCompleted, goals: 5, xgCovered: 20},
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 2, xgCovered: 19},
	})
	handler := NewHandler(&historyHTTPStore{archive: archive})
	path := "/nwsl-season/explore?view=team-history&team=alpha&measure=against&history-sort=for-actual&history-order=desc"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `data-history-sort=`) != 8 {
		t.Fatalf("missing sortable headers: %d %s", response.Code, body)
	}
	for _, fragment := range []string{`name="history-sort" value="for-actual"`, `name="history-order" value="desc"`, `aria-label="Sort by Goals scored: Goals"`, `aria-label="Sort by Goal differential: xG"`} {
		if !strings.Contains(body, fragment) {
			t.Errorf("missing %s", fragment)
		}
	}
	_, rows, _ := strings.Cut(body, `<tbody data-team-history-rows>`)
	rows, _, _ = strings.Cut(rows, `</tbody>`)
	first, second, third := strings.Index(rows, "2019"), strings.Index(rows, "2026"), strings.Index(rows, "2025")
	if first < 0 || first >= second || second >= third {
		t.Fatalf("HTML fallback is not sorted by unrounded goals with newest ties first: %s", rows)
	}
	for _, query := range []string{
		"history-sort=", "history-sort=bad", "history-sort=season&history-sort=played",
		"history-order=", "history-order=bad", "history-order=asc&history-order=desc",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=team-history&"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("query %s: status %d, want 400", query, response.Code)
		}
	}
}
