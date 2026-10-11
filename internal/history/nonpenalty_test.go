package history

import (
	"math"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

func withSplit(xg cache.GameXG, split cache.PenaltySplit) cache.GameXG {
	xg.Penalties = &split
	return xg
}

func summarizeBasis(t *testing.T, input cache.HistoricalSeason, basis ScoringBasis) SeasonScoring {
	t.Helper()
	summaries, err := SummarizeScoringBasis([]cache.HistoricalSeason{input}, basis)
	if err != nil {
		t.Fatal(err)
	}
	return summaries[0]
}

// twentyGames returns 20 completed 2-1 games; the first len(xg) carry data.
func twentyGames() []cache.Game {
	games := make([]cache.Game, 0, 20)
	for index := range 20 {
		games = append(games, testGame(string(rune('a'+index)), fixtures.CompletedStatus, 2, 1))
	}
	return games
}

func TestNonPenaltyArithmeticAndPointsInvariance(t *testing.T) {
	games := twentyGames()
	var xg []cache.GameXG
	for index, game := range games {
		observation := testXG(game, 2.00, 1.00, true, 2.1, .3)
		switch index {
		case 0:
			// Home 2.00 xG includes a .76 penalty and 2 goals include one.
			observation = withSplit(observation, cache.PenaltySplit{HomeXG: .76, HomeGoals: 1, HomeShots: 1})
		case 1:
			// A penalty xG above the total clamps the side at zero.
			observation = withSplit(observation, cache.PenaltySplit{AwayXG: 1.5, AwayGoals: 1, AwayShots: 2})
		default:
			observation = withSplit(observation, cache.PenaltySplit{})
		}
		xg = append(xg, observation)
	}
	input := testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))
	all, np := summarizeBasis(t, input, AllShots), summarizeBasis(t, input, NonPenalty)

	if all.TotalGoals != 60 || all.GoalsCovered != 20 || all.Basis != AllShots {
		t.Fatalf("all-shots totals = %+v", all)
	}
	// 60 goals minus the two penalty goals; xG 60 minus .76 and the clamped 1.00.
	if np.TotalGoals != 58 || np.GoalsCovered != 20 || np.XGCovered != 20 || np.Basis != NonPenalty || !np.PlotEligible {
		t.Fatalf("non-penalty totals = %+v", np)
	}
	assertFloat(t, np.GoalsPerMatch, 58.0/20)
	assertFloat(t, np.XGPerMatch, (60-.76-1.00)/20)
	assertFloat(t, np.GoalsMinusXGPerMatch, 58.0/20-(60-.76-1.00)/20)
	if np.GoalBins[2] != 2 || np.GoalBins[3] != 18 {
		t.Fatalf("non-penalty goal bins = %v, want games 0 and 1 to move from 3 goals to 2", np.GoalBins)
	}

	byID := func(summary SeasonScoring) map[string]TeamScoring {
		result := map[string]TeamScoring{}
		for _, team := range summary.Teams {
			result[team.TeamID] = team
		}
		return result
	}
	allTeams, npTeams := byID(all), byID(np)
	home, away := npTeams["home"], npTeams["away"]
	if home.GoalsFor != 39 || home.GoalsAgainst != 19 || away.GoalsFor != 19 || away.GoalsAgainst != 39 || home.GoalsCovered != 20 {
		t.Fatalf("non-penalty team goals: home=%+v away=%+v", home, away)
	}
	assertFloat(t, home.XGFor, 40-.76)
	assertFloat(t, home.XGAgainst, 20-1.00)
	for id, team := range npTeams {
		official := allTeams[id]
		if team.Points != official.Points || team.Played != official.Played || team.XPointsCovered != official.XPointsCovered ||
			team.XPoints == nil || official.XPoints == nil || math.Abs(*team.XPoints-*official.XPoints) > 1e-9 {
			t.Fatalf("%s points or xPts changed with the basis: %+v vs %+v", id, team, official)
		}
		for index, match := range team.Matches {
			if match.Points != official.Matches[index].Points || match.XPoints == nil || *match.XPoints != *official.Matches[index].XPoints {
				t.Fatalf("%s match %d points/xPts changed with the basis", id, index)
			}
			if match.ScoreFor != official.Matches[index].GoalsFor || match.ScoreAgainst != official.Matches[index].GoalsAgainst {
				t.Fatalf("%s match %d official score changed with the basis", id, index)
			}
		}
	}
	// The first game: home scored 2 with one penalty, and had 2.00 xG with .76.
	first := npTeams["home"].Matches[0]
	if first.GoalsFor != 1 || first.GoalsAgainst != 1 || first.ScoreFor != 2 || first.Points != 3 {
		t.Fatalf("first match = %+v, want 1-1 non-penalty goals with the official 2-1 win", first)
	}
	assertFloat(t, first.XGFor, 2.00-.76)
	assertFloat(t, first.XGAgainst, 1.00)
	second := npTeams["home"].Matches[1]
	assertFloat(t, second.XGAgainst, 0)
}

