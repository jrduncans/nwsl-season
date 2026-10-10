//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/server"
)

// exploreBase serves the application from a cache seeded with an apptest
// scenario, as cmd/preview does, and returns the app root with a trailing
// slash. The scheduler is off and the ASA base URL is unroutable, so nothing
// here can reach ASA.
func exploreBase(t *testing.T, scenario string) string {
	t.Helper()
	cfg := testConfig(t)
	ctx := context.Background()
	db, err := cache.Open(ctx, cfg.DBPath)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	apptest.Seed(t, db, scenario)
	if err := db.Close(); err != nil {
		t.Fatalf("close cache: %v", err)
	}
	srv, err := server.Build(ctx, cfg, server.Options{
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		ASABaseURL:         "http://127.0.0.1:1",
		ForecastIterations: e2eForecastIterations,
		StartScheduler:     false,
	})
	if err != nil {
		t.Fatalf("server.Build: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		srv.Wait()
		if err := srv.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	mux := http.NewServeMux()
	mux.Handle(mountPrefix+"/", http.StripPrefix(mountPrefix, srv.Handler()))
	web := httptest.NewServer(mux)
	t.Cleanup(web.Close)
	return web.URL + mountPrefix + "/"
}

// explorePage opens the Explore page at path (relative to the app root) in a
// browser sized to vp, and waits for its scripts.
func explorePage(t *testing.T, base string, vp viewport, path string) playwright.Page {
	t.Helper()
	page := newPage(t, vp)
	visit(t, page, base+path)
	return page
}

// evalJSON runs fn, a JavaScript function expression, in the page with arg and
// decodes the JSON it returns into out. JSON keeps NaN and Infinity from
// reaching Go as unusable numbers.
func evalJSON(t *testing.T, page playwright.Page, fn string, arg any, out any) {
	t.Helper()
	result, err := page.Evaluate(`(arg) => JSON.stringify((`+fn+`)(arg))`, arg)
	if err != nil {
		t.Fatalf("evaluate on %s: %v\n%s", page.URL(), err, fn)
	}
	text, ok := result.(string)
	if !ok {
		t.Fatalf("evaluate on %s returned %T, want a JSON string", page.URL(), result)
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
}

// queryOf returns the page's current query parameters.
func queryOf(t *testing.T, page playwright.Page) url.Values {
	t.Helper()
	parsed, err := url.Parse(page.URL())
	if err != nil {
		t.Fatalf("parse %s: %v", page.URL(), err)
	}
	return parsed.Query()
}

// controlValues reads the value of each control, "true" or "false" for a
// checkbox.
func controlValues(t *testing.T, page playwright.Page, selectors []string) map[string]string {
	t.Helper()
	var values map[string]string
	evalJSON(t, page, `(selectors) => Object.fromEntries(selectors.map((selector) => {
		const control = document.querySelector(selector);
		if (!control) return [selector, "<missing>"];
		return [selector, control.type === "checkbox" ? String(control.checked) : control.value];
	}))`, selectors, &values)
	return values
}

// chooseOption picks value in the select matching selector, which fires the
// same change event a person's choice does.
func chooseOption(t *testing.T, page playwright.Page, selector, value string) {
	t.Helper()
	if _, err := page.Locator(selector).SelectOption(playwright.SelectOptionValues{Values: &[]string{value}}); err != nil {
		t.Fatalf("choose %q in %s: %v", value, selector, err)
	}
}

// exploreView is one Explore analysis whose controls round-trip through the
// URL.
type exploreView struct {
	name     string
	scenario string
	// path is the starting page, relative to the app root.
	path string
	// controls are the selectors whose values are compared at every step.
	controls []string
	steps    []exploreStep
}

// exploreStep changes one control and names the query parameter that must
// carry the choice.
type exploreStep struct {
	selector, value string
	param           string
	// check, when set, examines the page after the change.
	check func(t *testing.T, page playwright.Page)
}

// historyEntry is the page state after one step: where the URL points and what
// every control shows.
type historyEntry struct {
	url      string
	controls map[string]string
}

// quadrantVisibility returns whether each of the Scored vs allowed chart's
// datasets (Goals, then xG) is shown.
func quadrantVisibility(t *testing.T, page playwright.Page) []bool {
	t.Helper()
	var visible []bool
	evalJSON(t, page, `() => {
		const chart = Chart.getChart(document.querySelector('[data-chart="team-quadrant"]'));
		return chart.data.datasets.map((_, index) => chart.isDatasetVisible(index));
	}`, nil, &visible)
	return visible
}

// ASA team IDs from apptest's sixteen clubs.
const (
	teamGotham   = "raMyrr25d2"
	teamPortland = "Pk5LeeNqOW"
)

func exploreViews() []exploreView {
	return []exploreView{
		{
			name: "team-quadrants", scenario: apptest.ScenarioSeasonTrend,
			path:     "explore?view=teams&display=quadrant",
			controls: []string{"[data-team-season]", "[data-team-units]", "[data-team-quadrant-data]"},
			steps: []exploreStep{
				{selector: "[data-team-season]", value: "2026", param: "season"},
				{selector: "[data-team-units]", value: "total", param: "units"},
				{selector: "[data-team-quadrant-data]", value: "both", param: "quadrant-data", check: func(t *testing.T, page playwright.Page) {
					if got := quadrantVisibility(t, page); len(got) != 2 || !got[0] || !got[1] {
						t.Errorf("Goals and xG visibility = %v, want both shown", got)
					}
				}},
				{selector: "[data-team-quadrant-data]", value: "xg", param: "quadrant-data", check: func(t *testing.T, page playwright.Page) {
					if got := quadrantVisibility(t, page); len(got) != 2 || got[0] || !got[1] {
						t.Errorf("xG-only visibility = %v, want only xG shown", got)
					}
				}},
			},
		},
		{
			name: "team-rankings", scenario: apptest.ScenarioSeasonTrend,
			path: "explore?view=team-rankings",
			controls: []string{
				"[data-team-rankings-controls] [name=season]", "[data-team-rankings-controls] [name=team]", "[data-team-rankings-controls] [name=units]",
			},
			steps: []exploreStep{
				{selector: "[data-team-rankings-controls] [name=season]", value: "2025", param: "season"},
				{selector: "[data-team-rankings-controls] [name=team]", value: teamGotham, param: "team"},
				{selector: "[data-team-rankings-controls] [name=units]", value: "total", param: "units"},
				{selector: "[data-team-rankings-controls] [name=team]", value: teamPortland, param: "team"},
			},
		},
		{
			name: "team-history", scenario: apptest.ScenarioTeamHistory,
			path: "explore?view=team-history",
			controls: []string{
				"[data-history-team]", "[data-history-measure]", "[data-history-series]", "[data-history-context]",
			},
			steps: []exploreStep{
				{selector: "[data-history-team]", value: teamGotham, param: "team"},
				{selector: "[data-history-measure]", value: "points", param: "measure"},
				{selector: "[data-history-series]", value: "xg", param: "series"},
				{selector: "[data-history-measure]", value: "for", param: "measure"},
			},
		},
		{
			name: "season-trend", scenario: apptest.ScenarioSeasonTrend,
			path: "explore?view=season-trend",
			controls: []string{
				"[data-trend-season]", "[data-trend-team]", "[data-trend-view]", "[data-trend-series]", "[data-trend-mode]",
				"[data-trend-window]", "[data-trend-average-series]", "[data-trend-reference]",
			},
			steps: []exploreStep{
				{selector: "[data-trend-season]", value: "2025", param: "season"},
				{selector: "[data-trend-team]", value: teamGotham, param: "team"},
				{selector: "[data-trend-series]", value: "xg", param: "series"},
				{selector: "[data-trend-window]", value: "10", param: "window"},
				{selector: "[data-trend-mode]", value: "match", param: "trend-mode", check: func(t *testing.T, page playwright.Page) {
					hidden, err := page.Locator("[data-trend-window-control]").IsHidden()
					if err != nil || !hidden {
						t.Errorf("window control hidden = %v (%v), want hidden in per-match mode", hidden, err)
					}
				}},
				{selector: "[data-trend-view]", value: "relative", param: "trend-view"},
				{selector: "[data-trend-average-series]", value: "points", param: "average-series"},
				{selector: "[data-trend-reference]", value: "best", param: "average-reference"},
			},
		},
	}
}

// TestExploreSelectionsRoundTripThroughURL changes each analysis's controls one
// at a time. Every change must reach the URL, Back and Forward must restore the
// URL and the controls exactly, and opening the final URL fresh must show the
// final controls.
func TestExploreSelectionsRoundTripThroughURL(t *testing.T) {
	t.Parallel()
	bases := map[string]string{}
	for _, view := range exploreViews() {
		if _, ok := bases[view.scenario]; !ok {
			bases[view.scenario] = exploreBase(t, view.scenario)
		}
	}
	for _, view := range exploreViews() {
		t.Run(view.name, func(t *testing.T) {
			t.Parallel()
			page := explorePage(t, bases[view.scenario], Desktop, view.path)

			snapshot := func() historyEntry {
				return historyEntry{url: page.URL(), controls: controlValues(t, page, view.controls)}
			}
			history := []historyEntry{snapshot()}
			for _, step := range view.steps {
				chooseOption(t, page, step.selector, step.value)
				if got := queryOf(t, page).Get(step.param); got != step.value {
					t.Fatalf("after choosing %s=%q in %s, URL parameter %s = %q (%s)", step.param, step.value, step.selector, step.param, got, page.URL())
				}
				entry := snapshot()
				if got := entry.controls[step.selector]; got != step.value {
					t.Fatalf("after choosing %q, %s shows %q", step.value, step.selector, got)
				}
				if entry.url == history[len(history)-1].url {
					t.Fatalf("choosing %s=%q left the URL unchanged: %s", step.param, step.value, entry.url)
				}
				if step.check != nil {
					step.check(t, page)
				}
				history = append(history, entry)
			}

			assertEntry := func(direction string, index int) {
				t.Helper()
				got, want := snapshot(), history[index]
				if got.url != want.url {
					t.Errorf("%s to step %d: URL = %s, want %s", direction, index, got.url, want.url)
				}
				for selector, value := range want.controls {
					if got.controls[selector] != value {
						t.Errorf("%s to step %d: %s = %q, want %q", direction, index, selector, got.controls[selector], value)
					}
				}
			}
			for index := len(history) - 2; index >= 0; index-- {
				if _, err := page.GoBack(); err != nil {
					t.Fatalf("back: %v", err)
				}
				assertEntry("back", index)
			}
			for index := 1; index < len(history); index++ {
				if _, err := page.GoForward(); err != nil {
					t.Fatalf("forward: %v", err)
				}
				assertEntry("forward", index)
			}

			// Opening the final URL fresh restores the same controls without
			// relying on in-page history state.
			last := history[len(history)-1]
			visit(t, page, last.url)
			assertEntry("direct load", len(history)-1)
		})
	}
}

// exploreTabs lists every Explore analysis: its group, its tab's link text and
// the query that opens it.
var exploreTabs = []struct{ group, tab, query string }{
	{"Compare teams", "Actual vs expected", "view=teams&display=chart"},
	{"Compare teams", "Gap to expected", "view=teams&display=gap"},
	{"Compare teams", "Outlier plot", "view=teams&display=scatter"},
	{"Compare teams", "Scored vs allowed", "view=teams&display=quadrant"},
	{"Compare teams", "Table", "view=teams&display=table"},
	{"Team profile", "Rankings", "view=team-rankings"},
	{"Team profile", "Match by match", "view=season-trend"},
	{"Team profile", "Season by season", "view=team-history"},
	{"League trends", "Scoring trend", "view=trend"},
	{"League trends", "Goal distribution", "view=distribution"},
	{"League trends", "Scoring table", "view=table"},
}

// TestExploreSwitchingAnalysesMakesNoRequests loads Explore once and then
// visits every tab by clicking. Switching analyses reuses the page's single
// data payload, so no document, script, style or fetch request may follow the
// first load. Team logos are images and are exempt.
func TestExploreSwitchingAnalysesMakesNoRequests(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&display=chart")

	requests := watchRequests(page)
	for _, tab := range exploreTabs {
		group := page.Locator(".explore-views").GetByText(tab.group, playwright.LocatorGetByTextOptions{Exact: playwright.Bool(true)})
		if err := group.Click(); err != nil {
			t.Fatalf("open group %q: %v", tab.group, err)
		}
		link := page.Locator(".explore-subviews:not([hidden])").GetByText(tab.tab, playwright.LocatorGetByTextOptions{Exact: playwright.Bool(true)})
		if err := link.Click(); err != nil {
			t.Fatalf("open tab %q: %v", tab.tab, err)
		}
		if err := settle(page); err != nil {
			t.Fatalf("settle after %q: %v", tab.tab, err)
		}
		query := queryOf(t, page)
		for _, pair := range strings.Split(tab.query, "&") {
			key, value, _ := strings.Cut(pair, "=")
			if got := query.Get(key); got != value {
				t.Errorf("after opening %q, URL %s = %q, want %q", tab.tab, key, got, value)
			}
		}
		title, err := page.Title()
		if err != nil || !strings.HasPrefix(title, tab.tab+" · ") {
			t.Errorf("after opening %q, the title is %q (%v)", tab.tab, title, err)
		}
	}
	requests.assertNone(t, "switching analyses")
}

// requestLog records a page's requests other than images. Playwright delivers
// events on its own goroutine, so reads and writes take the lock.
type requestLog struct {
	mu      sync.Mutex
	entries []string
}

// watchRequests starts recording page's non-image requests.
func watchRequests(page playwright.Page) *requestLog {
	log := &requestLog{}
	page.On("request", func(request playwright.Request) {
		if request.ResourceType() == "image" {
			return
		}
		log.mu.Lock()
		defer log.mu.Unlock()
		log.entries = append(log.entries, request.ResourceType()+" "+request.URL())
	})
	return log
}

// assertNone fails if any request was recorded. Team logos are images and are
// not recorded.
func (l *requestLog) assertNone(t *testing.T, what string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) > 0 {
		t.Errorf("%s made %d request(s), want none: %s", what, len(l.entries), strings.Join(l.entries, ", "))
	}
}

// TestExploreHasNoHorizontalOverflowOnPhone checks every analysis at 390px.
func TestExploreHasNoHorizontalOverflowOnPhone(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Mobile, "explore?"+exploreTabs[0].query)
	for _, tab := range exploreTabs {
		t.Run(tab.tab, func(t *testing.T) {
			gotoInPage(t, page, "explore?"+tab.query)
			assertNoHorizontalOverflow(t, page)
		})
	}
}

// teamTableColumn returns the text of one column of the Compare teams table,
// in row order. Columns are Team, Played, then Actual, Expected and Gap for
// each of goal differential, goals scored, goals allowed and points.
func teamTableColumn(t *testing.T, page playwright.Page, index int) []string {
	t.Helper()
	var cells []string
	evalJSON(t, page, `(index) => [...document.querySelectorAll('[data-team-rows] tr')]
		.map((row) => row.children[index].textContent.trim())`, index, &cells)
	return cells
}

// gotoInPage moves an open Explore page to another Explore URL the way Back
// and Forward do: it pushes the URL onto the history and fires popstate, so
// the script re-renders from its single data payload without a reload. It is
// cheaper than a fresh page, and the script's own URL handling is the code
// under test either way.
func gotoInPage(t *testing.T, page playwright.Page, path string) {
	t.Helper()
	_, query, _ := strings.Cut(path, "?")
	if _, err := page.Evaluate(`(query) => {
		history.pushState(null, '', '?' + query);
		window.dispatchEvent(new PopStateEvent('popstate'));
	}`, query); err != nil {
		t.Fatalf("move to %s: %v", path, err)
	}
	if err := settle(page); err != nil {
		t.Fatalf("settle after moving to %s: %v", path, err)
	}
}

// markDocument tags the current document so assertSameDocument can tell
// whether the page has since reloaded or navigated to a new document.
func markDocument(t *testing.T, page playwright.Page) {
	t.Helper()
	if _, err := page.Evaluate(`() => { window.__documentMarker = true; }`); err != nil {
		t.Fatalf("mark document: %v", err)
	}
}

// assertSameDocument fails if the page is no longer the document that
// markDocument tagged.
func assertSameDocument(t *testing.T, page playwright.Page, what string) {
	t.Helper()
	marked, err := page.Evaluate(`() => window.__documentMarker === true`)
	if err != nil || marked != true {
		t.Errorf("%s reloaded the page instead of updating it in place (marker %v, error %v)", what, marked, err)
	}
}

// expectInPlace marks the page's document and starts recording requests. The
// function it returns fails the test if the page has since reloaded or made a
// request. Call it before the interaction and defer the result.
func expectInPlace(t *testing.T, page playwright.Page, what string) func() {
	t.Helper()
	requests := watchRequests(page)
	markDocument(t, page)
	return func() {
		t.Helper()
		assertSameDocument(t, page, what)
		requests.assertNone(t, what)
	}
}

// Column positions in the Compare teams table.
const (
	columnTeam      = 0
	columnForActual = 5
	columnForXG     = 6
)

// numericCells parses a table column and counts the cells that read
// "Unavailable" instead of a number.
func numericCells(t *testing.T, cells []string) (values []float64, missing int) {
	t.Helper()
	for _, cell := range cells {
		if cell == "Unavailable" {
			missing++
			continue
		}
		value, err := strconv.ParseFloat(cell, 64)
		if err != nil {
			t.Fatalf("table cell %q is not a number: %v", cell, err)
		}
		values = append(values, value)
	}
	return values, missing
}

func assertSorted(t *testing.T, label string, values []float64, descending bool) {
	t.Helper()
	for i := 1; i < len(values); i++ {
		if (descending && values[i] > values[i-1]) || (!descending && values[i] < values[i-1]) {
			t.Errorf("%s is not sorted (descending=%v): %v", label, descending, values)
			return
		}
	}
	if len(values) > 1 && values[0] == values[len(values)-1] {
		t.Errorf("%s has one value throughout, so sorting proves nothing: %v", label, values)
	}
}

// TestExploreTableSortsBothDirections sorts the Compare teams table through
// its header links. Each header toggles direction, reports aria-sort, and keeps
// teams without expected values last whichever way the column runs.
func TestExploreTableSortsBothDirections(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&display=table&season=2026")
	// The server renders the same sorted table for the header links, so only
	// an unchanged document and no requests prove that the script sorted it.
	defer expectInPlace(t, page, "sorting")()

	ariaSort := func(key string) string {
		t.Helper()
		value, err := page.Locator(`[data-team-sort="` + key + `"]`).Locator("xpath=..").GetAttribute("aria-sort")
		if err != nil {
			t.Fatalf("aria-sort of %s: %v", key, err)
		}
		return value
	}
	click := func(key string) {
		t.Helper()
		if err := page.Locator(`[data-team-sort="` + key + `"]`).Click(); err != nil {
			t.Fatalf("sort by %s: %v", key, err)
		}
	}

	// Goals scored has a full set of values: a first click sorts high to low,
	// a second sorts low to high.
	click("for-actual")
	assertSorted(t, "goals scored, first click", mustNumbers(t, teamTableColumn(t, page, columnForActual)), true)
	if got := ariaSort("for-actual"); got != "descending" {
		t.Errorf("aria-sort after first click = %q, want descending", got)
	}
	if got := queryOf(t, page); got.Get("team-sort") != "for-actual" || got.Get("team-order") != "desc" {
		t.Errorf("URL after first click = %v, want team-sort=for-actual&team-order=desc", got)
	}
	click("for-actual")
	assertSorted(t, "goals scored, second click", mustNumbers(t, teamTableColumn(t, page, columnForActual)), false)
	if got := ariaSort("for-actual"); got != "ascending" {
		t.Errorf("aria-sort after second click = %q, want ascending", got)
	}
	if got := ariaSort("name"); got != "none" {
		t.Errorf("aria-sort of the unsorted Team column = %q, want none", got)
	}

	// The team name sorts A to Z first.
	click("name")
	names := teamTableColumn(t, page, columnTeam)
	for i := 1; i < len(names); i++ {
		if strings.Compare(names[i-1], names[i]) > 0 {
			t.Errorf("team names are not in A to Z order: %v", names)
			break
		}
	}

	// Two teams lack xG in this season. Their expected values sort last in both
	// directions.
	for _, order := range []string{"descending", "ascending"} {
		click("for-expected")
		values, missing := numericCells(t, teamTableColumn(t, page, columnForXG))
		if missing != 2 {
			t.Fatalf("expected goals column has %d unavailable cells, want 2", missing)
		}
		cells := teamTableColumn(t, page, columnForXG)
		if cells[len(cells)-1] != "Unavailable" || cells[len(cells)-2] != "Unavailable" {
			t.Errorf("%s sort does not leave unavailable xG last: %v", order, cells)
		}
		if got := ariaSort("for-expected"); got != order {
			t.Errorf("aria-sort = %q, want %q", got, order)
		}
		assertSorted(t, "expected goals, "+order, values, order == "descending")
	}
}

func mustNumbers(t *testing.T, cells []string) []float64 {
	t.Helper()
	values, missing := numericCells(t, cells)
	if missing > 0 {
		t.Fatalf("column %v has %d unavailable cells", cells, missing)
	}
	return values
}

// noScriptPage returns a page whose browser context has JavaScript disabled, so
// only the server-rendered HTML, native links and GET forms are in play. Failed
// or error responses from the app fail the test.
func noScriptPage(t *testing.T, vp viewport) playwright.Page {
	t.Helper()
	return newPageWith(t, pageOptions{Viewport: vp, NoScript: true})
}

// inspection is a chart's keyboard and pointer inspection state.
type inspection struct {
	Active []struct {
		DatasetIndex int `json:"datasetIndex"`
		Index        int `json:"index"`
	} `json:"active"`
	TooltipActive int    `json:"tooltipActive"`
	Status        string `json:"status"`
	Pinned        []int  `json:"pinned"`
	// Area is the plot area, which must not move when a tooltip opens or closes.
	Area struct{ Left, Right, Top, Bottom float64 } `json:"area"`
}

func chartInspection(t *testing.T, page playwright.Page, canvas string) inspection {
	t.Helper()
	var state inspection
	evalJSON(t, page, `(selector) => {
		const chart = Chart.getChart(document.querySelector(selector));
		return {
			active: chart.getActiveElements().map((e) => ({datasetIndex: e.datasetIndex, index: e.index})),
			tooltipActive: chart.tooltip.getActiveElements().length,
			status: document.querySelector('[data-chart-status]').textContent,
			pinned: chart.scatterPinnedIndexes ?? chart.quadrantPinnedIndexes ?? [],
			area: {left: chart.chartArea.left, right: chart.chartArea.right, top: chart.chartArea.top, bottom: chart.chartArea.bottom},
		};
	}`, canvas, &state)
	return state
}

// markPoint is a plotted point's position in the page's viewport.
type markPoint struct {
	X, Y float64
	Skip bool
}

// chartGeometry is where a scatter chart's marks and plot area are in the
// viewport.
type chartGeometry struct {
	Area struct{ Left, Right, Top, Bottom float64 }
	// Points holds each dataset's marks in the viewport's coordinates.
	Points [][]markPoint
	Names  []string
}

func scatterGeometry(t *testing.T, page playwright.Page, canvas string) chartGeometry {
	t.Helper()
	var geometry chartGeometry
	evalJSON(t, page, `(selector) => {
		const element = document.querySelector(selector);
		element.scrollIntoView({block: 'center'});
		const chart = Chart.getChart(element);
		const box = element.getBoundingClientRect();
		return {
			area: {left: box.x + chart.chartArea.left, right: box.x + chart.chartArea.right, top: box.y + chart.chartArea.top, bottom: box.y + chart.chartArea.bottom},
			points: chart.data.datasets.map((_, index) => chart.getDatasetMeta(index).data
				.map((p) => ({x: box.x + p.x, y: box.y + p.y, skip: !!p.skip}))),
			names: chart.teamRows.map((row) => row.name),
		};
	}`, canvas, &geometry)
	return geometry
}

// isolatedPoint returns the index of a dataset-0 mark at least 20px from every
// other mark in that dataset, or -1.
func isolatedPoint(marks []markPoint) int {
	for i, mark := range marks {
		if mark.Skip {
			continue
		}
		isolated := true
		for j, other := range marks {
			if i != j && !other.Skip && math.Hypot(mark.X-other.X, mark.Y-other.Y) < 20 {
				isolated = false
				break
			}
		}
		if isolated {
			return i
		}
	}
	return -1
}

// emptySpot returns a point inside the plot area at least minDistance from
// every mark and from the diagonal that runs from the area's bottom left to
// its top right, so a click there lands on neither a team nor a guide marker.
func emptySpot(geometry chartGeometry, minDistance float64) (x, y float64, ok bool) {
	area := geometry.Area
	for py := area.Top + 4; py < area.Bottom-4; py += 6 {
		for px := area.Left + 4; px < area.Right-4; px += 6 {
			free := true
			for _, dataset := range geometry.Points {
				for _, mark := range dataset {
					if !mark.Skip && math.Hypot(mark.X-px, mark.Y-py) < minDistance {
						free = false
					}
				}
			}
			// Distance from the bottom-left to top-right diagonal, assuming a
			// roughly square plot.
			width, height := area.Right-area.Left, area.Bottom-area.Top
			cross := (px-area.Left)*height + (py-area.Bottom)*width
			if free && math.Abs(cross)/math.Hypot(width, height) >= minDistance {
				return px, py, true
			}
		}
	}
	return 0, 0, false
}

// pinScenarios are the two charts that keep a clicked point's details.
var pinScenarios = []struct {
	name, path, canvas, selection, clear string
}{
	{"outlier plot", "explore?view=teams&display=scatter&season=2025", `[data-chart="team-scatter"]`, "[data-team-scatter-selection]", "[data-team-scatter-clear]"},
	{"scored vs allowed", "explore?view=teams&display=quadrant&season=2025", `[data-chart="team-quadrant"]`, "[data-team-quadrant-selection]", "[data-team-quadrant-clear]"},
}

// TestExplorePointsInspectAndPinWithKeyboardAndPointer walks the keyboard
// inspection keys, then pins a point, and clears the pin with the Clear
// selection button, Escape and a click on empty plot space.
func TestExplorePointsInspectAndPinWithKeyboardAndPointer(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, scenario := range pinScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			page := explorePage(t, base, Desktop, scenario.path)
			expect := playwright.NewPlaywrightAssertions()
			canvas := page.Locator(scenario.canvas)
			if err := canvas.Focus(); err != nil {
				t.Fatalf("focus chart: %v", err)
			}
			press := func(key string) {
				t.Helper()
				if err := page.Keyboard().Press(key); err != nil {
					t.Fatalf("press %s: %v", key, err)
				}
			}

			resting := chartInspection(t, page, scenario.canvas)

			// Arrow keys step through the marks; Home and End jump to the ends.
			press("Home")
			first := chartInspection(t, page, scenario.canvas)
			if first.Area != resting.Area {
				t.Errorf("plot area moved when the tooltip opened: %+v to %+v", resting.Area, first.Area)
			}
			if len(first.Active) != 1 || first.TooltipActive != 1 || first.Status == "" {
				t.Fatalf("after Home: %+v, want one active mark, an open tooltip and an announcement", first)
			}
			press("ArrowRight")
			second := chartInspection(t, page, scenario.canvas)
			if len(second.Active) != 1 || second.Status == first.Status {
				t.Errorf("after ArrowRight: %+v, want a different mark than %+v", second, first)
			}
			press("End")
			last := chartInspection(t, page, scenario.canvas)
			if len(last.Active) != 1 || last.Active[0] == first.Active[0] {
				t.Errorf("after End: %+v, want the last mark, not %+v", last, first)
			}
			press("ArrowRight")
			if again := chartInspection(t, page, scenario.canvas); len(again.Active) != 1 || again.Active[0] != last.Active[0] {
				t.Errorf("ArrowRight past the end moved to %+v, want to stay on %+v", again.Active, last.Active)
			}
			press("Escape")
			if cleared := chartInspection(t, page, scenario.canvas); len(cleared.Active) != 0 || cleared.TooltipActive != 0 || cleared.Status != "" {
				t.Errorf("after Escape: %+v, want no active mark, no tooltip and no announcement", cleared)
			} else if cleared.Area != resting.Area {
				t.Errorf("plot area moved when the tooltip closed: %+v to %+v", resting.Area, cleared.Area)
			}

			// Hovering an isolated point opens its tooltip; moving away closes it.
			geometry := scatterGeometry(t, page, scenario.canvas)
			target := isolatedPoint(geometry.Points[0])
			if target < 0 {
				t.Fatalf("no isolated point to click among %v", geometry.Points[0])
			}
			hover := geometry.Points[0][target]
			if err := page.Mouse().Move(hover.X, hover.Y); err != nil {
				t.Fatalf("hover: %v", err)
			}
			if hovered := chartInspection(t, page, scenario.canvas); len(hovered.Active) != 1 || hovered.Active[0].Index != target || hovered.TooltipActive != 1 {
				t.Errorf("hovering %s: %+v, want that team's tooltip", geometry.Names[target], hovered)
			} else if hovered.Area != resting.Area {
				t.Errorf("plot area moved under the pointer: %+v to %+v", resting.Area, hovered.Area)
			}
			if err := page.Mouse().Move(2, 2); err != nil {
				t.Fatalf("move away: %v", err)
			}
			if away := chartInspection(t, page, scenario.canvas); len(away.Active) != 0 || away.Area != resting.Area {
				t.Errorf("after moving away: %+v, want no active mark and a fixed plot area", away)
			}

			// Click the point to pin it.
			pin := func() {
				t.Helper()
				geometry := scatterGeometry(t, page, scenario.canvas)
				mark := geometry.Points[0][target]
				if err := page.Mouse().Click(mark.X, mark.Y); err != nil {
					t.Fatalf("click point: %v", err)
				}
				if err := expect.Locator(page.Locator(scenario.selection)).ToBeVisible(); err != nil {
					t.Fatalf("details card after clicking %s: %v", geometry.Names[target], err)
				}
				state := chartInspection(t, page, scenario.canvas)
				if len(state.Pinned) != 1 || state.Pinned[0] != target {
					t.Fatalf("pinned = %v, want [%d] (%s)", state.Pinned, target, geometry.Names[target])
				}
				if err := expect.Locator(page.Locator(scenario.selection)).ToContainText(geometry.Names[target]); err != nil {
					t.Errorf("details card does not name %s: %v", geometry.Names[target], err)
				}
			}
			assertUnpinned := func(how string) {
				t.Helper()
				if err := expect.Locator(page.Locator(scenario.selection)).ToBeHidden(); err != nil {
					t.Errorf("details card still shown after %s: %v", how, err)
				}
				if state := chartInspection(t, page, scenario.canvas); len(state.Pinned) != 0 {
					t.Errorf("pinned = %v after %s, want none", state.Pinned, how)
				}
			}

			pin()
			if err := page.Locator(scenario.clear).Click(); err != nil {
				t.Fatalf("click Clear selection: %v", err)
			}
			assertUnpinned("Clear selection")

			pin()
			press("Escape")
			assertUnpinned("Escape")

			pin()
			geometry = scatterGeometry(t, page, scenario.canvas)
			x, y, ok := emptySpot(geometry, 30)
			if !ok {
				t.Fatalf("no empty plot space in %+v", geometry.Area)
			}
			if err := page.Mouse().Click(x, y); err != nil {
				t.Fatalf("click empty space: %v", err)
			}
			assertUnpinned("a click on empty plot space")
		})
	}
}

