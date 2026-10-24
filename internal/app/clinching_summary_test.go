package app

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func summaryScore(team, rival string, minimum int, exact bool) scenarioRequirement {
	return scenarioRequirement{Score: &scenarios.ScoreCondition{TeamID: team, RivalID: rival, Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: minimum, ExactOnly: exact}}}}
}

func summaryFixture(id string, mask uint8) scenarioRequirement {
	return scenarioRequirement{GameID: id, Mask: mask}
}

func summaryProduct(left, right [][]scenarioRequirement) [][]scenarioRequirement {
	product := [][]scenarioRequirement{}
	for _, a := range left {
		for _, b := range right {
			product = append(product, append(slices.Clone(a), b...))
		}
	}
	return product
}

func assertSameSummaryPaths(t *testing.T, got, want [][]scenarioRequirement) {
	t.Helper()
	keys := func(paths [][]scenarioRequirement) []string {
		out := []string{}
		for _, path := range uniqueConjunctions(paths) {
			out = append(out, conjunctionKey(path))
		}
		slices.Sort(out)
		return out
	}
	if !reflect.DeepEqual(keys(got), keys(want)) {
		t.Fatalf("grouping changed the certified paths:\ngot %v\nwant %v", keys(got), keys(want))
	}
}

func TestClinchingSeasonSummaryGroupsKCAndUtahLosses(t *testing.T) {
	games := map[string]cache.Game{
		"kc-was":  {HomeTeamID: "kc", AwayTeamID: "was"},
		"lou-kc":  {HomeTeamID: "lou", AwayTeamID: "kc"},
		"njy-uta": {HomeTeamID: "njy", AwayTeamID: "uta"},
		"hou-por": {HomeTeamID: "hou", AwayTeamID: "por"},
		"la-bay":  {HomeTeamID: "la", AwayTeamID: "bay", KickoffUTC: "2026-10-24T00:00:00Z"},
		"la-bos":  {HomeTeamID: "la", AwayTeamID: "bos", KickoffUTC: "2026-11-01T00:00:00Z"},
		"sea-orl": {HomeTeamID: "sea", AwayTeamID: "orl"},
	}
	teams := map[string]string{}
	for _, g := range games {
		teams[g.HomeTeamID], teams[g.AwayTeamID] = g.HomeTeamID, g.AwayTeamID
	}
	for _, team := range []string{"kc", "uta"} {
		t.Run(team, func(t *testing.T) {
			la := summaryScore(team, "la", 5, false)
			angelCity := [][]scenarioRequirement{
				{summaryFixture("la-bay", 4), summaryFixture("la-bos", 4)},
				{summaryFixture("la-bay", 4), summaryFixture("la-bos", 2), la},
				{summaryFixture("la-bay", 2), summaryFixture("la-bos", 4), la},
			}
			shared := []scenarioRequirement{summaryFixture("kc-was", 4), summaryFixture("lou-kc", 1), summaryFixture("njy-uta", 1), summaryFixture("hou-por", 1), summaryScore(team, "por", 0, false)}
			seattle := [][]scenarioRequirement{
				{summaryFixture("sea-orl", 6), summaryScore(team, "uta", -4, false)},
				{summaryFixture("sea-orl", 1), summaryScore(team, "sea", -8, true), summaryScore(team, "uta", -4, true)},
				{summaryFixture("sea-orl", 1), summaryScore(team, "sea", -8, false), summaryScore(team, "uta", -4, false)},
			}
			wantPaths, wantRivals := 9, 3
			if team == "uta" {
				shared = append(shared, summaryScore(team, "kc", 6, false))
				seattle = [][]scenarioRequirement{{summaryFixture("sea-orl", 6)}, {summaryFixture("sea-orl", 1), summaryScore(team, "sea", -3, false)}}
				wantPaths, wantRivals = 6, 4
			}
			paths := summaryProduct([][]scenarioRequirement{shared}, summaryProduct(angelCity, seattle))
			clauses := []scenarios.Clause{}
			for _, path := range paths {
				clause := scenarios.Clause{}
				for _, r := range path {
					if r.Score != nil {
						clause.ScoreConditions = append(clause.ScoreConditions, *r.Score)
					} else {
						clause.Conditions = append(clause.Conditions, testScenarioCondition(r.GameID, r.Mask))
					}
				}
				clauses = append(clauses, clause)
			}
			original := fmt.Sprintf("%#v", clauses)
			options := clinchingGroupOptions{SummaryLimit: maxSeasonSummaryPaths, RestOfSeason: true}
			views := clinchingGroupViews(clauses, team, teams, teams, games, options)
			if len(views) != 1 || views[0].GroupedSummary == nil {
				t.Fatalf("loss scenario should have a grouped summary: %+v", views)
			}
			summary := views[0].GroupedSummary
			if len(summary.Groups) != 4 || len(views[0].Summary.Combinations()) != wantPaths {
				t.Fatalf("want %d original paths in four rival groups: %+v", wantPaths, summary)
			}
			expanded := [][]scenarioRequirement{{}}
			rivals := 0
			for _, group := range summary.Groups {
				expanded = summaryProduct(expanded, group.Expression.Combinations())
				if group.Rival != nil {
					rivals++
				}
			}
			if rivals != wantRivals {
				t.Fatalf("want %d compact rival statements, got %d", wantRivals, rivals)
			}
			assertSameSummaryPaths(t, expanded, views[0].Summary.Combinations())
			if fmt.Sprintf("%#v", clauses) != original {
				t.Fatal("grouping mutated stored proofs")
			}
			if !reflect.DeepEqual(views, clinchingGroupViews(clauses, team, teams, teams, games, options)) {
				t.Fatal("grouping is not deterministic")
			}
			options.RestOfSeason = false
			if next := clinchingGroupViews(clauses, team, teams, teams, games, options); next[0].GroupedSummary != nil {
				t.Fatal("a larger summary limit must not enable rest-of-season grouping")
			}
			options.RestOfSeason, options.SummaryLimit = true, maxScenarioSummaryPaths
			if season := clinchingGroupViews(clauses, team, teams, teams, games, options); season[0].GroupedSummary == nil {
				t.Fatal("rest-of-season grouping must not depend on its summary limit")
			}
		})
	}
}

