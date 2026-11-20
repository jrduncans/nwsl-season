package app

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var canonicalLinkPattern = regexp.MustCompile(`<link rel="canonical" href="([^"]*)">`)

// Every page names its own path, with the query string dropped, as
// canonical, so a shared scenario or selection link doesn't become another
// indexable page.
func TestPagesNameTheirOwnPathAsCanonical(t *testing.T) {
	for _, tc := range []struct{ target, relative string }{
		{"/seasons/2026/regular-season", "regular-season"},
		{"/seasons/2026/regular-season/fixtures", "fixtures"},
		{"/seasons/2026/regular-season/fixtures?fixture=future-1&outcome=home", "fixtures"},
		{"/seasons/2026/regular-season/schedule-difficulty?difficulty=venue", "schedule-difficulty"},
		{"/seasons/2026/regular-season/forecast?v=2&m=results-poisson-v1&c=current-pace-v1&team=alpha", "forecast"},
		{"/seasons/2026/regular-season/clinching?team=alpha", "clinching"},
		{"/seasons/2026/regular-season/model-evaluation", "model-evaluation"},
		{"/seasons/2026/regular-season/matches/future-1?h=1", "future-1"},
	} {
		for _, publicURL := range []string{"", "https://example.test/nwsl/"} {
			response := httptest.NewRecorder()
			handler := NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC, PublicURL: publicURL})
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.target, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("%s: status = %d", tc.target, response.Code)
			}
			want := tc.relative
			if publicURL != "" {
				want = "https://example.test/nwsl" + regexp.MustCompile(`\?.*`).ReplaceAllString(tc.target, "")
			}
			match := canonicalLinkPattern.FindStringSubmatch(response.Body.String())
			if match == nil || match[1] != want {
				t.Errorf("%s (public URL %q): canonical = %v, want href %q", tc.target, publicURL, match, want)
			}
		}
	}
}

func TestErrorPagesNameNoCanonical(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=99", nil))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `class="error-card"`) {
		t.Fatalf("status = %d, want the 400 error page", response.Code)
	}
	if match := canonicalLinkPattern.FindString(response.Body.String()); match != "" {
		t.Errorf("error page names a canonical link: %s", match)
	}
}
