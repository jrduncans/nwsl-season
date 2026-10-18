package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestExploreMarginTilesCoverageAndOfficialRecord(t *testing.T) {
	t.Parallel()
	value := func(n float64) *float64 { return &n }
	matches := []exploreMatchRecord{
		{Venue: "Home", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 2, Expected: value(1.25)}, {Actual: 0, Expected: value(.5)}, {}, {Actual: 3}}},
		{Venue: "Away", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 0}, {Actual: 1}, {}, {Actual: 0}}},
		{Venue: "Home", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 0, Expected: value(.5)}, {Actual: 0, Expected: value(.75)}, {}, {Actual: 3}}},
	}
	tiles := exploreMarginTiles(matches)
	want := exploreMarginTile{Label: "All", Played: 3, Wins: 2, Losses: 1, Points: 6, GoalsCovered: 3, XGCovered: 2, GoalsFor: 2, GoalsAgainst: 1, XGFor: 1.75, XGAgainst: 1.25}
	if tiles[0] != want {
		t.Fatalf("numeric total: %+v, want %+v", tiles[0], want)
	}
	views := formatMarginTiles(tiles, history.AllShots, "match")
	if views[0].Record != "2–0–1" || views[0].Rate != "2.00 pts/match" || views[0].Goals != "2–1 goals" || views[0].XG != "xG incomplete" || views[0].XGCoverage != "xG 2/3 matches" || views[1].XG != "1.75–1.25 xG" {
		t.Fatalf("formatted totals: %+v", views)
	}
	// Coverage is an input fact. A future producer may provide goals without xG
	// or xG without goals; neither observation is allowed to stand in for the other.
	matches[2].GoalsCovered = false
	tiles = exploreMarginTiles(matches)
	if tiles[0].GoalsCovered != 2 || tiles[0].XGCovered != 2 || tiles[0].GoalsAgainst != 1 {
		t.Fatalf("goal coverage inferred from xG: %+v", tiles[0])
	}
	views = formatMarginTiles(tiles, history.NonPenalty, "match")
	if views[0].Coverage != "2/3 with split" || views[0].XG != "npxG incomplete" || views[0].XGCoverage != "npxG 2/3 matches" {
		t.Fatalf("non-penalty formatting: %+v", views)
	}
	empty := formatMarginTiles(exploreMarginTiles(matches[:1]), history.AllShots, "match")[2]
	if empty.XG != "No matches" || empty.XGCoverage != "xG 0/0 matches" {
		t.Fatalf("empty venue: %+v", empty)
	}
}

