package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

func TestHistoryScoringRendersOneArchiveReadAndNoSeasonReads(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2019": {lifecycle: cache.SourceScopeCompleted, goals: 3},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 2},
	})}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/history/scoring", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if store.archiveCalls != 1 || store.seasonCalls != 0 || store.statusCalls != 0 {
		t.Fatalf("archive=%d season=%d status=%d; want 1/0/0", store.archiveCalls, store.seasonCalls, store.statusCalls)
	}
	body := response.Body.String()
	for _, want := range []string{
		"<h1>Scoring by season</h1>", "History · League trends", "Regular seasons since 2016 in the available archive",
		"The NWSL did not hold a regular season in 2020", "20 completed, valid matches", "<caption>Regular-season scoring data in the available archive</caption>",
		"<th scope=\"col\">Goals per match</th>", "<th scope=\"row\"><a href=\"scoring?season=2019\">2019</a></th>",
		">60</td><td>3.00</td>", "Active through 20 matches", "Cached matches; inventory unverified", "<details class=\"history-data\" open>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("history page missing %q", want)
		}
	}
	if strings.Contains(body, "all-time") || strings.Contains(body, "fake chart") {
		t.Fatal("history page made an unsupported claim or rendered a placeholder")
	}
}

func TestHistoryRouteAndProxyLinksResolveWithinMount(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{"2024": {lifecycle: cache.SourceScopeCompleted, goals: 2}})}
	handler := NewHandler(store)
	for _, test := range []struct {
		path, wantLocation string
	}{
		{"/history?season=2024", "history/scoring?season=2024"},
		{"/nwsl-season/history?season=2024", "history/scoring?season=2024"},
		{"/history/?season=2024", "../history?season=2024"},
		{"/nwsl-season/history/?season=2024", "../history?season=2024"},
		{"/history/scoring/?season=2024", "../scoring?season=2024"},
		{"/nwsl-season/history/scoring/?season=2024", "../scoring?season=2024"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != test.wantLocation {
				t.Fatalf("status=%d location=%q, want 303 %q", response.Code, response.Header().Get("Location"), test.wantLocation)
			}
		})
	}

	for _, pagePath := range []string{"/history/scoring", "/nwsl-season/history/scoring"} {
		t.Run(pagePath, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, pagePath, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertHistoryPageURLs(t, pagePath, response.Body.String())
		})
	}
	for _, archivePath := range []string{"/seasons", "/nwsl-season/seasons"} {
		t.Run(archivePath, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, archivePath, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertHistoryURLStaysMounted(t, archivePath, attributeValue(t, response.Body.String(), `href="`, `"`, "Explore history"))
		})
	}

	for _, path := range []string{"/history/unknown", "/nwsl-season/history/unknown"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status=%d, want 404", path, response.Code)
		}
	}
	for _, path := range []string{"/history", "/history/scoring", "/nwsl-season/history", "/nwsl-season/history/scoring"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s status=%d, want 405", path, response.Code)
		}
	}
	for _, path := range []string{"/seasons/2026", "/nwsl-season/seasons/2026"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusSeeOther {
			t.Errorf("existing season route %s status=%d, want 303", path, response.Code)
		}
	}
}

