//go:build e2e

package e2e

import (
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

// Switching the outlook is a local presentation choice: it must not discard
// an expanded distribution or an assumption that has not been applied yet.
func testForecastOutlookViews(t *testing.T, f *fixture) {
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp})
		visit(t, page, f.URL(seasonPath("forecast")))
		expect := playwright.NewPlaywrightAssertions()
		settings := page.Locator("[data-forecast-controls]")
		if err := expect.Locator(settings).ToHaveJSProperty("open", false); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("#forecast-model")).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		click(t, settings.Locator("summary"))
		for _, selector := range []string{"#forecast-model", "#forecast-comparison", ".forecast-model-detail", ".forecast-evaluation-link", ".forecast-meta"} {
			if err := expect.Locator(settings.Locator(selector)).ToBeVisible(); err != nil {
				t.Errorf("one disclosure should reveal %s: %v", selector, err)
			}
		}
		assertNoHorizontalOverflow(t, page)
		click(t, settings.Locator("summary"))
		table := page.Locator(`[data-forecast-view-panel="table"]`)
		heatmap := page.Locator(`[data-forecast-view-panel="heatmap"]`)
		if err := expect.Locator(table).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(heatmap).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		distribution := table.Locator(".forecast-distribution").First()
		click(t, distribution.Locator("summary"))
		click(t, page.Locator("form[data-assumption-builder] button[type=submit]"))
		if err := expect.Locator(page.Locator("#forecast-pending-list li")).ToHaveCount(1); err != nil {
			t.Fatal(err)
		}
		beforeURL := page.URL()
		beforeTable, err := table.TextContent()
		if err != nil {
			t.Fatal(err)
		}
		if err := page.Locator(`input[name="forecast-view"][value="heatmap"]`).Check(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(heatmap).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(table).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(heatmap.Locator("tbody tr")).ToHaveCount(fixtureTeams); err != nil {
			t.Error(err)
		}
		if err := expect.Locator(heatmap.Locator("tbody tr").First().Locator("td")).ToHaveCount(fixtureTeams); err != nil {
			t.Error(err)
		}
		assertNoHorizontalOverflow(t, page)
		if err := page.Locator(`input[name="forecast-view"][value="table"]`).Check(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(table).ToBeVisible(); err != nil {
			t.Error(err)
		}
		if err := expect.Locator(distribution).ToHaveJSProperty("open", true); err != nil {
			t.Errorf("switching views collapsed the existing finish distribution: %v", err)
		}
		if err := expect.Locator(table).ToHaveText(beforeTable); err != nil {
			t.Errorf("switching views changed the forecast: %v", err)
		}
		if err := expect.Locator(page.Locator("#forecast-pending-list li")).ToHaveCount(1); err != nil {
			t.Errorf("switching views discarded the pending assumption: %v", err)
		}
		if got := page.URL(); got != beforeURL {
			t.Errorf("switching views navigated from %q to %q", beforeURL, got)
		}
	})
}
