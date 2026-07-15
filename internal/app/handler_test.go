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
	if location := response.Header().Get("Location"); location != "seasons/2026" {
		t.Fatalf("location = %q, want current season", location)
	}
}

func TestRenderedHTTPPathsAreRelative(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026", nil)
	response := httptest.NewRecorder()

	NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, absolutePath := range []string{`href="/`, `src="/`, `href="/static/`, `src="/static/`} {
		if strings.Contains(body, absolutePath) {
			t.Fatalf("body contains absolute HTTP path %q", absolutePath)
		}
	}
	for _, relativePath := range []string{`href="2026/fixtures"`, `href="2026/schedule-difficulty"`, `href="2026/forecast"`, `href="../static/site.css"`, `src="../static/standings.js"`} {
		if !strings.Contains(body, relativePath) {
			t.Errorf("body does not contain relative path %q", relativePath)
		}
	}
}

func TestHandlerSupportsPreservedReverseProxyBasePath(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})

	rootRequest := httptest.NewRequest(http.MethodGet, "/explorer/", nil)
	rootResponse := httptest.NewRecorder()
	handler.ServeHTTP(rootResponse, rootRequest)
	if rootResponse.Code != http.StatusSeeOther || rootResponse.Header().Get("Location") != "seasons/2026" {
		t.Fatalf("base-path root = status %d, location %q", rootResponse.Code, rootResponse.Header().Get("Location"))
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026", nil)
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("base-path season status = %d, want 200", pageResponse.Code)
	}

	staticRequest := httptest.NewRequest(http.MethodGet, "/explorer/static/site.css", nil)
	staticResponse := httptest.NewRecorder()
	handler.ServeHTTP(staticResponse, staticRequest)
	if staticResponse.Code != http.StatusOK {
		t.Fatalf("base-path static status = %d, want 200", staticResponse.Code)
	}
}

func TestTrailingSlashSeasonPathRedirectsToCanonicalRelativePath(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	request := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026/?v=1", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", response.Code)
	}
	if location := response.Header().Get("Location"); location != "../2026?v=1" {
		t.Fatalf("location = %q, want relative canonical path", location)
	}
}

func TestTrailingSlashFixturesPathRedirectsToCanonicalRelativePath(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	request := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026/fixtures/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", response.Code)
	}
	if location := response.Header().Get("Location"); location != "../fixtures" {
		t.Fatalf("location = %q, want relative canonical path", location)
	}
}

func TestTrailingSlashScheduleDifficultyPathRedirectsToCanonicalRelativePath(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	request := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026/schedule-difficulty/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", response.Code)
	}
	if location := response.Header().Get("Location"); location != "../schedule-difficulty" {
		t.Fatalf("location = %q, want relative canonical path", location)
	}
}

func TestSeasonRendersStandingsAndFreshness(t *testing.T) {
	store := fakeStore{season: testSeasonData()}
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", PlayoffPlaces: 1, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	for _, text := range []string{"2026 season", "Alpha &amp; Co", "Bravo FC", "Forecast Lab", "Results and fixtures", "Schedule difficulty", "Cache refreshed Jul 9, 2026", ">Ahead</th>", "Harder"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	if strings.Contains(response.Body.String(), "Remaining schedule difficulty") || strings.Contains(response.Body.String(), "Toughest remaining schedule") {
		t.Fatal("main season page still renders the prominent schedule-difficulty summary")
	}
	for _, text := range []string{`data-standings-mode="per-game"`, ">Per game</button>", ">Totals</button>", "<th>GF</th>", `data-total="2" data-per-game="2.00"`} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain default per-game standings control %q", text)
		}
	}
	for _, logo := range []string{
		`src="https://american-soccer-analysis-headshots.s3.amazonaws.com/club_logos/alpha.png"`,
		`src="https://american-soccer-analysis-headshots.s3.amazonaws.com/club_logos/bravo.png"`,
	} {
		if got := strings.Count(response.Body.String(), logo); got != 1 {
			t.Errorf("%s appears %d times, want 1 in the standings", logo, got)
		}
	}
	if strings.Contains(response.Body.String(), "2–1") {
		t.Fatal("season page still renders fixture results")
	}
	if strings.Contains(response.Body.String(), "<script>alert") {
		t.Fatal("team name was not escaped")
	}
	if !strings.Contains(response.Body.String(), "Clinching is not evaluated") || strings.Contains(response.Body.String(), `class="badge"`) {
		t.Fatal("incomplete schedule produced misleading clinching indicators")
	}
}

func TestFixturesRendersResultsOnSeparatePage(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/fixtures", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{PlayoffPlaces: 1, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	for _, text := range []string{"Results and fixtures", "2–1", "Matchday 1", "Scheduled", `href="../2026"`, `href="forecast"`} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
}

