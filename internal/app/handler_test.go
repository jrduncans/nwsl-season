package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	NewHandler(nil).ServeHTTP(response, request)

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

	NewHandler(nil).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "seasons/2026" {
		t.Fatalf("location = %q, want current season", location)
	}
}

func TestDefaultOptionsLeavesRulesZeroForUnknownSeason(t *testing.T) {
	options := defaultOptions(Options{CurrentSeason: "2027", Stage: "Regular Season"})
	if hasRules(options.Rules) {
		t.Fatalf("unknown configured rules = %+v, want zero", options.Rules)
	}
}

func TestRulesForSeasonUsesConfiguredRulesAndReturnsCopies(t *testing.T) {
	explicit := testRules(17)
	explicit.Version = "configured-v1"
	application := newApplicationWithForecastExecutor(nil, Options{CurrentSeason: "2099", Rules: explicit, Location: time.UTC}, nil)

	current, ok := application.app.rulesForSeason("2099")
	if ok || hasRules(current) {
		t.Fatalf("uncataloged current rules = %+v, %t; want none", current, ok)
	}
	application = newApplicationWithForecastExecutor(nil, Options{CurrentSeason: "2026", Rules: explicit, Location: time.UTC}, nil)
	current, ok = application.app.rulesForSeason("2026")
	if !ok || !reflect.DeepEqual(current, explicit) {
		t.Fatalf("current rules = %+v, want explicit %+v", current, explicit)
	}
	current.Achievements[0].TopK = 99
	if again, _ := application.app.rulesForSeason("2026"); again.Achievements[0].TopK != explicit.Achievements[0].TopK {
		t.Fatalf("mutated returned rules affected later request: %+v", again)
	}
}

func TestRulesForSeasonUsesCatalogAndRequestFallback(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{CurrentSeason: "2099", Rules: testRules(17), Location: time.UTC}, nil)

	catalogRules, ok := application.app.rulesForSeason("2026")
	if !ok || catalogRules.Version != "2026-regular-v2" || catalogRules.GamesPerTeam != 30 || playoffPlaces(catalogRules) != 8 {
		t.Fatalf("catalog rules = %+v", catalogRules)
	}
	fallbackApplication := newApplicationWithForecastExecutor(nil, Options{CurrentSeason: "2099", Stage: "Invented Stage", Rules: testRules(17), Location: time.UTC}, nil)
	fallback, ok := fallbackApplication.app.rulesForSeason("2088")
	if ok || hasRules(fallback) {
		t.Fatalf("uncataloged rules = %+v, %t; want none", fallback, ok)
	}
}

func TestRequestCompetitionUsesOnlyExactCapabilities(t *testing.T) {
	rules := testRules(2)
	standingsOnly := requestCompetition{Cataloged: true, Entry: competition.Entry{Capabilities: []competition.Capability{competition.CapabilityStandings}}}
	if !standingsOnly.standingsAvailable() || standingsOnly.xgAvailable() || standingsOnly.scheduleDifficultyAvailable() || standingsOnly.forecastAvailable(rules, true) {
		t.Fatalf("standings-only availability is not capability exact")
	}
	if standingsOnly.fixturesAvailable() {
		t.Fatal("cataloged entry without fixtures acquired fixture capability")
	}
	unknown := requestCompetition{}
	if !unknown.fixturesAvailable() || !unknown.xgAvailable() || unknown.standingsAvailable() || unknown.forecastAvailable(rules, true) {
		t.Fatalf("unknown scope availability = %+v", unknown)
	}
	forecastWithoutRules := requestCompetition{Cataloged: true, Entry: competition.Entry{Capabilities: []competition.Capability{competition.CapabilityForecast}}}
	if forecastWithoutRules.forecastAvailable(competition.Rules{}, false) {
		t.Fatal("forecast became available without verified playoff rules")
	}
}

func TestCapabilityLimitedPresentationKeepsIndependentControls(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	var fixtures bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&fixtures, "fixtures", seasonPage{Title: "Fixtures", HasFixtureOutlooks: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fixtures.String(), "Scheduled fixtures include an xG Poisson outlook") || strings.Contains(fixtures.String(), "Explore the season forecast") {
		t.Fatalf("fixtures outlook note linked an unavailable forecast: %s", fixtures.String())
	}

	var standings bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&standings, "currentTable", seasonPage{}); err != nil {
		t.Fatal(err)
	}
	body := standings.String()
	for _, want := range []string{"Per game", "Totals"} {
		if !strings.Contains(body, want) {
			t.Errorf("score-only standings missing %q", want)
		}
	}
	if strings.Contains(body, `data-standings-stat-button`) || strings.Contains(body, ">xG<") {
		t.Errorf("score-only standings rendered xG controls: %s", body)
	}
}

func TestUnknownCachedScopeRendersFactualOnlyPages(t *testing.T) {
	data := testSeasonData()
	data.XGoals = append(data.XGoals, cache.GameXG{GameID: "completed", Availability: cache.XGAvailable, HomeXG: sql.NullFloat64{Float64: 2.36, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.11, Valid: true}})
	handler := NewHandler(fakeStore{season: data})

	seasonResponse := httptest.NewRecorder()
	handler.ServeHTTP(seasonResponse, httptest.NewRequest(http.MethodGet, "/seasons/2099", nil))
	if seasonResponse.Code != http.StatusOK {
		t.Fatalf("season status = %d, want 200", seasonResponse.Code)
	}
	seasonBody := seasonResponse.Body.String()
	for _, want := range []string{unknownFormatNotice, `href="2099/fixtures"`} {
		if !strings.Contains(seasonBody, want) {
			t.Errorf("factual season page missing %q", want)
		}
	}
	for _, forbidden := range []string{`<table class="standings"`, "qualification-badge", "playoff-line", "expected regular-season", "16 expected", "30 fixtures", "top 8"} {
		if strings.Contains(seasonBody, forbidden) {
			t.Errorf("factual season page contains %q", forbidden)
		}
	}

	fixturesResponse := httptest.NewRecorder()
	handler.ServeHTTP(fixturesResponse, httptest.NewRequest(http.MethodGet, "/seasons/2099/fixtures", nil))
	if fixturesResponse.Code != http.StatusOK {
		t.Fatalf("fixtures status = %d, want 200", fixturesResponse.Code)
	}
	fixturesBody := fixturesResponse.Body.String()
	for _, want := range []string{unknownFormatNotice, "2–1", "xG 2.36–1.11", "Results &amp; fixtures"} {
		if !strings.Contains(fixturesBody, want) {
			t.Errorf("factual fixtures page missing %q", want)
		}
	}
	for _, forbidden := range []string{"expected regular-season", "fixture-outlook", "Forecast lab", "Schedule difficulty", "Clinching scenarios"} {
		if strings.Contains(fixturesBody, forbidden) {
			t.Errorf("factual fixtures page contains %q", forbidden)
		}
	}
}

