package app

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestScoreConditionFinalStandingsWording(t *testing.T) {
	teams := map[string]string{"por": "POR", "uta": "UTA", "kc": "KC", "la": "LA"}
	gd := func(minimum, team, rival int) scenarios.ScoreLevel {
		return scenarios.ScoreLevel{Measure: scenarios.MeasureGoalDifference, MinimumGain: minimum, TeamNow: team, RivalNow: rival}
	}
	for _, test := range []struct {
		condition scenarios.ScoreCondition
		want      string
	}{
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{gd(1, 7, 7)}}, "POR must finish with a higher goal difference than UTA"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "kc", Levels: []scenarios.ScoreLevel{gd(6, 0, 5)}}, "POR must finish with a higher goal difference than KC"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "la", Levels: []scenarios.ScoreLevel{gd(11, 2, 12), {Measure: scenarios.MeasureGoalsScored, MinimumGain: 0, TeamNow: 40, RivalNow: 40}}}, "POR must finish with a higher goal difference than LA or, if level on goal difference, POR must finish with at least as many goals as LA"},
		{scenarios.ScoreCondition{TeamID: "uta", RivalID: "kc", Levels: []scenarios.ScoreLevel{gd(6, 7, 12), {Measure: scenarios.MeasureGoalsScored, MinimumGain: 7, TeamNow: 44, RivalNow: 50}}}, "UTA must finish with a higher goal difference than KC or, if level on goal difference, UTA must finish with more goals than KC"},
		{scenarios.ScoreCondition{TeamID: "uta", RivalID: "por", Levels: []scenarios.ScoreLevel{gd(1, 7, 7), {Measure: scenarios.MeasureGoalsScored, MinimumGain: -3, TeamNow: 44, RivalNow: 48}}}, "UTA must finish with a higher goal difference than POR or, if level on goal difference, UTA must finish with no more than 7 fewer goals than POR"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1, ExactOnly: true, TeamNow: 7, RivalNow: 7}, {Measure: scenarios.MeasureGoalsScored, MinimumGain: -3, ExactOnly: true, TeamNow: 48, RivalNow: 44}}},
			"POR must finish level on goal difference with UTA and POR must finish level on goals scored with UTA"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1, ExactOnly: true, TeamNow: 7, RivalNow: 7}, {Measure: scenarios.MeasureGoalsScored, MinimumGain: 1, ExactOnly: true, TeamNow: 48, RivalNow: 48}, {Measure: scenarios.MeasureHeadToHeadGoals, MinimumGain: 2, TeamNow: 2, RivalNow: 3}}},
			"POR must finish level on goal difference with UTA and POR must finish level on goals scored with UTA and POR must finish with more head-to-head goals than UTA"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{gd(0, 9, 6)}}, "POR must finish with a goal difference at least 3 higher than UTA"},
		{scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1, ExactOnly: true, TeamNow: 7, RivalNow: 7}, {Measure: scenarios.MeasureGoalsScored, MinimumGain: 1, TeamNow: 30, RivalNow: 31}, {Measure: scenarios.MeasureHeadToHeadGoals, MinimumGain: 1, TeamNow: 1, RivalNow: 1}}},
			"POR must finish level on goal difference with UTA and POR must finish with at least as many goals as UTA or, if level on goal difference and the final goals scored difference is -1, POR must finish with more head-to-head goals than UTA"},
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
	data := seasonEndData(2)
	for i := range data.Teams {
		if data.Teams[i].ID == "alpha" {
			data.Teams[i].Abbreviation = "ALP"
		}
	}
	store := seasonScenarioStore(data, []string{"future-4"}, []string{"future-4", "future-5"}, seasonResult())
	next := getClinching(t, store, "")
	if !strings.Contains(next, `data-segmented="view"`) || !strings.Contains(next, `value="next-matchday" checked`) {
		t.Fatal("penultimate matchweek should default to Next matchday with a switch")
	}
	if strings.Contains(next, "goal difference") {
		t.Fatal("next-matchday view must not show score conditions")
	}
	rest := getClinching(t, store, "?view=rest-of-season")
	forbidRaw(t, rest, "fixed results", "open scores", "Choosing a win, draw or loss")
	if !strings.Contains(rest, `value="rest-of-season" checked`) {
		t.Fatal("rest-of-season option should be selected")
	}
	for _, want := range []string{"must finish with a higher goal difference than", "The teams are currently level on goal difference at 1", "clinching-condition-score", "Disciplinary points may decide", "three or more teams finish level", "2 matches"} {
		if !strings.Contains(rest, want) {
			t.Errorf("rest-of-season view lacks %q", want)
		}
	}
	requireText(t, rest, ".clinching-result-header", "Win vs Bravo FC")
	forbidText(t, rest, ".clinching-result-header", "If ALP", "Alpha")
}

