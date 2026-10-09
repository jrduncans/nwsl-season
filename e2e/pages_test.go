//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/server"
)

// T9: browser tests for the pages that script (standings, forecast, clinching)
// and render-and-fit checks for the rest. The local-time, no-script and
// clipboard cases need browser-context options that newPage does not take, so
// newPageWith builds the context itself and applies the same failure rules.

// pageOptions are the browser-context settings a test needs beyond a viewport.
type pageOptions struct {
	Viewport viewport
	// Timezone is an IANA zone such as "Asia/Tokyo"; empty keeps the machine's.
	Timezone string
	// NoScript turns JavaScript off for the whole context.
	NoScript bool
	// Clipboard grants the page clipboard-read and clipboard-write.
	Clipboard bool
}

// newPageWith is newPage with context options. The test fails if the page logs
// a console error, throws, has a same-origin request fail or return a 4xx/5xx
// status, or violates the CSP.
func newPageWith(t *testing.T, opts pageOptions) playwright.Page {
	t.Helper()
	contextOptions := playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{Width: opts.Viewport.Width, Height: opts.Viewport.Height},
		// Fix the locale so Intl output does not depend on the machine.
		Locale: playwright.String("en-US"),
	}
	if opts.Timezone != "" {
		contextOptions.TimezoneId = playwright.String(opts.Timezone)
	}
	if opts.NoScript {
		contextOptions.JavaScriptEnabled = playwright.Bool(false)
	}
	if opts.Clipboard {
		contextOptions.Permissions = []string{"clipboard-read", "clipboard-write"}
	}
	browserContext, err := browser.NewContext(contextOptions)
	if err != nil {
		t.Fatalf("new browser context: %v", err)
	}
	if err := browserContext.AddInitScript(playwright.Script{Content: playwright.String(cspReporter)}); err != nil {
		t.Fatalf("add CSP init script: %v", err)
	}
	stubClubLogos(t, browserContext)
	traceDir := os.Getenv(traceDirEnv)
	if traceDir != "" {
		if err := browserContext.Tracing().Start(playwright.TracingStartOptions{Snapshots: playwright.Bool(true)}); err != nil {
			t.Fatalf("start tracing: %v", err)
		}
	}
	page, err := browserContext.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}

	var (
		mu       sync.Mutex
		problems []string
	)
	record := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	page.On("console", func(message playwright.ConsoleMessage) {
		if message.Type() == "error" {
			record("console error: %s", message.Text())
		}
	})
	page.On("pageerror", func(err error) { record("page error: %v", err) })
	page.On("requestfailed", func(request playwright.Request) {
		if sameOrigin(page, request.URL()) {
			record("request failed: %s %s: %v", request.Method(), request.URL(), request.Failure())
		}
	})
	page.On("response", func(response playwright.Response) {
		if sameOrigin(page, response.URL()) && response.Status() >= 400 {
			record("bad response: %s %s: %d", response.Request().Method(), response.URL(), response.Status())
		}
	})
	t.Cleanup(func() {
		if !opts.NoScript {
			// With JavaScript off, page.Evaluate never returns.
			_ = settle(page)
		}
		mu.Lock()
		for _, problem := range problems {
			t.Errorf("%s viewport: %s", opts.Viewport.Name, problem)
		}
		mu.Unlock()
		switch {
		case traceDir == "":
		case t.Failed():
			if err := os.MkdirAll(traceDir, 0o750); err != nil { //nolint:gosec // G703: traceDir is the developer-set trace directory, not request input
				t.Logf("create trace directory: %v", err)
			} else if err := browserContext.Tracing().Stop(filepath.Join(traceDir, traceName(t, opts.Viewport)+".zip")); err != nil {
				t.Logf("save trace: %v", err)
			}
		default:
			if err := browserContext.Tracing().Stop(); err != nil {
				t.Logf("stop tracing: %v", err)
			}
		}
		if err := browserContext.Close(); err != nil {
			t.Logf("close browser context: %v", err)
		}
	})
	return page
}

// visitStatic loads rawURL in a page whose JavaScript is off and requires a
// 2xx status and a visible heading. visit cannot be used there: it settles the
// page with page.Evaluate, which never returns without JavaScript.
func visitStatic(t *testing.T, page playwright.Page, rawURL string) {
	t.Helper()
	response, err := page.Goto(rawURL, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateLoad})
	if err != nil {
		t.Fatalf("goto %s: %v", rawURL, err)
	}
	if response == nil || !response.Ok() {
		t.Fatalf("goto %s: not 2xx", rawURL)
	}
	expect := playwright.NewPlaywrightAssertions()
	if err := expect.Locator(page.Locator("h1").First()).ToBeVisible(); err != nil {
		t.Fatalf("%s has no visible h1: %v", rawURL, err)
	}
}

// evalInto runs script in the page (with arg when non-nil) and decodes the
// JSON-serializable result into out.
func evalInto(t *testing.T, page playwright.Page, script string, arg any, out any) {
	t.Helper()
	var (
		result any
		err    error
	)
	if arg == nil {
		result, err = page.Evaluate(script)
	} else {
		result, err = page.Evaluate(script, arg)
	}
	if err != nil {
		t.Fatalf("evaluate on %s: %v", page.URL(), err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encode result on %s: %v", page.URL(), err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode result on %s: %v (%s)", page.URL(), err, raw)
	}
}

// selectValue picks the option with the given value.
func selectValue(t *testing.T, locator playwright.Locator, value string) {
	t.Helper()
	if _, err := locator.SelectOption(playwright.SelectOptionValues{Values: &[]string{value}}); err != nil {
		t.Fatalf("select %q: %v", value, err)
	}
}

func click(t *testing.T, locator playwright.Locator) {
	t.Helper()
	if err := locator.Click(); err != nil {
		t.Fatalf("click: %v", err)
	}
}

func seasonPath(rest string) string { return "seasons/" + currentSeason + "/" + rest }

// TestPages browses the pages of one 16-team half-played season. The subtests
// only read the shared fixture, so they run in parallel.
func TestPages(t *testing.T) {
	f := newFixture(t)
	f.ASA.ResetRequests()
	// Parallel subtests finish before the parent's cleanups run.
	t.Cleanup(func() { assertNoASARequests(t, f.ASA) })
	cases := map[string]func(*testing.T, *fixture){
		"standings local times":     testLocalTimes,
		"standings sorting":         testStandingsSorting,
		"standings without script":  testStandingsNoScript,
		"fixture toggle no script":  testFixtureToggleNoScript,
		"forecast assumption flow":  testForecastAssumptionFlow,
		"forecast copy link":        testForecastCopyLink,
		"forecast compare model":    testForecastCompareModel,
		"forecast without script":   testForecastNoScript,
		"fixtures":                  testFixturesPage,
		"schedule difficulty":       testScheduleDifficultyPage,
		"model evaluation":          testModelEvaluationPage,
		"proxy prefix on all pages": testProxyPrefix,
	}
	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		run := cases[name]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run(t, f)
		})
	}
}

// ---------------------------------------------------------------- local time

// expectedKickoff is how standings.js formats a UTC instant for a viewer in loc
// with the en-US locale: "Fri, Oct 2 at 4:00 AM GMT+9".
func expectedKickoff(t *testing.T, utc string, loc *time.Location) string {
	t.Helper()
	instant, err := time.Parse(time.RFC3339, utc)
	if err != nil {
		t.Fatalf("parse kickoff %q: %v", utc, err)
	}
	return normalizeSpace(localDate(instant, loc) + " at " + instant.In(loc).Format("3:04 PM") + " " + gmtLabel(instant.In(loc)))
}

