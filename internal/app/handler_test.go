package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
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

func TestStageRoutesRedirectLegacyAndRenderPlayoffFacts(t *testing.T) {
	data := testSeasonData()
	for i := range data.Games {
		data.Games[i].Season, data.Games[i].Stage, data.Games[i].KnockoutGame = "2026", "Playoffs", true
		data.Games[i].ExpandedMinutes = sql.NullInt64{Int64: 120, Valid: true}
	}
	handler := NewHandlerWithOptions(fakeStore{season: data}, Options{CurrentSeason: "2026", Location: time.UTC})
	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/seasons/2026/fixtures?x=1", nil))
	if legacy.Code != http.StatusSeeOther || legacy.Header().Get("Location") != "regular-season/fixtures?x=1" {
		t.Fatalf("legacy=%d %q", legacy.Code, legacy.Header().Get("Location"))
	}
	playoffs := httptest.NewRecorder()
	handler.ServeHTTP(playoffs, httptest.NewRequest(http.MethodGet, "/seasons/2026/playoffs/fixtures", nil))
	if playoffs.Code != http.StatusOK {
		t.Fatalf("playoffs=%d %s", playoffs.Code, playoffs.Body.String())
	}
	requireText(t, playoffs.Body.String(), "main", "Knockout game", "120 minutes")
	forbidRaw(t, playoffs.Body.String(), "Clinching scenarios")
	requireElements(t, playoffs.Body.String(), "[data-stage-selector]")
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/seasons/2026/not-a-stage", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown=%d", unknown.Code)
	}
}

func TestBracketRootRendersVerifiedShapeAndKeepsFixturesChronological(t *testing.T) {
	data := testSeasonData()
	for i := range data.Games {
		data.Games[i].Season, data.Games[i].Stage = "2024", "Playoffs"
		data.Games[i].KnockoutGame = true
	}
	data.Games[0].ExtraTime = sql.NullBool{Bool: true, Valid: true}
	data.Games[0].Penalties = sql.NullBool{Bool: true, Valid: true}
	data.Games[0].HomePenalties = sql.NullInt64{Int64: 4, Valid: true}
	data.Games[0].AwayPenalties = sql.NullInt64{Int64: 3, Valid: true}
	data.XGoals = []cache.GameXG{{GameID: data.Games[0].ASAID, Availability: cache.XGAvailable, HomeXG: sql.NullFloat64{Float64: 1.25, Valid: true}, AwayXG: sql.NullFloat64{Float64: .8, Valid: true}}}
	handler := NewHandler(fakeStore{season: data})
	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/seasons/2024/playoffs", nil))
	if root.Code != http.StatusOK {
		t.Fatalf("bracket root status = %d; body=%s", root.Code, root.Body.String())
	}
	requireText(t, root.Body.String(), "main [data-bracket-state]", "Quarterfinals", "Semifinals", "Final", "TBD", "Advances to", "After extra time", "Shootout 4–3", "xG 1.25–0.80")
	fixtures := httptest.NewRecorder()
	handler.ServeHTTP(fixtures, httptest.NewRequest(http.MethodGet, "/seasons/2024/playoffs/fixtures", nil))
	if fixtures.Code != http.StatusOK {
		t.Fatalf("fixtures route did not remain chronological: %d %s", fixtures.Code, fixtures.Body.String())
	}
	forbidElements(t, fixtures.Body.String(), "[data-bracket-state]")
	requireText(t, fixtures.Body.String(), "main", "Knockout game")
}

func TestBracketRootStatesKeepFactsAndRelativeFallback(t *testing.T) {
	base := testSeasonData()
	for i := range base.Games {
		base.Games[i].Season, base.Games[i].Stage, base.Games[i].KnockoutGame = "2024", "Playoffs", true
	}
	partial := testSeasonData()
	for i := range partial.Games {
		partial.Games[i].Season, partial.Games[i].Stage, partial.Games[i].KnockoutGame = "2024", "Playoffs", true
		partial.Games[i].KickoffUTC = "2024-11-01T19:00:00Z"
	}
	partial.Games = partial.Games[:1]
	unresolved := testSeasonData()
	for i := range unresolved.Games {
		unresolved.Games[i].Season, unresolved.Games[i].Stage, unresolved.Games[i].KnockoutGame = "2024", "Playoffs", true
		unresolved.Games[i].KickoffUTC = "2024-11-01T19:00:00Z"
	}
	unresolved.Games = unresolved.Games[:1]
	unresolved.Games[0].HomeScore = sql.NullInt64{Int64: 1, Valid: true}
	unresolved.Games[0].AwayScore = sql.NullInt64{Int64: 1, Valid: true}
	mismatch := testSeasonData()
	for i := range mismatch.Games {
		mismatch.Games[i].Season, mismatch.Games[i].Stage, mismatch.Games[i].KnockoutGame = "2024", "Playoffs", true
		mismatch.Games[i].KickoffUTC = "2024-11-01T19:00:00Z"
	}
	mismatch.Games = mismatch.Games[:1]
	mismatch.Games[0].AwayTeamID = mismatch.Games[0].HomeTeamID

	for _, tc := range []struct {
		name, state, notice string
		data                cache.SeasonData
		wantSource          bool
		empty               bool
	}{
		{name: "empty", notice: "Playoffs fixtures have not been loaded yet", data: cache.SeasonData{Teams: base.Teams}, empty: true},
		{name: "placeholder", notice: "Playoffs fixtures have not been loaded yet", data: cache.SeasonData{Games: []cache.Game{{ASAID: "placeholder"}}, Teams: base.Teams}, empty: true},
		{name: "partial", state: "partial", notice: "Partial bracket data", data: partial},
		{name: "unresolved", state: "unresolved", notice: "unresolved source facts", data: unresolved},
		{name: "mismatch", state: "format_mismatch", notice: "Bracket format mismatch", data: mismatch, wantSource: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandler(fakeStore{season: tc.data}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2024/playoffs", nil))
			body := response.Body.String()
			if response.Code != http.StatusOK {
				t.Fatalf("%s playoff root = %d %s", tc.name, response.Code, body)
			}
			requireText(t, body, "main", tc.notice)
			if tc.empty {
				forbidElements(t, body, "[data-bracket-state]")
				return
			}
			requireElements(t, body, `[data-bracket-state="`+tc.state+`"]`)
			if tc.wantSource {
				requireElements(t, body, `main a[href="playoffs/fixtures"]`)
			}
		})
	}
}

