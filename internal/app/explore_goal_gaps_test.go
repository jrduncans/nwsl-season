package app

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestExploreGoalGapTilesAndCoverage(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	path := "/explore?view=season-trend&season=2025&team=odMX2OJqYL&trend-view=compare&smoothing=none"
	code, body := getPage(t, archive, path)
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireExact(t, body, "[data-trend-view] option[value=compare]", "Goals vs xG")
	for _, venue := range []string{"All", "Home", "Away"} {
		requireText(t, body, "[data-trend-goal-gap-tiles]", venue)
	}
	requireText(t, body, "[data-trend-goal-gap-tiles]", "Scored")
	requireText(t, body, "[data-trend-goal-gap-tiles]", "Allowed")
	requireText(t, body, "[data-trend-goal-gap-tiles]", " vs ")
	requireText(t, body, "[data-trend-rows]", "+0.90")
	requireText(t, body, "[data-trend-rows]", "-0.70")
	for _, key := range []string{"scored-gap", "allowed-gap"} {
		for _, order := range []string{"asc", "desc"} {
			code, sorted := getPage(t, archive, path+"&trend-sort="+key+"&trend-order="+order)
			if code != http.StatusOK {
				t.Fatal(code)
			}
			requireAttr(t, sorted, "[data-trend-sort='"+key+"']", "data-trend-sort", key)
			rows := find(t, sorted, "[data-trend-rows] tr")
			column := 10
			if key == "allowed-gap" {
				column = 11
			}
			previous := -1e9
			if order == "desc" {
				previous = 1e9
			}
			for _, row := range rows {
				cells := findIn(t, row, "td")
				var value float64
				if _, err := fmt.Sscan(text(cells[column-1]), &value); err != nil {
					t.Fatal(err)
				}
				if order == "asc" && value < previous || order == "desc" && value > previous {
					t.Fatal("gaps out of order")
				}
				previous = value
			}
		}
	}
	_, rolling := getPage(t, archive, "/explore?view=season-trend&season=2025&team=odMX2OJqYL&trend-view=compare&smoothing=5")
	requireElements(t, rolling, "[data-trend-goal-gap-tiles][hidden]")
}

func TestExploreGoalGapTotalsAreCompletePopulations(t *testing.T) {
	t.Parallel()
	xgFor, xgAgainst := 1.25, .5
	rows := []exploreMatchRecord{{Venue: "Home", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 2, Expected: &xgFor}, {Actual: 0, Expected: &xgAgainst}}}}
	tiles := exploreGoalGapTiles(rows, history.AllShots, "meeting")
	if fmt.Sprint(tiles[0].Sides) != "[{Scored 2 vs 1.25 xG 0.75 above} {Allowed 0 vs 0.50 xG 0.50 below}]" || tiles[2].Empty != "No meetings" || tiles[2].Sides != nil {
		t.Fatalf("%+v", tiles)
	}
	rows = append(rows, exploreMatchRecord{Venue: "Away", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 3}, {Actual: 1}}})
	tiles = exploreGoalGapTiles(rows, history.AllShots, "meeting")
	if fmt.Sprint(tiles[0].Sides) != "[{Scored 5 xG incomplete} {Allowed 1 xG incomplete}]" || tiles[0].Coverage != "1 of 2 meetings with xG" {
		t.Fatalf("%+v", tiles)
	}
	rows[1].GoalsCovered = false
	tiles = exploreGoalGapTiles(rows, history.NonPenalty, "meeting")
	if fmt.Sprint(tiles[0].Sides[0]) != "{Scored Non-penalty goals incomplete }" || tiles[0].SplitCoverage != "1 of 2 meetings with penalty split" {
		t.Fatalf("%+v", tiles)
	}
	if goalGap(rows[1], exploreScored) != nil || goalGap(rows[1], exploreAllowed) != nil {
		t.Fatal("missing split became a bar")
	}
}

func TestExploreGoalGapSortPrecisionAndMissingLast(t *testing.T) {
	t.Parallel()
	var matches []exploreMatchRecord
	for i, expected := range []*float64{nil, new(.1001), new(.1002), new(.1001), new(.1001)} {
		matches = append(matches, exploreMatchRecord{GoalsCovered: i != 4, Values: [4]exploreTeamValues{{Actual: 1, Expected: expected}, {Actual: 1, Expected: expected}}})
	}
	teams := exploreTeamsView{TeamSeason: "2025", TeamSeasons: []exploreTeamSeason{{Season: "2025", Teams: []exploreTeamRecord{{ID: "team", Matches: matches}}}}}
	for _, key := range []string{"scored-gap", "allowed-gap"} {
		for _, order := range []string{"asc", "desc"} {
			query := url.Values{"trend-view": {"compare"}, "trend-mode": {"match"}, "trend-sort": {key}, "trend-order": {order}}
			page, err := exploreSeasonTrend(query, teams, teamNameView{ID: "team"})
			if err != nil {
				t.Fatal(err)
			}
			var got []int
			for _, row := range page.TrendRows {
				got = append(got, row.Number)
			}
			want := []int{3, 4, 2, 5, 1}
			if order == "desc" {
				want = []int{4, 2, 3, 5, 1}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("%s %s: %v, want %v", key, order, got, want)
			}
		}
	}
}

func TestExploreGoalGapDisplayPrecision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		gap          float64
		want, signed string
	}{
		{0, "matched expected", "0.00"},
		{-1e-10, "matched expected", "0.00"},
		{1e-10, "matched expected", "0.00"},
		{-.0049, "matched expected", "0.00"},
		{.0049, "matched expected", "0.00"},
		{-.0051, "0.01 below", "-0.01"},
		{.0051, "0.01 above", "+0.01"},
	} {
		expected := 1 - tc.gap
		rows := []exploreMatchRecord{{Venue: "Home", GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 1, Expected: &expected}, {Actual: 1, Expected: &expected}}}}
		tile := exploreGoalGapTiles(rows, history.AllShots, "match")[0]
		if tile.Sides[0].Gap != tc.want || tile.Sides[1].Gap != tc.want || goalGapText(rows[0], exploreScored) != tc.signed || h2hSignedMargin(tc.gap) != tc.signed {
			t.Fatalf("gap %g: %+v, signed %q", tc.gap, tile, h2hSignedMargin(tc.gap))
		}
	}
	for _, unit := range []string{"match", "meeting"} {
		tiles := exploreGoalGapTiles(nil, history.NonPenalty, unit)
		for _, tile := range tiles {
			if tile.Empty == "" || tile.Sides != nil || tile.Coverage != "" || tile.SplitCoverage != "" {
				t.Fatalf("empty %s tile: %+v", unit, tile)
			}
		}
	}
}
