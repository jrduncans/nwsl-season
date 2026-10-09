package app

import (
	"context"
	"database/sql"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

func TestHistoryScoringRendersOneArchiveReadAndNoSeasonReads(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2019": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 2},
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
	requireText(t, body, "main h1", "Scoring by season")
	requireText(t, body, "main", "History · League trends", "Regular seasons since 2016 in the available archive", "The NWSL did not hold a regular season in 2020", "20 completed, valid matches", "Active through 20 matches", "Cached matches; inventory unverified")
	requireText(t, body, "table caption", "Regular-season scoring data in the available archive")
	requireText(t, body, "table thead th[scope=col]", "Goals per match")
	requireAttr(t, body, "table tbody th[scope=row] a", "href", "scoring?season=2019")
	requireText(t, body, "table tbody tr", "60 3.00")
	requireElements(t, body, "details.history-data")
	var panels []string
	for _, panel := range find(t, body, "section.history-distribution, details.history-data") {
		panels = append(panels, panel.Data)
	}
	if !slices.Equal(panels, []string{"section", "details"}) {
		t.Fatalf("distribution panel is not visible before supporting data details: %q", panels)
	}
	forbidText(t, body, "main", "all-time", "fake chart")
}

func TestHistoryRouteAndProxyLinksResolveWithinMount(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{"2024": {Lifecycle: cache.SourceScopeCompleted, Goals: 2}})}
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
			assertHistoryURLStaysMounted(t, archivePath, attributeValue(t, response.Body.String(), `href="`, `"`, "Explore data"))
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
		"2016": {Lifecycle: cache.SourceScopeCompleted, Inventory: cache.InventoryCompletenessIncomplete, Goals: 4},
		"2024": {Lifecycle: cache.SourceScopeCompleted, Goals: 2},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3},
	})}
	handler := NewHandler(store)
	for _, test := range []struct{ path, want string }{
		{"/history/scoring", "2024"},
		{"/history/scoring?season=2016", "2016"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d %q", test.path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		requireText(t, body, "#selected-season-heading", test.want)
		requireText(t, body, "main", "Currently eligible for comparison: 2024, 2026.", "Excluded from comparison:", "2016 — known fixture inventory incomplete")
		requireAttr(t, body, "table tbody th[scope=row] a", "href", "scoring?season=2024")
		requireText(t, body, "table tbody tr", "40 2.00")
	}
	for _, path := range []string{
		"/history/scoring?season=", "/history/scoring?season=202", "/history/scoring?season=2020",
		"/history/scoring?season=2016&season=2024", "/history/scoring?season=2016;bad=value",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %q, want useful 400", path, response.Code, response.Body.String())
		}
		requireText(t, response.Body.String(), "body", "Invalid history selection")
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
	if validWithMalformedUnrelated.Code != http.StatusOK {
		t.Fatalf("valid selection with malformed unrelated query = %d %q, want selected 2024", validWithMalformedUnrelated.Code, validWithMalformedUnrelated.Body.String())
	}
	requireText(t, validWithMalformedUnrelated.Body.String(), "#selected-season-heading", "2024")

	unsupported := httptest.NewRecorder()
	NewHandler(fakeStore{}).ServeHTTP(unsupported, httptest.NewRequest(http.MethodGet, "/history/scoring", nil))
	if unsupported.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported store = %d %q", unsupported.Code, unsupported.Body.String())
	}
	requireText(t, unsupported.Body.String(), "body", "local archive")
	duplicate := historyArchive(t, map[string]historyArchiveState{"2016": {Lifecycle: cache.SourceScopeCompleted, Goals: 2}})
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
	if empty.Code != http.StatusOK {
		t.Fatalf("empty SQLite history = %d %s", empty.Code, empty.Body.String())
	}
	requireText(t, empty.Body.String(), "main", "Source data unavailable")
	requireText(t, empty.Body.String(), "table tbody th[scope=row] a", "2016")
	teams := []cache.Team{{ASAID: "alpha", Name: "Alpha", ShortName: "Alpha", Abbreviation: "ALP", RawJSON: "{}"}, {ASAID: "bravo", Name: "Bravo", ShortName: "Bravo", Abbreviation: "BRV", RawJSON: "{}"}}
	games := historyGames("2024", 20, 3)
	if _, err := db.ReplaceSeason(ctx, "2024", "Regular Season", teams, games, time.Now()); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewHandler(db).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/history/scoring?season=2024", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("temporary SQLite history = %d %s", response.Code, response.Body.String())
	}
	requireText(t, response.Body.String(), "#selected-season-heading", "2024")
	requireText(t, response.Body.String(), "table tbody tr", "60 3.00")
	assertHistoryCatalogRows(t, response.Body.String())
}