func TestUnpopulatedKnockoutsAreNotLinkedOrRenderedAsEmptyBrackets(t *testing.T) {
	readiness := []cache.SeasonReadinessSnapshot{
		{Scope: cache.SourceScope{Season: "2026", Stage: "Regular Season"}, Readiness: cache.SourceReadinessAvailable, ObservedGames: 1},
		{Scope: cache.SourceScope{Season: "2026", Stage: "Playoffs"}, Readiness: cache.SourceReadinessNotPublished},
		{Scope: cache.SourceScope{Season: "2026", Stage: "NWSL Challenge Cup Final"}, Readiness: cache.SourceReadinessNotPublished},
		{Scope: cache.SourceScope{Season: "2025", Stage: "Playoffs"}, Readiness: cache.SourceReadinessAvailable, ObservedGames: 1, ObservedTeams: 2},
	}
	store := seasonArchiveStore{fakeStore: fakeStore{season: testSeasonData()}, readiness: readiness}
	handler := NewHandler(store)

	regular := httptest.NewRecorder()
	handler.ServeHTTP(regular, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))
	if regular.Code != http.StatusOK {
		t.Fatalf("regular page = %d %s", regular.Code, regular.Body.String())
	}
	forbidElements(t, regular.Body.String(), `[data-stage-destination="playoffs"]`)

	populated := httptest.NewRecorder()
	handler.ServeHTTP(populated, httptest.NewRequest(http.MethodGet, "/seasons/2025/playoffs", nil))
	if populated.Code != http.StatusOK {
		t.Fatalf("populated playoffs = %d %s", populated.Code, populated.Body.String())
	}
	forbidElements(t, populated.Body.String(), `[href="../2026/playoffs"]`)
	requireElements(t, populated.Body.String(), `[href="../2026/regular-season"]`)

	archive := httptest.NewRecorder()
	handler.ServeHTTP(archive, httptest.NewRequest(http.MethodGet, "/seasons", nil))
	if archive.Code != http.StatusOK {
		t.Fatalf("archive = %d %s", archive.Code, archive.Body.String())
	}
	forbidElements(t, archive.Body.String(), `[href="seasons/2026/playoffs"]`)

	empty := httptest.NewRecorder()
	emptyStore := seasonArchiveStore{fakeStore: fakeStore{season: cache.SeasonData{}}, readiness: readiness}
	NewHandler(emptyStore).ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/seasons/2026/playoffs", nil))
	if empty.Code != http.StatusOK {
		t.Fatalf("unpopulated playoff root = %d %s", empty.Code, empty.Body.String())
	}
	forbidElements(t, empty.Body.String(), "[data-bracket-state]")
	requireText(t, empty.Body.String(), "main", "Playoffs fixtures have not been loaded yet")
}

