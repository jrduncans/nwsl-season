package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	NewHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); body != "ok\n" {
		t.Fatalf("body = %q, want %q", body, "ok\n")
	}
}

func TestHome(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	NewHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "NWSL season explorer") {
		t.Fatal("home page does not contain the site name")
	}
}

func TestCacheStatusWithoutReader(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/cache/status", nil)
	response := httptest.NewRecorder()

	NewHandler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(response.Body.String(), "cache status unavailable") {
		t.Fatalf("body = %q, want unavailable message", response.Body.String())
	}
}

func TestCacheStatusWithLastSuccessfulSync(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	reader := fakeStatusReader{status: cache.Status{
		LastAttempt: &cache.SyncRun{
			ID:            1,
			StartedAt:     now,
			FinishedAt:    now.Add(time.Second),
			Season:        "2026",
			Stage:         "Regular Season",
			Outcome:       "success",
			TeamsUpserted: 14,
			GamesUpserted: 182,
			GamesSeen:     182,
		},
		LastSuccess: &cache.SyncRun{
			ID:            1,
			StartedAt:     now,
			FinishedAt:    now.Add(time.Second),
			Season:        "2026",
			Stage:         "Regular Season",
			Outcome:       "success",
			TeamsUpserted: 14,
			GamesUpserted: 182,
			GamesSeen:     182,
		},
	}}

	request := httptest.NewRequest(http.MethodGet, "/cache/status", nil)
	response := httptest.NewRecorder()

	NewHandler(reader).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v, want true", body["ok"])
	}
	lastSuccess, ok := body["last_success"].(map[string]any)
	if !ok {
		t.Fatalf("last_success = %#v, want object", body["last_success"])
	}
	if lastSuccess["season"] != "2026" {
		t.Fatalf("season = %v, want 2026", lastSuccess["season"])
	}
}

type fakeStatusReader struct {
	status cache.Status
	err    error
}

func (f fakeStatusReader) Status(context.Context) (cache.Status, error) {
	return f.status, f.err
}