// canvasCall is one drawing call recorded while a chart redraws.
type canvasCall struct {
	Op    string    `json:"op"`
	Args  []any     `json:"args"`
	Dash  []float64 `json:"dash"`
	Align string    `json:"align"`
}

// recordDraw redraws the chart on canvas and returns the canvas calls the
// redraw made: lines (moveTo, lineTo, stroke) and text (fillText). It reads
// what the chart paints without comparing pixels.
func recordDraw(t *testing.T, page playwright.Page, canvas string) []canvasCall {
	t.Helper()
	var calls []canvasCall
	evalJSON(t, page, `(selector) => {
		const chart = Chart.getChart(document.querySelector(selector));
		const ctx = chart.ctx;
		const log = [];
		const names = ['moveTo', 'lineTo', 'stroke', 'fillText'];
		for (const name of names) {
			const original = ctx[name];
			ctx[name] = function (...args) {
				log.push({op: name, args: args.filter((arg) => typeof arg === 'number' || typeof arg === 'string'),
					dash: ctx.getLineDash(), align: ctx.textAlign});
				return original.apply(this, args);
			};
		}
		try { chart.draw(); } finally { for (const name of names) delete ctx[name]; }
		return log;
	}`, canvas, &calls)
	return calls
}

// drawnText returns the text each fillText call painted.
func drawnText(calls []canvasCall) []string {
	var texts []string
	for _, call := range calls {
		if call.Op == "fillText" && len(call.Args) > 0 {
			if text, ok := call.Args[0].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	return texts
}

// scaleState is one chart axis.
type scaleState struct {
	Min, Max float64
	Reverse  bool
	// PixelMin and PixelMax are where Min and Max sit on the canvas.
	PixelMin, PixelMax float64
}

// scatterState is what the Outlier plot and Scored vs allowed charts hold: both
// axes and every visible mark's data values.
type scatterState struct {
	X, Y   scaleState
	Points [][]struct{ X, Y *float64 }
	Names  []string
	// Visible says which datasets are shown.
	Visible []bool
}

func scatterChartState(t *testing.T, page playwright.Page, canvas string) scatterState {
	t.Helper()
	var state scatterState
	evalJSON(t, page, `(selector) => {
		const chart = Chart.getChart(document.querySelector(selector));
		const scale = (axis) => ({
			min: axis.min, max: axis.max, reverse: !!axis.options.reverse,
			pixelMin: axis.getPixelForValue(axis.min), pixelMax: axis.getPixelForValue(axis.max),
		});
		return {
			x: scale(chart.scales.x), y: scale(chart.scales.y),
			points: chart.data.datasets.map((dataset) => dataset.data.map((p) => ({x: p.x, y: p.y}))),
			names: chart.teamRows.map((row) => row.name),
			visible: chart.data.datasets.map((_, index) => chart.isDatasetVisible(index)),
		};
	}`, canvas, &state)
	return state
}

// TestExploreOutlierPlotUsesEqualAxesAndParityDiagonal checks every measure in
// both unit modes: the horizontal and vertical axes cover the same range, that
// range holds every point, nonnegative measures stop at zero, and the chart
// paints a dashed line from the range's low corner to its high corner.
func TestExploreOutlierPlotUsesEqualAxesAndParityDiagonal(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&display=scatter&season=2025")
	for _, measure := range []string{"for", "against", "difference", "points"} {
		for _, units := range []string{"per-match", "total"} {
			t.Run(measure+"/"+units, func(t *testing.T) {
				gotoInPage(t, page, "explore?view=teams&display=scatter&season=2025&measure="+measure+"&units="+units)
				const canvas = `[data-chart="team-scatter"]`
				state := scatterChartState(t, page, canvas)
				if state.X.Min != state.Y.Min || state.X.Max != state.Y.Max {
					t.Errorf("axes differ: x %v to %v, y %v to %v", state.X.Min, state.X.Max, state.Y.Min, state.Y.Max)
				}
				if state.X.Reverse || state.Y.Reverse {
					t.Errorf("Outlier plot reverses an axis: x %v, y %v", state.X.Reverse, state.Y.Reverse)
				}
				if len(state.Points) != 1 || len(state.Points[0]) != fixtureTeams {
					t.Fatalf("points = %d datasets, want one with %d teams", len(state.Points), fixtureTeams)
				}
				for i, point := range state.Points[0] {
					if point.X == nil || point.Y == nil {
						t.Fatalf("%s has no point", state.Names[i])
					}
					for _, value := range []float64{*point.X, *point.Y} {
						if value < state.X.Min || value > state.X.Max {
							t.Errorf("%s value %v is outside the axis range %v to %v", state.Names[i], value, state.X.Min, state.X.Max)
						}
					}
				}
				if measure != "difference" && state.X.Min < 0 {
					t.Errorf("a nonnegative measure's axis starts at %v, want zero or more", state.X.Min)
				}
				if measure == "difference" && state.X.Min >= 0 {
					t.Logf("goal differential range %v to %v has no negative values", state.X.Min, state.X.Max)
				}

				var found bool
				calls := recordDraw(t, page, canvas)
				for i := 0; i+2 < len(calls); i++ {
					move, line, stroke := calls[i], calls[i+1], calls[i+2]
					if move.Op != "moveTo" || line.Op != "lineTo" || stroke.Op != "stroke" || len(stroke.Dash) == 0 {
						continue
					}
					// The diagonal runs from (min, min) to (max, max) in data
					// coordinates, so the same pixel positions give each end on
					// both axes.
					near := func(got any, want float64) bool {
						value, ok := got.(float64)
						return ok && math.Abs(value-want) < 0.5
					}
					if len(move.Args) == 2 && len(line.Args) == 2 &&
						near(move.Args[0], state.X.PixelMin) && near(move.Args[1], state.Y.PixelMin) &&
						near(line.Args[0], state.X.PixelMax) && near(line.Args[1], state.Y.PixelMax) {
						found = true
					}
				}
				if !found {
					t.Errorf("no dashed stroke from (min, min) to (max, max) among %d canvas calls", len(calls))
				}
			})
		}
	}
}

// TestExploreScoredVsAllowedReversesTheAllowedAxis checks that goals allowed
// run from high to low on the horizontal axis, so up and right are better, and
// that both axes cover the same range.
func TestExploreScoredVsAllowedReversesTheAllowedAxis(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&display=quadrant&season=2025")
	for _, mode := range []string{"goals", "xg", "both"} {
		t.Run(mode, func(t *testing.T) {
			gotoInPage(t, page, "explore?view=teams&display=quadrant&season=2025&quadrant-data="+mode)
			const canvas = `[data-chart="team-quadrant"]`
			state := scatterChartState(t, page, canvas)
			if !state.X.Reverse || state.Y.Reverse {
				t.Errorf("reverse: allowed (x) %v, scored (y) %v; want only the allowed axis reversed", state.X.Reverse, state.Y.Reverse)
			}
			if state.X.PixelMax >= state.X.PixelMin {
				t.Errorf("the highest allowed value is at x=%v, the lowest at x=%v; want high values on the left", state.X.PixelMax, state.X.PixelMin)
			}
			if state.Y.PixelMax >= state.Y.PixelMin {
				t.Errorf("the highest scored value is at y=%v, the lowest at y=%v; want high values at the top", state.Y.PixelMax, state.Y.PixelMin)
			}
			if state.X.Min != state.Y.Min || state.X.Max != state.Y.Max {
				t.Errorf("axes differ: allowed %v to %v, scored %v to %v", state.X.Min, state.X.Max, state.Y.Min, state.Y.Max)
			}
			wantVisible := map[string][]bool{"goals": {true, false}, "xg": {false, true}, "both": {true, true}}[mode]
			if fmt.Sprint(state.Visible) != fmt.Sprint(wantVisible) {
				t.Errorf("visible datasets = %v, want %v", state.Visible, wantVisible)
			}
			// The corner captions are drawn with the allowed axis reversed: the
			// left side reads "More allowed".
			var left, right = math.NaN(), math.NaN()
			for _, call := range recordDraw(t, page, canvas) {
				if call.Op != "fillText" || len(call.Args) < 2 {
					continue
				}
				x, _ := call.Args[1].(float64)
				switch call.Args[0] {
				case "More allowed":
					left = x
				case "Fewer allowed":
					right = x
				}
			}
			if math.IsNaN(left) || math.IsNaN(right) || left >= right {
				t.Errorf("More allowed is drawn at x=%v and Fewer allowed at x=%v; want More allowed on the left", left, right)
			}
		})
	}
}

// TestExploreFlagsTeamsWithIncompleteExpectedValues uses the 2026 season, where
// two teams lack xG and four lack xPoints. Each chart names them and draws no
// point or bar for them, and the Gap to expected chart paints the
// "xG incomplete" and "xPts incomplete" labels.
func TestExploreFlagsTeamsWithIncompleteExpectedValues(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	// One page serves every case; each moves it to a URL in place.
	page := explorePage(t, base, Desktop, "explore?view=teams&season=2026&display=gap")
	count := func(texts []string, want string) int {
		n := 0
		for _, text := range texts {
			if text == want {
				n++
			}
		}
		return n
	}
	expect := playwright.NewPlaywrightAssertions()
	for _, tc := range []struct {
		measure, label string
		missing        []string
	}{
		{"difference", "xG incomplete", []string{"Boston Legacy FC", "Seattle Reign FC"}},
		{"for", "xG incomplete", []string{"Boston Legacy FC", "Seattle Reign FC"}},
		{"points", "xPts incomplete", []string{"Bay FC", "Boston Legacy FC", "Seattle Reign FC", "Utah Royals FC"}},
	} {
		t.Run(tc.measure, func(t *testing.T) {
			path := "explore?view=teams&season=2026&measure=" + tc.measure + "&display="

			gotoInPage(t, page, path+"gap")
			gap := page
			texts := drawnText(recordDraw(t, gap, `[data-chart="team-gap"]`))
			if got := count(texts, tc.label); got != len(tc.missing) {
				t.Errorf("Gap to expected paints %q %d times, want %d (all text: %v)", tc.label, got, len(tc.missing), texts)
			}
			for _, name := range tc.missing {
				if err := expect.Locator(gap.Locator("[data-team-gap-missing]")).ToContainText(name); err != nil {
					t.Errorf("Gap to expected note does not name %s: %v", name, err)
				}
			}
			var gaps []*float64
			evalJSON(t, gap, `() => Chart.getChart(document.querySelector('[data-chart="team-gap"]')).data.datasets[0].data`, nil, &gaps)
			nulls := 0
			for _, value := range gaps {
				if value == nil {
					nulls++
				}
			}
			if nulls != len(tc.missing) || len(gaps) != fixtureTeams {
				t.Errorf("Gap to expected has %d null bars among %d teams, want %d among %d", nulls, len(gaps), len(tc.missing), fixtureTeams)
			}

			gotoInPage(t, page, path+"scatter")
			scatter := page
			state := scatterChartState(t, scatter, `[data-chart="team-scatter"]`)
			if want := fixtureTeams - len(tc.missing); len(state.Points[0]) != want {
				t.Errorf("Outlier plot has %d points, want %d", len(state.Points[0]), want)
			}
			for _, name := range tc.missing {
				if err := expect.Locator(scatter.Locator("[data-team-scatter-missing]")).ToContainText(name); err != nil {
					t.Errorf("Outlier plot note does not name %s: %v", name, err)
				}
				for _, plotted := range state.Names {
					if plotted == name {
						t.Errorf("Outlier plot still plots %s", name)
					}
				}
			}
		})
	}

	t.Run("table", func(t *testing.T) {
		gotoInPage(t, page, "explore?view=teams&season=2026&display=table")
		if err := expect.Locator(page.Locator("[data-team-warning]")).ToContainText("xG and xPts"); err != nil {
			t.Errorf("table warning: %v", err)
		}
		_, missing := numericCells(t, teamTableColumn(t, page, columnForXG))
		if missing != 2 {
			t.Errorf("table shows %d unavailable xG values, want 2", missing)
		}
	})

	t.Run("scored vs allowed", func(t *testing.T) {
		gotoInPage(t, page, "explore?view=teams&season=2026&display=quadrant&quadrant-data=both")
		for _, name := range []string{"Boston Legacy FC", "Seattle Reign FC"} {
			if err := expect.Locator(page.Locator("[data-team-quadrant-missing]")).ToContainText(name); err != nil {
				t.Errorf("Scored vs allowed note does not name %s: %v", name, err)
			}
		}
		state := scatterChartState(t, page, `[data-chart="team-quadrant"]`)
		covered := 0
		for _, point := range state.Points[1] {
			if point.X != nil && point.Y != nil {
				covered++
			}
		}
		if covered != fixtureTeams-2 {
			t.Errorf("Scored vs allowed has %d xG points, want %d", covered, fixtureTeams-2)
		}
	})
}

// TestExploreOutlierPlotExplainsWhenNoTeamHasCompleteXG uses a season in which
// only one game has xG, so no team has complete xG and the Outlier plot has
// nothing to draw.
func TestExploreOutlierPlotExplainsWhenNoTeamHasCompleteXG(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioNoXG)
	page := explorePage(t, base, Desktop, "explore?view=teams&season=2026&measure=for&display=scatter")
	expect := playwright.NewPlaywrightAssertions()
	if err := expect.Locator(page.Locator("[data-team-scatter-empty]")).ToContainText("No teams have complete xG for this measure yet."); err != nil {
		t.Errorf("Outlier plot empty message: %v", err)
	}
	if err := expect.Locator(page.Locator("[data-team-scatter-chart-wrap]")).ToBeHidden(); err != nil {
		t.Errorf("Outlier plot chart should be hidden: %v", err)
	}
}

// TestExploreShowsEmptyStatesForSeasonsWithoutData selects a season whose
// results are not in the cache, and a team with no results in a season.
func TestExploreShowsEmptyStatesForSeasonsWithoutData(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&season=2024&display=chart")
	expect := playwright.NewPlaywrightAssertions()
	for _, display := range []string{"chart", "gap", "scatter", "quadrant", "table"} {
		t.Run(display, func(t *testing.T) {
			gotoInPage(t, page, "explore?view=teams&season=2024&display="+display)
			if err := expect.Locator(page.Locator("[data-team-empty]")).ToBeVisible(); err != nil {
				t.Errorf("empty message: %v", err)
			}
			if err := expect.Locator(page.Locator("[data-team-empty]")).ToContainText("No team comparison is available for this season"); err != nil {
				t.Errorf("empty message text: %v", err)
			}
			if err := expect.Locator(page.Locator("[data-team-results]")).ToBeHidden(); err != nil {
				t.Errorf("results are shown for an empty season: %v", err)
			}
		})
	}
	t.Run("rankings", func(t *testing.T) {
		gotoInPage(t, page, "explore?view=team-rankings&season=2024")
		if err := expect.Locator(page.Locator("[data-rankings-empty]")).ToBeVisible(); err != nil {
			t.Errorf("empty rankings message: %v", err)
		}
		if err := expect.Locator(page.Locator("[data-rankings-results]")).ToBeHidden(); err != nil {
			t.Errorf("rank cards are shown for an empty season: %v", err)
		}
	})
	t.Run("match by match", func(t *testing.T) {
		gotoInPage(t, page, "explore?view=season-trend&season=2024")
		if err := expect.Locator(page.Locator("[data-season-trend-empty]")).ToBeVisible(); err != nil {
			t.Errorf("empty match message: %v", err)
		}
	})
}

// TestExploreSquarePlotsFollowTheViewport checks the Outlier plot and Scored vs
// allowed sizing: both charts are square, grow to the viewport height less the
// page chrome with a 608px floor on desktop, and shrink to fit on a phone.
func TestExploreSquarePlotsFollowTheViewport(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	viewports := []viewport{
		{Name: "short desktop", Width: 1280, Height: 500},
		Desktop,
		{Name: "tall desktop", Width: 1280, Height: 1000},
		Mobile,
	}
	// One page per viewport serves both plots, which it moves between in place.
	pages := map[string]playwright.Page{}
	for _, vp := range viewports {
		pages[vp.Name] = explorePage(t, base, vp, "explore?view=teams&display=scatter&season=2025")
	}
	for _, plot := range []struct {
		name, path, canvas, wrap string
		// chrome is the vertical space the CSS reserves around the plot.
		chrome float64
	}{
		{"outlier plot", "explore?view=teams&display=scatter&season=2025", `[data-chart="team-scatter"]`, ".explore-team-scatter-wrap", 72},
		{"scored vs allowed", "explore?view=teams&display=quadrant&season=2025", `[data-chart="team-quadrant"]`, ".explore-team-quadrant-wrap", 32},
	} {
		for _, vp := range viewports {
			t.Run(plot.name+"/"+vp.Name, func(t *testing.T) {
				page := pages[vp.Name]
				gotoInPage(t, page, plot.path)
				var size struct{ Canvas, Wrap, Parent, Height float64 }
				evalJSON(t, page, `(arg) => {
					const canvas = document.querySelector(arg.canvas);
					const wrap = document.querySelector(arg.wrap);
					const box = canvas.getBoundingClientRect();
					return {canvas: box.width, height: box.height, wrap: wrap.getBoundingClientRect().width, parent: wrap.parentElement.getBoundingClientRect().width};
				}`, map[string]any{"canvas": plot.canvas, "wrap": plot.wrap}, &size)
				if math.Abs(size.Canvas-size.Height) > 1 {
					t.Errorf("plot is %.1f wide and %.1f tall, want square", size.Canvas, size.Height)
				}
				if vp.Width < 600 {
					if size.Wrap > float64(vp.Width)-40 || size.Wrap < 280 {
						t.Errorf("phone plot is %.1fpx wide in a %dpx viewport, want it to fit with margins", size.Wrap, vp.Width)
					}
					return
				}
				want := math.Min(math.Min(size.Parent, 1200), math.Max(608, float64(vp.Height)-plot.chrome))
				if math.Abs(size.Wrap-want) > 1 {
					t.Errorf("plot is %.1fpx wide in a %dx%d viewport, want %.1f (container %.1f)", size.Wrap, vp.Width, vp.Height, want, size.Parent)
				}
			})
		}
	}
}

// logoLayout is where a scatter chart's visible team logos, marks and plot area
// are, in viewport coordinates.
type logoLayout struct {
	Logos []struct{ Left, Top, Right, Bottom float64 }
	Marks []markPoint
	Area  struct{ Left, Right, Top, Bottom float64 }
	// Radius is the points' drawn radius.
	Radius float64
	Total  int
}

func scatterLogoLayout(t *testing.T, page playwright.Page, canvas, layer string) logoLayout {
	t.Helper()
	var layout logoLayout
	evalJSON(t, page, `(arg) => {
		const element = document.querySelector(arg.canvas);
		const chart = Chart.getChart(element);
		const box = element.getBoundingClientRect();
		const images = [...document.querySelector(arg.layer).querySelectorAll('img')];
		const marks = chart.data.datasets.flatMap((dataset, datasetIndex) => chart.isDatasetVisible(datasetIndex)
			? chart.getDatasetMeta(datasetIndex).data.filter((p) => !p.skip).map((p) => ({x: box.x + p.x, y: box.y + p.y})) : []);
		return {
			logos: images.filter((image) => image.style.visibility === 'visible').map((image) => {
				const r = image.getBoundingClientRect();
				return {left: r.left, top: r.top, right: r.right, bottom: r.bottom};
			}),
			marks,
			area: {left: box.x + chart.chartArea.left, right: box.x + chart.chartArea.right, top: box.y + chart.chartArea.top, bottom: box.y + chart.chartArea.bottom},
			radius: chart.scatterPointRadius,
			total: images.length,
		};
	}`, map[string]any{"canvas": canvas, "layer": layer}, &layout)
	return layout
}

// assertLogoLayout checks the placement rules: logos are 22 to 48px squares
// inside the plot, never overlap each other, and never cover a point.
func assertLogoLayout(t *testing.T, layout logoLayout) {
	t.Helper()
	if len(layout.Logos) < 4 {
		t.Errorf("only %d of %d logos are shown, want at least 4", len(layout.Logos), layout.Total)
	}
	const slack = 0.6
	for i, logo := range layout.Logos {
		width, height := logo.Right-logo.Left, logo.Bottom-logo.Top
		if width < 22-slack || width > 48+slack || math.Abs(width-height) > slack {
			t.Errorf("logo %d is %.1f x %.1f, want a square from 22 to 48px", i, width, height)
		}
		if logo.Left < layout.Area.Left-slack || logo.Right > layout.Area.Right+slack || logo.Top < layout.Area.Top-slack || logo.Bottom > layout.Area.Bottom+slack {
			t.Errorf("logo %d (%v) leaves the plot area %+v", i, logo, layout.Area)
		}
		for j, other := range layout.Logos[i+1:] {
			if logo.Left < other.Right && other.Left < logo.Right && logo.Top < other.Bottom && other.Top < logo.Bottom {
				t.Errorf("logos %d and %d overlap: %v and %v", i, i+1+j, logo, other)
			}
		}
		for _, mark := range layout.Marks {
			if mark.X > logo.Left-layout.Radius+slack && mark.X < logo.Right+layout.Radius-slack &&
				mark.Y > logo.Top-layout.Radius+slack && mark.Y < logo.Bottom+layout.Radius-slack {
				t.Errorf("logo %d (%v) covers the point at (%.1f, %.1f) of radius %.1f", i, logo, mark.X, mark.Y, layout.Radius)
			}
		}
	}
}

// TestExploreTeamLogosAvoidEachOtherAndThePoints checks the logo layout of the
// Outlier plot and Scored vs allowed on a desktop window, after the window is
// resized, and when the logos are turned off.
func TestExploreTeamLogosAvoidEachOtherAndThePoints(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, plot := range []struct{ name, path, canvas, layer, toggle string }{
		{"outlier plot", "explore?view=teams&display=scatter&season=2025", `[data-chart="team-scatter"]`, "[data-team-scatter-chart-wrap] [data-team-scatter-logo-layer]", "[data-team-scatter-logos]"},
		{"scored vs allowed", "explore?view=teams&display=quadrant&season=2025&quadrant-data=both", `[data-chart="team-quadrant"]`, "[data-team-quadrant-chart-wrap] [data-team-scatter-logo-layer]", "[data-team-quadrant-logos]"},
	} {
		t.Run(plot.name, func(t *testing.T) {
			t.Parallel()
			page := explorePage(t, base, Desktop, plot.path)
			expect := playwright.NewPlaywrightAssertions()
			// Logo images load asynchronously and the layout reruns when they do.
			if _, err := page.WaitForFunction(`(arg) => [...document.querySelector(arg.layer).querySelectorAll('img')]
				.some((image) => image.style.visibility === 'visible')`, map[string]any{"layer": plot.layer}); err != nil {
				t.Fatalf("no logo became visible: %v", err)
			}
			assertLogoLayout(t, scatterLogoLayout(t, page, plot.canvas, plot.layer))

			if err := page.SetViewportSize(960, 700); err != nil {
				t.Fatalf("resize: %v", err)
			}
			if err := settle(page); err != nil {
				t.Fatalf("settle after resize: %v", err)
			}
			assertLogoLayout(t, scatterLogoLayout(t, page, plot.canvas, plot.layer))

			if err := page.Locator(plot.toggle).Uncheck(); err != nil {
				t.Fatalf("hide logos: %v", err)
			}
			if err := expect.Locator(page.Locator(plot.layer)).ToBeHidden(); err != nil {
				t.Errorf("logo layer after turning logos off: %v", err)
			}
		})
	}
}

// TestExploreSortsScoringAndHistoryTablesInBothDirections sorts the Scoring
// table and the Season by season table in the browser by clicking their
// headers.
func TestExploreSortsScoringAndHistoryTablesInBothDirections(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	column := func(page playwright.Page, rows string, index int) []string {
		t.Helper()
		var cells []string
		evalJSON(t, page, `(arg) => [...document.querySelectorAll(arg.rows)].map((row) => row.children[arg.index].textContent.trim())`,
			map[string]any{"rows": rows, "index": index}, &cells)
		return cells
	}

	t.Run("scoring table", func(t *testing.T) {
		t.Parallel()
		page := explorePage(t, base, Desktop, "explore?view=table")
		defer expectInPlace(t, page, "sorting the Scoring table")()
		// Matches: 2025 has 240 completed matches and 2026 has 232.
		for _, want := range [][]string{{"240", "232"}, {"232", "240"}} {
			if err := page.Locator(`[data-sort="1"]`).Click(); err != nil {
				t.Fatalf("click Matches header: %v", err)
			}
			if got := column(page, `[data-panel="table"] .explore-table tbody tr`, 1); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("Matches column = %v, want %v", got, want)
			}
		}
	})

	t.Run("season by season", func(t *testing.T) {
		t.Parallel()
		page := explorePage(t, base, Desktop, "explore?view=team-history")
		defer expectInPlace(t, page, "sorting Season by season")()
		link := page.Locator(`[data-history-sort="season"]`)
		for _, want := range [][]string{{"2025", "2026"}, {"2026", "2025"}} {
			if err := link.Click(); err != nil {
				t.Fatalf("click Season header: %v", err)
			}
			if got := historyTableSeasons(t, page); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("Season column = %v, want %v", got, want)
			}
		}
		if got := queryOf(t, page); got.Get("history-sort") != "season" || got.Get("history-order") != "desc" {
			t.Errorf("URL after two clicks = %v, want history-sort=season&history-order=desc", got)
		}
	})
}