func TestFixtureMinutesAreKnockoutFactsOnly(t *testing.T) {
	data := testSeasonData()
	data.Games[0].ExpandedMinutes = sql.NullInt64{Int64: 120, Valid: true}
	regular := httptest.NewRecorder()
	NewHandler(fakeStore{season: data}).ServeHTTP(regular, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/fixtures", nil))
	if regular.Code != http.StatusOK {
		t.Fatalf("regular=%d %s", regular.Code, regular.Body.String())
	}
	forbidRaw(t, regular.Body.String(), "120 minutes")
	data.Games[0].Stage, data.Games[0].KnockoutGame = "Playoffs", true
	playoffs := httptest.NewRecorder()
	NewHandler(fakeStore{season: data}).ServeHTTP(playoffs, httptest.NewRequest(http.MethodGet, "/seasons/2026/playoffs/fixtures", nil))
	if playoffs.Code != http.StatusOK {
		t.Fatalf("playoffs=%d %s", playoffs.Code, playoffs.Body.String())
	}
	requireText(t, playoffs.Body.String(), "main", "120 minutes")
}

func TestChallengeCupGroupStageIsFactualAndChronological(t *testing.T) {
	data := testSeasonData()
	for i := range data.Games {
		data.Games[i].Season, data.Games[i].Stage = "2020", "NWSL Challenge Cup Group Stage"
		data.Games[i].Matchday = sql.NullInt64{Int64: int64(i + 1), Valid: true}
	}
	data.XGoals = []cache.GameXG{{GameID: "completed", Availability: cache.XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1.4, Valid: true}, AwayXG: sql.NullFloat64{Float64: .7, Valid: true}}}
	for _, path := range []string{"/seasons/2020/challenge-cup-group-stage", "/seasons/2020/challenge-cup-group-stage/fixtures"} {
		response := httptest.NewRecorder()
		NewHandler(fakeStore{season: data}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d; body=%s", path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		requireText(t, body, "[data-stage-selector] option", "Challenge Cup Group Stage")
		requireText(t, body, "main", "2–1", "xG 1.40–0.70")
		forbidRaw(t, body, "Matchday 1", "Clinching scenarios", "Forecast lab", "Schedule difficulty")
		for _, heading := range find(t, body, "h1") {
			if text(heading) == "Standings" {
				t.Errorf("%s unexpectedly rendered a Standings heading", path)
			}
		}
	}
}

func TestForecastURLsUseCanonicalStageBase(t *testing.T) {
	value := forecastURL("/seasons/2026/regular-season/forecast", "2026", "regular-season", forecaststate.State{ModelID: "results-poisson-v1", Fixed: map[string]simulation.Outcome{}}, "")
	if value != "forecast?m=results-poisson-v1&v=2" {
		t.Fatalf("forecast url=%q", value)
	}
}

func TestHome(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	NewHandler(nil).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "seasons/2026/regular-season" {
		t.Fatalf("location = %q, want current season", location)
	}
}

func TestSeasonArchiveListsPublicSeasonsWithoutChangingGlobalNavigation(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(fakeStore{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireExact(t, body, "main h1", "Seasons")
	requireText(t, body, "main", "Current season", "Historical season")
	requireElements(t, body, `a.brand[href="."]`, `[href="seasons/2026/regular-season"]`, `[href="seasons/2026/regular-season/fixtures"]`)
	var years []string
	for _, heading := range find(t, body, "main h2") {
		years = append(years, text(heading))
	}
	if current, historical := slices.Index(years, "2026"), slices.Index(years, "2025"); current < 0 || historical < 0 || current > historical {
		t.Errorf("season order is not descending: %v", years)
	}
	forbidElements(t, body, ".site-nav")
	forbidRaw(t, body, "Data fetch time unavailable")
}

func TestSeasonArchiveUsesOptionalReadinessAndReportsReadFailure(t *testing.T) {
	readiness := []cache.SeasonReadinessSnapshot{
		{Scope: cache.SourceScope{Season: "2025", Stage: "Regular Season"}, Readiness: cache.SourceReadinessNotPublished},
		{Scope: cache.SourceScope{Season: "2026", Stage: "Regular Season"}, Readiness: cache.SourceReadinessAvailable, Completeness: cache.InventoryCompletenessIncomplete},
	}
	response := httptest.NewRecorder()
	NewHandler(seasonArchiveStore{readiness: readiness}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	requireText(t, response.Body.String(), ".season-archive-stage", "Not published", "Partial data")

	failure := httptest.NewRecorder()
	NewHandler(seasonArchiveStore{err: errors.New("readiness failed")}).ServeHTTP(failure, httptest.NewRequest(http.MethodGet, "/seasons", nil))
	if failure.Code != http.StatusInternalServerError {
		t.Fatalf("readiness failure = %d %q", failure.Code, failure.Body.String())
	}
	requireText(t, failure.Body.String(), "body", "load season archive readiness")
}

func TestSeasonArchiveGroupsAllPublicStagesInCatalogOrder(t *testing.T) {
	items := seasonArchiveItems("/seasons", "2026", nil)
	if len(items) == 0 || items[0].Season != "2026" {
		t.Fatalf("archive seasons = %+v", items)
	}
	var stages []string
	for _, item := range items {
		if item.Season == "2026" {
			for _, stage := range item.Stages {
				stages = append(stages, stage.Label)
			}
		}
	}
	want := []string{"Regular Season", "Playoffs", "Challenge Cup Final"}
	if !reflect.DeepEqual(stages, want) {
		t.Fatalf("2026 stages = %v, want %v", stages, want)
	}
	for _, item := range items {
		for _, stage := range item.Stages {
			if len(stage.Links) == 0 || stage.Links[0].Path == "" {
				t.Fatalf("stage %s/%s has no canonical root link", item.Season, stage.Label)
			}
		}
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
	if err := application.app.pages.ExecuteTemplate(&fixtures, "fixtures", seasonPage{Title: "Fixtures", HasFixtureOutlooks: true, HasUpcomingFixtures: true}); err != nil {
		t.Fatal(err)
	}
	requireText(t, fixtures.String(), ".fixture-outlook-note", "Scheduled fixtures include a match outlook")
	forbidRaw(t, fixtures.String(), "Explore the season forecast")

	var toggled bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&toggled, "fixtures", seasonPage{Title: "Fixtures", HasFixtureOutlooks: true, HasResults: true, HasUpcomingFixtures: true, ShowFixtureViewToggle: true}); err != nil {
		t.Fatal(err)
	}
	// The note describes scheduled fixtures, so it belongs to the Upcoming view only.
	note, upcoming := strings.Index(toggled.String(), "fixture-outlook-note"), strings.Index(toggled.String(), `data-fixture-view="upcoming"`)
	if note < 0 || upcoming < 0 || note < upcoming {
		t.Fatalf("outlook note is outside the upcoming view: %s", toggled.String())
	}

	var standings bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&standings, "currentTable", seasonPage{}); err != nil {
		t.Fatal(err)
	}
	body := standings.String()
	requireText(t, body, "body", "Per game", "Totals")
	forbidElements(t, body, "[data-standings-stat-button]")
	for _, button := range find(t, body, "button") {
		if text(button) == "xG" {
			t.Errorf("score-only standings rendered an xG control: %s", body)
		}
	}
}

func TestUnknownCachedScopeRendersFactualOnlyPages(t *testing.T) {
	data := testSeasonData()
	data.XGoals = append(data.XGoals, cache.GameXG{GameID: "completed", Availability: cache.XGAvailable, HomeXG: sql.NullFloat64{Float64: 2.36, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.11, Valid: true}})
	handler := NewHandler(fakeStore{season: data})

	seasonResponse := httptest.NewRecorder()
	handler.ServeHTTP(seasonResponse, httptest.NewRequest(http.MethodGet, "/seasons/2099/regular-season", nil))
	if seasonResponse.Code != http.StatusOK {
		t.Fatalf("season status = %d, want 200", seasonResponse.Code)
	}
	seasonBody := seasonResponse.Body.String()
	requireText(t, seasonBody, "main", unknownFormatNotice)
	requireElements(t, seasonBody, `[href="regular-season/fixtures"]`)
	forbidElements(t, seasonBody, "table.standings", ".qualification-badge", ".playoff-line")
	forbidRaw(t, seasonBody, "expected regular-season", "16 expected", "30 fixtures", "top 8")

	fixturesResponse := httptest.NewRecorder()
	handler.ServeHTTP(fixturesResponse, httptest.NewRequest(http.MethodGet, "/seasons/2099/regular-season/fixtures", nil))
	if fixturesResponse.Code != http.StatusOK {
		t.Fatalf("fixtures status = %d, want 200", fixturesResponse.Code)
	}
	fixturesBody := fixturesResponse.Body.String()
	requireText(t, fixturesBody, "main", unknownFormatNotice, "2–1", "xG 2.36–1.11")
	requireText(t, fixturesBody, ".site-nav", "Results & fixtures")
	forbidRaw(t, fixturesBody, "expected regular-season", "Forecast lab", "Schedule difficulty", "Clinching scenarios")
	forbidRaw(t, fixturesBody, "fixture-outlook")
}

func TestHistoricalCatalogPagesUseRetrospectivePresentation(t *testing.T) {
	data := phaseTestData(standings.CompletedStatus, standings.CompletedStatus)
	data.Teams = append(data.Teams,
		standings.Team{ID: "charlie", Name: "Charlie FC"},
		standings.Team{ID: "delta", Name: "Delta FC"},
	)
	handler := NewHandler(fakeStore{season: data})
	for _, path := range []string{"/seasons/2019/regular-season", "/seasons/2019/regular-season/fixtures"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, response.Code)
		}
		body := response.Body.String()
		requireElements(t, body, "[data-season-selector]", ".season-selector")
		requireText(t, body, ".season-selector span", "Season")
		requireText(t, body, "[data-season-selector] option", "2026")
		if strings.HasSuffix(path, "/fixtures") {
			requireExact(t, body, "main h1", "Results")
			requireText(t, body, "main", "2–1")
			forbidRaw(t, body, "Historical results and xG", "Upcoming")
			forbidElements(t, body, "[data-fixture-view-toggle]", `[data-fixture-view="upcoming"]`)
		} else {
			requireText(t, body, "table.standings caption", "standings · totals")
			requireElements(t, body, `[data-standings-mode="total"]`, `[data-standings-mode-value="per-game"]`, `[data-standings-mode-value="total"]`, `[data-per-game-playoff-line="true"]`, `[data-total-playoff-line="true"]`)
			forbidRaw(t, body, "competition format", "playoff line")
		}
		forbidRaw(t, body, "All seasons", "2026 Regular Season", "2025 Regular Season", "Schedule difficulty", "Forecast lab", "Clinching scenarios", "top 8")
	}
}

func TestHistoricalCatalogEmptyCacheRendersLoadState(t *testing.T) {
	for _, test := range []struct {
		name, notice string
		store        Store
	}{
		{name: "unknown", notice: "isn&#39;t available in the explorer yet", store: fakeStore{}},
		{name: "not published", notice: "has not published historical data", store: historicalReadinessStore{fakeStore: fakeStore{}, readiness: cache.SeasonReadinessSnapshot{Readiness: cache.SourceReadinessNotPublished}, found: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandler(test.store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2018/regular-season/fixtures", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("historical empty response = %d %q", response.Code, response.Body.String())
			}
			requireText(t, response.Body.String(), "main", html.UnescapeString(test.notice), "Browse seasons")
			requireElements(t, response.Body.String(), "[data-season-selector]")
		})
	}
}

func TestSeasonSelectorPreservesStandingsOrResults(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	for _, test := range []struct {
		name, path, destination string
	}{
		{name: "standings", path: "/seasons/2026/regular-season", destination: "../2025/regular-season"},
		{name: "results", path: "/seasons/2026/regular-season/fixtures", destination: "../../2025/regular-season/fixtures"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			requireElements(t, body, "[data-season-switcher] .season-selector", "[data-season-selector] option[value=2025]", `[data-season-destination="2025"][hidden][href="`+test.destination+`"]`)
			requireText(t, body, ".season-selector span", "Season")
			requireText(t, body, "[data-season-selector] option", "2025")
			forbidRaw(t, body, "All seasons")
		})
	}
}

