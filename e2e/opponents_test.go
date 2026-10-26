//go:build e2e

package e2e

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/apptest"
)

const opponentsPath = "explore?view=season-trend&team=kRQa8JOqKZ"

// opponentState reads everything the opponent adjustment changes outside the
// charts: the control, note, tiles, official-results note and table.
const opponentState = `() => {
	const text = selector => document.querySelector(selector).textContent.replace(/\s+/g, ' ').trim();
	const official = document.querySelector('[data-trend-margin-official]');
	return {
		shown: !document.querySelector('[data-trend-opponents-control]').hidden,
		checked: document.querySelector('[data-trend-opponents]').checked,
		note: text('[data-season-trend-note]'),
		tiles: document.querySelector('[data-trend-margin-tiles]').hidden ? [] : [...document.querySelectorAll('[data-trend-margin-tiles] > *')].map(tile => [...tile.children].map(line => line.textContent.trim()).join(' | ')),
		pointsTiles: document.querySelector('[data-trend-points-tiles]').hidden ? [] : [...document.querySelectorAll('[data-trend-points-tiles] > *')].map(tile => [...tile.children].map(line => line.textContent.replace(/\s+/g, ' ').trim()).join(' | ')),
		official: official.hidden ? '' : text('[data-trend-margin-official]'),
		headings: [...document.querySelectorAll('[data-trend-headings] th')].map(cell => cell.textContent.trim()),
		rows: [...document.querySelectorAll('[data-trend-rows] tr')].map(row => [...row.cells].map(cell => cell.textContent.trim())),
	};
}`

type opponentView struct {
	Shown, Checked               bool
	Note, Official               string
	Tiles, PointsTiles, Headings []string
	Rows                         [][]string
}

func TestExploreOpponentAdjustmentGoBrowserParity(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, query := range []string{
		"&season=2025&smoothing=none&opponents=adjusted",
		"&season=2026&trend-view=difference&smoothing=none&opponents=adjusted",
		"&season=2025&smoothing=5&opponents=adjusted",
		"&season=2025&trend-view=difference&smoothing=none&opponents=adjusted&penalties=exclude",
		"&season=2025&smoothing=none&opponents=adjusted&trend-sort=adjusted-goals-against&trend-order=desc",
		"&season=2025&trend-view=compare&opponents=adjusted",
		"&season=2025&trend-view=points&smoothing=none&opponents=adjusted",
		"&season=2026&trend-view=points&smoothing=3&opponents=adjusted",
		"&season=2025&trend-view=points&smoothing=none&opponents=adjusted&trend-sort=adjusted-points&trend-order=desc",
	} {
		t.Run(query, func(t *testing.T) {
			noScript := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: true})
			visitStatic(t, noScript, base+opponentsPath+query)
			var want, got opponentView
			evalJSON(t, noScript, opponentState, nil, &want)
			page := explorePage(t, base, Mobile, opponentsPath+query)
			evalJSON(t, page, opponentState, nil, &got)
			if len(want.Rows) == 0 || !reflect.DeepEqual(got, want) {
				t.Fatalf("Go/JS differ\nGo: %+v\nJS: %+v", want, got)
			}
			assertNoHorizontalOverflow(t, page)
		})
	}
}

// seasonTrendBars reads the bar panels' first series, which the adjustment rescales.
func seasonTrendBars(t *testing.T, page playwright.Page) []any {
	t.Helper()
	var data []any
	evalJSON(t, page, `() => Chart.getChart(document.querySelector('[data-chart="season-margin"]')).data.datasets[0].data`, nil, &data)
	return data
}