func TestExploreMarginCoverageFromArchive(t *testing.T) {
	t.Parallel()
	archive := testArchive(t, map[string]archiveState{"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20}})
	for i := range archive[0].Data.XGoals {
		archive[0].Data.XGoals[i].Penalties = &cache.PenaltySplit{}
	}
	// The first raw observation has a split but invalid xG; the second has xG
	// without a split. The existing scoring contract rejects both for np goals.
	archive[0].Data.XGoals[0].HomeXG.Valid = false
	archive[0].Data.XGoals[1].Penalties = nil
	all, err := history.SummarizeScoringBasis(archive, history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	np, err := history.SummarizeScoringBasis(archive, history.NonPenalty)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		basis     history.ScoringBasis
		summaries []history.SeasonScoring
		covered   int
	}{{history.AllShots, all, 20}, {history.NonPenalty, np, 18}} {
		matches := exploreMatches(tc.summaries[0].Teams[0].Matches, nil)
		for i := 0; i < 2; i++ {
			if matches[i].GoalsCovered != (!tc.basis.ExcludesPenalties()) {
				t.Fatalf("basis %v, raw case %d coverage: %+v", tc.basis, i, matches[i])
			}
		}
		// Exercise the Match by match builder directly. Its HTTP view deliberately
		// excludes a non-penalty season with incomplete split coverage.
		teams := exploreTeamsView{TeamSeason: "2025", TeamSeasons: []exploreTeamSeason{{Season: "2025", Teams: []exploreTeamRecord{{ID: "alpha", Matches: matches}}}}}
		query := url.Values{"trend-view": {"difference"}, "trend-mode": {"match"}}
		if tc.basis.ExcludesPenalties() {
			query.Set("penalties", "exclude")
		}
		trend, err := exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		totals := exploreMarginTiles(matches)[0]
		if totals.GoalsCovered != tc.covered || len(trend.TrendMarginTiles) != 3 {
			t.Fatalf("season coverage: %+v", totals)
		}
		h2h, err := history.SummarizeHeadToHead(archive, "alpha", "bravo", tc.basis)
		if err != nil {
			t.Fatal(err)
		}
		if h2h.AllExpected.GoalsCovered != tc.covered {
			t.Fatalf("h2h coverage: %+v", h2h.AllExpected)
		}
		path := "/explore?view=head-to-head&team=alpha&opponent=bravo"
		if tc.basis.ExcludesPenalties() {
			path += "&penalties=exclude"
		}
		code, body := getPage(t, archive, path)
		if code != http.StatusOK {
			t.Fatal(code)
		}
		if tc.basis.ExcludesPenalties() {
			requireText(t, body, "[data-h2h-tile=All]", "18/20 with split", "npxG 18/20 meetings")
		} else {
			requireText(t, body, "[data-h2h-tile=All]", "xG 19/20 meetings")
			requireText(t, body, "[data-h2h-coverage]", "1 meeting lacks xG.")
		}
	}
	if np[0].TeamComparisonEligible() {
		t.Fatal("Match by match must retain non-penalty season exclusion")
	}

}

func TestExploreSeasonMarginsTilesAndRolling(t *testing.T) {
	t.Parallel()
	archive := testArchive(t, map[string]archiveState{"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19}})
	path := "/explore?view=season-trend&team=alpha&season=2025&trend-view=difference"
	code, body := getPage(t, archive, path+"&smoothing=none")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireExact(t, body, "[data-trend-view] option[value=difference]", "Margins")
	forbidElements(t, body, "[data-trend-margin-tiles][hidden]")
	if len(find(t, body, "[data-trend-margin-tiles] [data-h2h-tile]")) != 3 {
		t.Fatal("expected three tiles")
	}
	requireText(t, body, "[data-trend-margin-tiles]", "xG incomplete", "xG 19/20 matches", "20 matches")
	requireText(t, body, "[data-trend-headings]", "Goal margin", "xG margin")
	_, rolling := getPage(t, archive, path+"&smoothing=5")
	requireAttr(t, rolling, "[data-trend-margin-tiles]", "hidden", "")
}

func TestExploreHeadToHeadSeriesCanonicalRedirect(t *testing.T) {
	t.Parallel()
	archive := testArchive(t, map[string]archiveState{"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19}})
	for _, series := range []string{"expected", "actual", "both", "points"} {
		query := url.Values{"view": {"head-to-head"}, "team": {"alpha"}, "opponent": {"bravo"}, "h2h-series": {series}, "h2h-sort": {"margin"}, "h2h-order": {"asc"}}
		response := httptest.NewRecorder()
		NewHandler(&archiveHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/explore?"+query.Encode(), nil))
		if series == "expected" || series == "actual" {
			if response.Code != http.StatusFound {
				t.Fatalf("%s: status %d", series, response.Code)
			}
			target, err := url.Parse(response.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			query.Del("h2h-series")
			if target.Path != "" || target.Query().Encode() != query.Encode() {
				t.Fatalf("canonical redirect: %s", target)
			}
		} else if response.Code != http.StatusOK {
			t.Fatalf("%s must be preserved: %d", series, response.Code)
		}
	}
}

func TestExploreMarginCoverageLabelAndNullPayload(t *testing.T) {
	t.Parallel()
	archive := testArchive(t, map[string]archiveState{"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19}})
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	matches := exploreMatches(summaries[0].Teams[0].Matches, nil)

	found := false
	for _, match := range matches {
		if match.Values[exploreMargin].Expected != nil {
			continue
		}
		found = true
		if !match.GoalsCovered {
			t.Fatal("missing xG must leave official goals covered")
		}
		data, err := json.Marshal(match)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"expected":null`) {
			t.Fatalf("missing xG was replaced with zero: %s", data)
		}
	}
	if !found {
		t.Fatal("fixture must contain missing xG")
	}
	// The same formatter supplies both views' coverage labels.
	tile := formatMarginTiles([]exploreMarginTile{{Label: "All", Played: 27, GoalsCovered: 27, XGCovered: 25}}, history.AllShots, "match")[0]
	if tile.XGCoverage != "xG 25/27 matches" || tile.XG != "xG incomplete" {
		t.Fatalf("coverage: %+v", tile)
	}
}
