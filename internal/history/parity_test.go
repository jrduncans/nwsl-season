package history

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

func summarizeParity(t *testing.T, inputs ...cache.HistoricalSeason) []SeasonParity {
	t.Helper()
	got, err := SummarizeParity(inputs, AllShots)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func assertNear(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

// parityGames returns 24 completed games among teams a, b, c and d, each pair
// meeting four times: a wins every match, and every other match is a 1-1 draw.
// So a earns 3 PPG, and b, c and d each earn 8 points from 12 matches.
func parityGames(season string) []cache.Game {
	teams := []string{"a", "b", "c", "d"}
	var games []cache.Game
	for leg := range 4 {
		for i := range teams {
			for j := i + 1; j < len(teams); j++ {
				home, away := teams[i], teams[j]
				homeScore, awayScore := int64(1), int64(1)
				if home == "a" {
					homeScore = 2
				}
				if leg%2 == 1 {
					home, away, homeScore, awayScore = away, home, awayScore, homeScore
				}
				game := testGame(fmt.Sprintf("%s-%d-%s-%s", season, leg, home, away), fixtures.CompletedStatus, homeScore, awayScore)
				game.Season, game.HomeTeamID, game.AwayTeamID = season, home, away
				games = append(games, game)
			}
		}
	}
	return games
}

func parityXG(games []cache.Game, skip string) []cache.GameXG {
	var xg []cache.GameXG
	for _, game := range games {
		xg = append(xg, testXG(game, 1.2, 1, game.ASAID != skip, 1.5, 1))
	}
	return xg
}

func TestChanceSpreadFollowsTheDrawRateNullModel(t *testing.T) {
	t.Parallel()
	// No draws: every match is 3 or 0 points, so σ = 1.5 and nine matches give 0.5.
	assertNear(t, "no draws", ChanceSpread(0, 9), .5)
	// Only draws: every team earns one point a match, with no spread at all.
	assertNear(t, "only draws", ChanceSpread(1, 9), 0)
	// A quarter draws: E[x²] = 4.5 × .75 + .25 = 3.625 and E[x] = 1.375.
	assertNear(t, "quarter draws", ChanceSpread(.25, 22), math.Sqrt((3.625-1.375*1.375)/22))
	// More matches narrow the chance spread.
	if ChanceSpread(.25, 26) >= ChanceSpread(.25, 12) {
		t.Error("chance spread does not narrow with more matches")
	}
}

func TestQuantileInterpolatesBetweenClosestRanks(t *testing.T) {
	t.Parallel()
	values := []float64{1, 2, 3, 4, 5}
	for _, test := range []struct{ p, want float64 }{{0, 1}, {.25, 2}, {.5, 3}, {.75, 4}, {1, 5}} {
		assertNear(t, fmt.Sprintf("Quantile(%v)", test.p), Quantile(values, test.p), test.want)
	}
	// Four values: Q1 sits a quarter of the way from the first to the second.
	assertNear(t, "Q1 of four", Quantile([]float64{0, 4, 8, 12}, .25), 3)
	assertNear(t, "Q3 of four", Quantile([]float64{0, 4, 8, 12}, .75), 9)
	assertNear(t, "single value", Quantile([]float64{2}, .75), 2)
	if !math.IsNaN(Quantile(nil, .5)) {
		t.Error("Quantile of no values is not NaN")
	}
}

func TestParityMeasuresTheWholeLeague(t *testing.T) {
	t.Parallel()
	games := parityGames("2016")
	got := summarizeParity(t, testSeason("2016", games, parityXG(games, ""), availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)))[0]
	if !got.PlotEligible || got.Played != 24 || len(got.Teams) != 4 {
		t.Fatalf("season = %+v", got)
	}
	if got.Teams[0].TeamID != "a" || got.Teams[0].PPG != 3 || got.Teams[0].Points != 36 || got.Teams[0].Played != 12 {
		t.Fatalf("first team = %+v, want a on 3 PPG", got.Teams[0])
	}
	// PPG 3, 2/3, 2/3, 2/3: mean 1.25, so the population variance is
	// (1.75² + 3 × (7/12)²) / 4, not divided by three as a sample would be.
	assertNear(t, "spread", got.Spread, math.Sqrt((1.75*1.75+3*(7.0/12)*(7.0/12))/4))
	// Half the matches are draws and each team plays 12.
	assertNear(t, "draw rate", got.DrawRate, .5)
	assertNear(t, "matches per team", got.MatchesPerTeam, 12)
	assertNear(t, "chance", got.Chance, ChanceSpread(.5, 12))
	assertNear(t, "gap", got.Gap, 3-8.0/12)
	assertNear(t, "Q1", got.Q1, 8.0/12)
	// Q3 sits a quarter of the way from 2/3 to 3.
	assertNear(t, "Q3", got.Q3, 8.0/12+.25*(3-8.0/12))
	assertNear(t, "IQR", got.IQR(), .25*(3-8.0/12))
	if !slices.Equal(got.Top, []string{"a"}) || !slices.Equal(got.Bottom, []string{"b", "c", "d"}) {
		t.Errorf("extremes = %v / %v, want a / every tied team b, c, d", got.Top, got.Bottom)
	}

	// Every match gives home 1.5 and away 1 xPoints. a hosts six of its twelve
	// matches, as does everyone, so every team's xPPG is 1.25 and the spread is 0.
	if got.XSpread == nil {
		t.Fatal("complete xPoints left no xPPG spread")
	}
	assertNear(t, "xPPG spread", *got.XSpread, 0)
	for _, team := range got.Teams {
		if team.XPPG == nil {
			t.Fatalf("team %s has no xPPG", team.TeamID)
		}
		assertNear(t, team.TeamID+" xPPG", *team.XPPG, 1.25)
	}
}

func TestParityPartialXPointsKeepsActualValues(t *testing.T) {
	t.Parallel()
	games := parityGames("2016")
	got := summarizeParity(t, testSeason("2016", games, parityXG(games, games[3].ASAID), availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete)))[0]
	if !got.PlotEligible || got.XSpread != nil {
		t.Fatalf("partial xPoints = %+v, want an eligible season without an xPPG spread", got)
	}
	if got.Spread == 0 || got.Chance == 0 || got.Gap == 0 {
		t.Fatalf("actual values missing: %+v", got)
	}
	// Teams in the match without xPoints lose their xPPG; the others keep theirs.
	var missing []string
	for _, team := range got.Teams {
		if team.XPPG == nil {
			missing = append(missing, team.TeamID)
		}
	}
	if want := []string{games[3].HomeTeamID, games[3].AwayTeamID}; len(missing) != 2 || !slices.Contains(missing, want[0]) || !slices.Contains(missing, want[1]) {
		t.Errorf("teams without xPPG = %v, want %v", missing, want)
	}
}