func TestHistorySelectionAndErrorPaths(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2016": {lifecycle: cache.SourceScopeCompleted, inventory: cache.InventoryCompletenessIncomplete, goals: 4},
		"2024": {lifecycle: cache.SourceScopeCompleted, goals: 2},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 3},
	})}
	handler := NewHandler(store)
	for _, test := range []struct{ path, want string }{
		{"/history/scoring", "<h2 id=\"selected-season-heading\">2024</h2>"},
		{"/history/scoring?season=2016", "<h2 id=\"selected-season-heading\">2016</h2>"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s = %d %q; want %q", test.path, response.Code, response.Body.String(), test.want)
		}
		if !strings.Contains(response.Body.String(), "Currently eligible for comparison: 2024, 2026.") {
			t.Fatalf("%s changed the comparison population: %s", test.path, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), "Excluded from comparison:") || !strings.Contains(response.Body.String(), "<strong>2016</strong> — known fixture inventory incomplete") {
			t.Fatalf("%s omitted the excluded-season summary: %s", test.path, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `href="scoring?season=2024"`) || !strings.Contains(response.Body.String(), ">40</td><td>2.00</td>") {
			t.Fatalf("%s changed non-selected 2024 aggregate: %s", test.path, response.Body.String())
		}
	}
	for _, path := range []string{
		"/history/scoring?season=", "/history/scoring?season=202", "/history/scoring?season=2020",
		"/history/scoring?season=2016&season=2024", "/history/scoring?season=2016;bad=value",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Invalid history selection") {
			t.Errorf("%s = %d %q, want useful 400", path, response.Code, response.Body.String())
		}
	}
	ignored := httptest.NewRecorder()
	handler.ServeHTTP(ignored, httptest.NewRequest(http.MethodGet, "/history/scoring?source=archive&unexpected=1", nil))
	if ignored.Code != http.StatusOK {
		t.Fatalf("unrelated query keys = %d %q, want 200", ignored.Code, ignored.Body.String())
	}
	malformedIgnored := httptest.NewRecorder()
	handler.ServeHTTP(malformedIgnored, httptest.NewRequest(http.MethodGet, "/history/scoring?source=archive;unexpected=1", nil))
	if malformedIgnored.Code != http.StatusOK {
		t.Fatalf("malformed unrelated query = %d %q, want 200", malformedIgnored.Code, malformedIgnored.Body.String())
	}
	validWithMalformedUnrelated := httptest.NewRecorder()
	handler.ServeHTTP(validWithMalformedUnrelated, httptest.NewRequest(http.MethodGet, "/history/scoring?season=2024&note=%ZZ", nil))
	if validWithMalformedUnrelated.Code != http.StatusOK || !strings.Contains(validWithMalformedUnrelated.Body.String(), "<h2 id=\"selected-season-heading\">2024</h2>") {
		t.Fatalf("valid selection with malformed unrelated query = %d %q, want selected 2024", validWithMalformedUnrelated.Code, validWithMalformedUnrelated.Body.String())
	}

	unsupported := httptest.NewRecorder()
	NewHandler(fakeStore{}).ServeHTTP(unsupported, httptest.NewRequest(http.MethodGet, "/history/scoring", nil))
	if unsupported.Code != http.StatusServiceUnavailable || !strings.Contains(unsupported.Body.String(), "local archive") {
		t.Fatalf("unsupported store = %d %q", unsupported.Code, unsupported.Body.String())
	}
	duplicate := historyArchive(t, map[string]historyArchiveState{"2016": {lifecycle: cache.SourceScopeCompleted, goals: 2}})
	duplicate = append(duplicate, duplicate[0])
	for _, store := range []*historyHTTPStore{
		{err: errors.New("SELECT secret_token FROM source")},
		{archive: duplicate},
	} {
		response := httptest.NewRecorder()
		NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/history/scoring", nil))
		if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "secret_token") || strings.Contains(response.Body.String(), "SELECT") {
			t.Errorf("failure = %d %q; want safe 500", response.Code, response.Body.String())
		}
	}
}

func TestHistoryReadsTemporarySQLiteCache(t *testing.T) {
	ctx := context.Background()
	db, err := cache.Open(ctx, t.TempDir()+"/history.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	empty := httptest.NewRecorder()
	NewHandler(db).ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/history/scoring", nil))
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), "Source data unavailable") || !strings.Contains(empty.Body.String(), ">2016</a></th>") {
		t.Fatalf("empty SQLite history = %d %s", empty.Code, empty.Body.String())
	}
	teams := []cache.Team{{ASAID: "alpha", Name: "Alpha", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"}, {ASAID: "bravo", Name: "Bravo", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"}}
	games := historyGames("2024", 20, 3)
	if _, err := db.ReplaceSeason(ctx, "2024", "Regular Season", teams, games, time.Now()); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewHandler(db).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/history/scoring?season=2024", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<h2 id=\"selected-season-heading\">2024</h2>") || !strings.Contains(response.Body.String(), ">60</td><td>3.00</td>") {
		t.Fatalf("temporary SQLite history = %d %s", response.Code, response.Body.String())
	}
	assertHistoryCatalogRows(t, response.Body.String())
}

func TestHistoryRendersInvalidAndIncompleteHistoricalExclusions(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2016": {lifecycle: cache.SourceScopeCompleted, goals: 2},
		"2017": {lifecycle: cache.SourceScopeCompleted, goals: 2},
	})
	for index := range archive {
		switch archive[index].Entry.Season {
		case "2016":
			archive[index].Data.Games[0].HomeScore = sql.NullInt64{}
		case "2017":
			archive[index].Data.Games[0].Status = fixtures.PreMatchStatus
		}
	}
	response := httptest.NewRecorder()
	NewHandler(&historyHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/history/scoring", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{
		"<strong>2016</strong> — invalid completed results", "<strong>2017</strong> — historical results incomplete",
		"invalid completed results", "historical results incomplete",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("history exclusion copy missing %q", want)
		}
	}
}

type historyHTTPStore struct {
	archive                                []cache.HistoricalSeason
	err                                    error
	archiveCalls, seasonCalls, statusCalls int
}

func (s *historyHTTPStore) HistoricalRegularSeasons(context.Context) ([]cache.HistoricalSeason, error) {
	s.archiveCalls++
	return s.archive, s.err
}

func (s *historyHTTPStore) Season(context.Context, string, string) (cache.SeasonData, error) {
	s.seasonCalls++
	return cache.SeasonData{}, errors.New("unexpected season read")
}

func (s *historyHTTPStore) Status(context.Context, string, string) (cache.Status, error) {
	s.statusCalls++
	return cache.Status{}, errors.New("unexpected status read")
}

type historyArchiveState struct {
	lifecycle cache.SourceScopeLifecycle
	inventory cache.InventoryCompleteness
	goals     int64
}