func TestHistoryRendersInvalidAndIncompleteHistoricalExclusions(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2016": {Lifecycle: cache.SourceScopeCompleted, Goals: 2},
		"2017": {Lifecycle: cache.SourceScopeCompleted, Goals: 2},
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
	requireText(t, response.Body.String(), "main", "2016 — invalid completed results", "2017 — historical results incomplete")
}

func TestHistoryXGStateAndDistributionHTTP(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2019": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19},
		"2021": {Lifecycle: cache.SourceScopeCompleted, Goals: 2, XGCovered: 20},
	})
	for index := range archive {
		if archive[index].Entry.Season != "2021" {
			continue
		}
		for xgIndex := range archive[index].Data.XGoals {
			archive[index].Data.XGoals[xgIndex].HomeXG = sql.NullFloat64{Float64: 0, Valid: true}
			archive[index].Data.XGoals[xgIndex].AwayXG = sql.NullFloat64{Float64: 0, Valid: true}
		}
	}
	handler := NewHandler(&historyHTTPStore{archive: archive})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/history/scoring?metric=xg&season=2019", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("xG page status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`<h2 id="selected-season-heading">2019</h2>`, `Expected goals per match`, `xG available for 19 of 20 completed matches; a season average requires 20 of 20.`,
		`<a class="history-metric-link history-metric-link-selected" href="scoring?metric=xg&amp;season=2019" aria-current="page">Expected goals (xG)</a>`,
		`href="scoring?season=2019">Goals</a>`, `<caption>Goal distribution counts and percentages for catalog seasons; bars show goals-eligible seasons</caption>`,
		`<svg class="history-distribution-bar" viewBox="0 0 100 24" role="img"`, `<rect class="history-distribution-segment history-distribution-segment-3" x="0" y="0" width="100" height="24"></rect>`,
		`aria-label="2019: 0 goals: 0 matches (0.0%), 1 goals: 0 matches (0.0%), 2 goals: 0 matches (0.0%), 3 goals: 20 matches (100.0%)`, `xG covered / played`, `xPoints covered / played`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("xG page missing %q", want)
		}
	}
	if strings.Contains(body, `aria-label="2019: 1.50 expected goals`) {
		t.Fatal("partial-xG selected season received an xG chart point")
	}
	if strings.Contains(body, `class="history-distribution-segment history-distribution-segment-3" style=`) {
		t.Fatal("distribution bar still uses HTML span segments")
	}
	if !strings.Contains(body, `aria-label="2021: 0.00 expected goals per completed match`) {
		t.Fatal("complete zero xG season did not receive a chart point")
	}
	formPath := attributeAfter(t, body, `<form class="history-selector"`, `action="`)
	chartPath := attributeValue(t, body, `href="`, `"`, `aria-label="2021: 0.00 expected goals`)
	distributionStart := strings.Index(body, `<section class="history-distribution"`)
	if distributionStart < 0 {
		t.Fatal("xG distribution panel missing")
	}
	distributionPath := attributeValue(t, body[distributionStart:], `href="`, `"`, ">2019</a>")
	assertHistoryMetricRoundTrip(t, handler, formPath+"&season=2021", "2021")
	assertHistoryMetricRoundTrip(t, handler, chartPath, "2021")
	assertHistoryMetricRoundTrip(t, handler, distributionPath, "2019")

	goals := httptest.NewRecorder()
	handler.ServeHTTP(goals, httptest.NewRequest(http.MethodGet, "/nwsl-season/history/scoring?metric=goals&season=2019", nil))
	if goals.Code != http.StatusOK {
		t.Fatalf("Goals round trip = %d %s", goals.Code, goals.Body.String())
	}
	requireText(t, goals.Body.String(), "#selected-season-heading", "2019")
	requireText(t, goals.Body.String(), "main", "Goals per match")

	for _, query := range []string{"metric=", "metric=foo", "metric=xg&metric=goals"} {
		invalid := httptest.NewRecorder()
		handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/history/scoring?"+query, nil))
		if invalid.Code != http.StatusBadRequest {
			t.Errorf("query %q status=%d, want 400", query, invalid.Code)
		}
	}

	compare := httptest.NewRecorder()
	handler.ServeHTTP(compare, httptest.NewRequest(http.MethodGet, "/history/scoring?metric=compare&season=2019", nil))
	if compare.Code != http.StatusOK {
		t.Fatalf("compare page status=%d body=%s", compare.Code, compare.Body.String())
	}
	compareBody := compare.Body.String()
	if got := strings.Count(compareBody, `<svg class="history-chart`); got != 1 {
		t.Fatalf("compare chart SVG count=%d, want one overlaid chart", got)
	}
	if got := strings.Count(compareBody, `history-chart-point-link`); got != 4 || strings.Count(compareBody, `history-chart-point-link-secondary`) != 1 {
		t.Fatalf("compare chart point-link classes=%d (secondary=%d), want goals plus one complete-xG overlay", got, strings.Count(compareBody, `history-chart-point-link-secondary`))
	}
	for _, want := range []string{
		`href="scoring?metric=compare&amp;season=2019" aria-current="page">Compare</a>`,
		`Goals and expected goals per completed match by season`,
		`name="metric" value="compare"`,
	} {
		if !strings.Contains(compareBody, want) {
			t.Errorf("compare page missing %q", want)
		}
	}

	noXG := httptest.NewRecorder()
	noXGArchive := historyArchive(t, map[string]historyArchiveState{"2019": {Lifecycle: cache.SourceScopeCompleted, Goals: 3}})
	NewHandler(&historyHTTPStore{archive: noXGArchive}).ServeHTTP(noXG, httptest.NewRequest(http.MethodGet, "/history/scoring?metric=xg&season=2019", nil))
	if noXG.Code != http.StatusOK {
		t.Fatalf("all-unavailable xG state = %d %s", noXG.Code, noXG.Body.String())
	}
	forbidElements(t, noXG.Body.String(), "svg.history-chart")
	requireText(t, noXG.Body.String(), "main a[href=\"scoring?season=2019\"]", "View Goals")
}