func TestUnavailableFeaturesDoNotReadUnknownScope(t *testing.T) {
	store := &recordingStore{fakeStore: fakeStore{season: testSeasonData()}}
	application := NewHandler(store)
	for _, route := range []string{"schedule-difficulty", "forecast", "clinching", "model-evaluation"} {
		response := httptest.NewRecorder()
		application.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2099/regular-season/"+route, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", route, response.Code)
		}
		requireText(t, response.Body.String(), "main", "unavailable for 2099 Regular Season", "Return to the season")
		if strings.Contains(response.Body.String(), `href="/`) {
			t.Errorf("%s rendered an absolute path", route)
		}
	}
	if store.seasonReads != 0 {
		t.Fatalf("unsupported routes read season %d times", store.seasonReads)
	}
}

func TestRenderedHTTPPathsAreRelative(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil)
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
	for _, relativePath := range []string{`href="regular-season/fixtures"`, `href="regular-season/schedule-difficulty"`, `href="regular-season/forecast"`, `href="../../static/site.css?v=`, `src="../../static/standings.js?v=`} {
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
	if rootResponse.Code != http.StatusSeeOther || rootResponse.Header().Get("Location") != "seasons/2026/regular-season" {
		t.Fatalf("base-path root = status %d, location %q", rootResponse.Code, rootResponse.Header().Get("Location"))
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026/regular-season", nil)
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("base-path season status = %d, want 200", pageResponse.Code)
	}
	requireElements(t, pageResponse.Body.String(), "[data-season-selector] option[value=2025]", `[href="../2025/regular-season"]`)

	archiveRequest := httptest.NewRequest(http.MethodGet, "/explorer/seasons", nil)
	archiveResponse := httptest.NewRecorder()
	handler.ServeHTTP(archiveResponse, archiveRequest)
	if archiveResponse.Code != http.StatusOK {
		t.Fatalf("base-path archive = status %d, body %q", archiveResponse.Code, archiveResponse.Body.String())
	}
	requireElements(t, archiveResponse.Body.String(), `[href="seasons/2026/regular-season"]`)

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

func TestBasePathLegacyRoutesRedirectToPrimaryStage(t *testing.T) {
	handler := NewHandler(fakeStore{season: testSeasonData()})
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/nwsl-season/seasons/2026", want: "2026/regular-season"},
		{path: "/nwsl-season/seasons/2026/fixtures?x=1", want: "regular-season/fixtures?x=1"},
		{path: "/nwsl-season/seasons/2026/forecast", want: "regular-season/forecast"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))

			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != test.want {
				t.Fatalf("status = %d, location = %q; want %q", response.Code, response.Header().Get("Location"), test.want)
			}
		})
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
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	body := response.Body.String()
	requireText(t, body, ".season-selector span", "Season")
	requireText(t, body, ".season-selector option", "2026", "Regular Season")
	requireExact(t, body, "main h1", "Standings")
	requireText(t, body, "table.standings", "Alpha & Co", "Bravo FC", "Harder")
	requireText(t, body, "table.standings th", "SD")
	requireText(t, body, ".site-nav", "Forecast lab", "Results & fixtures", "Schedule difficulty")
	requireAttr(t, body, "a.github-link", "href", "https://github.com/jrduncans/nwsl-season")
	requireAttr(t, body, "a.github-link", "aria-label", "View the project source on GitHub")
	if footer := strings.Index(body, "<footer"); footer < 0 || strings.Contains(body[:footer], "Data last fetched on") {
		t.Fatal("season page renders the data fetch time above the footer")
	}
	forbidRaw(t, body, "Toughest remaining schedule")
	forbidText(t, body, "main h1", "Remaining schedule difficulty")
	requireText(t, body, ".site-footer", "Data last fetched on Jul 9, 2026 at 8:00 PM UTC.")
	requireAttr(t, body, ".site-footer time", "datetime", "2026-07-09T20:00:00Z")
	requireAttr(t, body, ".site-footer time", "data-local-time", "2026-07-09T20:00:00Z")
	requireText(t, body, "main button", "Goals", "xG", "Per game", "Totals")
	requireAttr(t, body, "th[data-standings-stat-column=goals]", "title", "Goals for / against")
	requireText(t, body, "th[data-standings-stat-column=goals]", "+/-")
	requireAttr(t, body, "th[data-standings-stat-column=xg]", "title", "Expected goals for / against")
	requireText(t, body, "th[data-standings-stat-column=xg]", "xG +/-")
	requireText(t, body, "main", "Incomplete xG data:")
	requireElements(t, body, `[data-total="2/1"][data-per-game="2.00/1.00"]`)
	for _, logo := range []string{
		`src="https://american-soccer-analysis-headshots.s3.amazonaws.com/club_logos/alpha.png"`,
		`src="https://american-soccer-analysis-headshots.s3.amazonaws.com/club_logos/bravo.png"`,
	} {
		if got := strings.Count(response.Body.String(), logo); got != 1 {
			t.Errorf("%s appears %d times, want 1 in the standings", logo, got)
		}
	}
	forbidRaw(t, body, "2–1", "Clinching is not evaluated")
	forbidElements(t, body, ".badge")
	if strings.Contains(response.Body.String(), "<script>alert") {
		t.Fatal("team name was not escaped")
	}
}

