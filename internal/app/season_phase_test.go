package app

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestClassifySeasonPhaseConservatively(t *testing.T) {
	completeInventory := &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 2, Games: 2}
	tests := []struct {
		name      string
		data      cache.SeasonData
		inventory *competition.InventoryExpectation
		phase     seasonPhase
		upcoming  bool
		finalSafe bool
	}{
		{name: "empty", data: cache.SeasonData{}, inventory: completeInventory, phase: seasonPhaseUnknown},
		{name: "upcoming without inventory", data: phaseTestData(fixtures.PreMatchStatus, fixtures.PreMatchStatus), phase: seasonPhaseUpcoming, upcoming: true},
		{name: "active", data: phaseTestData(standings.CompletedStatus, fixtures.PreMatchStatus), inventory: completeInventory, phase: seasonPhaseActive, upcoming: true},
		{name: "complete exact inventory", data: phaseTestData(standings.CompletedStatus, standings.CompletedStatus), inventory: completeInventory, phase: seasonPhaseComplete, finalSafe: true},
		{name: "abandoned is terminal but not final-safe", data: phaseTestData(standings.CompletedStatus, fixtures.AbandonedStatus), inventory: completeInventory, phase: seasonPhaseComplete},
		{name: "completed without score", data: phaseTestData(standings.CompletedStatus, standings.CompletedStatus), inventory: completeInventory, phase: seasonPhaseUnknown},
		{name: "unknown fixture status", data: phaseTestData(standings.CompletedStatus, "Delayed"), inventory: completeInventory, phase: seasonPhaseUnknown},
		{name: "incomplete game count", data: phaseTestData(standings.CompletedStatus), inventory: completeInventory, phase: seasonPhaseUnknown},
		{name: "unverified inventory", data: phaseTestData(standings.CompletedStatus, standings.CompletedStatus), phase: seasonPhaseUnknown},
	}
	tests[5].data.Games[1].HomeScore = sql.NullInt64{}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifySeasonPhase(test.data, test.inventory)
			if got.Phase != test.phase || got.HasUpcoming != test.upcoming || got.FinalStandingsSafe != test.finalSafe {
				t.Fatalf("presentation = %+v, want phase=%q upcoming=%t final-safe=%t", got, test.phase, test.upcoming, test.finalSafe)
			}
		})
	}
}

func TestVerifiedInventoryCompleteRequiresExactTeamAppearances(t *testing.T) {
	inventory := &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 2, Games: 2}
	data := phaseTestData(standings.CompletedStatus, standings.CompletedStatus)
	if !verifiedInventoryComplete(data, inventory) {
		t.Fatal("balanced exact inventory was not complete")
	}
	data.Games[1].AwayTeamID = "missing-team"
	if verifiedInventoryComplete(data, inventory) {
		t.Fatal("inventory with a missing catalog team and unexpected team was complete")
	}
}

func TestAbandonedFixturesAreGroupedAsResults(t *testing.T) {
	results, upcoming := fixtureGroupsByStatus(phaseTestData(fixtures.AbandonedStatus), time.UTC)
	if len(results) != 1 || len(upcoming) != 0 {
		t.Fatalf("abandoned fixture groups = %d results, %d upcoming; want 1, 0", len(results), len(upcoming))
	}
}