func TestUnavailableFeaturesDoNotReadUnknownScope(t *testing.T) {
	store := &recordingStore{fakeStore: fakeStore{season: testSeasonData()}}
	application := NewHandler(store)
	for _, route := range []string{"schedule-difficulty", "forecast", "clinching", "model-evaluation"} {
		response := httptest.NewRecorder()
		application.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2099/"+route, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", route, response.Code)
		}
		if !strings.Contains(response.Body.String(), "unavailable for 2099 Regular Season") || !strings.Contains(response.Body.String(), "Return to the season") || strings.Contains(response.Body.String(), `href="/`) {
			t.Errorf("%s did not render explanatory relative unavailable page", route)
		}
	}
	if store.seasonReads != 0 {
		t.Fatalf("unsupported routes read season %d times", store.seasonReads)
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

func TestTrailingSlashClinchingPathRedirectsToCanonicalRelativePath(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	request := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026/clinching/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "../clinching" {
		t.Fatalf("status = %d, location = %q; want canonical clinching redirect", response.Code, response.Header().Get("Location"))
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

	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	body := response.Body.String()
	for _, text := range []string{"2026 season", "Alpha &amp; Co", "Bravo FC", "Forecast lab", "Results &amp; fixtures", "Schedule difficulty", "Data last fetched on", `class="github-link" href="https://github.com/jrduncans/nwsl-season"`, `aria-label="View the project source on GitHub"`, ">SD</th>", "Harder"} {
		if !strings.Contains(body, text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	footerStart := strings.Index(body, `<footer class="site-footer">`)
	if footerStart < 0 {
		t.Fatal("body does not contain the site footer")
	}
	if strings.Contains(body[:footerStart], "Data last fetched on") {
		t.Fatal("season page renders the data fetch time above the footer")
	}
	if !strings.Contains(body[footerStart:], `Data last fetched on <time datetime="2026-07-09T20:00:00Z" data-local-time="2026-07-09T20:00:00Z">Jul 9, 2026 at 8:00 PM UTC</time>.`) {
		t.Fatal("site footer does not render the data fetch time with a browser-local timestamp")
	}
	if strings.Contains(response.Body.String(), "<h1>Remaining schedule difficulty</h1>") || strings.Contains(response.Body.String(), "Toughest remaining schedule") {
		t.Fatal("main season page still renders the prominent schedule-difficulty summary")
	}
	for _, text := range []string{`data-standings-mode="per-game"`, `data-standings-stat="goals"`, ">Goals</button>", ">xG</button>", ">Per game</button>", ">Totals</button>", `data-standings-stat-column="goals" title="Goals for / against">+/-</th>`, `data-standings-stat-column="xg" title="Expected goals for / against">xG +/-</th>`, `data-total="2/1" data-per-game="2.00/1.00"`, "Incomplete xG data:"} {
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
	if strings.Contains(response.Body.String(), "Clinching is not evaluated") || strings.Contains(response.Body.String(), `class="badge"`) {
		t.Fatal("season page still renders the removed clinching note or misleading indicator")
	}
}

func TestModelEvaluationPageRendersInteractiveChart(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{CurrentSeason: "2026", Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/model-evaluation", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, value := range []string{"How close were the forecasts?", `data-evaluation-chart`, "Final points error", "Relative to the simple baseline", "Straight-line pace", "Model evaluation"} {
		if !strings.Contains(body, value) {
			t.Errorf("body does not contain %q", value)
		}
	}
}

func TestSeasonRendersPersistedQualificationBadge(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	store := fullFakeStore{fakeStore: fakeStore{season: data}, qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}, Statuses: []cache.QualificationStatus{{TeamID: "alpha", Achievement: competition.AchievementShield, TopK: 1, Status: clinching.Clinched}}}}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	for _, value := range []string{`class="badge qualification-badge"`, "✓ Shield", "Guaranteed achievements: Shield"} {
		if !strings.Contains(response.Body.String(), value) {
			t.Errorf("body does not contain %q", value)
		}
	}
}

func TestNonCurrentCatalogSeasonUsesCatalogRulesForStandingsScheduleAndQualification(t *testing.T) {
	data := catalogSeasonData()
	data.FixtureSnapshotID = "snapshot-2026"
	store := &recordingFullFakeStore{
		fullFakeStore: fullFakeStore{
			fakeStore:     fakeStore{season: data},
			qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}},
		},
	}
	configured := testRules(17)
	configured.Version = "configured-v1"
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2099", Rules: configured, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{`data-per-game-playoff-line="true"`, `data-total-playoff-line="true"`, "6 of 240 expected regular-season fixtures"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if got, want := store.qualificationRulesVersions, []string{"2026-regular-v2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("qualification rules versions = %v, want %v", got, want)
	}
	if got, want := store.qualificationSnapshotIDs, []string{"snapshot-2026"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("qualification snapshots = %v, want %v", got, want)
	}
}

func TestSeasonRendersXGInStandingsWithoutCoverageWarning(t *testing.T) {
	data := testSeasonData()
	data.XGoals = []cache.GameXG{{
		GameID: "completed", Availability: cache.XGAvailable,
		HomeXG: sql.NullFloat64{Float64: 2.36, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.11, Valid: true},
		HomeXPoints: sql.NullFloat64{Float64: 2.47, Valid: true}, AwayXPoints: sql.NullFloat64{Float64: .367, Valid: true},
	}}
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, value := range []string{`data-total="2.36/1.11" data-per-game="2.36/1.11"`, `data-total="&#43;1.25" data-per-game="&#43;1.25"`, `data-standings-caption data-goals-label="Current standings" data-xg-label="xG standings, ordered by xPts"`, `data-standings-points-label data-goals-label="Pts" data-xg-label="xPts"`, `data-standings-points data-total="3" data-per-game="3.00" data-xg-total="2.47" data-xg-per-game="2.47"`} {
		if !strings.Contains(body, value) {
			t.Errorf("body does not contain xG value %q", value)
		}
	}
	if strings.Contains(body, "Incomplete xG data:") {
		t.Fatal("season page warns about xG coverage when every completed match has xG")
	}
}