func TestModelEvaluationPageRendersInteractiveChart(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{CurrentSeason: "2026", Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/model-evaluation", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireElements(t, body, "[data-season-selector]", "[data-stage-selector]")
	requireExact(t, body, "main h1", "Forecast model evaluation")
	requireText(t, body, "main", "Final points error", "Relative to the simple baseline")
	requireAttrContains(t, body, "[data-evaluation-chart]", "data-evaluation", "Straight-line pace", "xG Poisson (schedule load)", "xg-poisson-schedule-load-v1")
	requireText(t, body, "title", "Model evaluation")
}

func TestSeasonRendersPersistedQualificationBadge(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	store := fullFakeStore{
		fakeStore:     fakeStore{season: data},
		qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}, Statuses: []cache.QualificationStatus{{TeamID: "alpha", Achievement: competition.AchievementShield, TopK: 1, Status: clinching.Clinched}}},
		scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Outcome: "complete"}, Results: []cache.ScenarioResult{
			{Result: scenarios.Result{TeamID: "bravo", Achievement: competition.AchievementPlayoffs, AlreadyEliminated: true}},
		}},
	}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	requireText(t, body, "table.standings .badge.qualification-badge", "✓ Shield")
	requireText(t, body, "table.standings", "Guaranteed achievements: Shield")
	requireElements(t, body, `.standings-status.elimination-status[role=img][aria-label="Eliminated from playoff contention."][title="Eliminated from playoff contention."]`)
	requireText(t, body, "main", "× Eliminated from playoffs")
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
	NewHandlerWithOptions(store, Options{CurrentSeason: "2099", Rules: configured, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireElements(t, body, `tr[data-per-game-playoff-line=true]`, `tr[data-total-playoff-line=true]`)
	requireText(t, body, "main", "6 of 240 expected regular-season fixtures")
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

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	requireElements(t, body, `[data-total="2.36/1.11"][data-per-game="2.36/1.11"]`, `[data-total="+1.25"][data-per-game="+1.25"]`)
	requireText(t, body, "[data-standings-caption]", "2026 Regular Season")
	requireAttr(t, body, "[data-standings-caption]", "data-xg-label", "2026 Regular Season xG, ordered by xPts")
	requireText(t, body, "table.standings caption", "2026 Regular Season · per game · through Jul 1")
	requireAttr(t, body, "[data-standings-caption]", "data-goals-label", "2026 Regular Season")
	requireAttr(t, body, "th[data-standings-points-label]", "data-goals-label", "Pts")
	requireAttr(t, body, "th[data-standings-points-label]", "data-xg-label", "xPts")
	requireElements(t, body, `td[data-standings-points][data-total=3][data-per-game="3.00"][data-xg-total="2.47"][data-xg-per-game="2.47"]`)
	forbidRaw(t, body, "Incomplete xG data:")
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
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	// The slate disclosure and the "can clinch the playoffs" heading are
	// exercised by TestPagesClinching in e2e.
	requireText(t, body, "main h2", "Clinching scenarios", "Elimination scenarios", "Season-long paths without outside help")
	requireAttr(t, body, ".clinching-opportunity-elimination h3", "aria-label", "Alpha & Co <script>alert(1)</script> can be eliminated from the playoffs")
	requireText(t, body, ".clinching-result-group h4", "If Alpha & Co <script>alert(1)</script> wins vs Bravo FC", "If Alpha & Co <script>alert(1)</script> loses vs Bravo FC")
	requireText(t, body, "main .clinching-statement", "with 1 win", "Win each of these remaining matches: vs Bravo FC.")
	forbidRaw(t, body, "Current opportunities", "All qualification statuses", "Already clinched.", "cannot_clinch", "scenario computation budget exhausted", "can clinch the Shield", "Calculation notes", "Unable to evaluate", "Alpha &amp; Co &lt;script&gt;alert(1)&lt;/script&gt; — the Shield (no-help path)", "Bravo FC — the Shield")
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
	NewHandlerWithOptions(store, Options{CurrentSeason: "2099", Rules: configured, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if got, want := store.qualificationRulesVersions, []string{"2026-regular-v2", "2026-regular-v2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("qualification rules versions = %v, want %v", got, want)
	}
	if got, want := store.scenarioRulesVersions, []string{"2026-regular-v2", "2026-regular-v2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scenario rules versions = %v, want %v", got, want)
	}
	if got, want := store.scenarioSnapshotIDs, []string{"snapshot-2026", "snapshot-2026"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scenario snapshots = %v, want %v", got, want)
	}
	if got, want := store.scenarioDefinitionVersions, []string{scenarios.DefinitionVersion, scenarios.DefinitionVersion}; !reflect.DeepEqual(got, want) {
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
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireText(t, body, ".clinching-no-help", "Season-long paths without outside help", "These paths may include matches after this slate", "Can clinch the playoffs with 1 win", "Win each of these remaining matches: vs Bravo FC.")
	requireAttr(t, body, ".clinching-team-card img", "src", "https://american-soccer-analysis-headshots.s3.amazonaws.com/club_logos/alpha.png")
	forbidElements(t, body, "[data-clinching-team-filter]", ".clinching-slate")
	forbidRaw(t, body, "Show scenarios for")
}

func TestClinchingPageShowsCompletedQualificationWhenScenariosArePending(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	found := false
	store := fullFakeStore{
		fakeStore:     fakeStore{season: data},
		scenarioFound: &found,
		qualification: cache.QualificationSnapshot{Run: cache.QualificationRun{Outcome: "complete"}, Statuses: []cache.QualificationStatus{
			{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 8, Status: clinching.Clinched},
			{TeamID: "alpha", Achievement: competition.AchievementShield, TopK: 1, Status: clinching.Clinched},
		}},
	}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireText(t, body, "main", "Recalculation pending.")
	requireText(t, body, ".clinching-status-group-clinched h2", "Already clinched the Shield", "Already clinched the playoffs")
	if got := len(find(t, body, "li.clinching-status-item")); got != 2 {
		t.Fatalf("clinched status rows = %d, want one row per achievement group", got)
	}
	requireAttr(t, body, "li.clinching-status-item", "aria-label", "Alpha & Co <script>alert(1)</script> has already clinched the Shield")
	requireAttr(t, body, "li.clinching-status-item", "aria-label", "Alpha & Co <script>alert(1)</script> has already clinched the playoffs")
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
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	cards := find(t, body, "article.clinching-team-card")
	if len(cards) != 2 {
		t.Fatalf("no-help team cards = %d, want 2", len(cards))
	}
	var owners []string
	for _, card := range cards {
		names := findIn(t, card, ".team-name")
		if len(names) == 0 {
			t.Fatal("no-help team card has no team name")
		}
		owners = append(owners, attr(names[0], "title"))
	}
	if !slices.Equal(owners, []string{"Alpha & Co <script>alert(1)</script>", "Bravo FC"}) {
		t.Fatalf("cards are not ordered by the shortest path, then achievement importance: %q", owners)
	}
	var alphaPaths []string
	for _, path := range findIn(t, cards[0], "summary") {
		alphaPaths = append(alphaPaths, text(path))
	}
	if len(alphaPaths) != 2 || !strings.HasPrefix(alphaPaths[0], "Can clinch the Shield") || !strings.HasPrefix(alphaPaths[1], "Can clinch the playoffs") {
		t.Errorf("paths inside a team card are not ordered by relevance: %q", alphaPaths)
	}
}

func TestClinchingPageShowsPlayoffEliminationScenario(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	store := fullFakeStore{
		fakeStore: fakeStore{season: data},
		scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: scenarios.Slate{State: scenarios.SlateReady, Source: scenarios.SourceMatchday, Matchday: 2, FixtureIDs: []string{"future-1"}}}, Results: []cache.ScenarioResult{
			{Result: scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCannotClinch, CanBeEliminated: true, EliminationClauses: []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{{GameID: "future-1", AllowedOutcomes: []clinching.Outcome{clinching.AwayWin}}}}}}},
			{Result: scenarios.Result{TeamID: "bravo", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCannotClinch, AlreadyEliminated: true}},
		}},
	}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireText(t, body, "main", "Elimination scenarios", "Already eliminated")
	requireAttr(t, body, "li.clinching-status-item", "aria-label", "Bravo FC has already been eliminated from the playoffs")
	requireAttr(t, body, ".clinching-opportunity-elimination h3", "aria-label", "Alpha & Co <script>alert(1)</script> can be eliminated from the playoffs")
	requireText(t, body, ".clinching-result-group h4", "If Alpha & Co <script>alert(1)</script> loses vs Bravo FC")
	var headings []string
	for _, heading := range find(t, body, "main h2") {
		headings = append(headings, text(heading))
	}
	alreadyEliminated, conditionalScenarios := slices.Index(headings, "Already eliminated from the playoffs"), slices.Index(headings, "Elimination scenarios")
	if alreadyEliminated < 0 || conditionalScenarios < 0 || alreadyEliminated > conditionalScenarios {
		t.Fatalf("confirmed eliminations are not shown before conditional elimination scenarios: %q", headings)
	}
	forbidElements(t, body, ".clinching-status-group-eliminated .clinching-opportunity")
	forbidRaw(t, body, "No teams can clinch during this slate.")
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