func localDate(instant time.Time, loc *time.Location) string {
	return instant.In(loc).Format("Mon, Jan 2")
}

// gmtLabel is Chromium's short zone name for zones without an en-US
// abbreviation: GMT+9, GMT+5:30.
func gmtLabel(local time.Time) string {
	_, offset := local.Zone()
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	label := "GMT" + sign + strconv.Itoa(offset/3600)
	if minutes := offset % 3600 / 60; minutes != 0 {
		label += fmt.Sprintf(":%02d", minutes)
	}
	return label
}

// normalizeSpace folds the narrow no-break space that Intl puts before AM/PM
// into ordinary spacing.
func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

type timeStamp struct {
	UTC  string `json:"utc"`
	Text string `json:"text"`
}

const readFixtureTimesScript = `() => Array.from(document.querySelectorAll("ul.fixtures time[data-local-time]")).map((el) => ({
	utc: el.dataset.localTime, text: el.textContent,
}))`

// testLocalTimes checks that kickoffs are rewritten into the browser's time
// zone: the same page shows different text in two zones, each equal to the
// expected local rendering of the UTC instant.
func testLocalTimes(t *testing.T, f *fixture) {
	zones := []string{"Asia/Tokyo", "Asia/Kolkata"}
	texts := make(map[string][]string, len(zones))
	for _, zone := range zones {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Fatalf("load %s: %v", zone, err)
			}
			page := newPageWith(t, pageOptions{Viewport: Desktop, Timezone: zone})
			visit(t, page, f.URL(seasonPath("fixtures")))
			var stamps []timeStamp
			evalInto(t, page, readFixtureTimesScript, nil, &stamps)
			if len(stamps) != fixtureGames {
				t.Fatalf("kickoff times = %d, want %d", len(stamps), fixtureGames)
			}
			for _, stamp := range stamps {
				if got, want := normalizeSpace(stamp.Text), expectedKickoff(t, stamp.UTC, loc); got != want {
					t.Errorf("kickoff %s in %s = %q, want %q", stamp.UTC, zone, got, want)
				}
				texts[zone] = append(texts[zone], stamp.Text)
			}

			// The forecast builder labels its fixture options the same way.
			visit(t, page, f.URL(seasonPath("forecast")))
			var option struct {
				UTC  string `json:"utc"`
				Text string `json:"text"`
			}
			evalInto(t, page, `() => {
				const el = document.querySelector("#forecast-fixture option");
				return { utc: el.dataset.localTime, text: el.textContent };
			}`, nil, &option)
			if want := expectedKickoff(t, option.UTC, loc); !strings.HasPrefix(normalizeSpace(option.Text), want+" · ") {
				t.Errorf("forecast fixture option = %q, want prefix %q", option.Text, want+" · ")
			}
		})
	}
	// Guard against both zones passing because nothing was localized: the
	// zones are 3.5 hours apart, so every kickoff text must differ.
	tokyo, kolkata := texts[zones[0]], texts[zones[1]]
	if len(tokyo) == 0 || len(tokyo) != len(kolkata) {
		return
	}
	for i := range tokyo {
		if tokyo[i] == kolkata[i] {
			t.Errorf("kickoff %d reads %q in both zones", i, tokyo[i])
			break
		}
	}
}

// ------------------------------------------------------------------ standing

type standingsRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Position   string `json:"position"`
	Points     string `json:"points"`
	PerGame    string `json:"perGame"`
	Total      string `json:"total"`
	XGPerGame  string `json:"xgPerGame"`
	XGTotal    string `json:"xgTotal"`
	PerGamePos string `json:"perGamePos"`
	TotalPos   string `json:"totalPos"`
}

const readStandingsScript = `() => Array.from(document.querySelectorAll("table.standings tbody tr")).map((row) => {
	const points = row.querySelector("[data-standings-points]");
	const position = row.querySelector("[data-standings-position]");
	return {
		id: row.dataset.teamId,
		name: row.dataset.teamName,
		position: position.textContent.trim(),
		points: points.querySelector("[data-standings-value]").textContent.trim(),
		perGame: points.dataset.perGame,
		total: points.dataset.total,
		xgPerGame: points.dataset.xgPerGame,
		xgTotal: points.dataset.xgTotal,
		perGamePos: position.dataset.perGame,
		totalPos: position.dataset.total,
	};
})`

func readStandingsRows(t *testing.T, page playwright.Page) []standingsRow {
	t.Helper()
	var rows []standingsRow
	evalInto(t, page, readStandingsScript, nil, &rows)
	if len(rows) != fixtureTeams {
		t.Fatalf("standings rows = %d, want %d", len(rows), fixtureTeams)
	}
	return rows
}

func floatOf(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parse number %q: %v", s, err)
	}
	return v
}

func ids(rows []standingsRow) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.ID
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertSequentialPositions requires the position cells to read 1..16 in row order.
func assertSequentialPositions(t *testing.T, label string, rows []standingsRow) {
	t.Helper()
	for i, row := range rows {
		if row.Position != strconv.Itoa(i+1) {
			t.Errorf("%s: row %d (%s) shows position %q, want %d", label, i, row.Name, row.Position, i+1)
		}
	}
}