func TestClinchingIndependentGroupsPreserveAllPaths(t *testing.T) {
	games := map[string]cache.Game{
		"la-bay":  {HomeTeamID: "la", AwayTeamID: "bay"},
		"la-bos":  {HomeTeamID: "la", AwayTeamID: "bos"},
		"sea-orl": {HomeTeamID: "sea", AwayTeamID: "orl"},
	}
	variables := []scenarioRequirement{
		summaryFixture("la-bay", 4), summaryFixture("la-bos", 2), summaryScore("kc", "la", 5, false),
		summaryFixture("sea-orl", 1), summaryScore("kc", "sea", 3, false), summaryScore("kc", "uta", 2, true),
	}
	random := rand.New(rand.NewPCG(204, 42)) // #nosec G404 -- reproducible proof cases, not security randomness.
	for range 300 {
		paths := [][]scenarioRequirement{}
		for range 1 + random.IntN(20) {
			path := []scenarioRequirement{}
			for _, r := range variables {
				if random.IntN(2) == 0 {
					path = append(path, r)
				}
			}
			paths = append(paths, path)
		}
		expanded := [][]scenarioRequirement{{}}
		for _, group := range independentRivalGroups(paths, "kc", games) {
			expanded = summaryProduct(expanded, group)
		}
		assertSameSummaryPaths(t, expanded, paths)
	}
	// Both rivals' conditions occur, but one combined case is missing. It
	// must stay coupled rather than inventing that fourth combination.
	paths := [][]scenarioRequirement{{variables[0], variables[3]}, {variables[0], variables[4]}, {variables[2], variables[3]}}
	if groups := independentRivalGroups(paths, "kc", games); len(groups) != 1 {
		t.Fatalf("a missing Cartesian combination must not be invented: %+v", groups)
	}
}

