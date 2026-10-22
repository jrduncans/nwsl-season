package history

import (
	"math"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func teamSeasonsSummary(t *testing.T, inputs ...cache.HistoricalSeason) []TeamSeason {
	t.Helper()
	rows, err := SummarizeTeamSeasons(inputs, AllShots)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func teamSeasonByID(t *testing.T, rows []TeamSeason, id, season string) TeamSeason {
	t.Helper()
	for _, row := range rows {
		if row.TeamID == id && row.Season == season {
			return row
		}
	}
	t.Fatalf("no %s in %s", id, season)
	return TeamSeason{}
}

func TestTeamSeasonsAllMeasuresAndRanks(t *testing.T) {
	t.Parallel()
	games := parityGames("2016")
	rows := teamSeasonsSummary(t, testSeason("2016", games, parityXG(games, ""), availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessUnknown)))
	a := teamSeasonByID(t, rows, "a", "2016")
	for _, tc := range []struct {
		measure               TeamSeasonMeasure
		actual, expected, gap float64
		ranks                 [3]int
	}{
		{TeamSeasonPoints, 3, 1.25, 1.75, [3]int{1, 1, 1}},
		{TeamSeasonFor, 2, 1.1, .9, [3]int{1, 1, 1}},
		{TeamSeasonAgainst, 1, 1.1, -.1, [3]int{1, 1, 1}},
		{TeamSeasonDifference, 1, 0, 1, [3]int{1, 1, 1}},
	} {
		value := a.Values[tc.measure]
		assertFloat(t, value.Actual, tc.actual)
		assertFloat(t, value.Expected, tc.expected)
		assertFloat(t, value.Gap, tc.gap)
		if value.Ranks != tc.ranks || !value.Eligible {
			t.Errorf("measure %d: %+v", tc.measure, value)
		}
	}
	// Everyone is tied on expected; actual PPG skips from a's rank 1 to rank 2.
	for _, id := range []string{"b", "c", "d"} {
		v := teamSeasonByID(t, rows, id, "2016").Values[TeamSeasonPoints]
		if v.Ranks != [3]int{2, 1, 2} {
			t.Errorf("%s: %+v", id, v)
		}
	}
}

func TestTeamSeasonsPPGTiesNamesAndSeparateSeasons(t *testing.T) {
	t.Parallel()
	var inputs []cache.HistoricalSeason
	for _, year := range []string{"2016", "2017"} {
		games := parityGames(year)
		input := testSeason(year, games, parityXG(games, ""), availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessUnknown))
		input.Data.Teams = []standings.Team{{ID: "b", Name: "Zulu"}, {ID: "c", Name: "Alpha"}, {ID: "d", Name: "Alpha"}}
		inputs = append(inputs, input)
	}
	rows := teamSeasonsSummary(t, inputs...)
	if len(rows) != 8 {
		t.Fatalf("rows %+v", rows)
	}
	// Same identity in separate seasons retains both rows and both tied ranks.
	for _, year := range []string{"2016", "2017"} {
		a := teamSeasonByID(t, rows, "a", year)
		b := teamSeasonByID(t, rows, "b", year)
		if a.Values[TeamSeasonPoints].Ranks[0] != 1 || b.Values[TeamSeasonPoints].Ranks[0] != 3 {
			t.Fatalf("competition ranks: %+v %+v", a, b)
		}
	}
	if rows[0].TeamID != "c" || rows[0].Season != "2017" || rows[1].TeamID != "d" || rows[2].TeamID != "b" {
		t.Fatalf("tie ordering: %+v", rows)
	}
	// A real PPG tie: Gotham and Washington's 56 / 26, with distinct xPoints.
	actual := 56.0 / 26
	summaries := []SeasonScoring{{Season: "2024", Lifecycle: cache.SourceScopeCompleted, Played: 26, Teams: []TeamScoring{
		{TeamID: "gotham", Played: 26, Points: 56, GoalsCovered: 26}, {TeamID: "spirit", Played: 26, Points: 56, GoalsCovered: 26},
	}}}
	tied := TeamSeasons(summaries, summaries, nil)
	for _, row := range tied {
		assertFloat(t, row.Values[TeamSeasonPoints].Actual, actual)
		if row.Values[TeamSeasonPoints].Ranks[0] != 1 {
			t.Error("PPG tie split")
		}
	}
}

func TestTeamSeasonsCoverageActualDoesNotNeedExpected(t *testing.T) {
	t.Parallel()
	games := parityGames("2016")
	xg := parityXG(games, games[3].ASAID)
	xg[1].HomeXG.Float64 = math.NaN()
	rows := teamSeasonsSummary(t, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessUnknown)))
	for _, row := range rows {
		missingPoints := row.TeamID == games[3].HomeTeamID || row.TeamID == games[3].AwayTeamID
		missingGoals := row.TeamID == games[1].HomeTeamID || row.TeamID == games[1].AwayTeamID
		for measure := TeamSeasonFor; measure <= TeamSeasonPoints; measure++ {
			v := row.Values[measure]
			missing := missingGoals
			if measure == TeamSeasonPoints {
				missing = missingPoints
			}
			if v.Actual == nil || v.Ranks[0] == 0 || (v.Expected == nil) != missing || (v.Gap == nil) != missing || (v.Ranks[1] == 0) != missing || (v.Ranks[2] == 0) != missing {
				t.Errorf("coverage %s measure %d: %+v", row.TeamID, measure, v)
			}
		}
	}
}

