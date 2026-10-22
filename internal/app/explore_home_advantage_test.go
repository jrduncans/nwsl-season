package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestExploreHomeAdvantageRendersEligibleSeasonsFromOneArchiveRead(t *testing.T) {
	// Every archive game is a 1-2 home loss, so each gap is easy to read:
	// goals -1, points -3 and, where complete, xG +1 (home 1 against away 0).
	store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2018": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
		"2019": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19},
		"2022": {Lifecycle: cache.SourceScopeCompleted, Inventory: cache.InventoryCompletenessIncomplete, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3, XGCovered: 20},
	})}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=home-advantage", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if store.archiveCalls != 1 || store.seasonCalls != 0 || store.statusCalls != 0 {
		t.Errorf("reads: archive=%d season=%d status=%d, want one archive read only", store.archiveCalls, store.seasonCalls, store.statusCalls)
	}
	requireExact(t, body, "title", "Home advantage · Explore · NWSL Season Explorer")
	requireAttr(t, body, `a[data-view-choice="home-advantage"]`, "aria-current", "page")
	requireExact(t, body, "#explore-home-title", "Home advantage per match (regular-season)")
	requireAttr(t, body, `canvas[data-chart="home-goals"]`, "role", "img")
	requireAttr(t, body, `canvas[data-chart="home-points"]`, "role", "img")

	// The table lists eligible seasons, newest first, and leaves unavailable
	// expected values labeled rather than zero.
	bySeason := map[string]string{}
	var seasons []string
	for _, row := range find(t, body, `[data-home-rows] tr`) {
		season := text(findIn(t, row, "th")[0])
		seasons = append(seasons, season)
		bySeason[season] = text(row)
	}
	if got := strings.Join(seasons, ","); got != "2026,2019,2018" {
		t.Errorf("table seasons = %s, want 2026,2019,2018", got)
	}
	cells := func(season string) string { return bySeason[season] }
	// Season, matches, goals, xG, points, xPoints, home win–draw–loss share.
	if got, want := cells("2018"), "2018 20 -1.00 +1.00 -3.00 Unavailable 0% – 0% – 100%"; got != want {
		t.Errorf("2018 row = %q, want %q", got, want)
	}
	if got, want := cells("2019"), "2019 20 -1.00 Unavailable -3.00 Unavailable 0% – 0% – 100%"; got != want {
		t.Errorf("2019 row = %q, want %q (partial xG leaves its gap, actual values remain)", got, want)
	}

	requireText(t, body, "[data-panel=home-advantage]",
		"2026: in progress through 20 matches",
		"xG is incomplete in 2019, so those seasons have no xG mark.",
		"xPoints is incomplete in 2018, 2019, 2026, so those seasons have no xPoints mark.",
		"ASA has no neutral-site flag",
		"not a cause",
	)
	forbidText(t, body, "[data-panel=home-advantage]", "Points include penalties")
	data := text(find(t, body, "#explore-home-data")[0])
	if strings.Contains(data, `"2022"`) || !strings.Contains(data, `"season":"2019"`) || !strings.Contains(data, `"xg":null`) {
		t.Errorf("home advantage chart data = %s, want eligible seasons only with a null xG for 2019", data)
	}
}

func TestExploreHomeAdvantageNonPenaltyBasisChangesOnlyGoalsAndXG(t *testing.T) {
	archive := penaltyArchive(t, 20)
	_, all := getPage(t, archive, "/explore?view=home-advantage")
	code, np := getPage(t, archive, "/explore?view=home-advantage&penalties=exclude")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	// All shots: goals -1 (1 against 2), xG +1. The 20 home penalties (one goal
	// and .76 xG each) leave non-penalty goals at -2 and npxG at +0.24.
	requireText(t, all, "[data-home-rows]", "-1.00", "+1.00", "-3.00")
	requireText(t, np, "[data-home-rows]", "-2.00", "+0.24", "-3.00")
	requireText(t, np, "[data-panel=home-advantage]", "Points include penalties; ASA publishes no non-penalty xPoints.")
	requireText(t, np, "[data-home-rows]", "0% – 0% – 100%")
	requireExact(t, np, "thead a[data-home-sort=goals]", "Non-penalty goals")
	requireExact(t, np, "thead a[data-home-sort=xg]", "npxG")
	requireExact(t, np, "thead a[data-home-sort=points]", "Points")
	requireExact(t, all, "thead a[data-home-sort=goals]", "Goals")
	requireAttr(t, np, "thead a[data-home-sort=goals]", "href", "?home-order=desc&home-sort=goals&penalties=exclude&view=home-advantage")
	forbidText(t, all, "[data-panel=home-advantage]", "Points include penalties")

	// A season missing a penalty breakdown is incomplete on this basis, exactly
	// as in the Scoring trend.
	_, partial := getPage(t, penaltyArchive(t, 19), "/explore?view=home-advantage&penalties=exclude")
	requireText(t, partial, "[data-panel=home-advantage]", "No seasons currently have enough complete results to compare.")
}