func TestClinchingPagePrioritizesOpportunities(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	store := fullFakeStore{
		fakeStore: fakeStore{season: data},
		qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}, Statuses: []cache.QualificationStatus{
			{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.NotClinched, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed, FixtureIDs: []string{"future-1"}}},
			{TeamID: "bravo", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.NotClinched, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed, FixtureIDs: []string{"future-1"}}},
			{TeamID: "alpha", Achievement: competition.AchievementShield, TopK: 1, Status: clinching.NotClinched, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "calculation budget exhausted"}},
		}},
		scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: scenarios.Slate{State: scenarios.SlateReady, Source: scenarios.SourceMatchday, Matchday: 2, FixtureIDs: []string{"future-1"}}}, Results: []cache.ScenarioResult{
			{Result: scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCanClinch, CanClinch: true, Clauses: []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{{GameID: "future-1", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin}}}}}, CanBeEliminated: true, EliminationClauses: []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{{GameID: "future-1", AllowedOutcomes: []clinching.Outcome{clinching.AwayWin}}}}}}},
			{Result: scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementShield, TopK: 1, State: scenarios.OpportunityAlreadyClinched, AlreadyClinched: true}},
			{Result: scenarios.Result{TeamID: "bravo", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCannotClinch}},
			{Result: scenarios.Result{TeamID: "bravo", Achievement: competition.AchievementShield, TopK: 1, State: scenarios.OpportunityUnresolved, Limitation: "scenario computation budget exhausted"}},
		}},
	}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, value := range []string{"Included slate", "Possible clinching scenarios", "Playoff-elimination scenarios", "Paths without outside help", "can clinch the playoffs", "can be eliminated from the playoffs", "With Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt; beats Bravo FC.", "With Bravo FC beats Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt;.", "with <span class=\"clinching-disclosure\">1 win</span>.", "Win each of these remaining matches: vs Bravo FC."} {
		if !strings.Contains(body, value) {
			t.Errorf("body does not contain %q", value)
		}
	}
	for _, value := range []string{"Current opportunities", "All qualification statuses", "Already clinched.", "cannot_clinch", "scenario computation budget exhausted", "can clinch the Shield", "Calculation notes", "Unable to evaluate", "Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt; — the Shield (no-help path)", "Bravo FC — the Shield"} {
		if strings.Contains(body, value) {
			t.Errorf("body contains removed or internal text %q", value)
		}
	}
}

func TestClinchingNonCurrentCatalogSeasonUsesCatalogRulesVersion(t *testing.T) {
	data := catalogSeasonData()
	data.FixtureSnapshotID = "snapshot-2026"
	store := &recordingFullFakeStore{
		fullFakeStore: fullFakeStore{
			fakeStore:     fakeStore{season: data},
			qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}},
			scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: scenarios.Slate{
				State: scenarios.SlateReady, Source: scenarios.SourceMatchday, FixtureIDs: []string{"future-1"},
			}}},
		},
	}
	configured := testRules(17)
	configured.Version = "configured-v1"
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2099", Rules: configured, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/clinching", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if got, want := store.qualificationRulesVersions, []string{"2026-regular-v2", "2026-regular-v2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("qualification rules versions = %v, want %v", got, want)
	}
	if got, want := store.scenarioRulesVersions, []string{"2026-regular-v2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scenario rules versions = %v, want %v", got, want)
	}
	if got, want := store.scenarioSnapshotIDs, []string{"snapshot-2026"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scenario snapshots = %v, want %v", got, want)
	}
	if got, want := store.scenarioDefinitionVersions, []string{scenarios.DefinitionVersion}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scenario definition versions = %v, want %v", got, want)
	}
}

func TestClinchingPageHidesSlateForNoHelpOnlyPath(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	store := fullFakeStore{
		fakeStore: fakeStore{season: data},
		qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}, Statuses: []cache.QualificationStatus{
			{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.NotClinched, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed, FixtureIDs: []string{"future-1"}}},
		}},
		scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: scenarios.Slate{State: scenarios.SlateReady, Source: scenarios.SourceMatchday, Matchday: 2, FixtureIDs: []string{"future-1"}}}, Results: []cache.ScenarioResult{
			{Result: scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCannotClinch}},
		}},
	}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, value := range []string{"Paths without outside help", "These paths depend only on that team’s results", "id=\"clinching-team\"", "data-clinching-team-card data-clinching-team=\"alpha\"", "Can clinch the playoffs with <span class=\"clinching-disclosure\">1 win</span>.", "Win each of these remaining matches: vs Bravo FC."} {
		if !strings.Contains(body, value) {
			t.Errorf("body does not contain %q", value)
		}
	}
	if strings.Contains(body, "Included slate") {
		t.Error("body contains an irrelevant included slate")
	}
}

func TestClinchingPageShowsCompletedQualificationWhenScenariosArePending(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	found := false
	store := fullFakeStore{
		fakeStore:     fakeStore{season: data},
		scenarioFound: &found,
		qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}, Statuses: []cache.QualificationStatus{{
			TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.Clinched,
		}}},
	}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, value := range []string{"Recalculation pending.", "Already clinched", "Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt; has already clinched the playoffs"} {
		if !strings.Contains(body, value) {
			t.Errorf("body does not contain %q", value)
		}
	}
}

