package history

import (
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

func summarizeHome(t *testing.T, basis ScoringBasis, inputs ...cache.HistoricalSeason) []SeasonHomeAdvantage {
	t.Helper()
	got, err := SummarizeHomeAdvantage(inputs, basis)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// homeAdvantageGames returns 20 completed games: ten 2-0 home wins, five 1-1
// draws and five 0-1 away wins.
func homeAdvantageGames() []cache.Game {
	games := make([]cache.Game, 0, 20)
	for index := range 20 {
		home, away := int64(2), int64(0)
		switch {
		case index >= 15:
			home, away = 0, 1
		case index >= 10:
			home, away = 1, 1
		}
		games = append(games, testGame(string(rune('a'+index)), fixtures.CompletedStatus, home, away))
	}
	return games
}

func TestHomeAdvantageActualAndExpectedGaps(t *testing.T) {
	t.Parallel()
	games := homeAdvantageGames()
	xg := make([]cache.GameXG, 0, len(games))
	for _, game := range games {
		// Home 1.5 xG and 2 xPoints against away 1.0 xG and 0.5 xPoints.
		xg = append(xg, testXG(game, 1.5, 1, true, 2, .5))
	}
	got := summarizeHome(t, AllShots, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)))[0]

	if !got.PlotEligible || got.Played != 20 || got.HomeWins != 10 || got.Draws != 5 || got.AwayWins != 5 {
		t.Fatalf("season = %+v", got)
	}
	// Goals: 10 × (+2) + 5 × 0 + 5 × (−1) = 15. Points: 10 × 3 − 5 × 3 = 15.
	assertFloat(t, got.Goals, 15.0/20)
	assertFloat(t, got.Points, 15.0/20)
	assertFloat(t, got.XG, .5)
	assertFloat(t, got.XPoints, 1.5)
}

func TestHomeAdvantagePartialExpectedDataKeepsActualValues(t *testing.T) {
	t.Parallel()
	games := homeAdvantageGames()
	var xg []cache.GameXG
	for index, game := range games {
		switch index {
		case 0:
			// xG without xPoints: only the xPoints rate is missing.
			xg = append(xg, testXG(game, 1.5, 1, false, 0, 0))
		case 1:
			// Unavailable xG, so neither expected rate is complete.
			xg = append(xg, cache.GameXG{GameID: game.ASAID, Availability: cache.XGUnavailable, HomeTeamID: "home", AwayTeamID: "away"})
		default:
			xg = append(xg, testXG(game, 1.5, 1, true, 2, .5))
		}
	}
	got := summarizeHome(t, AllShots, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)))[0]
	if !got.PlotEligible || got.XG != nil || got.XPoints != nil {
		t.Fatalf("partial expected rates = %+v", got)
	}
	assertFloat(t, got.Goals, 15.0/20)
	assertFloat(t, got.Points, 15.0/20)

	// Complete xG with one match missing xPoints keeps xG and drops xPoints.
	xg = xg[:0]
	for index, game := range games {
		xg = append(xg, testXG(game, 1.5, 1, index != 3, 2, .5))
	}
	got = summarizeHome(t, AllShots, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)))[0]
	if got.XPoints != nil {
		t.Fatalf("xPoints with one match missing = %v, want none", *got.XPoints)
	}
	assertFloat(t, got.XG, .5)

	// Complete xPoints with one unavailable xG keeps xPoints and drops xG.
	xg = xg[:0]
	for index, game := range games {
		observation := testXG(game, 1.5, 1, true, 2, .5)
		if index == 3 {
			observation.HomeXG.Valid = false
		}
		xg = append(xg, observation)
	}
	got = summarizeHome(t, AllShots, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)))[0]
	if got.XG != nil {
		t.Fatalf("xG with one match missing = %v, want none", *got.XG)
	}
	assertFloat(t, got.XPoints, 1.5)
}

