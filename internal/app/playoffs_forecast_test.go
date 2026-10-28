package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func playoffResponse(store handoffStore, target string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{ForecastIterations: 40, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

func TestPlayoffsForecastAvailabilityAndRegularRedirect(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	store := handoffStore{regular: regular}
	base := "/nwsl-season/seasons/2026/regular-season/forecast"
	for _, tc := range []struct{ query, want string }{
		{"", "../playoffs/forecast"},
		{"?v=2&m=current-pace-v1&c=results-poisson-v1&extra=ignored&team=" + seeds.Seeds[1], "../playoffs/forecast?c=results-poisson-v1&m=current-pace-v1&team=" + seeds.Seeds[1] + "&v=2"},
		{"?v=2&m=current-pace-v1&team=" + seeds.Seeds[9], "../playoffs/forecast?m=current-pace-v1&v=2"},
	} {
		response := playoffResponse(store, base+tc.query)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != tc.want {
			t.Fatalf("redirect = %d %q, want %q", response.Code, response.Header().Get("Location"), tc.want)
		}
	}
	response := playoffResponse(store, base+"?v=2&m=current-pace-v1&c=results-poisson-v1&p=old:h")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("stale = %d", response.Code)
	}
	requireText(t, response.Body.String(), "main", "out of date")
	requireElements(t, response.Body.String(), `a[href="../playoffs/forecast?c=results-poisson-home-two-seasons-v1&m=current-pace-v1&v=2"]`)
	response = playoffResponse(store, "/seasons/2026/playoffs/forecast")
	if response.Code != http.StatusOK {
		t.Fatalf("playoff forecast = %d: %s", response.Code, response.Body.String())
	}
	requireExact(t, response.Body.String(), `nav a[aria-current=page]`, "Forecast lab")
	requireElements(t, response.Body.String(), ".playoffs-forecast-table", ".playoffs-heatmap", "#playoff-selection")
	requireText(t, response.Body.String(), "thead", "Semifinal", "Final", "Champion")
	if got := len(find(t, response.Body.String(), ".playoffs-forecast-table tbody tr")); got != 8 {
		t.Fatalf("qualifiers=%d", got)
	}
	body := handoffResponse(t, store, "/seasons/2026/regular-season")
	forbidText(t, body, "nav", "Forecast lab")
	body = handoffResponse(t, store, "/seasons/2026/playoffs")
	requireText(t, body, "nav", "Playoffs", "Matches", "Forecast lab", "Explore")
	store.regular.Games[0].Status = fixtures.PreMatchStatus
	response = playoffResponse(store, "/seasons/2026/playoffs/forecast")
	if response.Code != http.StatusNotFound {
		t.Fatalf("early playoffs = %d", response.Code)
	}
	response = playoffResponse(store, "/seasons/2025/playoffs/forecast")
	if response.Code != http.StatusNotFound {
		t.Fatalf("historical playoffs = %d", response.Code)
	}
}

func TestPlayoffsForecastScenariosAndStaleness(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	seeds := playoffSeedsFrom("2026", store.regular)
	state := forecaststate.State{ModelID: "current-pace-v1"}
	state = state.WithPlayoffWinner("quarterfinal-1", simulation.PostseasonMatch{Home: seeds.Seeds[1], Away: seeds.Seeds[8], Winner: seeds.Seeds[8]})
	target := "/seasons/2026/playoffs/" + forecastURL("/seasons/2026/playoffs/forecast", "2026", "playoffs", state, "")
	response := playoffResponse(store, target)
	if response.Code != http.StatusOK {
		t.Fatalf("scenario = %d %s", response.Code, response.Body.String())
	}
	requireText(t, response.Body.String(), ".forecast-assumptions", "advances")
	requireText(t, response.Body.String(), ".playoffs-forecast-table", "Out in quarterfinal", "Through")
	selection := state.Values()[0]
	response = playoffResponse(store, "/seasons/2026/playoffs/forecast?v=2&m=current-pace-v1&action=add&selection="+url.QueryEscape(selection))
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "p=") {
		t.Fatalf("add = %d %s", response.Code, response.Body.String())
	}
	// The chosen winner remains but its opponent changes.
	changed := state.WithPlayoffWinner("quarterfinal-1", simulation.PostseasonMatch{Home: seeds.Seeds[2], Away: seeds.Seeds[8], Winner: seeds.Seeds[8]})
	response = playoffResponse(store, "/seasons/2026/playoffs/"+forecastURL("/seasons/2026/playoffs/forecast", "2026", "playoffs", changed, ""))
	if response.Code != http.StatusBadRequest {
		t.Fatal("changed participant was not stale")
	}
	store.playoffs.Games = []cache.Game{forecastPlayoffGame(seeds, "played", 1, 8, true)}
	response = playoffResponse(store, target)
	if response.Code != http.StatusBadRequest {
		t.Fatal("recorded result was not stale")
	}
	requireElements(t, response.Body.String(), `a[href="forecast?m=current-pace-v1&v=2"]`)
}

