package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/app"
	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestPreviewNoScriptPreservesResponseAndRestrictsCSP(t *testing.T) {
	db, err := cache.Open(context.Background(), filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	apptest.Seed(t, db, apptest.ScenarioDefault)
	application := app.NewHandler(db)
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/history/scoring", http.StatusOK},
		{"/static/site.css", http.StatusOK},
		{"/seasons/2026", http.StatusSeeOther},
		{"/not-a-route", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, mountPrefix+tc.path, nil)
			normal, blocked := httptest.NewRecorder(), httptest.NewRecorder()
			previewHandler(application, false).ServeHTTP(normal, request)
			previewHandler(application, true).ServeHTTP(blocked, request)
			if normal.Code != tc.status || blocked.Code != tc.status {
				t.Fatalf("status = %d / %d, want %d", normal.Code, blocked.Code, tc.status)
			}
			// Result reads committed headers, not the mutable Header map.
			normalResponse, blockedResponse := normal.Result(), blocked.Result()
			defer func() { _ = normalResponse.Body.Close() }()
			defer func() { _ = blockedResponse.Body.Close() }()
			policies := normalResponse.Header.Values("Content-Security-Policy")
			if len(policies) != 1 || policies[0] == "" {
				t.Fatalf("normal preview CSP = %v", policies)
			}
			want := append(slices.Clone(policies), "script-src 'none'")
			if got := blockedResponse.Header.Values("Content-Security-Policy"); !slices.Equal(got, want) {
				t.Errorf("no-script CSP = %v, want %v", got, want)
			}
			for _, header := range []string{"Content-Type", "Location", "X-Content-Type-Options"} {
				if blockedResponse.Header.Get(header) != normalResponse.Header.Get(header) {
					t.Errorf("no-script changed %s", header)
				}
			}
			if blocked.Body.String() != normal.Body.String() {
				t.Error("no-script changed the response body")
			}
		})
	}
}
