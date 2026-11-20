package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPagesCarrySocialSharingTags(t *testing.T) {
	handler := NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{CurrentSeason: "2026", Location: time.UTC, PublicURL: "https://nwsl.example.test/"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	const description = "2026 NWSL regular-season table with points, goal difference and expected goals for all 2 teams."
	requireAttr(t, body, `meta[name="description"]`, "content", description)
	requireAttr(t, body, `meta[property="og:description"]`, "content", description)
	requireAttr(t, body, `meta[property="og:site_name"]`, "content", "NWSL by the Numbers")
	requireAttr(t, body, `meta[property="og:title"]`, "content", "2026 NWSL standings · NWSL by the Numbers")
	requireAttr(t, body, `meta[property="og:image"]`, "content", "https://nwsl.example.test/static/share.png?v="+staticVersions[shareImageName])
	requireAttr(t, body, `meta[name="twitter:card"]`, "content", "summary_large_image")

	image := httptest.NewRecorder()
	handler.ServeHTTP(image, httptest.NewRequest(http.MethodGet, "/static/share.png?v="+staticVersions[shareImageName], nil))
	if image.Code != http.StatusOK || image.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("share image = %d %q", image.Code, image.Header().Get("Content-Type"))
	}
	if got := image.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
		t.Fatalf("share image Cross-Origin-Resource-Policy = %q, want cross-origin", got)
	}
	stylesheet := httptest.NewRecorder()
	handler.ServeHTTP(stylesheet, httptest.NewRequest(http.MethodGet, "/static/site.css", nil))
	if got := stylesheet.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Fatalf("stylesheet Cross-Origin-Resource-Policy = %q, want same-origin", got)
	}
}

func TestSocialSharingTagsOmitImageWithoutPublicURL(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	forbidElements(t, body, `meta[property="og:image"]`)
	requireAttr(t, body, `meta[name="twitter:card"]`, "content", "summary")
}