func assertHistoryMetricRoundTrip(t *testing.T, handler http.Handler, rawPath, season string) {
	t.Helper()
	rawPath = html.UnescapeString(rawPath)
	base, err := url.Parse("https://example.test/nwsl-season/history/scoring")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := base.Parse(rawPath)
	if err != nil {
		t.Fatalf("resolve %q: %v", rawPath, err)
	}
	if resolved.Path != "/nwsl-season/history/scoring" {
		t.Fatalf("path %q escaped proxy mount as %q", rawPath, resolved.Path)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, resolved.RequestURI(), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("round trip %q = %d; want selected xG season %s", rawPath, response.Code, season)
	}
	requireText(t, response.Body.String(), "#selected-season-heading", season)
	requireText(t, response.Body.String(), "main", "Expected goals per match")
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

// The history fixtures live in internal/apptest so browser tests and
// cmd/preview share them.
type historyArchiveState = apptest.ArchiveState

func historyArchive(t *testing.T, states map[string]historyArchiveState) []cache.HistoricalSeason {
	t.Helper()
	return apptest.Archive(t, states)
}

func historyGames(season string, count int, totalGoals int64) []cache.Game {
	return apptest.Games(season, count, totalGoals)
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
	start := strings.Index(body, `<table class="history-table">`)
	if start < 0 {
		t.Fatalf("history summary table missing")
	}
	end := strings.Index(body[start:], `<table class="history-distribution-table">`)
	if start < 0 || end < 0 {
		t.Fatalf("history supporting tables missing")
	}
	if got := strings.Count(body[start:start+end], `<th scope="row"><a href="scoring?season=`); got != len(seasons) {
		t.Fatalf("history table rows = %d, want %d supported catalog years", got, len(seasons))
	}
	tableBody := body[start : start+end]
	lastRow := -1
	for _, season := range seasons {
		option := `<option value="` + season + `"`
		if got := strings.Count(body, option); got != 1 {
			t.Errorf("season selector entry %s count = %d, want 1", season, got)
		}
		row := `<th scope="row"><a href="scoring?season=` + season + `">` + season + `</a></th>`
		index := strings.Index(tableBody, row)
		if index < 0 {
			t.Errorf("history table omitted catalog year %s", season)
			continue
		}
		if strings.Count(tableBody, row) != 1 {
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