func TestClinchingSummaryStatesEverySharedRequirementOnce(t *testing.T) {
	shared := []scenarioRequirement{
		{GameID: "a", Mask: 1},
		{GameID: "b", Mask: 2},
		{Score: &scenarios.ScoreCondition{TeamID: "por", RivalID: "uta", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1}}}},
	}
	paths := [][]scenarioRequirement{
		append(append([]scenarioRequirement{}, shared...), scenarioRequirement{GameID: "c", Mask: 1}),
		append(append([]scenarioRequirement{}, shared...), scenarioRequirement{GameID: "d", Mask: 4}),
	}
	summary := factorRequirements(paths, 2)
	if len(summary.Conditions) != len(shared) || len(summary.Alternatives) != 2 {
		t.Fatalf("shared conditions should occupy one level: %+v", summary)
	}
	for _, branch := range summary.Alternatives {
		if len(branch.Conditions) != 1 || len(branch.Alternatives) != 0 {
			t.Fatalf("only the differing result belongs in an alternative: %+v", branch)
		}
	}
	if !reflect.DeepEqual(uniqueConjunctions(summary.Combinations()), uniqueConjunctions(paths)) {
		t.Fatal("factoring changed the represented scenarios")
	}
}

func TestClinchingPageShowsRestOfSeasonAndSeedingOnDecisionDay(t *testing.T) {
	store := seasonScenarioStore(seasonEndData(1), []string{"future-5"}, []string{"future-5"}, seasonResult())
	store.seasonScenario.Results[0].Clauses[0].Conditions = []scenarios.FixtureCondition{testScenarioCondition("future-5", 1)}
	body := getClinching(t, store, "")
	if !strings.Contains(body, `value="seeding"`) || strings.Contains(body, `value="next-matchday"`) {
		t.Fatal("decision day offers Rest of season and Seeding")
	}
	if !strings.Contains(body, "clinching-condition-score") {
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
				if requirement.Text != "ALP must finish with a higher goal difference than CIT" {
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

func seedingTestData() cache.SeasonData {
	data := seasonEndData(2)
	for i := range data.Teams {
		data.Teams[i].Abbreviation = strings.ToUpper(data.Teams[i].ID[:3])
	}
	return data
}

func seedingTestClause(conditions ...scenarios.FixtureCondition) scenarios.Clause {
	return scenarios.Clause{Conditions: conditions, RepresentedAssignments: 1, ProofMethods: []clinching.ProofMethod{clinching.ProofAccessibleTiebreak}}
}

func TestClinchingSeedingGridAndFixedResults(t *testing.T) {
	snapshot := seasonResult()
	clause := snapshot.Results[0].Clauses[0]
	snapshot.Results[0].Seeding = &scenarios.SeedingResult{DisciplinaryPossible: true, Seeds: []scenarios.SeedFinish{
		{Seed: 1, OpponentSeed: 2, Clauses: []scenarios.Clause{clause}, Opponents: []scenarios.SeedOpponent{{TeamID: "bravo", PossibleResults: []scenarios.Clause{seedingTestClause(clause.Conditions...)}}}},
		{Clauses: []scenarios.Clause{seedingTestClause(testScenarioCondition("future-4", 6))}, PossibleResults: []scenarios.Clause{seedingTestClause(testScenarioCondition("future-4", 6))}},
	}}
	store := seasonScenarioStore(seedingTestData(), []string{"future-4"}, []string{"future-4", "future-5"}, snapshot)
	body := getClinching(t, store, "?view=seeding&team=ALP")
	for _, want := range []string{`id="seeding-team"`, `value="ALP" selected`, `value="seeding" checked`, "1st", "hosts 2nd", "Misses the playoffs", "W–any", "D/L–any", `data-assumption-builder`, `name="team" value="ALP"`, "Disciplinary points may decide"} {
		if !strings.Contains(body, want) {
			t.Errorf("seeding view missing %q", want)
		}
	}
	if strings.Contains(body, "must finish with a higher goal difference") || strings.Contains(body, `id="seeding-conditions"`) {
		t.Fatal("overview must not render scenario conditions")
	}
	detail := getClinching(t, store, "?view=seeding&team=ALP&finish=1")
	for _, want := range []string{"finishes 1st", "must finish with a higher goal difference", `aria-current="true">1st`} {
		if !strings.Contains(detail, want) {
			t.Errorf("selected finish missing %q", want)
		}
	}
	filtered := getClinching(t, store, "?view=seeding&team=ALP&finish=1&p=future-4:h")
	if strings.Contains(filtered, "Misses the playoffs") || strings.Contains(filtered, "D/L") || strings.Contains(filtered, "in that order") {
		t.Fatal("contradictory finish or own result remained")
	}
	if !strings.Contains(filtered, "must finish with a higher goal difference") || !strings.Contains(filtered, `name="p" value="future-4:h"`) {
		t.Fatal("fixed score conditions or URL state disappeared")
	}
	gone := getClinching(t, store, "?view=seeding&team=ALP&finish=miss&p=future-4:h")
	if !strings.Contains(gone, "misses the playoffs") || !strings.Contains(gone, "Not possible with these results.") {
		t.Fatal("an impossible selected finish should say so")
	}
	for _, query := range []string{"?view=seeding&team=UNKNOWN", "?view=seeding&team=ALP&p=no-game:h", "?view=seeding&team=ALP&p=future-4:x", "?view=seeding&team=ALP&p=future-4:h&p=future-4:d", "?view=seeding&team=ALP&finish=0", "?view=seeding&team=ALP&finish=x"} {
		response := httptest.NewRecorder()
		NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", query, response.Code)
		}
	}
}

func TestClinchingSeedingAllAlternativesAccessible(t *testing.T) {
	gd := func(gain int) scenarios.ScoreCondition {
		return scenarios.ScoreCondition{TeamID: "alpha", RivalID: "bravo", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: gain}}}
	}
	clauses := []scenarios.Clause{}
	for i := range 300 {
		clause := seedingTestClause(testScenarioCondition("future-4", 1))
		clause.ScoreConditions = []scenarios.ScoreCondition{gd(i + 1)}
		clauses = append(clauses, clause)
	}
	snapshot := seasonResult()
	snapshot.Results[0].Seeding = &scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Seed: 1, OpponentSeed: 2, Clauses: clauses, Opponents: []scenarios.SeedOpponent{{TeamID: "bravo", PossibleResults: []scenarios.Clause{seedingTestClause()}}}}}}
	store := seasonScenarioStore(seedingTestData(), []string{"future-4"}, []string{"future-4", "future-5"}, snapshot)
	body := getClinching(t, store, "?view=seeding&team=ALP&finish=1")
	requireText(t, body, "#seeding-conditions", "300 ways", "See scenarios for finishing 1st", "This summary alone does not guarantee the finish.")
	forbidRaw(t, body, "they aren’t listed", "tiebreaker swing", "alternative 300")

	for _, number := range []int{1, 4, 12, 300} {
		detail := getClinching(t, store, fmt.Sprintf("?view=seeding&team=ALP&finish=1&alternative=%d", number))
		requireText(t, detail, "#seeding-alternative", fmt.Sprintf("Scenario %d of 300", number), "if all 2 conditions happen")
		requireAttr(t, detail, "a", "href", "?finish=1&team=ALP&view=seeding#seeding-conditions")
		if strings.Count(detail, "clinching-condition-score") != seedingScenariosPerPage {
			t.Fatal("detail must render a bounded page of complete scenarios")
		}
	}
}