// distributionRows returns each Goal distribution table row's cell texts.
func distributionRows(t *testing.T, page playwright.Page) [][]string {
	t.Helper()
	var rows [][]string
	evalJSON(t, page, `() => [...document.querySelectorAll('[data-distribution-rows] tr')]
		.map((row) => [...row.cells].map((cell) => cell.textContent.trim()))`, nil, &rows)
	return rows
}

// distributionSeasons returns the Season column of the distribution table.
func distributionSeasons(t *testing.T, page playwright.Page) string {
	t.Helper()
	var seasons []string
	for _, row := range distributionRows(t, page) {
		seasons = append(seasons, row[0])
	}
	return strings.Join(seasons, ",")
}

// TestExploreGoalDistributionShowsCountsSharesAndBins checks the chart data
// against the table, the bin selector's URL with Back and Forward, keyboard
// inspection of both distribution charts, and table sorting.
func TestExploreGoalDistributionShowsCountsSharesAndBins(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	expect := playwright.NewPlaywrightAssertions()

	t.Run("chart shares match the table counts", func(t *testing.T) {
		t.Parallel()
		page := explorePage(t, base, Desktop, "explore?view=distribution&distribution-sort=season&distribution-order=asc")
		var chart struct {
			Labels []string
			Shares [][]float64
		}
		evalJSON(t, page, `() => {
			const chart = Chart.getChart(document.querySelector('[data-chart="distribution"]'));
			return {labels: chart.data.labels, shares: chart.data.datasets.map((dataset) => dataset.data)};
		}`, nil, &chart)
		rows := distributionRows(t, page)
		if len(rows) != 2 || len(chart.Labels) != 2 || len(chart.Shares) != 5 {
			t.Fatalf("rows = %v, chart labels = %v, datasets = %d; want 2 seasons and 5 bins", rows, chart.Labels, len(chart.Shares))
		}
		for season, row := range rows {
			played, err := strconv.ParseFloat(row[1], 64)
			if err != nil {
				t.Fatalf("matches cell %q: %v", row[1], err)
			}
			var total float64
			for bin := range 5 {
				// A cell reads "20 (8.3%)": the count and its share.
				count, share, _ := strings.Cut(row[2+bin], " (")
				wantCount, err := strconv.ParseFloat(count, 64)
				if err != nil {
					t.Fatalf("count cell %q: %v", row[2+bin], err)
				}
				got := chart.Shares[bin][season]
				if math.Abs(got-wantCount/played*100) > 1e-9 {
					t.Errorf("%s, bin %d: chart share %v, want %v of %v matches", row[0], bin, got, wantCount, played)
				}
				if want := fmt.Sprintf("%.1f%%)", got); share != want {
					t.Errorf("%s, bin %d: table share %q, chart share reads %q", row[0], bin, share, want)
				}
				total += got
			}
			if math.Abs(total-100) > 1e-9 {
				t.Errorf("%s: bin shares sum to %v, want 100", row[0], total)
			}
		}
	})

	t.Run("bin selection reaches the URL and survives Back and Forward", func(t *testing.T) {
		t.Parallel()
		page := explorePage(t, base, Desktop, "explore?view=distribution")
		defer expectInPlace(t, page, "choosing a goal bin")()
		chooseOption(t, page, "[data-distribution-bin]", "3")
		if got := queryOf(t, page).Get("distribution-bin"); got != "3" {
			t.Fatalf("distribution-bin = %q, want 3", got)
		}
		if err := expect.Locator(page.Locator("[data-distribution-trend-panel]")).ToBeVisible(); err != nil {
			t.Errorf("share-by-season chart for one bin: %v", err)
		}
		if err := expect.Locator(page.Locator("[data-distribution-stacked-panel]")).ToBeHidden(); err != nil {
			t.Errorf("stacked chart still shown for one bin: %v", err)
		}
		if err := expect.Locator(page.Locator("#explore-distribution-title")).ToContainText("3 goals"); err != nil {
			t.Errorf("heading for the 3-goal bin: %v", err)
		}
		var shares []*float64
		evalJSON(t, page, `() => Chart.getChart(document.querySelector('[data-chart="bin-trend"]')).data.datasets[0].data`, nil, &shares)
		// The 3-goal bin is 59 of 240 matches in 2025 and 56 of 232 in 2026.
		var populated []float64
		for _, share := range shares {
			if share != nil {
				populated = append(populated, *share)
			}
		}
		if len(populated) != 2 || math.Abs(populated[0]-59.0/240*100) > 1e-9 || math.Abs(populated[1]-56.0/232*100) > 1e-9 {
			t.Errorf("3-goal shares = %v, want %v and %v", populated, 59.0/240*100, 56.0/232*100)
		}

		chooseOption(t, page, "[data-distribution-bin]", "0")
		if _, err := page.GoBack(); err != nil {
			t.Fatalf("back: %v", err)
		}
		if got := queryOf(t, page).Get("distribution-bin"); got != "3" {
			t.Errorf("after Back, distribution-bin = %q, want 3", got)
		}
		if got := controlValues(t, page, []string{"[data-distribution-bin]"})["[data-distribution-bin]"]; got != "3" {
			t.Errorf("after Back, the selector shows %q, want 3", got)
		}
		if _, err := page.GoBack(); err != nil {
			t.Fatalf("back: %v", err)
		}
		if err := expect.Locator(page.Locator("[data-distribution-stacked-panel]")).ToBeVisible(); err != nil {
			t.Errorf("stacked chart after Back to all totals: %v", err)
		}
		if _, err := page.GoForward(); err != nil {
			t.Fatalf("forward: %v", err)
		}
		if got := controlValues(t, page, []string{"[data-distribution-bin]"})["[data-distribution-bin]"]; got != "3" {
			t.Errorf("after Forward, the selector shows %q, want 3", got)
		}
	})

	t.Run("keyboard inspection of both charts", func(t *testing.T) {
		t.Parallel()
		page := explorePage(t, base, Desktop, "explore?view=distribution")
		assertKeyboardInspection(t, page, `[data-chart="distribution"]`, "of 240 matches")
		chooseOption(t, page, "[data-distribution-bin]", "2")
		assertKeyboardInspection(t, page, `[data-chart="bin-trend"]`, "2 goals")
	})

	t.Run("table sorts and the sorted URL opens the disclosure", func(t *testing.T) {
		t.Parallel()
		page := explorePage(t, base, Desktop, "explore?view=distribution")
		defer expectInPlace(t, page, "sorting the distribution table")()
		if err := page.Locator("[data-distribution-values] summary").Click(); err != nil {
			t.Fatalf("open Distribution values: %v", err)
		}
		for _, want := range []struct{ order, seasons string }{{"descending", "2025,2026"}, {"ascending", "2026,2025"}} {
			if err := page.Locator(`[data-distribution-sort="matches"]`).Click(); err != nil {
				t.Fatalf("click Matches header: %v", err)
			}
			if got := distributionSeasons(t, page); got != want.seasons {
				t.Errorf("seasons after sorting %s = %s, want %s", want.order, got, want.seasons)
			}
			if err := expect.Locator(page.Locator(`th:has([data-distribution-sort="matches"])`)).ToHaveAttribute("aria-sort", want.order); err != nil {
				t.Errorf("aria-sort: %v", err)
			}
		}
		if got := queryOf(t, page); got.Get("distribution-sort") != "matches" || got.Get("distribution-order") != "asc" {
			t.Errorf("URL = %v, want distribution-sort=matches&distribution-order=asc", got)
		}
		if _, err := page.GoBack(); err != nil {
			t.Fatalf("back: %v", err)
		}
		if got := distributionSeasons(t, page); got != "2025,2026" {
			t.Errorf("after Back, seasons = %s, want 2025,2026", got)
		}

		// A sorted URL opened directly shows the table already open.
		direct := explorePage(t, base, Desktop, "explore?view=distribution&distribution-sort=matches&distribution-order=asc")
		if err := expect.Locator(direct.Locator("[data-distribution-values]")).ToHaveAttribute("open", ""); err != nil {
			t.Errorf("disclosure of a directly opened sorted URL: %v", err)
		}
		if got := distributionSeasons(t, direct); got != "2026,2025" {
			t.Errorf("directly opened sorted URL shows seasons %s, want 2026,2025", got)
		}
	})
}

