//go:build e2e

package e2e

import (
	"math"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestExploreThresholds(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioThresholds)
	expect := playwright.NewPlaywrightAssertions()
	for _, vp := range []viewport{Desktop, Mobile} {
		t.Run(vp.Name, func(t *testing.T) {
			page := newPageWith(t, pageOptions{Viewport: vp, Touch: vp.Name == "mobile"})
			visit(t, page, base+"explore?view=thresholds&threshold-season=2023")
			canvas := page.Locator(`[data-chart="thresholds"]`)
			var chart struct {
				Labels []string     `json:"labels"`
				Values [][]*float64 `json:"values"`
				Spans  []bool       `json:"spans"`
			}
			evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="thresholds"]'));return {labels:c.data.labels,values:c.data.datasets.map(d=>d.data),spans:c.data.datasets.map(d=>d.spanGaps)}}`, nil, &chart)
			if len(chart.Labels) != 10 || chart.Labels[4] != "2020" || len(chart.Values) != 4 {
				t.Fatal(chart)
			}
			for i, values := range chart.Values {
				if values[4] != nil || chart.Spans[i] {
					t.Fatal("2020 bridged")
				}
			}
			// Payloads keep six decimal places (payloadDecimals), well beyond
			// the two the chart displays.
			if math.Abs(*chart.Values[3][7]-31.0/22) > 1e-6 {
				t.Fatal("rounded chart ratio")
			}
			if err := expect.Locator(page.Locator(`[data-threshold-detail="2023"]`)).ToContainText("Equal-points tie across the boundary"); err != nil {
				t.Fatal(err)
			}
			// All coincident series stay in the index tooltip without perturbing data.
			evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="thresholds"]'));c.data.datasets[1].data[7]=c.data.datasets[3].data[7];c.update('none');return true}`, nil, new(bool))
			var point struct{ X, Y float64 }
			evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="thresholds"]'));const p=c.getDatasetMeta(3).data[7];return{x:p.x,y:p.y}}`, nil, &point)
			if err := canvas.Hover(playwright.LocatorHoverOptions{Position: &playwright.Position{X: point.X, Y: point.Y}}); err != nil {
				t.Fatal(err)
			}
			if _, err := page.WaitForFunction(`()=>Chart.getChart(document.querySelector('[data-chart="thresholds"]')).tooltip.opacity>0`, nil); err != nil {
				t.Fatal(err)
			}
			var lines []string
			var title []string
			evalJSON(t, page, `()=>Chart.getChart(document.querySelector('[data-chart="thresholds"]')).tooltip.title`, nil, &title)
			if len(title) != 1 || title[0] != "2023 · 6 playoff places · 2 byes" {
				t.Fatalf("tooltip format: %v", title)
			}
			evalJSON(t, page, `()=>Chart.getChart(document.querySelector('[data-chart="thresholds"]')).tooltip.body.flatMap(row=>row.lines)`, nil, &lines)
			if len(lines) != 4 || !strings.Contains(strings.Join(lines, " "), "Home game: Seattle Reign FC") || !strings.Contains(strings.Join(lines, " "), "Playoffs: NJ/NY Gotham") {
				t.Fatalf("coincident tooltip lost identities: %v", lines)
			}
			var fits bool
			evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="thresholds"]'));const tip=c.tooltip;return tip.x>=0 && tip.x+tip.width<=c.width}`, nil, &fits)
			if !fits {
				t.Fatal("tooltip clipped by chart")
			}
			for _, line := range lines {
				if strings.Contains(line, "played") || strings.Contains(line, " points") {
					t.Fatalf("excessive tooltip: %s", line)
				}
			}
			// Restore original values after exercising coincident geometry.
			evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="thresholds"]'));const rows=JSON.parse(document.querySelector('#explore-threshold-data').textContent);c.data.datasets[1].data[7]=rows.find(r=>r.season==='2023').values[1].ppg;c.update('none');return true}`, nil, new(bool))
			if err := canvas.Focus(); err != nil {
				t.Fatal(err)
			}
			if err := canvas.Press("Home"); err != nil {
				t.Fatal(err)
			}
			if err := canvas.Press("Enter"); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(`[data-threshold-detail="2016"]`)).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			if queryOf(t, page).Get("threshold-season") != "2016" {
				t.Fatal(page.URL())
			}
			if _, err := page.GoBack(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(`[data-threshold-detail="2023"]`)).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			if _, err := page.GoForward(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(`[data-threshold-detail="2016"]`)).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			if _, err := page.Reload(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(`[data-threshold-detail="2016"]`)).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			if err := page.Locator(`nav[aria-label="Threshold season details"] a[data-threshold-year="2021"]`).Click(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(`[data-threshold-detail="2021"]`)).ToContainText("41 earned points / 23 played"); err != nil {
				t.Fatal(err)
			}
			evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="thresholds"]'));const p=c.getDatasetMeta(0).data[8];return{x:p.x,y:p.y}}`, nil, &point)
			if vp.Name == "mobile" {
				if err := canvas.Tap(playwright.LocatorTapOptions{Position: &playwright.Position{X: point.X, Y: point.Y}}); err != nil {
					t.Fatal(err)
				}
			} else if err := canvas.Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: point.X, Y: point.Y}}); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(`[data-threshold-detail="2024"]`)).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			assertNoHorizontalOverflow(t, page)
			if err := page.Locator(`[data-threshold-table] > summary`).Click(); err != nil {
				t.Fatal(err)
			}
			assertNoHorizontalOverflow(t, page)
			chooseOption(t, page, "[data-threshold-controls] [data-segmented=threshold-outcome]", "bye")
			if err := expect.Locator(page.Locator(`[data-threshold-table-rows] tr:visible`)).ToHaveCount(3); err != nil {
				t.Fatal(err)
			}
			if err := page.Locator(`[data-threshold-sort="ppg"]`).Click(); err != nil {
				t.Fatal(err)
			}
			var values []float64
			evalJSON(t, page, `()=>[...document.querySelectorAll('[data-threshold-table-rows] tr:not([hidden])')].map(row=>Number(row.dataset.ppg))`, nil, &values)
			for i := 1; i < len(values); i++ {
				if values[i] > values[i-1] {
					t.Fatal("PPG sort", values)
				}
			}
			if _, err := page.Reload(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-segmented=threshold-outcome] input:checked")).ToHaveValue("bye"); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(`[data-threshold-table-rows] tr:visible`)).ToHaveCount(3); err != nil {
				t.Fatal(err)
			}
			var tableFits bool
			evalJSON(t, page, `()=>{const t=document.querySelector('.threshold-table');return t.scrollWidth<=t.clientWidth+1}`, nil, &tableFits)
			if !tableFits {
				t.Fatal("table requires horizontal scrolling")
			}
			assertNoHorizontalOverflow(t, page)
		})
	}
	t.Run("without scripts", func(t *testing.T) {
		page := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: true})
		if _, err := page.Goto(base + "explore?view=thresholds"); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator(`nav[aria-label="Threshold season details"] a[data-threshold-year="2022"]`).Click(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator(`[data-threshold-detail="2022"]`)).ToContainText("Equal-points tie across the boundary"); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator(`[data-threshold-table] > summary`).Click(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator(`[data-panel="thresholds"] table`)).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		chooseOption(t, page, "[data-threshold-controls] [data-segmented=threshold-outcome]", "bye")
		if err := page.Locator(`[data-threshold-controls] button`).Click(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator(`[data-threshold-table-rows] tr:visible`)).ToHaveCount(3); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator(`[data-threshold-sort="ppg"]`).Click(); err != nil {
			t.Fatal(err)
		}
		if queryOf(t, page).Get("threshold-outcome") != "bye" || queryOf(t, page).Get("threshold-sort") != "ppg" {
			t.Fatal("native table controls lose selection")
		}
		assertNoHorizontalOverflow(t, page)
	})
}
