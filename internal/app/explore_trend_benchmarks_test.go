package app

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestExploreTrendBenchmarksWeightsDirectionTiesAndCoverage(t *testing.T) {
	zero, one, three := 0.0, 1.0, 3.0
	teams := []exploreTeamRecord{
		{ID: "a", Name: "Alpha", Matches: []exploreMatchRecord{
			{Values: [4]exploreTeamValues{{Actual: 4, Expected: &three}, {Actual: 0, Expected: &zero}, {}, {Actual: 3, Expected: &one}}},
		}},
		{ID: "b", Name: "Bravo", Matches: []exploreMatchRecord{
			{Values: [4]exploreTeamValues{{Actual: 1, Expected: &one}, {Actual: 2, Expected: &one}, {}, {Actual: 1, Expected: &zero}}},
			{Values: [4]exploreTeamValues{{Actual: 3}, {Actual: 0}, {}, {Actual: 3, Expected: &three}}},
		}},
	}
	mean := exploreTrendMeanFor(teams, 0, false)
	if mean.Value == nil || math.Abs(*mean.Value-8.0/3) > 1e-12 || mean.Count != 3 || mean.Total != 3 {
		t.Fatalf("match-weighted mean = %+v", mean)
	}
	expected := exploreTrendMeanFor(teams, 0, true)
	if expected.Value == nil || *expected.Value != 2 || expected.Count != 2 || expected.Total != 3 {
		t.Fatalf("available-xG mean = %+v", expected)
	}
	points := exploreTrendMeanFor(teams, 3, true)
	if points.Value == nil || math.Abs(*points.Value-4.0/3) > 1e-12 || points.Count != 3 {
		t.Fatalf("independent xPoints mean = %+v", points)
	}
	for _, measure := range []int{0, 1, 3} {
		holders := exploreTrendBestTeams(teams, measure, false, 2)
		if len(holders) != 1 || holders[0].ID != "a" {
			t.Fatalf("direction for metric %d: %+v", measure, holders)
		}
	}
	if holders := exploreTrendBestTeams(teams, 0, true, 2); holders != nil {
		t.Fatalf("partial xG cannot identify league best: %+v", holders)
	}
	if holders := exploreTrendBestTeams(teams, 3, true, 2); len(holders) != 1 || holders[0].ID != "b" {
		t.Fatalf("complete xPoints must remain usable: %+v", holders)
	}
	teams[1].Matches[0].Values[0].Actual = 5
	teams[1].Matches[1].Values[0].Actual = 3
	if holders := exploreTrendBestTeams(teams, 0, false, 2); len(holders) != 2 {
		t.Fatalf("tied holders = %+v", holders)
	}
	teams[1].Matches[1].Values[0].Actual += .00001
	if holders := exploreTrendBestTeams(teams, 0, false, 2); len(holders) != 1 || holders[0].ID != "b" {
		t.Fatalf("must compare before rounding: %+v", holders)
	}
	if holders := exploreTrendBestTeams(teams, 0, false, 3); holders != nil {
		t.Fatal("unplayed league team must withhold best")
	}
}

func exploreBenchmarkArchive(t *testing.T, year string, active bool) []cache.HistoricalSeason {
	t.Helper()
	lifecycle := cache.SourceScopeCompleted
	if active {
		lifecycle = cache.SourceScopeActive
	}
	archive := historyArchive(t, map[string]historyArchiveState{year: {lifecycle: lifecycle}})
	data := &archive[0].Data
	data.Games = nil
	for i := range 10 {
		data.Teams = append(data.Teams, standings.Team{ID: fmt.Sprintf("team-%d", i), Name: fmt.Sprintf("Team %d", i)})
	}
	for i := range 10 {
		for j := i + 1; j < 10; j++ {
			data.Games = append(data.Games, cache.Game{ASAID: fmt.Sprintf("%d-%d", i, j), Season: year, Stage: "Regular Season", Status: fixtures.CompletedStatus,
				HomeTeamID: data.Teams[i].ID, AwayTeamID: data.Teams[j].ID, HomeScore: sql.NullInt64{Int64: int64(10 - i), Valid: true}, AwayScore: sql.NullInt64{Int64: 0, Valid: true}})
		}
	}
	return archive
}

func TestExploreTrendBenchmarksSeasonGroupsAndFallback(t *testing.T) {
	for _, year := range []string{"2016", "2021", "2026"} {
		archive := exploreBenchmarkArchive(t, year, false)
		summaries, err := history.SummarizeScoring(archive)
		if err != nil {
			t.Fatal(err)
		}
		teams, err := exploreTeams(nil, summaries, archive)
		if err != nil {
			t.Fatal(err)
		}
		season := teams.TeamSeasons[0]
		if len(season.Benchmarks) != 5 {
			t.Fatalf("benchmarks = %+v", season.Benchmarks)
		}
		league := season.Benchmarks[0]
		wantTop4, wantShield := "Top 4 average", "Shield winner"
		if season.Benchmarks[2].Label != wantTop4 || season.Benchmarks[3].Label != wantShield {
			t.Fatalf("comparison labels = %q / %q", season.Benchmarks[2].Label, season.Benchmarks[3].Label)
		}
		if *league.Means[0][0].Value != *league.Means[1][0].Value {
			t.Fatal("league scored and allowed must share the exact same benchmark")
		}
		for _, benchmark := range season.Benchmarks {
			count := map[string]int{"league": 10, "playoff": archive[0].Entry.PlayoffPlaces, "top-four": 4, "shield": 1}[benchmark.Key]
			if benchmark.Key == "best" {
				continue
			}
			mean := benchmark.Means[0][0]
			if mean.Value == nil || len(mean.Names) != count || mean.Total != count*9 {
				t.Fatalf("%s %s: %+v", year, benchmark.Key, mean)
			}
			if benchmark.Key == "shield" && !slices.Equal(mean.Names, []string{"Team 0"}) {
				t.Fatalf("Shield favorite/winner = %+v", mean)
			}
			query := url.Values{"trend-view": {"relative"}, "average-reference": {benchmark.Key}, "trend-mode": {"match"}}
			page, err := exploreSeasonTrend(query, teams, teamNameView{ID: "team-9"})
			if err != nil || len(page.TrendReferences) != 4 {
				t.Fatalf("references = %+v, %v", page, err)
			}
			for _, mode := range []string{"match", "rolling"} {
				query.Set("trend-mode", mode)
				response := httptest.NewRecorder()
				store := &historyHTTPStore{archive: archive}
				NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend&season="+year+"&team=team-9&"+query.Encode(), nil))
				body := response.Body.String()
				if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
					t.Fatalf("cache-only fallback: %d, %d/%d", response.Code, store.archiveCalls, store.seasonCalls)
				}
				if !strings.Contains(body, benchmark.Label) || !strings.Contains(body, "Benchmark values, teams and coverage") || !strings.Contains(body, page.TrendReferences[1].Value+" per match") {
					t.Fatal("benchmark fallback omitted values or membership")
				}
			}
		}
	}
}