func TestConditionTextUsesResultAndVenue(t *testing.T) {
	teams := map[string]string{"home": "Home FC", "away": "Away FC"}
	games := map[string]cache.Game{"game": {HomeTeamID: "home", AwayTeamID: "away"}}
	for _, test := range []struct {
		name     string
		outcomes []clinching.Outcome
		want     string
	}{
		{name: "home win", outcomes: []clinching.Outcome{clinching.HomeWin}, want: "Home FC wins vs Away FC"},
		{name: "away win", outcomes: []clinching.Outcome{clinching.AwayWin}, want: "Away FC wins at Home FC"},
		{name: "draw", outcomes: []clinching.Outcome{clinching.Draw}, want: "Home FC draws vs Away FC"},
		{name: "home does not lose", outcomes: []clinching.Outcome{clinching.HomeWin, clinching.Draw}, want: "Home FC wins or draws vs Away FC"},
		{name: "away does not lose", outcomes: []clinching.Outcome{clinching.Draw, clinching.AwayWin}, want: "Away FC wins or draws at Home FC"},
		{name: "no draw", outcomes: []clinching.Outcome{clinching.HomeWin, clinching.AwayWin}, want: "Home FC does not draw vs Away FC"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := conditionText(scenarios.FixtureCondition{GameID: "game", AllowedOutcomes: test.outcomes}, teams, games)
			if got != test.want {
				t.Fatalf("condition text = %q, want %q", got, test.want)
			}
		})
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
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/fixtures", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	body := response.Body.String()
	// The team filter select, its options and the fixture rows'
	// data-fixture-home-team/away-team hooks are exercised by testFixturesPage in e2e.
	requireExact(t, body, "main h1", "Results and fixtures")
	requireText(t, body, "main", "2–1", "xG 2.36–1.11", "Matchday 1", "Show fixtures for")
	requireElements(t, body, "[data-fixture-team-filter]", "[data-fixture-view-toggle]", "[data-fixture-view-button=results]", "[data-fixture-view-button=upcoming]", "[data-fixture-view=results]", "[data-fixture-view=upcoming]", `[href="../regular-season"]`, `[href="forecast"]`)
	requireText(t, body, ".fixture-outlook-note", "Scheduled fixtures include a match outlook for each result.", "Outlooks use expected goals, venue, recovery time, and recent fixture load.")
	requireAttr(t, body, ".fixture-outlook-model", "title", "Selected Forecast Lab model: xG Poisson (schedule load)")
	requireText(t, body, ".fixture-outlook-model", "Match outlook")
	requireAttrContains(t, body, ".fixture-outlook", "aria-label", "Match outlook:")
	requireText(t, body, ".fixture-outlook", "Home win", "Draw", "Away win")
	requireElements(t, body, ".fixture-outlook strong", ".fixture-outcome-segment.fixture-outcome-home", ".fixture-team-full", ".fixture-team-code[aria-hidden=true]")
	requireAttrContains(t, body, ".fixture-outcome-segment.fixture-outcome-home", "style", "--fixture-outcome-share: ")
	for _, tag := range find(t, body, "main span") {
		if text(tag) == "Scheduled" {
			t.Error("upcoming fixtures repeat a Scheduled tag")
		}
	}
	if got := len(find(t, body, ".fixture-outlook")); got != 5 {
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
		if outlook.ModelName != forecast.Default().Model.Info().Name {
			t.Errorf("%s model name = %q, want default model name %q", id, outlook.ModelName, forecast.Default().Model.Info().Name)
		}
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
		t.Fatal("remaining fixture outlook = nil, want match outlook")
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

func TestUnknownSeasonPhaseKeepsActiveSeasonCapabilities(t *testing.T) {
	data := testSeasonData()
	data.Games = data.Games[:1]
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(2), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireAttr(t, body, "table.standings th", "title", "Venue- and load-adjusted remaining schedule difficulty relative to the league baseline")
	requireText(t, body, "table.standings th", "SD")
	requireElements(t, body, `[aria-label="Remaining schedule difficulty unavailable"]`, ".schedule-key-track[aria-hidden=true]")
	requireText(t, body, "main", "SD is remaining schedule difficulty")
	requireText(t, body, ".site-nav a", "Schedule difficulty", "Forecast lab", "Clinching scenarios")
	fixturesResponse := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(2), Location: time.UTC}).ServeHTTP(fixturesResponse, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/fixtures", nil))
	if fixturesResponse.Code != http.StatusOK {
		t.Fatalf("unknown incomplete fixtures = %d %q", fixturesResponse.Code, fixturesResponse.Body.String())
	}
	requireExact(t, fixturesResponse.Body.String(), "main h1", "Results and fixtures")
}

func TestScheduleDifficultyRendersComparisonAndFixtureDetails(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/schedule-difficulty", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	// The "Toughest remaining schedule" overview and the plot rows are exercised by
	// testScheduleDifficultyPage in e2e.
	requireText(t, body, "main", "Remaining schedule difficulty", "Easiest remaining schedule", "Venue- and load-adjusted comparison", "Compare venue-only opponent PPG", "Compare raw opponent PPG", "Team and fixture detail", "Raw opponent PPG", "Relative load", "Final difficulty", "Home", "Away", "Alpha & Co")
	requireText(t, body, "main", "adjusts for the two teams’ relative fixture load", "between six and five elapsed days", "third or later match within nine elapsed days", "exp(team congestion − opponent congestion)", "same strongly shrunk effects", "shown for comparison")
	forbidRaw(t, body, "These estimates do not change the official standings", "recommended venue-adjusted", "Venue-adjusted comparison", "not a forecast, adjusted ranking, or power rating", "The data cutoff is")
	requireElements(t, body, ".comparison-disclosure", ".team-schedule-detail")
}

func TestScheduleDifficultyPreservesUnavailableFixtureDetails(t *testing.T) {
	data := testSeasonData()
	data.Games = data.Games[1:2]
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/schedule-difficulty", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	requireText(t, response.Body.String(), "main", "Schedule difficulty is unavailable", "Remaining fixtures for Alpha & Co", "Bravo FC", "Unavailable")
}

func TestScheduleDifficultySuppressesPartialLeagueComparison(t *testing.T) {
	data := cache.SeasonData{
		Teams: []standings.Team{{ID: "alpha", Name: "Alpha"}, {ID: "bravo", Name: "Bravo"}, {ID: "charlie", Name: "Charlie"}},
		Games: []cache.Game{
			{ASAID: "done", KickoffUTC: "2026-05-01 20:00:00 UTC", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: sql.NullInt64{Int64: 1, Valid: true}, AwayScore: sql.NullInt64{Valid: true}},
			{ASAID: "alpha-charlie", KickoffUTC: "2026-05-08 20:00:00 UTC", Status: "PreMatch", HomeTeamID: "alpha", AwayTeamID: "charlie"},
			{ASAID: "bravo-charlie", KickoffUTC: "2026-05-09 20:00:00 UTC", Status: "PreMatch", HomeTeamID: "bravo", AwayTeamID: "charlie"},
		},
	}
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/schedule-difficulty", nil))

	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, body)
	}
	requireText(t, body, "main", "League-wide schedule comparison is unavailable")
	forbidRaw(t, body, "Toughest remaining schedule", "Venue-adjusted comparison")
}

