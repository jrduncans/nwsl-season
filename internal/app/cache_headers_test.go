package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCacheHeadersByResponseKind(t *testing.T) {
	handler := NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{CurrentSeason: "2026", Location: time.UTC})
	for _, tc := range []struct {
		method string
		path   string
		status int
		want   string
		tags   string
	}{
		{http.MethodGet, "/seasons/2026/regular-season", http.StatusOK, cacheControlCurrentPage, "nwsl-page,nwsl-live"},
		{http.MethodHead, "/seasons/2026/regular-season", http.StatusOK, cacheControlCurrentPage, "nwsl-page,nwsl-live"},
		{http.MethodGet, "/seasons/2026", http.StatusSeeOther, cacheControlNoStore, ""},
		{http.MethodGet, "/explorer/seasons/2026/fixtures/", http.StatusSeeOther, cacheControlNoStore, ""},
		{http.MethodGet, "/not-a-route", http.StatusNotFound, cacheControlNoStore, ""},
		{http.MethodGet, "/healthz", http.StatusOK, cacheControlNoStore, ""},
		{http.MethodGet, "/cache/status", http.StatusOK, cacheControlNoStore, ""},
		{http.MethodGet, "/robots.txt", http.StatusOK, cacheControlNoStore, ""},
		// Versioned static files keep the handler's own policy and need no
		// purge.
		{http.MethodGet, "/" + staticURL("/", "site.css"), http.StatusOK, "public, max-age=31536000, immutable", ""},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, response.Code, tc.status)
		}
		if got := response.Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s %s Cache-Control = %q, want %q", tc.method, tc.path, got, tc.want)
		}
		if got := response.Header().Get("Cache-Tag"); got != tc.tags {
			t.Errorf("%s %s Cache-Tag = %q, want %q", tc.method, tc.path, got, tc.tags)
		}
	}
}

// With edge cache purging on, pages keep their tags and stay cached longer.
func TestCacheHeadersWithEdgeCachePurge(t *testing.T) {
	handler := NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{CurrentSeason: "2026", Location: time.UTC, EdgeCachePurge: true})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))
	if got := response.Header().Get("Cache-Control"); got != cacheControlPurgedCurrentPage {
		t.Errorf("Cache-Control = %q, want %q", got, cacheControlPurgedCurrentPage)
	}
	if got, want := response.Header().Get("Cache-Tag"), "nwsl-page,nwsl-live"; got != want {
		t.Errorf("Cache-Tag = %q, want %q", got, want)
	}
}

func TestCachePolicyHeaders(t *testing.T) {
	for _, tc := range []struct {
		name   string
		purged bool
		kind   pageCache
		want   string
		tags   string
	}{
		{"live", false, pageLive, cacheControlCurrentPage, "nwsl-page,nwsl-live"},
		{"live purged", true, pageLive, cacheControlPurgedCurrentPage, "nwsl-page,nwsl-live"},
		{"past season", false, pagePastSeason, cacheControlPastSeasonPage, "nwsl-page"},
		{"past season purged", true, pagePastSeason, cacheControlPurgedPastSeasonPage, "nwsl-page"},
		{"not cached", true, pageNotCached, cacheControlNoStore, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{}
			cachePolicy{currentSeason: "2026", purged: tc.purged}.setHeaders(header, tc.kind)
			if got := header.Get("Cache-Control"); got != tc.want {
				t.Errorf("Cache-Control = %q, want %q", got, tc.want)
			}
			if got := header.Get("Cache-Tag"); got != tc.tags {
				t.Errorf("Cache-Tag = %q, want %q", got, tc.tags)
			}
		})
	}
}

func TestPageCacheFor(t *testing.T) {
	const html = "text/html; charset=utf-8"
	for _, tc := range []struct {
		name        string
		method      string
		path        string
		status      int
		contentType string
		want        pageCache
	}{
		{"current season page", http.MethodGet, "/seasons/2026/playoffs/matches/abc", http.StatusOK, html, pageLive},
		{"past season page", http.MethodGet, "/seasons/2021/regular-season/matches/abc", http.StatusOK, html, pagePastSeason},
		{"past season under legacy base path", http.MethodGet, "/explorer/seasons/2019/regular-season", http.StatusOK, html, pagePastSeason},
		{"future season page", http.MethodGet, "/seasons/2027/regular-season", http.StatusOK, html, pageLive},
		{"page outside a season", http.MethodGet, "/explore", http.StatusOK, html, pageLive},
		{"malformed season", http.MethodGet, "/seasons/20x1/regular-season", http.StatusOK, html, pageLive},
		{"past season error page", http.MethodGet, "/seasons/2021/regular-season", http.StatusInternalServerError, html, pageNotCached},
		{"past season redirect", http.MethodGet, "/seasons/2021", http.StatusSeeOther, html, pageNotCached},
		{"non-HTML success", http.MethodGet, "/cache/status", http.StatusOK, "application/json", pageNotCached},
		{"non-GET request", http.MethodPost, "/seasons/2026/regular-season", http.StatusOK, html, pageNotCached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			if got := pageCacheFor(request, tc.status, tc.contentType, "2026"); got != tc.want {
				t.Errorf("pageCacheFor = %d, want %d", got, tc.want)
			}
		})
	}
}

// A handler that writes a body without calling WriteHeader still gets a
// policy, chosen from the sniffed content type.
func TestCacheHeadersWithImplicitHeader(t *testing.T) {
	handler := withCacheHeaders(cachePolicy{currentSeason: "2026"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>t</title>"))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2018/regular-season", nil))
	if got := response.Header().Get("Cache-Control"); got != cacheControlPastSeasonPage {
		t.Errorf("Cache-Control = %q, want %q", got, cacheControlPastSeasonPage)
	}
	if got := response.Header().Get("Cache-Tag"); got != "nwsl-page" {
		t.Errorf("Cache-Tag = %q, want nwsl-page", got)
	}
}
