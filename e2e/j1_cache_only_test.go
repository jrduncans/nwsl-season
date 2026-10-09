//go:build e2e

package e2e

import (
	"regexp"
	"strconv"
	"sync"
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
}

// cachePages lists the pages that must render from the local cache alone.
// The numbers come from the fixture: 16 teams, 240 games, 120 played.
func cachePages() []cachePage {
	season := "seasons/" + currentSeason + "/"
	standings := cachePage{
		heading: "Standings",
		counts:  []selectorCount{{"table.standings tbody tr", fixtureTeams}},
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

// identity is what a page shows as its headline: the h1 and the document title.
type identity struct{ heading, title string }

func pageIdentity(t *testing.T, page playwright.Page) identity {
	t.Helper()
	heading, err := page.Locator("h1").First().TextContent()
	if err != nil {
		t.Fatalf("read h1 of %s: %v", page.URL(), err)
	}
	title, err := page.Title()
	if err != nil {
		t.Fatalf("read title of %s: %v", page.URL(), err)
	}
	return identity{heading, title}
}

// visitCachePages visits every cache page at every viewport in parallel, each
// in its own browser context. It returns after all of them finish, because a
// parent test waits for its parallel subtests. Subtests must only read the
// shared fixture. Each page's identity is stored in seen; when compare is
// true it must equal what an earlier phase stored.
func visitCachePages(t *testing.T, f *fixture, seen *sync.Map, compare bool) {
	t.Helper()
	for _, vp := range []viewport{Desktop, Mobile} {
		for _, want := range cachePages() {
			t.Run(vp.Name+" /"+want.path, func(t *testing.T) {
				t.Parallel()
				page := newPage(t, vp)
				visit(t, page, f.URL(want.path))
				assertRendersData(t, page, want)
				assertNoHorizontalOverflow(t, page)

				key := vp.Name + " " + want.path
				got := pageIdentity(t, page)
				if !compare {
					seen.Store(key, got)
					return
				}
				before, ok := seen.Load(key)
				if !ok {
					t.Fatalf("no ASA-up identity recorded for %s", key)
				}
				if before != any(got) {
					t.Errorf("page changed while ASA was down: up %+v, down %+v", before, got)
				}
			})
		}
	}
}

// TestJ1CacheOnlyPages is journey J1: every page loads real data from the
// cache with no browser errors or overflow and without contacting ASA, and
// looks the same while ASA is down.
//
// The two phases run one after the other on a shared fixture: SetDown and
// Requests are fixture-wide, so the phase that asserts zero ASA requests must
// not overlap the phase that takes ASA down. Pages within a phase run in
// parallel.
func TestJ1CacheOnlyPages(t *testing.T) {
	f := newFixture(t)
	f.ASA.ResetRequests()

	var seen sync.Map
	t.Run("ASA up", func(t *testing.T) { visitCachePages(t, f, &seen, false) })
	assertNoASARequests(t, f.ASA)

	f.ASA.SetDown(true)
	t.Run("ASA down", func(t *testing.T) { visitCachePages(t, f, &seen, true) })
	assertNoASARequests(t, f.ASA)
}
