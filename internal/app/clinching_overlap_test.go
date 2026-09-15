package app

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func TestClinchingGroupsSubtractGothamOverlap(t *testing.T) {
	games := map[string]cache.Game{
		"own":     {HomeTeamID: "nc", AwayTeamID: "njy"},
		"la-sea":  {HomeTeamID: "la", AwayTeamID: "sea"},
		"uta-la":  {HomeTeamID: "uta", AwayTeamID: "la"},
		"den-sea": {HomeTeamID: "den", AwayTeamID: "sea"},
	}
	teams := map[string]string{"nc": "NC", "njy": "NJY", "la": "LA", "sea": "SEA", "uta": "UTA", "den": "DEN"}
	c := testScenarioCondition
	for _, test := range []struct {
		name    string
		clauses []scenarios.Clause
		want    []string
	}{
		{
			name: "draw already covered by shorter path",
			clauses: []scenarios.Clause{
				testScenarioClause(c("own", 4), c("la-sea", 6), c("uta-la", 2)),
				testScenarioClause(c("own", 4), c("la-sea", 6), c("uta-la", 3), c("den-sea", 6)),
			},
			want: []string{
				"SEA wins or draws at LA + UTA draws vs LA",
				"SEA wins or draws at LA + UTA wins vs LA + SEA wins or draws at DEN",
			},
		},
		{
			name: "two draws shared by equal length paths",
			clauses: []scenarios.Clause{
				testScenarioClause(c("own", 4), c("la-sea", 2), c("uta-la", 3)),
				testScenarioClause(c("own", 4), c("la-sea", 6), c("uta-la", 2)),
			},
			want: []string{
				"LA draws vs SEA + UTA wins or draws vs LA",
				"SEA wins at LA + UTA draws vs LA",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := fmt.Sprintf("%#v", test.clauses)
			groups := clinchingGroups(test.clauses, "njy", teams, games)
			if len(groups) != 1 || groups[0].Heading != "If NJY wins at NC" {
				t.Fatalf("groups = %+v", groups)
			}
			paths := []string{}
			for _, path := range groups[0].Help.Combinations() {
				words := []string{}
				for _, requirement := range path {
					words = append(words, requirement.Text)
				}
				paths = append(paths, strings.Join(words, " + "))
			}
			slices.Sort(paths)
			slices.Sort(test.want)
			if !slices.Equal(paths, test.want) {
				t.Fatalf("paths = %q, want %q", paths, test.want)
			}
			assertScenarioCoverage(t, test.clauses, groups, games, true)
			if fmt.Sprintf("%#v", test.clauses) != original {
				t.Fatal("presentation mutated stored proof")
			}
			slices.Reverse(test.clauses)
			if !reflect.DeepEqual(groups, clinchingGroups(test.clauses, "njy", teams, games)) {
				t.Fatal("path selection depends on input clause order")
			}
		})
	}
}

func TestClinchingOverlapPreservesEveryTwoFixtureUnion(t *testing.T) {
	games := map[string]cache.Game{"a": {HomeTeamID: "a1", AwayTeamID: "a2"}, "b": {HomeTeamID: "b1", AwayTeamID: "b2"}}
	// Exhaust every pair of nonempty masks, including unrestricted fixtures.
	for a := uint8(1); a <= 7; a++ {
		for b := uint8(1); b <= 7; b++ {
			for c := uint8(1); c <= 7; c++ {
				for d := uint8(1); d <= 7; d++ {
					clauses := []scenarios.Clause{
						testScenarioClause(testScenarioCondition("a", a), testScenarioCondition("b", b)),
						testScenarioClause(testScenarioCondition("a", c), testScenarioCondition("b", d)),
					}
					groups := clinchingGroups(clauses, "own", nil, games)
					assertScenarioCoverage(t, clauses, groups, games, true)
				}
			}
		}
	}
}

func TestClinchingOverlapSplitsResidualAndRemovesCollectiveCoverage(t *testing.T) {
	games := map[string]cache.Game{"a": {}, "b": {}, "c": {}}
	c := testScenarioCondition
	for _, test := range []struct {
		name    string
		clauses []scenarios.Clause
		paths   int
	}{
		{
			name: "wholly covered path disappears",
			clauses: []scenarios.Clause{
				testScenarioClause(c("a", 1), c("b", 1)),
				testScenarioClause(c("a", 3), c("b", 3)),
			},
			paths: 1, // The first clause is wholly covered before subtraction.
		},
		{
			name: "partial overlap needs two residual paths",
			clauses: []scenarios.Clause{
				testScenarioClause(c("a", 1), c("b", 1)),
				testScenarioClause(c("a", 3), c("c", 1)),
			},
			paths: 3,
		},
		{
			name: "two paths together cover a third",
			clauses: []scenarios.Clause{
				testScenarioClause(c("a", 1), c("b", 3)),
				testScenarioClause(c("a", 2), c("b", 3)),
				testScenarioClause(c("a", 3), c("b", 1)),
			},
			paths: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			groups := clinchingGroups(test.clauses, "own", nil, games)
			if len(groups) != 1 || len(groups[0].Help.Combinations()) != test.paths {
				t.Fatalf("groups = %+v", groups)
			}
			assertScenarioCoverage(t, test.clauses, groups, games, true)
		})
	}
}

func TestClinchingOverlapBoundsExpansionWithoutLosingPaths(t *testing.T) {
	for _, count := range []int{16, maxDisjointPaths + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			clauses := [][]scenarioRequirement{}
			for i := range count {
				clauses = append(clauses, []scenarioRequirement{
					{GameID: fmt.Sprintf("%03d-a", i), Mask: 1},
					{GameID: fmt.Sprintf("%03d-b", i), Mask: 1},
				})
			}
			// Independent pairs require exponentially many disjoint residuals;
			// both expansion overflow and oversized inputs retain the exact input.
			if got := disjointRequirements(clauses); !reflect.DeepEqual(got, clauses) {
				t.Fatalf("fallback did not preserve the original %d paths", count)
			}
		})
	}
}
