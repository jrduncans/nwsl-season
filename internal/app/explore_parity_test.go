package app

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestExploreParityRendersEverySeasonFromOneArchiveRead(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioOverview)
	store := &archiveHTTPStore{archive: archive}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=parity", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if store.archiveCalls != 1 || store.seasonCalls != 0 || store.statusCalls != 0 {
		t.Errorf("reads: archive=%d season=%d status=%d, want one archive read only", store.archiveCalls, store.seasonCalls, store.statusCalls)
	}
	requireExact(t, body, "title", "Parity · Explore · NWSL Season Explorer")
	requireAttr(t, body, `a[data-view-choice="parity"]`, "aria-current", "page")
	requireAttr(t, body, `canvas[data-chart="parity"]`, "role", "img")
	requireText(t, body, "[data-panel=parity]",
		"how far teams' points per game (PPG) sit from the league average",
		"random results alone if every team were equally good",
		"2026: in progress through 20 matches (6.7 per team)",
	)
	forbidText(t, body, "[data-panel=parity]", "Points include penalties", "xPoints incomplete")

	// The newest completed season is the default strip, not the active one.
	seasons, err := history.SummarizeParity(archive, history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	var want history.SeasonParity
	for _, season := range seasons {
		if season.Season == "2025" {
			want = season
		}
	}
	requireExact(t, body, "[data-parity-strip-title]", "Teams in 2025")
	requireAttr(t, body, `a[data-parity-season-link="2025"]`, "aria-current", "true")
	requireAttr(t, body, `[data-parity-season] option[value="2025"]`, "selected", "")
	requireText(t, body, "[data-parity-summary]", fmt.Sprintf("Top-to-bottom gap: %.2f PPG", want.Gap), fmt.Sprintf("interquartile range %.2f", want.IQR()))
	if got := len(find(t, body, "[data-parity-teams] tr")); got != len(want.Teams) || got != 6 {
		t.Errorf("strip teams = %d, want 6", got)
	}
	// The no-JS strip lists teams by exact PPG, highest first.
	if first := text(find(t, body, ".explore-parity-list li")[0]); !strings.HasPrefix(first, "Angel City FC: 2.20 PPG") {
		t.Errorf("first strip team = %q", first)
	}

	// Every eligible season is in the table, newest first, with the values the
	// chart receives.
	var rows []string
	for _, row := range find(t, body, `[data-parity-rows] tr`) {
		rows = append(rows, text(findIn(t, row, "th")[0]))
	}
	if got := strings.Join(rows, ","); got != "2026 (in progress),2025,2024,2023,2022,2021,2019,2018,2017,2016" {
		t.Errorf("table seasons = %s", got)
	}
	var data []exploreParityRecord
	if err := json.Unmarshal([]byte(text(find(t, body, "#explore-parity-data")[0])), &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 10 || data[0].Season != "2016" || !data[9].Active {
		t.Fatalf("chart data = %+v", data)
	}
	// Payload values carry six decimal places, so the rounded IQR may differ
	// from the rounded quartiles' difference in the last place.
	for _, record := range data {
		if record.Spread <= 0 || record.Chance <= 0 || record.XSpread == nil || math.Abs(record.IQR-(record.Q3-record.Q1)) > 2e-6 {
			t.Errorf("%s record = %+v", record.Season, record)
		}
	}
	// 2024 has two teams tied on the best PPG, and both are named.
	for _, record := range data {
		if record.Season == "2024" && len(record.Top) != 2 {
			t.Errorf("2024 top = %v, want both tied teams", record.Top)
		}
	}
}

func TestExploreParitySelectsSeasonsAndSeries(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioOverview)
	_, active := getPage(t, archive, "/explore?view=parity&season=2026")
	requireExact(t, active, "[data-parity-strip-title]", "Teams in 2026 (in progress through 20 matches)")
	requireAttr(t, active, `a[data-parity-season-link="2026"]`, "aria-current", "true")

	_, actual := getPage(t, archive, "/explore?view=parity&season=2018")
	requireExact(t, actual, "[data-parity-strip-title]", "Teams in 2018")
	requireAttr(t, actual, "[data-parity-expected-legend]", "hidden", "")
	forbidText(t, actual, ".explore-parity-list", "xPPG")

	_, both := getPage(t, archive, "/explore?view=parity&season=2018&parity-series=both")
	forbidRaw(t, both, `data-parity-expected-legend hidden`)
	requireText(t, both, ".explore-parity-list", "xPPG")
	requireAttr(t, both, `[data-segmented=parity-series] input[value="both"][form=parity-controls]`, "checked", "")
	// Season links keep the rest of the selection.
	requireAttr(t, both, `a[data-parity-season-link="2016"]`, "href", "?parity-series=both&season=2016&view=parity")
	requireAttr(t, both, "thead a[data-parity-sort=spread]", "href", "?parity-order=desc&parity-series=both&parity-sort=spread&season=2018&view=parity")
}