func TestUpcomingAndCompleteSeasonPresentation(t *testing.T) {
	upcoming := phaseTestData(fixtures.PreMatchStatus, fixtures.PreMatchStatus)
	upcomingResponse := renderSeasonRequest(t, upcoming, "/seasons/2026/regular-season")
	for _, want := range []string{"<h1>Season overview</h1>", "Season schedule", "View the published schedule", ">Schedule</a>", ">Forecast lab</a>"} {
		if !contains(upcomingResponse, want) {
			t.Errorf("upcoming season missing %q", want)
		}
	}
	for _, forbidden := range []string{`<table class="standings"`, ">Clinching scenarios</a>"} {
		if contains(upcomingResponse, forbidden) {
			t.Errorf("upcoming season rendered %q", forbidden)
		}
	}

	complete := completeCatalogSeasonData()
	completeResponse := renderSeasonRequest(t, complete, "/seasons/2026/regular-season")
	for _, want := range []string{"Final standings", `data-standings-mode="total"`, `data-standings-mode-value="per-game"`, `data-standings-mode-value="total"`, ">Results</a>"} {
		if !contains(completeResponse, want) {
			t.Errorf("complete season missing %q", want)
		}
	}
	for _, forbidden := range []string{">Schedule difficulty</a>", ">Forecast lab</a>", ">Clinching scenarios</a>", `title="Venue- and load-adjusted remaining schedule difficulty relative to the league baseline"`} {
		if contains(completeResponse, forbidden) {
			t.Errorf("complete season rendered %q", forbidden)
		}
	}

	fixturesResponse := renderSeasonRequest(t, complete, "/seasons/2026/regular-season/fixtures")
	for _, want := range []string{"<h1>Results</h1>", "Show fixtures for"} {
		if !contains(fixturesResponse, want) {
			t.Errorf("complete results missing %q", want)
		}
	}
	for _, forbidden := range []string{`data-fixture-view-toggle`, `data-fixture-view="upcoming"`, ">Upcoming<", "fixture-outlook"} {
		if contains(fixturesResponse, forbidden) {
			t.Errorf("complete results rendered %q", forbidden)
		}
	}
	for _, route := range []string{"schedule-difficulty", "clinching", "forecast"} {
		response := httptest.NewRecorder()
		NewHandler(fakeStore{season: complete}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/"+route, nil))
		if response.Code != http.StatusNotFound || !contains(response.Body.String(), "unavailable for 2026 Regular Season") {
			t.Errorf("complete season %s = status %d, body %q", route, response.Code, response.Body.String())
		}
		for _, forbidden := range []string{">Schedule difficulty</a>", ">Forecast lab</a>", ">Clinching scenarios</a>"} {
			if contains(response.Body.String(), forbidden) {
				t.Errorf("complete season %s error page rendered %q", route, forbidden)
			}
		}
	}

	abandoned := completeCatalogSeasonData()
	abandoned.Games[0].Status = fixtures.AbandonedStatus
	abandoned.Games[0].HomeScore = sql.NullInt64{}
	abandoned.Games[0].AwayScore = sql.NullInt64{}
	abandonedSeason := renderSeasonRequest(t, abandoned, "/seasons/2026/regular-season")
	if !contains(abandonedSeason, "Season standings") || contains(abandonedSeason, "Final standings") {
		t.Fatalf("abandoned complete season used an unsafe standings caption")
	}
	abandonedResults := renderSeasonRequest(t, abandoned, "/seasons/2026/regular-season/fixtures")
	if !contains(abandonedResults, "<h1>Results</h1>") || !contains(abandonedResults, "Abandoned") || contains(abandonedResults, `data-fixture-view-toggle`) {
		t.Fatalf("abandoned complete season did not render as terminal results")
	}
}

func phaseTestData(statuses ...string) cache.SeasonData {
	data := cache.SeasonData{Teams: []standings.Team{{ID: "alpha", Name: "Alpha FC"}, {ID: "bravo", Name: "Bravo FC"}}}
	for index, status := range statuses {
		game := cache.Game{
			ASAID: fmt.Sprintf("phase-%d", index), Season: "2026", Stage: "Regular Season",
			KickoffUTC: fmt.Sprintf("2026-07-%02d 19:00:00 UTC", index+1), Status: status,
			HomeTeamID: "alpha", AwayTeamID: "bravo", Matchday: sql.NullInt64{Int64: int64(index + 1), Valid: true},
		}
		if status == standings.CompletedStatus {
			game.HomeScore = sql.NullInt64{Int64: 2, Valid: true}
			game.AwayScore = sql.NullInt64{Int64: 1, Valid: true}
		}
		data.Games = append(data.Games, game)
	}
	return data
}

func completeCatalogSeasonData() cache.SeasonData {
	data := catalogSeasonData()
	data.Games = nil
	for index := range 240 {
		home := data.Teams[index%len(data.Teams)].ID
		away := data.Teams[(index+1)%len(data.Teams)].ID
		data.Games = append(data.Games, cache.Game{
			ASAID: fmt.Sprintf("complete-%03d", index), Season: "2026", Stage: "Regular Season",
			KickoffUTC: "2026-10-01 19:00:00 UTC", Status: standings.CompletedStatus,
			HomeTeamID: home, AwayTeamID: away,
			HomeScore: sql.NullInt64{Int64: 1, Valid: true}, AwayScore: sql.NullInt64{Int64: 0, Valid: true},
		})
	}
	return data
}

func renderSeasonRequest(t *testing.T, data cache.SeasonData, route string) string {
	t.Helper()
	response := httptest.NewRecorder()
	NewHandler(fakeStore{season: data}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200; body=%s", route, response.Code, response.Body.String())
	}
	return response.Body.String()
}

func contains(body, text string) bool {
	return strings.Contains(body, text)
}