func TestSeedingGridMergesOwnResultsWithTheSameFinishes(t *testing.T) {
	labels := map[string]string{"alpha": "ALP", "bravo": "BRV", "charlie": "CHA"}
	games := map[string]cache.Game{
		"first":  {HomeTeamID: "alpha", AwayTeamID: "bravo", KickoffUTC: "2026-10-01 19:00:00 UTC"},
		"second": {HomeTeamID: "charlie", AwayTeamID: "alpha", KickoffUTC: "2026-10-08 19:00:00 UTC"},
		"other":  {HomeTeamID: "bravo", AwayTeamID: "charlie", KickoffUTC: "2026-10-08 19:00:00 UTC"},
	}
	result := scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{
		{Seed: 2, OpponentSeed: 1, Clauses: []scenarios.Clause{seedingTestClause()}, Opponents: []scenarios.SeedOpponent{
			{TeamID: "charlie", PossibleResults: []scenarios.Clause{seedingTestClause(testScenarioCondition("first", 6))}},
			{TeamID: "bravo", PossibleResults: []scenarios.Clause{seedingTestClause(testScenarioCondition("first", 6), testScenarioCondition("other", 1))}},
		}},
		{Seed: 1, OpponentSeed: 2, Clauses: []scenarios.Clause{seedingTestClause()}, Opponents: []scenarios.SeedOpponent{
			{TeamID: "bravo", PossibleResults: []scenarios.Clause{seedingTestClause(testScenarioCondition("first", 1), testScenarioCondition("second", 4))}},
		}},
	}}
	ids := []string{"other", "second", "first"}
	columns, rows := seedingGrid(result, map[string]clinching.Outcome{}, newSeedingOwnMatches("alpha", ids, nil, games), labels, games)
	got := [][]string{}
	for _, c := range columns {
		got = append(got, c.Lines)
	}
	want := [][]string{{"W–W"}, {"W–D/L"}, {"D/L–any"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("columns = %q", got)
	}
	if len(rows) != 2 || rows[0].Heading != "1st" || rows[1].Detail != "at 1st" {
		t.Fatalf("rows = %+v", rows)
	}
	if !reflect.DeepEqual(rows[1].Cells[2].Opponents, []string{"BRV", "CHA"}) || rows[1].Cells[0].Possible || !reflect.DeepEqual(rows[0].Cells[0].Opponents, []string{"BRV"}) {
		t.Fatalf("cells = %+v", rows)
	}
	fixed := map[string]clinching.Outcome{"first": clinching.Draw}
	columns, _ = seedingGrid(scenarios.FilterSeeding(result, fixed), fixed, newSeedingOwnMatches("alpha", ids, fixed, games), labels, games)
	if len(columns) != 1 || columns[0].Lines[0] != "Any result" {
		t.Fatalf("a fixed own match leaves its other results: %+v", columns)
	}
}

