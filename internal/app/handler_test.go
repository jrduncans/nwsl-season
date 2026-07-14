package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
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

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "/seasons/2026" {
		t.Fatalf("location = %q, want current season", location)
	}
}

func TestSeasonRendersStandingsFixturesAndFreshness(t *testing.T) {
	store := fakeStore{season: testSeasonData()}
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", PlayoffPlaces: 1, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	for _, text := range []string{"2026 season", "Alpha &amp; Co", "Bravo FC", "2–1", "Build a what-if scenario", "Cache refreshed Jul 9, 2026", "Remaining schedule strength", "Raw opp PPG", "Venue-adjusted PPG", "4.50"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	if strings.Contains(response.Body.String(), "<script>alert") {
		t.Fatal("team name was not escaped")
	}
	if !strings.Contains(response.Body.String(), "Clinching is not evaluated") || strings.Contains(response.Body.String(), `class="badge"`) {
		t.Fatal("incomplete schedule produced misleading clinching indicators")
	}
}

func TestSeasonRendersStrengthEmptyStateWhenScheduleIsComplete(t *testing.T) {
	data := testSeasonData()
	data.Games = data.Games[:1]
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{PlayoffPlaces: 1, GamesPerTeam: 2, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "No remaining regular-season fixtures are present in the cache.") {
		t.Fatal("complete schedule did not render the strength empty state")
	}
	if strings.Contains(response.Body.String(), "Venue-adjusted PPG</th>") {
		t.Fatal("strength table rendered despite no remaining fixtures")
	}
}

func TestSeasonRouteReadsTemporarySQLiteCache(t *testing.T) {
	ctx := context.Background()
	db, err := cache.Open(ctx, t.TempDir()+"/season.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []cache.Team{
		{ASAID: "alpha", Name: "Alpha FC", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"},
		{ASAID: "bravo", Name: "Bravo FC", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"},
	}
	games := []cache.Game{
		{ASAID: "done", Season: "2026", Stage: "Regular Season", KickoffUTC: "2026-03-01 20:00:00 UTC", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: sql.NullInt64{Int64: 3, Valid: true}, AwayScore: sql.NullInt64{Int64: 1, Valid: true}, RawJSON: "{}"},
		{ASAID: "future", Season: "2026", Stage: "Regular Season", KickoffUTC: "2026-09-01 20:00:00 UTC", Status: "PreMatch", HomeTeamID: "bravo", AwayTeamID: "alpha", RawJSON: "{}"},
	}
	if _, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, games, time.Now()); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/seasons/2026", nil)
	response := httptest.NewRecorder()
	NewHandlerWithOptions(db, Options{PlayoffPlaces: 1, GamesPerTeam: 2, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Alpha FC") || !strings.Contains(response.Body.String(), "3–1") {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
}

func TestWhatIfFormCanonicalizesToShareableURL(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/what-if?g.future-2=&g.future-1=h", nil)
	response := httptest.NewRecorder()

	NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", response.Code)
	}
	if location := response.Header().Get("Location"); location != "/seasons/2026/what-if?p=future-1%3Ah&v=1" {
		t.Fatalf("location = %q, want canonical state URL", location)
	}
}

func TestWhatIfRendersProjectedTableAndSelectedFixture(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/what-if?v=1&p=future-1:h", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{PlayoffPlaces: 1, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"Hypothetical projection", "Projected standings", "1 hypothetical result applied", `value="h" selected`, "1–0 score"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
}

func TestWhatIfRejectsStaleOrInvalidState(t *testing.T) {
	for _, target := range []string{
		"/seasons/2026/what-if?v=2&p=future-1:h",
		"/seasons/2026/what-if?v=1&p=completed:h",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", target, response.Code)
		}
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
	reader := fakeStore{status: cache.Status{
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
			GamesInserted: 2,
			GamesUpdated:  3,
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
			GamesInserted: 2,
			GamesUpdated:  3,
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
	if lastSuccess["duration_ms"] != float64(1000) || lastSuccess["games_inserted"] != float64(2) || lastSuccess["games_updated"] != float64(3) {
		t.Fatalf("status metrics = %#v, want duration and row counts", lastSuccess)
	}
}

type fakeStore struct {
	status cache.Status
	season cache.SeasonData
	err    error
}

func (f fakeStore) Status(context.Context, string, string) (cache.Status, error) {
	return f.status, f.err
}

func (f fakeStore) Season(context.Context, string, string) (cache.SeasonData, error) {
	return f.season, f.err
}

func testSeasonData() cache.SeasonData {
	now := time.Date(2026, 7, 9, 20, 0, 0, 0, time.UTC)
	data := cache.SeasonData{
		Teams: []standings.Team{
			{ID: "alpha", Name: "Alpha & Co <script>alert(1)</script>"},
			{ID: "bravo", Name: "Bravo FC"},
		},
		LastSuccess: &cache.SyncRun{Season: "2026", Stage: "Regular Season", FinishedAt: now},
	}
	data.Games = append(data.Games, cache.Game{
		ASAID: "completed", Season: "2026", Stage: "Regular Season", KickoffUTC: "2026-07-01 19:00:00 UTC",
		Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo",
		HomeScore: sql.NullInt64{Int64: 2, Valid: true}, AwayScore: sql.NullInt64{Int64: 1, Valid: true}, Matchday: sql.NullInt64{Int64: 1, Valid: true},
	})
	for index := 1; index <= 5; index++ {
		data.Games = append(data.Games, cache.Game{
			ASAID: fmt.Sprintf("future-%d", index), Season: "2026", Stage: "Regular Season",
			KickoffUTC: fmt.Sprintf("2026-07-%02d 19:00:00 UTC", 10+index), Status: "PreMatch",
			HomeTeamID: "alpha", AwayTeamID: "bravo", Matchday: sql.NullInt64{Int64: int64(index + 1), Valid: true},
		})
	}
	return data
}
