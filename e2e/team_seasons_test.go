//go:build e2e

package e2e

import (
	"fmt"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestExploreTeamSeasonsModesAndTable(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	page := explorePage(t, exploreBase(t, apptest.ScenarioTeamHistory), Mobile, "explore?view=team-seasons")
	defer expectInPlace(t, page, "Team-seasons modes, penalties and sorting")()
	if err := expect.Locator(page.Locator("[data-team-seasons-values]")).Not().ToHaveAttribute("open", ""); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-summary]")).ToHaveText("Table: all 144 team-seasons"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-bars] a, [data-team-seasons-rows] a")).ToHaveCount(0); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-penalties]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	var chartHeight float64
	evalJSON(t, page, `() => document.querySelector('[data-team-seasons-chart]').getBoundingClientRect().height`, nil, &chartHeight)
	if chartHeight > 1500 {
		t.Fatalf("390px chart is too tall: %.0fpx", chartHeight)
	}
	headings := map[string][3]string{
		"points":     {"Points", "Expected points", "Points compared with expected,"},
		"for":        {"Goals scored", "Expected goals scored", "Goals scored compared with expected,"},
		"against":    {"Goals allowed", "Expected goals allowed", "Goals allowed compared with expected,"},
		"difference": {"Goal differential", "Expected goal differential", "Goal differential compared with expected,"},
	}
	for _, measure := range []string{"points", "for", "against", "difference"} {
		for i, by := range []string{"actual", "expected", "gap"} {
			chooseOption(t, page, "[data-team-seasons-measure]", measure)
			chooseOption(t, page, "[data-team-seasons-rank-by]", by)
			heading := headings[measure][i] + " per match, since 2016"
			if err := expect.Locator(page.Locator("[data-team-seasons-title]")).ToHaveText(heading); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-team-seasons-chart]")).ToHaveAttribute("aria-label", heading+", ranked "+by+" values with comparisons"); err != nil {
				t.Fatal(err)
			}
			var matches bool
			evalJSON(t, page, `() => {
   const p = new URLSearchParams(location.search), m = ['for','against','difference','points'].indexOf(p.get('team-seasons-measure') || 'points'), by = ['actual','expected','gap'].indexOf(p.get('team-seasons-rank-by') || 'actual');
   const records = JSON.parse(document.querySelector('#explore-team-seasons-data').textContent).all;
   return [...document.querySelectorAll('[data-team-seasons-row]')].every(bar => {
    const r = records.find(r => r.id === bar.dataset.teamId && r.season === bar.dataset.season);
    const row = [...document.querySelectorAll('[data-team-seasons-rows] tr')].find(tr => tr.dataset.teamId === r.id && tr.dataset.season === r.season);
    const logo = bar.querySelector('img');
    return row.children[0].textContent === String(r.values[m].ranks[by]) && Number(row.children[4+by].textContent) === Number(bar.querySelector('strong').textContent) && logo.getAttribute('alt') === '' && logo.getAttribute('src') === r.logo && row.querySelector('img').getAttribute('src') === r.logo;
   });
  }`, nil, &matches)
			if !matches {
				t.Fatalf("chart/table/logo disagree %s %s", measure, by)
			}
			assertNoHorizontalOverflow(t, page)
		}
	}
	// History restores both the measure and ranking mode, from the same payload.
	chooseOption(t, page, "[data-team-seasons-measure]", "for")
	chooseOption(t, page, "[data-team-seasons-rank-by]", "expected")
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-rank-by]")).ToHaveValue("gap"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-measure]")).ToHaveValue("difference"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoForward(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-measure]")).ToHaveValue("for"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoForward(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-rank-by]")).ToHaveValue("expected"); err != nil {
		t.Fatal(err)
	}
	chooseOption(t, page, "[data-team-seasons-penalties]", "exclude")
	if err := expect.Locator(page.Locator("[data-team-seasons-title]")).ToHaveText("Expected non-penalty goals scored per match, since 2016"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-points-note]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	chooseOption(t, page, "[data-team-seasons-measure]", "points")
	if queryOf(t, page).Has("penalties") {
		t.Fatal("stale basis")
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-missing]")).ToContainText("team-seasons have xPts incomplete"); err != nil {
		t.Fatal(err)
	}
	button := page.Locator("[data-team-seasons-break] button")
	if err := button.Focus(); err != nil {
		t.Fatal(err)
	}
	if err := button.Press("Enter"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-values]")).ToHaveAttribute("open", ""); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-table]")).ToBeFocused(); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"rank", "team", "season", "played", "actual", "expected", "gap"} {
		if err := page.Locator(fmt.Sprintf(`[data-team-seasons-sort="%s"]`, column)).Click(); err != nil {
			t.Fatal(err)
		}
		if queryOf(t, page).Get("team-seasons-sort") != column {
			t.Fatal("sort URL")
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-values]")).ToHaveAttribute("open", ""); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-rows] tr").Last()).ToContainText("xPts incomplete"); err != nil {
			t.Fatal(err)
		}
	}
	assertNoHorizontalOverflow(t, page)
}

