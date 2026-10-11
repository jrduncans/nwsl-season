//go:build e2e

package e2e

import (
	"maps"
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
	if got := textOf(t, page, "[data-history-series]"); !strings.Contains(got, "Non-penalty goals and npxG") {
		t.Errorf("history series choices = %q", got)
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

// fixtureNPxG matches the appended non-penalty line of a fixture result.
var fixtureNPxG = regexp.MustCompile(`xG \d+\.\d\d–\d+\.\d\d · npxG \d+\.\d\d–\d+\.\d\d`)

func TestFixtureResultsShowNPxGOnlyForPenaltyMatches(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, vp := range []viewport{Desktop, Mobile} {
		t.Run(vp.Name, func(t *testing.T) {
			page := explorePage(t, base, vp, "seasons/2025/regular-season/fixtures")
			var lines []string
			evalJSON(t, page, `() => [...document.querySelectorAll('.fixture-xg')].map((el) => el.textContent.trim())`, nil, &lines)
			if len(lines) == 0 {
				t.Fatal("no fixture shows xG")
			}
			withNP := 0
			for _, line := range lines {
				if strings.Contains(line, "npxG") {
					withNP++
					if !fixtureNPxG.MatchString(line) {
						t.Errorf("npxG line %q does not follow the xG text", line)
					}
				}
			}
			if withNP == 0 || withNP == len(lines) {
				t.Errorf("%d of %d xG lines carry npxG, want only the matches with a penalty", withNP, len(lines))
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
