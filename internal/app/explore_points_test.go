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
		{"All", "4", "4.00 xPts", "0.00 above expected", "1.33 pts vs 1.33 xPts per match", "3 of 3 matches"},
		{"Home", "3", "1.40 xPts", "1.60 above expected", "1.50 pts vs 0.70 xPts per match", "2 of 2 matches"},
		{"Away", "1", "2.60 xPts", "1.60 below expected", "1.00 pts vs 2.60 xPts per match", "1 of 1 matches"},
	}
	if !reflect.DeepEqual(tiles, want) {
		t.Fatalf("tiles = %+v", tiles)
	}
	matches[0].Values[3].Expected = nil
	tiles = explorePointsTiles(matches, "match")
	if tiles[0].Expected != "xPts incomplete" || tiles[0].Gap != "xPts incomplete" || tiles[0].Coverage != "2 of 3 matches" || tiles[1].Expected != "xPts incomplete" || tiles[2] != want[2] {
		t.Fatalf("incomplete tiles: %+v", tiles)
	}
}

func TestExplorePointsTrendFallback(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	path := "/explore?view=season-trend&season=2025&trend-view=points&trend-mode=match"
	code, body := getPage(t, archive, path)
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireText(t, body, "[data-trend-points-tiles]", "matches")
	if len(find(t, body, "[data-trend-points-tiles] [data-points-tile]")) != 3 {
		t.Fatal("missing venue tiles")
	}
	requireText(t, body, "[data-trend-sort=points-gap]", "Points − xPoints")
	requireAttr(t, body, "[data-trend-points-tiles]", "class", "explore-h2h-record")
	_, rolling := getPage(t, archive, strings.Replace(path, "trend-mode=match", "trend-mode=rolling", 1))
	requireAttr(t, rolling, "[data-trend-points-tiles]", "hidden", "")
	requireElements(t, rolling, "[data-trend-sort=points-gap]")
	for _, bad := range []string{"trend-sort=", "trend-sort=unknown", "trend-sort=points-gap&trend-sort=points-gap", "trend-order=", "trend-order=sideways"} {
		code, _ := getPage(t, archive, path+"&"+bad)
		if code != http.StatusBadRequest {
			t.Fatalf("accepted %s", bad)
		}
	}
	_, incomplete := getPage(t, archive, strings.Replace(path, "season=2025", "season=2026&team=315VnJ759x", 1))
	requireText(t, incomplete, "[data-trend-points-tiles] [data-points-tile=All]", "xPts incomplete")
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
	requireAttr(t, body, "[data-h2h-series] option[value=points]", "selected", "")
	requireAttr(t, body, "[data-h2h-penalties-label]", "hidden", "")
	requireText(t, body, "[data-h2h-fallback] li", "Points − xPoints:")
	requireText(t, body, "[data-h2h-fallback] li", "Points:")
	requireText(t, body, "[data-h2h-rows]", "+")
	requireText(t, body, "[data-h2h-points-tiles]", "26 of 26 meetings")
	requireText(t, body, "[data-h2h-points-tiles] [data-points-tile=Home]", "13 of 13 meetings")
	requireText(t, body, "[data-h2h-points-tiles] [data-points-tile=Away]", "13 of 13 meetings")
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
	requireText(t, incomplete, "[data-h2h-points-tiles] [data-points-tile=All]", "xPts incomplete")
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
		if tiles[2].Actual != want || tiles[2].Expected != "" || tiles[2].Gap != "" || tiles[2].Rate != "" {
			t.Fatalf("empty venue: %+v", tiles[2])
		}
	}
}

func TestExplorePointsSortFormState(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	path := "/explore?view=season-trend&season=2025&trend-view=points&trend-mode=match"
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
	for _, selection := range []string{"trend-view=compare&trend-mode=rolling", "trend-view=balance&trend-mode=match"} {
		response := httptest.NewRecorder()
		NewHandler(&archiveHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/explore?view=season-trend&"+selection+"&trend-sort=points-gap&trend-order=desc", nil))
		if response.Code != http.StatusFound {
			t.Fatalf("stale sort status: %d", response.Code)
		}
		target, err := url.Parse(response.Header().Get("Location"))
		if err != nil || target.Path != "" || target.Query().Has("trend-sort") || target.Query().Has("trend-order") {
			t.Fatalf("canonical sort redirect: %s, %v", target, err)
		}
		if target.Query().Get("trend-mode") == "" || target.Query().Get("view") != "season-trend" {
			t.Fatal("redirect lost selection")
		}
	}
}