// testStandingsSorting drives the Per game/Totals and Goals/xG toggles on the
// home standings and checks the row order, the positions and the values after
// every change.
func testStandingsSorting(t *testing.T, f *fixture) {
	page := newPageWith(t, pageOptions{Viewport: Desktop})
	visit(t, page, f.URL(""))
	expect := playwright.NewPlaywrightAssertions()
	caption := page.Locator("[data-standings-caption]")
	modeLabel := page.Locator("[data-standings-mode-label]")
	pointsHeader := page.Locator("th[data-standings-points-label]")
	perGame := page.Locator("[data-standings-mode-value=per-game]")
	totals := page.Locator("[data-standings-mode-value=total]")
	goals := page.Locator("[data-standings-stat-value=goals]")
	xg := page.Locator("[data-standings-stat-value=xg]")
	pressed := func(button playwright.Locator, want bool) {
		t.Helper()
		if err := expect.Locator(button).ToHaveAttribute("aria-pressed", strconv.FormatBool(want)); err != nil {
			t.Errorf("aria-pressed should be %v: %v", want, err)
		}
	}

	// Goals, per game: the server's order.
	initial := readStandingsRows(t, page)
	assertSequentialPositions(t, "initial", initial)
	pressed(perGame, true)
	pressed(totals, false)
	pressed(goals, true)
	if err := expect.Locator(pointsHeader).ToHaveText("Pts"); err != nil {
		t.Error(err)
	}
	for _, row := range initial {
		if row.Points != row.PerGame {
			t.Errorf("initial: %s shows %q points, want per-game %q", row.Name, row.Points, row.PerGame)
		}
	}
	if got := sortedByPositionData(initial, func(r standingsRow) string { return r.PerGamePos }); !sameStrings(got, ids(initial)) {
		t.Errorf("initial order = %v, want per-game position order %v", ids(initial), got)
	}

	// Totals: values switch to totals, positions follow the total ranking.
	click(t, totals)
	pressed(totals, true)
	pressed(perGame, false)
	if err := expect.Locator(modeLabel).ToHaveText("totals"); err != nil {
		t.Error(err)
	}
	byTotals := readStandingsRows(t, page)
	assertSequentialPositions(t, "totals", byTotals)
	if got := sortedByPositionData(byTotals, func(r standingsRow) string { return r.TotalPos }); !sameStrings(got, ids(byTotals)) {
		t.Errorf("totals order = %v, want total position order %v", ids(byTotals), got)
	}
	for i, row := range byTotals {
		if row.Points != row.Total {
			t.Errorf("totals: %s shows %q points, want total %q", row.Name, row.Points, row.Total)
		}
		if i > 0 && floatOf(t, row.Total) > floatOf(t, byTotals[i-1].Total) {
			t.Errorf("totals: %s (%s points) sits below %s (%s points)", row.Name, row.Total, byTotals[i-1].Name, byTotals[i-1].Total)
		}
	}
	click(t, perGame)
	pressed(perGame, true)
	if err := expect.Locator(modeLabel).ToHaveText("per game"); err != nil {
		t.Error(err)
	}
	if got := ids(readStandingsRows(t, page)); !sameStrings(got, ids(initial)) {
		t.Errorf("per game again: order = %v, want the initial %v", got, ids(initial))
	}

	// xG: expected points order the rows, ties by team name, positions renumber.
	click(t, xg)
	pressed(xg, true)
	pressed(goals, false)
	if err := expect.Locator(pointsHeader).ToHaveText("xPts"); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(caption).ToHaveText(initialCaption(t, page, true)); err != nil {
		t.Error(err)
	}
	byXG := readStandingsRows(t, page)
	assertSequentialPositions(t, "xG per game", byXG)
	assertXGOrder(t, "xG per game", byXG, func(r standingsRow) string { return r.XGPerGame })
	for _, row := range byXG {
		if row.Points != row.XGPerGame {
			t.Errorf("xG per game: %s shows %q, want %q", row.Name, row.Points, row.XGPerGame)
		}
	}
	if sameStrings(ids(byXG), ids(initial)) {
		t.Error("xG order equals the goals order, so the fixture cannot show that the xG sort ran")
	}
	click(t, totals)
	pressed(totals, true)
	if err := expect.Locator(modeLabel).ToHaveText("totals"); err != nil {
		t.Error(err)
	}
	byXGTotal := readStandingsRows(t, page)
	assertSequentialPositions(t, "xG totals", byXGTotal)
	assertXGOrder(t, "xG totals", byXGTotal, func(r standingsRow) string { return r.XGTotal })

	// Back to goals and per game restores the server's order.
	click(t, goals)
	pressed(goals, true)
	click(t, perGame)
	pressed(perGame, true)
	if err := expect.Locator(pointsHeader).ToHaveText("Pts"); err != nil {
		t.Error(err)
	}
	if got := ids(readStandingsRows(t, page)); !sameStrings(got, ids(initial)) {
		t.Errorf("goals per game again: order = %v, want the initial %v", got, ids(initial))
	}
	assertNoHorizontalOverflow(t, page)
}

func initialCaption(t *testing.T, page playwright.Page, xg bool) string {
	t.Helper()
	attribute := "data-goals-label"
	if xg {
		attribute = "data-xg-label"
	}
	value, err := page.Locator("[data-standings-caption]").GetAttribute(attribute)
	if err != nil {
		t.Fatalf("read caption label: %v", err)
	}
	return value
}

func sortedByPositionData(rows []standingsRow, key func(standingsRow) string) []string {
	sorted := append([]standingsRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, _ := strconv.Atoi(key(sorted[i]))
		b, _ := strconv.Atoi(key(sorted[j]))
		return a < b
	})
	return ids(sorted)
}

// assertXGOrder requires rows in descending value order with ties broken by
// team name, which is how standings.js sorts the xG view.
func assertXGOrder(t *testing.T, label string, rows []standingsRow, value func(standingsRow) string) {
	t.Helper()
	for i := 1; i < len(rows); i++ {
		prev, cur := floatOf(t, value(rows[i-1])), floatOf(t, value(rows[i]))
		switch {
		case cur > prev:
			t.Errorf("%s: %s (%v) sits below %s (%v)", label, rows[i].Name, cur, rows[i-1].Name, prev)
		case cur == prev && strings.Compare(rows[i-1].Name, rows[i].Name) > 0:
			t.Errorf("%s: tied %s should come before %s", label, rows[i].Name, rows[i-1].Name)
		}
	}
}

// testStandingsNoScript checks the server-rendered standings with JavaScript
// off: the full ordered table is there and the script-only controls change
// nothing.
func testStandingsNoScript(t *testing.T, f *fixture) {
	page := newPageWith(t, pageOptions{Viewport: Desktop, NoScript: true, Timezone: "Asia/Tokyo"})
	visitStatic(t, page, f.URL(""))
	expect := playwright.NewPlaywrightAssertions()
	rowsOf := page.Locator("table.standings tbody tr")
	readRows := func() (order []string) {
		t.Helper()
		if err := expect.Locator(rowsOf).ToHaveCount(fixtureTeams); err != nil {
			t.Fatal(err)
		}
		for i := range fixtureTeams {
			row := rowsOf.Nth(i)
			id, err := row.GetAttribute("data-team-id")
			if err != nil {
				t.Fatalf("read row %d: %v", i, err)
			}
			order = append(order, id)
			position, err := row.Locator("[data-standings-position]").TextContent()
			if err != nil || strings.TrimSpace(position) != strconv.Itoa(i+1) {
				t.Errorf("row %d (%s) shows position %q, want %d (%v)", i, id, position, i+1, err)
			}
			shown, err := row.Locator("[data-standings-points] [data-standings-value]").TextContent()
			perGame, perGameErr := row.Locator("[data-standings-points]").GetAttribute("data-per-game")
			if err != nil || perGameErr != nil || strings.TrimSpace(shown) != perGame {
				t.Errorf("row %d (%s) shows %q points, want per-game %q (%v, %v)", i, id, shown, perGame, err, perGameErr)
			}
		}
		return order
	}
	before := readRows()
	// The buttons are present but cannot act without the script.
	click(t, page.Locator("[data-standings-mode-value=total]"))
	click(t, page.Locator("[data-standings-stat-value=xg]"))
	if after := readRows(); !sameStrings(after, before) {
		t.Errorf("order changed without script: %v then %v", before, after)
	}
	if err := expect.Locator(page.Locator("[data-standings-mode-value=per-game]")).ToHaveAttribute("aria-pressed", "true"); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(page.Locator("th[data-standings-points-label]")).ToHaveText("Pts"); err != nil {
		t.Error(err)
	}

	// Kickoffs keep the server's text instead of the browser's zone.
	visitStatic(t, page, f.URL(seasonPath("fixtures")))
	times := page.Locator("ul.fixtures time[data-local-time]")
	if err := expect.Locator(times).ToHaveCount(fixtureGames); err != nil {
		t.Fatal(err)
	}
	texts, err := times.AllTextContents()
	if err != nil {
		t.Fatalf("read kickoff texts: %v", err)
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	fallback := regexp.MustCompile(`^[A-Z][a-z]{2} [A-Z][a-z]{2} \d{1,2}, \d{1,2}:\d{2} [AP]M \S+$`)
	for i, text := range texts {
		utc, err := times.Nth(i).GetAttribute("data-local-time")
		if err != nil {
			t.Fatalf("read kickoff %d: %v", i, err)
		}
		text = normalizeSpace(text)
		if !fallback.MatchString(text) {
			t.Errorf("kickoff %s reads %q without script, want the server's text", utc, text)
		}
		if text == expectedKickoff(t, utc, tokyo) {
			t.Errorf("kickoff %s was localized without script: %q", utc, text)
		}
	}
	// Every fixture is listed and the page carries the filter's no-script note
	// (a noscript element is never displayed in this browser setup, so it is
	// only checked for presence).
	if err := expect.Locator(page.Locator("[data-fixture-home-team]")).ToHaveCount(fixtureGames); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(page.Locator("noscript p", playwright.PageLocatorOptions{HasText: "Team filtering requires JavaScript"})).ToBeAttached(); err != nil {
		t.Error(err)
	}
}

// testFixtureToggleNoScript checks that the Results/Upcoming toggle, which only
// the script can operate, stays hidden when JavaScript is off.
func testFixtureToggleNoScript(t *testing.T, f *fixture) {
	t.Skip("bug: .fixture-view-toggle display:inline-flex overrides hidden without JS; see https://github.com/jrduncans/nwsl-season/issues/122")
	page := newPageWith(t, pageOptions{Viewport: Desktop, NoScript: true})
	visitStatic(t, page, f.URL(seasonPath("fixtures")))
	expect := playwright.NewPlaywrightAssertions()
	if err := expect.Locator(page.Locator("[data-fixture-view-toggle]")).ToBeHidden(); err != nil {
		t.Errorf("the fixture view toggle should be hidden without JavaScript: %v", err)
	}
}

// ------------------------------------------------------------------ forecast

const (
	modelDefault    = "xg-poisson-schedule-load-v1"
	modelComparison = "results-poisson-home-two-seasons-v1"
	teamUnderTest   = "team-0"
	// remainingPerTeam is each team's unplayed games: 15 of 30.
	remainingPerTeam = fixtureTeams - 1
)

// forecastNavTimeout bounds navigations that make the server compute a
// forecast. On a loaded CI runner one took over 8s, so the 5s assertion
// default is too short; this does not change what is asserted.
const forecastNavTimeout = 30 * time.Second

// waitForecastURL waits for the page to navigate to a URL matching want and
// finish loading, allowing forecastNavTimeout for the server's computation.
func waitForecastURL(page playwright.Page, want any) error {
	if err := playwright.NewPlaywrightAssertions(forecastNavTimeout.Seconds() * 1000).Page(page).ToHaveURL(want); err != nil {
		return err
	}
	return page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateLoad, Timeout: playwright.Float(forecastNavTimeout.Seconds() * 1000),
	})
}