// assertKeyboardInspection focuses the chart and walks Home, ArrowRight, End
// and Escape: a mark becomes active with an open tooltip and an announcement
// that contains wantText, and Escape clears all three.
func assertKeyboardInspection(t *testing.T, page playwright.Page, canvas, wantText string) {
	t.Helper()
	if err := page.Locator(canvas).Focus(); err != nil {
		t.Fatalf("focus %s: %v", canvas, err)
	}
	press := func(key string) {
		t.Helper()
		if err := page.Keyboard().Press(key); err != nil {
			t.Fatalf("press %s on %s: %v", key, canvas, err)
		}
	}
	press("Home")
	first := chartInspection(t, page, canvas)
	if len(first.Active) == 0 || first.TooltipActive == 0 || !strings.Contains(first.Status, wantText) {
		t.Fatalf("%s after Home: %+v, want an active mark, an open tooltip and an announcement containing %q", canvas, first, wantText)
	}
	press("ArrowRight")
	second := chartInspection(t, page, canvas)
	if len(second.Active) == 0 || second.Active[0] == first.Active[0] {
		t.Errorf("%s after ArrowRight: %+v, want a different mark than %+v", canvas, second, first.Active)
	}
	press("End")
	last := chartInspection(t, page, canvas)
	if len(last.Active) == 0 || last.Active[0] == first.Active[0] {
		t.Errorf("%s after End: %+v, want the last mark, not %+v", canvas, last, first.Active)
	}
	// Nothing lies beyond the last mark, so ArrowRight must leave it active.
	press("ArrowRight")
	if beyond := chartInspection(t, page, canvas); len(beyond.Active) == 0 || beyond.Active[0] != last.Active[0] || beyond.Status != last.Status {
		t.Errorf("%s: ArrowRight after End moved to %+v, want to stay on %+v", canvas, beyond.Active, last.Active)
	}
	press("Escape")
	if cleared := chartInspection(t, page, canvas); len(cleared.Active) != 0 || cleared.TooltipActive != 0 || cleared.Status != "" {
		t.Errorf("%s after Escape: %+v, want nothing active", canvas, cleared)
	}
}