func TestExploreOpponentAdjustmentTogglesInPlace(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, vp := range []viewport{Desktop, Mobile} {
		t.Run(vp.Name, func(t *testing.T) {
			page := explorePage(t, base, vp, opponentsPath+"&season=2025&smoothing=none")
			checkbox := page.Locator("[data-trend-opponents]")
			read := func() opponentView {
				t.Helper()
				var state opponentView
				evalJSON(t, page, opponentState, nil, &state)
				return state
			}
			official := seasonTrendBars(t, page)
			before, top := read(), chartTop(t, page)
			done := expectInPlace(t, page, "opponent adjustment")
			if err := checkbox.Check(); err != nil {
				t.Fatal(err)
			}
			if got := queryOf(t, page).Get("opponents"); got != "adjusted" {
				t.Fatalf("URL opponents = %q", got)
			}
			adjusted := read()
			if !strings.Contains(adjusted.Note, "Adjust for opponents rescales values") || reflect.DeepEqual(adjusted.Tiles, before.Tiles) || adjusted.Note != before.Note ||
				adjusted.Official != "W–D–L and points use official results." || !strings.Contains(strings.Join(adjusted.Headings, "|"), "Adjusted xG against") {
				t.Fatalf("adjusted state = %+v", adjusted)
			}
			if err := expect.Locator(page.Locator(`[data-trend-basis="opponents"]`)).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(seasonTrendBars(t, page), official) {
				t.Fatal("Scoring balance bars did not change")
			}
			if vp == Desktop {
				if got := chartTop(t, page); got != top {
					t.Fatalf("chart moved from %v to %v px when the adjustment turned on", top, got)
				}
			}
			if err := expect.Locator(page.Locator("[data-trend-margin-title]")).ToHaveText("Adjusted goals"); err != nil {
				t.Fatal(err)
			}

			// Margins keeps it; Goals vs xG hides the control but keeps the value.
			chooseOption(t, page, "[data-trend-view]", "difference")
			if err := expect.Locator(page.Locator("[data-trend-margin-title]")).ToHaveText("Adjusted goal margin"); err != nil {
				t.Fatal(err)
			}
			// An adjusted margin can change sign, so it no longer reads as the result.
			if err := expect.Locator(page.Locator("[data-trend-margin-direction]")).ToHaveText("Zero: level"); err != nil {
				t.Fatal(err)
			}
			chooseOption(t, page, "[data-trend-view]", "compare")
			if err := expect.Locator(page.Locator("[data-trend-opponents-control]")).ToBeHidden(); err != nil {
				t.Fatal(err)
			}
			if got := queryOf(t, page).Get("opponents"); got != "adjusted" {
				t.Fatalf("hidden control dropped opponents: %q", got)
			}
			if strings.Contains(strings.Join(read().Headings, "|"), "Adjusted") {
				t.Fatal("Goals vs xG applied the adjustment")
			}

			// Back and Forward restore each state.
			if _, err := page.GoBack(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-trend-margin-title]")).ToHaveText("Adjusted goal margin"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := page.GoBack(); err != nil {
					t.Fatal(err)
				}
			}
			if err := expect.Locator(checkbox).Not().ToBeChecked(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(seasonTrendBars(t, page), official) {
				t.Fatal("Back did not restore official bars")
			}
			if _, err := page.GoForward(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(checkbox).ToBeChecked(); err != nil {
				t.Fatal(err)
			}
			done()

			// A reload renders the same adjusted state from the server.
			if _, err := page.Reload(); err != nil {
				t.Fatal(err)
			}
			if reloaded := read(); !reflect.DeepEqual(reloaded, adjusted) {
				t.Fatalf("reload = %+v, want %+v", reloaded, adjusted)
			}
			if err := checkbox.Uncheck(); err != nil {
				t.Fatal(err)
			}
			if queryOf(t, page).Has("opponents") {
				t.Fatal("unchecking kept opponents in the URL")
			}
		})
	}
}

func TestExploreOpponentVisibilityMatchesSelectionMatrix(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, "explore?view=season-trend")
	control := page.Locator("[data-trend-opponents-control]")
	for _, selection := range apptest.ExploreOpponentCases() {
		// The control lives in Match by match; other views hide its panel.
		if !strings.Contains(selection.Query, "view=season-trend") {
			continue
		}
		gotoInPage(t, page, "explore?"+selection.Query)
		if selection.Shown {
			if err := expect.Locator(control).ToBeVisible(); err != nil {
				t.Fatalf("%s: expected Adjust for opponents visible: %v", selection.Query, err)
			}
		} else if err := expect.Locator(control).ToBeHidden(); err != nil {
			t.Fatalf("%s: expected Adjust for opponents hidden: %v", selection.Query, err)
		}
	}
}

