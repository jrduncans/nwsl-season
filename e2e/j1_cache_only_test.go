//go:build e2e

package e2e

import (
	"testing"
)

// cachePages are the app paths that must render from the local cache alone.
// Paths are relative to the mount prefix.
func cachePages() []string {
	season := "seasons/" + currentSeason
	return []string{
		"",
		"seasons",
		"history",
		"explore",
		season,
		season + "/fixtures",
		season + "/schedule-difficulty",
		season + "/forecast",
		season + "/model-evaluation",
		season + "/clinching",
	}
}

// TestJ1CacheOnlyPages is journey J1: every page loads from the cache with no
// browser errors or overflow and without contacting ASA, and still renders
// while ASA is down.
func TestJ1CacheOnlyPages(t *testing.T) {
	f := newFixture(t)
	f.ASA.ResetRequests()

	for _, vp := range []viewport{Desktop, Mobile} {
		t.Run(vp.Name, func(t *testing.T) {
			page := newPage(t, vp)

			visitAll := func(t *testing.T) {
				for _, path := range cachePages() {
					t.Run("/"+path, func(t *testing.T) {
						visit(t, page, f.URL(path))
						assertNoHorizontalOverflow(t, page)
					})
				}
			}

			visitAll(t)
			assertNoASARequests(t, f.ASA)

			f.ASA.SetDown(true)
			t.Cleanup(func() { f.ASA.SetDown(false) })
			t.Run("ASA down", visitAll)
			assertNoASARequests(t, f.ASA)
		})
	}
}