func TestAddTotalPositionsUsesTotalStandingsOrder(t *testing.T) {
	rows := []tableRowView{{TeamID: "alpha", Position: 1, PlayoffLine: true}, {TeamID: "bravo", Position: 2}}
	totals := []standings.TableRow{{Team: standings.Team{ID: "bravo"}}, {Team: standings.Team{ID: "alpha"}}}

	got := addTotalPositions(rows, totals, 1)
	if got[0].TotalPosition != 2 || got[0].TotalPlayoffLine {
		t.Fatalf("alpha total placement = %#v, want second and below playoff line", got[0])
	}
	if got[1].TotalPosition != 1 || !got[1].TotalPlayoffLine {
		t.Fatalf("bravo total placement = %#v, want first and on playoff line", got[1])
	}
}

func TestPlotPositionLeavesVisualMarginAtTrackEdges(t *testing.T) {
	if got := plotPosition(10, 0, 10); got != "95.0" {
		t.Fatalf("maximum plot position = %q, want 95.0", got)
	}
	if got := plotPosition(0, 0, 10); got != "5.0" {
		t.Fatalf("minimum plot position = %q, want 5.0", got)
	}
}

func TestSeasonKeepsScheduleIndicatorWhenScheduleIsComplete(t *testing.T) {
	data := testSeasonData()
	data.Games = data.Games[:1]
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{PlayoffPlaces: 1, GamesPerTeam: 2, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), ">Ahead</th>") || !strings.Contains(response.Body.String(), `aria-label="Schedule ahead unavailable"`) {
		t.Fatal("complete schedule did not render unavailable schedule indicators")
	}
	if strings.Contains(response.Body.String(), "Remaining schedule difficulty") {
		t.Fatal("main season page rendered schedule-difficulty detail")
	}
}

func TestScheduleDifficultyRendersComparisonAndFixtureDetails(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/schedule-difficulty", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{PlayoffPlaces: 1, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"Remaining schedule difficulty", "Toughest remaining schedule", "Easiest remaining schedule", "Venue-adjusted comparison", "Compare raw opponent PPG", "Team and fixture detail", "Raw opponent PPG", "Adjusted contribution", "Home", "Away", "Alpha &amp; Co"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	if !strings.Contains(response.Body.String(), `class="comparison-disclosure"`) || !strings.Contains(response.Body.String(), `class="team-schedule-detail"`) {
		t.Fatal("schedule detail does not use native disclosures")
	}
}

func TestScheduleDifficultyPreservesUnavailableFixtureDetails(t *testing.T) {
	data := testSeasonData()
	data.Games = data.Games[1:2]
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/schedule-difficulty", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{PlayoffPlaces: 1, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"Schedule difficulty is unavailable", "Remaining fixtures for Alpha &amp; Co", "Bravo FC", "Unavailable"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
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

	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/fixtures", nil)
	response := httptest.NewRecorder()
	NewHandlerWithOptions(db, Options{PlayoffPlaces: 1, GamesPerTeam: 2, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Alpha FC") || !strings.Contains(response.Body.String(), "3–1") {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
}

func TestClubLogoURLPathEscapesTeamID(t *testing.T) {
	if got, want := clubLogoURL("team/id ?"), clubLogoBaseURL+"team%2Fid%20%3F.png"; got != want {
		t.Fatalf("clubLogoURL = %q, want %q", got, want)
	}
	if got := clubLogoURL(""); got != "" {
		t.Fatalf("clubLogoURL for empty team ID = %q, want empty", got)
	}
}

func TestForecastRendersDefaultUncertaintyAndMetadata(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{PlayoffPlaces: 1, ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"Forecast Lab", "Results Poisson", "results-poisson-v1", "Simulated seasons", ">20</dd>", "Expected points", "Playoffs", "Shield", "View positions", "Add a result", "Data cutoff"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	if strings.Contains(response.Body.String(), "Build a what-if scenario") {
		t.Fatal("Forecast Lab still uses legacy visible navigation")
	}
}

func TestForecastAddResultRedirectsToCanonicalState(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast?v=1&m=results-poisson-v1&p=future-2:d&action=add&fixture=future-1&outcome=h", nil)
	response := httptest.NewRecorder()

	NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", response.Code)
	}
	if got, want := response.Header().Get("Location"), "forecast?m=results-poisson-v1&p=future-1%3Ah&p=future-2%3Ad&v=1"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
}

func TestForecastPreservesReverseProxyBasePath(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026/forecast", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{PlayoffPlaces: 1, ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), `href="/`) || !strings.Contains(response.Body.String(), `href="../2026"`) {
		t.Fatalf("forecast page does not preserve relative base-path links: %s", response.Body.String())
	}
}

func TestForecastRejectsStaleStateAndUnknownModel(t *testing.T) {
	for _, target := range []string{
		"/seasons/2026/forecast?v=1&m=results-poisson-v1&p=completed:h",
		"/seasons/2026/forecast?v=1&m=other&p=future-1:h",
	} {
		response := httptest.NewRecorder()
		NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", target, response.Code)
		}
	}
}

func TestRemovedWhatIfRouteReturnsNotFound(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/what-if", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
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