func TestExploreParityFallsBackFromAnUnplottedCatalogSeason(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3},
	})
	// Ten results are enough to compare teams but below Parity's minimum.
	for index := range archive {
		if archive[index].Entry.Season == "2026" {
			archive[index].Data.Games = archive[index].Data.Games[:10]
		}
	}
	code, body := getPage(t, archive, "/explore?view=parity&season=2026")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	requireExact(t, body, "[data-parity-strip-title]", "Teams in 2025")
	requireExact(t, body, "[data-parity-season-note]", "2026 is not plotted, so the strip shows 2025.")
	// Two teams of a 20-match season: every match is a 1-2 away win.
	requireText(t, body, "[data-parity-summary]", "from bravo (3.00) down to alpha (0.00)")
	// The season select offers only seasons with parity values.
	if got := len(find(t, body, `[data-parity-season] option[value="2026"]`)); got != 0 {
		t.Errorf("Parity season select offers 2026 %d times, want none", got)
	}
}

func TestExploreParityIgnoresAndDropsNonPenaltyBasis(t *testing.T) {
	archive := penaltyArchive(t, 20)
	_, all := getPage(t, archive, "/explore?view=parity")
	_, np := getPage(t, archive, "/explore?view=parity&penalties=exclude")
	forbidText(t, np, "[data-panel=parity]", "Points include penalties")
	requireAttr(t, np, "[data-panel=parity] label.penalties-select", "hidden", "")
	forbidRaw(t, np, `data-parity-controls><input type="hidden" name="penalties"`)
	forbidText(t, all, "[data-panel=parity]", "Points include penalties")
	if a, b := text(find(t, all, "[data-parity-rows]")[0]), text(find(t, np, "[data-parity-rows]")[0]); a != b {
		t.Errorf("values changed with the basis: %q vs %q", a, b)
	}
	// Penalty-split coverage cannot affect Parity eligibility.
	_, partial := getPage(t, penaltyArchive(t, 19), "/explore?view=parity&penalties=exclude")
	forbidText(t, partial, "[data-panel=parity]", "No seasons currently have enough complete results to compare.")
}

func TestExploreParitySortUsesUnroundedValuesAndKeepsMissingLast(t *testing.T) {
	value := func(v float64) *float64 { return &v }
	build := func() []exploreParityRow {
		return []exploreParityRow{
			// .5001 and .5004 both display as 0.50.
			{exploreParityRecord: exploreParityRecord{Season: "2021", Spread: .5001, XSpread: value(.3), Matches: 24}},
			{exploreParityRecord: exploreParityRecord{Season: "2022", Spread: .5004, Matches: 22}},
			{exploreParityRecord: exploreParityRecord{Season: "2023", Spread: .2, XSpread: value(.4), Matches: 22}},
		}
	}
	for _, test := range []struct{ column, order, want string }{
		{"spread", "desc", "2022,2021,2023"},
		{"spread", "asc", "2023,2021,2022"},
		{"xspread", "desc", "2023,2021,2022"},
		{"xspread", "asc", "2021,2023,2022"},
		// Equal values keep the newest season first.
		{"matches", "asc", "2023,2022,2021"},
		{"season", "asc", "2021,2022,2023"},
		{"season", "desc", "2023,2022,2021"},
	} {
		rows := build()
		sortExploreParity(rows, test.column, test.order)
		var got []string
		for _, row := range rows {
			got = append(got, row.Season)
		}
		if joined := strings.Join(got, ","); joined != test.want {
			t.Errorf("%s %s = %s, want %s", test.column, test.order, joined, test.want)
		}
	}
}

func TestExploreParitySortedURLRendersOpenSortableTable(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioOverview)
	_, closed := getPage(t, archive, "/explore?view=parity")
	forbidRaw(t, closed, `data-parity-values open`)
	_, body := getPage(t, archive, "/explore?view=parity&parity-sort=spread&parity-order=asc")
	requireAttr(t, body, `details[data-parity-values]`, "open", "")
	requireAttr(t, body, `th[aria-sort=ascending] a`, "data-parity-sort", "spread")
	// The no-JS form keeps the sort when it changes the season.
	requireAttr(t, body, `[data-parity-controls] input[name=parity-sort]`, "value", "spread")
	var spreads []float64
	for _, row := range find(t, body, `[data-parity-rows] tr`) {
		var spread float64
		if _, err := fmt.Sscan(attr(findIn(t, row, "td")[2], "data-value"), &spread); err != nil {
			t.Fatal(err)
		}
		spreads = append(spreads, spread)
	}
	for index := 1; index < len(spreads); index++ {
		if spreads[index] < spreads[index-1] {
			t.Fatalf("spreads not ascending: %v", spreads)
		}
	}
}

func TestExploreParityEmptyArchiveStillRenders(t *testing.T) {
	code, body := getPage(t, testArchive(t, map[string]archiveState{}), "/explore?view=parity")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	requireText(t, body, "[data-panel=parity]", "No seasons currently have enough complete results to compare.")
	forbidRaw(t, body, `data-chart="parity"`, `data-parity-strip`)
}