func TestSeedingOwnResultsUsePointRangesWhenExact(t *testing.T) {
	games := map[string]cache.Game{"a": {HomeTeamID: "alpha", AwayTeamID: "bravo"}, "b": {HomeTeamID: "charlie", AwayTeamID: "alpha"}}
	m := newSeedingOwnMatches("alpha", []string{"a", "b"}, nil, games)
	labels := map[string]string{"alpha": "ALP", "bravo": "BRV", "charlie": "CHA"}
	for _, test := range []struct {
		combos [][]int
		want   string
	}{
		{[][]int{{0, 2}, {1, 1}, {1, 2}, {2, 0}, {2, 1}}, "takes 1 to 3 points"},
		{[][]int{{0, 0}, {0, 1}, {1, 0}}, "takes 4 points or more"},
		{[][]int{{1, 2}, {2, 1}}, "takes 1 point"},
		{[][]int{{0, 2}, {1, 1}}, ""},
	} {
		got, ok := m.pointsPhrase(test.combos)
		if got != test.want || ok != (test.want != "") {
			t.Errorf("%v: %q %v, want %q", test.combos, got, ok, test.want)
		}
	}
	if got := m.describe([][]int{{2, 0}, {2, 1}, {2, 2}}, labels, games); !reflect.DeepEqual(got, []string{"loses vs BRV"}) {
		t.Errorf("a product keeps its result wording: %q", got)
	}
	if got := m.shorthand([][]int{{0, 2}, {1, 1}, {1, 2}, {2, 0}, {2, 1}}); !reflect.DeepEqual(got, []string{"1 to 3 points"}) {
		t.Errorf("shorthand points range: %q", got)
	}
	if got := m.shorthand([][]int{{0, 2}, {1, 1}}); !reflect.DeepEqual(got, []string{"W–L", "D–D"}) {
		t.Errorf("shorthand products: %q", got)
	}
}

