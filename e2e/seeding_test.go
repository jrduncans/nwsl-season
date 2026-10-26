//go:build e2e

package e2e

import (
	"strings"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

func TestPagesClinchingSeeding(t *testing.T) {
	t.Parallel()
	j := newJourney(t, lateSeason())
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPage(t, vp)
		visit(t, page, j.URL(seasonPath("clinching")+"?view=seeding"))
		expect := playwright.NewPlaywrightAssertions()
		if err := expect.Locator(page.Locator(".seeding-grid")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		// Without a chosen finish, only the likeliest one's conditions render.
		conditions, err := page.Locator("#seeding-conditions").Count()
		if err != nil {
			t.Fatal(err)
		}
		if mostLikely, err := page.Locator(".seeding-most-likely").Count(); err != nil || mostLikely != conditions {
			t.Fatalf("conditions %d, most likely %d, %v", conditions, mostLikely, err)
		}
		assertSeedingDOMBudget(t, page)
		if err := page.Locator(".seeding-grid tbody th a").First().Click(); err != nil {
			t.Fatal(err)
		}
		if err := page.WaitForURL("**/clinching?**finish=**"); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("#seeding-conditions")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(page.URL(), "finish=") {
			t.Fatalf("finish selection missing from URL: %s", page.URL())
		}
		assertSeedingDOMBudget(t, page)
		assertSeedingAlternatives(t, page)
		assertSeedingFocus(t, page, vp)
		if err := page.Locator(".seeding-fixed-results > summary").Click(); err != nil {
			t.Fatal(err)
		}
		selected, err := page.Locator("#seeding-team").InputValue()
		if err != nil {
			t.Fatal(err)
		}
		fixture := page.Locator("#forecast-fixture")
		fixtureID, err := fixture.InputValue()
		if err != nil || fixtureID == "" {
			t.Fatalf("fixture=%q err=%v", fixtureID, err)
		}
		if err := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Add result", Exact: playwright.Bool(true)}).Click(); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator("#forecast-update-button").Click(); err != nil {
			t.Fatal(err)
		}
		if err := page.WaitForURL("**/clinching?**p=**"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(page.URL(), "team="+selected) || !strings.Contains(page.URL(), "view=seeding") {
			t.Fatalf("builder lost selection: %s", page.URL())
		}
		if err := expect.Locator(page.Locator(".forecast-scenario .forecast-assumptions li")).ToHaveCount(1); err != nil {
			t.Fatal(err)
		}
		// Changing teams retains the fixed result, including behind a proxy prefix.
		options, err := page.Locator("#seeding-team option").All()
		if err != nil || len(options) < 2 {
			t.Fatalf("team options: %v", err)
		}
		other, err := options[1].GetAttribute("value")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := page.Locator("#seeding-team").SelectOption(playwright.SelectOptionValues{Values: &[]string{other}}); err != nil {
			t.Fatal(err)
		}
		if err := page.WaitForURL("**/clinching?**team=" + other + "**"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(page.URL(), "p=") {
			t.Fatalf("team switch lost fixed result: %s", page.URL())
		}
	})
}

func assertSeedingFocus(t *testing.T, page playwright.Page, vp viewport) {
	t.Helper()
	expect := playwright.NewPlaywrightAssertions()
	url := page.URL()
	conditions, err := page.Locator("#seeding-conditions").InnerHTML()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := page.Locator(".seeding-grid tbody tr").Count()
	if err != nil {
		t.Fatal(err)
	}
	columns, err := page.Locator(".seeding-grid thead th").Count()
	if err != nil || columns < 2 {
		t.Fatalf("columns = %d, %v", columns, err)
	}
	if err := page.SetViewportSize(390, 844); err != nil {
		t.Fatal(err)
	}
	focus := page.Locator("#seeding-focus")
	if err := expect.Locator(focus).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if value, err := focus.InputValue(); err != nil || value != "all" {
		t.Fatalf("initial focus = %q, %v", value, err)
	}
	for _, selected := range []string{"0", "all", "0"} {
		selectValue(t, focus, selected)
		want := 2
		if selected == "all" {
			want = columns
		}
		if err := expect.Locator(page.Locator(".seeding-grid thead th:visible")).ToHaveCount(want); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator(".seeding-grid tbody tr:visible")).ToHaveCount(rows); err != nil {
			t.Fatal(err)
		}
		if selected != "all" {
			if n, err := page.Locator(".seeding-joint-heading:visible").Count(); err != nil || n != 1 {
				t.Fatalf("focused chance labels = %d, %v", n, err)
			}
		}
	}
	if err := page.SetViewportSize(1280, 800); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(focus).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator(".seeding-grid thead th:visible")).ToHaveCount(columns); err != nil {
		t.Fatal(err)
	}
	if err := page.SetViewportSize(390, 844); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator(".seeding-grid thead th:visible")).ToHaveCount(2); err != nil {
		t.Fatal(err)
	}
	selectValue(t, focus, "all")
	after, err := page.Locator("#seeding-conditions").InnerHTML()
	if err != nil || after != conditions || page.URL() != url {
		t.Fatalf("table focus changed scenario state: %s, %v", page.URL(), err)
	}
	if err := expect.Locator(page.Locator(".seeding-grid")).ToHaveCount(1); err != nil {
		t.Fatal(err)
	}
	assertKeyboardDisclosures(t, page)
	assertNoHorizontalOverflow(t, page)
	if err := page.SetViewportSize(vp.Width, vp.Height); err != nil {
		t.Fatal(err)
	}
}

