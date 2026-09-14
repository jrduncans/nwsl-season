package app

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

// A requirement is either an allowed result for one fixture, or an exactly
// equivalent points threshold across two specified fixtures. Identity uses IDs,
// never display names, so similarly named teams or repeat opponents stay distinct.
type scenarioRequirement struct {
	GameID               string
	Mask                 uint8
	TeamID, SecondGameID string
	Points               int
	Text                 string
}

type scenarioExpression struct {
	Conditions   []scenarioRequirement
	Alternatives []scenarioExpression
}

func (e scenarioExpression) VisibleAlternatives() []scenarioExpression {
	return e.Alternatives[:min(3, len(e.Alternatives))]
}
func (e scenarioExpression) AdditionalAlternatives() []scenarioExpression {
	return e.Alternatives[min(3, len(e.Alternatives)):]
}

type clinchingGroupView struct {
	Heading        string
	Own            []scenarioRequirement
	AlternativeOwn []scenarioRequirement
	Help           scenarioExpression
}

var scenarioOutcomes = []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin}

func outcomeMask(outcomes []clinching.Outcome) uint8 {
	var mask uint8
	for i, outcome := range scenarioOutcomes {
		if slices.Contains(outcomes, outcome) {
			mask |= 1 << i
		}
	}
	return mask
}

func requirementText(r scenarioRequirement, perspective string, teams map[string]string, games map[string]cache.Game) string {
	if r.SecondGameID != "" {
		unit := "points"
		if r.Points == 1 {
			unit = "point"
		}
		return fmt.Sprintf("%s earns at least %d %s from these two matches: %s", teams[r.TeamID], r.Points, unit, noHelpFixtureText(clinching.NoHelpPath{FixtureIDs: []string{r.GameID, r.SecondGameID}}, r.TeamID, games, teams))
	}
	outcomes := []clinching.Outcome{}
	for i, o := range scenarioOutcomes {
		if r.Mask&(1<<i) != 0 {
			outcomes = append(outcomes, o)
		}
	}
	g := games[r.GameID]
	if perspective != "" && len(outcomes) == 1 {
		opponent, win := g.AwayTeamID, clinching.HomeWin
		if g.AwayTeamID == perspective {
			opponent, win = g.HomeTeamID, clinching.AwayWin
		}
		verb := "loses to"
		switch outcomes[0] {
		case win:
			verb = "beats"
		case clinching.Draw:
			verb = "draws with"
		}
		return teams[perspective] + " " + verb + " " + teams[opponent]
	}
	return conditionText(scenarios.FixtureCondition{GameID: r.GameID, AllowedOutcomes: outcomes}, teams, games)
}