func TestSeedingLikelihoodsFillTheGridAndPickTheLikeliestFinish(t *testing.T) {
	labels := map[string]string{"alpha": "ALP", "bravo": "BRV", "charlie": "CHA"}
	games := map[string]cache.Game{
		"first":  {HomeTeamID: "alpha", AwayTeamID: "bravo", KickoffUTC: "2026-10-01 19:00:00 UTC"},
		"second": {HomeTeamID: "charlie", AwayTeamID: "alpha", KickoffUTC: "2026-10-08 19:00:00 UTC"},
	}
	result := scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{
		{Seed: 1, OpponentSeed: 2, Clauses: []scenarios.Clause{seedingTestClause()}, Opponents: []scenarios.SeedOpponent{
			{TeamID: "bravo", PossibleResults: []scenarios.Clause{seedingTestClause(testScenarioCondition("first", 1), testScenarioCondition("second", 4))}},
		}},
		{Seed: 2, OpponentSeed: 1, Clauses: []scenarios.Clause{seedingTestClause()}, Opponents: []scenarios.SeedOpponent{
			{TeamID: "bravo", PossibleResults: []scenarios.Clause{seedingTestClause(testScenarioCondition("first", 6))}},
			{TeamID: "charlie", PossibleResults: []scenarios.Clause{seedingTestClause(testScenarioCondition("first", 6))}},
		}},
	}}
	m := newSeedingOwnMatches("alpha", []string{"first", "second"}, nil, games)
	columns, rows := seedingGrid(result, nil, m, labels, games)
	counts := []simulation.SeedingCount{
		{Seed: 1, OpponentID: "bravo", Own: map[string]simulation.Outcome{"first": simulation.HomeWin, "second": simulation.AwayWin}, Probability: 0.3},
		{Seed: 2, OpponentID: "charlie", Own: map[string]simulation.Outcome{"first": simulation.Draw, "second": simulation.HomeWin}, Probability: 0.5},
		{Seed: 2, OpponentID: "bravo", Own: map[string]simulation.Outcome{"first": simulation.AwayWin, "second": simulation.AwayWin}, Probability: 0.1},
		// The exact search rules this out, so it is ignored.
		{Seed: 2, OpponentID: "bravo", Own: map[string]simulation.Outcome{"first": simulation.HomeWin, "second": simulation.AwayWin}, Probability: 0.1},
	}
	applySeedingLikelihoods(columns, rows, counts, m)
	if rows[0].Likelihood != "30%" || rows[1].Likelihood != "60%" {
		t.Fatalf("row likelihoods %q %q", rows[0].Likelihood, rows[1].Likelihood)
	}
	last := len(columns) - 1
	if !reflect.DeepEqual(rows[1].Cells[last].Opponents, []string{"CHA", "BRV"}) || rows[1].Cells[last].Likelihood != "60%" || columns[last].Likelihood != "60%" || columns[0].Likelihood != "30%" {
		t.Fatalf("columns %+v rows %+v", columns, rows)
	}
	if got := rows[mostLikelyRow(rows)].seed; got != 2 {
		t.Fatalf("most likely seed %d", got)
	}
	if rows[1].opponentLikelihood("charlie", true) != "50%" || rows[1].opponentLikelihood("bravo", true) != "10%" {
		t.Fatal("opponent likelihoods")
	}
	for _, test := range []struct {
		p    float64
		only bool
		want string
	}{{0, false, "<1%"}, {0.004, false, "<1%"}, {0.42, false, "42%"}, {0.999, false, ">99%"}, {0.97, true, "100%"}} {
		if got := likelihoodText(test.p, test.only); got != test.want {
			t.Errorf("likelihoodText(%v, %v) = %q", test.p, test.only, got)
		}
	}
}

