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

	"github.com/jrduncans/nwsl-season/internal/apptest"
)

func TestExplorePointsTiles(t *testing.T) {
	t.Parallel()
	xp := func(v float64) *float64 { return &v }
	matches := []exploreMatchRecord{
		{ID: "a", Venue: "Home", Values: [4]exploreTeamValues{3: {Actual: 3, Expected: xp(.4)}}},
		{ID: "b", Venue: "Away", Values: [4]exploreTeamValues{3: {Actual: 1, Expected: xp(2.6)}}},
		{ID: "c", Venue: "Home", Values: [4]exploreTeamValues{3: {Actual: 0, Expected: xp(1)}}},
	}
	tiles := explorePointsTiles(matches, "match")
	want := []explorePointsTile{
		{Label: "All", Actual: "4", Expected: "4.00 xPoints", Gap: "0.00 above expected"},
		{Label: "Home", Actual: "3", Expected: "1.40 xPoints", Gap: "1.60 above expected"},
		{Label: "Away", Actual: "1", Expected: "2.60 xPoints", Gap: "1.60 below expected"},
	}
	if !reflect.DeepEqual(tiles, want) {
		t.Fatalf("tiles = %+v", tiles)
	}
	matches[0].Values[3].Expected = nil
	tiles = explorePointsTiles(matches, "match")
	if tiles[0].Expected != "xPoints incomplete" || tiles[0].Gap != "xPoints incomplete" || tiles[0].Coverage != "2 of 3 matches" || tiles[1].Expected != "xPoints incomplete" || tiles[2] != want[2] {
		t.Fatalf("incomplete tiles: %+v", tiles)
	}
}