func TestTeamSeasonsGoalsAllowedRanksLowestFirst(t *testing.T) {
	t.Parallel()
	expectedA, expectedB := 3.0, 1.0
	summaries := []SeasonScoring{{Season: "2024", Played: 20, Lifecycle: cache.SourceScopeCompleted, Teams: []TeamScoring{
		{TeamID: "a", Played: 10, GoalsCovered: 10, GoalsAgainst: 2, XGAgainst: &expectedA},
		{TeamID: "b", Played: 10, GoalsCovered: 10, GoalsAgainst: 4, XGAgainst: &expectedB},
	}}}
	rows := TeamSeasons(summaries, summaries, nil)
	a, b := teamSeasonByID(t, rows, "a", "2024"), teamSeasonByID(t, rows, "b", "2024")
	if a.Values[TeamSeasonAgainst].Ranks != [3]int{1, 2, 1} || b.Values[TeamSeasonAgainst].Ranks != [3]int{2, 1, 2} {
		t.Fatalf("allowed direction: %+v", rows)
	}
	assertFloat(t, a.Values[TeamSeasonAgainst].Gap, -.1)
}

func TestTeamSeasonsActiveGateEveryMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		count              int
		idle, active, want bool
	}{{18, false, true, false}, {20, false, true, true}, {20, true, true, false}, {1, false, false, true}} {
		full := parityGames("2016")
		games := full[:tc.count]
		if tc.count == 20 {
			games = append(full[:18:18], full[18], full[23])
		}
		lifecycle := cache.SourceScopeCompleted
		if tc.active {
			lifecycle = cache.SourceScopeActive
		}
		input := testSeason("2016", games, parityXG(games, ""), availableReadiness(lifecycle, cache.InventoryCompletenessUnknown))
		if tc.idle {
			input.Data.Teams = []standings.Team{{ID: "idle", Name: "Idle"}}
		}
		for _, row := range teamSeasonsSummary(t, input) {
			for _, v := range row.Values {
				if v.Eligible != tc.want {
					t.Errorf("gate %+v: %+v", tc, v)
				}
				for _, rank := range v.Ranks {
					if (rank > 0) != tc.want {
						t.Errorf("gate rank %+v: %+v", tc, v)
					}
				}
			}
		}
	}
}

func TestTeamSeasonsBasisPointsEligibilityAndSeasonIntegrity(t *testing.T) {
	t.Parallel()
	games := parityGames("2016")
	xg := parityXG(games, "")
	for i := range xg {
		xg[i] = withSplit(xg[i], cache.PenaltySplit{HomeGoals: 1, HomeXG: .3, HomeShots: 1})
	}
	input := testSeason("2016", games, xg, availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessUnknown))
	all := teamSeasonsSummary(t, input)
	np, err := SummarizeTeamSeasons([]cache.HistoricalSeason{input}, NonPenalty)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range np {
		p, old := row.Values[TeamSeasonPoints], all[i].Values[TeamSeasonPoints]
		if p.Ranks != old.Ranks || *p.Actual != *old.Actual || *p.Gap != *old.Gap {
			t.Fatal("basis changed points")
		}
		assertNear(t, "NP scored gap", *row.Values[TeamSeasonFor].Gap, *all[i].Values[TeamSeasonFor].Gap-.35)
		assertNear(t, "NP allowed gap", *row.Values[TeamSeasonAgainst].Gap, *all[i].Values[TeamSeasonAgainst].Gap-.35)
	}
	xg[0].Penalties = nil
	np, err = SummarizeTeamSeasons([]cache.HistoricalSeason{input}, NonPenalty)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range np {
		if row.Values[TeamSeasonPoints].Ranks[0] == 0 || !row.Values[TeamSeasonPoints].Eligible {
			t.Fatal("incomplete NP split changed points population")
		}
		if row.Values[TeamSeasonFor].Eligible || row.Values[TeamSeasonFor].Ranks != [3]int{} {
			t.Fatal("ineligible non-penalty goals ranked")
		}
	}
	input.Readiness.Completeness = cache.InventoryCompletenessIncomplete
	for _, row := range teamSeasonsSummary(t, input) {
		for _, v := range row.Values {
			if v.Eligible || v.Ranks != [3]int{} {
				t.Fatal("ineligible season ranked")
			}
		}
	}
}

func TestTeamSeasonsRecordedTeamWithoutResults(t *testing.T) {
	t.Parallel()
	games := parityGames("2016")
	input := testSeason("2016", games, parityXG(games, ""), availableReadiness(cache.SourceScopeCompleted, cache.InventoryCompletenessUnknown))
	input.Data.Teams = append(input.Data.Teams, standings.Team{ID: "idle", Name: "Idle"})
	row := teamSeasonByID(t, teamSeasonsSummary(t, input), "idle", "2016")
	for _, value := range row.Values {
		if value.Actual != nil || value.Eligible || value.Reason == "" {
			t.Fatalf("empty recorded team: %+v", value)
		}
	}
}
