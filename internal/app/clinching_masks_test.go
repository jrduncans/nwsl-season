package app

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func TestClinchingGroupsMergeAlternativeFixtureResults(t *testing.T) {
	games := map[string]cache.Game{
		"own": {HomeTeamID: "nc", AwayTeamID: "den"},
		"sea": {HomeTeamID: "sea", AwayTeamID: "orl"},
		"kc":  {HomeTeamID: "kc", AwayTeamID: "was"},
	}
	labels := map[string]string{"nc": "NC", "den": "DEN", "sea": "SEA", "orl": "ORL", "kc": "KC", "was": "WAS"}
	for _, test := range []struct {
		masks []uint8
		want  string
	}{
		{[]uint8{4, 2}, "ORL wins or draws at SEA"},
		{[]uint8{1, 2}, "SEA wins or draws vs ORL"},
		{[]uint8{1, 4}, "SEA does not draw vs ORL"},
		{[]uint8{1, 2, 4}, ""},
	} {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/shared=%t", test.masks, shared), func(t *testing.T) {
				clauses := []scenarios.Clause{}
				for _, mask := range test.masks {
					clause := testScenarioClause(testScenarioCondition("own", 1), testScenarioCondition("sea", mask))
					if shared {
						clause.Conditions = append(clause.Conditions, testScenarioCondition("kc", 1))
					}
					clauses = append(clauses, clause)
				}
				groups := clinchingGroups(clauses, "nc", labels, games)
				if len(groups) != 1 || len(groups[0].Summary.Alternatives) != 0 || len(groups[0].Help.Alternatives) != 0 {
					t.Fatalf("expected a single merged condition set: %+v", groups)
				}
				texts := map[string]bool{}
				for _, r := range groups[0].Summary.Conditions {
					texts[r.Text] = true
				}
				want := map[string]bool{}
				if test.want != "" {
					want[test.want] = true
				}
				if shared {
					want["KC wins vs WAS"] = true
				}
				if !reflect.DeepEqual(texts, want) {
					t.Fatalf("conditions = %v, want %v", texts, want)
				}
				assertScenarioCoverage(t, clauses, groups, games, true)
			})
		}
	}
}

func TestClinchingFixtureMergePreservesEveryTwoFixtureUnion(t *testing.T) {
	games := map[string]cache.Game{"a": {}, "b": {}}
	for union := range 1 << 9 {
		paths := [][]scenarioRequirement{}
		for x := range 3 {
			for y := range 3 {
				if union&(1<<(x*3+y)) != 0 {
					paths = append(paths, []scenarioRequirement{{GameID: "a", Mask: 1 << x}, {GameID: "b", Mask: 1 << y}})
				}
			}
		}
		original := fmt.Sprintf("%#v", paths)
		merged := compactFixtureMasks(paths)
		for x := range 3 {
			for y := range 3 {
				assignment := map[string]clinching.Outcome{"a": scenarioOutcomes[x], "b": scenarioOutcomes[y]}
				got := testExpressionMatches(factorRequirements(merged, 0), assignment, games)
				// An empty path list is a false disjunction, while an empty
				// expression represents no restrictions.
				got = len(merged) > 0 && got
				if want := union&(1<<(x*3+y)) != 0; got != want {
					t.Fatalf("union %09b at %d/%d: merged=%+v", union, x, y, merged)
				}
			}
		}
		if fmt.Sprintf("%#v", paths) != original {
			t.Fatal("merging mutated the input")
		}
	}
}

func TestClinchingFixtureMergeRequiresIdenticalOtherConditions(t *testing.T) {
	for _, pair := range [][2]scenarioRequirement{
		{summaryFixture("other", 1), summaryFixture("other", 2)},
		{summaryScore("nc", "sea", 1, false), summaryScore("nc", "sea", 2, false)},
		{{GameID: "a", SecondGameID: "b", TeamID: "sea", Points: 3}, {GameID: "a", SecondGameID: "b", TeamID: "sea", Points: 4}},
	} {
		paths := [][]scenarioRequirement{{summaryFixture("sea", 4), pair[0]}, {summaryFixture("sea", 2), pair[1]}}
		if merged := compactFixtureMasks(paths); len(merged) != 2 {
			t.Fatalf("merged differing requirements: %+v", merged)
		}
		paths[1][1] = pair[0]
		if merged := compactFixtureMasks(paths); len(merged) != 1 {
			t.Fatalf("failed to merge identical requirements: %+v", merged)
		}
	}
}