func TestPagesClinchingSeedingNoScript(t *testing.T) {
	t.Parallel()
	j := newJourney(t, lateSeason())
	forEachViewport(t, func(t *testing.T, vp viewport) {
		page := newPageWith(t, pageOptions{Viewport: vp, NoScript: true})
		visitStatic(t, page, j.URL(seasonPath("clinching")+"?view=seeding"))
		expect := playwright.NewPlaywrightAssertions()
		if err := expect.Locator(page.Locator("#seeding-focus")).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		columns, err := page.Locator(".seeding-grid thead th").Count()
		if err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator(".seeding-grid thead th:visible")).ToHaveCount(columns); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator(".seeding-grid tbody th a").First().Click(); err != nil {
			t.Fatal(err)
		}
		if err := page.WaitForURL("**/clinching?**finish=**"); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("#seeding-conditions")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		assertKeyboardDisclosures(t, page)
		assertSeedingDOMBudget(t, page)
		assertSeedingAlternatives(t, page)
	})
}

func assertSeedingAlternatives(t *testing.T, page playwright.Page) {
	t.Helper()
	before := page.URL()
	expect := playwright.NewPlaywrightAssertions()
	if err := page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "Inspect complete certified alternatives", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("#seeding-alternative")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.URL(), "alternative=1") {
		t.Fatalf("missing alternative state: %s", page.URL())
	}
	count, err := page.Locator("#seeding-alternative .clinching-condition").Count()
	if err != nil || count > 4 {
		t.Fatalf("unbounded requirements: %d, %v", count, err)
	}
	assertSeedingDOMBudget(t, page)
	assertKeyboardDisclosures(t, page)
	assertNoHorizontalOverflow(t, page)
	if err := page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "← All finishes and summary", Exact: playwright.Bool(true)}).Click(); err != nil {
		t.Fatal(err)
	}
	if page.URL() != before {
		t.Fatalf("alternative navigation changed selected scenario: %s => %s", before, page.URL())
	}
}

// Catch a return to eagerly rendering every cached alternative.
func assertSeedingDOMBudget(t *testing.T, page playwright.Page) {
	t.Helper()
	nodes, err := page.Locator("*").Count()
	if err != nil {
		t.Fatal(err)
	}
	if nodes > 1500 {
		t.Fatalf("seeding page rendered %d elements; limit is 1500", nodes)
	}
	images, err := page.Locator("img").Count()
	if err != nil {
		t.Fatal(err)
	}
	if images > 250 {
		t.Fatalf("seeding page rendered %d images; limit is 250", images)
	}
}