func TestClinchingPageGroupsNoHelpPathsByRelevantTeamPath(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	store := fullFakeStore{
		fakeStore: fakeStore{season: data},
		qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}, Statuses: []cache.QualificationStatus{
			{TeamID: "alpha", Achievement: competition.AchievementShield, TopK: 1, Status: clinching.NotClinched, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed, FixtureIDs: []string{"future-1"}}},
			{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 8, Status: clinching.NotClinched, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed, FixtureIDs: []string{"future-1", "future-2"}}},
			{TeamID: "bravo", Achievement: competition.AchievementPlayoffs, TopK: 8, Status: clinching.NotClinched, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed, FixtureIDs: []string{"future-1"}}},
		}},
		scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: scenarios.Slate{State: scenarios.SlateReady, Source: scenarios.SourceMatchday, Matchday: 2, FixtureIDs: []string{"future-1"}}}, Results: []cache.ScenarioResult{
			{Result: scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementShield, TopK: 1, State: scenarios.OpportunityCannotClinch}},
			{Result: scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 8, State: scenarios.OpportunityCannotClinch}},
			{Result: scenarios.Result{TeamID: "bravo", Achievement: competition.AchievementPlayoffs, TopK: 8, State: scenarios.OpportunityCannotClinch}},
		}},
	}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	alphaCard := strings.Index(body, `data-clinching-team-card data-clinching-team="alpha"`)
	bravoCard := strings.Index(body, `data-clinching-team-card data-clinching-team="bravo"`)
	if alphaCard < 0 || bravoCard < 0 {
		t.Fatalf("no-help team cards missing; body=%s", body)
	}
	if alphaCard > bravoCard {
		t.Error("cards are not ordered by the shortest path, then achievement importance")
	}
	alphaPaths := body[alphaCard:bravoCard]
	if shield, playoffs := strings.Index(alphaPaths, "Can clinch the Shield"), strings.Index(alphaPaths, "Can clinch the playoffs"); shield < 0 || playoffs < 0 || shield > playoffs {
		t.Error("paths inside a team card are not ordered by relevance")
	}
}

func TestClinchingPageShowsPlayoffEliminationScenario(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	store := fullFakeStore{
		fakeStore: fakeStore{season: data},
		scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: scenarios.Slate{State: scenarios.SlateReady, Source: scenarios.SourceMatchday, Matchday: 2, FixtureIDs: []string{"future-1"}}}, Results: []cache.ScenarioResult{
			{Result: scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCannotClinch, CanBeEliminated: true, EliminationClauses: []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{{GameID: "future-1", AllowedOutcomes: []clinching.Outcome{clinching.AwayWin}}}}}}},
		}},
	}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, value := range []string{"Included slate", "Playoff-elimination scenarios", "Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt; can be eliminated from the playoffs", "With Bravo FC beats Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt;."} {
		if !strings.Contains(body, value) {
			t.Errorf("body does not contain %q", value)
		}
	}
	if strings.Contains(body, "No teams can clinch during this slate.") {
		t.Fatal("body hides the only actionable elimination scenario")
	}
}

func TestNoHelpTextUsesWinCountAndHidesUnresolvedReason(t *testing.T) {
	got := noHelpText(clinching.NoHelpPath{State: clinching.NoHelpGuaranteed, FixtureIDs: make([]string, 13)}, "San Diego Wave FC", "the playoffs")
	if got != "San Diego Wave FC can clinch the playoffs with 13 wins." {
		t.Fatalf("no-help text = %q", got)
	}
	if got := noHelpText(clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "calculation budget exhausted"}, "San Diego Wave FC", "the playoffs"); got != "" {
		t.Fatalf("unresolved no-help text = %q, want empty", got)
	}
}

func TestConditionTextRendersNoDrawAlternative(t *testing.T) {
	got := conditionText(
		scenarios.FixtureCondition{GameID: "game", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin, clinching.AwayWin}},
		map[string]string{"home": "Home FC", "away": "Away FC"},
		map[string]cache.Game{"game": {HomeTeamID: "home", AwayTeamID: "away"}},
	)
	if got != "Home FC and Away FC do not draw" {
		t.Fatalf("condition text = %q", got)
	}
}

func TestNoHelpFixtureTextUsesVenueAndOpponent(t *testing.T) {
	got := noHelpFixtureText(
		clinching.NoHelpPath{State: clinching.NoHelpGuaranteed, FixtureIDs: []string{"away", "home"}},
		"target",
		map[string]cache.Game{
			"away": {ASAID: "away", HomeTeamID: "kc", AwayTeamID: "target"},
			"home": {ASAID: "home", HomeTeamID: "target", AwayTeamID: "seattle"},
		},
		map[string]string{"kc": "Kansas City Current", "seattle": "Seattle Reign FC"},
	)
	if got != "at Kansas City Current and vs Seattle Reign FC" {
		t.Fatalf("no-help fixture text = %q", got)
	}
}

func TestJoinConditionsHandlesEmptyList(t *testing.T) {
	if got := joinConditions(nil); got != "" {
		t.Fatalf("joinConditions(nil) = %q, want empty string", got)
	}
}