func requirementKey(r scenarioRequirement) string {
	return strconv.Quote(r.GameID) + ":" + strconv.Itoa(int(r.Mask)) + ":" + strconv.Quote(r.TeamID) + ":" + strconv.Quote(r.SecondGameID) + ":" + strconv.Itoa(r.Points)
}
func conjunctionKey(conditions []scenarioRequirement) string {
	keys := make([]string, len(conditions))
	for i, c := range conditions {
		keys[i] = requirementKey(c)
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}
func uniqueConjunctions(clauses [][]scenarioRequirement) [][]scenarioRequirement {
	out := [][]scenarioRequirement{}
	seen := map[string]bool{}
	for _, c := range clauses {
		key := conjunctionKey(c)
		if !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	return out
}

// Split overlapping own-result masks (for example win and win-or-draw) into
// distinct outcome groups. Only games mentioned by the proofs need partitioning.
// For unusually large slates, retain the original overlapping groups instead of
// expanding more than 3^4 profiles. Both representations preserve the same union.
func clinchingGroups(clauses []scenarios.Clause, teamID string, teams map[string]string, games map[string]cache.Game) []clinchingGroupView {
	ownIDs := []string{}
	raw := [][]scenarioRequirement{}
	for _, clause := range clauses {
		c := []scenarioRequirement{}
		for _, condition := range clause.Conditions {
			mask := outcomeMask(condition.AllowedOutcomes)
			if mask == 7 {
				continue
			}
			r := scenarioRequirement{GameID: condition.GameID, Mask: mask}
			c = append(c, r)
			g := games[r.GameID]
			if (g.HomeTeamID == teamID || g.AwayTeamID == teamID) && !slices.Contains(ownIDs, r.GameID) {
				ownIDs = append(ownIDs, r.GameID)
			}
		}
		raw = append(raw, c)
	}
	sort.Slice(ownIDs, func(i, j int) bool {
		a, b := games[ownIDs[i]], games[ownIDs[j]]
		if a.KickoffUTC != b.KickoffUTC {
			return a.KickoffUTC < b.KickoffUTC
		}
		return ownIDs[i] < ownIDs[j]
	})
	raw = uniqueConjunctions(raw)
	type group struct {
		own  []scenarioRequirement
		help [][]scenarioRequirement
	}
	groups := []group{}
	if len(ownIDs) <= 4 {
		var visit func(int, []scenarioRequirement, [][]scenarioRequirement)
		visit = func(index int, own []scenarioRequirement, active [][]scenarioRequirement) {
			if len(active) == 0 {
				return
			}
			if index == len(ownIDs) {
				groups = append(groups, group{own: own, help: active})
				return
			}
			id := ownIDs[index]
			masks := []uint8{1, 2, 4}
			if games[id].AwayTeamID == teamID {
				masks = []uint8{4, 2, 1}
			}
			for _, mask := range masks {
				next := [][]scenarioRequirement{}
				for _, c := range active {
					rest := []scenarioRequirement{}
					allowed := true
					for _, r := range c {
						if r.GameID == id {
							allowed = allowed && r.Mask&mask != 0
						} else {
							rest = append(rest, r)
						}
					}
					if allowed {
						next = append(next, rest)
					}
				}
				visit(index+1, append(slices.Clone(own), scenarioRequirement{GameID: id, Mask: mask}), next)
			}
		}
		visit(0, nil, raw)
	} else {
		indices := map[string]int{}
		for _, c := range raw {
			own, help := []scenarioRequirement{}, []scenarioRequirement{}
			for _, r := range c {
				if slices.Contains(ownIDs, r.GameID) {
					own = append(own, r)
				} else {
					help = append(help, r)
				}
			}
			key := conjunctionKey(own)
			index, ok := indices[key]
			if !ok {
				index = len(groups)
				indices[key] = index
				groups = append(groups, group{own: own})
			}
			groups[index].help = append(groups[index].help, help)
		}
	}
	views := []clinchingGroupView{}
	for _, g := range groups {
		words := []string{}
		for i, r := range g.own {
			g.own[i].Text = requirementText(r, teamID, teams, games)
			words = append(words, g.own[i].Text)
		}
		heading := "Regardless of " + teams[teamID] + "’s results"
		if len(words) > 0 {
			heading = "If " + joinConditions(words)
		}
		help := removeCoveredRequirements(compactPoints(removeCoveredRequirements(uniqueConjunctions(g.help), games), games), games)
		for i, c := range help {
			for j, r := range c {
				help[i][j].Text = requirementText(r, "", teams, games)
			}
		}
		views = append(views, clinchingGroupView{Heading: heading, Own: g.own, Help: orderScenarioExpression(factorRequirements(help, 0))})
	}
	return combineOwnPairs(views, teamID, teams, games)
}

// Factor only identical predicates: A&B OR A&C becomes A AND (B OR C).
// Limit nesting for readability; a flat OR retains every remaining alternative.
func factorRequirements(clauses [][]scenarioRequirement, depth int) scenarioExpression {
	clauses = uniqueConjunctions(clauses)
	for _, c := range clauses {
		if len(c) == 0 {
			return scenarioExpression{}
		}
	}
	if len(clauses) == 1 {
		return scenarioExpression{Conditions: clauses[0]}
	}
	counts := map[string]int{}
	best := scenarioRequirement{}
	count := 1
	for _, c := range clauses {
		for _, r := range c {
			key := requirementKey(r)
			counts[key]++
			if counts[key] > count {
				best = r
				count = counts[key]
			}
		}
	}
	if count == 1 || depth >= 3 {
		e := scenarioExpression{}
		for _, c := range clauses {
			e.Alternatives = append(e.Alternatives, scenarioExpression{Conditions: c})
		}
		return e
	}
	with, without := [][]scenarioRequirement{}, [][]scenarioRequirement{}
	for _, c := range clauses {
		rest := []scenarioRequirement{}
		found := false
		for _, r := range c {
			if requirementKey(r) == requirementKey(best) {
				found = true
			} else {
				rest = append(rest, r)
			}
		}
		if found {
			with = append(with, rest)
		} else {
			without = append(without, c)
		}
	}
	branch := factorRequirements(with, depth+1)
	branch.Conditions = append([]scenarioRequirement{best}, branch.Conditions...)
	if len(without) == 0 {
		return branch
	}
	other := factorRequirements(without, depth+1)
	alternatives := []scenarioExpression{branch}
	if len(other.Conditions) == 0 && len(other.Alternatives) > 0 {
		alternatives = append(alternatives, other.Alternatives...)
	} else {
		alternatives = append(alternatives, other)
	}
	return scenarioExpression{Alternatives: alternatives}
}

// Combine alternatives differing only in two matches of the same team when
// their complete nine-outcome truth table equals a minimum-points condition.
func compactPoints(clauses [][]scenarioRequirement, games map[string]cache.Game) [][]scenarioRequirement {
	ids := []string{}
	for _, c := range clauses {
		for _, r := range c {
			if !slices.Contains(ids, r.GameID) {
				ids = append(ids, r.GameID)
			}
		}
	}
	sort.Strings(ids)
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			ga, gb := games[a], games[b]
			for _, team := range []string{ga.HomeTeamID, ga.AwayTeamID} {
				if team == "" || (team != gb.HomeTeamID && team != gb.AwayTeamID) {
					continue
				}
				clauses = compactPointsPair(clauses, a, b, team, games)
			}
		}
	}
	return uniqueConjunctions(clauses)
}