func TestHomeAdvantageIgnoresAbandonedAndRejectsMalformedFixtures(t *testing.T) {
	t.Parallel()
	ready := availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)

	abandoned := append(homeAdvantageGames(), testGame("abandoned", fixtures.AbandonedStatus, 3, 0))
	got := summarizeHome(t, AllShots, testSeason("2016", abandoned, nil, ready))[0]
	if !got.PlotEligible || got.Played != 20 || got.HomeWins != 10 {
		t.Fatalf("abandoned fixture counted: %+v", got)
	}
	assertFloat(t, got.Goals, 15.0/20)

	malformed := testGame("malformed", fixtures.CompletedStatus, 1, 0)
	malformed.AwayScore.Valid = false
	got = summarizeHome(t, AllShots, testSeason("2016", append(homeAdvantageGames(), malformed), nil, ready))[0]
	if got.PlotEligible || got.Goals != nil || got.Points != nil || !slices.Contains(got.Exclusions, "invalid_completed_results") {
		t.Fatalf("malformed completed fixture = %+v", got)
	}

	short := summarizeHome(t, AllShots, testSeason("2016", homeAdvantageGames()[:19], nil, ready))[0]
	if short.PlotEligible || short.Goals != nil || short.Played != 19 || !slices.Contains(short.Exclusions, "below_minimum_matches") {
		t.Fatalf("short season = %+v", short)
	}
}

func TestHomeAdvantageNonPenaltyBasisChangesOnlyGoalsAndXG(t *testing.T) {
	t.Parallel()
	games := homeAdvantageGames()
	var xg []cache.GameXG
	for index, game := range games {
		observation := testXG(game, 1.5, 1, true, 2, .5)
		split := cache.PenaltySplit{}
		if index == 0 {
			// The first 2-0 home win includes one home penalty goal worth .76 xG.
			split = cache.PenaltySplit{HomeXG: .76, HomeGoals: 1, HomeShots: 1}
		}
		xg = append(xg, withSplit(observation, split))
	}
	input := testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))
	all, np := summarizeHome(t, AllShots, input)[0], summarizeHome(t, NonPenalty, input)[0]

	if np.Basis != NonPenalty || !np.PlotEligible {
		t.Fatalf("non-penalty season = %+v", np)
	}
	assertFloat(t, all.Goals, 15.0/20)
	assertFloat(t, np.Goals, 14.0/20)
	assertFloat(t, all.XG, .5)
	assertFloat(t, np.XG, (.5*20-.76)/20)
	assertFloat(t, np.Points, 15.0/20)
	assertFloat(t, np.XPoints, 1.5)
	if np.HomeWins != all.HomeWins || np.Draws != all.Draws || np.AwayWins != all.AwayWins {
		t.Fatalf("results changed with the basis: all=%+v np=%+v", all, np)
	}

	// A played match without a penalty split leaves the season incomplete on
	// this basis, exactly as it does in the scoring summary.
	xg[5].Penalties = nil
	incomplete := summarizeHome(t, NonPenalty, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)))[0]
	if incomplete.PlotEligible || incomplete.Goals != nil || !slices.Contains(incomplete.Exclusions, "incomplete_non_penalty_data") {
		t.Fatalf("season without a complete split = %+v", incomplete)
	}
}

func TestHomeAdvantageKeepsActiveSeasonsAndSortsSeasons(t *testing.T) {
	t.Parallel()
	completed := testSeason("2018", gamesForSeason("2018", homeAdvantageGames()), nil, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))
	active := testSeason("2019", gamesForSeason("2019", homeAdvantageGames()), nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	got := summarizeHome(t, AllShots, active, completed)
	if len(got) != 2 || got[0].Season != "2018" || got[1].Season != "2019" {
		t.Fatalf("seasons = %+v", got)
	}
	if got[0].Lifecycle != cache.SourceScopeCompleted || got[1].Lifecycle != cache.SourceScopeActive || !got[1].PlotEligible || got[1].Played != 20 {
		t.Fatalf("lifecycles = %+v", got)
	}
	assertFloat(t, got[1].Goals, 15.0/20)
	if got[1].XG != nil || got[1].XPoints != nil {
		t.Fatalf("expected rates without xG = %+v", got[1])
	}
}
