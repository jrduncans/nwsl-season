package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestScoreConditionTextMatchesIssueExamples(t *testing.T) {
	teams := map[string]string{"por": "POR", "uta": "UTA", "kc": "KC", "la": "LA"}
	gd := func(minimum, team, rival int) scenarios.ScoreLevel {
		return scenarios.ScoreLevel{Measure: scenarios.MeasureGoalDifference, MinimumGain: minimum, TeamNow: team, RivalNow: rival}
	}
	for _, test := range []struct {
		condition scenarios.ScoreCondition
		want      string
	}{
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{gd(1, 7, 7)}}, "POR gains 1+ goal difference on UTA (level at +7 now)"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "kc", Levels: []scenarios.ScoreLevel{gd(6, 0, 5)}}, "POR gains 6+ goal difference on KC (KC +5 now)"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "la", Levels: []scenarios.ScoreLevel{gd(11, 2, 12), {Measure: scenarios.MeasureGoalsScored, MinimumGain: 0, TeamNow: 40, RivalNow: 40}}}, "POR gains 11+ goal difference on LA (LA +10 now), or exactly 10 and loses no goals scored (level at 40 now)"},
		{scenarios.ScoreCondition{TeamID: "uta", RivalID: "kc", Levels: []scenarios.ScoreLevel{gd(6, 7, 12), {Measure: scenarios.MeasureGoalsScored, MinimumGain: 7, TeamNow: 44, RivalNow: 50}}}, "UTA gains 6+ goal difference on KC (KC +5 now), or exactly 5 and gains 7+ goals scored (KC +6 now)"},
		{scenarios.ScoreCondition{TeamID: "uta", RivalID: "por", Levels: []scenarios.ScoreLevel{gd(1, 7, 7), {Measure: scenarios.MeasureGoalsScored, MinimumGain: -3, TeamNow: 44, RivalNow: 48}}}, "UTA gains 1+ goal difference on POR (level at +7 now), or none and loses at most 3 goals scored (POR +4 now)"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1, ExactOnly: true, TeamNow: 7, RivalNow: 7}, {Measure: scenarios.MeasureGoalsScored, MinimumGain: -3, ExactOnly: true, TeamNow: 48, RivalNow: 44}}},
			"POR finishes level with UTA on goal difference and goals scored (level at +7 now, POR +4 goals scored now)"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1, ExactOnly: true, TeamNow: 7, RivalNow: 7}, {Measure: scenarios.MeasureGoalsScored, MinimumGain: 1, ExactOnly: true, TeamNow: 48, RivalNow: 48}, {Measure: scenarios.MeasureHeadToHeadGoals, MinimumGain: 2, TeamNow: 2, RivalNow: 3}}},
			"POR finishes level with UTA on goal difference and goals scored (level at +7 now, level at 48 goals scored now), and gains 2+ head-to-head goals (UTA +1 now)"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{gd(0, 9, 6)}}, "POR loses no goal difference to UTA (POR +3 now)"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1, ExactOnly: true, TeamNow: 7, RivalNow: 7}, {Measure: scenarios.MeasureGoalsScored, MinimumGain: 1, TeamNow: 30, RivalNow: 31}, {Measure: scenarios.MeasureHeadToHeadGoals, MinimumGain: 1, TeamNow: 1, RivalNow: 1}}},
			"POR finishes level with UTA on goal difference (level at +7 now), and gains 1+ goals scored (UTA +1 now), or none and gains 1+ head-to-head goals (level at 1 now)"},
	} {
		if got := scoreConditionText(test.condition, teams); got != test.want {
			t.Errorf("got %q, want %q", got, test.want)
		}
	}
}

// seasonEndData leaves two matchdays: future-4 then future-5.
func seasonEndData(remaining int) cache.SeasonData {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	for i := range data.Games {
		if data.Games[i].Status == "PreMatch" && i <= len(data.Games)-1-remaining {
			data.Games[i].Status = standings.CompletedStatus
			data.Games[i].HomeScore = sql.NullInt64{Int64: 1, Valid: true}
			data.Games[i].AwayScore = sql.NullInt64{Int64: 1, Valid: true}
		}
	}
	return data
}

func seasonScenarioStore(data cache.SeasonData, nextIDs, seasonIDs []string, season *cache.ScenarioSnapshot) fullFakeStore {
	next := scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCanClinch, CanClinch: true, Clauses: []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{testScenarioCondition(nextIDs[0], 1)}}}}
	slate := scenarios.Slate{State: scenarios.SlateReady, StartsAtUTC: time.Date(2026, 7, 14, 19, 0, 0, 0, time.UTC), LatestKickoffUTC: time.Date(2026, 7, 14, 19, 0, 0, 0, time.UTC), FixtureIDs: nextIDs}
	if season != nil {
		season.Run.Slate = scenarios.Slate{State: scenarios.SlateReady, Source: scenarios.SourceRestOfSeason, StartsAtUTC: slate.StartsAtUTC, LatestKickoffUTC: time.Date(2026, 7, 15, 19, 0, 0, 0, time.UTC), FixtureIDs: seasonIDs}
	}
	return fullFakeStore{fakeStore: fakeStore{season: data}, scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: slate}, Results: []cache.ScenarioResult{{Result: next}}}, seasonScenario: season}
}

func seasonResult() *cache.ScenarioSnapshot {
	condition := scenarios.ScoreCondition{TeamID: "alpha", RivalID: "bravo", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1, TeamNow: 1, RivalNow: 1}}}
	result := scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityCanClinch, CanClinch: true, DisciplinaryPossible: true, Limitation: scenarios.LimitationSeasonMultiwayTie,
		Clauses: []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{testScenarioCondition("future-4", 1)}, ScoreConditions: []scenarios.ScoreCondition{condition}}}}
	return &cache.ScenarioSnapshot{Results: []cache.ScenarioResult{{Result: result}}}
}

