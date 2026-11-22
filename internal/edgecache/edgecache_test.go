package edgecache

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestCloudflarePurgeTagsSendsTagsToZone(t *testing.T) {
	var gotPath, gotAuthorization, gotContentType string
	var gotBody struct {
		Tags []string `json:"tags"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"messages":[],"result":{"id":"zone"}}`))
	}))
	defer server.Close()

	purger := Cloudflare{ZoneID: "zone123", Token: "test-token", BaseURL: server.URL + "/"}
	if err := purger.PurgeTags(context.Background(), []string{TagLive, TagPage}); err != nil {
		t.Fatalf("PurgeTags() error = %v", err)
	}
	if want := "POST /zones/zone123/purge_cache"; gotPath != want {
		t.Errorf("request = %q, want %q", gotPath, want)
	}
	if want := "Bearer test-token"; gotAuthorization != want {
		t.Errorf("Authorization = %q, want %q", gotAuthorization, want)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if want := []string{TagLive, TagPage}; !slices.Equal(gotBody.Tags, want) {
		t.Errorf("tags = %v, want %v", gotBody.Tags, want)
	}
}

func TestCloudflarePurgeTagsReportsFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"api errors", http.StatusForbidden, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`, "HTTP 403): 10000: Authentication error"},
		{"rate limited without JSON", http.StatusTooManyRequests, `slow down`, "HTTP 429): slow down"},
		{"unconfirmed success", http.StatusOK, `{"success":false,"errors":[]}`, "HTTP 200)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := Cloudflare{ZoneID: "zone", Token: "token", BaseURL: server.URL}.PurgeTags(context.Background(), []string{TagPage})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PurgeTags() error = %v, want it to contain %q", err, test.want)
			}
			if strings.Contains(err.Error(), "token") {
				t.Errorf("error %q must not contain the token", err)
			}
		})
	}
}

func TestCloudflarePurgeTagsNeedsCredentials(t *testing.T) {
	if err := (Cloudflare{ZoneID: "zone"}).PurgeTags(context.Background(), []string{TagPage}); err == nil {
		t.Error("PurgeTags() without a token succeeded")
	}
}

func TestCloudflarePurgeTagsWithoutTagsSendsNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unexpected purge request")
	}))
	defer server.Close()
	if err := (Cloudflare{ZoneID: "zone", Token: "token", BaseURL: server.URL}).PurgeTags(context.Background(), nil); err != nil {
		t.Fatalf("PurgeTags(nil) error = %v", err)
	}
}