func TestScheduleDifficultyRendersMissingVenueSplitAndNoFixturesAccurately(t *testing.T) {
	data := cache.SeasonData{
		Teams: []standings.Team{{ID: "alpha", Name: "Alpha"}, {ID: "bravo", Name: "Bravo"}, {ID: "charlie", Name: "Charlie"}, {ID: "delta", Name: "Delta"}},
		Games: []cache.Game{
			{ASAID: "alpha-bravo", KickoffUTC: "2026-05-01 20:00:00 UTC", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: sql.NullInt64{Int64: 2, Valid: true}, AwayScore: sql.NullInt64{Int64: 0, Valid: true}},
			{ASAID: "charlie-delta", KickoffUTC: "2026-05-02 20:00:00 UTC", Status: standings.CompletedStatus, HomeTeamID: "charlie", AwayTeamID: "delta", HomeScore: sql.NullInt64{Int64: 1, Valid: true}, AwayScore: sql.NullInt64{Int64: 1, Valid: true}},
			{ASAID: "future", KickoffUTC: "2026-05-10 20:00:00 UTC", Status: "PreMatch", HomeTeamID: "charlie", AwayTeamID: "delta"},
		},
	}
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/schedule-difficulty", nil))

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

	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/fixtures", nil)
	response := httptest.NewRecorder()
	NewHandlerWithOptions(db, Options{Rules: testRules(2), Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	requireText(t, response.Body.String(), "main", "Alpha FC", "3–1")
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
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	// The model form, the assumption builder form, the pending/update hooks and the
	// fixture option kickoff times are exercised by TestPages in e2e
	// (testForecastNoScript, testForecastAssumptionFlow, testLocalTimes).
	requireExact(t, body, "main h1", "Forecast lab")
	requireElements(t, body, "#forecast-model option[value=xg-poisson-schedule-load-v1][selected]")
	requireText(t, body, "#forecast-model option[selected]", "xG Poisson (schedule load)")
	requireText(t, body, ".forecast-model-detail summary .badge", "Default")
	requireText(t, body, "[data-forecast-control-status]", "Changes keep your assumptions")
	requireText(t, body, ".forecast-comparison-control summary", "Compare another approach")
	requireText(t, body, ".forecast-evaluation-link", "Model evaluation", "See how the forecast approaches performed historically.")
	requireElements(t, body, `.forecast-evaluation-link a[href="model-evaluation"]`)
	requireText(t, body, ".forecast-meta", "Possible seasons considered 20", "Data updated")
	requireText(t, body, "table.forecast-table thead", "Expected points", "Top 4", "Playoffs", "Shield")
	requireText(t, body, "table.forecast-table .forecast-distribution", "Finish distribution", "Middle 80%")
	requireText(t, body, ".forecast-builder", "Build a scenario", "Filter by team", "Choose a fixture", "Add result", "Apply scenario")
	requireAttr(t, body, "a[data-copy-scenario]", "aria-label", "Copy scenario link")
	requireAttr(t, body, "#forecast-fixture option", "data-home-label", "Home vs Bravo FC")
	requireAttr(t, body, "#forecast-fixture option", "data-away-label", "Away at Alpha & Co <script>alert(1)</script>")
	requireText(t, body, ".forecast-outcomes", "Alpha & Co <script>alert(1)</script> win", "Bravo FC win")
	requireText(t, body, ".forecast-cutline", "Playoff line: top 1")
	forbidText(t, body, "table.forecast-table thead", "Expected finish")
	forbidElements(t, body, "[data-auto-submit]", "details.forecast-comparison-control[open]", "[data-fixture-filter]")
	forbidRaw(t, body, "Show fixtures", "Find fixture", "Add assumption", "Update forecast", "Build a what-if scenario")
	forbidText(t, body, ".forecast-outcomes", "Home win", "Away win")
	for _, link := range find(t, body, "a") {
		if strings.Contains(attr(link, "href"), "docs/model-evaluation") || text(link) == "Formula" {
			t.Errorf("Forecast Lab links to repository documentation that the server does not expose: %q", attr(link, "href"))
		}
	}
}

func TestForecastRendersChampionshipOddsWithEightTeamBracket(t *testing.T) {
	data := testSeasonData()
	for i := 0; i < 6; i++ {
		data.Teams = append(data.Teams, standings.Team{ID: fmt.Sprintf("extra-%d", i), Name: fmt.Sprintf("Extra %d", i)})
	}
	rules := testRules(30)
	rules.ExpectedTeams = 8
	rules.Achievements[0].TopK = 8
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=2&m=results-poisson-v1&c=current-pace-v1", nil)
	response := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: rules, ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireText(t, body, "table.forecast-table th[scope=col]", "Championship")
	requireText(t, body, "table.forecast-comparison-table th[scope=col]", "Championship chance")
	requireText(t, body, ".forecast-method", "Championship odds continue each simulated table")
	requireText(t, body, ".forecast-results .section-heading", "Championship chances include a simulated playoff bracket")
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
	application.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=2&m=current-pace-v1&c=results-poisson-v1", nil))

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
	requireText(t, response.Body.String(), ".forecast-cutline", "Playoff line: top 8")
	requireText(t, response.Body.String(), "main", "6 of 240 expected regular-season fixtures")
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
		{team: "alpha", want: `data-home-team-id=alpha`, fixtures: 4},
		{team: "bravo", want: `data-away-team-id=bravo`, fixtures: 5},
	} {
		request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?team="+test.team, nil)
		response := httptest.NewRecorder()

		NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("team %s: status = %d, want 200; body=%s", test.team, response.Code, response.Body.String())
		}
		body := response.Body.String()
		requireElements(t, body, "#forecast-fixture option["+test.want+"]")
		if got := len(find(t, body, "select#forecast-fixture option")); got != test.fixtures {
			t.Errorf("team %s: rendered %d fixtures in the fallback selector, want %d", test.team, got, test.fixtures)
		}
		if got := len(find(t, body, "template#forecast-all-fixtures")); got != 1 {
			t.Errorf("team %s: rendered %d client fixture sources, want 1", test.team, got)
		}
	}
}

func TestForecastComparisonUsesDedicatedDeltaTable(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=2&m=results-poisson-v1&c=current-pace-v1", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	// The comparison section, its heading and one row per team are exercised by
	// testForecastCompareModel in e2e.
	requireText(t, body, ".forecast-comparison", "Model comparison", "Results Poisson vs Current pace", "Values read Current pace → Results Poisson.")
	requireText(t, body, "table.forecast-comparison-table", "Top 4 chance", "more points", "pp higher")
	requireElements(t, body, "details.forecast-comparison-control[open]")
	var sections []string
	for _, section := range find(t, body, "main section") {
		if class := attr(section, "class"); class == "forecast-comparison" || class == "forecast-results" {
			sections = append(sections, class)
		}
	}
	if !slices.Equal(sections, []string{"forecast-comparison", "forecast-results"}) {
		t.Fatalf("model comparison should be rendered before the primary projection: %q", sections)
	}
	forbidText(t, body, "table.forecast-table", "Δ comparison − active")
}

func TestForecastAcceptsRecentFormModel(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=2&m=xg-poisson-recent-form-v1", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	requireElements(t, response.Body.String(), "#forecast-model option[value=xg-poisson-recent-form-v1][selected]")
	requireText(t, response.Body.String(), "#forecast-model option[selected]", "xG Poisson (recent form)")
	requireText(t, response.Body.String(), ".forecast-model-detail", "experimental xG Poisson model")
}