var assumptionURL = regexp.MustCompile(`[?&]p=`)

// fixedResults reads the forecast page's "Fixed results" count, which counts assumptions, not played games.
func fixedResults(t *testing.T, page playwright.Page) int {
	t.Helper()
	text, err := page.Locator(".forecast-meta div:has(dt:text-is('Fixed results')) dd").TextContent()
	if err != nil {
		t.Fatalf("read fixed results: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		t.Fatalf("fixed results %q: %v", text, err)
	}
	return n
}

// testForecastAssumptionFlow builds a scenario with the script: filter by
// team, choose a fixture, add a result, remove one, and apply.
func testForecastAssumptionFlow(t *testing.T, f *fixture) {
	for _, vp := range []viewport{Desktop, Mobile} {
		t.Run(vp.Name, func(t *testing.T) {
			page := newPageWith(t, pageOptions{Viewport: vp})
			visit(t, page, f.URL(seasonPath("forecast")))
			expect := playwright.NewPlaywrightAssertions()
			baseline := fixedResults(t, page)
			if baseline != 0 {
				t.Fatalf("baseline fixed results = %d, want 0 (played games are not assumptions)", baseline)
			}
			fixtureOptions := page.Locator("#forecast-fixture option")
			pendingItems := page.Locator("#forecast-pending-list li")
			applyButton := page.Locator("#forecast-update-button")
			addButton := page.Locator("form[data-assumption-builder] button[type=submit]")
			expectCount := func(locator playwright.Locator, want int, what string) {
				t.Helper()
				if err := expect.Locator(locator).ToHaveCount(want); err != nil {
					t.Errorf("%s should number %d: %v", what, want, err)
				}
			}

			// Unfiltered, every unplayed game is a choice.
			expectCount(fixtureOptions, fixtureGames-fixturePlayedGames, "unfiltered fixture options")
			if err := expect.Locator(page.Locator("#forecast-pending")).ToBeHidden(); err != nil {
				t.Error(err)
			}
			if err := expect.Locator(applyButton).ToBeDisabled(); err != nil {
				t.Error(err)
			}

			// Filtering by team keeps only that team's games, labeled by venue.
			selectValue(t, page.Locator("#forecast-team"), teamUnderTest)
			expectCount(fixtureOptions, remainingPerTeam, "team-0 fixture options")
			var options []struct {
				Home, Away, Text string
			}
			evalInto(t, page, `() => Array.from(document.querySelectorAll("#forecast-fixture option")).map((o) => ({
				home: o.dataset.homeTeamId, away: o.dataset.awayTeamId, text: o.textContent,
			}))`, nil, &options)
			for _, option := range options {
				if option.Home != teamUnderTest && option.Away != teamUnderTest {
					t.Errorf("option %q is not a %s game", option.Text, teamUnderTest)
				}
				if !strings.Contains(option.Text, "Home vs") && !strings.Contains(option.Text, "Away at") {
					t.Errorf("option %q is not labeled by venue", option.Text)
				}
			}

			// Choosing a fixture relabels the result choices with its teams.
			if _, err := page.Locator("#forecast-fixture").SelectOption(playwright.SelectOptionValues{Indexes: &[]int{1}}); err != nil {
				t.Fatalf("select second fixture: %v", err)
			}
			var teams struct{ Home, Away string }
			evalInto(t, page, `() => {
				const o = document.querySelector("#forecast-fixture").selectedOptions[0];
				return { home: o.dataset.homeTeam, away: o.dataset.awayTeam };
			}`, nil, &teams)
			if err := expect.Locator(page.Locator(`[data-forecast-outcome="h"]`)).ToHaveText(teams.Home + " win"); err != nil {
				t.Error(err)
			}
			if err := expect.Locator(page.Locator(`[data-forecast-outcome="a"]`)).ToHaveText(teams.Away + " win"); err != nil {
				t.Error(err)
			}

			// Add a draw, then an away win; each takes its fixture off the list.
			if err := page.Locator("input[name=outcome][value=d]").Check(); err != nil {
				t.Fatalf("choose draw: %v", err)
			}
			click(t, addButton)
			expectCount(pendingItems, 1, "pending assumptions")
			expectCount(fixtureOptions, remainingPerTeam-1, "fixture options after one add")
			if err := expect.Locator(page.Locator("#forecast-pending-status")).ToHaveText("1 new assumption ready to apply."); err != nil {
				t.Error(err)
			}
			if err := expect.Locator(applyButton).ToHaveText("Apply scenario (1)"); err != nil {
				t.Error(err)
			}
			if err := expect.Locator(applyButton).ToBeEnabled(); err != nil {
				t.Error(err)
			}
			if err := expect.Locator(pendingItems.First()).ToContainText("Draw"); err != nil {
				t.Error(err)
			}

			var kept struct{ Home, Away string }
			evalInto(t, page, `() => {
				const o = document.querySelector("#forecast-fixture").selectedOptions[0];
				return { home: o.dataset.homeTeam, away: o.dataset.awayTeam };
			}`, nil, &kept)
			if err := page.Locator("input[name=outcome][value=a]").Check(); err != nil {
				t.Fatalf("choose away win: %v", err)
			}
			click(t, addButton)
			expectCount(pendingItems, 2, "pending assumptions")
			expectCount(fixtureOptions, remainingPerTeam-2, "fixture options after two adds")
			if err := expect.Locator(page.Locator("#forecast-pending-status")).ToHaveText("2 new assumptions ready to apply."); err != nil {
				t.Error(err)
			}
			if err := expect.Locator(applyButton).ToHaveText("Apply scenario (2)"); err != nil {
				t.Error(err)
			}

			// Removing the draw returns its fixture to the list.
			click(t, page.Locator("#forecast-pending-list button[data-remove-assumption]").First())
			expectCount(pendingItems, 1, "pending assumptions after remove")
			expectCount(fixtureOptions, remainingPerTeam-1, "fixture options after remove")
			if err := expect.Locator(pendingItems.First()).ToContainText(kept.Away + " win"); err != nil {
				t.Error(err)
			}

			// Applying navigates to the scenario, which fixes one more result.
			click(t, applyButton)
			if err := waitForecastURL(page, assumptionURL); err != nil {
				t.Fatalf("applying the scenario should navigate to a URL with p=: %v", err)
			}
			if err := expect.Locator(page.Locator("h1")).ToHaveText("Forecast lab"); err != nil {
				t.Error(err)
			}
			if got := fixedResults(t, page); got != baseline+1 {
				t.Errorf("fixed results after apply = %d, want %d", got, baseline+1)
			}
			assumptions := page.Locator("section.forecast-scenario .forecast-assumptions li")
			expectCount(assumptions, 1, "applied assumptions")
			if err := expect.Locator(assumptions.First()).ToContainText(kept.Away + " win"); err != nil {
				t.Error(err)
			}
			if err := expect.Locator(page.GetByText("Reset all assumptions")).ToBeVisible(); err != nil {
				t.Error(err)
			}
			assertNoHorizontalOverflow(t, page)
		})
	}
}

// testForecastCopyLink applies a scenario, copies its link, and opens the
// copied link to find the same scenario.
func testForecastCopyLink(t *testing.T, f *fixture) {
	page := newPageWith(t, pageOptions{Viewport: Desktop, Clipboard: true})
	visit(t, page, f.URL(seasonPath("forecast")))
	expect := playwright.NewPlaywrightAssertions()
	selectValue(t, page.Locator("#forecast-team"), teamUnderTest)
	if err := page.Locator("input[name=outcome][value=d]").Check(); err != nil {
		t.Fatalf("choose draw: %v", err)
	}
	click(t, page.Locator("form[data-assumption-builder] button[type=submit]"))
	click(t, page.Locator("#forecast-update-button"))
	if err := waitForecastURL(page, assumptionURL); err != nil {
		t.Fatalf("applying the scenario should navigate to a URL with p=: %v", err)
	}
	scenarioURL := page.URL()
	assumptionText, err := page.Locator("section.forecast-scenario .forecast-assumptions").TextContent()
	if err != nil || strings.TrimSpace(assumptionText) == "" {
		t.Fatalf("applied scenario has no assumption text: %q, %v", assumptionText, err)
	}

	link := page.Locator("[data-copy-scenario]")
	label := page.Locator("[data-copy-scenario-label]")
	if err := expect.Locator(label).ToHaveText("Copy link"); err != nil {
		t.Error(err)
	}
	click(t, link)
	if err := expect.Locator(page.Locator("[data-scenario-copy-status]")).ToHaveText("Scenario link copied to the clipboard."); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(label).ToHaveText("Copied"); err != nil {
		t.Error(err)
	}
	var copied string
	evalInto(t, page, `() => navigator.clipboard.readText()`, nil, &copied)
	var href string
	evalInto(t, page, `() => document.querySelector("[data-copy-scenario]").href`, nil, &href)
	if copied != href {
		t.Errorf("clipboard = %q, want the link's address %q", copied, href)
	}
	parsed, err := url.Parse(copied)
	if err != nil || !strings.HasPrefix(parsed.Path, mountPrefix+"/") || !assumptionURL.MatchString("?"+parsed.RawQuery) {
		t.Errorf("copied link %q should point at the scenario under %s/", copied, mountPrefix)
	}
	if parsed != nil && parsed.Query().Get("p") != mustQuery(t, scenarioURL).Get("p") {
		t.Errorf("copied link assumes p=%q, the page's URL has p=%q", parsed.Query().Get("p"), mustQuery(t, scenarioURL).Get("p"))
	}
	// The label returns to its original text.
	if err := expect.Locator(label).ToHaveText("Copy link"); err != nil {
		t.Errorf("label should reset after the copy notice: %v", err)
	}

	// A fresh page in the same context opens the copied link to the same scenario.
	reopened, err := page.Context().NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	visit(t, reopened, copied)
	got, err := reopened.Locator("section.forecast-scenario .forecast-assumptions").TextContent()
	if err != nil {
		t.Fatalf("read reopened assumptions: %v", err)
	}
	if got != assumptionText {
		t.Errorf("copied link shows assumptions %q, want %q", got, assumptionText)
	}
	var reopenedHref string
	evalInto(t, reopened, `() => document.querySelector("[data-copy-scenario]").href`, nil, &reopenedHref)
	if reopenedHref != href {
		t.Errorf("reopened page's canonical link = %q, want %q", reopenedHref, href)
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed.Query()
}

// testForecastCompareModel keeps a scenario while the model and its comparison
// change.
func testForecastCompareModel(t *testing.T, f *fixture) {
	page := newPageWith(t, pageOptions{Viewport: Desktop})
	visit(t, page, f.URL(seasonPath("forecast")))
	expect := playwright.NewPlaywrightAssertions()
	if err := expect.Locator(page.Locator(".forecast-comparison")).ToHaveCount(0); err != nil {
		t.Errorf("no comparison before one is chosen: %v", err)
	}
	selectValue(t, page.Locator("#forecast-team"), teamUnderTest)
	click(t, page.Locator("form[data-assumption-builder] button[type=submit]"))
	click(t, page.Locator("#forecast-update-button"))
	if err := waitForecastURL(page, assumptionURL); err != nil {
		t.Fatalf("applying the scenario should navigate to a URL with p=: %v", err)
	}
	assumed := mustQuery(t, page.URL()).Get("p")

	// The comparison option for the selected model is disabled.
	if err := expect.Locator(page.Locator(`#forecast-comparison option[value="`+modelDefault+`"]`)).ToHaveJSProperty("disabled", true); err != nil {
		t.Error(err)
	}
	click(t, page.Locator(".forecast-comparison-control summary")) // opens the closed disclosure
	selectValue(t, page.Locator("#forecast-comparison"), modelComparison)
	if err := waitForecastURL(page, regexp.MustCompile(`[?&]c=`+modelComparison)); err != nil {
		t.Fatalf("choosing a comparison should reload with c=: %v", err)
	}
	if got := mustQuery(t, page.URL()).Get("p"); got != assumed {
		t.Errorf("comparison dropped the assumption: p=%q, want %q", got, assumed)
	}
	section := page.Locator(".forecast-comparison")
	if err := expect.Locator(section.Locator("h2")).ToHaveText(regexp.MustCompile(`^.+ vs Results Poisson$`)); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(section.Locator("table tbody tr")).ToHaveCount(fixtureTeams); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(page.Locator(`#forecast-model option[value="`+modelDefault+`"]`)).ToHaveJSProperty("selected", true); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(page.Locator(`#forecast-comparison option[value="`+modelComparison+`"]`)).ToHaveJSProperty("selected", true); err != nil {
		t.Error(err)
	}
	if got := fixedResults(t, page); got != 1 {
		t.Errorf("fixed results with comparison = %d, want %d", got, 1)
	}
	assertNoHorizontalOverflow(t, page)

	// Making the compared model the main model clears the comparison.
	selectValue(t, page.Locator("#forecast-model"), modelComparison)
	if err := waitForecastURL(page, regexp.MustCompile(`[?&]m=`+modelComparison)); err != nil {
		t.Fatalf("choosing a model should reload with m=: %v", err)
	}
	if err := expect.Locator(page.Locator(".forecast-comparison")).ToHaveCount(0); err != nil {
		t.Errorf("comparison should clear when it equals the model: %v", err)
	}
	if got := mustQuery(t, page.URL()).Get("p"); got != assumed {
		t.Errorf("model change dropped the assumption: p=%q, want %q", got, assumed)
	}
}

// testForecastNoScript adds an assumption with JavaScript off: the form falls
// back to a server-side add.
func testForecastNoScript(t *testing.T, f *fixture) {
	page := newPageWith(t, pageOptions{Viewport: Desktop, NoScript: true})
	visitStatic(t, page, f.URL(seasonPath("forecast")))
	expect := playwright.NewPlaywrightAssertions()
	// The script-only pending list stays hidden and the apply button disabled.
	if err := expect.Locator(page.Locator("#forecast-pending")).ToBeHidden(); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(page.Locator("#forecast-update-button")).ToHaveJSProperty("disabled", true); err != nil {
		t.Error(err)
	}
	// The server lists every unplayed game.
	if err := expect.Locator(page.Locator("#forecast-fixture option")).ToHaveCount(fixtureGames - fixturePlayedGames); err != nil {
		t.Error(err)
	}
	if err := page.Locator("input[name=outcome][value=d]").Check(); err != nil {
		t.Fatalf("choose draw: %v", err)
	}
	click(t, page.Locator("form[data-assumption-builder] button[type=submit]"))
	if err := waitForecastURL(page, assumptionURL); err != nil {
		t.Fatalf("the plain form should add the assumption on the server: %v", err)
	}
	if err := expect.Locator(page.Locator("section.forecast-scenario .forecast-assumptions li")).ToHaveCount(1); err != nil {
		t.Error(err)
	}
	if err := expect.Locator(page.Locator("section.forecast-scenario .forecast-assumptions li").First()).ToContainText("Draw"); err != nil {
		t.Error(err)
	}
	if got := fixedResults(t, page); got != 1 {
		t.Errorf("fixed results = %d, want %d", got, 1)
	}
	// The model form's submit button replaces the auto-submit.
	if err := expect.Locator(page.Locator("[data-forecast-model-form] button[type=submit]")).ToBeAttached(); err != nil {
		t.Error(err)
	}
}

// ------------------------------------------------------- plain render checks

// forEachViewport runs check in a subtest at the desktop and mobile widths.
func forEachViewport(t *testing.T, check func(*testing.T, viewport)) {
	t.Helper()
	for _, vp := range []viewport{Desktop, Mobile} {
		t.Run(vp.Name, func(t *testing.T) { check(t, vp) })
	}
}

func testFixturesPage(t *testing.T, f *fixture) {
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp})
		visit(t, page, f.URL(seasonPath("fixtures")))
		expect := playwright.NewPlaywrightAssertions()
		if err := expect.Locator(page.Locator("[data-fixture-home-team]")).ToHaveCount(fixtureGames); err != nil {
			t.Error(err)
		}
		assertNoHorizontalOverflow(t, page)

		// The team filter narrows the list to that team's 30 games and back.
		selectValue(t, page.Locator("#fixture-team"), teamUnderTest)
		if err := expect.Locator(page.Locator("[data-fixture-filter-summary]")).ToHaveText("Showing 30 fixtures for Team 0 FC."); err != nil {
			t.Error(err)
		}
		if err := expect.Locator(page.Locator("[data-fixture-home-team]:not([hidden])")).ToHaveCount(2 * (fixtureTeams - 1)); err != nil {
			t.Error(err)
		}
		selectValue(t, page.Locator("#fixture-team"), "")
		if err := expect.Locator(page.Locator("[data-fixture-filter-summary]")).ToHaveText("All teams shown."); err != nil {
			t.Error(err)
		}
		assertNoHorizontalOverflow(t, page)
	})
}

