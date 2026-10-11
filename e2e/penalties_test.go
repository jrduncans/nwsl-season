//go:build e2e

package e2e

import (
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/apptest"
)

// penaltiesSelect is the visible Penalties select of the current view.
const penaltiesSelect = "select[data-penalties-selector]:visible"

// choosePenalties changes the Penalties select and waits for the full server
// render on the chosen basis.
func choosePenalties(t *testing.T, page playwright.Page, choice string) {
	t.Helper()
	chooseOption(t, page, penaltiesSelect, choice)
	if _, err := page.WaitForFunction(`(want) => (new URLSearchParams(location.search).get('penalties') === 'exclude') === (want === 'exclude')`, choice); err != nil {
		t.Fatalf("penalties %s did not navigate: %v", choice, err)
	}
	if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateLoad}); err != nil {
		t.Fatalf("load after penalties %s: %v", choice, err)
	}
	expect := playwright.NewPlaywrightAssertions()
	if err := expect.Locator(page.Locator(penaltiesSelect)).ToHaveValue(choice); err != nil {
		t.Fatalf("penalties %s is not selected after the reload: %v", choice, err)
	}
	if err := settle(page); err != nil {
		t.Fatalf("settle: %v", err)
	}
}

func textOf(t *testing.T, page playwright.Page, selector string) string {
	t.Helper()
	text, err := page.Locator(selector).First().InnerText()
	if err != nil {
		t.Fatalf("read %s: %v", selector, err)
	}
	return text
}

