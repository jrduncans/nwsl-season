//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/asa"
)

func TestPlayoffHandoff(t *testing.T) {
	t.Parallel()
	j := newJourney(t, journeyConfig{playedRounds: 30, score: func(game asa.Game) (int, int) {
		if game.HomeTeamID < game.AwayTeamID {
			return 2, 0
		}
		return 0, 2
	}})
	j.ASA.ResetRequests()
	t.Cleanup(func() { assertNoASARequests(t, j.ASA) })
	forEachViewport(t, func(t *testing.T, vp viewport) {
		for _, noScript := range []bool{false, true} {
			page := newPageWith(t, pageOptions{Viewport: vp, NoScript: noScript})
			load := visit
			if noScript {
				load = visitStatic
			}
			load(t, page, j.URL("seasons/2026/regular-season"))
			expect := playwright.NewPlaywrightAssertions()
			if err := expect.Locator(page.Locator(".season-handoff")).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			capturePlayoffPage(t, page, vp, noScript, "standings")
			click(t, page.Locator(".season-handoff a").First())
			if err := page.WaitForURL("**/nwsl-season/seasons/2026/playoffs"); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(".bracket-slot")).ToHaveCount(7); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator(".bracket-provenance").First()).ToContainText("Expected from final seeds"); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("main")).ToContainText("not published fixtures"); err != nil {
				t.Fatal(err)
			}
			if !noScript {
				assertNoHorizontalOverflow(t, page)
			}
			capturePlayoffPage(t, page, vp, noScript, "bracket")
			click(t, page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "View final regular-season standings", Exact: playwright.Bool(true)}))
			if err := page.WaitForURL("**/nwsl-season/seasons/2026/regular-season"); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func capturePlayoffPage(t *testing.T, page playwright.Page, vp viewport, noScript bool, name string) {
	t.Helper()
	if dir := os.Getenv(traceDirEnv); dir != "" {
		path := filepath.Join(dir, fmt.Sprintf("playoffs-%s-noscript-%t-%s.png", vp.Name, noScript, name))
		if _, err := page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String(path), FullPage: playwright.Bool(true)}); err != nil {
			t.Fatal(err)
		}
	}
}