func testScheduleDifficultyPage(t *testing.T, f *fixture) {
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp})
		visit(t, page, f.URL(seasonPath("schedule-difficulty")))
		expect := playwright.NewPlaywrightAssertions()
		if err := expect.Locator(page.GetByText("Toughest remaining schedule")).ToBeVisible(); err != nil {
			t.Error(err)
		}
		if err := expect.Locator(page.Locator(".schedule-plot-row")).Not().ToHaveCount(0); err != nil {
			t.Errorf("schedule plot has no rows: %v", err)
		}
		assertNoHorizontalOverflow(t, page)
	})
}

func testModelEvaluationPage(t *testing.T, f *fixture) {
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp})
		visit(t, page, f.URL(seasonPath("model-evaluation")))
		expect := playwright.NewPlaywrightAssertions()
		svg := page.Locator("[data-evaluation-svg]")
		if err := expect.Locator(svg.Locator("path").First()).ToBeAttached(); err != nil {
			t.Error(err)
		}
		if err := expect.Locator(svg.Locator("title")).ToHaveText("Final-points forecast error through the season"); err != nil {
			t.Error(err)
		}
		selectValue(t, page.Locator("[data-evaluation-metric]"), "position")
		if err := expect.Locator(svg.Locator("title")).ToHaveText("Final table-position forecast error through the season"); err != nil {
			t.Error(err)
		}
		assertNoHorizontalOverflow(t, page)
	})
}

