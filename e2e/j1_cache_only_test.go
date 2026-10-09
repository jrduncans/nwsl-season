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

// visitCachePages visits every cache page at every viewport in parallel, each
// in its own browser context. It returns after all of them finish, because a
// parent test waits for its parallel subtests. Subtests must only read the
// shared fixture.
func visitCachePages(t *testing.T, f *fixture) {
	t.Helper()
	for _, vp := range []viewport{Desktop, Mobile} {
		for _, path := range cachePages() {
			t.Run(vp.Name+" /"+path, func(t *testing.T) {
				t.Parallel()
				page := newPage(t, vp)
				visit(t, page, f.URL(path))
				assertNoHorizontalOverflow(t, page)
			})
		}
	}
}

// TestJ1CacheOnlyPages is journey J1: every page loads from the cache with no
// browser errors or overflow and without contacting ASA, and still renders
// while ASA is down.
//
// The two phases run one after the other on a shared fixture: SetDown and
// Requests are fixture-wide, so the phase that asserts zero ASA requests must
// not overlap the phase that takes ASA down. Pages within a phase run in
// parallel.
func TestJ1CacheOnlyPages(t *testing.T) {
	f := newFixture(t)
	f.ASA.ResetRequests()

	t.Run("ASA up", func(t *testing.T) { visitCachePages(t, f) })
	assertNoASARequests(t, f.ASA)

	f.ASA.SetDown(true)
	t.Run("ASA down", func(t *testing.T) { visitCachePages(t, f) })
	assertNoASARequests(t, f.ASA)
}
