package history

import (
	"database/sql"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

func TestScoringGoalsAndCompleteXG(t *testing.T) {
	t.Parallel()
	games := []cache.Game{
		testGame("zero", fixtures.CompletedStatus, 0, 0),
		testGame("one", fixtures.CompletedStatus, 1, 0),
		testGame("two", fixtures.CompletedStatus, 1, 1),
		testGame("three", fixtures.CompletedStatus, 2, 1),
		testGame("four", fixtures.CompletedStatus, 3, 2),
	}
	xg := make([]cache.GameXG, 0, len(games))
	for _, game := range games {
		xg = append(xg, testXG(game, 1, 1, true, 1.5, 1.5))
	}

	for _, test := range []struct {
		name          string
		xg            []cache.GameXG
		wantCoverage  int
		wantXGPresent bool
	}{
		{name: "fully covered", xg: xg, wantCoverage: 5, wantXGPresent: true},
		{name: "one xG pair absent", xg: xg[:4], wantCoverage: 4, wantXGPresent: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := oneSummary(t, testSeason("2016", games, test.xg, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)))
			if got.Played != 5 || got.TotalGoals != 11 || got.GoalBins != [5]int{1, 1, 1, 1, 1} {
				t.Fatalf("goal summary = %+v", got)
			}
			assertFloat(t, got.GoalsPerMatch, 2.2)
			if got.XGCovered != test.wantCoverage || (got.XGPerMatch != nil) != test.wantXGPresent || (got.GoalsMinusXGPerMatch != nil) != test.wantXGPresent {
				t.Fatalf("xG coverage/rates = %+v", got)
			}
			if test.wantXGPresent {
				assertFloat(t, got.XGPerMatch, 2)
				assertFloat(t, got.GoalsMinusXGPerMatch, .2)
			}
			if got.PlotEligible || !slices.Contains(got.Exclusions, "below_minimum_matches") {
				t.Fatalf("short season eligibility = %+v", got)
			}
		})
	}
}

func TestScoringCoverageValidation(t *testing.T) {
	t.Parallel()
	games := []cache.Game{
		testGame("good", fixtures.CompletedStatus, 1, 0),
		testGame("missing-points", fixtures.CompletedStatus, 1, 0),
		testGame("points-only", fixtures.CompletedStatus, 1, 0),
		testGame("unavailable", fixtures.CompletedStatus, 1, 0),
		testGame("nan", fixtures.CompletedStatus, 1, 0),
		testGame("infinite", fixtures.CompletedStatus, 1, 0),
		testGame("negative", fixtures.CompletedStatus, 1, 0),
		testGame("wrong-team", fixtures.CompletedStatus, 1, 0),
		testGame("above-three", fixtures.CompletedStatus, 1, 0),
		testGame("null-xg-side", fixtures.CompletedStatus, 1, 0),
		testGame("null-xpoints-side", fixtures.CompletedStatus, 1, 0),
	}
	nullXGSide := testXG(games[9], 1, 1, true, 1, 2)
	nullXGSide.HomeXG.Valid = false
	nullXPointsSide := testXG(games[10], 1, 1, true, 1, 2)
	nullXPointsSide.AwayXPoints.Valid = false
	xg := []cache.GameXG{
		testXG(games[0], 0, 0, true, 0, 3),
		testXG(games[1], 1, 1, false, 0, 0),
		testXG(games[2], math.NaN(), math.NaN(), true, 1, 2),
		{GameID: games[3].ASAID, Availability: cache.XGUnavailable, HomeTeamID: "home", AwayTeamID: "away"},
		testXG(games[4], math.NaN(), 1, true, 1, 2),
		testXG(games[5], math.Inf(1), 1, true, 1, 2),
		testXG(games[6], -1, 1, true, 1, 2),
		{GameID: games[7].ASAID, Availability: cache.XGAvailable, HomeTeamID: "other", AwayTeamID: "away", HomeXG: floatValue(1), AwayXG: floatValue(1), HomeXPoints: floatValue(1), AwayXPoints: floatValue(2)},
		testXG(games[8], 1, 1, true, 3.01, 0),
		nullXGSide,
		nullXPointsSide,
		{GameID: "orphan", Availability: cache.XGAvailable, HomeTeamID: "home", AwayTeamID: "away", HomeXG: floatValue(1), AwayXG: floatValue(1), HomeXPoints: floatValue(1), AwayXPoints: floatValue(2)},
	}
	got := oneSummary(t, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)))
	if got.XGCovered != 4 || got.XPointsCovered != 6 {
		t.Fatalf("independent coverage = %+v", got)
	}
	if got.XGPerMatch != nil || got.GoalsMinusXGPerMatch != nil {
		t.Fatalf("partial xG rates = %+v", got)
	}
	if got.GoalBins[1] != 11 || got.TotalGoals != 11 {
		t.Fatalf("orphan observation affected scoring = %+v", got)
	}

	disabled := testSeason("2016", games[:1], xg[:1], availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	disabled.Entry.Capabilities = []competition.Capability{competition.CapabilityFixtures}
	got = oneSummary(t, disabled)
	if got.XGCovered != 0 || got.XPointsCovered != 0 || got.XGPerMatch != nil {
		t.Fatalf("capability-disabled xG = %+v", got)
	}
}