// ---------------------------------------------------------------- clinching

// assertKeyboardDisclosures focuses every details summary on the page and
// checks that Enter and Space each expand and collapse it, showing and hiding
// its content. It returns how many disclosures it found.
func assertKeyboardDisclosures(t *testing.T, page playwright.Page) int {
	t.Helper()
	expect := playwright.NewPlaywrightAssertions()
	count, err := page.Locator("details").Count()
	if err != nil {
		t.Fatalf("count disclosures: %v", err)
	}
	for i := range count {
		details := page.Locator("details").Nth(i)
		summary := details.Locator("> summary")
		name, err := summary.InnerText()
		if err != nil {
			t.Fatalf("read summary %d: %v", i, err)
		}
		if err := summary.Focus(); err != nil {
			t.Fatalf("focus %q: %v", name, err)
		}
		if err := expect.Locator(summary).ToBeFocused(); err != nil {
			t.Errorf("%q should take focus: %v", name, err)
		}
		if err := expect.Locator(details).ToHaveJSProperty("open", false); err != nil {
			t.Errorf("%q should start collapsed: %v", name, err)
		}
		for _, key := range []string{"Enter", "Space"} {
			if err := summary.Press(key); err != nil {
				t.Fatalf("press %s on %q: %v", key, name, err)
			}
			if err := expect.Locator(details).ToHaveJSProperty("open", true); err != nil {
				t.Errorf("%s should expand %q: %v", key, name, err)
			}
			// The body is shown while expanded.
			if err := expect.Locator(details.Locator("> :not(summary)").First()).ToBeVisible(); err != nil {
				t.Errorf("%q should show its content when expanded: %v", name, err)
			}
			if err := summary.Press(key); err != nil {
				t.Fatalf("press %s on %q: %v", key, name, err)
			}
			if err := expect.Locator(details).ToHaveJSProperty("open", false); err != nil {
				t.Errorf("%s should collapse %q: %v", key, name, err)
			}
			if err := expect.Locator(details.Locator("> :not(summary)").First()).ToBeHidden(); err != nil {
				t.Errorf("%q should hide its content when collapsed: %v", name, err)
			}
		}
	}
	return count
}