func TestExploreHomeAdvantageSortUsesUnroundedValuesAndKeepsMissingLast(t *testing.T) {
	value := func(v float64) *float64 { return &v }
	build := func() []exploreHomeAdvantageRow {
		return []exploreHomeAdvantageRow{
			// 0.5004 and 0.5001 both display as 0.50.
			{Season: "2021", Played: 20, GoalsValue: value(.5001), XGValue: value(.1), HomeWinShare: .5},
			{Season: "2022", Played: 22, GoalsValue: value(.5004), HomeWinShare: .5},
			{Season: "2023", Played: 20, GoalsValue: value(-.2), XGValue: value(.3), HomeWinShare: .55},
		}
	}
	for _, test := range []struct {
		column, order, want string
	}{
		{"goals", "desc", "2022,2021,2023"},
		{"goals", "asc", "2023,2021,2022"},
		// The missing xG sorts last in both directions.
		{"xg", "desc", "2023,2021,2022"},
		{"xg", "asc", "2021,2023,2022"},
		{"matches", "desc", "2022,2023,2021"},
		{"results", "desc", "2023,2022,2021"},
		{"season", "asc", "2021,2022,2023"},
		{"season", "desc", "2023,2022,2021"},
		// Equal values keep the newest season first.
		{"results", "asc", "2022,2021,2023"},
	} {
		rows := build()
		sortExploreHomeAdvantage(rows, test.column, test.order)
		var got []string
		for _, row := range rows {
			got = append(got, row.Season)
		}
		if joined := strings.Join(got, ","); joined != test.want {
			t.Errorf("%s %s = %s, want %s", test.column, test.order, joined, test.want)
		}
	}
}

func TestExploreHomeAdvantageSortedURLRendersOpenSortableTable(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{
		"2018": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2019": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
	})
	_, closed := getPage(t, archive, "/explore?view=home-advantage")
	forbidRaw(t, closed, `data-home-values open`)
	_, body := getPage(t, archive, "/explore?view=home-advantage&home-sort=season&home-order=asc")
	requireAttr(t, body, `details[data-home-values]`, "open", "")
	requireAttr(t, body, `th[aria-sort=ascending]`, "aria-sort", "ascending")
	requireText(t, body, "[data-home-rows]", "2018", "2019")
	if first := text(find(t, body, `[data-home-rows] th`)[0]); first != "2018" {
		t.Errorf("first row = %s, want 2018 on an ascending season sort", first)
	}
	// The sort links keep the rest of the selection and flip direction.
	requireAttr(t, body, "thead a[data-home-sort=season]", "href", "?home-order=desc&home-sort=season&view=home-advantage")
	requireAttr(t, body, "thead a[data-home-sort=goals]", "href", "?home-order=desc&home-sort=goals&view=home-advantage")
}

func TestExploreHomeAdvantageEmptyArchiveStillRenders(t *testing.T) {
	code, body := getPage(t, testArchive(t, map[string]archiveState{}), "/explore?view=home-advantage")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	requireText(t, body, "[data-panel=home-advantage]", "No seasons currently have enough complete results to compare.")
	forbidRaw(t, body, `data-chart="home-goals"`)
}

func TestExploreHomeAdvantageViewMatchesHistoryCalculation(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 4, XGCovered: 20}})
	seasons, err := history.SummarizeHomeAdvantage(archive, history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	view, err := exploreHomeAdvantage(nil, seasons, history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	// 2-2 draws: no goal or point gap, and a 0% – 100% – 0% result split.
	if len(view.HomeAdvantageRows) != 1 || view.HomeAdvantageRows[0].Goals != "0.00" || view.HomeAdvantageRows[0].Points != "0.00" || view.HomeAdvantageRows[0].Results != "0% – 100% – 0%" {
		t.Fatalf("rows = %+v", view.HomeAdvantageRows)
	}
}