func compactPointsPair(clauses [][]scenarioRequirement, a, b, team string, games map[string]cache.Game) [][]scenarioRequirement {
	type candidate struct {
		rest     []scenarioRequirement
		indices  []int
		coverage [9]bool
	}
	candidates := []candidate{}
	byKey := map[string]int{}
	for index, c := range clauses {
		rest := []scenarioRequirement{}
		ma, mb := uint8(7), uint8(7)
		for _, r := range c {
			if r.SecondGameID == "" && r.GameID == a {
				ma = r.Mask
			} else if r.SecondGameID == "" && r.GameID == b {
				mb = r.Mask
			} else {
				rest = append(rest, r)
			}
		}
		key := conjunctionKey(rest)
		n, ok := byKey[key]
		if !ok {
			n = len(candidates)
			byKey[key] = n
			candidates = append(candidates, candidate{rest: rest})
		}
		v := &candidates[n]
		v.indices = append(v.indices, index)
		for x := range 3 {
			for y := range 3 {
				if ma&(1<<x) != 0 && mb&(1<<y) != 0 {
					v.coverage[x*3+y] = true
				}
			}
		}
	}
	replacements := map[int][]scenarioRequirement{}
	removed := map[int]bool{}
	for _, c := range candidates {
		if len(c.indices) < 2 {
			continue
		}
		for threshold := 1; threshold <= 6; threshold++ {
			equal := true
			for x := range 3 {
				for y := range 3 {
					points := scenarioPoints(x, team, games[a]) + scenarioPoints(y, team, games[b])
					if c.coverage[x*3+y] != (points >= threshold) {
						equal = false
					}
				}
			}
			if !equal {
				continue
			}
			replacements[c.indices[0]] = append(slices.Clone(c.rest), scenarioRequirement{GameID: a, SecondGameID: b, TeamID: team, Points: threshold})
			for _, index := range c.indices[1:] {
				removed[index] = true
			}
			break
		}
	}
	out := [][]scenarioRequirement{}
	for i, c := range clauses {
		if replacement, ok := replacements[i]; ok {
			out = append(out, replacement)
		} else if !removed[i] {
			out = append(out, c)
		}
	}
	return out
}

func scenarioPoints(outcome int, team string, game cache.Game) int {
	if outcome == 1 {
		return 1
	}
	if (outcome == 0 && game.HomeTeamID == team) || (outcome == 2 && game.AwayTeamID == team) {
		return 3
	}
	return 0
}

// A stronger sufficient clause adds no outcomes if a weaker clause already
// covers it. Bound the pairwise pass for large, unminimized partial batches.
func removeCoveredRequirements(clauses [][]scenarioRequirement, games map[string]cache.Game) [][]scenarioRequirement {
	for _, c := range clauses {
		if len(c) == 0 {
			return [][]scenarioRequirement{{}}
		}
	}
	if len(clauses) > 256 {
		return clauses
	}
	kept := [][]scenarioRequirement{}
	for i, source := range clauses {
		covered := false
		for j, target := range clauses {
			if i == j || !requirementsImply(source, target, games) {
				continue
			}
			// For equivalent clauses retain the first, rather than removing both.
			if j < i || !requirementsImply(target, source, games) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, source)
		}
	}
	return kept
}