func TestScoringStatusesAndEligibility(t *testing.T) {
	t.Parallel()
	invalid := testGame("invalid", fixtures.CompletedStatus, 0, 0)
	invalid.HomeScore = sql.NullInt64{}
	negative := testGame("negative", fixtures.CompletedStatus, 0, 0)
	negative.AwayScore.Int64 = -1
	statuses := []cache.Game{
		testGame("played", fixtures.CompletedStatus, 2, 1),
		testGame("pre", fixtures.PreMatchStatus, 8, 0),
		testGame("abandoned", fixtures.AbandonedStatus, 4, 1),
		testGame("unknown", "Delayed", 5, 0),
		invalid,
		negative,
	}
	got := oneSummary(t, testSeason("2016", statuses, nil, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)))
	if got.InventoryGames != 6 || got.Played != 1 || got.Pending != 2 || got.Abandoned != 1 || got.InvalidCompleted != 2 || got.TotalGoals != 3 {
		t.Fatalf("status summary = %+v", got)
	}
	if !slices.Contains(got.Exclusions, "historical_results_incomplete") || !slices.Contains(got.Exclusions, "invalid_completed_results") {
		t.Fatalf("status exclusions = %v", got.Exclusions)
	}

	for _, test := range []struct {
		name       string
		season     string
		readiness  *cache.SeasonReadinessSnapshot
		games      []cache.Game
		wantPlot   bool
		wantReason []string
	}{
		{name: "nineteen matches", season: "2016", readiness: availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown), games: scoredGames(19), wantReason: []string{"below_minimum_matches"}},
		{name: "twenty active matches", season: "2016", readiness: availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown), games: scoredGames(20), wantPlot: true},
		{name: "twenty completed complete matches", season: "2016", readiness: availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete), games: scoredGames(20), wantPlot: true},
		{name: "twenty valid plus invalid completed", season: "2016", readiness: availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown), games: append(scoredGames(20), invalid), wantReason: []string{"invalid_completed_results"}},
		{name: "completed with pending", season: "2016", readiness: availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete), games: append(scoredGames(20), testGame("pending", fixtures.PreMatchStatus, 0, 0)), wantReason: []string{"historical_results_incomplete"}},
		{name: "active with pending", season: "2016", readiness: availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessComplete), games: append(scoredGames(20), testGame("pending", fixtures.PreMatchStatus, 0, 0)), wantPlot: true},
		{name: "upcoming", season: "2016", readiness: availableReadiness(cache.SourceScopeUpcoming, cache.InventoryCompletenessUnknown), games: scoredGames(20), wantReason: []string{"upcoming"}},
		{name: "incomplete inventory", season: "2016", readiness: availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessIncomplete), games: scoredGames(20), wantReason: []string{"inventory_incomplete"}},
		{name: "unavailable source", season: "2016", readiness: unavailableReadiness(), games: scoredGames(20), wantReason: []string{"source_unavailable"}},
		{name: "missing scope", season: "2016", games: scoredGames(20), wantReason: []string{"source_unavailable", "lifecycle_unknown"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := oneSummary(t, testSeason(test.season, test.games, nil, test.readiness))
			if got.PlotEligible != test.wantPlot {
				t.Fatalf("PlotEligible = %v, exclusions = %v", got.PlotEligible, got.Exclusions)
			}
			for _, reason := range test.wantReason {
				if !slices.Contains(got.Exclusions, reason) {
					t.Fatalf("exclusions %v do not contain %q", got.Exclusions, reason)
				}
			}
		})
	}
}