func TestExploreTeamSeasonsNoScript(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	page := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: true})
	visitStatic(t, page, base+"explore?view=team-seasons&penalties=exclude")
	if err := expect.Locator(page.Locator("[data-team-seasons-values]")).Not().ToHaveAttribute("open", ""); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-break] button, [data-team-seasons-bars] a, [data-team-seasons-rows] a")).ToHaveCount(0); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-row] img")).ToHaveCount(20); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-rows] img")).ToHaveCount(144); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-points-note]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-penalties]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	chooseOption(t, page, "[data-team-seasons-measure]", "for")
	chooseOption(t, page, "[data-team-seasons-rank-by]", "expected")
	if err := page.Locator("[data-team-seasons-submit]").Click(); err != nil {
		t.Fatal(err)
	}
	if queryOf(t, page).Has("penalties") || queryOf(t, page).Has("team-seasons-sort") {
		t.Fatal("form carries stale state")
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-penalties]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	chooseOption(t, page, "[data-team-seasons-penalties]", "exclude")
	if err := page.Locator("[data-team-seasons-submit]").Click(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-points-note]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-title]")).ToHaveText("Expected non-penalty goals scored per match, since 2016"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("[data-team-seasons-summary]").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`[data-team-seasons-sort="team"]`).Click(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-values]")).ToHaveAttribute("open", ""); err != nil {
		t.Fatal(err)
	}
	chooseOption(t, page, "[data-team-seasons-measure]", "points")
	if err := page.Locator("[data-team-seasons-submit]").Click(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-penalties]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-points-note]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	assertNoHorizontalOverflow(t, page)
}

func TestExploreTeamSeasonsSmallPopulationAndActiveGate(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	small := exploreBaseArchive(t, apptest.TeamHistorySmall(t))
	for _, noScript := range []bool{false, true} {
		page := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: noScript})
		if noScript {
			visitStatic(t, page, small+"explore?view=team-seasons")
		} else {
			visit(t, page, small+"explore?view=team-seasons")
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-row]")).ToHaveCount(16); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-high]")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-low]")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-break]")).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		assertNoHorizontalOverflow(t, page)
	}
	page := explorePage(t, exploreBaseArchive(t, apptest.TeamHistoryBelowGate(t)), Mobile, "explore?view=team-seasons")
	if err := expect.Locator(page.Locator("[data-team-seasons-rows] tr")).ToHaveCount(128); err != nil {
		t.Fatal(err)
	}
	chooseOption(t, page, "[data-team-seasons-measure]", "against")
	chooseOption(t, page, "[data-team-seasons-rank-by]", "gap")
	if err := expect.Locator(page.Locator(`[data-team-seasons-rows] tr[data-season="2026"]`)).ToHaveCount(0); err != nil {
		t.Fatal(err)
	}
}