// TestPagesClinching keyboard-toggles every disclosure on the late-season clinching
// page. The late-season arrangement (see lateSeason) gives team-0 and team-8
// clinching and elimination scenarios grouped by result.
func TestPagesClinching(t *testing.T) {
	j := newJourney(t, lateSeason())
	path := seasonPath("clinching")
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp, Timezone: "Asia/Tokyo"})
		visit(t, page, j.URL(path))
		expect := playwright.NewPlaywrightAssertions()

		// Results are grouped under one heading per outcome.
		groups := page.Locator("section.clinching-result-group h4")
		headings, err := groups.AllTextContents()
		if err != nil || len(headings) == 0 {
			t.Fatalf("no grouped result headings: %v, %v", headings, err)
		}
		for _, heading := range headings {
			if !strings.HasPrefix(heading, "If ") {
				t.Errorf("group heading %q should start with \"If \"", heading)
			}
		}
		if err := expect.Locator(page.Locator(`h3[aria-label="Team 0 FC can clinch the playoffs"]`)).ToBeVisible(); err != nil {
			t.Error(err)
		}

		// The slate's dates are written in the viewer's zone.
		var dates []timeStamp
		evalInto(t, page, `() => Array.from(document.querySelectorAll("time[data-local-date]")).map((el) => ({ utc: el.dataset.localDate, text: el.textContent }))`, nil, &dates)
		tokyo, _ := time.LoadLocation("Asia/Tokyo")
		if len(dates) == 0 {
			t.Error("the slate has no local dates")
		}
		for _, date := range dates {
			instant, err := time.Parse(time.RFC3339, date.UTC)
			if err != nil {
				t.Fatalf("parse %q: %v", date.UTC, err)
			}
			if got, want := normalizeSpace(date.Text), localDate(instant, tokyo); got != want {
				t.Errorf("slate date %s = %q, want %q", date.UTC, got, want)
			}
		}

		if got := assertKeyboardDisclosures(t, page); got < 2 {
			t.Errorf("clinching page has %d disclosures, want at least the slate and a season-long path", got)
		}
		assertNoHorizontalOverflow(t, page)
	})
}

// TestPagesClinchingExactPaths covers the grouped summary with its "View exact
// paths" disclosure, which appears only when a result needs one of several
// outside results. Arrangement after 29 of 30 rounds: team-1..6 are far ahead
// and the rest far behind; team-0 has 47 points, team-8 46 and team-9 44, and
// only their last-round games are unplayed. Team-8 and team-9 can each pass
// team-0 only by winning, and team-0 stays in the top eight unless both do, so
// a result for team-0 that needs help has two disjoint outside alternatives.
// The test asserts the page shows the disclosure, not the arithmetic.
func TestPagesClinchingExactPaths(t *testing.T) {
	order := []string{"team-1", "team-2", "team-3", "team-4", "team-5", "team-6",
		"team-0", "team-8", "team-9", "team-7", "team-10", "team-11", "team-12", "team-13", "team-14", "team-15"}
	rank := func(id string) int {
		for i, o := range order {
			if o == id {
				return i
			}
		}
		panic("unknown team " + id)
	}
	// Strength order decides every game except team-0 v team-8 and team-8 v
	// team-9, which are draws.
	draw := func(a, b string) bool { return (a == "team-0" && b == "team-8") || (a == "team-8" && b == "team-9") }
	cfg := lateSeason()
	cfg.score = func(g asa.Game) (int, int) {
		if draw(g.HomeTeamID, g.AwayTeamID) || draw(g.AwayTeamID, g.HomeTeamID) {
			return 1, 1
		}
		if rank(g.HomeTeamID) < rank(g.AwayTeamID) {
			return 1, 0
		}
		return 0, 1
	}
	cfg.leaveUnplayed = func(g asa.Game) bool {
		in := func(id string) bool { return id == "team-0" || id == "team-8" || id == "team-9" }
		return in(g.HomeTeamID) || in(g.AwayTeamID)
	}
	j := newJourney(t, cfg)
	table := expectedTable(j.Teams, j.Games)
	for id, want := range map[string]int{"team-0": 47, "team-8": 46, "team-9": 44} {
		if got := table[id].Points; got != want {
			t.Fatalf("%s has %d points, want %d", id, got, want)
		}
	}
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp})
		visit(t, page, j.URL(seasonPath("clinching")))
		expect := playwright.NewPlaywrightAssertions()
		exact := page.Locator("details.clinching-exact-paths")
		if n, err := exact.Count(); err != nil || n == 0 {
			t.Fatalf("no \"View exact paths\" disclosure on the page (%d, %v); the arrangement no longer needs outside results", n, err)
		}
		// The grouped summary sits next to the disclosure, outside it.
		if err := expect.Locator(page.Locator("section.clinching-result-group .clinching-summary").First()).ToBeVisible(); err != nil {
			t.Errorf("grouped summary should be visible: %v", err)
		}
		if err := expect.Locator(exact.First().Locator("summary")).ToHaveText("View exact paths"); err != nil {
			t.Error(err)
		}
		assertKeyboardDisclosures(t, page)
		assertNoHorizontalOverflow(t, page)
	})
}

// ------------------------------------------------------------------ bracket