// Unsmoothed Points vs xPoints draws Points, xPoints and points − xPoints
// panels with or without the adjustment: the switch moves the first two, and
// leaves the gap, which it cannot change.
func TestExploreOpponentAdjustedPointsPanels(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := explorePage(t, base, Desktop, opponentsPath+"&season=2025&trend-view=points&smoothing=none&opponents=adjusted")
	var got struct {
		MarginsHidden, GapHidden bool
		Titles                   []string
		Points, XPoints          []*float64
		Table                    [][]string
		Inspection               []string
		Axis                     []float64
	}
	evalJSON(t, page, `() => {
		const chart = name => Chart.getChart(document.querySelector('[data-chart="' + name + '"]'));
		const headings = [...document.querySelectorAll('[data-trend-headings] th')].map(cell => cell.textContent.trim());
		const column = label => headings.indexOf(label);
		const rows = [...document.querySelectorAll('[data-trend-rows] tr')].map(row => [...row.cells].map(cell => cell.textContent.trim()));
		const points = chart('season-margin');
		return {
			marginsHidden: document.querySelector('[data-trend-margins-panel]').hidden,
			gapHidden: document.querySelector('[data-trend-points-panel]').hidden,
			titles: [...document.querySelectorAll('[data-trend-margin-title], [data-trend-second-title]')].map(title => title.textContent),
			points: points.data.datasets[0].data, xPoints: chart('season-margin-xg').data.datasets[0].data,
			table: rows.map(row => [row[column('Adjusted points')], row[column('Adjusted xPoints')]]),
			inspection: points.inspection(points.meetings[0], 0).lines,
			axis: [points.options.scales.y.min, points.options.scales.y.max],
		};
	}`, nil, &got)
	if got.MarginsHidden || got.GapHidden || !reflect.DeepEqual(got.Titles, []string{"Adjusted points", "Adjusted xPoints"}) {
		t.Fatalf("panels: %+v", got)
	}
	var official struct {
		Titles      []string
		Points, Gap []*float64
		AdjustedGap []*float64
	}
	gaps := `() => Chart.getChart(document.querySelector('[data-chart="season-points-gap"]')).data.datasets[0].data`
	evalJSON(t, page, gaps, nil, &official.AdjustedGap)
	if err := page.Locator("[data-trend-opponents]").Uncheck(); err != nil {
		t.Fatal(err)
	}
	evalJSON(t, page, `() => ({titles: [...document.querySelectorAll('[data-trend-margin-title], [data-trend-second-title]')].map(title => title.textContent),
		points: Chart.getChart(document.querySelector('[data-chart="season-margin"]')).data.datasets[0].data})`, nil, &official)
	evalJSON(t, page, gaps, nil, &official.Gap)
	if !reflect.DeepEqual(official.Titles, []string{"Points", "xPoints"}) || reflect.DeepEqual(official.Points, got.Points) {
		t.Fatalf("unadjusted panels: %+v", official)
	}
	for index := range official.Gap {
		if (official.Gap[index] == nil) != (official.AdjustedGap[index] == nil) || official.Gap[index] != nil && math.Abs(*official.Gap[index]-*official.AdjustedGap[index]) > 1e-9 {
			t.Fatalf("match %d gap changed with the adjustment: %v, %v", index+1, official.Gap[index], official.AdjustedGap[index])
		}
	}
	for index, value := range official.Points {
		if value == nil || (*value != 0 && *value != 1 && *value != 3) {
			t.Fatalf("match %d official points bar = %v", index+1, value)
		}
	}
	if len(got.Points) == 0 || len(got.Points) != len(got.Table) {
		t.Fatalf("%d bars for %d rows", len(got.Points), len(got.Table))
	}
	format := func(value *float64) string {
		if value == nil {
			return "Unavailable"
		}
		if math.Abs(*value) < .005 {
			return "0.00"
		}
		return fmt.Sprintf("%.2f", *value)
	}
	for index := range got.Points {
		if bars := []string{format(got.Points[index]), format(got.XPoints[index])}; !reflect.DeepEqual(bars, got.Table[index]) {
			t.Fatalf("match %d bars %v, table %v", index+1, bars, got.Table[index])
		}
	}
	if got.Axis[0] > -1 || got.Axis[1] < 4 {
		t.Errorf("axis = %v, want at least −1 to 4", got.Axis)
	}
	joined := strings.Join(got.Inspection, " · ")
	for _, want := range []string{"Points: ", " · adjusted ", "xPoints: ", "Average team's expected points: ", "(season average "} {
		if !strings.Contains(joined, want) {
			t.Errorf("inspection %q lacks %q", joined, want)
		}
	}
	assertNoHorizontalOverflow(t, page)
}

// chartTop is the plot's offset within the chart card, which text above the
// chart would push down.
func chartTop(t *testing.T, page playwright.Page) float64 {
	t.Helper()
	var top float64
	evalJSON(t, page, `() => document.querySelector('[data-season-trend-plot]').getBoundingClientRect().top - document.querySelector('[data-trend-chart-card]').getBoundingClientRect().top`, nil, &top)
	return top
}

// With smoothing off, toggling Adjust for opponents leaves the chart where it
// was: the note and tiles keep their lines.
func TestExploreOpponentAdjustmentKeepsChartInPlace(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	for _, view := range []string{"balance", "difference", "points"} {
		for _, smoothing := range []string{"none", "5"} {
			query := "&season=2026&trend-view=" + view + "&smoothing=" + smoothing
			t.Run(view+"/"+smoothing, func(t *testing.T) {
				page := explorePage(t, base, Desktop, opponentsPath+query)
				top := chartTop(t, page)
				if err := page.Locator("[data-trend-opponents]").Check(); err != nil {
					t.Fatal(err)
				}
				if got := chartTop(t, page); got != top {
					t.Fatalf("chart moved from %v to %v px", top, got)
				}
			})
		}
	}
}