func getClinching(t *testing.T, store fullFakeStore, query string) string {
	t.Helper()
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching"+query, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	return response.Body.String()
}

func TestClinchingPageSwitchesToRestOfSeason(t *testing.T) {
	store := seasonScenarioStore(seasonEndData(2), []string{"future-4"}, []string{"future-4", "future-5"}, seasonResult())
	next := getClinching(t, store, "")
	if !strings.Contains(next, `data-segmented="view"`) || !strings.Contains(next, `value="next-matchday" checked`) {
		t.Fatal("penultimate matchweek should default to Next matchday with a switch")
	}
	if strings.Contains(next, "goal difference") {
		t.Fatal("next-matchday view must not show score conditions")
	}
	rest := getClinching(t, store, "?view=rest-of-season")
	if !strings.Contains(rest, `value="rest-of-season" checked`) {
		t.Fatal("rest-of-season option should be selected")
	}
	for _, want := range []string{"gains 1&#43; goal difference on", "(level at &#43;1 now)", "clinching-condition-score", "Disciplinary points may decide", "three or more teams finish level", "2 matches"} {
		if !strings.Contains(rest, want) {
			t.Errorf("rest-of-season view lacks %q", want)
		}
	}
}

func TestClinchingPageShowsOnlyRestOfSeasonOnDecisionDay(t *testing.T) {
	store := seasonScenarioStore(seasonEndData(1), []string{"future-5"}, []string{"future-5"}, seasonResult())
	store.seasonScenario.Results[0].Clauses[0].Conditions = []scenarios.FixtureCondition{testScenarioCondition("future-5", 1)}
	body := getClinching(t, store, "")
	if strings.Contains(body, `data-segmented="view"`) {
		t.Fatal("decision day has no view switch")
	}
	if !strings.Contains(body, "gains 1&#43; goal difference on") {
		t.Fatal("decision day shows rest-of-season scenarios")
	}
}

func TestClinchingPageRestOfSeasonPending(t *testing.T) {
	store := seasonScenarioStore(seasonEndData(2), []string{"future-4"}, nil, nil)
	body := getClinching(t, store, "?view=rest-of-season")
	if !strings.Contains(body, "Recalculation pending.") || !strings.Contains(body, `data-segmented="view"`) {
		t.Fatal("a missing rest-of-season batch is pending, and the switch stays available")
	}
}

func TestClinchingGroupsKeepScoreConditionsIntact(t *testing.T) {
	teams := map[string]string{"a": "ALP", "b": "BRA", "c": "CIT", "d": "DEN"}
	games := map[string]cache.Game{"own": {HomeTeamID: "a", AwayTeamID: "b"}, "x": {HomeTeamID: "c", AwayTeamID: "d"}}
	c := testScenarioCondition
	score := []scenarios.ScoreCondition{{TeamID: "a", RivalID: "c", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 2, TeamNow: 3, RivalNow: 4}}}}
	clauses := []scenarios.Clause{
		{Conditions: []scenarios.FixtureCondition{c("own", 2), c("x", 4)}},
		{Conditions: []scenarios.FixtureCondition{c("own", 2), c("x", 3)}, ScoreConditions: score},
		// Covered by the first clause, so it adds nothing.
		{Conditions: []scenarios.FixtureCondition{c("own", 2), c("x", 4)}, ScoreConditions: score},
	}
	groups := clinchingGroups(clauses, "a", teams, games)
	if len(groups) != 1 {
		t.Fatalf("groups = %#v", groups)
	}
	combinations := groups[0].Help.Combinations()
	if len(combinations) != 2 {
		t.Fatalf("combinations = %+v", combinations)
	}
	withScore := 0
	for _, combination := range combinations {
		for _, requirement := range combination {
			if requirement.Score != nil {
				withScore++
				if requirement.Text != "ALP gains 2+ goal difference on CIT (CIT +1 now)" {
					t.Errorf("score text = %q", requirement.Text)
				}
			}
		}
	}
	if withScore != 1 {
		t.Fatalf("want one score condition, kept with the CIT-win-or-draw alternative: %+v", combinations)
	}
}

func TestClinchingGroupsMergeIdenticalTwoMatchResultsIntoAPointsTotal(t *testing.T) {
	// WAS clinches with any result but two losses; then it needs CHA to win.
	teams := map[string]string{"was": "WAS", "kc": "KC", "chi": "CHI", "cha": "CHA", "den": "DEN"}
	games := map[string]cache.Game{
		"a": {HomeTeamID: "kc", AwayTeamID: "was", KickoffUTC: "2026-10-25T21:00:00Z"},
		"b": {HomeTeamID: "was", AwayTeamID: "chi", KickoffUTC: "2026-11-01T22:00:00Z"},
		"h": {HomeTeamID: "cha", AwayTeamID: "den", KickoffUTC: "2026-11-01T22:00:00Z"},
	}
	c := testScenarioCondition
	clauses := []scenarios.Clause{
		testScenarioClause(c("a", 6)),
		testScenarioClause(c("a", 1), c("b", 3)),
		testScenarioClause(c("a", 1), c("b", 4), c("h", 1)),
	}
	groups := clinchingGroups(clauses, "was", teams, games)
	if len(groups) != 2 || groups[0].Heading != "If WAS earns at least 1 point from these two matches: at KC and vs CHI" || len(groups[0].Summary.Combinations()) != 1 || len(groups[0].Summary.Combinations()[0]) != 0 {
		t.Fatalf("groups = %+v", groups)
	}
	assertScenarioCoverage(t, clauses, groups, games, true)
}
