package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
)

func TestRootIconsAreServed(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	for path, want := range map[string]string{
		"/favicon.ico":                      "image/x-icon",
		"/apple-touch-icon.png":             "image/png",
		"/apple-touch-icon-precomposed.png": "image/png",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, response.Code)
		}
		if got := response.Header().Get("Content-Type"); got != want {
			t.Errorf("%s Content-Type = %q, want %q", path, got, want)
		}
		if got := response.Header().Get("Cache-Control"); got != "public, max-age=86400" {
			t.Errorf("%s Cache-Control = %q", path, got)
		}
		if response.Body.Len() == 0 {
			t.Errorf("%s has an empty body", path)
		}

		revalidate := httptest.NewRequest(http.MethodGet, path, nil)
		revalidate.Header.Set("If-None-Match", response.Header().Get("ETag"))
		notModified := httptest.NewRecorder()
		handler.ServeHTTP(notModified, revalidate)
		if notModified.Code != http.StatusNotModified {
			t.Errorf("%s revalidation status = %d, want 304", path, notModified.Code)
		}
	}
}

// Every page links the icons with relative, versioned URLs that resolve to
// the static files from the page's own path, including under the legacy base
// path.
func TestPagesLinkIcons(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	link := regexp.MustCompile(`<link rel="(icon|apple-touch-icon)" href="([^"]+)"`)
	for _, page := range []string{"/seasons/2026/regular-season", "/seasons/2026/regular-season/fixtures", "/explorer/seasons/2026/regular-season"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, page, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", page, response.Code)
		}
		matches := link.FindAllStringSubmatch(response.Body.String(), -1)
		if len(matches) != 2 {
			t.Fatalf("%s has %d icon links, want 2", page, len(matches))
		}
		base, _ := url.Parse("https://nwsl.example" + page)
		for _, match := range matches {
			href, err := url.Parse(match[2])
			if err != nil {
				t.Fatalf("%s: %v", page, err)
			}
			resolved := base.ResolveReference(href)
			iconResponse := httptest.NewRecorder()
			handler.ServeHTTP(iconResponse, httptest.NewRequest(http.MethodGet, resolved.RequestURI(), nil))
			if iconResponse.Code != http.StatusOK {
				t.Errorf("%s: %s resolves to %s, status %d", page, match[2], resolved.RequestURI(), iconResponse.Code)
			}
			if got := iconResponse.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
				t.Errorf("%s: %s Cache-Control = %q, want the versioned static policy", page, resolved.RequestURI(), got)
			}
		}
	}
}

func TestStaticSibling(t *testing.T) {
	for stylesheet, want := range map[string]string{
		"../static/site.css?v=abc":    "../static/favicon.svg?v=" + staticVersions["favicon.svg"],
		"static/site.css?v=abc":       "static/favicon.svg?v=" + staticVersions["favicon.svg"],
		"../../../static/site.css?v=": "../../../static/favicon.svg?v=" + staticVersions["favicon.svg"],
		"":                            "",
	} {
		if got := staticSibling(stylesheet, "favicon.svg"); got != want {
			t.Errorf("staticSibling(%q) = %q, want %q", stylesheet, got, want)
		}
	}
}
