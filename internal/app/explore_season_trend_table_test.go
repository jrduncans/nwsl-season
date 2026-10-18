package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
)

func TestExploreTrendTableColumnsAndNativeSorting(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	base := "/explore?view=season-trend&season=2025&team=odMX2OJqYL"
	common := []string{"Match", "Date (venue local)", "Venue", "Opponent", "Score", "Result", "Points", "xG for", "xG against", "xPoints"}
	for _, chart := range []struct {
		view    string
		derived []string
	}{
		{"balance", nil}, {"relative", nil}, {"difference", []string{"Goal margin", "xG margin"}},
		{"compare", []string{"Goals − xG", "Goals allowed − xG allowed"}}, {"points", []string{"Points − xPoints"}},
	} {
		for _, mode := range []string{"none", "5"} {
			path := base + "&trend-view=" + chart.view + "&smoothing=" + mode
			code, body := getPage(t, archive, path)
			if code != http.StatusOK {
				t.Fatal(code)
			}
			headings := find(t, body, "[data-trend-headings] th")
			want := append(slices.Clone(common), chart.derived...)
			for i, label := range want {
				if text(headings[i]) != label {
					t.Fatalf("%s/%s heading %d: %s, want %s", chart.view, mode, i, text(headings[i]), label)
				}
			}
			values := findIn(t, find(t, body, "[data-trend-rows] tr")[0], "td")
			if !slices.Equal([]string{text(values[3]), text(values[4]), text(values[5]), text(values[6]), text(values[7]), text(values[8])}, []string{"2–0", "Win", "3", "1.10", "0.70", "1.54"}) {
				t.Fatal("per-match values must be independent of chart and smoothing")
			}
			forbidElements(t, body, "[data-trend-values][open]")
			for _, suffix := range []string{"&trend-sort=match", "&trend-order=asc", "&trend-order=desc", "&trend-sort=venue", "&trend-sort=result"} {
				code, sorted := getPage(t, archive, path+suffix)
				if code != http.StatusOK {
					t.Fatal(code)
				}
				requireElements(t, sorted, "[data-trend-values][open]")
			}
			// A real GET link works without JavaScript and preserves the selection.
			target, err := url.Parse(attr(find(t, body, "[data-trend-sort=venue]")[0], "href"))
			if err != nil || target.Path != "" {
				t.Fatalf("relative sort URL: %v, %v", target, err)
			}
			if target.Query().Get("trend-view") != chart.view || target.Query().Get("smoothing") != mode {
				t.Fatal("sort lost chart selection")
			}
			code, sorted := getPage(t, archive, "/explore"+target.String())
			if code != http.StatusOK {
				t.Fatal(code)
			}
			requireAttr(t, sorted, "[data-trend-sort=venue]", "aria-label", "Sort by Venue")
			requireAttr(t, sorted, "[data-trend-sort=venue]", "data-trend-sort", "venue")
		}
	}
	_, body := getPage(t, archive, base+"&trend-view=compare&smoothing=5&penalties=exclude")
	requireText(t, body, "[data-trend-headings]", "Non-penalty goals", "npxG for", "npxG against", "Non-penalty goals − npxG", "5-match average")
}

func TestExploreTrendTableValidationAndChartFallback(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	base := "/nwsl-season/explore?view=season-trend&season=2025"
	for _, bad := range []string{"trend-sort=", "trend-sort=other", "trend-sort=venue&trend-sort=result", "trend-order=", "trend-order=up", "trend-order=asc&trend-order=asc"} {
		code, _ := getPage(t, archive, base+"&"+bad)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	for _, selection := range []string{"trend-view=balance&trend-sort=points-gap", "trend-view=points&trend-sort=goal-margin", "trend-view=compare&trend-sort=xg-margin", "trend-view=relative&trend-sort=allowed-gap"} {
		response := httptest.NewRecorder()
		NewHandler(&archiveHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, base+"&"+selection+"&trend-order=desc", nil))
		target, err := url.Parse(response.Header().Get("Location"))
		if err != nil || response.Code != http.StatusFound || target.Path != "" || target.Query().Has("trend-sort") || target.Query().Has("trend-order") || target.Query().Get("trend-view") == "" {
			t.Fatalf("chart fallback: %d %v %v", response.Code, target, err)
		}
	}
}