func TestExplorePenaltiesToggleSwitchesBasisAndSurvivesNavigation(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=teams&display=table&season=2025")
	expect := playwright.NewPlaywrightAssertions()

	if got := textOf(t, page, "label.penalties-select:visible"); !strings.Contains(got, "Penalties") || !strings.Contains(got, "Included") || !strings.Contains(got, "Excluded") {
		t.Fatalf("Penalties control = %q", got)
	}
	if err := expect.Locator(page.Locator(penaltiesSelect)).ToHaveAttribute("title", "Excluded removes penalty kicks from both goals and xG."); err != nil {
		t.Errorf("Penalties help: %v", err)
	}
	// The control is a data control: last in the form row, never in navigation.
	var last string
	evalJSON(t, page, `() => { const form = document.querySelector('[data-team-controls]'); return [...form.children].filter((el) => !el.hidden && el.tagName === 'LABEL').pop().className; }`, nil, &last)
	if last != "penalties-select" {
		t.Errorf("last visible label in the row has class %q, want penalties-select", last)
	}
	if err := expect.Locator(page.Locator("nav select, nav .penalties-select, header .penalties-select")).ToHaveCount(0); err != nil {
		t.Errorf("Penalties control found in navigation: %v", err)
	}
	defaultHeader := textOf(t, page, "table.explore-team-table thead")
	if strings.Contains(defaultHeader, "npxG") || strings.Contains(defaultHeader, "Non-penalty") {
		t.Fatalf("default table header already uses the non-penalty basis: %q", defaultHeader)
	}
	defaultXG := teamTableColumn(t, page, columnForXG)
	defaultGoals := teamTableColumn(t, page, columnForActual)
	pointsByTeam := func() map[string]string {
		names, points := teamTableColumn(t, page, 0), teamTableColumn(t, page, 11)
		byTeam := make(map[string]string, len(names))
		for i, name := range names {
			byTeam[name] = points[i]
		}
		return byTeam
	}
	defaultPoints := pointsByTeam()

	choosePenalties(t, page, "exclude")
	if got := queryOf(t, page); got.Get("penalties") != "exclude" || got.Get("season") != "2025" || got.Get("display") != "table" {
		t.Fatalf("URL after excluding penalties = %v", got)
	}
	header := textOf(t, page, "table.explore-team-table thead")
	for _, want := range []string{"npxG", "Non-penalty goals scored", "Non-penalty goal differential"} {
		if !strings.Contains(header, want) {
			t.Errorf("non-penalty table header lacks %q: %q", want, header)
		}
	}
	if got := teamTableColumn(t, page, columnForXG); slices.Equal(got, defaultXG) {
		t.Errorf("npxG column equals the xG column: %v", got)
	}
	if got := teamTableColumn(t, page, columnForActual); slices.Equal(got, defaultGoals) {
		t.Errorf("non-penalty goals column equals the goals column: %v", got)
	}
	if got := pointsByTeam(); !maps.Equal(got, defaultPoints) {
		t.Errorf("points changed with the basis: %v vs %v", got, defaultPoints)
	}
	if err := expect.Locator(page.Locator("[data-points-note=teams]")).ToHaveText("Points include penalties; ASA publishes no non-penalty expected points."); err != nil {
		t.Errorf("points note on the table: %v", err)
	}

	// Every analysis keeps the basis, offers the control, and shows it in its labels.
	for _, tab := range exploreTabs {
		if err := page.Locator("a[data-group-choice]").Filter(playwright.LocatorFilterOptions{HasText: tab.group}).Click(); err != nil {
			t.Fatalf("open group %s: %v", tab.group, err)
		}
		if err := page.Locator("nav.explore-subviews:not([hidden]) a").Filter(playwright.LocatorFilterOptions{HasText: regexp.MustCompile("^" + regexp.QuoteMeta(tab.tab) + "$")}).Click(); err != nil {
			t.Fatalf("open tab %s: %v", tab.tab, err)
		}
		if got := queryOf(t, page).Get("penalties"); got != "exclude" {
			t.Errorf("%s / %s dropped penalties: %s", tab.group, tab.tab, page.URL())
		}
		if err := expect.Locator(page.Locator(penaltiesSelect)).ToHaveValue("exclude"); err != nil {
			t.Errorf("%s / %s does not offer Penalties = Excluded: %v", tab.group, tab.tab, err)
		}
		for _, href := range hrefs(t, page, "nav.explore-views a, nav.explore-subviews a") {
			if parsed, err := url.Parse(href); err != nil || parsed.Query().Get("penalties") != "exclude" {
				t.Errorf("%s / %s: link %q lost penalties=exclude", tab.group, tab.tab, href)
			}
		}
	}

	// The league views name the basis too.
	gotoInPage(t, page, "explore?view=trend&penalties=exclude")
	if got := textOf(t, page, "#explore-trend-title"); got != "Non-penalty goals and chances per match (regular-season)" {
		t.Errorf("trend title = %q", got)
	}
	gotoInPage(t, page, "explore?view=distribution&penalties=exclude")
	if got := textOf(t, page, "#explore-distribution-title"); !strings.Contains(got, "non-penalty goals") {
		t.Errorf("distribution title = %q", got)
	}
	gotoInPage(t, page, "explore?view=team-history&penalties=exclude&measure=for")
	// Option text is basis-independent: the Penalties select states the basis.
	if got := textOf(t, page, "[data-history-series]"); got != "Goals and xG\nGoals\nxG" {
		t.Errorf("history series choices = %q", got)
	}
	if got := textOf(t, page, "[data-history-measure]"); !strings.Contains(got, "Goal differential") || strings.Contains(got, "on-penalty") {
		t.Errorf("history measure choices = %q", got)
	}

	// Included returns to the default basis and drops the parameter.
	choosePenalties(t, page, "include")
	if got := queryOf(t, page); got.Has("penalties") || got.Get("view") != "team-history" || got.Get("measure") != "for" {
		t.Errorf("URL after including penalties = %v", got)
	}
	if err := expect.Locator(page.Locator("[data-penalties=exclude]")).ToHaveCount(0); err != nil {
		t.Errorf("page still on the non-penalty basis: %v", err)
	}
}

func hrefs(t *testing.T, page playwright.Page, selector string) []string {
	t.Helper()
	var links []string
	evalJSON(t, page, `(selector) => [...document.querySelectorAll(selector)].map((a) => a.getAttribute('href'))`, selector, &links)
	return links
}