func TestScoringEmptyAndPendingAvoidDivisionByZero(t *testing.T) {
	t.Parallel()
	for _, games := range [][]cache.Game{nil, {testGame("pending", fixtures.PreMatchStatus, 2, 1), testGame("abandoned", fixtures.AbandonedStatus, 2, 1)}} {
		got := oneSummary(t, testSeason("2016", games, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)))
		if got.GoalsPerMatch != nil || got.XGPerMatch != nil || got.GoalsMinusXGPerMatch != nil {
			t.Fatalf("empty scoring rates = %+v", got)
		}
		if got.Played != 0 || got.GoalBins != [5]int{} || got.InventoryGames != got.Played+got.Pending+got.Abandoned+got.InvalidCompleted {
			t.Fatalf("empty/status accounting = %+v", got)
		}
	}
}

func TestScoringUnavailableAndEmptyRowsRemainDistinct(t *testing.T) {
	t.Parallel()
	activeGames := gamesForSeason("2016", scoredGames(20))
	unavailableGames := gamesForSeason("2017", scoredGames(20))
	summaries, err := SummarizeScoring([]cache.HistoricalSeason{
		testSeason("2018", nil, nil, nil),
		testSeason("2017", unavailableGames, nil, unavailableReadiness()),
		testSeason("2016", activeGames, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 3 || summaries[0].Season != "2016" || summaries[1].Season != "2017" || summaries[2].Season != "2018" {
		t.Fatalf("ordered rows = %+v", summaries)
	}
	if !summaries[0].PlotEligible || summaries[0].Inventory != cache.InventoryCompletenessUnknown {
		t.Fatalf("active unknown-inventory row = %+v", summaries[0])
	}
	if summaries[1].Readiness != cache.SourceReadinessUnknown || summaries[1].Lifecycle != cache.SourceScopeActive || summaries[1].Played != 20 || !reflect.DeepEqual(summaries[1].Exclusions, []string{"source_unavailable"}) {
		t.Fatalf("unavailable row = %+v", summaries[1])
	}
	if summaries[2].Readiness != cache.SourceReadinessUnknown || summaries[2].Lifecycle != "" || summaries[2].InventoryGames != 0 || !reflect.DeepEqual(summaries[2].Exclusions, []string{"source_unavailable", "lifecycle_unknown", "below_minimum_matches"}) {
		t.Fatalf("missing empty row = %+v", summaries[2])
	}
}

func TestScoringFixtureShuffleIsDeterministic(t *testing.T) {
	t.Parallel()
	games := []cache.Game{
		testGame("zero", fixtures.CompletedStatus, 0, 0),
		testGame("two", fixtures.CompletedStatus, 1, 1),
		testGame("five", fixtures.CompletedStatus, 3, 2),
	}
	xg := []cache.GameXG{testXG(games[0], 1, 0, true, 1, 2), testXG(games[1], 1, 1, true, 1, 2), testXG(games[2], 2, 1, true, 1, 2)}
	shuffledGames := []cache.Game{games[2], games[0], games[1]}
	shuffledXG := []cache.GameXG{xg[1], xg[2], xg[0]}
	first := oneSummary(t, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)))
	second := oneSummary(t, testSeason("2016", shuffledGames, shuffledXG, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("shuffled summary = %+v, want %+v", second, first)
	}
}

func TestScoringRejectsBadInputAndDoesNotMutate(t *testing.T) {
	t.Parallel()
	valid := testSeason("2016", []cache.Game{testGame("one", fixtures.CompletedStatus, 1, 0)}, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	for _, test := range []struct {
		name   string
		inputs []cache.HistoricalSeason
	}{
		{name: "duplicate season", inputs: []cache.HistoricalSeason{valid, valid}},
		{name: "duplicate fixture ID", inputs: []cache.HistoricalSeason{testSeason("2016", []cache.Game{testGame("one", fixtures.CompletedStatus, 1, 0), testGame("one", fixtures.CompletedStatus, 1, 0)}, nil, nil)}},
		{name: "duplicate xG ID", inputs: []cache.HistoricalSeason{testSeason("2016", []cache.Game{testGame("one", fixtures.CompletedStatus, 1, 0)}, []cache.GameXG{testXG(testGame("one", fixtures.CompletedStatus, 1, 0), 1, 1, true, 1, 2), testXG(testGame("one", fixtures.CompletedStatus, 1, 0), 1, 1, true, 1, 2)}, nil)}},
		{name: "blank fixture ID", inputs: []cache.HistoricalSeason{testSeason("2016", []cache.Game{testGame(" ", fixtures.CompletedStatus, 1, 0)}, nil, nil)}},
		{name: "scope mismatch", inputs: []cache.HistoricalSeason{testSeason("2016", []cache.Game{{ASAID: "one", Season: "2016", Stage: "Playoffs"}}, nil, nil)}},
		{name: "cup input", inputs: []cache.HistoricalSeason{{Entry: competition.Entry{Season: "2016", Stage: "Playoffs", Public: true, SourceAvailable: true, Capabilities: []competition.Capability{competition.CapabilityFixtures}}}}},
		{name: "goal overflow", inputs: []cache.HistoricalSeason{testSeason("2016", []cache.Game{testGame("one", fixtures.CompletedStatus, math.MaxInt64, 1)}, nil, nil)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := SummarizeScoring(test.inputs); err == nil {
				t.Fatal("SummarizeScoring succeeded")
			}
		})
	}

	later := testGame("later", fixtures.CompletedStatus, 1, 0)
	later.Season = "2021"
	first := testSeason("2021", []cache.Game{later}, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	second := testSeason("2016", []cache.Game{testGame("earlier", fixtures.CompletedStatus, 1, 0)}, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	inputs := []cache.HistoricalSeason{first, second}
	before := append([]cache.HistoricalSeason(nil), inputs...)
	got, err := SummarizeScoring(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Season != "2016" || got[1].Season != "2021" || !reflect.DeepEqual(inputs, before) {
		t.Fatalf("sort/mutation result=%+v inputs=%+v before=%+v", got, inputs, before)
	}
}

func oneSummary(t *testing.T, input cache.HistoricalSeason) SeasonScoring {
	t.Helper()
	summaries, err := SummarizeScoring([]cache.HistoricalSeason{input})
	if err != nil {
		t.Fatal(err)
	}
	return summaries[0]
}

func testSeason(season string, games []cache.Game, xg []cache.GameXG, readiness *cache.SeasonReadinessSnapshot) cache.HistoricalSeason {
	return cache.HistoricalSeason{
		Entry:     competition.Entry{Season: season, Stage: "Regular Season", Public: true, SourceAvailable: true, Capabilities: []competition.Capability{competition.CapabilityFixtures, competition.CapabilityXG}},
		Readiness: readiness,
		Data:      cache.SeasonData{Games: games, XGoals: xg},
	}
}

func testGame(id, status string, home, away int64) cache.Game {
	return cache.Game{ASAID: id, Season: "2016", Stage: "Regular Season", Status: status, HomeTeamID: "home", AwayTeamID: "away", HomeScore: sql.NullInt64{Int64: home, Valid: true}, AwayScore: sql.NullInt64{Int64: away, Valid: true}}
}

func scoredGames(count int) []cache.Game {
	games := make([]cache.Game, 0, count)
	for i := range count {
		games = append(games, testGame(string(rune('a'+i)), fixtures.CompletedStatus, 1, 0))
	}
	return games
}

func gamesForSeason(season string, games []cache.Game) []cache.Game {
	seasonGames := append([]cache.Game(nil), games...)
	for index := range seasonGames {
		seasonGames[index].Season = season
	}
	return seasonGames
}

func testXG(game cache.Game, home, away float64, points bool, homePoints, awayPoints float64) cache.GameXG {
	xg := cache.GameXG{GameID: game.ASAID, Availability: cache.XGAvailable, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID, HomeXG: floatValue(home), AwayXG: floatValue(away)}
	if points {
		xg.HomeXPoints = floatValue(homePoints)
		xg.AwayXPoints = floatValue(awayPoints)
	}
	return xg
}

func floatValue(value float64) sql.NullFloat64 { return sql.NullFloat64{Float64: value, Valid: true} }

func availableReadiness(lifecycle cache.SourceScopeLifecycle, inventory cache.InventoryCompleteness) *cache.SeasonReadinessSnapshot {
	return &cache.SeasonReadinessSnapshot{Scope: cache.SourceScope{Lifecycle: lifecycle}, Readiness: cache.SourceReadinessAvailable, Completeness: inventory}
}

func unavailableReadiness() *cache.SeasonReadinessSnapshot {
	return &cache.SeasonReadinessSnapshot{Scope: cache.SourceScope{Lifecycle: cache.SourceScopeActive}, Readiness: cache.SourceReadinessUnknown, Completeness: cache.InventoryCompletenessUnknown}
}

func assertFloat(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Fatalf("value = %v, want %v", got, want)
	}
}
