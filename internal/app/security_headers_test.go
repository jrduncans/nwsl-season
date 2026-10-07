package app

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestSecurityHeadersApplyToEveryResponseKind(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/seasons/2026/regular-season", http.StatusOK},
		{"/static/site.css", http.StatusOK},
		{"/healthz", http.StatusOK},
		{"/seasons/2026", http.StatusSeeOther},
		{"/explorer/seasons/2026/fixtures/", http.StatusSeeOther},
		{"/not-a-route", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.status {
			t.Errorf("%s status = %d, want %d", tc.path, response.Code, tc.status)
		}
		for name, want := range map[string]string{
			"Content-Security-Policy":      contentSecurityPolicy,
			"X-Content-Type-Options":       "nosniff",
			"X-Frame-Options":              "DENY",
			"Referrer-Policy":              "strict-origin-when-cross-origin",
			"Cross-Origin-Opener-Policy":   "same-origin",
			"Cross-Origin-Resource-Policy": "same-origin",
		} {
			if got := response.Header().Get(name); got != want {
				t.Errorf("%s %s = %q, want %q", tc.path, name, got, want)
			}
		}
	}
}

func TestContentSecurityPolicyRejectsInlineScriptAndUnlistedOrigins(t *testing.T) {
	for _, directive := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "img-src 'self' " + clubLogoOrigin, "frame-ancestors 'none'"} {
		if !strings.Contains(contentSecurityPolicy, directive) {
			t.Errorf("policy %q lacks %q", contentSecurityPolicy, directive)
		}
	}
	if strings.Contains(contentSecurityPolicy, "script-src 'self' ") || strings.Contains(contentSecurityPolicy, "unsafe-eval") {
		t.Errorf("policy %q loosens script execution", contentSecurityPolicy)
	}
}

// The browser silently drops anything the policy blocks, so keep templates and
// first-party scripts within it. A failure here means either the change should
// follow the existing pattern or contentSecurityPolicy needs a deliberate update.
func TestTemplatesAndScriptsStayWithinContentSecurityPolicy(t *testing.T) {
	scriptTag := regexp.MustCompile(`<script\b[^>]*>`)
	executableScript := regexp.MustCompile(`^<script src="\{\{[^}]+\}\}" defer>$`)
	eventHandler := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	absoluteResource := regexp.MustCompile(`\s(src|srcset|poster|data)="(https?:)?//`)
	err := fs.WalkDir(pageFiles, "templates", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := fs.ReadFile(pageFiles, path)
		if err != nil {
			return err
		}
		page := string(content)
		for _, tag := range scriptTag.FindAllString(page, -1) {
			if tag != `<script type="application/json">` && !strings.HasPrefix(tag, `<script type="application/json" `) && !executableScript.MatchString(tag) {
				t.Errorf("%s: %s is neither a JSON data block nor a same-origin script file", path, tag)
			}
		}
		if strings.Contains(page, "<script>") {
			t.Errorf("%s contains an inline script", path)
		}
		for _, pattern := range []struct {
			name  string
			match func(string) bool
		}{
			{"a <style> element", func(s string) bool { return strings.Contains(s, "<style") }},
			{"an inline event handler", eventHandler.MatchString},
			{"a javascript: URL", func(s string) bool { return strings.Contains(strings.ToLower(s), "javascript:") }},
			{"a third-party resource", absoluteResource.MatchString},
		} {
			if pattern.match(page) {
				t.Errorf("%s contains %s, which the Content Security Policy blocks", path, pattern.name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	blockedScriptAPI := regexp.MustCompile(`\beval\(|new Function\(|\bfetch\(|XMLHttpRequest|EventSource|WebSocket|sendBeacon`)
	for _, path := range []string{"static/explore.js", "static/standings.js"} {
		content, err := fs.ReadFile(pageFiles, path)
		if err != nil {
			t.Fatal(err)
		}
		if match := blockedScriptAPI.FindString(string(content)); match != "" {
			t.Errorf("%s uses %q, which the Content Security Policy blocks", path, match)
		}
	}
}

func TestRenderedImagesUseAllowedOrigins(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))
	sources := regexp.MustCompile(`<img [^>]*src="([^"]*)"`).FindAllStringSubmatch(response.Body.String(), -1)
	if len(sources) == 0 {
		t.Fatal("season page rendered no images to check")
	}
	for _, source := range sources {
		if value := source[1]; strings.Contains(value, "//") && !strings.HasPrefix(value, clubLogoOrigin+"/") {
			t.Errorf("image source %q is outside the Content Security Policy", value)
		}
	}
}
