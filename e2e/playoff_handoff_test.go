//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
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
			if err := expect.Locator(page.Locator(".bracket-slot .fixture-outlook")).ToHaveCount(4); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("main")).ToContainText("Extra time"); err != nil {
				t.Fatal(err)
			}
			if !noScript {
				assertNoHorizontalOverflow(t, page)
			}
			capturePlayoffPage(t, page, vp, noScript, "bracket")
			click(t, page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "View final standings", Exact: playwright.Bool(true)}))
			if err := page.WaitForURL("**/nwsl-season/seasons/2026/regular-season"); err != nil {
				t.Fatal(err)
			}
		}
	})

	// Lower team IDs win every game, so final seeds follow team ID order.
	ids := make([]string, 0, len(j.Teams))
	for _, team := range j.Teams {
		ids = append(ids, team.TeamID)
	}
	sort.Strings(ids)
	seed := func(n int) string { return ids[n-1] }
	kickoff := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)
	match := func(i, home, away int, played bool) cache.Game {
		game := cache.Game{
			ASAID: fmt.Sprintf("playoff-%d", i), Season: currentSeason, Stage: "Playoffs", Status: fixtures.PreMatchStatus,
			HomeTeamID: seed(home), AwayTeamID: seed(away), KnockoutGame: true, RawJSON: "{}",
			KickoffUTC: kickoff.AddDate(0, 0, i).Format("2006-01-02 15:04:05 MST"),
		}
		if played {
			game.Status = fixtures.CompletedStatus
			game.HomeScore, game.AwayScore = sql.NullInt64{Int64: 2, Valid: true}, sql.NullInt64{Int64: 0, Valid: true}
		}
		return game
	}
	full := []cache.Game{}
	for i, pair := range [][2]int{{1, 8}, {4, 5}, {2, 7}, {3, 6}, {1, 4}, {2, 3}, {1, 2}} {
		full = append(full, match(i, pair[0], pair[1], true))
	}
	for _, tc := range []struct {
		name      string
		games     []cache.Game
		published int
		state     string
	}{
		{name: "partial", games: []cache.Game{match(3, 3, 6, false)}, published: 1, state: "partial"},
		{name: "full", games: full, state: "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replacePlayoffs(t, j, tc.games)
			forEachViewport(t, func(t *testing.T, vp viewport) {
				page := newPageWith(t, pageOptions{Viewport: vp})
				visit(t, page, j.URL("seasons/2026/playoffs"))
				expect := playwright.NewPlaywrightAssertions()
				if err := expect.Locator(page.Locator(fmt.Sprintf(`[data-bracket-state="%s"]`, tc.state))).ToBeVisible(); err != nil {
					t.Fatal(err)
				}
				if err := expect.Locator(page.Locator(".bracket-provenance")).ToHaveCount(0); err != nil {
					t.Fatal(err)
				}
				assertNoHorizontalOverflow(t, page)
				capturePlayoffPage(t, page, vp, false, tc.name)
			})
		})
	}
}

// replacePlayoffs writes the season's playoff matches straight to the cache,
// standing in for a sync that has picked them up from ASA.
func replacePlayoffs(t *testing.T, j *journey, games []cache.Game) {
	t.Helper()
	ctx := context.Background()
	db, err := cache.Open(ctx, j.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	teams := make([]cache.Team, 0, len(j.Teams))
	for _, team := range j.Teams {
		teams = append(teams, cache.Team{ASAID: team.TeamID, Name: team.TeamName, ShortName: team.TeamShortName, Abbreviation: team.TeamAbbreviation, RawJSON: "{}"})
	}
	if _, err := db.ReplaceSeason(ctx, currentSeason, "Playoffs", teams, games, time.Now().UTC()); err != nil {
		t.Fatalf("seed playoffs: %v", err)
	}
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
