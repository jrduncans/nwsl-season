//go:build e2e

package e2e

import (
	"reflect"
	"strings"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/apptest"
)

const opponentsPath = "explore?view=season-trend&team=odMX2OJqYL"

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
		official: official.hidden ? '' : text('[data-trend-margin-official]'),
		headings: [...document.querySelectorAll('[data-trend-headings] th')].map(cell => cell.textContent.trim()),
		rows: [...document.querySelectorAll('[data-trend-rows] tr')].map(row => [...row.cells].map(cell => cell.textContent.trim())),
	};
}`

type opponentView struct {
	Shown, Checked  bool
	Note, Official  string
	Tiles, Headings []string
	Rows            [][]string
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
			var state opponentView
			read := func() opponentView {
				t.Helper()
				evalJSON(t, page, opponentState, nil, &state)
				return state
			}
			official := seasonTrendBars(t, page)
			done := expectInPlace(t, page, "opponent adjustment")
			if err := checkbox.Check(); err != nil {
				t.Fatal(err)
			}
			if got := queryOf(t, page).Get("opponents"); got != "adjusted" {
				t.Fatalf("URL opponents = %q", got)
			}
			adjusted := read()
			if !strings.Contains(adjusted.Note, "Adjusted to an average opponent") || !strings.Contains(strings.Join(adjusted.Tiles, "|"), "adjusted goals") ||
				adjusted.Official != "W–D–L and points use official results." || !strings.Contains(strings.Join(adjusted.Headings, "|"), "Adjusted xG against") {
				t.Fatalf("adjusted state = %+v", adjusted)
			}
			if reflect.DeepEqual(seasonTrendBars(t, page), official) {
				t.Fatal("Scoring balance bars did not change")
			}
			if err := expect.Locator(page.Locator("[data-trend-margin-title]")).ToHaveText("Adjusted goals"); err != nil {
				t.Fatal(err)
			}

			// Margins keeps it; Goals vs xG hides the control but keeps the value.
			if _, err := page.Locator("[data-trend-view]").SelectOption(playwright.SelectOptionValues{Values: &[]string{"difference"}}); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-trend-margin-title]")).ToHaveText("Adjusted goal margin"); err != nil {
				t.Fatal(err)
			}
			// An adjusted margin can change sign, so it no longer reads as the result.
			if err := expect.Locator(page.Locator("[data-trend-margin-direction]")).ToHaveText("Zero: level"); err != nil {
				t.Fatal(err)
			}
			if _, err := page.Locator("[data-trend-view]").SelectOption(playwright.SelectOptionValues{Values: &[]string{"compare"}}); err != nil {
				t.Fatal(err)
			}
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
			if !reflect.DeepEqual(read(), adjusted) {
				t.Fatalf("reload = %+v, want %+v", state, adjusted)
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

func TestExploreOpponentAdjustmentNativeSubmission(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioSeasonTrend)
	page := newPageWith(t, pageOptions{Viewport: Desktop, NoScript: true})
	visitStatic(t, page, base+opponentsPath+"&season=2025&smoothing=none")
	for _, adjusted := range []bool{true, false} {
		if err := page.Locator("[data-trend-opponents]").SetChecked(adjusted); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator("[data-trend-submit]").Click(); err != nil {
			t.Fatal(err)
		}
		if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateLoad}); err != nil {
			t.Fatal(err)
		}
		query := queryOf(t, page)
		if got := query["opponents"]; adjusted && (len(got) != 1 || got[0] != "adjusted") || !adjusted && len(got) != 0 {
			t.Fatalf("adjusted=%v submitted opponents %q", adjusted, got)
		}
		if query.Get("season") != "2025" || query.Get("team") != "odMX2OJqYL" {
			t.Fatalf("submission lost the selection: %s", query.Encode())
		}
		headings := page.Locator("[data-trend-headings]")
		if adjusted {
			err := expect.Locator(headings).ToContainText("Adjusted goals for")
			if err != nil {
				t.Fatal(err)
			}
		} else if err := expect.Locator(headings).Not().ToContainText("Adjusted"); err != nil {
			t.Fatal(err)
		}
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