func TestParityKeepsActiveSeasonsAndSkipsIneligibleOnes(t *testing.T) {
	t.Parallel()
	completed := testSeason("2018", parityGames("2018"), nil, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))
	// The active season has played 20 of its matches so far.
	active := testSeason("2019", parityGames("2019")[:20], nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	short := testSeason("2017", parityGames("2017")[:19], nil, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))
	got := summarizeParity(t, active, completed, short)
	if len(got) != 3 || got[0].Season != "2017" || got[1].Season != "2018" || got[2].Season != "2019" {
		t.Fatalf("seasons = %+v", got)
	}
	if got[0].PlotEligible || got[0].Teams != nil || got[0].Spread != 0 || !slices.Contains(got[0].Exclusions, "below_minimum_matches") {
		t.Errorf("short season = %+v, want no values", got[0])
	}
	if !got[2].PlotEligible || got[2].Lifecycle != cache.SourceScopeActive || got[2].Played != 20 {
		t.Fatalf("active season = %+v", got[2])
	}
	// Fewer matches per team so far widen the chance spread.
	if got[2].MatchesPerTeam != 10 || got[2].Chance <= got[1].Chance {
		t.Errorf("active chance = %v over %v matches, completed = %v", got[2].Chance, got[2].MatchesPerTeam, got[1].Chance)
	}
	if got[2].XSpread != nil {
		t.Errorf("xPPG spread without xPoints = %v", *got[2].XSpread)
	}
}

func TestParityNonPenaltyBasisKeepsPointsButSharesEligibility(t *testing.T) {
	t.Parallel()
	games := parityGames("2016")
	xg := parityXG(games, "")
	for index := range xg {
		xg[index] = withSplit(xg[index], cache.PenaltySplit{})
	}
	input := testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))
	all := summarizeParity(t, input)[0]
	np, err := SummarizeParity([]cache.HistoricalSeason{input}, NonPenalty)
	if err != nil {
		t.Fatal(err)
	}
	if !np[0].PlotEligible || np[0].Spread != all.Spread || np[0].Chance != all.Chance {
		t.Fatalf("non-penalty parity = %+v, want the all-shots values", np[0])
	}
	xg[5].Penalties = nil
	np, err = SummarizeParity([]cache.HistoricalSeason{testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessComplete))}, NonPenalty)
	if err != nil {
		t.Fatal(err)
	}
	if np[0].PlotEligible || !slices.Contains(np[0].Exclusions, "incomplete_non_penalty_data") {
		t.Errorf("season without a complete split = %+v", np[0])
	}
}