func TestHistoryPenaltiesToggleSwitchesBasis(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "history/scoring?season=2025&metric=compare")
	before := textOf(t, page, ".history-primary-metrics")
	if strings.Contains(before, "Non-penalty") {
		t.Fatalf("default history page already uses the non-penalty basis: %q", before)
	}
	choosePenalties(t, page, "exclude")
	if got := queryOf(t, page); got.Get("penalties") != "exclude" || got.Get("season") != "2025" || got.Get("metric") != "compare" {
		t.Fatalf("URL after excluding penalties = %v", got)
	}
	if got := textOf(t, page, "form.history-selector"); !strings.Contains(got, "Season") || !strings.Contains(got, "Penalties") {
		t.Errorf("history selector row = %q", got)
	}
	after := textOf(t, page, ".history-primary-metrics")
	if !strings.Contains(after, "Non-penalty goals per match") || !strings.Contains(after, "npxG per match") || after == before {
		t.Errorf("primary metrics did not switch: %q -> %q", before, after)
	}
	if got := textOf(t, page, ".history-metric-choice"); !strings.Contains(got, "Non-penalty goals") || !strings.Contains(got, "Non-penalty expected goals (npxG)") {
		t.Errorf("metric choices = %q", got)
	}
	if got := textOf(t, page, ".history-distribution"); !strings.Contains(got, "non-penalty goals") {
		t.Errorf("distribution section = %q", got)
	}
	// Selecting another season keeps the basis.
	chooseOption(t, page, "#history-season", "2026")
	if err := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Show season"}).Click(); err != nil {
		t.Fatalf("show season: %v", err)
	}
	if got := queryOf(t, page); got.Get("penalties") != "exclude" || got.Get("season") != "2026" {
		t.Errorf("season form dropped the basis: %v", got)
	}
	for _, href := range hrefs(t, page, "a.history-metric-link, a.history-chart-point-link") {
		if !strings.Contains(href, "penalties=exclude") {
			t.Errorf("link %q lost the basis", href)
		}
	}
}

// fixtureNPxG matches the non-penalty line under a fixture result's xG line.
var fixtureNPxG = regexp.MustCompile(`^npxG \d+\.\d\d–\d+\.\d\d$`)

