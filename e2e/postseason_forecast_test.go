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
				assertPlayoffIdentityAndTitleVisible(t, page)
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
				assertTournamentGeometry(t, page, ".playoff-forecast-bracket")
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
					if err := expect.Locator(page.Locator(".bracket")).ToContainText("Penalties 5–4"); err != nil {
						t.Fatal(err)
					}
				}
				assertNoHorizontalOverflow(t, page)
				assertTournamentGeometry(t, page, ".bracket")
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
		// A local match change must offer exactly its participants and submit
		// that pair, rather than leaving options from the preceding match.
		selectValue(t, page.Locator("#playoff-match"), "quarterfinal-2")
		if err := playwright.NewPlaywrightAssertions().Locator(page.Locator("#playoff-selection option")).ToHaveCount(2); err != nil {
			t.Fatal(err)
		}
		if err := playwright.NewPlaywrightAssertions().Locator(page.Locator("#playoff-selection")).ToHaveValue(fmt.Sprintf("quarterfinal-2:%s:%s:%s", ids[3], ids[3], ids[4])); err != nil {
			t.Fatal("match change offered the wrong winner or participants")
		}
		click(t, page.Locator(`input[name="forecast-view"][value="bracket"]`).Locator(".."))
		if err := playwright.NewPlaywrightAssertions().Locator(page.Locator("#playoff-match")).ToHaveValue("quarterfinal-2"); err != nil {
			t.Fatal("changing views lost the pending match")
		}
		// The recovery check below uses the first quarterfinal.
		selectValue(t, page.Locator("#playoff-match"), "quarterfinal-1")
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

func TestPlayoffScenarioWithoutJavaScript(t *testing.T) {
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
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp, NoScript: true})
		visitStatic(t, page, j.URL("seasons/2026/playoffs/forecast"))
		selection := fmt.Sprintf("quarterfinal-2:%s:%s:%s", ids[4], ids[3], ids[4])
		selectValue(t, page.Locator("#playoff-selection"), selection)
		click(t, page.Locator(".playoff-scenario-form button"))
		if err := waitForecastURL(page, assumptionURL); err != nil {
			t.Fatal(err)
		}
		if got := mustQuery(t, page.URL()).Get("p"); got != selection {
			t.Fatalf("grouped winner selector submitted %q, want %q", got, selection)
		}
		assertNoHorizontalOverflow(t, page)
	})
}

func assertPlayoffIdentityAndTitleVisible(t *testing.T, page playwright.Page) {
	t.Helper()
	var issues []string
	evalJSON(t, page, `() => {
		const issues = [];
		const table = document.querySelector('.playoffs-forecast-table');
		const edge = table.closest('.table-scroll').getBoundingClientRect().right;
		const champion = table.querySelector('tbody td').getBoundingClientRect();
		if (champion.right > edge + 1) issues.push('Champion requires horizontal scrolling');
		for (const identity of table.querySelectorAll('.playoff-team-identity')) {
			const seed = identity.querySelector('.bracket-seed').getBoundingClientRect();
			const team = identity.querySelector('.team-name').getBoundingClientRect();
			if (Math.abs(seed.y + seed.height / 2 - team.y - team.height / 2) > 1) issues.push('seed is not centered with its team');
		}
		return issues;
	}`, nil, &issues)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
}

// Check the visible layout, including shortened candidate lists after results,
// rather than hard-coding the card heights used by one screenshot.
func assertTournamentGeometry(t *testing.T, page playwright.Page, selector string) {
	t.Helper()
	if _, err := page.WaitForFunction(`selector => {
		const root = document.querySelector(selector);
		return root.classList.contains('bracket') && innerWidth <= 700 || root.classList.contains('bracket-is-laid-out');
	}`, selector); err != nil {
		t.Fatal(err)
	}
	var issues []string
	evalJSON(t, page, `selector => {
		const root = document.querySelector(selector), issues = [];
		const columns = [...root.querySelectorAll('[data-bracket-round]')];
		const nodes = new Map([...root.querySelectorAll('[data-bracket-node]')].map(card => [card.dataset.bracketNode, card]));
		const center = card => { const r = card.getBoundingClientRect(); return r.y + r.height / 2; };
		for (const column of columns) {
			const cards = [...column.querySelectorAll('[data-bracket-node]')].sort((a, b) => center(a) - center(b));
			for (let i = 1; i < cards.length; i++) {
				if (cards[i].getBoundingClientRect().top < cards[i-1].getBoundingClientRect().bottom + 15) issues.push('cards overlap or lack a readable gap');
			}
		}
		if (root.classList.contains('bracket') && innerWidth <= 700) {
			if (root.classList.contains('bracket-is-laid-out')) issues.push('phone bracket should stack rounds');
			return issues;
		}
		const headings = columns.map(c => c.querySelector('h2').getBoundingClientRect().top);
		if (Math.max(...headings) - Math.min(...headings) > 1) issues.push('round headings are not aligned');
		let expectedPaths = 0;
		for (const card of nodes.values()) {
			const sources = [...card.querySelectorAll('[data-bracket-source]')].map(a => nodes.get(a.dataset.bracketSource)).filter(Boolean);
			expectedPaths += sources.length;
			if (sources.length && Math.abs(center(card) - sources.reduce((sum, s) => sum + center(s), 0) / sources.length) > 2) issues.push('match is not centered between its feeders');
		}
		const paths = [...root.querySelectorAll('.bracket-connectors path')];
		if (paths.length !== expectedPaths) issues.push('missing feeder connections');
		for (const path of paths) {
			const source = nodes.get(path.dataset.bracketFrom), target = nodes.get(path.dataset.bracketTo);
			const anchor = [...target.querySelectorAll('[data-bracket-source]')].find(a => a.dataset.bracketSource === source.dataset.bracketNode);
			const start = path.getPointAtLength(0).matrixTransform(path.getScreenCTM());
			const end = path.getPointAtLength(path.getTotalLength()).matrixTransform(path.getScreenCTM());
			if (Math.abs(start.x - source.getBoundingClientRect().right) > 2 || Math.abs(start.y - center(source)) > 2 || Math.abs(end.x - target.getBoundingClientRect().left) > 2 || Math.abs(end.y - center(anchor)) > 2) issues.push('connector misses its source or destination');
		}
		return issues;
	}`, selector, &issues)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
}