func requirementsImply(source, target []scenarioRequirement, games map[string]cache.Game) bool {
	for _, want := range target {
		for x := range 3 {
			for y := range 3 {
				assignment := map[string]int{want.GameID: x}
				if want.SecondGameID != "" {
					assignment[want.SecondGameID] = y
				}
				possible := true
				for _, have := range source {
					_, first := assignment[have.GameID]
					_, second := assignment[have.SecondGameID]
					if first && (have.SecondGameID == "" || second) && !requirementMatches(have, assignment, games) {
						possible = false
						break
					}
				}
				if possible && !requirementMatches(want, assignment, games) {
					return false
				}
			}
		}
	}
	return true
}

func requirementMatches(r scenarioRequirement, assignment map[string]int, games map[string]cache.Game) bool {
	if r.SecondGameID == "" {
		return r.Mask&(1<<assignment[r.GameID]) != 0
	}
	return scenarioPoints(assignment[r.GameID], r.TeamID, games[r.GameID])+scenarioPoints(assignment[r.SecondGameID], r.TeamID, games[r.SecondGameID]) >= r.Points
}

func orderScenarioExpression(e scenarioExpression) scenarioExpression {
	for i, alternative := range e.Alternatives {
		e.Alternatives[i] = orderScenarioExpression(alternative)
	}
	sort.SliceStable(e.Alternatives, func(i, j int) bool { return expressionSize(e.Alternatives[i]) < expressionSize(e.Alternatives[j]) })
	return e
}
func expressionSize(e scenarioExpression) int {
	count := len(e.Conditions)
	for _, alternative := range e.Alternatives {
		count += expressionSize(alternative)
	}
	return count
}

// When either ordering of the same two own results has identical outside help,
// name that combination once (for example win one and lose the other).
func combineOwnPairs(groups []clinchingGroupView, team string, teams map[string]string, games map[string]cache.Game) []clinchingGroupView {
	out := []clinchingGroupView{}
	for _, group := range groups {
		if len(group.Own) != 2 || !singleResult(group.Own[0].Mask) || !singleResult(group.Own[1].Mask) {
			out = append(out, group)
			continue
		}
		results := [2]int{ownPoints(group.Own[0], team, games), ownPoints(group.Own[1], team, games)}
		matches := noHelpFixtureText(clinching.NoHelpPath{FixtureIDs: []string{group.Own[0].GameID, group.Own[1].GameID}}, team, games, teams)
		verbs := map[int]string{0: "loses", 1: "draws", 3: "wins"}
		if results[0] == results[1] {
			group.Heading = fmt.Sprintf("If %s %s both matches (%s)", teams[team], verbs[results[0]], matches)
		} else {
			merged := false
			for i := range out {
				prior := &out[i]
				if len(prior.Own) != 2 || len(prior.AlternativeOwn) != 0 || prior.Own[0].GameID != group.Own[0].GameID || prior.Own[1].GameID != group.Own[1].GameID {
					continue
				}
				if ownPoints(prior.Own[0], team, games) != results[1] || ownPoints(prior.Own[1], team, games) != results[0] || expressionKey(prior.Help) != expressionKey(group.Help) {
					continue
				}
				prior.AlternativeOwn = group.Own
				prior.Heading = fmt.Sprintf("If %s %s one match and %s the other (%s)", teams[team], verbs[max(results[0], results[1])], verbs[min(results[0], results[1])], matches)
				merged = true
				break
			}
			if merged {
				continue
			}
		}
		out = append(out, group)
	}
	return out
}
func singleResult(mask uint8) bool { return mask == 1 || mask == 2 || mask == 4 }
func ownPoints(r scenarioRequirement, team string, games map[string]cache.Game) int {
	for i := range 3 {
		if r.Mask == 1<<i {
			return scenarioPoints(i, team, games[r.GameID])
		}
	}
	return -1
}
func expressionKey(e scenarioExpression) string {
	alternatives := []string{}
	for _, a := range e.Alternatives {
		alternatives = append(alternatives, expressionKey(a))
	}
	sort.Strings(alternatives)
	return strconv.Quote(conjunctionKey(e.Conditions)) + "[" + strings.Join(alternatives, ",") + "]"
}