func historyArchive(t *testing.T, states map[string]historyArchiveState) []cache.HistoricalSeason {
	t.Helper()
	archive := make([]cache.HistoricalSeason, 0, len(states))
	for season, state := range states {
		entry, ok := competition.Lookup(season, "Regular Season")
		if !ok {
			t.Fatalf("catalog lacks %s regular season", season)
		}
		inventory := state.inventory
		if inventory == "" {
			inventory = cache.InventoryCompletenessUnknown
		}
		archive = append(archive, cache.HistoricalSeason{Entry: entry, Readiness: &cache.SeasonReadinessSnapshot{
			Scope:     cache.SourceScope{Season: season, Stage: "Regular Season", Lifecycle: state.lifecycle, Discovery: cache.SourceScopeAvailable},
			Readiness: cache.SourceReadinessAvailable, Completeness: inventory,
		}, Data: cache.SeasonData{Games: historyGames(season, 20, state.goals)}})
	}
	return archive
}

func historyGames(season string, count int, totalGoals int64) []cache.Game {
	games := make([]cache.Game, 0, count)
	for index := range count {
		home := totalGoals / 2
		away := totalGoals - home
		games = append(games, cache.Game{ASAID: fmt.Sprintf("history-%s-%d", season, index), Season: season, Stage: "Regular Season", Status: fixtures.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: sql.NullInt64{Int64: home, Valid: true}, AwayScore: sql.NullInt64{Int64: away, Valid: true}})
	}
	return games
}

func assertHistoryCatalogRows(t *testing.T, body string) {
	t.Helper()
	seasons := make([]string, 0)
	for _, entry := range competition.PublicEntries() {
		if entry.Stage == "Regular Season" && entry.SourceAvailable && entry.Supports(competition.CapabilityFixtures) {
			seasons = append(seasons, entry.Season)
		}
	}
	sort.Strings(seasons)
	if got := strings.Count(body, `<option value="`); got != len(seasons) {
		t.Fatalf("season selector options = %d, want %d supported catalog years", got, len(seasons))
	}
	if got := strings.Count(body, `<th scope="row"><a href="scoring?season=`); got != len(seasons) {
		t.Fatalf("history table rows = %d, want %d supported catalog years", got, len(seasons))
	}
	lastRow := -1
	for _, season := range seasons {
		option := `<option value="` + season + `"`
		if got := strings.Count(body, option); got != 1 {
			t.Errorf("season selector entry %s count = %d, want 1", season, got)
		}
		row := `<th scope="row"><a href="scoring?season=` + season + `">` + season + `</a></th>`
		index := strings.Index(body, row)
		if index < 0 {
			t.Errorf("history table omitted catalog year %s", season)
			continue
		}
		if strings.Count(body, row) != 1 {
			t.Errorf("history table row %s was rendered more than once", season)
		}
		if index <= lastRow {
			t.Errorf("history table rows are not ascending: %s appears after a later season", season)
		}
		lastRow = index
	}
}

func assertHistoryPageURLs(t *testing.T, pagePath, body string) {
	t.Helper()
	pageURL, err := url.Parse("https://example.test" + pagePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		attributeValue(t, body, `href="`, `"`, ">Seasons</a>"),
		attributeAfter(t, body, `<form class="history-selector"`, `action="`),
		attributeValue(t, body, `href="`, `"`, "site.css"),
		attributeValue(t, body, `src="`, `"`, "standings.js"),
		attributeValue(t, body, `href="`, `"`, ">2024</a>"),
	} {
		assertHistoryURLStaysMounted(t, pageURL.String(), raw)
	}
}

func assertHistoryURLStaysMounted(t *testing.T, pagePath, raw string) {
	t.Helper()
	pageURL, err := url.Parse(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	if !pageURL.IsAbs() {
		pageURL, err = url.Parse("https://example.test" + pagePath)
		if err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := pageURL.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	prefix := "/"
	if strings.HasPrefix(pageURL.Path, "/nwsl-season/") {
		prefix = "/nwsl-season/"
	}
	if !strings.HasPrefix(resolved.Path, prefix) {
		t.Errorf("%s from %s resolved to %s outside %s", raw, pagePath, resolved.Path, prefix)
	}
}

func attributeValue(t *testing.T, body, prefix, suffix, contains string) string {
	t.Helper()
	start := strings.Index(body, contains)
	if start < 0 {
		t.Fatalf("body lacks %q", contains)
	}
	before := body[:start]
	valueStart := strings.LastIndex(before, prefix)
	if valueStart < 0 {
		t.Fatalf("no %s before %s", prefix, contains)
	}
	valueStart += len(prefix)
	valueEnd := strings.Index(body[valueStart:], suffix)
	if valueEnd < 0 {
		t.Fatalf("unterminated attribute before %s", contains)
	}
	return body[valueStart : valueStart+valueEnd]
}

func attributeAfter(t *testing.T, body, marker, attribute string) string {
	t.Helper()
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("body lacks %q", marker)
	}
	start += strings.Index(body[start:], attribute) + len(attribute)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("unterminated %q", attribute)
	}
	return body[start : start+end]
}
