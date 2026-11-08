package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

// preciseResponse simulates enough runs that unlikely upsets still appear, so
// pairings are not mistaken for settled ones.
func preciseResponse(store handoffStore, target string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{ForecastIterations: 4000, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

func headerTexts(t *testing.T, root *html.Node, selector string) []string {
	t.Helper()
	var out []string
	for _, header := range findIn(t, root, selector) {
		out = append(out, text(header))
	}
	return out
}

func TestPlayoffsForecastByMatch(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	response := preciseResponse(store, "/seasons/2026/playoffs/forecast")
	if response.Code != http.StatusOK {
		t.Fatalf("forecast = %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	requireElements(t, body, `input[value="match"]`, "#forecast-match-panel")
	forbidElements(t, body, `input[value="team"]`, "#forecast-team-panel")
	forbidElements(t, body, ".playoff-seed-heading")

	// Every quarterfinal is unplayed, so each gets a table; a quarterfinal result
	// still moves the Final and Champion chances, so both are labeled.
	sections := find(t, body, ".playoff-impact")
	if len(sections) != 4 {
		t.Fatalf("impact sections = %d, want 4", len(sections))
	}
	if got := len(find(t, body, ".playoff-impact-table tbody tr")); got != 4*8 {
		t.Fatalf("impact rows = %d, want %d", got, 4*8)
	}
	headers := headerTexts(t, findIn(t, sections[0], "thead tr")[1], "th")
	if want := []string{"Final", "Champion", "Final", "Champion", "Final", "Champion"}; !slices.Equal(headers, want) {
		t.Fatalf("metric headers = %q, want %q", headers, want)
	}
	for _, row := range find(t, body, ".playoff-impact-table tbody tr") {
		if cells := len(findIn(t, row, "td")); cells != 6 {
			t.Fatalf("impact row has %d cells, want Now plus two outcomes of two metrics", cells)
		}
	}
	if participants := find(t, body, ".playoff-impact-participant"); len(participants) != 4*2 {
		t.Fatalf("participant rows = %d, want 8", len(participants))
	}
	requireText(t, body, ".playoff-impact-table", "Out", "Now", "Champion")
}

func TestPlayoffsForecastByMatchRespectsAssumptions(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	seeds := playoffSeedsFrom("2026", store.regular)
	state := forecaststate.State{ModelID: "current-pace-v1"}
	state = state.WithPlayoffWinner("quarterfinal-1", simulation.PostseasonMatch{Home: seeds.Seeds[1], Away: seeds.Seeds[8], Winner: seeds.Seeds[8]})
	response := preciseResponse(store, "/seasons/2026/playoffs/"+forecastURL("/seasons/2026/playoffs/forecast", "2026", "playoffs", state, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("scenario = %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	// The assumed match is already a scenario, so it gets no table of its own,
	// and its loser is no longer listed.
	if got := len(find(t, body, ".playoff-impact")); got != 3 {
		t.Fatalf("impact sections = %d, want 3", got)
	}
	if got := len(find(t, body, ".playoff-impact-table tbody tr")); got != 3*7 {
		t.Fatalf("impact rows = %d, want %d", got, 3*7)
	}
}

func TestPlayoffsForecastByMatchNarrowsAsTheBracketResolves(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	seeds := playoffSeedsFrom("2026", store.regular)
	store.playoffs.Games = []cache.Game{
		forecastPlayoffGame(seeds, "qf1", 1, 8, true), forecastPlayoffGame(seeds, "qf2", 4, 5, true),
		forecastPlayoffGame(seeds, "qf3", 2, 7, true), forecastPlayoffGame(seeds, "qf4", 3, 6, true),
		forecastPlayoffGame(seeds, "sf1", 1, 4, false), forecastPlayoffGame(seeds, "sf2", 2, 3, false),
	}
	body := preciseResponse(store, "/seasons/2026/playoffs/forecast").Body.String()
	sections := find(t, body, ".playoff-impact")
	if len(sections) != 2 {
		t.Fatalf("impact sections = %d, want both semifinals", len(sections))
	}
	// Whoever wins a semifinal reaches the Final for certain, so only the title
	// still varies.
	if headers := headerTexts(t, findIn(t, sections[0], "thead tr")[1], "th"); !slices.Equal(headers, []string{"Champion", "Champion", "Champion"}) {
		t.Fatalf("semifinal metric headers = %q", headers)
	}
	// With both semifinals played, only the Final remains and its result would
	// just restate the title, so the view is not offered.
	store.playoffs.Games[4].Status, store.playoffs.Games[5].Status = fixtures.CompletedStatus, fixtures.CompletedStatus
	store.playoffs.Games[4].HomeScore, store.playoffs.Games[4].AwayScore = sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true}
	store.playoffs.Games[5].HomeScore, store.playoffs.Games[5].AwayScore = sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Valid: true}
	store.playoffs.Games = append(store.playoffs.Games, forecastPlayoffGame(seeds, "final", 1, 2, false))
	forbidElements(t, preciseResponse(store, "/seasons/2026/playoffs/forecast").Body.String(), `input[value="match"]`, ".playoff-impact")
}

func TestPlayoffsForecastWithheldOmitsByMatch(t *testing.T) {
	response := playoffResponse(handoffStore{regular: completeCatalogSeasonData()}, "/seasons/2026/playoffs/forecast")
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	forbidElements(t, response.Body.String(), `input[value="match"]`, ".playoff-impact")
}