func TestNonPenaltyCoverageAndExclusion(t *testing.T) {
	build := func(splits int, xgCovered int) cache.HistoricalSeason {
		games := twentyGames()
		var xg []cache.GameXG
		for index, game := range games {
			if index >= xgCovered {
				break
			}
			observation := testXG(game, 1.5, .5, false, 0, 0)
			if index < splits {
				observation = withSplit(observation, cache.PenaltySplit{HomeXG: .5, HomeGoals: 1, HomeShots: 1})
			}
			xg = append(xg, observation)
		}
		return testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))
	}

	// All-shots output ignores splits entirely, so a missing split is harmless.
	if all := summarizeBasis(t, build(19, 20), AllShots); !all.PlotEligible || all.GoalsCovered != 20 || all.XGCovered != 20 {
		t.Fatalf("all-shots changed by missing splits: %+v", all)
	}

	incomplete := summarizeBasis(t, build(19, 20), NonPenalty)
	if incomplete.PlotEligible || !slices.Contains(incomplete.Exclusions, "incomplete_non_penalty_data") {
		t.Fatalf("one game without a split must exclude the season: %+v", incomplete.Exclusions)
	}
	if incomplete.GoalsCovered != 19 || incomplete.XGCovered != 19 || incomplete.XGPerMatch != nil || incomplete.GoalsMinusXGPerMatch != nil {
		t.Fatalf("coverage = goals %d xG %d, XGPerMatch %v", incomplete.GoalsCovered, incomplete.XGCovered, incomplete.XGPerMatch)
	}
	// Rates use only the split-covered games: 19 games with 2 goals, one a penalty... 19*(3-1)=38.
	if incomplete.TotalGoals != 38 {
		t.Fatalf("TotalGoals = %d, want 38 from the 19 covered games", incomplete.TotalGoals)
	}
	assertFloat(t, incomplete.GoalsPerMatch, 2)
	if sum := incomplete.GoalBins[2]; sum != 19 {
		t.Fatalf("goal bins = %v, want 19 covered games with 2 non-penalty goals", incomplete.GoalBins)
	}
	if incomplete.TeamComparisonEligible() {
		t.Error("an incomplete non-penalty season must not support team comparison")
	}
	for _, team := range incomplete.Teams {
		if team.GoalsCovered != 19 || team.Played != 20 {
			t.Fatalf("team %s coverage = %d of %d, want 19 of 20", team.TeamID, team.GoalsCovered, team.Played)
		}
		if team.XGFor != nil || team.XGAgainst != nil {
			t.Fatalf("team %s published xG from incomplete coverage", team.TeamID)
		}
	}

	// Without xG, or when a split overstates goals, the game has no basis.
	if noXG := summarizeBasis(t, build(0, 0), NonPenalty); noXG.GoalsCovered != 0 || noXG.GoalsPerMatch != nil || noXG.PlotEligible {
		t.Fatalf("no xG: %+v", noXG)
	}
	overstated := build(20, 20)
	overstated.Data.XGoals[3].Penalties.HomeGoals = 3
	if summary := summarizeBasis(t, overstated, NonPenalty); summary.GoalsCovered != 19 || summary.PlotEligible {
		t.Fatalf("a split with more penalty goals than goals must not count: %+v", summary)
	}

	complete := summarizeBasis(t, build(20, 20), NonPenalty)
	if !complete.PlotEligible || !complete.TeamComparisonEligible() || len(complete.Exclusions) != 0 {
		t.Fatalf("complete non-penalty season: %+v", complete.Exclusions)
	}
	assertFloat(t, complete.XGPerMatch, 1.5)
}

func TestNonPenaltyZeroValueIsAllShots(t *testing.T) {
	if AllShots != 0 || AllShots.ExcludesPenalties() || !NonPenalty.ExcludesPenalties() {
		t.Fatal("AllShots must be the zero value")
	}
	input := testSeason("2016", twentyGames(), nil, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))
	got, want := oneSummary(t, input), summarizeBasis(t, input, AllShots)
	if got.TotalGoals != want.TotalGoals || got.GoalsCovered != 20 || got.Basis != AllShots {
		t.Fatalf("default summary = %+v", got)
	}
}