// TestExploreChartsInspectWithTheKeyboard walks the keyboard on the charts the
// pin test does not cover: the league trend, the paired-dot and gap-bar team
// charts, Match by match and Season by season.
func TestExploreChartsInspectWithTheKeyboard(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=trend")
	for _, chart := range []struct{ name, path, canvas, text string }{
		{"scoring trend", "explore?view=trend", `[data-chart="trend"]`, "per match"},
		{"actual vs expected", "explore?view=teams&display=chart&season=2025", `[data-chart="teams"]`, "played"},
		{"gap to expected", "explore?view=teams&display=gap&season=2025", `[data-chart="team-gap"]`, "Gap:"},
		{"match by match", "explore?view=season-trend&season=2025", `[data-chart="season-trend"]`, ""},
		{"season by season", "explore?view=team-history", `[data-chart="team-history"]`, ""},
	} {
		t.Run(chart.name, func(t *testing.T) {
			gotoInPage(t, page, chart.path)
			assertKeyboardInspection(t, page, chart.canvas, chart.text)
		})
	}
}

// TestExploreDisplaySwitchesRestoreWithBackAndForward moves through the
// Compare teams displays and back, checking the URL, the current tab and which
// plot is shown at each step.
func TestExploreDisplaySwitchesRestoreWithBackAndForward(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&display=chart&season=2025")
	defer expectInPlace(t, page, "switching displays")()
	plots := map[string]string{
		"chart": "[data-team-plot]", "gap": "[data-team-gap-plot]", "scatter": "[data-team-scatter-plot]",
		"quadrant": "[data-team-quadrant-plot]", "table": "[data-team-table]",
	}
	expect := playwright.NewPlaywrightAssertions()
	assertDisplay := func(step, display string) {
		t.Helper()
		if got := queryOf(t, page).Get("display"); got != display {
			t.Errorf("%s: display = %q, want %q", step, got, display)
		}
		if err := expect.Locator(page.Locator("[data-team-display][aria-current=page]").First()).ToHaveAttribute("data-team-display", display); err != nil {
			t.Errorf("%s: current tab: %v", step, err)
		}
		for name, selector := range plots {
			locator := page.Locator("[data-team-results] " + selector).First()
			if name == display {
				if err := expect.Locator(locator).ToBeVisible(); err != nil {
					t.Errorf("%s: the %s plot is not shown: %v", step, name, err)
				}
			} else if err := expect.Locator(locator).ToBeHidden(); err != nil {
				t.Errorf("%s: the %s plot is shown: %v", step, name, err)
			}
		}
	}
	order := []string{"chart", "gap", "scatter", "quadrant", "table"}
	assertDisplay("initial", "chart")
	for _, display := range order[1:] {
		if err := page.Locator(".explore-subviews:not([hidden]) [data-team-display=" + display + "]").Click(); err != nil {
			t.Fatalf("open %s: %v", display, err)
		}
		assertDisplay("forward through "+display, display)
	}
	for i := len(order) - 2; i >= 0; i-- {
		if _, err := page.GoBack(); err != nil {
			t.Fatalf("back: %v", err)
		}
		assertDisplay("back to "+order[i], order[i])
	}
	for _, display := range order[1:] {
		if _, err := page.GoForward(); err != nil {
			t.Fatalf("forward: %v", err)
		}
		assertDisplay("forward to "+display, display)
	}
}

