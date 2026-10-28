//go:build e2e

package e2e

import (
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

func TestPostseasonForecast(t *testing.T) {
	t.Parallel()
	j := newJourney(t, journeyConfig{playedRounds: 30, score: func(game asa.Game) (int, int) {
		if game.HomeTeamID < game.AwayTeamID {
			return 2, 0
		}
		return 0, 2
	}})
	j.ASA.ResetRequests()
	t.Cleanup(func() { assertNoASARequests(t, j.ASA) })
	ids := make([]string, 0, len(j.Teams))
	for _, team := range j.Teams {
		ids = append(ids, team.TeamID)
	}
	sort.Strings(ids)
	games := []cache.Game{}
	for i, pair := range [][2]int{{1, 8}, {4, 5}, {2, 7}, {3, 6}, {1, 4}, {2, 3}, {1, 2}} {
		games = append(games, cache.Game{
			ASAID: fmt.Sprintf("postseason-%d", i), Season: currentSeason, Stage: "Playoffs", Status: fixtures.CompletedStatus,
			HomeTeamID: ids[pair[0]-1], AwayTeamID: ids[pair[1]-1], KnockoutGame: true,
			KickoffUTC: fmt.Sprintf("2026-11-%02dT20:00:00Z", i+1),
			HomeScore:  sql.NullInt64{Int64: 2, Valid: true}, AwayScore: sql.NullInt64{Valid: true},
		})
	}
	// One quarterfinal advances through a shootout; the recorded score stays
	// tied while the actual winner advances and the loser gets exact zero.
	games[0].AwayScore = games[0].HomeScore
	games[0].Penalties = sql.NullBool{Bool: true, Valid: true}
	games[0].HomePenalties, games[0].AwayPenalties = sql.NullInt64{Int64: 5, Valid: true}, sql.NullInt64{Int64: 4, Valid: true}
	partial := games[3]
	partial.Status = fixtures.PreMatchStatus
	partial.HomeScore, partial.AwayScore = sql.NullInt64{}, sql.NullInt64{}
	unresolved := games[0]
	unresolved.Penalties = sql.NullBool{}
	unresolved.HomePenalties, unresolved.AwayPenalties = sql.NullInt64{}, sql.NullInt64{}
	for _, tc := range []struct {
		name  string
		games []cache.Game
	}{
		{name: "waiting"},
		{name: "partial", games: []cache.Game{partial}},
		{name: "quarterfinal", games: games[:1]},
		{name: "semifinal", games: games[:6]},
		{name: "champion", games: games},
		{name: "unresolved", games: []cache.Game{unresolved}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.games) > 0 {
				replacePlayoffs(t, j, tc.games)
			}
			forEachViewport(t, func(t *testing.T, vp viewport) {
				page := newPage(t, vp)
				visit(t, page, j.URL("seasons/2026/playoffs/forecast"))
				expect := playwright.NewPlaywrightAssertions()
				if err := expect.Locator(page.Locator(".playoffs-forecast-table")).ToBeVisible(); err != nil {
					t.Fatal(err)
				}
				if tc.name == "champion" {
					if err := expect.Locator(page.Locator(".playoffs-forecast-table tbody tr").First()).ToContainText("100%"); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "quarterfinal" || tc.name == "semifinal" || tc.name == "champion" {
					if err := expect.Locator(page.Locator(".playoffs-forecast-table tbody tr").Nth(7)).ToContainText("0%"); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "unresolved" {
					if err := expect.Locator(page.Locator(".playoffs-forecast-table")).ToContainText("—"); err != nil {
						t.Fatal(err)
					}
				}
				assertNoHorizontalOverflow(t, page)
				capturePlayoffPage(t, page, vp, false, "forecast-"+tc.name)
				click(t, page.Locator(`input[name="forecast-view"][value="heatmap"]`).Locator(".."))
				if err := expect.Locator(page.Locator(".playoffs-heatmap")).ToBeVisible(); err != nil {
					t.Fatal(err)
				}
				assertNoHorizontalOverflow(t, page)
				capturePlayoffPage(t, page, vp, false, "forecast-heatmap-"+tc.name)
				click(t, page.Locator(`input[name="forecast-view"][value="bracket"]`).Locator(".."))
				if err := expect.Locator(page.Locator(".playoff-forecast-bracket")).ToBeVisible(); err != nil {
					t.Fatal(err)
				}
				assertNoHorizontalOverflow(t, page)
				capturePlayoffPage(t, page, vp, false, "forecast-bracket-"+tc.name)
				visit(t, page, j.URL("seasons/2026/playoffs"))
				if tc.name == "waiting" || tc.name == "partial" {
					if err := expect.Locator(page.Locator(".bracket-slot .fixture-outlook")).ToHaveCount(4); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "unresolved" {
					if err := expect.Locator(page.Locator(".bracket-slot .fixture-outlook")).ToHaveCount(0); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "quarterfinal" {
					if err := expect.Locator(page.Locator(".bracket")).ToContainText("2–2 (5–4 pens)"); err != nil {
						t.Fatal(err)
					}
				}
				assertNoHorizontalOverflow(t, page)
				capturePlayoffPage(t, page, vp, false, "bracket-"+tc.name)

			})
		})
	}
}

func TestPlayoffsForecastScenario(t *testing.T) {
	t.Parallel()
	forEachViewport(t, func(t *testing.T, vp viewport) {
		j := newJourney(t, journeyConfig{playedRounds: 30, score: func(game asa.Game) (int, int) {
			if game.HomeTeamID < game.AwayTeamID {
				return 2, 0
			}
			return 0, 2
		}})
		j.ASA.ResetRequests()
		t.Cleanup(func() { assertNoASARequests(t, j.ASA) })
		ids := make([]string, 0, len(j.Teams))
		for _, team := range j.Teams {
			ids = append(ids, team.TeamID)
		}
		sort.Strings(ids)
		page := newPageWith(t, pageOptions{Viewport: vp, ExpectedBadRequestURL: func(raw string) bool {
			parsed, err := url.Parse(raw)
			return err == nil && parsed.Path == "/nwsl-season/seasons/2026/playoffs/forecast" && parsed.Query().Get("p") == fmt.Sprintf("quarterfinal-1:%s:%s:%s", ids[0], ids[0], ids[7]) && parsed.Query().Get("c") == modelComparison
		}})
		visit(t, page, j.URL("seasons/2026/playoffs/forecast"))
		click(t, page.Locator(".playoff-scenario-form button"))
		if err := waitForecastURL(page, assumptionURL); err != nil {
			t.Fatal(err)
		}
		expect := playwright.NewPlaywrightAssertions()
		if err := expect.Locator(page.Locator(".forecast-assumptions li")).ToHaveCount(1); err != nil {
			t.Fatal(err)
		}
		assumed := mustQuery(t, page.URL()).Get("p")
		click(t, page.Locator("[data-forecast-controls] > summary"))
		selectValue(t, page.Locator("#forecast-comparison"), modelComparison)
		if err := waitForecastURL(page, regexp.MustCompile(`[?&]c=`+modelComparison)); err != nil {
			t.Fatal(err)
		}
		if got := mustQuery(t, page.URL()).Get("p"); got != assumed {
			t.Fatal("comparison dropped playoff scenario")
		}
		if err := expect.Locator(page.Locator(".playoffs-forecast-table thead")).ToContainText("Results Poisson"); err != nil {
			t.Fatal(err)
		}
		staleURL := page.URL()
		assertNoHorizontalOverflow(t, page)
		capturePlayoffPage(t, page, vp, false, "forecast-scenario")
		click(t, page.Locator(".forecast-assumptions a"))
		if err := expect.Locator(page.Locator(".forecast-assumptions li")).ToHaveCount(0); err != nil {
			t.Fatal(err)
		}
		replacePlayoffs(t, j, []cache.Game{{ASAID: "played-scenario", Season: currentSeason, Stage: "Playoffs", Status: fixtures.CompletedStatus, HomeTeamID: ids[0], AwayTeamID: ids[7], KickoffUTC: "2026-11-01T20:00:00Z", HomeScore: sql.NullInt64{Int64: 2, Valid: true}, AwayScore: sql.NullInt64{Valid: true}}})
		response, err := page.Goto(staleURL)
		if err != nil || response == nil || response.Status() != 400 {
			t.Fatalf("stale page response=%v err=%v", response, err)
		}
		if err := expect.Locator(page.Locator("h1")).ToContainText("out of date"); err != nil {
			t.Fatal(err)
		}
		click(t, page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "View the latest forecast without fixed results", Exact: playwright.Bool(true)}))
		if err := expect.Locator(page.Locator(".playoffs-forecast-table")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if got := mustQuery(t, page.URL()); got.Get("p") != "" || got.Get("c") != modelComparison {
			t.Fatal("recovery lost model choices or retained a stale winner")
		}
		assertNoHorizontalOverflow(t, page)
	})
}