func TestPlayoffsForecastSettledAndWithheld(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	seeds := playoffSeedsFrom("2026", store.regular)
	for _, pair := range [][2]int{{1, 8}, {4, 5}, {2, 7}, {3, 6}, {1, 4}, {2, 3}, {1, 2}} {
		game := forecastPlayoffGame(seeds, fmt.Sprintf("game-%d", len(store.playoffs.Games)), pair[0], pair[1], true)
		game.KickoffUTC = fmt.Sprintf("2026-11-%02dT20:00:00Z", len(store.playoffs.Games)+1)
		store.playoffs.Games = append(store.playoffs.Games, game)
	}
	response := playoffResponse(store, "/seasons/2026/playoffs/forecast?v=2&m=current-pace-v1&c=results-poisson-v1")
	if response.Code != http.StatusOK {
		t.Fatalf("settled=%d %s", response.Code, response.Body.String())
	}
	rows := find(t, response.Body.String(), ".playoffs-forecast-table tbody tr")
	if len(rows) != 8 || !strings.Contains(text(rows[0]), "100%") {
		t.Fatal("champion missing")
	}
	for _, row := range rows[1:] {
		if !strings.Contains(text(row), "0%") {
			t.Fatal("eliminated team has odds")
		}
	}
	requireText(t, response.Body.String(), "nav", "Results")
	forbidElements(t, response.Body.String(), "#playoff-selection")
	store.playoffs.Games = store.playoffs.Games[:2]
	store.playoffs.Games[1].AwayScore = store.playoffs.Games[1].HomeScore

	response = playoffResponse(store, "/seasons/2026/playoffs/forecast")
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	requireText(t, response.Body.String(), ".data-warning", "withheld")
	requireText(t, response.Body.String(), ".playoffs-forecast-table", "Out")
	forbidText(t, response.Body.String(), ".playoffs-forecast-table", "%")
}

func TestPlayoffsForecastRowsSortAndBoundExtremes(t *testing.T) {
	data := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", data)
	state := &simulation.Postseason{Seeds: seeds.Seeds, Slots: map[string]simulation.PostseasonMatch{}}
	entry, _ := competition.Lookup("2026", "Playoffs")
	result := simulation.Result{}
	for seed := 1; seed <= 8; seed++ {
		var team simulation.TeamResult
		for _, candidate := range data.Teams {
			if candidate.ID == seeds.Seeds[seed] {
				team.Team = candidate
			}
		}
		team.ChampionshipProbability = .2
		team.RoundReach = map[string]float64{"semifinals": 1, "final": 0}
		if seed == 1 {
			team.ChampionshipProbability = 1
		}
		if seed == 7 || seed == 8 {
			team.ChampionshipEliminated = true
			team.EliminatedRound = "quarterfinals"
			if seed == 8 {
				team.EliminatedRound = "semifinals"
			}
		}
		result.Teams = append(result.Teams, team)
	}
	page := playoffsForecastView(forecastPage{Season: "2026"}, data, state, *entry.BracketFormat, result, nil)
	if page.PlayoffRows[0].Champion != ">99.9%" || page.PlayoffRows[0].Rounds[0] != ">99.9%" {
		t.Fatalf("unproven extreme: %+v", page.PlayoffRows[0])
	}
	if page.PlayoffRows[6].Seed != 8 || page.PlayoffRows[7].Seed != 7 {
		t.Fatalf("elimination order=%+v", page.PlayoffRows)
	}
}

func TestPlayoffForecastBaselineReusesWarmCache(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	app := NewApplication(store, Options{ForecastIterations: 10})
	if err := app.PrecacheForecasts(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := len(app.app.forecasts.cache)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/playoffs/forecast", nil))
	if response.Code != http.StatusOK || len(app.app.forecasts.cache) != before {
		t.Fatalf("baseline cache miss: %d, %d -> %d", response.Code, before, len(app.app.forecasts.cache))
	}
}

func TestPlayoffForecastUnresolvedSeedsWithholdsPercentages(t *testing.T) {
	response := playoffResponse(handoffStore{regular: completeCatalogSeasonData()}, "/seasons/2026/playoffs/forecast")
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	requireText(t, response.Body.String(), ".data-warning", "final playoff seeds are unresolved")
	forbidText(t, response.Body.String(), ".playoffs-forecast-table", "%")
}

// Keep malformed score coverage alongside the page's conservative withholding.
func TestPlayoffForecastMissingResultWithholds(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	seeds := playoffSeedsFrom("2026", store.regular)
	game := forecastPlayoffGame(seeds, "missing", 1, 8, true)
	game.HomeScore = sql.NullInt64{}
	store.playoffs.Games = []cache.Game{game}
	response := playoffResponse(store, "/seasons/2026/playoffs/forecast")
	requireText(t, response.Body.String(), ".data-warning", "withheld")
}