// The shift from the season's average match applies to points and xPoints
// alike, so adjusted tiles keep the official lines and gaps; only totals move.
func TestExploreAdjustedPointsTiles(t *testing.T) {
	t.Parallel()
	matches := []exploreMatchRecord{
		{ID: "a", Venue: "Home", Values: [4]exploreTeamValues{3: {Actual: 3, Expected: new(.4)}}, Opponents: &exploreOpponentFactors{Scored: 1, Allowed: 1, ExpectedPoints: 1.9}},
		{ID: "b", Venue: "Away", Values: [4]exploreTeamValues{3: {Actual: 1, Expected: new(2.6)}}, Opponents: &exploreOpponentFactors{Scored: 1, Allowed: 1, ExpectedPoints: .9}},
	}
	adjusted := opponentAdjustedMatches(matches, 1.4)
	near := func(name string, got, want float64) {
		if math.Abs(got-want) > 1e-12 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	near("easy win", adjusted[0].Values[3].Actual, 2.5)
	near("easy win xPoints", *adjusted[0].Values[3].Expected, -.1)
	near("hard draw", adjusted[1].Values[3].Actual, 1.5)
	near("hard draw xPoints", *adjusted[1].Values[3].Expected, 3.1)
	tiles := exploreAdjustedPointsTiles(adjusted)
	official := explorePointsTiles(matches, "match")
	want := []explorePointsTile{
		{Label: "All", Actual: "4.00", Expected: "3.00 xPoints", Gap: "1.00 above expected"},
		{Label: "Home", Actual: "2.50", Expected: "-0.10 xPoints", Gap: "2.60 above expected"},
		{Label: "Away", Actual: "1.50", Expected: "3.10 xPoints", Gap: "1.60 below expected"},
	}
	if !reflect.DeepEqual(tiles, want) {
		t.Fatalf("adjusted tiles = %+v", tiles)
	}
	for index := range tiles {
		if tiles[index].Gap != official[index].Gap {
			t.Errorf("%s gap = %q, want the official %q", tiles[index].Label, tiles[index].Gap, official[index].Gap)
		}
	}
	// Without xPoints, coverage qualifies the total; without factors, no total.
	matches[1].Values[3].Expected = nil
	matches[0].Opponents = nil
	tiles = exploreAdjustedPointsTiles(opponentAdjustedMatches(matches, 1.4))
	if tiles[0].Actual != "Points unavailable" || tiles[1].Actual != "Points unavailable" || tiles[2].Actual != "1.50" || tiles[2].Expected != "xPoints incomplete" {
		t.Fatalf("partial tiles: %+v", tiles)
	}
}

func TestExplorePointsTrendFallback(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	path := "/explore?view=season-trend&season=2025&trend-view=points&smoothing=none"
	code, body := getPage(t, archive, path)
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireText(t, body, "[data-trend-points-tiles]", "expected")
	if len(find(t, body, "[data-trend-points-tiles] [data-points-tile]")) != 3 {
		t.Fatal("missing venue tiles")
	}
	requireText(t, body, "[data-trend-sort=points-gap]", "Points − xPoints")
	requireAttr(t, body, "[data-trend-points-tiles]", "class", "explore-h2h-record")
	_, rolling := getPage(t, archive, strings.Replace(path, "smoothing=none", "smoothing=5", 1))
	requireAttr(t, rolling, "[data-trend-points-tiles]", "hidden", "")
	requireElements(t, rolling, "[data-trend-sort=points-gap]")
	for _, bad := range []string{"trend-sort=", "trend-sort=unknown", "trend-sort=points-gap&trend-sort=points-gap", "trend-order=", "trend-order=sideways"} {
		code, _ := getPage(t, archive, path+"&"+bad)
		if code != http.StatusBadRequest {
			t.Fatalf("accepted %s", bad)
		}
	}
	_, incomplete := getPage(t, archive, strings.Replace(path, "season=2025", "season=2026&team=315VnJ759x", 1))
	requireText(t, incomplete, "[data-trend-points-tiles] [data-points-tile=All]", "xPoints incomplete")
	requireText(t, incomplete, "[data-trend-points-tiles] [data-points-tile=All]", "28 of 29 matches")
	for _, key := range []string{"trend-sort", "trend-order"} {
		requireAttr(t, body, "[data-season-trend-controls] input[name="+key+"]", "disabled", "")
	}
	for _, order := range []string{"asc", "desc"} {
		_, sorted := getPage(t, archive, path+"&trend-sort=points-gap&trend-order="+order)
		rows := find(t, sorted, "[data-trend-rows] tr")
		if len(rows) == 0 {
			t.Fatal("no sorted rows")
		}
		previous := math.Inf(-1)
		if order == "desc" {
			previous = math.Inf(1)
		}
		for _, cell := range find(t, sorted, "[data-trend-rows] tr td:last-child") {
			value, err := strconv.ParseFloat(text(cell), 64)
			if err != nil {
				t.Fatal(err)
			}
			if order == "asc" && value < previous || order == "desc" && value > previous {
				t.Fatal("gap sort order")
			}
			previous = value
		}
		requireAttr(t, sorted, "[data-trend-sort=points-gap]", "data-trend-sort", "points-gap")
	}
}

func TestExploreHeadToHeadPoints(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	path := "/explore?view=head-to-head&opponent=" + h2hSeattleID + "&h2h-series=points"
	code, body := getPage(t, archive, path)
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireAttr(t, body, "[data-segmented=h2h-series] input[value=points]", "checked", "")
	requireAttr(t, body, "[data-h2h-penalties-label]", "hidden", "")
	requireText(t, body, "[data-h2h-fallback] li", "Points − xPoints:")
	requireText(t, body, "[data-h2h-fallback] li", "Points:")
	requireText(t, body, "[data-h2h-rows]", "+")
	requireText(t, body, "[data-h2h-points-tiles]", "expected")
	if len(find(t, body, "[data-h2h-points-tiles] .note")) != 0 {
		t.Fatal("complete coverage still stated")
	}
	for _, link := range find(t, body, "[data-h2h-sort]") {
		target, err := url.Parse(attr(link, "href"))
		if err != nil || target.Query().Get("h2h-series") != "points" {
			t.Fatal("points state lost")
		}
	}
	_, excluded := getPage(t, archive, path+"&penalties=exclude")
	if text(find(t, body, "[data-h2h-points-tiles]")[0]) != text(find(t, excluded, "[data-h2h-points-tiles]")[0]) {
		t.Fatal("points tiles changed with penalties")
	}
	for i := range archive {
		for j := range archive[i].Data.XGoals {
			archive[i].Data.XGoals[j].HomeXPoints.Valid = false
		}
	}
	_, incomplete := getPage(t, archive, path)
	requireText(t, incomplete, "[data-h2h-points-tiles] [data-points-tile=All]", "xPoints incomplete")
	requireText(t, incomplete, "[data-h2h-fallback]", "Points − xPoints: Unavailable")
}

func TestExplorePointsEmptyVenue(t *testing.T) {
	t.Parallel()
	for _, unit := range []string{"match", "meeting"} {
		tiles := explorePointsTiles([]exploreMatchRecord{{Venue: "Home"}}, unit)
		want := "No matches"
		if unit == "meeting" {
			want = "No meetings"
		}
		if tiles[2].Actual != want || tiles[2].Expected != "" || tiles[2].Gap != "" || tiles[2].Coverage != "" {
			t.Fatalf("empty venue: %+v", tiles[2])
		}
	}
}

func TestExplorePointsSortFormState(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	path := "/explore?view=season-trend&season=2025&trend-view=points&smoothing=none"
	for _, order := range []string{"asc", "desc"} {
		_, body := getPage(t, archive, path+"&trend-sort=points-gap&trend-order="+order)
		input := find(t, body, "[data-season-trend-controls] input[name=trend-sort]")[0]
		if _, disabled := lookupAttr(input, "disabled"); disabled {
			t.Fatal("gap sort must submit")
		}
		orderInput := find(t, body, "[data-season-trend-controls] input[name=trend-order]")[0]
		if _, disabled := lookupAttr(orderInput, "disabled"); disabled != (order == "asc") {
			t.Fatal("only non-default order should submit")
		}
		target, err := url.Parse(attr(find(t, body, "[data-trend-sort=points-gap]")[0], "href"))
		if err != nil {
			t.Fatal(err)
		}
		if target.Query().Has("trend-order") != (order == "asc") {
			t.Fatal("ascending links should omit default order")
		}
	}
	for _, selection := range []string{"trend-view=compare&smoothing=5", "trend-view=balance&smoothing=none"} {
		response := httptest.NewRecorder()
		NewHandler(&archiveHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/explore?view=season-trend&"+selection+"&trend-sort=points-gap&trend-order=desc", nil))
		if response.Code != http.StatusFound {
			t.Fatalf("stale sort status: %d", response.Code)
		}
		target, err := url.Parse(response.Header().Get("Location"))
		if err != nil || target.Path != "" || target.Query().Has("trend-sort") || target.Query().Has("trend-order") {
			t.Fatalf("canonical sort redirect: %s, %v", target, err)
		}
		if target.Query().Get("smoothing") == "" || target.Query().Get("view") != "season-trend" {
			t.Fatal("redirect lost selection")
		}
	}
}
