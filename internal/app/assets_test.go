package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaticAssetsCacheByContentVersion(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	version := staticVersions["site.css"]
	for _, tc := range []struct {
		name, query, cacheControl string
	}{
		{"current version", "?v=" + version, "public, max-age=31536000, immutable"},
		{"stale version", "?v=000000000000", "no-cache"},
		{"unversioned", "", "no-cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/static/site.css"+tc.query, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.Code)
			}
			if got := response.Header().Get("Cache-Control"); got != tc.cacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, tc.cacheControl)
			}
			if got, want := response.Header().Get("ETag"), `"`+version+`"`; got != want {
				t.Errorf("ETag = %q, want %q", got, want)
			}
		})
	}

	request := httptest.NewRequest(http.MethodGet, "/static/site.css", nil)
	request.Header.Set("If-None-Match", `"`+version+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want 304", response.Code)
	}
}
