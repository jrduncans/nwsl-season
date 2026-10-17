package app

import (
	"net/http"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestExploreMarginTilesCoverageAndOfficialRecord(t *testing.T) {
	t.Parallel()
	value := func(n float64) *float64 { return &n }
	matches := []exploreMatchRecord{
		{Venue: "Home", Values: [4]exploreTeamValues{{Actual: 2, Expected: value(1.25)}, {Actual: 0, Expected: value(.5)}, {}, {Actual: 3}}},
		{Venue: "Away", Values: [4]exploreTeamValues{{Actual: 0}, {Actual: 1}, {}, {Actual: 0}}},
		// An official win from a penalty becomes level on the selected basis.
		{Venue: "Home", Values: [4]exploreTeamValues{{Actual: 0, Expected: value(.5)}, {Actual: 0, Expected: value(.75)}, {}, {Actual: 3}}},
	}
	for _, np := range []bool{false, true} {
		tiles := exploreMarginTiles(matches, np)
		if len(tiles) != 3 || tiles[0].Record != "2–0–1" || tiles[0].Rate != "2.00 pts/match" || tiles[0].Matches != "3 matches" || tiles[1].Record != "2–0–0" || tiles[2].Record != "0–0–1" {
			t.Fatalf("official record changed: %+v", tiles)
		}
		if np {
			if tiles[0].Goals != "2–0 np goals" || tiles[0].Coverage != "2/3 with split" || tiles[2].Goals != "Goals unavailable" || tiles[0].XG != "npxG incomplete" || tiles[0].XGCoverage != "npxG 2/3 matches" {
				t.Fatalf("non-penalty coverage: %+v", tiles)
			}
		} else if tiles[0].Goals != "2–1 goals" || tiles[0].XG != "xG incomplete" || tiles[0].XGCoverage != "xG 2/3 matches" || tiles[1].XG != "1.75–1.25 xG" {
			t.Fatalf("partial totals presented as complete: %+v", tiles)
		}
	}
	tiles := exploreMarginTiles(matches[:1], false)
	if tiles[2].XG != "No matches" || tiles[2].XGCoverage != "xG 0/0 matches" {
		t.Fatalf("empty away tile: %+v", tiles[2])
	}
}

func TestExploreSeasonMarginsTilesAndRolling(t *testing.T) {
	t.Parallel()
	archive := testArchive(t, map[string]archiveState{"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19}})
	path := "/explore?view=season-trend&team=alpha&season=2025&trend-view=difference"
	code, body := getPage(t, archive, path+"&trend-mode=match")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireExact(t, body, "[data-trend-view] option[value=difference]", "Margins")
	forbidElements(t, body, "[data-trend-margin-tiles][hidden]")
	requireText(t, body, "[data-trend-margin-tiles]", "xG incomplete", "xG 19/20 matches", "20 matches")
	requireText(t, body, "[data-trend-headings]", "Goal margin", "xG margin")
	_, rolling := getPage(t, archive, path+"&trend-mode=rolling")
	requireAttr(t, rolling, "[data-trend-margin-tiles]", "hidden", "")
}