func TestRemovedXGRouteReturnsNotFound(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/xg", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestFixturesRendersResultsOnSeparatePage(t *testing.T) {
	data := testSeasonData()
	data.XGoals = []cache.GameXG{{
		GameID: "completed", Availability: cache.XGAvailable,
		HomeXG: sql.NullFloat64{Float64: 2.36, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.11, Valid: true},
	}}
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/fixtures", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	for _, text := range []string{"Results and fixtures", "2–1", "xG 2.36–1.11", "Matchday 1", "Scheduled", "Show fixtures for", `data-fixture-team-filter`, `data-fixture-view-toggle`, `data-fixture-view-button="results"`, `data-fixture-view-button="upcoming"`, `data-fixture-view="results"`, `data-fixture-view="upcoming"`, `value="alpha"`, `value="bravo"`, `data-fixture-home-team="alpha"`, `data-fixture-away-team="bravo"`, `href="."`, `href="forecast"`, "Scheduled fixtures include an xG Poisson outlook for each result.", `class="fixture-outlook"`, "Home win <strong>", "Draw <strong>", "Away win <strong>", `class="fixture-outcome-segment fixture-outcome-home"`, `style="--fixture-outcome-share: `} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	if got := strings.Count(response.Body.String(), `class="fixture-outlook"`); got != 5 {
		t.Errorf("rendered %d fixture outlooks, want one for each of 5 remaining fixtures", got)
	}
}

func TestFixtureOutlooksUseTheDefaultModelForRemainingFixtures(t *testing.T) {
	data := testSeasonData()
	outlooks := fixtureOutlooks(data)

	if got, want := len(outlooks), 5; got != want {
		t.Fatalf("fixture outlook count = %d, want %d", got, want)
	}
	for id, outlook := range outlooks {
		if outlook.HomeWin <= 0 || outlook.Draw <= 0 || outlook.AwayWin <= 0 {
			t.Errorf("%s outcome probabilities = %#v, want positive values", id, outlook)
		}
		if total := outlook.HomeWin + outlook.Draw + outlook.AwayWin; math.Abs(total-1) > 1e-9 {
			t.Errorf("%s outcome probability total = %.12f, want 1", id, total)
		}
		if outlook.HomeWinText != percent(outlook.HomeWin) || outlook.DrawText != percent(outlook.Draw) || outlook.AwayWinText != percent(outlook.AwayWin) {
			t.Errorf("%s rendered outcome text = %#v, want values formatted as percentages", id, outlook)
		}
	}
}

func TestFixtureOutlooksDoNotAttachToCompletedFixtures(t *testing.T) {
	data := testSeasonData()
	outlooks := fixtureOutlooks(data)
	groups := fixtureGroupsWithOutlooks(data, time.UTC, outlooks)

	if groups[0].Games[0].Outlook != nil {
		t.Fatalf("completed fixture outlook = %#v, want nil", groups[0].Games[0].Outlook)
	}
	if groups[1].Games[0].Outlook == nil {
		t.Fatal("remaining fixture outlook = nil, want xG Poisson outlook")
	}
}

func TestFixtureOutlooksGracefullyOmitFailedFits(t *testing.T) {
	if got := fixtureOutlooksFor(testSeasonData(), fixtureOutlookFitFailure{}); len(got) != 0 {
		t.Fatalf("fixture outlooks = %#v, want no outlooks after a failed fit", got)
	}
}

func TestFixtureGroupsByStatusKeepsMatchdaysTogether(t *testing.T) {
	data := cache.SeasonData{Games: []cache.Game{
		{ASAID: "completed-early", KickoffUTC: "2026-07-01 19:00:00 UTC", Status: standings.CompletedStatus, Matchday: sql.NullInt64{Int64: 1, Valid: true}},
		{ASAID: "completed-late-a", KickoffUTC: "2026-07-04 19:00:00 UTC", Status: standings.CompletedStatus, Matchday: sql.NullInt64{Int64: 2, Valid: true}},
		{ASAID: "completed-late-b", KickoffUTC: "2026-07-04 21:00:00 UTC", Status: standings.CompletedStatus, Matchday: sql.NullInt64{Int64: 2, Valid: true}},
		{ASAID: "scheduled-late", KickoffUTC: "2026-07-05 19:00:00 UTC", Status: remainingStatus, Matchday: sql.NullInt64{Int64: 2, Valid: true}},
		{ASAID: "upcoming", KickoffUTC: "2026-07-11 19:00:00 UTC", Status: remainingStatus, Matchday: sql.NullInt64{Int64: 3, Valid: true}},
	}}

	results, upcoming := fixtureGroupsByStatus(data, time.UTC)

	if got := []string{results[0].Label, results[0].Games[0].ID, results[0].Games[1].ID, results[0].Games[2].ID, results[1].Label}; !reflect.DeepEqual(got, []string{"Matchday 2", "completed-late-a", "completed-late-b", "scheduled-late", "Matchday 1"}) {
		t.Fatalf("results = %#v, want complete matchdays with newest first", got)
	}
	if !results[0].InProgress || results[0].StartUTC != "2026-07-04T19:00:00Z" {
		t.Fatalf("result group = %#v, want an in-progress matchday starting at its first kickoff", results[0])
	}
	if got := []string{upcoming[0].Label, upcoming[0].Games[0].ID}; !reflect.DeepEqual(got, []string{"Matchday 3", "upcoming"}) {
		t.Fatalf("upcoming = %#v, want chronological fixture order", got)
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

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(2), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `title="Remaining schedule difficulty relative to the league baseline">SD</th>`) || !strings.Contains(response.Body.String(), `aria-label="Remaining schedule difficulty unavailable"`) {
		t.Fatal("complete schedule did not render unavailable schedule indicators")
	}
	if strings.Contains(response.Body.String(), "<h1>Remaining schedule difficulty</h1>") {
		t.Fatal("main season page rendered schedule-difficulty detail")
	}
}

func TestScheduleDifficultyRendersComparisonAndFixtureDetails(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/schedule-difficulty", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"Remaining schedule difficulty", "Toughest remaining schedule", "Easiest remaining schedule", "Home/Away-adjusted comparison", "Compare raw opponent PPG", "Team and fixture detail", "Raw opponent PPG", "Adjusted contribution", "Home", "Away", "Alpha &amp; Co"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	body := response.Body.String()
	for _, text := range []string{"average points per game (PPG) of a team’s remaining opponents", "Each remaining fixture is adjusted separately", "the opponent is playing away", "league’s average home and away PPG", "shown for comparison"} {
		if !strings.Contains(body, text) {
			t.Errorf("body does not contain schedule explanation %q", text)
		}
	}
	for _, text := range []string{"These estimates do not change the official standings", "recommended venue-adjusted", "Venue-adjusted comparison", "not a forecast, adjusted ranking, or power rating", "The data cutoff is"} {
		if strings.Contains(body, text) {
			t.Errorf("body still contains removed schedule wording %q", text)
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

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"Schedule difficulty is unavailable", "Remaining fixtures for Alpha &amp; Co", "Bravo FC", "Unavailable"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
}

func TestScheduleDifficultySuppressesPartialLeagueComparison(t *testing.T) {
	data := cache.SeasonData{
		Teams: []standings.Team{{ID: "alpha", Name: "Alpha"}, {ID: "bravo", Name: "Bravo"}, {ID: "charlie", Name: "Charlie"}},
		Games: []cache.Game{
			{ASAID: "done", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: sql.NullInt64{Int64: 1, Valid: true}, AwayScore: sql.NullInt64{Valid: true}},
			{ASAID: "alpha-charlie", Status: "PreMatch", HomeTeamID: "alpha", AwayTeamID: "charlie"},
			{ASAID: "bravo-charlie", Status: "PreMatch", HomeTeamID: "bravo", AwayTeamID: "charlie"},
		},
	}
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/schedule-difficulty", nil))

	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "League-wide schedule comparison is unavailable") {
		t.Fatalf("status = %d; body=%s", response.Code, body)
	}
	if strings.Contains(body, "Toughest remaining schedule") || strings.Contains(body, "Venue-adjusted comparison") {
		t.Fatal("partial data rendered league-wide comparison")
	}
}

func TestScheduleDifficultyRendersMissingVenueSplitAndNoFixturesAccurately(t *testing.T) {
	data := cache.SeasonData{
		Teams: []standings.Team{{ID: "alpha", Name: "Alpha"}, {ID: "bravo", Name: "Bravo"}, {ID: "charlie", Name: "Charlie"}, {ID: "delta", Name: "Delta"}},
		Games: []cache.Game{
			{ASAID: "alpha-bravo", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: sql.NullInt64{Int64: 2, Valid: true}, AwayScore: sql.NullInt64{Int64: 0, Valid: true}},
			{ASAID: "charlie-delta", Status: standings.CompletedStatus, HomeTeamID: "charlie", AwayTeamID: "delta", HomeScore: sql.NullInt64{Int64: 1, Valid: true}, AwayScore: sql.NullInt64{Int64: 1, Valid: true}},
			{ASAID: "future", Status: "PreMatch", HomeTeamID: "charlie", AwayTeamID: "delta"},
		},
	}
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/schedule-difficulty", nil))

	body := response.Body.String()
	for _, want := range []string{"Home opponent PPG: 1.00; away opponent PPG: —", "No remaining fixtures are currently present for this team."} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestScheduleDifficultyNoteDetectsExcludedStatusesAndConfiguredCoverage(t *testing.T) {
	rules := testRules(2)
	rules.ExpectedTeams = 4
	data := cache.SeasonData{
		Teams: []standings.Team{{ID: "alpha"}, {ID: "bravo"}},
		Games: []cache.Game{
			{ASAID: "done", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo"},
			{ASAID: "abandoned", Status: fixtures.AbandonedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo"},
		},
	}
	note := scheduleDifficultyNote(data, &competition.InventoryExpectation{Teams: rules.ExpectedTeams, GamesPerTeam: rules.GamesPerTeam, Games: 4})
	for _, want := range []string{"2 of 4 expected teams", "2 of 4 expected regular-season fixtures", "status excluded"} {
		if !strings.Contains(note, want) {
			t.Errorf("note = %q, want %q", note, want)
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
	NewHandlerWithOptions(db, Options{Rules: testRules(2), Location: time.UTC}).ServeHTTP(response, request)

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

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"Forecast lab", "xG Poisson", "xg-poisson-home-two-seasons-v1", "Default", "Changes keep your assumptions", `data-forecast-model-form`, "Compare another approach", `href="model-evaluation">Model evaluation</a>`, "See how the forecast approaches performed historically.", "Possible seasons considered", ">20</dd>", "Expected points", "Top 4", "Playoffs", "Shield", "Finish distribution", "Middle 80%", "Build a scenario", "Filter by team", "Choose a fixture", "Add result", "Apply scenario", "Copy scenario link", `data-assumption-builder`, `id="forecast-update"`, `id="forecast-pending-values"`, "Data updated", `data-local-time="2026-07-11T19:00:00Z"`, `data-home-label="Home vs Bravo FC"`, `data-away-label="Away at Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt;"`, "Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt; win", "Bravo FC win", "Playoff line:</strong> top 1"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	if strings.Contains(response.Body.String(), ">Expected finish<") {
		t.Fatal("forecast still renders the expected-finish column")
	}
	if strings.Contains(response.Body.String(), `data-auto-submit`) {
		t.Fatal("forecast builder still uses page-submit behavior")
	}
	if strings.Contains(response.Body.String(), `class="forecast-comparison-control" open`) {
		t.Fatal("comparison control should stay collapsed until a comparison is selected")
	}
	for _, text := range []string{"Show fixtures", "Find fixture", `data-fixture-filter`, "Add assumption", "Update forecast"} {
		if strings.Contains(response.Body.String(), text) {
			t.Errorf("body still contains retired forecast-builder control %q", text)
		}
	}
	if strings.Contains(response.Body.String(), ">Home win<") || strings.Contains(response.Body.String(), ">Away win<") {
		t.Fatal("forecast outcome choices still use home and away labels")
	}
	if strings.Contains(response.Body.String(), "Build a what-if scenario") {
		t.Fatal("Forecast Lab still uses legacy visible navigation")
	}
	if strings.Contains(response.Body.String(), "docs/model-evaluation") || strings.Contains(response.Body.String(), "Formula</a>") {
		t.Fatal("Forecast Lab links to repository documentation that the server does not expose")
	}
}

func TestForecastNonCurrentCatalogSeasonUsesCatalogRules(t *testing.T) {
	data := catalogSeasonData()
	configured := testRules(17)
	configured.Version = "configured-v1"
	options := defaultOptions(Options{CurrentSeason: "2099", Rules: configured, ForecastIterations: 20, Location: time.UTC})
	executor := newForecastExecutor(1, time.Second)
	application := newApplicationWithForecastExecutor(fakeStore{season: data}, options, executor)
	requests := []simulation.Request{}
	application.app.forecasts.run = func(ctx context.Context, request simulation.Request) (simulation.Result, error) {
		requests = append(requests, request)
		return simulation.Run(ctx, request)
	}
	response := httptest.NewRecorder()
	application.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast?v=2&m=current-pace-v1&c=results-poisson-v1", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if len(requests) != 2 {
		t.Fatalf("simulation requests = %+v, want active and comparison requests", requests)
	}
	for _, request := range requests {
		if request.PlayoffPlaces != 8 {
			t.Fatalf("simulation request = %+v, want 8 playoff places", request)
		}
	}
	state := forecaststate.State{ModelID: "current-pace-v1", ComparisonModelID: "results-poisson-v1", Fixed: map[string]simulation.Outcome{}}
	catalogKey := forecastResultKey(data, state, "current-pace-v1", options.ForecastIterations, 8)
	configuredKey := forecastResultKey(data, state, "current-pace-v1", options.ForecastIterations, playoffPlaces(configured))
	if _, found := application.app.forecasts.cache[catalogKey]; !found {
		t.Fatal("forecast cache has no result keyed with the catalog playoff-place count")
	}
	if configuredKey != catalogKey {
		if _, found := application.app.forecasts.cache[configuredKey]; found {
			t.Fatal("forecast cache used the configured current-scope playoff-place count")
		}
	}
	for _, want := range []string{"Playoff line:</strong> top 8", "6 of 240 expected regular-season fixtures"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestForecastTeamFilterRendersFilteredFallbackAndClientFixtureSource(t *testing.T) {
	data := testSeasonData()
	data.Teams = append(data.Teams, standings.Team{ID: "charlie", Name: "Charlie FC"})
	data.Games[len(data.Games)-1].HomeTeamID = "bravo"
	data.Games[len(data.Games)-1].AwayTeamID = "charlie"
	for _, test := range []struct {
		team, want string
		fixtures   int
	}{
		{team: "alpha", want: `data-home-team-id="alpha"`, fixtures: 4},
		{team: "bravo", want: `data-away-team-id="bravo"`, fixtures: 5},
	} {
		request := httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast?team="+test.team, nil)
		response := httptest.NewRecorder()

		NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("team %s: status = %d, want 200; body=%s", test.team, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), test.want) {
			t.Errorf("team %s: body does not contain %q", test.team, test.want)
		}
		body := response.Body.String()
		start := strings.Index(body, `<select id="forecast-fixture"`)
		end := strings.Index(body[start:], `</select>`)
		if start < 0 || end < 0 {
			t.Fatalf("team %s: body does not contain the visible fixture selector", test.team)
		}
		if got := strings.Count(body[start:start+end], `<option value="future-`); got != test.fixtures {
			t.Errorf("team %s: rendered %d fixtures in the fallback selector, want %d", test.team, got, test.fixtures)
		}
		if got := strings.Count(body, `<template id="forecast-all-fixtures">`); got != 1 {
			t.Errorf("team %s: rendered %d client fixture sources, want 1", test.team, got)
		}
	}
}

