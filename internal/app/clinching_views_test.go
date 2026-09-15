package app

import (
	"fmt"
	"math/rand/v2"
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
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestClinchingPageBoundsVisiblePathsAndPreservesAlternatives(t *testing.T) {
	for _, elimination := range []bool{false, true} {
		t.Run(fmt.Sprintf("elimination=%t", elimination), func(t *testing.T) {
			data := testSeasonData()
			data.FixtureSnapshotID = "snapshot"
			clauses := []scenarios.Clause{}
			for i := range 8 {
				id := fmt.Sprintf("other-%d", i)
				data.Games = append(data.Games, cache.Game{ASAID: id, HomeTeamID: fmt.Sprintf("home-%d", i), AwayTeamID: fmt.Sprintf("away-%d", i)})
				clauses = append(clauses, scenarios.Clause{Conditions: []scenarios.FixtureCondition{testScenarioCondition("future-1", 1), testScenarioCondition(id, 1)}})
			}
			result := scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 8, State: scenarios.OpportunityCanClinch, CanClinch: true, Clauses: clauses, Limitation: scenarios.LimitationBudgetPartial}
			if elimination {
				result.State, result.CanClinch, result.Clauses = scenarios.OpportunityCannotClinch, false, nil
				result.CanBeEliminated, result.EliminationClauses = true, clauses
			}
			store := fullFakeStore{fakeStore: fakeStore{season: data}, scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: scenarios.Slate{State: scenarios.SlateReady, FixtureIDs: []string{"future-1"}}}, Results: []cache.ScenarioResult{{Result: result}}}}
			response := httptest.NewRecorder()
			NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			body := response.Body.String()
			if strings.Count(body, `class="clinching-path-option"`) != 8 || strings.Contains(body, "View 5 more paths") {
				t.Fatal("all paths should be visible without a second disclosure")
			}
			if strings.Count(body, `class="clinching-result-group"`) != 1 {
				t.Fatal("shared own result should appear once")
			}
			if !strings.Contains(body, "Scenario search was incomplete.") {
				t.Fatal("incomplete-search notice missing")
			}
			if strings.Contains(body, "Choose a group matching") || strings.Contains(body, "Other paths may exist") {
				t.Fatal("obsolete explanatory copy was rendered")
			}
			if strings.Index(body, `class="clinching-path-option"`) > strings.Index(body, `id="clinching-matches"`) {
				t.Fatal("schedule must follow results")
			}
			if strings.Contains(body, "<script>alert(1)</script>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
				t.Fatal("team names must remain escaped")
			}
		})
	}
}

func TestClinchingPageDoesNotCallTiebreakDependentPathsIncomplete(t *testing.T) {
	data := testSeasonData()
	data.FixtureSnapshotID = "snapshot"
	result := scenarios.Result{
		TeamID:      "alpha",
		Achievement: competition.AchievementPlayoffs,
		TopK:        8,
		State:       scenarios.OpportunityCanClinch,
		CanClinch:   true,
		Clauses:     []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{testScenarioCondition("future-1", 1)}}},
		Limitation:  "additional paths may depend on score or unavailable tiebreak data; no outcome-only path is published",
	}
	store := fullFakeStore{fakeStore: fakeStore{season: data}, scenario: cache.ScenarioSnapshot{Run: cache.ScenarioRun{Slate: scenarios.Slate{State: scenarios.SlateReady, FixtureIDs: []string{"future-1"}}}, Results: []cache.ScenarioResult{{Result: result}}}}
	response := httptest.NewRecorder()
	NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "Scenario search was incomplete.") {
		t.Fatal("tiebreak-dependent paths must not be presented as an incomplete search")
	}
}

func testScenarioCondition(id string, mask uint8) scenarios.FixtureCondition {
	outcomes := []clinching.Outcome{}
	for i, o := range []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin} {
		if mask&(1<<i) != 0 {
			outcomes = append(outcomes, o)
		}
	}
	return scenarios.FixtureCondition{GameID: id, AllowedOutcomes: outcomes}
}

func TestScenarioTeamCodePrefersAbbreviation(t *testing.T) {
	if got := scenarioTeamCode(standings.Team{ID: "sd", Name: "San Diego Wave FC", ShortName: "San Diego", Abbreviation: "SD"}); got != "SD" {
		t.Fatalf("scenario team code = %q", got)
	}
	if got := scenarioTeamCode(standings.Team{ID: "kc", Name: "Kansas City Current", ShortName: "Kansas City"}); got != "Kansas City" {
		t.Fatalf("scenario team code fallback = %q", got)
	}
}

