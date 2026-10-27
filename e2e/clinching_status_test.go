//go:build e2e

package e2e

import (
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

func TestClinchingStatusDisclosure(t *testing.T) {
	t.Parallel()
	j := newJourney(t, lateSeason())
	for _, view := range []string{"next-matchday", "rest-of-season"} {
		for _, vp := range []viewport{Desktop, Mobile} {
			t.Run(view+"/"+vp.Name, func(t *testing.T) {
				page := newPage(t, vp)
				visit(t, page, j.URL(seasonPath("clinching")+"?view="+view))
				disclosure := page.Locator(".clinching-status-overview")
				expect := playwright.NewPlaywrightAssertions()
				if err := expect.Locator(disclosure).ToHaveCount(1); err != nil {
					t.Fatal(err)
				}
				check := func(open bool) {
					t.Helper()
					got, err := disclosure.Evaluate("element => element.open", nil)
					if err != nil || got != open {
						t.Fatalf("open = %v, want %v: %v", got, open, err)
					}
				}
				check(vp.Width > 700)
				other := Desktop
				if vp.Width > 700 {
					other = Mobile
				}
				if err := page.SetViewportSize(other.Width, other.Height); err != nil {
					t.Fatal(err)
				}
				if err := expect.Locator(disclosure).ToHaveJSProperty("open", other.Width > 700); err != nil {
					t.Fatal(err)
				}
				summary := disclosure.Locator("summary")
				if err := summary.Focus(); err != nil {
					t.Fatal(err)
				}
				if err := page.Keyboard().Press("Enter"); err != nil {
					t.Fatal(err)
				}
				if err := expect.Locator(disclosure).ToHaveJSProperty("open", other.Width <= 700); err != nil {
					t.Fatal(err)
				}
				if err := page.SetViewportSize(vp.Width, vp.Height); err != nil {
					t.Fatal(err)
				}
				check(other.Width <= 700)
			})
			t.Run(view+"/"+vp.Name+"/no-script", func(t *testing.T) {
				page := newPageWith(t, pageOptions{Viewport: vp, NoScript: true})
				visitStatic(t, page, j.URL(seasonPath("clinching")+"?view="+view))
				if err := playwright.NewPlaywrightAssertions().Locator(page.Locator(".clinching-status-overview .clinching-status-item").First()).ToBeVisible(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