func TestForecastComparisonUsesDedicatedDeltaTable(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast?v=2&m=results-poisson-v1&c=current-pace-v1", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, text := range []string{"Model comparison", "Results Poisson vs Current pace", `class="forecast-comparison-table"`, "Values read <strong>Current pace</strong> → <strong>Results Poisson</strong>.", "Top 4 chance", "more points", "pp higher"} {
		if !strings.Contains(body, text) {
			t.Errorf("body does not contain %q", text)
		}
	}
	if !strings.Contains(body, `class="forecast-comparison-control" open`) {
		t.Fatal("comparison control should open when a comparison is selected")
	}
	if comparison, projection := strings.Index(body, `class="forecast-comparison"`), strings.Index(body, `class="forecast-results"`); comparison < 0 || projection < 0 || comparison > projection {
		t.Fatalf("model comparison should be rendered before the primary projection: comparison=%d projection=%d", comparison, projection)
	}
	if strings.Contains(body, "Δ comparison − active") {
		t.Fatal("forecast still renders the comparison inside the primary forecast table")
	}
}

func TestForecastAcceptsRecentFormModel(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast?v=2&m=xg-poisson-recent-form-v1", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"xG Poisson (recent form)", "xg-poisson-recent-form-v1", "experimental xG Poisson model"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
}