// TestExploreTeamHistoryLeagueContextToggles checks the League context
// checkbox: it reaches the URL, shows the context details, legend and second
// chart, and Back removes them again.
func TestExploreTeamHistoryLeagueContextToggles(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	page := explorePage(t, base, Desktop, "explore?view=team-history")
	defer expectInPlace(t, page, "toggling league context")()
	expect := playwright.NewPlaywrightAssertions()
	assertContext := func(step string, on bool) {
		t.Helper()
		// Off is the default, so the parameter is "off" or absent.
		if got := queryOf(t, page).Get("context"); (got == "on") != on {
			t.Errorf("%s: context = %q, want on = %v", step, got, on)
		}
		if err := expect.Locator(page.Locator("[data-history-context]")).ToBeChecked(playwright.LocatorAssertionsToBeCheckedOptions{Checked: playwright.Bool(on)}); err != nil {
			t.Errorf("%s: checkbox: %v", step, err)
		}
		for _, selector := range []string{"[data-history-context-details]", "[data-history-context-legend]", "[data-history-xg-panel]"} {
			locator := page.Locator(selector)
			if on {
				if err := expect.Locator(locator).ToBeVisible(); err != nil {
					t.Errorf("%s: %s: %v", step, selector, err)
				}
			} else if err := expect.Locator(locator).ToBeHidden(); err != nil {
				t.Errorf("%s: %s: %v", step, selector, err)
			}
		}
	}
	assertContext("initial", false)
	if err := page.Locator("[data-history-context]").Check(); err != nil {
		t.Fatalf("check league context: %v", err)
	}
	assertContext("checked", true)
	if err := page.Locator("[data-history-context]").Uncheck(); err != nil {
		t.Fatalf("uncheck league context: %v", err)
	}
	assertContext("unchecked", false)
	if _, err := page.GoBack(); err != nil {
		t.Fatalf("back: %v", err)
	}
	assertContext("Back to checked", true)
	if _, err := page.GoBack(); err != nil {
		t.Fatalf("back: %v", err)
	}
	assertContext("Back to the start", false)
}