func TestSeedingOverflowStatesNecessaryResults(t *testing.T) {
	labels := map[string]string{"a": "AAA", "b": "BBB", "c": "CCC", "d": "DDD"}
	games := map[string]cache.Game{
		"ab": {HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: "2026-10-01 19:00:00 UTC"},
		"cd": {HomeTeamID: "c", AwayTeamID: "d", KickoffUTC: "2026-10-02 19:00:00 UTC"},
		"ac": {HomeTeamID: "a", AwayTeamID: "c", KickoffUTC: "2026-10-03 19:00:00 UTC"},
		"bd": {HomeTeamID: "b", AwayTeamID: "d", KickoffUTC: "2026-10-04 19:00:00 UTC"},
	}
	score := scenarios.ScoreCondition{TeamID: "a", RivalID: "b", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1}}}
	paths := [][]scenarioRequirement{
		{{GameID: "ab", Mask: 1}, {GameID: "cd", Mask: 1}, {GameID: "bd", Mask: 4}},
		{{GameID: "ab", Mask: 2}, {GameID: "ac", Mask: 4}, {GameID: "bd", Mask: 1}},
		{{GameID: "ab", Mask: 1}, {Score: &score}},
		// With a draw here too, bd's needed result varies between ways.
		{{GameID: "ab", Mask: 1}, {GameID: "bd", Mask: 2}},
	}
	view := seedingOverflow(paths, labels, games)
	if view.Ways != 4 || len(view.Always) != 1 || view.Always[0].Text != "AAA wins or draws vs BBB" {
		t.Fatalf("always = %+v", view.Always)
	}
	if len(view.Some) != 2 || view.Some[0][0].Text != "CCC wins vs DDD" || view.Some[1][0].Text != "CCC wins at AAA" {
		t.Fatalf("some = %+v", view.Some)
	}
	if !reflect.DeepEqual(view.Depends, []string{"BBB vs DDD"}) || !view.Scores || view.SomeLow != 0 || view.SomeHigh != 1 {
		t.Fatalf("view = %+v", view)
	}
	if got := view.SomeText(); got != "" {
		t.Fatalf("some text %q", got)
	}
}

func TestSeedingCoverIsExact(t *testing.T) {
	set := [][]int{{0, 0}, {0, 1}, {1, 0}, {2, 2}}
	cubes := seedingCover(set, 2)
	covered := map[[2]int]bool{}
	for _, cube := range cubes {
		for a := range 3 {
			for b := range 3 {
				if cube[0]&(1<<a) != 0 && cube[1]&(1<<b) != 0 {
					covered[[2]int{a, b}] = true
				}
			}
		}
	}
	want := map[[2]int]bool{{0, 0}: true, {0, 1}: true, {1, 0}: true, {2, 2}: true}
	if !reflect.DeepEqual(covered, want) || len(cubes) != 3 {
		t.Fatalf("cubes %v cover %v", cubes, covered)
	}
}