func TestExploreTrendTableExactSortAndNewestTies(t *testing.T) {
	t.Parallel()
	matches := []exploreMatchRecord{
		{Venue: "Home", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 1, Expected: new(.1001)}, {Expected: new(0.0)}, {Actual: 1, Expected: new(.8999)}, {Actual: 3, Expected: new(2.1001)}}},
		{Venue: "Away", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 1, Expected: new(.1002)}, {Expected: new(0.0)}, {Actual: 1, Expected: new(.8998)}, {Actual: 3, Expected: new(2.1002)}}},
		{Venue: "Home", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 1, Expected: new(.1001)}, {Expected: new(0.0)}, {Actual: 1, Expected: new(.8999)}, {Actual: 3, Expected: new(2.1001)}}},
		{Venue: "Away"},
		{Venue: "Home"},
		{Venue: "Away", Undated: true, GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 5, Expected: new(0.0)}, {Expected: new(0.0)}, {Actual: 5, Expected: new(5.0)}, {Actual: 3, Expected: new(0.0)}}},
	}
	teams := exploreTeamsView{TeamSeason: "2025", TeamSeasons: []exploreTeamSeason{{Season: "2025", Teams: []exploreTeamRecord{{ID: "team", Matches: matches}}}}}
	for _, tc := range []struct {
		view, key, order string
		want             []int
	}{
		{"balance", "match", "asc", []int{1, 2, 3, 4, 5, 6}}, {"balance", "match", "desc", []int{5, 4, 3, 2, 1, 6}},
		{"balance", "venue", "asc", []int{4, 2, 5, 3, 1, 6}}, {"balance", "venue", "desc", []int{5, 3, 1, 4, 2, 6}},
		{"balance", "result", "asc", []int{5, 4, 3, 2, 1, 6}}, {"balance", "result", "desc", []int{3, 2, 1, 5, 4, 6}},
		{"points", "points-gap", "asc", []int{2, 3, 1, 5, 4, 6}}, {"points", "points-gap", "desc", []int{3, 1, 2, 5, 4, 6}},
		{"compare", "scored-gap", "asc", []int{2, 3, 1, 5, 4, 6}}, {"compare", "scored-gap", "desc", []int{3, 1, 2, 5, 4, 6}},
		{"compare", "allowed-gap", "asc", []int{3, 2, 1, 5, 4, 6}}, {"compare", "allowed-gap", "desc", []int{3, 2, 1, 5, 4, 6}},
		{"difference", "goal-margin", "asc", []int{3, 2, 1, 5, 4, 6}}, {"difference", "goal-margin", "desc", []int{3, 2, 1, 5, 4, 6}},
		{"difference", "xg-margin", "asc", []int{2, 3, 1, 5, 4, 6}}, {"difference", "xg-margin", "desc", []int{3, 1, 2, 5, 4, 6}},
	} {
		for _, mode := range []string{"match", "rolling"} {
			page, err := exploreSeasonTrend(url.Values{"trend-view": {tc.view}, "trend-mode": {mode}, "trend-sort": {tc.key}, "trend-order": {tc.order}}, teams, teamNameView{ID: "team"})
			if err != nil {
				t.Fatal(err)
			}
			var got []int
			for _, row := range page.TrendRows {
				got = append(got, row.Number)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("%s/%s/%s: %v, want %v", mode, tc.key, tc.order, got, tc.want)
			}
			for _, row := range page.TrendRows {
				if row.Number == 4 || row.Number == 5 {
					if row.DisplayValues[7] != "Unavailable" || row.DisplayValues[9] != "Unavailable" {
						t.Fatal("missing values became zero")
					}
				}
			}
		}
	}
}

func TestExploreTrendTableMissingPenaltySplit(t *testing.T) {
	t.Parallel()
	matches := []exploreMatchRecord{
		{GoalsCovered: true, Score: "2–0", Values: [4]exploreTeamValues{{Actual: 1, Expected: new(.5)}, {Expected: new(.25)}, {Actual: 1, Expected: new(.25)}, {Actual: 3, Expected: new(1.5)}}},
		{Score: "1–0", Values: [4]exploreTeamValues{3: {Actual: 3, Expected: new(1.5)}}},
	}
	teams := exploreTeamsView{TeamSeason: "2025", TeamSeasons: []exploreTeamSeason{{Season: "2025", Teams: []exploreTeamRecord{{ID: "team", Matches: matches}}}}}
	page, err := exploreSeasonTrend(url.Values{"trend-view": {"difference"}, "penalties": {"exclude"}}, teams, teamNameView{ID: "team"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(page.TrendRows[0].DisplayValues[4:], []string{"2–0", "Win", "3", "1–0", "0.50", "0.25", "1.50", "1", "0.25", "1.00", "0.25"}) {
		t.Fatalf("basis/official values or rolling columns: %v", page.TrendRows[0].DisplayValues)
	}
	if !slices.Equal(page.TrendRows[1].DisplayValues[4:], []string{"1–0", "Win", "3", "Unavailable", "Unavailable", "Unavailable", "1.50", "Unavailable", "Unavailable", "Unavailable", "Unavailable"}) {
		t.Fatalf("missing penalty split must withhold basis goals, derived values and affected windows: %v", page.TrendRows[1].DisplayValues)
	}
}