func TestExploreTrendBenchmarksWithholdUnresolvedGroups(t *testing.T) {
	archive := exploreBenchmarkArchive(t, "2026", false)
	for i := range archive[0].Data.Games {
		archive[0].Data.Games[i].HomeScore.Int64 = 0
	}
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, benchmark := range teams.TeamSeasons[0].Benchmarks {
		if slices.Contains([]string{"playoff", "top-four", "shield"}, benchmark.Key) && (benchmark.Means[0][0].Value != nil || !strings.Contains(benchmark.Note, "tie")) {
			t.Fatalf("arbitrary standings order certified group: %+v", benchmark)
		}
	}
	// Historical rules are not inferred from the current season. Even a tie
	// resolved by 2026's goal-difference order cannot certify an older cut line.
	archive = exploreBenchmarkArchive(t, "2016", false)
	for i := range archive[0].Data.Games {
		game := &archive[0].Data.Games[i]
		if game.HomeTeamID == "team-3" && game.AwayTeamID == "team-4" {
			game.HomeScore.Int64, game.AwayScore.Int64 = 0, 0
		}
	}
	summaries, err = history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err = exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	if teams.TeamSeasons[0].Benchmarks[1].Means[0][0].Value != nil {
		t.Fatal("historical points tie at playoff cut needs verified historical rules")
	}
}

func TestExploreSeasonTrendPointsCoverageRollingAndBenchmarkSelection(t *testing.T) {
	archive := exploreBenchmarkArchive(t, "2026", true)
	for i, game := range archive[0].Data.Games {
		xg := cache.GameXG{GameID: game.ASAID, Availability: cache.XGAvailable, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID,
			HomeXPoints: sql.NullFloat64{Float64: float64(i % 3), Valid: i != 1}, AwayXPoints: sql.NullFloat64{Float64: 0, Valid: true}}
		archive[0].Data.XGoals = append(archive[0].Data.XGoals, xg)
	}
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{"trend-view": {"points"}, "trend-mode": {"match"}, "series": {"xg"}}
	page, err := exploreSeasonTrend(query, teams, teamNameView{ID: "team-0"})
	if err != nil || page.TrendMissingXG || !page.TrendMissingXPoints || !slices.Equal(page.TrendRows[0].DisplayValues, []string{"3.00", "0.00"}) || page.TrendRows[1].DisplayValues[1] != "Unavailable" {
		t.Fatalf("points with independently missing xG/xPoints = %+v, %v", page, err)
	}
	for _, window := range []string{"3", "5"} {
		query.Set("trend-mode", "rolling")
		query.Set("window", window)
		page, err = exploreSeasonTrend(query, teams, teamNameView{ID: "team-0"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(page.TrendRows[0].DisplayValues, []string{"3.00", "0.00"}) || page.TrendRows[1].DisplayValues[1] != "Unavailable" || page.TrendRows[2].DisplayValues[1] != "Unavailable" || page.TrendRows[4].DisplayValues[0] != "3.00" {
			t.Fatal("points must show expanding averages and then full windows, retaining missing xPoints")
		}
		if page.TrendRows[4].DisplayValues[1] == "Unavailable" && window == "3" {
			t.Fatal("xPoints must recover when missing observation leaves the window")
		}
		if page.TrendRows[4].DisplayValues[1] != "Unavailable" && window == "5" {
			t.Fatal("xPoints must leave gaps throughout affected windows")
		}
	}
	query.Set("trend-view", "relative")
	query.Set("average-series", "xpoints")
	query.Set("average-reference", "best")
	page, err = exploreSeasonTrend(query, teams, teamNameView{ID: "team-0"})
	if err != nil || len(page.TrendColumns) != 1 || page.TrendColumns[0].Label != "xPoints" || len(page.TrendReferences) != 2 || page.TrendReferences[1].Value != "Unavailable" || !strings.Contains(page.TrendReferenceStatus, "Best team average unavailable for xPoints") {
		t.Fatalf("xPoints benchmark must honor independent coverage and selection: %+v, %v", page, err)
	}
	response := httptest.NewRecorder()
	NewHandler(&historyHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend&season=2026&team=team-0&trend-view=points&trend-mode=match", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<th scope="col">xPoints</th>`) || !strings.Contains(response.Body.String(), `data-trend-points-warning>`) || !strings.Contains(response.Body.String(), `data-trend-warning hidden`) {
		t.Fatal("points fallback must render points and its own coverage warning")
	}
}