func TestForecastShowsXGCoverageOnlyWhenRelevant(t *testing.T) {
	data := testSeasonData()
	data.XGoals = []cache.GameXG{{
		GameID: "completed", Availability: cache.XGAvailable,
		HomeXG: sql.NullFloat64{Float64: 2.1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.0, Valid: true},
	}}
	options := Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/seasons/2026/forecast", want: true},
		{path: "/seasons/2026/forecast?v=2&m=results-poisson-v1", want: false},
		{path: "/seasons/2026/forecast?v=2&m=xg-poisson-v1", want: true},
		{path: "/seasons/2026/forecast?v=2&m=xg-poisson-recent-form-v1", want: true},
	} {
		response := httptest.NewRecorder()
		NewHandlerWithOptions(fakeStore{season: data}, options).ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body=%s", test.path, response.Code, response.Body.String())
		}
		if got := strings.Contains(response.Body.String(), "xG coverage:"); got != test.want {
			t.Errorf("%s: xG coverage shown = %t, want %t", test.path, got, test.want)
		}
	}
}

func TestForecastShowsIndependentXGFreshnessAndFailure(t *testing.T) {
	data := testSeasonData()
	data.XGoals = []cache.GameXG{{
		GameID: "completed", Availability: cache.XGAvailable,
		HomeXG: sql.NullFloat64{Float64: 2.1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.0, Valid: true},
	}}
	success := cache.XGSyncRun{FinishedAt: time.Date(2026, 7, 8, 20, 0, 0, 0, time.UTC), Outcome: "success"}
	attempt := cache.XGSyncRun{FinishedAt: time.Date(2026, 7, 9, 21, 0, 0, 0, time.UTC), Outcome: "failure"}
	data.XGStatus = cache.XGStatus{LastSuccess: &success, LastAttempt: &attempt}
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast?v=2&m=xg-poisson-v1", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{"xG data refreshed", `data-local-time="2026-07-08T20:00:00Z"`, "the latest xG refresh failed"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestForecastScheduleNoteReportsExcludedStatusesAndUnevenSchedule(t *testing.T) {
	data := cache.SeasonData{
		Teams: []standings.Team{{ID: "alpha"}, {ID: "bravo"}, {ID: "charlie"}, {ID: "delta"}},
		Games: []cache.Game{
			{ASAID: "abandoned", Status: "Abandoned", HomeTeamID: "alpha", AwayTeamID: "bravo"},
			{ASAID: "future", Status: "PreMatch", HomeTeamID: "alpha", AwayTeamID: "charlie"},
		},
	}
	note := forecastScheduleNote(data, &competition.InventoryExpectation{GamesPerTeam: 1}, false, false)
	for _, want := range []string{"cannot be simulated", "excluded", "team(s) do not have"} {
		if !strings.Contains(note, want) {
			t.Errorf("note = %q, want %q", note, want)
		}
	}
}

func TestForecastResultKeyChangesWithTeamPresentation(t *testing.T) {
	data := cache.SeasonData{Teams: []standings.Team{{ID: "alpha", Name: "Alpha"}}}
	first := forecastResultKey(data, forecaststate.State{}, "results-poisson-v1", 50000, 8)
	data.Teams[0].Name = "Renamed Alpha"
	if second := forecastResultKey(data, forecaststate.State{}, "results-poisson-v1", 50000, 8); second == first {
		t.Fatal("forecast result key did not change with team presentation")
	}
}

func TestForecastResultKeyChangesWithKickoff(t *testing.T) {
	data := cache.SeasonData{Games: []cache.Game{{ASAID: "completed", KickoffUTC: "2026-05-01 20:00:00 UTC"}}}
	first := forecastResultKey(data, forecaststate.State{}, "xg-poisson-recent-form-v1", 50000, 8)
	data.Games[0].KickoffUTC = "2026-05-02 20:00:00 UTC"
	if second := forecastResultKey(data, forecaststate.State{}, "xg-poisson-recent-form-v1", 50000, 8); second == first {
		t.Fatal("forecast result key did not change with fixture kickoff")
	}
}