func TestFixtureResultsShowNPxGOnlyForPenaltyMatches(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, vp := range []viewport{Desktop, Mobile} {
		t.Run(vp.Name, func(t *testing.T) {
			page := explorePage(t, base, vp, "seasons/2025/regular-season/fixtures")
			var xgLines int
			evalJSON(t, page, `() => document.querySelectorAll('.fixture-xg').length`, nil, &xgLines)
			if xgLines == 0 {
				t.Fatal("no fixture shows xG")
			}
			// Each npxG line sits directly after its match's xG line, on a line of
			// its own below it, with the values left-aligned in one column. The
			// line wrappers use display: contents, so measure the value spans.
			var npLines []struct {
				Text     string  `json:"text"`
				AfterXG  bool    `json:"afterXG"`
				Top      float64 `json:"top"`
				Left     float64 `json:"left"`
				Width    float64 `json:"width"`
				XGBottom float64 `json:"xgBottom"`
				XGLeft   float64 `json:"xgLeft"`
				SameLen  bool    `json:"sameLen"`
			}
			evalJSON(t, page, `() => [...document.querySelectorAll('.fixture-npxg')].map((el) => {
				const xg = el.previousElementSibling;
				const afterXG = !!xg && xg.classList.contains('fixture-xg');
				const value = el.lastElementChild.getBoundingClientRect();
				const xgValue = afterXG ? xg.lastElementChild.getBoundingClientRect() : { bottom: 0, left: 0 };
				const sameLen = afterXG && xg.lastElementChild.textContent.trim().length === el.lastElementChild.textContent.trim().length;
				return { text: el.textContent.trim(), afterXG, top: value.top, left: value.left, width: value.width, xgBottom: xgValue.bottom, xgLeft: xgValue.left, sameLen };
			})`, nil, &npLines)
			if len(npLines) == 0 || len(npLines) == xgLines {
				t.Errorf("%d of %d matches show npxG, want only the matches with a penalty", len(npLines), xgLines)
			}
			for _, line := range npLines {
				if !fixtureNPxG.MatchString(line.Text) {
					t.Errorf("npxG line %q, want %q", line.Text, fixtureNPxG)
				}
				if !line.AfterXG {
					t.Errorf("npxG line %q does not follow an xG line", line.Text)
				} else if line.Top < line.XGBottom-1 {
					t.Errorf("npxG line %q shares a line with its xG (top %.1f, xG bottom %.1f)", line.Text, line.Top, line.XGBottom)
				}
				if line.Width == 0 {
					t.Errorf("npxG value for %q has no rendered box", line.Text)
				}
				if line.SameLen && math.Abs(line.Left-line.XGLeft) > 1 {
					t.Errorf("npxG value for %q starts at x=%.1f, xG value at x=%.1f; want them aligned", line.Text, line.Left, line.XGLeft)
				}
			}
			var titles []string
			evalJSON(t, page, `() => [...document.querySelectorAll('.fixture-npxg')].map((el) => el.title)`, nil, &titles)
			for _, title := range titles {
				if title != "Non-penalty expected goals" {
					t.Errorf("npxG title = %q", title)
				}
			}
			assertNoHorizontalOverflow(t, page)
		})
	}
}

func TestExplorePenaltiesControlFitsAPhone(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Mobile, "explore?view=teams&display=table&penalties=exclude")
	assertNoHorizontalOverflow(t, page)
	for _, tab := range exploreTabs {
		gotoInPage(t, page, "explore?"+tab.query+"&penalties=exclude")
		assertNoHorizontalOverflow(t, page)
	}
	history := explorePage(t, base, Mobile, "history/scoring?penalties=exclude")
	assertNoHorizontalOverflow(t, history)
}

// controlBox is the page-relative bounding box of an element.
type controlBox struct {
	X, Y, W, H float64
}

// controlGeometry is one visible data-control select and its label, plus the
// bottom of the view's heading.
type controlGeometry struct {
	Key    string
	Select controlBox
	Label  controlBox
}

type controlLayout struct {
	// AnchorY is the page offset the controls are compared against: zero for
	// Explore, whose content above the controls does not depend on the basis,
	// and the selector form's top on the history page, which follows a chart
	// whose captions do.
	AnchorY       float64
	HeadingBottom float64
	Controls      []controlGeometry
}

// controlLayoutScript measures every visible select in the page's current
// view, keyed by its position and name so the two bases compare like for like.
const controlLayoutScript = `([headingSelector, anchorSelector]) => {
	const box = (el) => { const r = el.getBoundingClientRect(); return {X: r.left + scrollX, Y: r.top + scrollY, W: r.width, H: r.height}; };
	const visible = (el) => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
	const heading = document.querySelector(headingSelector);
	const controls = [...document.querySelectorAll('main select, .explore select, form select')]
		.filter((el, index, all) => all.indexOf(el) === index && visible(el))
		.map((el, index) => ({Key: index + ':' + (el.name || el.dataset.penaltiesSelector !== undefined && 'penalties' || el.id), Select: box(el), Label: box(el.closest('label') || el)}));
	const anchor = anchorSelector ? document.querySelector(anchorSelector) : null;
	return {AnchorY: anchor ? box(anchor).Y : 0, HeadingBottom: heading ? box(heading).Y + box(heading).H : -1, Controls: controls};
}`

func closeEnough(a, b float64) bool { return a-b <= 1 && b-a <= 1 }

