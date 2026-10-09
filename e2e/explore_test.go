//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/server"
)

// exploreBase serves the application from a cache seeded with an apptest
// scenario, as cmd/preview does, and returns the app root with a trailing
// slash. The scheduler is off and the ASA base URL is unroutable, so nothing
// here can reach ASA.
func exploreBase(t *testing.T, scenario string) string {
	t.Helper()
	t.Setenv("NWSL_DATA_DIR", t.TempDir())
	cfg, err := config.FromEnvironment()
	if err != nil {
		t.Fatalf("config.FromEnvironment: %v", err)
	}
	cfg.DBPath = filepath.Join(cfg.DataDir, "nwsl-season.sqlite")
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
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		ASABaseURL:     "http://127.0.0.1:1",
		StartScheduler: false,
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

// logoPNG is a small opaque PNG that stands in for team logos, so the tests
// never contact the logo host.
var logoPNG = func() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.NRGBA{R: 0x33, G: 0x66, B: 0x99, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}()

// logoHost matches the club logo origin the Content Security Policy allows.
const logoHost = "https://american-soccer-analysis-headshots.s3.amazonaws.com/**"

// stubLogos answers every request to the club logo host with logoPNG.
func stubLogos(t *testing.T, page playwright.Page) {
	t.Helper()
	err := page.Route(logoHost, func(route playwright.Route) {
		if err := route.Fulfill(playwright.RouteFulfillOptions{Body: logoPNG, ContentType: playwright.String("image/png")}); err != nil {
			t.Logf("fulfill logo: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("stub logos: %v", err)
	}
}

// explorePage opens the Explore page at path (relative to the app root) in a
// browser sized to vp, with logos stubbed, and waits for its scripts.
func explorePage(t *testing.T, base string, vp viewport, path string) playwright.Page {
	t.Helper()
	page := newPage(t, vp)
	stubLogos(t, page)
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
	for _, view := range exploreViews() {
		t.Run(view.name, func(t *testing.T) {
			base := exploreBase(t, view.scenario)
			page := explorePage(t, base, Desktop, view.path)

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
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&display=chart")

	var requests []string
	page.On("request", func(request playwright.Request) {
		if request.ResourceType() != "image" {
			requests = append(requests, request.ResourceType()+" "+request.URL())
		}
	})
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
	if len(requests) > 0 {
		t.Errorf("switching analyses made %d request(s), want none: %s", len(requests), strings.Join(requests, ", "))
	}
}

// TestExploreHasNoHorizontalOverflowOnPhone checks every analysis at 390px.
func TestExploreHasNoHorizontalOverflowOnPhone(t *testing.T) {
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, tab := range exploreTabs {
		t.Run(tab.tab, func(t *testing.T) {
			page := explorePage(t, base, Mobile, "explore?"+tab.query)
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
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&display=table&season=2026")

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
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport:          &playwright.Size{Width: vp.Width, Height: vp.Height},
		JavaScriptEnabled: playwright.Bool(false),
	})
	if err != nil {
		t.Fatalf("new browser context: %v", err)
	}
	page, err := context.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	stubLogos(t, page)
	var mu sync.Mutex
	var problems []string
	page.On("response", func(response playwright.Response) {
		if sameOrigin(page, response.URL()) && response.Status() >= 400 {
			mu.Lock()
			problems = append(problems, fmt.Sprintf("%s: %d", response.URL(), response.Status()))
			mu.Unlock()
		}
	})
	t.Cleanup(func() {
		mu.Lock()
		for _, problem := range problems {
			t.Errorf("no-script page: bad response %s", problem)
		}
		mu.Unlock()
		if err := context.Close(); err != nil {
			t.Logf("close browser context: %v", err)
		}
	})
	return page
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
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, scenario := range pinScenarios {
		t.Run(scenario.name, func(t *testing.T) {
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
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, measure := range []string{"for", "against", "difference", "points"} {
		for _, units := range []string{"per-match", "total"} {
			t.Run(measure+"/"+units, func(t *testing.T) {
				page := explorePage(t, base, Desktop, "explore?view=teams&display=scatter&season=2025&measure="+measure+"&units="+units)
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
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, mode := range []string{"goals", "xg", "both"} {
		t.Run(mode, func(t *testing.T) {
			page := explorePage(t, base, Desktop, "explore?view=teams&display=quadrant&season=2025&quadrant-data="+mode)
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
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
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

			gap := explorePage(t, base, Desktop, path+"gap")
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

			scatter := explorePage(t, base, Desktop, path+"scatter")
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
		page := explorePage(t, base, Desktop, "explore?view=teams&season=2026&display=table")
		if err := expect.Locator(page.Locator("[data-team-warning]")).ToContainText("xG and xPts"); err != nil {
			t.Errorf("table warning: %v", err)
		}
		_, missing := numericCells(t, teamTableColumn(t, page, columnForXG))
		if missing != 2 {
			t.Errorf("table shows %d unavailable xG values, want 2", missing)
		}
	})

	t.Run("scored vs allowed", func(t *testing.T) {
		page := explorePage(t, base, Desktop, "explore?view=teams&season=2026&display=quadrant&quadrant-data=both")
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

// TestExploreShowsEmptyStatesForSeasonsWithoutData selects a season whose
// results are not in the cache, and a team with no results in a season.
func TestExploreShowsEmptyStatesForSeasonsWithoutData(t *testing.T) {
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	expect := playwright.NewPlaywrightAssertions()
	for _, display := range []string{"chart", "gap", "scatter", "quadrant", "table"} {
		t.Run(display, func(t *testing.T) {
			page := explorePage(t, base, Desktop, "explore?view=teams&season=2024&display="+display)
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
		page := explorePage(t, base, Desktop, "explore?view=team-rankings&season=2024")
		if err := expect.Locator(page.Locator("[data-rankings-empty]")).ToBeVisible(); err != nil {
			t.Errorf("empty rankings message: %v", err)
		}
		if err := expect.Locator(page.Locator("[data-rankings-results]")).ToBeHidden(); err != nil {
			t.Errorf("rank cards are shown for an empty season: %v", err)
		}
	})
	t.Run("match by match", func(t *testing.T) {
		page := explorePage(t, base, Desktop, "explore?view=season-trend&season=2024")
		if err := expect.Locator(page.Locator("[data-season-trend-empty]")).ToBeVisible(); err != nil {
			t.Errorf("empty match message: %v", err)
		}
	})
}

// TestExploreSquarePlotsFollowTheViewport checks the Outlier plot and Scored vs
// allowed sizing: both charts are square, grow to the viewport height less the
// page chrome with a 608px floor on desktop, and shrink to fit on a phone.
func TestExploreSquarePlotsFollowTheViewport(t *testing.T) {
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, plot := range []struct {
		name, path, canvas, wrap string
		// chrome is the vertical space the CSS reserves around the plot.
		chrome float64
	}{
		{"outlier plot", "explore?view=teams&display=scatter&season=2025", `[data-chart="team-scatter"]`, ".explore-team-scatter-wrap", 72},
		{"scored vs allowed", "explore?view=teams&display=quadrant&season=2025", `[data-chart="team-quadrant"]`, ".explore-team-quadrant-wrap", 32},
	} {
		for _, vp := range []viewport{
			{Name: "short desktop", Width: 1280, Height: 500},
			Desktop,
			{Name: "tall desktop", Width: 1280, Height: 1000},
			Mobile,
		} {
			t.Run(plot.name+"/"+vp.Name, func(t *testing.T) {
				page := explorePage(t, base, vp, plot.path)
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

// TestExploreSortsScoringAndHistoryTablesInBothDirections sorts the Scoring
// table and the Season by season table in the browser by clicking their
// headers.
func TestExploreSortsScoringAndHistoryTablesInBothDirections(t *testing.T) {
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	column := func(page playwright.Page, rows string, index int) []string {
		t.Helper()
		var cells []string
		evalJSON(t, page, `(arg) => [...document.querySelectorAll(arg.rows)].map((row) => row.children[arg.index].textContent.trim())`,
			map[string]any{"rows": rows, "index": index}, &cells)
		return cells
	}

	t.Run("scoring table", func(t *testing.T) {
		page := explorePage(t, base, Desktop, "explore?view=table")
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
		page := explorePage(t, base, Desktop, "explore?view=team-history")
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
