//go:build e2e

package e2e

import (
	"regexp"
	"strconv"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

// selectorCount is a CSS selector and the number of elements it must match.
type selectorCount struct {
	selector string
	count    int
}

// cachePage describes one page that must render real data from the local
// cache. A 2xx and a heading are not enough: some pages answer 200 with a
// degraded notice instead of data, so each page also names data markers.
type cachePage struct {
	// path is relative to the mount prefix.
	path string
	// heading is the exact h1 text.
	heading string
	// text must all appear in the page text; each is a string or *regexp.Regexp.
	text []any
	// absent must not appear in the page text: the page's degraded notices.
	absent []string
	// counts are selectors that must match exactly this many elements.
	counts []selectorCount
	// attached are selectors that must match at least one element after the
	// page's scripts have run.
	attached []string
	// check, when set, makes further assertions about the loaded page.
	check func(t *testing.T, page playwright.Page, f *fixture)
}

// cachePages lists the pages that must render from the local cache alone.
// The numbers come from the fixture: 16 teams, 240 games, 120 played.
func cachePages() []cachePage {
	season := "seasons/" + currentSeason + "/"
	standings := cachePage{
		heading: "Standings",
		counts:  []selectorCount{{"table.standings tbody tr", fixtureTeams}},
		check:   assertStandingsMatchFake,
	}
	home, seasonHome := standings, standings
	home.path = ""
	seasonHome.path = "seasons/" + currentSeason
	return []cachePage{
		home,
		{
			path: "seasons", heading: "Seasons",
			text: []any{"Current season", "Loaded"},
		},
		{
			path: "history", heading: "Scoring by season",
			text: []any{regexp.MustCompile(`Selected ` + currentSeason + `: [0-9.]+ goals per match`)},
		},
		{
			path: "explore", heading: "Explore",
			text: []any{currentSeason + ": in progress through " + strconv.Itoa(fixturePlayedGames) + " matches"},
		},
		seasonHome,
		{
			path: season + "fixtures", heading: "Results and fixtures",
			counts: []selectorCount{{"[data-fixture-home-team]", fixtureGames}},
		},
		{
			path: season + "schedule-difficulty", heading: "Remaining schedule difficulty",
			text:   []any{"Toughest remaining schedule"},
			absent: []string{"Schedule difficulty is unavailable"},
		},
		{
			path: season + "forecast", heading: "Forecast lab",
			counts: []selectorCount{{"table.forecast-table tbody tr", fixtureTeams}},
		},
		{
			path: season + "model-evaluation", heading: "Forecast model evaluation",
			// The chart is drawn by script; a failed draw replaces it with a warning.
			attached: []string{"[data-evaluation-svg] path"},
			counts:   []selectorCount{{"[data-evaluation-chart] .data-warning", 0}},
		},
		{
			path: season + "clinching", heading: "Clinching scenarios",
			text:   []any{"Can clinch the playoffs"},
			absent: []string{"Scenario calculations are unavailable.", "Clinching scenarios is unavailable"},
		},
	}
}

// assertRendersData checks the page's data markers.
func assertRendersData(t *testing.T, page playwright.Page, want cachePage) {
	t.Helper()
	expect := playwright.NewPlaywrightAssertions()
	if err := expect.Locator(page.Locator("h1").First()).ToHaveText(want.heading); err != nil {
		t.Errorf("%s heading: %v", want.path, err)
	}
	body := page.Locator("body")
	for _, text := range want.text {
		if err := expect.Locator(body).ToContainText(text); err != nil {
			t.Errorf("%s should show %v: %v", want.path, text, err)
		}
	}
	for _, text := range want.absent {
		if err := expect.Locator(body).Not().ToContainText(text); err != nil {
			t.Errorf("%s shows degraded notice %q: %v", want.path, text, err)
		}
	}
	for _, c := range want.counts {
		if err := expect.Locator(page.Locator(c.selector)).ToHaveCount(c.count); err != nil {
			t.Errorf("%s should have %d of %q: %v", want.path, c.count, c.selector, err)
		}
	}
	for _, selector := range want.attached {
		if err := expect.Locator(page.Locator(selector).First()).ToBeAttached(); err != nil {
			t.Errorf("%s should have %q: %v", want.path, selector, err)
		}
	}
}

// assertStandingsMatchFake checks that the first sync's standings equal the
// table computed in the test from the fake's results, ordered by points.
func assertStandingsMatchFake(t *testing.T, page playwright.Page, f *fixture) {
	t.Helper()
	want := expectedTable(f.Season.Teams, f.Season.Games)
	var played int
	for _, row := range want {
		played += row.Played
	}
	if played != 2*fixturePlayedGames {
		t.Fatalf("the scenario has %d played team-games, want %d", played, 2*fixturePlayedGames)
	}
	rows := readStandings(t, page)
	assertRecords(t, rows, want)
	// Every team has played the same number of games, so the table is
	// ordered by points.
	for i := 1; i < len(rows); i++ {
		if rows[i].Points > rows[i-1].Points {
			t.Errorf("row %d (%s, %d points) is ranked below %s with %d points", i, rows[i].ID, rows[i].Points, rows[i-1].ID, rows[i-1].Points)
		}
	}
}

// TestJ1CacheOnlyPages is journeys J1 and J2: after the first sync, every page
// loads real data from the cache with no browser errors or overflow and
// without contacting ASA, and the standings equal the table computed from the
// fake's results. Pages run in parallel, each in its own browser context, and
// only read the shared fixture.
//
// The fake records every request, so "no requests while browsing" already
// proves the pages work with ASA down; a second pass with the fake down would
// load every page again to show nothing new.
func TestJ1CacheOnlyPages(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// The fixture's CheckNow must have reached the fake, or "no requests
	// after the reset" would hold for the wrong reason.
	if len(f.ASA.Requests()) == 0 {
		t.Fatal("CheckNow made no requests to the fake ASA; the cache was not filled from it")
	}
	f.ASA.ResetRequests()
	// Parallel subtests finish before the parent's cleanups run.
	t.Cleanup(func() { assertNoASARequests(t, f.ASA) })

	for _, vp := range []viewport{Desktop, Mobile} {
		for _, want := range cachePages() {
			t.Run(vp.Name+" /"+want.path, func(t *testing.T) {
				t.Parallel()
				page := newPage(t, vp)
				visit(t, page, f.URL(want.path))
				assertRendersData(t, page, want)
				if want.check != nil {
					want.check(t, page, f)
				}
				assertNoHorizontalOverflow(t, page)
			})
		}
	}
}