func TestForecastAcceptsScheduleLoadComparison(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=2&m=xg-poisson-schedule-load-v1&c=xg-poisson-home-two-seasons-v1", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	requireElements(t, response.Body.String(), "#forecast-model option[value=xg-poisson-schedule-load-v1][selected]")
	requireText(t, response.Body.String(), "#forecast-model option[selected]", "xG Poisson (schedule load)")
	requireText(t, response.Body.String(), ".forecast-comparison", "Model comparison", "xG Poisson (schedule load) vs xG Poisson")
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
		{path: "/seasons/2026/regular-season/forecast", want: true},
		{path: "/seasons/2026/regular-season/forecast?v=2&m=results-poisson-v1", want: false},
		{path: "/seasons/2026/regular-season/forecast?v=2&m=xg-poisson-v1", want: true},
		{path: "/seasons/2026/regular-season/forecast?v=2&m=xg-poisson-recent-form-v1", want: true},
		{path: "/seasons/2026/regular-season/forecast?v=2&m=xg-poisson-schedule-load-v1", want: true},
	} {
		response := httptest.NewRecorder()
		NewHandlerWithOptions(fakeStore{season: data}, options).ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body=%s", test.path, response.Code, response.Body.String())
		}
		if got := len(find(t, response.Body.String(), ".forecast-xg-coverage")) > 0; got != test.want {
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

	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=2&m=xg-poisson-v1", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	requireText(t, response.Body.String(), ".forecast-xg-coverage", "xG data refreshed", "the latest xG refresh failed")
	requireAttr(t, response.Body.String(), ".forecast-xg-coverage time", "data-local-time", "2026-07-08T20:00:00Z")
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
	for _, modelID := range []string{"xg-poisson-recent-form-v1", "xg-poisson-schedule-load-v1"} {
		data := cache.SeasonData{Games: []cache.Game{{ASAID: "completed", KickoffUTC: "2026-05-01 20:00:00 UTC"}}}
		first := forecastResultKey(data, forecaststate.State{}, modelID, 50000, 8)
		data.Games[0].KickoffUTC = "2026-05-02 20:00:00 UTC"
		if second := forecastResultKey(data, forecaststate.State{}, modelID, 50000, 8); second == first {
			t.Errorf("%s result key did not change with fixture kickoff", modelID)
		}
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

func TestForecastCaptionNamesModelResultsDateAndFixedResults(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=2&m=xg-poisson-schedule-load-v1&p=future-1:h", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	requireText(t, response.Body.String(), "table.forecast-table caption", "2026 forecast · xG Poisson (schedule load) · through Jul 1 · 1 fixed result")
}

func TestLatestCompletedMatchDateUsesLocalDateOfCompletedMatches(t *testing.T) {
	pacific, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	games := []cache.Game{
		{Status: standings.CompletedStatus, KickoffUTC: "2026-10-04 23:00:00 UTC"},
		// A later kickoff in UTC that is still Oct 4 in Pacific time.
		{Status: standings.CompletedStatus, KickoffUTC: "2026-10-05 02:30:00 UTC"},
		{Status: "PreMatch", KickoffUTC: "2026-10-16 23:00:00 UTC"},
	}
	if got, ok := latestCompletedMatchDate(games, pacific); !ok || got != "Oct 4" {
		t.Fatalf("latestCompletedMatchDate = %q, %v; want Oct 4", got, ok)
	}
	if _, ok := latestCompletedMatchDate(games[2:], pacific); ok {
		t.Fatal("latestCompletedMatchDate reported a date without completed matches")
	}
}

func TestForecastAssumptionsIncludeBrowserLocalTimeData(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=1&m=results-poisson-v1&p=future-1:h", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	requireText(t, response.Body.String(), "ul.forecast-assumptions li", "Alpha & Co <script>alert(1)</script> win", "Alpha & Co <script>alert(1)</script> vs Bravo FC", "Sat Jul 11, 7:00 PM UTC")
	requireAttr(t, response.Body.String(), "ul.forecast-assumptions time", "data-local-time", "2026-07-11T19:00:00Z")
}

func TestForecastAddResultRedirectsToCanonicalState(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/forecast?v=1&m=results-poisson-v1&p=future-2:d&action=add&fixture=future-1&outcome=h", nil)
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
	request := httptest.NewRequest(http.MethodGet, "/explorer/seasons/2026/regular-season/forecast", nil)
	response := httptest.NewRecorder()

	NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), `href="/`) {
		t.Fatalf("forecast page rendered an absolute path: %s", response.Body.String())
	}
	requireElements(t, response.Body.String(), `[href="../regular-season"]`)
}

func TestSeasonNavigationIsSharedAcrossPages(t *testing.T) {
	paths := []struct {
		path, current, heading string
		seasonSelector         bool
	}{
		{"/seasons/2026/regular-season", "Standings", "Standings", true},
		{"/seasons/2026/regular-season/fixtures", "Results & fixtures", "Results and fixtures", true},
		{"/seasons/2026/regular-season/schedule-difficulty", "Schedule difficulty", "Remaining schedule difficulty", true},
		{"/seasons/2026/regular-season/clinching", "Clinching scenarios", "Clinching scenarios", true},
		{"/seasons/2026/regular-season/forecast", "Forecast lab", "Forecast lab", true},
	}
	for _, test := range paths {
		t.Run(test.current, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			requireElements(t, body, "[data-stage-selector]", "nav.site-nav[aria-label=Season sections]")
			requireExact(t, body, "main h1", test.heading)
			forbidRaw(t, body, "· Regular Season")
			if got := len(find(t, body, "[data-season-selector]")) > 0; got != test.seasonSelector {
				t.Errorf("season selector presence = %t, want %t", got, test.seasonSelector)
			}
			var labels []string
			for _, link := range find(t, body, "nav.site-nav a") {
				labels = append(labels, text(link))
			}
			if want := []string{"Standings", "Results & fixtures", "Schedule difficulty", "Clinching scenarios", "Forecast lab", "Explore"}; !slices.Equal(labels, want) {
				t.Errorf("navigation labels = %q, want %q", labels, want)
			}
			if test.path != "/seasons/2026/regular-season" {
				requireElements(t, body, `nav.site-nav a[href="../regular-season"]`)
			}
			requireExact(t, body, `nav.site-nav a[aria-current=page]`, test.current)
		})
	}
}

func TestForecastRejectsStaleStateAndUnknownModel(t *testing.T) {
	for _, target := range []string{
		"/seasons/2026/regular-season/forecast?v=1&m=results-poisson-v1&p=completed:h",
		"/seasons/2026/regular-season/forecast?v=1&m=other&p=future-1:h",
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

type historicalReadinessStore struct {
	fakeStore
	readiness cache.SeasonReadinessSnapshot
	found     bool
}

func (f historicalReadinessStore) SeasonReadiness(context.Context, string, string) (cache.SeasonReadinessSnapshot, bool, error) {
	return f.readiness, f.found, nil
}

type seasonArchiveStore struct {
	fakeStore
	readiness []cache.SeasonReadinessSnapshot
	err       error
}

func (f seasonArchiveStore) SeasonReadinesses(context.Context) ([]cache.SeasonReadinessSnapshot, error) {
	return f.readiness, f.err
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
