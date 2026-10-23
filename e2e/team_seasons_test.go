//go:build e2e

package e2e

import (
	"fmt"
	"regexp"
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
			chooseOption(t, page, "[data-segmented=team-seasons-measure]", measure)
			chooseOption(t, page, "[data-segmented=team-seasons-rank-by]", by)
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
	chooseOption(t, page, "[data-segmented=team-seasons-measure]", "for")
	chooseOption(t, page, "[data-segmented=team-seasons-rank-by]", "expected")
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-segmented=team-seasons-rank-by] input:checked")).ToHaveValue("gap"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-segmented=team-seasons-measure] input:checked")).ToHaveValue("difference"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoForward(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-segmented=team-seasons-measure] input:checked")).ToHaveValue("for"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoForward(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-segmented=team-seasons-rank-by] input:checked")).ToHaveValue("expected"); err != nil {
		t.Fatal(err)
	}
	setPenalties(t, page, "[data-team-seasons-penalties]", true)
	if err := expect.Locator(page.Locator("[data-team-seasons-title]")).ToHaveText("Expected non-penalty goals scored per match, since 2016"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-points-note]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	chooseOption(t, page, "[data-segmented=team-seasons-measure]", "points")
	if queryOf(t, page).Has("penalties") {
		t.Fatal("stale basis")
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-missing]")).ToContainText("team-seasons have xPoints incomplete"); err != nil {
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
		if err := expect.Locator(page.Locator("[data-team-seasons-rows] tr").Last()).ToContainText("xPoints incomplete"); err != nil {
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
	if err := expect.Locator(page.Locator("[data-team-seasons-points-note]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-penalties]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	chooseOption(t, page, "[data-segmented=team-seasons-measure]", "for")
	chooseOption(t, page, "[data-segmented=team-seasons-rank-by]", "expected")
	if err := page.Locator("[data-team-seasons-submit]").Click(); err != nil {
		t.Fatal(err)
	}
	if queryOf(t, page).Has("penalties") || queryOf(t, page).Has("team-seasons-sort") {
		t.Fatal("form carries stale state")
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-penalties]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	setPenalties(t, page, "[data-team-seasons-penalties]", true)
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
	chooseOption(t, page, "[data-segmented=team-seasons-measure]", "points")
	if err := page.Locator("[data-team-seasons-submit]").Click(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-penalties]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-points-note]")).ToBeHidden(); err != nil {
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
	chooseOption(t, page, "[data-segmented=team-seasons-measure]", "against")
	chooseOption(t, page, "[data-segmented=team-seasons-rank-by]", "gap")
	if err := expect.Locator(page.Locator(`[data-team-seasons-rows] tr[data-season="2026"]`)).ToHaveCount(0); err != nil {
		t.Fatal(err)
	}
}

func TestExploreTeamSeasonsScatter(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	for _, vp := range []viewport{Desktop, Mobile} {
		t.Run(fmt.Sprintf("%dpx", vp.Width), func(t *testing.T) {
			page := newPageWith(t, pageOptions{Viewport: vp, Touch: vp == Mobile})
			visit(t, page, base+"explore?view=team-seasons&team-seasons-rank-by=gap")
			expect := playwright.NewPlaywrightAssertions()
			defer expectInPlace(t, page, "scatter display, measures, identification and history")()
			clickDisplay := func(display string) {
				t.Helper()
				chooseOption(t, page, "[data-segmented=team-seasons-display]", display)
			}
			clickDisplay("scatter")
			if err := expect.Locator(page.Locator("[data-segmented=team-seasons-rank-by]")).ToBeHidden(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-team-seasons-values]")).Not().ToHaveAttribute("open", ""); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-team-seasons-identification]")).ToBeHidden(); err != nil {
				t.Fatal(err)
			}
			const selector = `[data-chart="team-seasons-scatter"]`
			canvas := page.Locator(selector)
			for _, measure := range []string{"points", "for", "against", "difference"} {
				chooseOption(t, page, "[data-segmented=team-seasons-measure]", measure)
				var correct bool
				evalJSON(t, page, `() => {
     const chart = Chart.getChart(document.querySelector('[data-chart="team-seasons-scatter"]'));
     const m = ['for','against','difference','points'].indexOf(new URLSearchParams(location.search).get('team-seasons-measure') || 'points');
     const rows = JSON.parse(document.querySelector('#explore-team-seasons-data').textContent).all.filter(row => row.values[m].eligible && row.values[m].expected !== null);
     return chart.data.datasets[0].data.length === rows.length && chart.scales.x.min === chart.scales.y.min && chart.scales.x.max === chart.scales.y.max &&
       (m !== 3 || chart.scales.x.min === 0 && chart.scales.x.max === 3) &&
       chart.options.scales.x.title.text.includes('(expected)') && chart.options.scales.y.title.text.includes('(actual)') &&
       chart.data.datasets[0].data.every((v,i) => v.x === chart.teamRows[i].values[m].expected && v.y === chart.teamRows[i].values[m].actual) &&
       chart.teamRows.some((row,i) => row.active && chart.getDatasetMeta(0).data[i].options.backgroundColor !== chart.getDatasetMeta(0).data[i].options.borderColor);
    }`, nil, &correct)
				if !correct {
					t.Fatalf("scatter disagrees with payload, axes or hollow marks on %s", measure)
				}
				if err := expect.Locator(page.Locator("[data-team-seasons-missing]")).ToContainText("cannot be plotted"); err != nil {
					t.Fatal(err)
				}
				assertNoHorizontalOverflow(t, page)
			}
			setPenalties(t, page, "[data-team-seasons-penalties]", true)
			var label string
			evalJSON(t, page, `() => Chart.getChart(document.querySelector('[data-chart="team-seasons-scatter"]')).options.scales.x.title.text`, nil, &label)
			if label != "npxGD per match (expected)" {
				t.Fatalf("basis axis = %s", label)
			}
			chooseOption(t, page, "[data-segmented=team-seasons-measure]", "points")
			// The existing archive has coincident points; identify the same group using
			// keyboard and pointer rather than inventing a special fixture.
			var group struct {
				Index, Count int
				X, Y         float64
			}
			evalJSON(t, page, `() => {
    const chart=Chart.getChart(document.querySelector('[data-chart="team-seasons-scatter"]')), marks=chart.getDatasetMeta(0).data;
    const index=marks.findIndex((p,i) => marks.some((q,j) => i!==j && Math.hypot(p.x-q.x,p.y-q.y)<=.25));
    return {Index:index,Count:marks.filter(p=>Math.hypot(p.x-marks[index].x,p.y-marks[index].y)<=.25).length,X:marks[index].x,Y:marks[index].y};
   }`, nil, &group)
			if group.Index < 0 || group.Count < 2 {
				t.Fatal("scenario has no overlap")
			}
			if err := canvas.Focus(); err != nil {
				t.Fatal(err)
			}
			if err := canvas.Press("Home"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < group.Index; i++ {
				if err := canvas.Press("ArrowRight"); err != nil {
					t.Fatal(err)
				}
			}
			if err := expect.Locator(page.Locator("[data-team-seasons-scatter-hint]")).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			if err := canvas.Press("Enter"); err != nil {
				t.Fatal(err)
			}
			selection := page.Locator("[data-team-seasons-identification]")
			if err := expect.Locator(selection).ToContainText(fmt.Sprintf("%d team-seasons at this point", group.Count)); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(selection.Locator("img")).ToHaveCount(group.Count); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(selection).ToContainText("Actual"); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(canvas).ToHaveAttribute("aria-label", regexp.MustCompile(`Actual: .*Expected: .*Gap:`)); err != nil {
				t.Fatal(err)
			}
			if queryOf(t, page).Has("team-season") {
				t.Fatal("identification saved in URL")
			}
			if err := canvas.Press("Escape"); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(selection).ToBeHidden(); err != nil {
				t.Fatal(err)
			}
			if vp == Mobile {
				if err := canvas.Tap(playwright.LocatorTapOptions{Position: &playwright.Position{X: group.X, Y: group.Y}}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := canvas.Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: group.X, Y: group.Y}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := expect.Locator(selection).ToContainText(fmt.Sprintf("%d team-seasons at this point", group.Count)); err != nil {
				t.Fatal(err)
			}
			clickDisplay("ranking")
			if queryOf(t, page).Has("team-seasons-display") {
				t.Fatal("ranking default spelled in URL")
			}
			if err := expect.Locator(page.Locator("[data-segmented=team-seasons-rank-by] input:checked")).ToHaveValue("gap"); err != nil {
				t.Fatal(err)
			}
			if _, err := page.GoBack(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(canvas).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(selection).ToBeHidden(); err != nil {
				t.Fatal(err)
			}
			if _, err := page.GoForward(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(canvas).ToBeHidden(); err != nil {
				t.Fatal(err)
			}
			clickDisplay("scatter")
			if err := page.Locator("[data-team-seasons-summary]").Click(); err != nil {
				t.Fatal(err)
			}
			if err := page.Locator(`[data-team-seasons-sort="expected"]`).Click(); err != nil {
				t.Fatal(err)
			}
			if queryOf(t, page).Get("team-seasons-display") != "scatter" {
				t.Fatal("table sort lost scatter")
			}
			assertNoHorizontalOverflow(t, page)
		})
	}
}

func TestExploreTeamSeasonsScatterNoScript(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	expect := playwright.NewPlaywrightAssertions()
	for _, vp := range []viewport{Desktop, Mobile} {
		page := newPageWith(t, pageOptions{Viewport: vp, NoScript: true})
		visitStatic(t, page, base+"explore?view=team-seasons&team-seasons-rank-by=expected")
		chooseOption(t, page, "[data-segmented=team-seasons-display]", "scatter")
		if err := page.Locator("[data-team-seasons-submit]").Click(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-segmented=team-seasons-rank-by]")).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-chart]")).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-scatter] noscript p")).ToContainText("table below"); err != nil {
			t.Fatal(err)
		}
		chooseOption(t, page, "[data-segmented=team-seasons-measure]", "against")
		if err := page.Locator("[data-team-seasons-submit]").Click(); err != nil {
			t.Fatal(err)
		}
		if queryOf(t, page).Get("team-seasons-display") != "scatter" {
			t.Fatal("GET form lost display")
		}
		if err := page.Locator("[data-team-seasons-summary]").Click(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-team-seasons-rows] tr")).ToHaveCount(144); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator(`[data-team-seasons-sort="actual"]`).Click(); err != nil {
			t.Fatal(err)
		}
		if queryOf(t, page).Get("team-seasons-display") != "scatter" {
			t.Fatal("sort lost display")
		}
		assertNoHorizontalOverflow(t, page)
	}
}

func TestExploreTeamSeasonsScatterReloadAndEmpty(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	page := explorePage(t, exploreBase(t, apptest.ScenarioTeamHistory), Mobile, "explore?view=team-seasons&team-seasons-display=scatter")
	canvas := page.Locator(`[data-chart="team-seasons-scatter"]`)
	if err := canvas.Press("Home"); err != nil {
		t.Fatal(err)
	}
	if err := canvas.Press("Enter"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-identification]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if _, err := page.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-team-seasons-identification]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(canvas).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	empty := explorePage(t, exploreBase(t, apptest.ScenarioEmpty), Mobile, "explore?view=team-seasons&team-seasons-display=scatter")
	if err := expect.Locator(empty.Locator("[data-team-seasons-scatter-empty]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(empty.Locator("[data-team-seasons-scatter-wrap]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	assertNoHorizontalOverflow(t, empty)
}