func TestForecastResultKeyChangesWhenHistoricalVenueSummaryArrives(t *testing.T) {
	data := cache.SeasonData{Teams: []standings.Team{{ID: "alpha", Name: "Alpha"}}}
	first := forecastResultKey(data, forecaststate.State{}, "xg-poisson-home-two-seasons-v1", 50000, 8)
	data.VenueHistory = []cache.VenueSummary{{Season: "2025", Stage: "Regular Season", FixtureReady: true, XGReady: true, Matches: 182, HomeGoals: 260, AwayGoals: 220, XGMatches: 182, HomeXG: 250.5, AwayXG: 215.5}}
	if second := forecastResultKey(data, forecaststate.State{}, "xg-poisson-home-two-seasons-v1", 50000, 8); second == first {
		t.Fatal("forecast result key did not change with historical venue summary")
	}
}

func TestForecastAssumptionsIncludeBrowserLocalTimeData(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast?v=1&m=results-poisson-v1&p=future-1:h", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	for _, text := range []string{"Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt; win", `<time data-local-time="2026-07-11T19:00:00Z">Sat Jul 11, 7:00 PM UTC</time>`, "Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt; vs Bravo FC"} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("body does not contain %q", text)
		}
	}
}

func TestForecastAddResultRedirectsToCanonicalState(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/forecast?v=1&m=results-poisson-v1&p=future-2:d&action=add&fixture=future-1&outcome=h", nil)
	response := httptest.NewRecorder()

	NewHandler(fakeStore{season: testSeasonData()}).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want redirect", response.Code)
	}
	if got, want := response.Header().Get("Location"), "forecast?m=results-poisson-home-two-seasons-v1&p=future-1%3Ah&p=future-2%3Ad&v=2"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
}

func TestForecastPreservesReverseProxyBasePath(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026/forecast", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), `href="/`) || !strings.Contains(response.Body.String(), `href="."`) {
		t.Fatalf("forecast page does not preserve relative base-path links: %s", response.Body.String())
	}
}

func TestSeasonNavigationIsSharedAcrossPages(t *testing.T) {
	paths := []struct {
		path, current string
	}{
		{"/seasons/2026", "Standings"},
		{"/seasons/2026/fixtures", "Results &amp; fixtures"},
		{"/seasons/2026/schedule-difficulty", "Schedule difficulty"},
		{"/seasons/2026/clinching", "Clinching scenarios"},
		{"/seasons/2026/forecast", "Forecast lab"},
	}
	for _, test := range paths {
		t.Run(test.current, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			if !strings.Contains(body, `<nav class="site-nav" aria-label="Season sections">`) {
				t.Fatal("page does not render the shared season navigation")
			}
			navEnd := strings.Index(body, "</nav>")
			if navEnd == -1 {
				t.Fatal("page does not close the shared season navigation")
			}
			navigation := body[:navEnd]
			for _, label := range []string{"Standings", "Results &amp; fixtures", "Schedule difficulty", "Clinching scenarios", "Forecast lab"} {
				if !strings.Contains(navigation, ">"+label+"</a>") {
					t.Errorf("navigation does not contain %q", label)
				}
			}
			if strings.Contains(navigation, `>Model evaluation</a>`) {
				t.Error("navigation still includes Model evaluation")
			}
			if !strings.Contains(navigation, `aria-current="page">`+test.current+"</a>") {
				t.Errorf("navigation does not mark %q as current", test.current)
			}
		})
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

	NewHandler(nil).ServeHTTP(response, request)

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

type recordingStore struct {
	fakeStore
	seasonReads int
}

func (f *recordingStore) Season(ctx context.Context, season, stage string) (cache.SeasonData, error) {
	f.seasonReads++
	return f.fakeStore.Season(ctx, season, stage)
}

type fixtureOutlookFitFailure struct{}

func (fixtureOutlookFitFailure) Info() forecast.Info {
	return forecast.Info{ID: "fixture-outlook-fit-failure"}
}

func (fixtureOutlookFitFailure) Fit(forecast.FitInput) (forecast.Predictor, error) {
	return nil, errors.New("xG input unavailable")
}

type fullFakeStore struct {
	fakeStore
	qualification cache.QualificationSnapshot
	scenario      cache.ScenarioSnapshot
	scenarioFound *bool
}

type recordingFullFakeStore struct {
	fullFakeStore
	qualificationSnapshotIDs   []string
	qualificationRulesVersions []string
	scenarioSnapshotIDs        []string
	scenarioRulesVersions      []string
	scenarioDefinitionVersions []string
}

func (f *recordingFullFakeStore) QualificationForSnapshot(_ context.Context, snapshotID, rulesVersion string) (cache.QualificationSnapshot, bool, error) {
	f.qualificationSnapshotIDs = append(f.qualificationSnapshotIDs, snapshotID)
	f.qualificationRulesVersions = append(f.qualificationRulesVersions, rulesVersion)
	return f.qualification, true, nil
}

func (f *recordingFullFakeStore) ScenarioForSnapshot(_ context.Context, snapshotID, rulesVersion, definitionVersion string) (cache.ScenarioSnapshot, bool, error) {
	f.scenarioSnapshotIDs = append(f.scenarioSnapshotIDs, snapshotID)
	f.scenarioRulesVersions = append(f.scenarioRulesVersions, rulesVersion)
	f.scenarioDefinitionVersions = append(f.scenarioDefinitionVersions, definitionVersion)
	if f.scenarioFound != nil {
		return f.scenario, *f.scenarioFound, nil
	}
	return f.scenario, true, nil
}

func (f fullFakeStore) QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error) {
	return f.qualification, true, nil
}

func (f fullFakeStore) ScenarioForSnapshot(context.Context, string, string, string) (cache.ScenarioSnapshot, bool, error) {
	if f.scenarioFound != nil {
		return f.scenario, *f.scenarioFound, nil
	}
	return f.scenario, true, nil
}

func testRules(gamesPerTeam int) competition.Rules {
	return competition.Rules{
		Season: "test", Stage: "Regular Season", Version: "test-v1", ExpectedTeams: 2, GamesPerTeam: gamesPerTeam,
		Achievements: []competition.Achievement{{ID: competition.AchievementPlayoffs, Label: "Playoffs", TopK: 1}},
	}
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

func catalogSeasonData() cache.SeasonData {
	data := testSeasonData()
	for index := 1; index <= 14; index++ {
		data.Teams = append(data.Teams, standings.Team{ID: fmt.Sprintf("team-%02d", index), Name: fmt.Sprintf("Team %02d", index)})
	}
	return data
}