// newBracketFixture serves a cache seeded with the teams scenario plus a 2024
// Playoffs knockout, with no ASA behind it. A knockout stage renders a bracket
// only when it has at least one concrete pairing.
func newBracketFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("NWSL_DATA_DIR", dir)
	t.Setenv("NWSL_SYNC_SEASON", currentSeason)
	t.Setenv("NWSL_SYNC_STAGE", "Regular Season")
	cfg, err := config.FromEnvironment()
	if err != nil {
		t.Fatalf("config.FromEnvironment: %v", err)
	}
	cfg.DataDir = dir
	cfg.DBPath = filepath.Join(dir, "nwsl-season.sqlite")

	ctx := context.Background()
	db, err := cache.Open(ctx, cfg.DBPath)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	now := time.Now().UTC()
	// A seeded archive gives the other pages data to link to.
	apptest.Seed(t, db, apptest.ScenarioTeams)
	teams := []cache.Team{
		{ASAID: "alpha", Name: "Alpha", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"},
		{ASAID: "bravo", Name: "Bravo", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"},
	}
	var games []cache.Game
	for i, score := range [][2]int64{{2, 1}, {1, 1}, {0, 3}} {
		game := cache.Game{
			ASAID: fmt.Sprintf("playoff-%d", i), Season: "2024", Stage: "Playoffs", Status: fixtures.CompletedStatus,
			HomeTeamID: "alpha", AwayTeamID: "bravo", KnockoutGame: true, RawJSON: "{}",
			KickoffUTC: time.Date(2024, 11, 8+i*7, 1, 0, 0, 0, time.UTC).Format("2006-01-02 15:04:05 MST"),
			HomeScore:  sql.NullInt64{Int64: score[0], Valid: true}, AwayScore: sql.NullInt64{Int64: score[1], Valid: true},
		}
		games = append(games, game)
	}
	if _, err := db.ReplaceSeason(ctx, "2024", "Playoffs", teams, games, now); err != nil {
		t.Fatalf("seed 2024 playoffs: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed cache: %v", err)
	}

	srv, err := server.Build(ctx, cfg, server.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		ASABaseURL: "http://127.0.0.1:1", // unroutable: nothing here may reach ASA
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
	return &fixture{Server: srv, BaseURL: web.URL + mountPrefix + "/"}
}

func TestPagesBracket(t *testing.T) {
	f := newBracketFixture(t)
	path := "seasons/2024/playoffs"
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp})
		visit(t, page, f.URL(path))
		expect := playwright.NewPlaywrightAssertions()
		if err := expect.Locator(page.Locator("[data-bracket-state]")).ToBeVisible(); err != nil {
			t.Errorf("the page should be a bracket: %v", err)
		}
		for _, round := range []string{"Quarterfinals", "Semifinals", "Final"} {
			if err := expect.Locator(page.Locator(".bracket-round h2", playwright.PageLocatorOptions{HasText: round}).First()).ToBeVisible(); err != nil {
				t.Errorf("bracket should show %s: %v", round, err)
			}
		}
		if err := expect.Locator(page.Locator(".bracket-match").First()).ToContainText("Alpha"); err != nil {
			t.Error(err)
		}
		assertNoHorizontalOverflow(t, page)
	})
	t.Run("proxy prefix", func(t *testing.T) {
		assertUnderPrefix(t, Desktop, f, path, false)
	})
}

// -------------------------------------------------------------- proxy prefix

// pageLink is a URL a page references.
type pageLink struct {
	Kind   string `json:"kind"`
	Raw    string `json:"raw"`
	URL    string `json:"url"`
	Hidden bool   `json:"hidden"`
}

const collectLinksScript = `() => {
	const links = [];
	const add = (kind, selector, attribute) => document.querySelectorAll(selector).forEach((el) => {
		const raw = el.getAttribute(attribute);
		if (!raw || raw.startsWith("#") || /^(javascript|mailto|data):/.test(raw)) return;
		links.push({ kind, raw, url: new URL(raw, document.baseURI).href, hidden: el.hidden });
	});
	add("a", "a[href]", "href");
	add("link", "link[href]", "href");
	add("script", "script[src]", "src");
	add("img", "img[src]", "src");
	add("form", "form[action]", "action");
	return links;
}`

// assertUnderPrefix loads path and checks that every network response, link
// and asset that stays on the app's origin is under the /nwsl-season/ mount,
// and, when resolveLinks is set, that each visible same-origin link answers
// 2xx or 3xx (newPage already fails the test on a 4xx/5xx response during the
// page load). The bracket fixture sets it false: its cache has no synced
// current season, so pages such as the home page legitimately error.
func assertUnderPrefix(t *testing.T, vp viewport, f *fixture, path string, resolveLinks bool) {
	t.Helper()
	page := newPageWith(t, pageOptions{Viewport: vp})
	var (
		mu        sync.Mutex
		responses []string
	)
	page.On("response", func(response playwright.Response) {
		mu.Lock()
		defer mu.Unlock()
		responses = append(responses, response.URL())
	})
	visit(t, page, f.URL(path))
	base, err := url.Parse(f.BaseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	underPrefix := func(raw string) (sameOrigin, ok bool) {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host != base.Host {
			return false, true
		}
		return true, parsed.Path == mountPrefix || strings.HasPrefix(parsed.Path, mountPrefix+"/")
	}

	mu.Lock()
	seenResponses := append([]string(nil), responses...)
	mu.Unlock()
	if len(seenResponses) == 0 {
		t.Errorf("%s: no network responses recorded", path)
	}
	for _, response := range seenResponses {
		if same, ok := underPrefix(response); same && !ok {
			t.Errorf("%s: response %s is outside %s/", path, response, mountPrefix)
		}
	}

	var links []pageLink
	evalInto(t, page, collectLinksScript, nil, &links)
	if len(links) == 0 {
		t.Fatalf("%s: found no links or assets", path)
	}
	unique := map[string]bool{}
	hiddenTargets := map[string]bool{}
	for _, link := range links {
		same, ok := underPrefix(link.URL)
		if !same {
			continue // external, such as the GitHub link and team logos
		}
		if !ok {
			t.Errorf("%s: %s %q resolves to %s, outside %s/", path, link.Kind, link.Raw, link.URL, mountPrefix)
			continue
		}
		parsed, _ := url.Parse(link.URL)
		parsed.Fragment = ""
		unique[parsed.String()] = true
		if link.Hidden {
			// The season and competition switchers keep a hidden link per
			// choice for the script to click. A season without the feature
			// answers 404 with the intentional "<feature> unavailable" page.
			hiddenTargets[parsed.String()] = true
		}
	}
	targets := make([]string, 0, len(unique))
	for target := range unique {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	if !resolveLinks {
		return
	}
	// Fetch outside the page: the CSP's connect-src would block the page's own
	// fetch. The request context uses the browser context's network settings.
	for _, target := range targets {
		response, err := page.Context().Request().Get(target)
		if err != nil {
			t.Errorf("%s: link %s: %v", path, target, err)
			continue
		}
		status := response.Status()
		if status >= 200 && status < 400 {
			continue
		}
		if hiddenTargets[target] && status == http.StatusNotFound {
			if body, err := response.Text(); err == nil && strings.Contains(body, " is unavailable for ") {
				continue
			}
		}
		t.Errorf("%s: link %s answered %d", path, target, status)
	}
}

// testProxyPrefix checks every page of the season, plus a forecast with a
// comparison, at the desktop width. The browser sees the app only under the
// /nwsl-season/ mount, as behind the production proxy.
func testProxyPrefix(t *testing.T, f *fixture) {
	paths := []string{
		"seasons",
		"history",
		"history/scoring",
		"seasons/" + currentSeason,
		"explore",
		"",
		seasonPath("fixtures"),
		seasonPath("schedule-difficulty"),
		seasonPath("forecast"),
		seasonPath("forecast") + "?v=2&m=" + modelDefault + "&c=" + modelComparison,
		seasonPath("model-evaluation"),
		seasonPath("clinching"),
	}
	for _, path := range paths {
		t.Run("/"+path, func(t *testing.T) { assertUnderPrefix(t, Desktop, f, path, true) })
	}
}
