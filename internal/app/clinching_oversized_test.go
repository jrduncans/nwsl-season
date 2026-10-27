package app

import (
	"net/url"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func TestOversizedThresholdCountsRenderedConditions(t *testing.T) {
	g := clinchingGroupView{Summary: scenarioExpression{Conditions: make([]scenarioRequirement, oversizedConditionLimit)}}
	if oversizedGroup(g) {
		t.Fatal("threshold itself should remain inline")
	}
	g.Summary.Alternatives = []scenarioExpression{{}}
	if !oversizedGroup(g) {
		t.Fatal("nested alternative should cross threshold")
	}
	g = clinchingGroupView{Summary: scenarioExpression{Conditions: []scenarioRequirement{{Score: &scenarios.ScoreCondition{Levels: make([]scenarios.ScoreLevel, oversizedConditionLimit)}}}}}
	if !oversizedGroup(g) {
		t.Fatal("score cascade must count toward size")
	}
}

func TestOversizedClausesIncludeOwnAndScore(t *testing.T) {
	games := rivalTestGames()
	game := games["sd-bos"]
	team := game.HomeTeamID
	group := clinchingGroupView{Own: []scenarioRequirement{{GameID: "sd-bos", Mask: maskHomeWin}}}
	score := scenarios.ScoreCondition{TeamID: team, RivalID: game.AwayTeamID, Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference}, {Measure: scenarios.MeasureGoalsScored}}}
	clauses := []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{{GameID: "sd-bos", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin, clinching.Draw}}}, ScoreConditions: []scenarios.ScoreCondition{score}}}
	got := clausesForGroup(clauses, group, team, games)
	if len(got) != 1 || len(got[0].Conditions) != 1 || got[0].Conditions[0].AllowedOutcomes[0] != clinching.HomeWin || len(got[0].ScoreConditions) != 1 || len(got[0].ScoreConditions[0].Levels) != 2 {
		t.Fatalf("lost requirements: %+v", got)
	}
}

func TestClinchingScenarioURLPreservesState(t *testing.T) {
	q := url.Values{"view": {"rest-of-season"}, "custom": {"retained"}}
	link := clinchingScenarioURL(q, "team", "playoffs", 2, 5)
	if !strings.Contains(link, "custom=retained") || !strings.Contains(link, "alternative=5") || !strings.HasSuffix(link, "#clinching-scenarios") {
		t.Fatal(link)
	}
	if q.Get("alternative") != "" {
		t.Fatal("mutated query")
	}
}

func TestOversizedScenarioCardsBoundedAndComplete(t *testing.T) {
	score := scenarios.ScoreCondition{TeamID: "sd", RivalID: "bos", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference}, {Measure: scenarios.MeasureGoalsScored}}}
	clauses := make([]scenarios.Clause, 101)
	for i := range clauses {
		clauses[i] = scenarios.Clause{Conditions: []scenarios.FixtureCondition{{GameID: "sd-bos", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin}}}, ScoreConditions: []scenarios.ScoreCondition{score}}
	}
	query := url.Values{"alternative": {"5"}, "view": {"rest-of-season"}, "scenario_team": {"sd"}}
	view, err := seedingAlternatives(scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Seed: 1, Clauses: clauses}}}, 1, "njy", query, rivalTestTeams, rivalTestGames(), nil, scenarioHistory{})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Scenarios) != 4 || view.First != 5 || view.Last != 8 || view.Previous == "" || view.Next == "" {
		t.Fatalf("unbounded or missing navigation: %+v", view)
	}
	for _, card := range view.Scenarios {
		if len(card.Conditions) != 2 || card.Conditions[1].Score == nil || len(card.Conditions[1].Score.Levels) != 2 {
			t.Fatalf("incomplete card: %+v", card)
		}
	}
}
