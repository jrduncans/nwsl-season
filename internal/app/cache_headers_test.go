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
	}{
		{http.MethodGet, "/seasons/2026/regular-season", http.StatusOK, cacheControlCurrentPage},
		{http.MethodHead, "/seasons/2026/regular-season", http.StatusOK, cacheControlCurrentPage},
		{http.MethodGet, "/seasons/2026", http.StatusSeeOther, cacheControlNoStore},
		{http.MethodGet, "/explorer/seasons/2026/fixtures/", http.StatusSeeOther, cacheControlNoStore},
		{http.MethodGet, "/not-a-route", http.StatusNotFound, cacheControlNoStore},
		{http.MethodGet, "/healthz", http.StatusOK, cacheControlNoStore},
		{http.MethodGet, "/cache/status", http.StatusOK, cacheControlNoStore},
		{http.MethodGet, "/robots.txt", http.StatusOK, cacheControlNoStore},
		// Versioned static files keep the handler's own policy.
		{http.MethodGet, "/" + staticURL("/", "site.css"), http.StatusOK, "public, max-age=31536000, immutable"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, response.Code, tc.status)
		}
		if got := response.Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s %s Cache-Control = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestCacheControlFor(t *testing.T) {
	const html = "text/html; charset=utf-8"
	for _, tc := range []struct {
		name        string
		method      string
		path        string
		status      int
		contentType string
		want        string
	}{
		{"current season page", http.MethodGet, "/seasons/2026/playoffs/matches/abc", http.StatusOK, html, cacheControlCurrentPage},
		{"past season page", http.MethodGet, "/seasons/2021/regular-season/matches/abc", http.StatusOK, html, cacheControlPastSeasonPage},
		{"past season under legacy base path", http.MethodGet, "/explorer/seasons/2019/regular-season", http.StatusOK, html, cacheControlPastSeasonPage},
		{"future season page", http.MethodGet, "/seasons/2027/regular-season", http.StatusOK, html, cacheControlCurrentPage},
		{"page outside a season", http.MethodGet, "/explore", http.StatusOK, html, cacheControlCurrentPage},
		{"malformed season", http.MethodGet, "/seasons/20x1/regular-season", http.StatusOK, html, cacheControlCurrentPage},
		{"past season error page", http.MethodGet, "/seasons/2021/regular-season", http.StatusInternalServerError, html, cacheControlNoStore},
		{"past season redirect", http.MethodGet, "/seasons/2021", http.StatusSeeOther, html, cacheControlNoStore},
		{"non-HTML success", http.MethodGet, "/cache/status", http.StatusOK, "application/json", cacheControlNoStore},
		{"non-GET request", http.MethodPost, "/seasons/2026/regular-season", http.StatusOK, html, cacheControlNoStore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			if got := cacheControlFor(request, tc.status, tc.contentType, "2026"); got != tc.want {
				t.Errorf("cacheControlFor = %q, want %q", got, tc.want)
			}
		})
	}
}

// A handler that writes a body without calling WriteHeader still gets a
// policy, chosen from the sniffed content type.
func TestCacheHeadersWithImplicitHeader(t *testing.T) {
	handler := withCacheHeaders("2026", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>t</title>"))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2018/regular-season", nil))
	if got := response.Header().Get("Cache-Control"); got != cacheControlPastSeasonPage {
		t.Errorf("Cache-Control = %q, want %q", got, cacheControlPastSeasonPage)
	}
}
