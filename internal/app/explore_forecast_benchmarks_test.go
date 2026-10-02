package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func TestExploreForecastBenchmarksUseDefaultProjectionAndSharedCache(t *testing.T) {
	archive := exploreBenchmarkArchive(t, "2026", true)
	data := &archive[0].Data
	data.Games = append(data.Games, cache.Game{ASAID: "future", Season: "2026", Stage: "Regular Season", Status: fixtures.PreMatchStatus, HomeTeamID: "team-0", AwayTeamID: "team-1"})
	data.VenueHistory = []cache.VenueSummary{{Season: "2025", FixtureReady: true, XGReady: true, Matches: 20, XGMatches: 20, HomeXG: 30}, {Season: "2024", FixtureReady: true, XGReady: true, Matches: 20, XGMatches: 20, AwayXG: 20}}
	store := &historyHTTPStore{archive: archive}
	executor := newForecastExecutor(1, time.Second)
	calls := 0
	executor.run = func(_ context.Context, request simulation.Request) (simulation.Result, error) {
		calls++
		if request.Model.Info().ID != forecast.Default().Model.Info().ID || len(request.Fixed) != 0 || request.Iterations != 12 || request.PlayoffPlaces != 8 || request.PlayoffBracket == nil || request.HistoricalVenue.HomeXG != 30 || len(request.Games) != 46 {
			t.Fatalf("Explore must use the baseline Forecast Lab inputs: %+v", request)
		}
		result := simulation.Result{Model: request.Model.Info(), Iterations: request.Iterations, Remaining: 1}
		for i, team := range request.Teams {
			shield := .01
			if team.ID == "team-3" {
				shield = .91
			}
			result.Teams = append(result.Teams, simulation.TeamResult{Team: team, ExpectedPoints: float64(i), ShieldProbability: shield})
		}
		return result, nil
	}
	app := newApplicationWithForecastExecutor(store, Options{ForecastIterations: 12}, executor)
	// Prewarm exactly the key used by Forecast Lab and startup. Explore must
	// reuse it instead of fitting another model or reading a second snapshot.
	model := forecast.Default().Model
	key := forecastResultKey(*data, forecaststate.State{}, model.Info().ID, 12, 8)
	_, err := executor.results(context.Background(), []forecastTask{{key: key, request: simulation.Request{Model: model, Teams: data.Teams, Games: standingsGames(data.Games), HistoricalVenue: forecastVenueSample(*data), PlayoffPlaces: 8, PlayoffBracket: forecastPlayoffBracket("2026", 8), Iterations: 12}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"shield", "playoff", "top-four"} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend&team=team-0&trend-view=relative&average-reference="+ref, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), model.Info().Name) {
			t.Fatalf("projection unavailable: %d", response.Code)
		}
	}
	if calls != 1 || store.archiveCalls != 3 || store.seasonCalls != 0 {
		t.Fatalf("shared cache/snapshot: runs=%d archive=%d season=%d", calls, store.archiveCalls, store.seasonCalls)
	}
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	app.app.populateExploreForecastBenchmarks(context.Background(), &teams, archive)
	benchmarks := teams.TeamSeasons[0].Benchmarks
	if !slices.Equal(benchmarks[3].Means[0][0].Names, []string{"Team 3"}) {
		t.Fatalf("Shield favorite must use highest Shield odds, not current leader or projected points: %+v", benchmarks[3])
	}
	if !slices.Equal(benchmarks[2].Means[0][0].Names, []string{"Team 9", "Team 8", "Team 7", "Team 6"}) || len(benchmarks[1].Means[0][0].Names) != 8 || benchmarks[1].Means[0][0].Names[0] != "Team 9" {
		t.Fatalf("projected groups must follow expected final points: %+v", benchmarks)
	}
	if *benchmarks[3].Means[0][0].Value != *exploreTrendMeanFor([]exploreTeamRecord{teams.TeamSeasons[0].Teams[3]}, 0, false).Value {
		t.Fatal("forecast selects membership; means must still use played observations")
	}
	// Changed inputs invalidate the shared cache and require a new projection.
	data.Games[0].HomeScore.Int64++
	app.app.populateExploreForecastBenchmarks(context.Background(), &teams, archive)
	if calls != 2 {
		t.Fatal("new fixture result must invalidate projection")
	}
}

