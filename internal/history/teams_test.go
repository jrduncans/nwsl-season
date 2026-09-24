package history

import (
	"math"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

func TestTeamScoringUsesBothVenuesAndPerTeamCoverage(t *testing.T) {
	games := []cache.Game{
		testGame("first", fixtures.CompletedStatus, 3, 1),
		testGame("second", fixtures.CompletedStatus, 0, 2),
		testGame("missing", fixtures.CompletedStatus, 1, 1),
		testGame("pending", fixtures.PreMatchStatus, 9, 9),
		testGame("abandoned", fixtures.AbandonedStatus, 9, 9),
		testGame("invalid", fixtures.CompletedStatus, -1, 2),
	}
	games[1].HomeTeamID, games[1].AwayTeamID = "third", "home"
	games[2].HomeTeamID, games[2].AwayTeamID = "away", "third"
	xg := []cache.GameXG{testXG(games[0], 1.25, 0, false, 0, 0), testXG(games[1], .75, 2.25, false, 0, 0)}
	summary := oneSummary(t, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)))
	if len(summary.Teams) != 3 || summary.XGPerMatch != nil {
		t.Fatalf("teams/league coverage: %+v", summary)
	}
	byID := make(map[string]TeamScoring)
	for _, team := range summary.Teams {
		byID[team.TeamID] = team
	}
	home := byID["home"]
	if home.Played != 2 || home.Points != 6 || home.GoalsFor != 5 || home.GoalsAgainst != 1 || home.XGCovered != 2 || home.XPointsCovered != 0 || home.XPoints != nil {
		t.Fatalf("home and away results not combined: %+v", home)
	}
	assertFloat(t, home.XGFor, 3.5)
	assertFloat(t, home.XGAgainst, .75)
	for id, want := range map[string][3]int64{"away": {2, 4, 1}, "third": {1, 3, 1}} {
		team := byID[id]
		if team.Played != 2 || team.GoalsFor != want[0] || team.GoalsAgainst != want[1] || int64(team.Points) != want[2] || team.XGCovered != 1 || team.XGFor != nil || team.XGAgainst != nil || team.XPoints != nil {
			t.Fatalf("partial team %s: %+v", id, team)
		}
	}
	if summary.TeamComparisonEligible() {
		t.Error("malformed completed result must prevent team comparison")
	}
}

func TestTeamScoringEligibilityAndZeroXG(t *testing.T) {
	game := testGame("zero", fixtures.CompletedStatus, 0, 0)
	input := testSeason("2016", []cache.Game{game}, []cache.GameXG{testXG(game, 0, 0, true, 0, 3)}, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	summary := oneSummary(t, input)
	if !summary.TeamComparisonEligible() || summary.PlotEligible {
		t.Fatal("one played match should support team comparison, not a league trend")
	}
	for _, team := range summary.Teams {
		if team.Points != 1 || team.XPointsCovered != 1 {
			t.Fatalf("draw points and zero xPoints coverage = %+v", team)
		}
		wantXPoints := 3.0
		if team.TeamID == "home" {
			wantXPoints = 0
		}
		assertFloat(t, team.XPoints, wantXPoints)
		assertFloat(t, team.XGFor, 0)
		assertFloat(t, team.XGAgainst, 0)
	}
	for _, mutate := range []func(*cache.HistoricalSeason){
		func(s *cache.HistoricalSeason) { s.Readiness.Completeness = cache.InventoryCompletenessIncomplete },
		func(s *cache.HistoricalSeason) { s.Readiness.Readiness = cache.SourceReadinessUnknown },
		func(s *cache.HistoricalSeason) { s.Readiness.Scope.Lifecycle = cache.SourceScopeUpcoming },
		func(s *cache.HistoricalSeason) {
			s.Readiness.Scope.Lifecycle = cache.SourceScopeCompleted
			s.Data.Games = append(s.Data.Games, testGame("pending", fixtures.PreMatchStatus, 0, 0))
		},
		func(s *cache.HistoricalSeason) { s.Data.Games, s.Data.XGoals = nil, nil },
	} {
		copyInput := input
		copyReadiness := *input.Readiness
		copyInput.Readiness = &copyReadiness
		mutate(&copyInput)
		if oneSummary(t, copyInput).TeamComparisonEligible() {
			t.Error("unsafe or empty season is eligible")
		}
	}
}

func TestTeamScoringExpectedPointsCoverageIsIndependentOfXG(t *testing.T) {
	t.Parallel()
	games := []cache.Game{
		testGame("home-win", fixtures.CompletedStatus, 2, 0),
		testGame("draw", fixtures.CompletedStatus, 1, 1),
		testGame("away-win", fixtures.CompletedStatus, 0, 1),
	}
	games[1].HomeTeamID, games[1].AwayTeamID = "third", "home"
	games[2].HomeTeamID, games[2].AwayTeamID = "away", "third"
	xg := []cache.GameXG{
		testXG(games[0], 1.5, .5, true, 2.25, .5),
		testXG(games[1], math.NaN(), 1, true, 1.125, 1.375),
		testXG(games[2], 1, 1, false, 0, 0),
	}
	summary := oneSummary(t, testSeason("2016", games, xg, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)))
	if summary.XGCovered != 2 || summary.XPointsCovered != 2 {
		t.Fatalf("metric coverage = %+v", summary)
	}
	byID := make(map[string]TeamScoring)
	for _, team := range summary.Teams {
		byID[team.TeamID] = team
	}
	home := byID["home"]
	if home.Played != 2 || home.Points != 4 || home.XGCovered != 1 || home.XGFor != nil || home.XGAgainst != nil || home.XPointsCovered != 2 {
		t.Fatalf("complete xPoints with partial xG = %+v", home)
	}
	assertFloat(t, home.XPoints, 3.625)
	away := byID["away"]
	if away.Played != 2 || away.Points != 0 || away.XGCovered != 2 || away.XPointsCovered != 1 || away.XPoints != nil {
		t.Fatalf("complete xG with partial xPoints = %+v", away)
	}
	assertFloat(t, away.XGFor, 1.5)
	third := byID["third"]
	if third.Played != 2 || third.Points != 4 || third.XPointsCovered != 1 || third.XPoints != nil {
		t.Fatalf("other team's missing xPoints affected own results = %+v", third)
	}
}

func TestTeamScoringRequiresMatchingAvailablePairedXG(t *testing.T) {
	game := testGame("one", fixtures.CompletedStatus, 2, 1)
	for name, change := range map[string]func(*cache.GameXG){
		"identity":         func(xg *cache.GameXG) { xg.HomeTeamID = "other" },
		"unavailable":      func(xg *cache.GameXG) { xg.Availability = cache.XGUnavailable },
		"one side missing": func(xg *cache.GameXG) { xg.AwayXG.Valid = false },
		"negative":         func(xg *cache.GameXG) { xg.HomeXG.Float64 = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			xg := testXG(game, 1, 1, true, 1, 1)
			change(&xg)
			summary := oneSummary(t, testSeason("2016", []cache.Game{game}, []cache.GameXG{xg}, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown)))
			for _, team := range summary.Teams {
				if team.Played != 1 || team.XGCovered != 0 || team.XGFor != nil || team.XGAgainst != nil {
					t.Fatalf("invalid xG affected team: %+v", team)
				}
			}
		})
	}
}