// historyTableSeasons returns the Season column of the Season by season table.
func historyTableSeasons(t *testing.T, page playwright.Page) []string {
	t.Helper()
	texts, err := page.Locator("[data-team-history-rows] tr th").AllTextContents()
	if err != nil {
		t.Fatalf("history table seasons: %v", err)
	}
	return texts
}

// TestExploreWorksWithoutJavaScript turns scripts off and uses what the server
// renders: sortable header links, GET forms with their submit buttons, and the
// full tables.
func TestExploreWorksWithoutJavaScript(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := noScriptPage(t, Desktop)
	goTo := func(path string) {
		t.Helper()
		if _, err := page.Goto(base + path); err != nil {
			t.Fatalf("goto %s: %v", path, err)
		}
	}
	expect := playwright.NewPlaywrightAssertions()

	t.Run("compare teams table sorts through links", func(t *testing.T) {
		goTo("explore?view=teams&display=table&season=2025")
		if err := expect.Locator(page.Locator("[data-team-rows] tr")).ToHaveCount(fixtureTeams); err != nil {
			t.Fatalf("server-rendered table rows: %v", err)
		}
		for _, descending := range []bool{true, false} {
			if err := page.Locator(`[data-team-sort="for-actual"]`).Click(); err != nil {
				t.Fatalf("click sort link: %v", err)
			}
			assertSorted(t, "goals scored", mustNumbers(t, teamTableColumn(t, page, columnForActual)), descending)
			want := "ascending"
			if descending {
				want = "descending"
			}
			if err := expect.Locator(page.Locator(`th:has([data-team-sort="for-actual"])`)).ToHaveAttribute("aria-sort", want); err != nil {
				t.Errorf("aria-sort: %v", err)
			}
		}
	})

	t.Run("compare teams form submits a GET", func(t *testing.T) {
		goTo("explore?view=teams&display=table&season=2025")
		chooseOption(t, page, "[data-team-season]", "2026")
		chooseOption(t, page, "[data-team-units]", "total")
		if err := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Show comparison"}).Click(); err != nil {
			t.Fatalf("submit: %v", err)
		}
		query := queryOf(t, page)
		for key, want := range map[string]string{"view": "teams", "display": "table", "season": "2026", "units": "total"} {
			if got := query.Get(key); got != want {
				t.Errorf("submitted URL %s = %q, want %q (%s)", key, got, want, page.URL())
			}
		}
		if err := expect.Locator(page.Locator("[data-team-table-caption]")).ToContainText("Totals"); err != nil {
			t.Errorf("table caption after submitting totals: %v", err)
		}
	})

	t.Run("season by season sorts and filters", func(t *testing.T) {
		goTo("explore?view=team-history")
		if got := historyTableSeasons(t, page); len(got) != 2 || got[0] != "2026" || got[1] != "2025" {
			t.Fatalf("default season order = %v, want newest first", got)
		}
		if err := page.Locator(`[data-history-sort="season"]`).Click(); err != nil {
			t.Fatalf("click season header: %v", err)
		}
		if got := historyTableSeasons(t, page); len(got) != 2 || got[0] != "2025" || got[1] != "2026" {
			t.Errorf("season order after toggling = %v, want oldest first", got)
		}
		if got := queryOf(t, page); got.Get("history-sort") != "season" || got.Get("history-order") != "asc" {
			t.Errorf("URL after toggling = %v, want history-sort=season&history-order=asc", got)
		}
		chooseOption(t, page, "[data-history-team]", teamGotham)
		if err := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Show team history"}).Click(); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if err := expect.Locator(page.Locator("[data-history-team-name]")).ToContainText("Gotham FC"); err != nil {
			t.Errorf("history team after submitting: %v", err)
		}
		if got := queryOf(t, page); got.Get("history-order") != "asc" {
			t.Errorf("submitting the form dropped the sort order: %v", got)
		}
	})

	t.Run("rankings form submits a GET", func(t *testing.T) {
		goTo("explore?view=team-rankings&season=2025")
		chooseOption(t, page, "[data-team-rankings-controls] [name=team]", teamGotham)
		if err := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Show rankings"}).Click(); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if err := expect.Locator(page.Locator("[data-rankings-team]")).ToContainText("Gotham FC"); err != nil {
			t.Errorf("rankings team after submitting: %v", err)
		}
		if err := expect.Locator(page.Locator("[data-rankings-cards] .explore-rank-card")).ToHaveCount(6); err != nil {
			t.Errorf("rank cards: %v", err)
		}
	})

	t.Run("match by match form submits a GET", func(t *testing.T) {
		goTo("explore?view=season-trend&season=2025")
		chooseOption(t, page, "[data-trend-mode]", "match")
		if err := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Show matches"}).Click(); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if got := queryOf(t, page).Get("trend-mode"); got != "match" {
			t.Errorf("submitted trend-mode = %q, want match (%s)", got, page.URL())
		}
		if err := expect.Locator(page.Locator("[data-trend-rows] tr")).ToHaveCount(30); err != nil {
			t.Errorf("match values table rows: %v", err)
		}
	})
}