func TestClinchingSummaryRivalNeedChecksEveryResult(t *testing.T) {
	games := map[string]cache.Game{
		"la-bay": {HomeTeamID: "la", AwayTeamID: "bay"},
		"la-bos": {HomeTeamID: "la", AwayTeamID: "bos"},
	}
	score := summaryScore("kc", "la", 5, false)
	paths := [][]scenarioRequirement{
		{summaryFixture("la-bay", 4), summaryFixture("la-bos", 4)},
		{summaryFixture("la-bay", 4), summaryFixture("la-bos", 2), score},
		{summaryFixture("la-bay", 2), summaryFixture("la-bos", 4), score},
	}
	need := summaryRivalNeed(paths, "kc", "la", games)
	if need == nil || need.MaxPoints != 0 || len(need.Ties) != 1 || need.Ties[0].Points != 1 {
		t.Fatalf("want two losses, or one point with the score condition: %+v", need)
	}
	for x := range 3 {
		for y := range 3 {
			for _, scoreHolds := range []bool{false, true} {
				assignment := map[string]int{"la-bay": x, "la-bos": y}
				want := false
				for _, path := range paths {
					matches := true
					for _, r := range path {
						if r.Score != nil {
							matches = matches && scoreHolds
						} else {
							matches = matches && requirementMatches(r, assignment, games)
						}
					}
					want = want || matches
				}
				points := scenarioPoints(x, "la", games["la-bay"]) + scenarioPoints(y, "la", games["la-bos"])
				got := points <= need.MaxPoints || (points == need.Ties[0].Points && scoreHolds)
				if got != want {
					t.Fatalf("results (%d,%d), score=%t: got %t, want %t", x, y, scoreHolds, got, want)
				}
			}
		}
	}
	if summaryRivalNeed(paths[:2], "kc", "la", games) != nil {
		t.Fatal("missing the other draw/loss ordering must prevent a points rewrite")
	}
	differentScore := slices.Clone(paths)
	differentScore[2] = []scenarioRequirement{summaryFixture("la-bay", 2), summaryFixture("la-bos", 4), summaryScore("kc", "la", 6, false)}
	if summaryRivalNeed(differentScore, "kc", "la", games) != nil {
		t.Fatal("different score requirements for the same points must not merge")
	}
}

func TestClinchingSummaryRivalRewritePreservesScoresAndResults(t *testing.T) {
	games := map[string]cache.Game{
		"la-bay": {HomeTeamID: "la", AwayTeamID: "bay"},
		"bos-la": {HomeTeamID: "bos", AwayTeamID: "la"},
	}
	scores := []scenarioRequirement{summaryScore("kc", "la", 5, false), summaryScore("kc", "la", 6, false)}
	masks := []uint8{1, 2, 3, 4, 5, 6}
	random := rand.New(rand.NewPCG(204, 99)) // #nosec G404 -- reproducible proof cases, not security randomness.
	for range 300 {
		paths := [][]scenarioRequirement{}
		for range 1 + random.IntN(8) {
			path := []scenarioRequirement{summaryFixture("la-bay", masks[random.IntN(len(masks))])}
			if random.IntN(2) == 0 {
				path = append(path, summaryFixture("bos-la", masks[random.IntN(len(masks))]))
			}
			if random.IntN(2) == 0 {
				path = append(path, scores[random.IntN(len(scores))])
			}
			paths = append(paths, path)
		}
		need := summaryRivalNeed(paths, "kc", "la", games)
		if need == nil {
			continue
		}
		for x := range 3 {
			for y := range 3 {
				for truth := range 4 {
					assignment := map[string]int{"la-bay": x, "bos-la": y}
					holds := map[string]bool{scoreConditionKey(*scores[0].Score): truth&1 != 0, scoreConditionKey(*scores[1].Score): truth&2 != 0}
					want := false
					for _, path := range paths {
						matches := true
						for _, r := range path {
							if r.Score != nil {
								matches = matches && holds[scoreConditionKey(*r.Score)]
							} else {
								matches = matches && requirementMatches(r, assignment, games)
							}
						}
						want = want || matches
					}
					points, wins := 0, 0
					for _, id := range need.GameIDs {
						p := scenarioPoints(assignment[id], "la", games[id])
						points += p
						if p == 3 {
							wins++
						}
					}
					got := points <= need.MaxPoints
					for _, tie := range need.Ties {
						got = got || (points == tie.Points && wins == tie.Wins && holds[scoreConditionKey(*tie.Condition)])
					}
					if got != want {
						t.Fatalf("results=%v, scores=%v: rewrite=%t, original=%t; paths=%+v, need=%+v", assignment, holds, got, want, paths, need)
					}
				}
			}
		}
	}
}