func TestRequirementPartsUseClubTokens(t *testing.T) {
	parts := requirementParts(
		scenarioRequirement{GameID: "game", Mask: 1},
		"",
		map[string]string{"home": "HOM", "away": "AWY"},
		map[string]cache.Game{"game": {HomeTeamID: "home", AwayTeamID: "away"}},
	)
	if len(parts) != 3 || parts[0].Team == nil || parts[0].Team.Code != "HOM" || parts[0].Team.LogoURL != clubLogoURL("home") || parts[1].Text != "wins vs" || parts[2].Team == nil || parts[2].Team.Code != "AWY" {
		t.Fatalf("club tokens = %#v", parts)
	}
}

func TestRequirementTextUsesResultAndVenue(t *testing.T) {
	teams := map[string]string{"home": "HOM", "away": "AWY"}
	games := map[string]cache.Game{"game": {HomeTeamID: "home", AwayTeamID: "away"}}
	for _, test := range []struct {
		name, perspective, want string
		mask                    uint8
	}{
		{name: "home win", perspective: "home", mask: 1, want: "HOM wins vs AWY"},
		{name: "away win", perspective: "away", mask: 4, want: "AWY wins at HOM"},
		{name: "home draw", perspective: "home", mask: 2, want: "HOM draws vs AWY"},
		{name: "away draw", perspective: "away", mask: 2, want: "AWY draws at HOM"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := requirementText(scenarioRequirement{GameID: "game", Mask: test.mask}, test.perspective, teams, games)
			if got != test.want {
				t.Fatalf("requirement text = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClinchingGroupHeadingsUseFullNames(t *testing.T) {
	clauses := []scenarios.Clause{testScenarioClause(testScenarioCondition("game", 4))}
	groups := clinchingGroupsWithHeadingTeams(
		clauses,
		"away",
		map[string]string{"home": "NC", "away": "NJY"},
		map[string]string{"home": "North Carolina Courage", "away": "NJ/NY Gotham FC"},
		map[string]cache.Game{"game": {HomeTeamID: "home", AwayTeamID: "away"}},
	)
	if len(groups) != 1 || groups[0].Heading != "If NJ/NY Gotham FC wins at North Carolina Courage" {
		t.Fatalf("heading = %#v", groups)
	}
}

func TestPointRequirementPartsUseClubTokens(t *testing.T) {
	parts := requirementParts(
		scenarioRequirement{GameID: "first", SecondGameID: "second", TeamID: "sea", Points: 4},
		"",
		map[string]string{"sea": "SEA", "la": "LA", "den": "DEN"},
		map[string]cache.Game{
			"first":  {HomeTeamID: "la", AwayTeamID: "sea"},
			"second": {HomeTeamID: "den", AwayTeamID: "sea"},
		},
	)
	if len(parts) != 6 || parts[0].Team == nil || parts[0].Team.Code != "SEA" || parts[0].Team.LogoURL != clubLogoURL("sea") || parts[1].Text != "earns at least 4 points from these two matches:" || parts[2].Text != "at" || parts[3].Team == nil || parts[3].Team.Code != "LA" || parts[3].Team.LogoURL != clubLogoURL("la") || parts[4].Text != "and at" || parts[5].Team == nil || parts[5].Team.Code != "DEN" || parts[5].Team.LogoURL != clubLogoURL("den") {
		t.Fatalf("point requirement parts = %#v", parts)
	}
}

func testScenarioClause(conditions ...scenarios.FixtureCondition) scenarios.Clause {
	return scenarios.Clause{Conditions: conditions}
}

func TestClinchingGroupsExposeDrawAndFactorSharedHelp(t *testing.T) {
	teams := map[string]string{"a": "Alpha", "b": "Bravo", "s": "Seattle", "c": "City", "d": "Denver", "k": "Kansas", "w": "Wave"}
	games := map[string]cache.Game{"own": {HomeTeamID: "b", AwayTeamID: "a"}, "s1": {HomeTeamID: "c", AwayTeamID: "s"}, "s2": {HomeTeamID: "s", AwayTeamID: "d"}, "kc": {HomeTeamID: "k", AwayTeamID: "w"}}
	c := testScenarioCondition
	clauses := []scenarios.Clause{
		testScenarioClause(c("own", 4), c("kc", 3), c("s1", 4)),
		testScenarioClause(c("own", 4), c("kc", 3), c("s2", 1)),
		testScenarioClause(c("own", 4), c("kc", 3), c("s1", 6), c("s2", 3)),
		testScenarioClause(c("own", 6), c("kc", 1), c("s1", 4), c("s2", 1)),
	}
	original := fmt.Sprintf("%#v", clauses)
	groups := clinchingGroups(clauses, "a", teams, games)
	if len(groups) != 2 || groups[0].Heading != "If Alpha wins at Bravo" || groups[1].Heading != "If Alpha draws at Bravo" {
		t.Fatalf("own-result groups = %#v", groups)
	}
	// A draw must inherit the win-or-draw clause, but none of the win-only help.
	if len(groups[1].Help.Conditions) != 3 || len(groups[1].Help.Alternatives) != 0 {
		t.Fatalf("draw requirements = %#v", groups[1].Help)
	}
	text := fmt.Sprintf("%+v", groups[0].Help)
	if !strings.Contains(text, "Seattle earns at least 2 points from these two matches") {
		t.Fatalf("missing exact points simplification: %s", text)
	}
	assertScenarioEquivalent(t, clauses, groups, games)
	if fmt.Sprintf("%#v", clauses) != original {
		t.Fatal("presentation mutated stored proof")
	}
	if !reflect.DeepEqual(groups, clinchingGroups(clauses, "a", teams, games)) {
		t.Fatal("grouping is not deterministic")
	}
}

func TestClinchingGroupsPreserveAllOutcomes(t *testing.T) {
	games := map[string]cache.Game{
		"own1": {HomeTeamID: "a", AwayTeamID: "b"}, "own2": {HomeTeamID: "c", AwayTeamID: "a"},
		"s1": {HomeTeamID: "s", AwayTeamID: "b"}, "s2": {HomeTeamID: "c", AwayTeamID: "s"},
		"r1": {HomeTeamID: "b", AwayTeamID: "c"},
	}
	teams := map[string]string{"a": "Same name", "b": "Same name", "c": "Other", "s": "Seattle"}
	ids := []string{"own1", "own2", "s1", "s2", "r1"}
	random := rand.New(rand.NewPCG(123, 456)) // #nosec G404 -- reproducible test cases, not security randomness.
	for iteration := range 150 {
		clauses := []scenarios.Clause{}
		for range 1 + random.IntN(14) {
			conditions := []scenarios.FixtureCondition{}
			for _, id := range ids {
				if random.IntN(3) != 0 {
					conditions = append(conditions, testScenarioCondition(id, []uint8{1, 2, 3, 4, 5, 6, 7}[random.IntN(7)]))
				}
			}
			clauses = append(clauses, testScenarioClause(conditions...))
		}
		t.Run(fmt.Sprint(iteration), func(t *testing.T) {
			assertScenarioCoverage(t, clauses, clinchingGroups(clauses, "a", teams, games), games, true)
		})
	}
}

func TestClinchingGroupsLargeOwnSlateFallbackAndUnrestrictedPaths(t *testing.T) {
	games := map[string]cache.Game{}
	conditions := []scenarios.FixtureCondition{}
	for i := range 5 {
		id := fmt.Sprint(i)
		games[id] = cache.Game{HomeTeamID: "a", AwayTeamID: "b"}
		conditions = append(conditions, testScenarioCondition(id, 3))
	}
	clauses := []scenarios.Clause{testScenarioClause(conditions...), testScenarioClause(testScenarioCondition("0", 4))}
	groups := clinchingGroups(clauses, "a", map[string]string{"a": "Alpha", "b": "Bravo"}, games)
	if len(groups) != 2 {
		t.Fatalf("fallback expanded to %d groups", len(groups))
	}
	assertScenarioEquivalent(t, clauses, groups, games)
	clauses = []scenarios.Clause{testScenarioClause()}
	groups = clinchingGroups(clauses, "a", map[string]string{"a": "Alpha"}, games)
	if len(groups) != 1 || len(groups[0].Help.Conditions) != 0 || len(groups[0].Help.Alternatives) != 0 {
		t.Fatal("unrestricted path should require no help")
	}
	assertScenarioEquivalent(t, clauses, groups, games)
	if got := clinchingGroups(nil, "a", nil, games); len(got) != 0 {
		t.Fatal("empty proof must not produce a group")
	}
}

func TestClinchingGroupsDoNotOverstatePointsRequirement(t *testing.T) {
	games := map[string]cache.Game{"s1": {HomeTeamID: "s", AwayTeamID: "b"}, "s2": {HomeTeamID: "c", AwayTeamID: "s"}}
	// Winning either match alone is insufficient to claim "at least two points":
	// two draws are absent from these partial certified results.
	clauses := []scenarios.Clause{testScenarioClause(testScenarioCondition("s1", 1)), testScenarioClause(testScenarioCondition("s2", 4))}
	groups := clinchingGroups(clauses, "a", map[string]string{"s": "Seattle"}, games)
	if strings.Contains(fmt.Sprintf("%+v", groups), "at least 2 points") {
		t.Fatal("invented a two-draw path")
	}
	assertScenarioEquivalent(t, clauses, groups, games)
}

// Exhaustively compare the rendered expression's semantics to the original
// disjunction, independently interpreting every fixture and points predicate.
func assertScenarioEquivalent(t *testing.T, clauses []scenarios.Clause, groups []clinchingGroupView, games map[string]cache.Game) {
	t.Helper()
	assertScenarioCoverage(t, clauses, groups, games, false)
}

func assertScenarioCoverage(t *testing.T, clauses []scenarios.Clause, groups []clinchingGroupView, games map[string]cache.Game, disjoint bool) {
	t.Helper()
	ids := []string{}
	for id := range games {
		ids = append(ids, id)
	}
	assignment := map[string]clinching.Outcome{}
	outcomes := []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin}
	var walk func(int)
	walk = func(index int) {
		if index < len(ids) {
			for _, outcome := range outcomes {
				assignment[ids[index]] = outcome
				walk(index + 1)
			}
			return
		}
		want := false
		for _, c := range clauses {
			matches := true
			for _, condition := range c.Conditions {
				allowed := false
				for _, outcome := range condition.AllowedOutcomes {
					if outcome == assignment[condition.GameID] {
						allowed = true
					}
				}
				matches = matches && allowed
			}
			want = want || matches
		}
		got := false
		for _, g := range groups {
			helpMatches := testExpressionMatches(g.Help, assignment, games)
			flatMatches := false
			matchingPaths := 0
			for _, combination := range g.Help.Combinations() {
				if testRequirementsMatch(combination, assignment, games) {
					flatMatches = true
					matchingPaths++
				}
			}
			if disjoint && matchingPaths > 1 {
				t.Fatalf("assignment %v matches %d alternatives; group=%+v", assignment, matchingPaths, g)
			}
			if flatMatches != helpMatches {
				t.Fatalf("assignment %v: flattened=%t expression=%t; expression=%+v", assignment, flatMatches, helpMatches, g.Help)
			}
			got = got || ((testRequirementsMatch(g.Own, assignment, games) || (len(g.AlternativeOwn) > 0 && testRequirementsMatch(g.AlternativeOwn, assignment, games))) && flatMatches)
		}
		if got != want {
			t.Fatalf("assignment %v: grouped=%t original=%t; groups=%+v", assignment, got, want, groups)
		}
	}
	walk(0)
}
func testRequirementsMatch(conditions []scenarioRequirement, assignment map[string]clinching.Outcome, games map[string]cache.Game) bool {
	for _, r := range conditions {
		if r.SecondGameID != "" {
			points := 0
			for _, id := range []string{r.GameID, r.SecondGameID} {
				result, g := assignment[id], games[id]
				if result == clinching.Draw {
					points++
				} else if (result == clinching.HomeWin && g.HomeTeamID == r.TeamID) || (result == clinching.AwayWin && g.AwayTeamID == r.TeamID) {
					points += 3
				}
			}
			if points < r.Points {
				return false
			}
		} else {
			mask := uint8(0)
			switch assignment[r.GameID] {
			case clinching.HomeWin:
				mask = 1
			case clinching.Draw:
				mask = 2
			case clinching.AwayWin:
				mask = 4
			}
			if r.Mask&mask == 0 {
				return false
			}
		}
	}
	return true
}
func testExpressionMatches(e scenarioExpression, assignment map[string]clinching.Outcome, games map[string]cache.Game) bool {
	if !testRequirementsMatch(e.Conditions, assignment, games) {
		return false
	}
	if len(e.Alternatives) == 0 {
		return true
	}
	for _, alternative := range e.Alternatives {
		if testExpressionMatches(alternative, assignment, games) {
			return true
		}
	}
	return false
}

func TestClinchingGroupsCombineSymmetricOwnResults(t *testing.T) {
	games := map[string]cache.Game{"one": {HomeTeamID: "b", AwayTeamID: "a"}, "two": {HomeTeamID: "a", AwayTeamID: "c"}, "help": {HomeTeamID: "b", AwayTeamID: "c"}}
	c := testScenarioCondition
	clauses := []scenarios.Clause{testScenarioClause(c("one", 4), c("two", 4), c("help", 1)), testScenarioClause(c("one", 1), c("two", 1), c("help", 1)), testScenarioClause(c("one", 1), c("two", 4))}
	groups := clinchingGroups(clauses, "a", map[string]string{"a": "Alpha", "b": "Bravo", "c": "Charlie"}, games)
	if len(groups) != 2 || !strings.Contains(groups[0].Heading, "wins one match and loses the other") || !strings.Contains(groups[1].Heading, "loses both matches") {
		t.Fatalf("groups = %+v", groups)
	}
	assertScenarioEquivalent(t, clauses, groups, games)
	// Different outside help must keep the two orderings distinct.
	clauses[1].Conditions[2] = c("help", 2)
	groups = clinchingGroups(clauses, "a", nil, games)
	if len(groups) != 3 {
		t.Fatal("merged different outside requirements")
	}
	assertScenarioEquivalent(t, clauses, groups, games)
}