func TestExploreForecastBenchmarksWithRealDefaultModel(t *testing.T) {
	archive := exploreBenchmarkArchive(t, "2026", true)
	archive[0].Data.Games = append(archive[0].Data.Games, cache.Game{ASAID: "future", Season: "2026", Stage: "Regular Season", Status: fixtures.PreMatchStatus, HomeTeamID: "team-0", AwayTeamID: "team-1"})
	for index := range archive[0].Data.Games {
		archive[0].Data.Games[index].KickoffUTC = time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(index) * 24 * time.Hour).Format(time.RFC3339)
	}
	app := NewApplication(&historyHTTPStore{archive: archive}, Options{ForecastIterations: 20})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend&team=team-0&trend-view=relative&average-reference=shield", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "Shield favorite unavailable") || !strings.Contains(response.Body.String(), forecast.Default().Model.Info().Name) {
		t.Fatalf("real baseline forecast unavailable: %d", response.Code)
	}
	if len(app.app.forecasts.cache) != 1 {
		t.Fatal("real baseline forecast must populate the shared result cache")
	}
}

func TestExploreForecastBenchmarksUnavailableNeverUsesCurrentStandings(t *testing.T) {
	for _, condition := range []string{"unsupported", "failure", "overloaded", "incomplete"} {
		t.Run(condition, func(t *testing.T) {
			archive := exploreBenchmarkArchive(t, "2026", true)
			archive[0].Data.Games = append(archive[0].Data.Games, cache.Game{ASAID: "future", Season: "2026", Stage: "Regular Season", Status: fixtures.PreMatchStatus, HomeTeamID: "team-0", AwayTeamID: "team-1"})
			if condition == "unsupported" {
				archive[0].Entry.Capabilities = []competition.Capability{competition.CapabilityFixtures, competition.CapabilityStandings}
			}
			if condition == "incomplete" {
				archive[0].Readiness.Completeness = cache.InventoryCompletenessIncomplete
			}
			executor := newForecastExecutor(1, time.Second)
			calls := 0
			executor.run = func(context.Context, simulation.Request) (simulation.Result, error) {
				calls++
				return simulation.Result{}, errors.New("projection failed")
			}
			if condition == "overloaded" {
				executor.slots <- struct{}{}
			}
			app := newApplicationWithForecastExecutor(&historyHTTPStore{archive: archive}, Options{}, executor)
			response := httptest.NewRecorder()
			selection := "/explore?view=season-trend&trend-view=relative&average-reference=shield"
			if condition != "incomplete" {
				selection += "&team=team-0"
			}
			app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, selection, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("forecast failure must preserve Explore: %d", response.Code)
			}
			if condition == "incomplete" {
				if !strings.Contains(response.Body.String(), `data-season-trend-results hidden`) {
					t.Fatal("known incomplete archive must remain withheld")
				}
			} else if !strings.Contains(response.Body.String(), "Shield favorite unavailable") {
				t.Fatal("must show unavailable instead of a current-standings favorite")
			}
			if condition != "failure" && calls != 0 {
				t.Fatalf("must skip unsupported, overloaded or excluded projection: %d", calls)
			}
		})
	}
}

func TestExploreProjectedGroupsTiedShieldFavoritesAndMissingObservations(t *testing.T) {
	archive := exploreBenchmarkArchive(t, "2026", true)
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	season := teams.TeamSeasons[0]
	result := simulation.Result{Model: forecast.Default().Model.Info()}
	for i, team := range archive[0].Data.Teams {
		odds := .0
		if i == 3 || i == 5 {
			odds = .5
		}
		result.Teams = append(result.Teams, simulation.TeamResult{Team: team, ExpectedPoints: float64(i), ShieldProbability: odds})
	}
	groups := exploreProjectedGroups(season, 8, result)
	if len(groups["shield"]) != 2 {
		t.Fatal("equal Shield favorites must retain both teams")
	}
	season.Teams = slices.DeleteFunc(season.Teams, func(team exploreTeamRecord) bool { return team.ID == "team-5" })
	groups = exploreProjectedGroups(season, 8, result)
	if groups["shield"] != nil || groups["playoff"] != nil || len(groups["top-four"]) != 4 {
		t.Fatal("unplayed projected team must withhold only its affected references")
	}
}
