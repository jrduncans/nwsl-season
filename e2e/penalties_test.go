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

// penaltiesSelect is the visible Exclude penalties checkbox of the current view.
const penaltiesSelect = "input[data-penalties-selector]:visible"

// setPenalties checks or clears the Exclude penalties checkbox at selector,
// firing the same change event a person's click does.
func setPenalties(t *testing.T, page playwright.Page, selector string, exclude bool) {
	t.Helper()
	if err := page.Locator(selector).SetChecked(exclude); err != nil {
		t.Fatalf("set %s to %v: %v", selector, exclude, err)
	}
}

// choosePenalties changes the Exclude penalties checkbox and waits for the
// page to show the chosen basis, in place or after a server render.
func choosePenalties(t *testing.T, page playwright.Page, choice string) {
	t.Helper()
	setPenalties(t, page, penaltiesSelect, choice == "exclude")
	if _, err := page.WaitForFunction(`(want) => (new URLSearchParams(location.search).get('penalties') === 'exclude') === (want === 'exclude')`, choice); err != nil {
		t.Fatalf("penalties %s did not navigate: %v", choice, err)
	}
	if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateLoad}); err != nil {
		t.Fatalf("load after penalties %s: %v", choice, err)
	}
	if err := playwright.NewPlaywrightAssertions().Locator(page.Locator(penaltiesSelect)).ToBeChecked(playwright.LocatorAssertionsToBeCheckedOptions{Checked: playwright.Bool(choice == "exclude")}); err != nil {
		t.Fatalf("penalties %s is not shown after the reload: %v", choice, err)
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

	if got := textOf(t, page, "label.penalties-select:visible"); got != "Exclude penalties" {
		t.Fatalf("Penalties control = %q", got)
	}
	if err := expect.Locator(page.Locator(penaltiesSelect)).ToHaveAttribute("title", "Excluding removes penalty kicks from both goals and xG."); err != nil {
		t.Errorf("Penalties help: %v", err)
	}
	// The control is a data control: last in the form row, never in navigation.
	var last string
	evalJSON(t, page, `() => { const form = document.querySelector('[data-team-controls]'); return [...form.children].filter((el) => !el.hidden && el.tagName === 'LABEL').pop().className; }`, nil, &last)
	if last != "penalties-select switch" {
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
	if err := expect.Locator(page.Locator("[data-points-note=teams]")).ToHaveText("Points include penalties; ASA publishes no non-penalty xPoints."); err != nil {
		t.Errorf("points note on the table: %v", err)
	}

	// Every analysis keeps the carried basis. Team-seasons on Points explains
	// that it ignores the basis and hides its control.
	carriedExclusion := true
	for _, tab := range exploreTabs {
		if err := page.Locator("a[data-group-choice]").Filter(playwright.LocatorFilterOptions{HasText: tab.group}).Click(); err != nil {
			t.Fatalf("open group %s: %v", tab.group, err)
		}
		if err := page.Locator("nav.explore-subviews:not([hidden]) a").Filter(playwright.LocatorFilterOptions{HasText: regexp.MustCompile("^" + regexp.QuoteMeta(tab.tab) + "$")}).Click(); err != nil {
			t.Fatalf("open tab %s: %v", tab.tab, err)
		}
		pointsOnlyTab := tab.tab == "Parity" || tab.tab == "Team-seasons"
		if pointsOnlyTab {
			carriedExclusion = false
		}
		if got := queryOf(t, page).Get("penalties"); (got == "exclude") != carriedExclusion {
			t.Errorf("%s / %s penalties = %q, carried exclusion = %v: %s", tab.group, tab.tab, got, carriedExclusion, page.URL())
		}
		if tab.tab == "Team-seasons" {
			if err := expect.Locator(page.Locator("[data-team-seasons-penalties]")).ToBeHidden(); err != nil {
				t.Error(err)
			}
			if err := expect.Locator(page.Locator("[data-team-seasons-points-note]")).ToBeHidden(); err != nil {
				t.Error(err)
			}
		} else if tab.tab == "Parity" {
			if err := expect.Locator(page.Locator("[data-parity-controls] .penalties-select")).ToBeHidden(); err != nil {
				t.Error(err)
			}
		} else if carriedExclusion && tab.tab != "Shield and playoff thresholds" {
			if err := expect.Locator(page.Locator(penaltiesSelect)).ToBeChecked(); err != nil {
				t.Errorf("%s / %s does not show Exclude penalties checked: %v", tab.group, tab.tab, err)
			}
		}
		for _, href := range hrefs(t, page, "nav.explore-views a, nav.explore-subviews a") {
			if parsed, err := url.Parse(href); err != nil || (parsed.Query().Get("penalties") == "exclude") != carriedExclusion {
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
	if got := textOf(t, page, "[data-panel=team-history] [data-segmented=series] .segmented-options"); got != "Goals and xG\nGoals\nxG" {
		t.Errorf("history series choices = %q", got)
	}
	if got := textOf(t, page, "[data-panel=team-history] [data-segmented=measure] .segmented-options"); !strings.Contains(got, "Goal differential") || strings.Contains(got, "on-penalty") {
		t.Errorf("history measure choices = %q", got)
	}

	// Included returns to the default basis and drops the parameter.
	choosePenalties(t, page, "include")
	if got := queryOf(t, page); got.Has("penalties") || got.Get("view") != "team-history" || got.Get("measure") != "for" {
		t.Errorf("URL after including penalties = %v", got)
	}
	if err := expect.Locator(page.Locator(`[data-explore][data-basis="non-penalty"]`)).ToHaveCount(0); err != nil {
		t.Errorf("page still on the non-penalty basis: %v", err)
	}
}

func hrefs(t *testing.T, page playwright.Page, selector string) []string {
	t.Helper()
	var links []string
	evalJSON(t, page, `(selector) => [...document.querySelectorAll(selector)].map((a) => a.getAttribute('href'))`, selector, &links)
	return links
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
	// AnchorY is the page offset the controls are compared against: the view
	// heading's bottom, because heading text changes with the basis and can
	// wrap onto another line on a narrow window.
	AnchorY       float64
	HeadingBottom float64
	Controls      []controlGeometry
}

// controlLayoutScript measures every visible data control in the page's
// current view: the selects and the named checkboxes that submit a selection.
// Chart display toggles, such as team logos, sit below the chart and are not
// data controls. Controls are keyed by position and name so the two bases
// compare like for like.
const controlLayoutScript = `(headingSelector) => {
	const box = (el) => { const r = el.getBoundingClientRect(); return {X: r.left + scrollX, Y: r.top + scrollY, W: r.width, H: r.height}; };
	const visible = (el) => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
	const heading = document.querySelector(headingSelector);
	const headingBottom = heading ? box(heading).Y + box(heading).H : -1;
	const controls = [...document.querySelectorAll('main select, .explore select, form select, .explore input[type="checkbox"][name]')]
		.filter((el, index, all) => all.indexOf(el) === index && visible(el))
		.map((el, index) => {
			// The status line above Match by match's chart card names the basis and
			// can wrap on phones, so the card's controls are measured from the card.
			const card = el.closest('[data-trend-chart-card]');
			const shift = card ? box(card).Y - headingBottom : 0;
			const select = box(el), label = box(el.closest('label') || el);
			select.Y -= shift; label.Y -= shift;
			return {Key: index + ':' + (el.name || el.dataset.penaltiesSelector !== undefined && 'penalties' || el.id), Select: select, Label: label};
		});
	return {AnchorY: headingBottom, HeadingBottom: headingBottom, Controls: controls};
}`

func closeEnough(a, b float64) bool { return a-b <= 1 && b-a <= 1 }

func sameBox(a, b controlBox) bool {
	return closeEnough(a.X, b.X) && closeEnough(a.Y, b.Y) && closeEnough(a.W, b.W) && closeEnough(a.H, b.H)
}

// TestDataControlsKeepTheirGeometryAcrossBases checks that, in every Explore
// view and display, the data controls sit below the
// view heading and occupy exactly the same boxes, relative to that heading,
// whether penalties are included or excluded, at three window sizes. Option text never changes with
// the basis, so no select may resize.
func TestDataControlsKeepTheirGeometryAcrossBases(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	type target struct{ name, query, heading string }
	targets := make([]target, 0, len(exploreTabs))
	for _, tab := range exploreTabs {
		targets = append(targets, target{tab.tab, "explore?" + tab.query, ".explore [data-panel]:not([hidden]) h2"})
	}
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
					evalJSON(t, page, controlLayoutScript, tg.heading, &layout)
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

// A basis switched in Head to head's own checkbox carries to the next view,
// which shows it in place as a server render would.
func TestExplorePenaltiesSwitchInHeadToHeadCarriesToOtherViews(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	for _, exclude := range []bool{true, false} {
		path := "explore?view=head-to-head&opponent=" + seattleH2H
		if !exclude {
			path += "&penalties=exclude"
		}
		page := explorePage(t, base, Desktop, path)
		markDocument(t, page)
		setPenalties(t, page, "[data-h2h-penalties]", exclude)
		assertSameDocument(t, page, "switching basis in Head to head")
		if err := page.Locator(`[data-view-choice="team-rankings"]`).Click(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator(`[data-panel="team-rankings"]`)).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		assertSameDocument(t, page, "entering Rankings on the switched basis")
		if got := queryOf(t, page).Get("penalties"); (got == "exclude") != exclude {
			t.Fatalf("penalties = %q, want exclude %v", got, exclude)
		}
		if err := expect.Locator(page.Locator(penaltiesSelect)).ToBeChecked(playwright.LocatorAssertionsToBeCheckedOptions{Checked: playwright.Bool(exclude)}); err != nil {
			t.Fatal(err)
		}
		var got, want []string
		evalJSON(t, page, basisSnapshot, nil, &got)
		evalJSON(t, explorePage(t, base, Desktop, strings.TrimPrefix(page.URL(), base)), basisSnapshot, nil, &want)
		if diff := lineDifferences(got, want); len(diff) > 0 {
			t.Errorf("Rankings differs from its server render:\n%s", strings.Join(diff, "\n"))
		}
	}
}

// basisSnapshot is what a reader sees of the open view, with its disclosures
// open: text, accessible labels, visible links (with sorted queries) and chart
// data, one item per line.
const basisSnapshot = `() => {
	const panel = document.querySelector('[data-panel]:not([hidden])');
	const lines = ['title ' + document.title, ...panel.innerText.split('\n').map((line) => 'text ' + line)];
	panel.querySelectorAll('[aria-label]').forEach((el) => { if (el.closest('[hidden]') === null) lines.push('label ' + el.getAttribute('aria-label')); });
	document.querySelectorAll('[data-explore] a[href]').forEach((a) => { if (a.offsetParent) lines.push('link ' + a.textContent.trim() + ' ' + [...new URLSearchParams(a.getAttribute('href').split('?')[1] || '')].sort().join('&')); });
	panel.querySelectorAll('canvas').forEach((c) => { const chart = Chart.getChart(c); if (chart) lines.push('chart ' + JSON.stringify({labels: chart.data.labels, sets: chart.data.datasets.map((d) => ({label: d.label, data: d.data, hidden: !!d.hidden}))})); });
	return lines;
}`

// lineDifferences lists lines whose counts differ, as +got or -want.
func lineDifferences(got, want []string) []string {
	counts := map[string]int{}
	for _, line := range got {
		counts[line]++
	}
	for _, line := range want {
		counts[line]--
	}
	var out []string
	for line, n := range counts {
		if n > 0 {
			out = append(out, "+ "+line)
		} else if n < 0 {
			out = append(out, "- "+line)
		}
	}
	slices.Sort(out)
	return out
}

// Switching penalties in place redraws the open view from the embedded
// other-basis payloads; the result must equal a server render on the new
// basis. A season with no teams on the new basis takes that server render.
func TestExplorePenaltiesSwitchMatchesServerRender(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		scenario, path string
		inPlace        bool
	}{
		{apptest.ScenarioSeasonTrend, "explore?view=teams", true},
		{apptest.ScenarioSeasonTrend, "explore?view=teams&display=gap", true},
		{apptest.ScenarioSeasonTrend, "explore?view=teams&display=scatter", true},
		{apptest.ScenarioSeasonTrend, "explore?view=teams&display=quadrant", true},
		{apptest.ScenarioSeasonTrend, "explore?view=teams&display=quadrant&quadrant-data=both", true},
		{apptest.ScenarioSeasonTrend, "explore?view=teams&display=table", true},
		{apptest.ScenarioSeasonTrend, "explore?view=teams&display=table&units=total&team-sort=for-expected", true},
		{apptest.ScenarioSeasonTrend, "explore?view=team-rankings", true},
		{apptest.ScenarioSeasonTrend, "explore?view=season-trend", true},
		{apptest.ScenarioSeasonTrend, "explore?view=season-trend&trend-view=compare&smoothing=none", true},
		{apptest.ScenarioSeasonTrend, "explore?view=season-trend&trend-view=difference&smoothing=none", true},
		{apptest.ScenarioSeasonTrend, "explore?view=season-trend&trend-view=relative", true},
		{apptest.ScenarioSeasonTrend, "explore?view=season-trend&trend-view=balance&smoothing=none&opponents=adjusted", true},
		{apptest.ScenarioSeasonTrend, "explore?view=team-history", true},
		{apptest.ScenarioSeasonTrend, "explore?view=team-history&context=on&measure=for", true},
		{apptest.ScenarioSeasonTrend, "explore?view=trend", true},
		{apptest.ScenarioSeasonTrend, "explore?view=distribution", true},
		{apptest.ScenarioSeasonTrend, "explore?view=distribution&distribution-bin=2", true},
		{apptest.ScenarioSeasonTrend, "explore?view=home-advantage", true},
		{apptest.ScenarioSeasonTrend, "explore?view=table", true},
		{apptest.ScenarioTeams, "explore?view=teams&display=table", false},
	} {
		for _, exclude := range []bool{true, false} {
			from, to := tc.path, tc.path+"&penalties=exclude"
			if !exclude {
				from, to = to, from
			}
			t.Run(tc.scenario+"/"+from, func(t *testing.T) {
				t.Parallel()
				base := exploreBase(t, tc.scenario)
				switched := explorePage(t, base, Desktop, from)
				markDocument(t, switched)
				if err := switched.Locator(penaltiesSelect).First().Click(); err != nil {
					t.Fatal(err)
				}
				if _, err := switched.WaitForFunction(`(want) => (new URLSearchParams(location.search).get('penalties') === 'exclude') === want && document.readyState === 'complete'`, exclude); err != nil {
					t.Fatal(err)
				}
				if err := settle(switched); err != nil {
					t.Fatal(err)
				}
				var same bool
				evalJSON(t, switched, `() => window.__documentMarker === true`, nil, &same)
				if same != tc.inPlace {
					t.Errorf("switched in place = %v, want %v", same, tc.inPlace)
				}
				snapshot := func(page playwright.Page) []string {
					evalJSON(t, page, `() => { document.querySelectorAll('[data-panel]:not([hidden]) details').forEach((d) => { d.open = true; }); return true }`, nil, new(bool))
					if err := settle(page); err != nil {
						t.Fatal(err)
					}
					var lines []string
					evalJSON(t, page, basisSnapshot, nil, &lines)
					return lines
				}
				got, want := snapshot(switched), snapshot(explorePage(t, base, Desktop, to))
				if diff := lineDifferences(got, want); len(diff) > 0 {
					t.Errorf("switched page differs from the server render of %s:\n%s", to, strings.Join(diff, "\n"))
				}
			})
		}
	}
}

// Without JavaScript the checkbox submits through its view's GET form, both in
// a view with other selections and in one whose form exists only for it.
func TestExplorePenaltiesNativeSubmission(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	for _, tc := range []struct{ path, panel, kept string }{
		{"explore?view=teams&display=table&season=2025", "teams", "season"},
		{"explore?view=trend&metric=gap", "trend", "metric"},
	} {
		page := newPageWith(t, pageOptions{Viewport: Desktop, NoScript: true})
		visitStatic(t, page, base+tc.path)
		want := queryOf(t, page).Get(tc.kept)
		for _, exclude := range []bool{true, false} {
			form := page.Locator(`[data-panel="` + tc.panel + `"] form:has(input[data-penalties-selector])`)
			setPenalties(t, page, `[data-panel="`+tc.panel+`"] input[data-penalties-selector]`, exclude)
			if err := form.Locator(`button[type=submit]`).Click(); err != nil {
				t.Fatal(err)
			}
			if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateLoad}); err != nil {
				t.Fatal(err)
			}
			query := queryOf(t, page)
			if got := query["penalties"]; exclude && (len(got) != 1 || got[0] != "exclude") || !exclude && len(got) != 0 {
				t.Fatalf("%s exclude=%v submitted penalties %q", tc.panel, exclude, got)
			}
			if query.Get("view") != tc.panel || query.Get(tc.kept) != want {
				t.Fatalf("%s submission lost the selection: %s", tc.panel, query.Encode())
			}
			if err := expect.Locator(page.Locator(`[data-panel="` + tc.panel + `"] input[data-penalties-selector]`)).ToBeChecked(playwright.LocatorAssertionsToBeCheckedOptions{Checked: playwright.Bool(exclude)}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
