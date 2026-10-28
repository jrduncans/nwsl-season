//go:build e2e

package e2e

import (
	"database/sql"
	"fmt"
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
				visit(t, page, j.URL("seasons/2026/regular-season"))
				click(t, page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "Forecast lab", Exact: playwright.Bool(true)}))
				expect := playwright.NewPlaywrightAssertions()
				if err := expect.Locator(page.Locator(".postseason-table")).ToBeVisible(); err != nil {
					t.Fatal(err)
				}
				if err := expect.Locator(page.Locator(".forecast-builder, .forecast-distribution, .forecast-view-toolbar")).ToHaveCount(0); err != nil {
					t.Fatal(err)
				}
				if tc.name == "champion" {
					if err := expect.Locator(page.Locator(".postseason-table tbody tr").First()).ToContainText("100%"); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "quarterfinal" || tc.name == "semifinal" || tc.name == "champion" {
					if err := expect.Locator(page.Locator(".postseason-table tbody tr").Nth(7)).ToContainText("0%"); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "unresolved" {
					if err := expect.Locator(page.Locator(".postseason-table")).ToContainText("Withheld"); err != nil {
						t.Fatal(err)
					}
				}
				assertNoHorizontalOverflow(t, page)
				capturePlayoffPage(t, page, vp, false, "forecast-"+tc.name)
			})
		})
	}
}