func sameBox(a, b controlBox) bool {
	return closeEnough(a.X, b.X) && closeEnough(a.Y, b.Y) && closeEnough(a.W, b.W) && closeEnough(a.H, b.H)
}

// TestDataControlsKeepTheirGeometryAcrossBases checks that, in every Explore
// view and display and on the history page, the data controls sit below the
// view heading and occupy exactly the same boxes whether penalties are
// included or excluded, at three window sizes. Option text never changes with
// the basis, so no select may resize.
func TestDataControlsKeepTheirGeometryAcrossBases(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	type target struct{ name, query, heading, anchor string }
	targets := make([]target, 0, len(exploreTabs)+1)
	for _, tab := range exploreTabs {
		targets = append(targets, target{tab.tab, "explore?" + tab.query, ".explore [data-panel]:not([hidden]) h2", ""})
	}
	targets = append(targets, target{"History", "history/scoring", "#history-chart-heading", ".history-selector"})
	viewports := []viewport{
		{Name: "1280x900", Width: 1280, Height: 900},
		{Name: "1024x768", Width: 1024, Height: 768},
		{Name: "390x844", Width: 390, Height: 844},
	}
	for _, vp := range viewports {
		t.Run(vp.Name, func(t *testing.T) {
			t.Parallel()
			page := newPage(t, vp)
			// gaps records, per Compare teams display, the distance from the heading
			// to the first control so a display switch cannot move the row.
			gaps := map[string]float64{}
			for _, tg := range targets {
				measure := func(suffix string) controlLayout {
					visit(t, page, base+tg.query+suffix)
					if err := settle(page); err != nil {
						t.Fatalf("settle %s: %v", tg.name, err)
					}
					var layout controlLayout
					evalJSON(t, page, controlLayoutScript, []string{tg.heading, tg.anchor}, &layout)
					return layout
				}
				included := measure("")
				excluded := measure(map[bool]string{true: "&", false: "?"}[strings.Contains(tg.query, "?")] + "penalties=exclude")
				if len(included.Controls) == 0 {
					t.Fatalf("%s: no visible data controls", tg.name)
				}
				if len(included.Controls) != len(excluded.Controls) {
					t.Fatalf("%s: %d controls included, %d excluded", tg.name, len(included.Controls), len(excluded.Controls))
				}
				if included.HeadingBottom < 0 {
					t.Fatalf("%s: heading %q not found", tg.name, tg.heading)
				}
				for i, a := range included.Controls {
					b := excluded.Controls[i]
					if a.Key != b.Key {
						t.Errorf("%s: control %d is %q included but %q excluded", tg.name, i, a.Key, b.Key)
					}
					a.Select.Y, a.Label.Y = a.Select.Y-included.AnchorY, a.Label.Y-included.AnchorY
					b.Select.Y, b.Label.Y = b.Select.Y-excluded.AnchorY, b.Label.Y-excluded.AnchorY
					if !sameBox(a.Select, b.Select) || !sameBox(a.Label, b.Label) {
						t.Errorf("%s %s: control %s moved or resized with the basis: select %+v -> %+v, label %+v -> %+v", vp.Name, tg.name, a.Key, a.Select, b.Select, a.Label, b.Label)
					}
					if a.Label.Y+included.AnchorY < included.HeadingBottom-1 {
						t.Errorf("%s %s: control %s (top %.0f) is not below the heading (bottom %.0f)", vp.Name, tg.name, a.Key, a.Label.Y+included.AnchorY, included.HeadingBottom)
					}
				}
				if strings.Contains(tg.query, "view=teams") {
					gaps[tg.name] = included.Controls[0].Label.Y - included.HeadingBottom
				}
			}
			var first float64
			seen := false
			for name, gap := range gaps {
				if !seen {
					first, seen = gap, true
				}
				if !closeEnough(gap, first) {
					t.Errorf("%s: Compare teams %s controls sit %.1fpx below the heading, other displays %.1fpx", vp.Name, name, gap, first)
				}
			}
		})
	}
}
