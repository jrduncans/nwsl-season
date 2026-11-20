package app

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

// stageStore serves each season and stage its own data, and nothing for
// stages it doesn't list.
type stageStore map[[2]string]cache.SeasonData

func (s stageStore) Status(context.Context, string, string) (cache.Status, error) {
	return cache.Status{}, nil
}

func (s stageStore) Season(_ context.Context, season, stage string) (cache.SeasonData, error) {
	return s[[2]string{season, stage}], nil
}

func sitemapHandler(publicURL string) http.Handler {
	store := stageStore{
		{"2026", "Regular Season"}: testSeasonData(),
		{"2025", "Regular Season"}: testSeasonData(),
	}
	return NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Rules: testRules(30), ForecastIterations: 20, Location: time.UTC, PublicURL: publicURL})
}

func get(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

func TestSitemapListsEveryStagePageAndMatch(t *testing.T) {
	handler := sitemapHandler("https://example.test/nwsl/")
	response := get(t, handler, "/sitemap.xml")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != cacheControlCurrentPage {
		t.Errorf("Cache-Control = %q, want %q", got, cacheControlCurrentPage)
	}
	var set sitemapURLSet
	if err := xml.Unmarshal(response.Body.Bytes(), &set); err != nil {
		t.Fatalf("parse sitemap: %v", err)
	}
	lastMod := map[string]string{}
	for _, entry := range set.URLs {
		path, ok := strings.CutPrefix(entry.Loc, "https://example.test/nwsl")
		if !ok || strings.Contains(path, "?") {
			t.Errorf("loc %q is not a canonical URL under the public URL", entry.Loc)
			continue
		}
		if _, dup := lastMod[path]; dup {
			t.Errorf("%s is listed twice", path)
		}
		lastMod[path] = entry.LastMod
	}

	for path, want := range map[string]string{
		"/seasons/2026/regular-season":                   "2026-07-09T20:00:00Z",
		"/seasons/2026/regular-season/fixtures":          "2026-07-09T20:00:00Z",
		"/seasons/2026/regular-season/forecast":          "2026-07-09T20:00:00Z",
		"/seasons/2026/regular-season/model-evaluation":  "2026-07-09T20:00:00Z",
		"/seasons/2026/regular-season/matches/completed": "2026-07-09T20:00:00Z",
		"/seasons/2026/regular-season/matches/future-1":  "2026-07-09T20:00:00Z",
		"/seasons/2025/regular-season":                   "",
		"/seasons/2025/regular-season/matches/completed": "",
		"/explore": "",
	} {
		got, ok := lastMod[path]
		if !ok {
			t.Errorf("%s is missing", path)
			continue
		}
		if want != "" && got != want {
			t.Errorf("%s lastmod = %q, want %q", path, got, want)
		}
	}
	// An earlier season's pages carry the date of its last match, not the
	// time of its last sync.
	if got := lastMod["/seasons/2025/regular-season"]; len(got) != len(time.DateOnly) {
		t.Errorf("2025 lastmod = %q, want a date", got)
	}
	// Stages without fixtures, such as the unscheduled playoffs, are left out.
	for path := range lastMod {
		if strings.HasPrefix(path, "/seasons/2026/playoffs") || strings.HasPrefix(path, "/seasons/2024/") {
			t.Errorf("%s is listed for a stage without fixtures", path)
		}
	}

	// Every listed page renders. Explore needs an archive this store lacks
	// and has tests of its own, but every view's page is listed.
	for _, group := range exploreViewGroups {
		for _, view := range group.Views {
			if _, ok := lastMod[exploreViewPath(view)]; !ok {
				t.Errorf("%s is missing", exploreViewPath(view))
			}
		}
	}
	for path := range lastMod {
		if strings.HasPrefix(path, "/explore") {
			continue
		}
		if code := get(t, handler, path).Code; code != http.StatusOK {
			t.Errorf("%s: status = %d", path, code)
		}
	}
}

func TestSitemapNeedsAPublicURL(t *testing.T) {
	handler := sitemapHandler("")
	if code := get(t, handler, "/sitemap.xml").Code; code != http.StatusNotFound {
		t.Errorf("sitemap status = %d, want 404", code)
	}
	if body := get(t, handler, "/robots.txt").Body.String(); strings.Contains(body, "Sitemap:") {
		t.Errorf("robots.txt names a sitemap without a public URL:\n%s", body)
	}
}

func TestRobotsNamesTheSitemap(t *testing.T) {
	body := get(t, sitemapHandler("https://example.test/nwsl/"), "/robots.txt").Body.String()
	if !strings.HasSuffix(body, "\nSitemap: https://example.test/nwsl/sitemap.xml\n") {
		t.Errorf("robots.txt does not end with the sitemap line:\n%s", body)
	}
}
