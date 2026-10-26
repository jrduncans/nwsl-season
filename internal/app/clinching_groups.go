package app

import (
	"fmt"
	"math/bits"
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
	// Score is a rest-of-season tiebreaker condition. It is opaque to the
	// fixture-mask rewrites: it never merges, narrows or is subtracted, and a
	// clause implies it only by containing the same condition.
	Score       *scenarios.ScoreCondition
	Text        string
	Parts       []scenarioRequirementPart
	Explanation []scenarioRequirementPart
	Historical  string
}

type scenarioRequirementPart struct {
	Text string
	Team *scenarioTeamToken
	// NoSpaceBefore attaches punctuation to the preceding team token.
	NoSpaceBefore bool
	BreakBefore   bool
}

type scenarioTeamToken struct {
	Code, LogoURL string
}

type scenarioExpression struct {
	Conditions   []scenarioRequirement
	Alternatives []scenarioExpression
}

// Combinations expands the factored expression into complete, readable paths.
// Factoring reduces work when finding the proof, but the display is clearer
// when every line states all of the requirements joined by “+”.
func (e scenarioExpression) Combinations() [][]scenarioRequirement {
	if len(e.Alternatives) == 0 {
		return [][]scenarioRequirement{slices.Clone(e.Conditions)}
	}
	combinations := [][]scenarioRequirement{}
	for _, alternative := range e.Alternatives {
		for _, combination := range alternative.Combinations() {
			combinations = append(combinations, append(slices.Clone(e.Conditions), combination...))
		}
	}
	return uniqueConjunctions(combinations)
}

type clinchingGroupView struct {
	Heading         string
	HeadingParts    []scenarioRequirementPart
	UnrestrictedOwn []string
	Own             []scenarioRequirement
	AlternativeOwn  []scenarioRequirement
	Summary         scenarioExpression
	GroupedSummary  *clinchingGroupedSummary
	SummaryOverflow bool
	Help            scenarioExpression
	// Tone colors the group by the team's own result: "win", "draw", "loss"
	// or "" when the group spans several matches or none.
	Tone string
	// A rest-of-season group with a rival summary states each rival instead
	// of a summary expression, under RivalState ("Clinches if all of:").
	// MergeKey stands in for the outside help when merging own results.
	Rivals      []clinchingRivalView
	RivalState  string
	RivalChoice bool // only some of the rival requirements need to hold
	MergeKey    string
	// paths are the group's summary paths, kept even when they overflow the
	// summary limit so a caller can describe them another way.
	paths [][]scenarioRequirement
}

// mergeKey identifies a group's outside help, so own results with the same
// help can share a heading.
func (g clinchingGroupView) mergeKey() string {
	if g.MergeKey != "" {
		return g.MergeKey
	}
	return expressionKey(g.Help)
}

const maxScenarioSummaryPaths = 12

// maxSeasonSummaryPaths lets rest-of-season summaries show every scenario,
// within the same bound as the pairwise coverage pass: those scenarios are
// the whole answer, so they are written out rather than left to the exact
// list.
const maxSeasonSummaryPaths = 256

type clinchingGroupOptions struct {
	SummaryLimit int
	RestOfSeason bool
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
	if r.Score != nil {
		return scoreConditionText(*r.Score, teams)
	}
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
	if perspective != "" {
		opponent, win := g.AwayTeamID, clinching.HomeWin
		venue := "vs"
		if g.AwayTeamID == perspective {
			opponent, win = g.HomeTeamID, clinching.AwayWin
			venue = "at"
		}
		loss := clinching.AwayWin
		if win == clinching.AwayWin {
			loss = clinching.HomeWin
		}
		verb := ""
		switch r.Mask {
		case outcomeMask([]clinching.Outcome{win}):
			verb = "wins"
		case outcomeMask([]clinching.Outcome{clinching.Draw}):
			verb = "draws"
		case outcomeMask([]clinching.Outcome{loss}):
			verb = "loses"
		case outcomeMask([]clinching.Outcome{win, clinching.Draw}):
			verb = "wins or draws"
		case outcomeMask([]clinching.Outcome{loss, clinching.Draw}):
			verb = "draws or loses"
		case outcomeMask([]clinching.Outcome{win, loss}):
			verb = "does not draw"
		}
		if verb != "" {
			return teams[perspective] + " " + verb + " " + venue + " " + teams[opponent]
		}
	}
	return conditionText(scenarios.FixtureCondition{GameID: r.GameID, AllowedOutcomes: outcomes}, teams, games)
}

func requirementParts(r scenarioRequirement, perspective string, teams map[string]string, games map[string]cache.Game) []scenarioRequirementPart {
	if r.Score != nil {
		return scoreConditionParts(*r.Score, teams)
	}
	if r.SecondGameID != "" {
		unit := "points"
		if r.Points == 1 {
			unit = "point"
		}
		parts := []scenarioRequirementPart{
			{Team: &scenarioTeamToken{Code: teams[r.TeamID], LogoURL: clubLogoURL(r.TeamID)}},
			{Text: fmt.Sprintf("earns at least %d %s from these two matches:", r.Points, unit)},
		}
		fixtures := 0
		for _, gameID := range []string{r.GameID, r.SecondGameID} {
			game, ok := games[gameID]
			if !ok {
				continue
			}
			opponent, venue := "", ""
			switch {
			case game.HomeTeamID == r.TeamID:
				opponent, venue = game.AwayTeamID, "vs"
			case game.AwayTeamID == r.TeamID:
				opponent, venue = game.HomeTeamID, "at"
			}
			if opponent == "" {
				continue
			}
			if fixtures > 0 {
				venue = "and " + venue
			}
			parts = append(parts,
				scenarioRequirementPart{Text: venue},
				scenarioRequirementPart{Team: &scenarioTeamToken{Code: teams[opponent], LogoURL: clubLogoURL(opponent)}},
			)
			fixtures++
		}
		return parts
	}
	outcomes := []clinching.Outcome{}
	for i, outcome := range scenarioOutcomes {
		if r.Mask&(1<<i) != 0 {
			outcomes = append(outcomes, outcome)
		}
	}
	g := games[r.GameID]
	team := func(id string) scenarioRequirementPart {
		return scenarioRequirementPart{Team: &scenarioTeamToken{Code: teams[id], LogoURL: clubLogoURL(id)}}
	}
	text := func(value string) scenarioRequirementPart { return scenarioRequirementPart{Text: value} }
	if perspective != "" {
		opponent, win := g.AwayTeamID, clinching.HomeWin
		venue := "vs"
		if g.AwayTeamID == perspective {
			opponent, win = g.HomeTeamID, clinching.AwayWin
			venue = "at"
		}
		verb := "loses"
		switch r.Mask {
		case outcomeMask([]clinching.Outcome{win}):
			verb = "wins"
		case maskDraw:
			verb = "draws"
		case outcomeMask([]clinching.Outcome{win, clinching.Draw}):
			verb = "wins or draws"
		case maskAny:
			verb = "has any result"
		case maskNotDraw:
			verb = "does not draw"
		case maskDraw | (maskNotDraw ^ outcomeMask([]clinching.Outcome{win})):
			verb = "draws or loses"
		}
		return []scenarioRequirementPart{team(perspective), text(verb), text(venue), team(opponent)}
	}
	if slices.Contains(outcomes, clinching.HomeWin) && slices.Contains(outcomes, clinching.Draw) && len(outcomes) == 2 {
		return []scenarioRequirementPart{team(g.HomeTeamID), text("wins or draws vs"), team(g.AwayTeamID)}
	}
	if slices.Contains(outcomes, clinching.Draw) && slices.Contains(outcomes, clinching.AwayWin) && len(outcomes) == 2 {
		return []scenarioRequirementPart{team(g.AwayTeamID), text("wins or draws at"), team(g.HomeTeamID)}
	}
	if slices.Contains(outcomes, clinching.HomeWin) && slices.Contains(outcomes, clinching.AwayWin) && len(outcomes) == 2 {
		return []scenarioRequirementPart{team(g.HomeTeamID), text("does not draw vs"), team(g.AwayTeamID)}
	}
	if slices.Contains(outcomes, clinching.HomeWin) {
		return []scenarioRequirementPart{team(g.HomeTeamID), text("wins vs"), team(g.AwayTeamID)}
	}
	if slices.Contains(outcomes, clinching.AwayWin) {
		return []scenarioRequirementPart{team(g.AwayTeamID), text("wins at"), team(g.HomeTeamID)}
	}
	return []scenarioRequirementPart{team(g.HomeTeamID), text("draws vs"), team(g.AwayTeamID)}
}

func requirementKey(r scenarioRequirement) string {
	key := strconv.Quote(r.GameID) + ":" + strconv.Itoa(int(r.Mask)) + ":" + strconv.Quote(r.TeamID) + ":" + strconv.Quote(r.SecondGameID) + ":" + strconv.Itoa(r.Points)
	if r.Score != nil {
		key += ":" + scoreConditionKey(*r.Score)
	}
	return key
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
	return clinchingGroupsWithHeadingTeams(clauses, teamID, teams, teams, games)
}

// clinchingGroupsWithHeadingTeams keeps the compact, logo-accompanied labels
// in outside-help paths while using full club names in the unadorned result
// headings.
func clinchingGroupsWithHeadingTeams(clauses []scenarios.Clause, teamID string, teams, headingTeams map[string]string, games map[string]cache.Game) []clinchingGroupView {
	return clinchingGroupViews(clauses, teamID, teams, headingTeams, games, clinchingGroupOptions{SummaryLimit: maxScenarioSummaryPaths})
}

// clinchingGroupViews groups clauses by the team's own results. A group with
// more than options.SummaryLimit summary paths directs readers to its exact list.
func clinchingGroupViews(clauses []scenarios.Clause, teamID string, teams, headingTeams map[string]string, games map[string]cache.Game, options clinchingGroupOptions) []clinchingGroupView {
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
		for _, condition := range clause.ScoreConditions {
			c = append(c, scenarioRequirement{Score: &condition})
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
			g.own[i].Text = requirementText(r, teamID, headingTeams, games)
			g.own[i].Parts = requirementParts(r, teamID, teams, games)
			words = append(words, g.own[i].Text)
		}
		heading := "Regardless of " + headingTeams[teamID] + "’s results"
		if len(words) > 0 {
			heading = "If " + joinConditions(words)
		}
		summaryPaths := removeCoveredRequirements(compactFixtureMasks(uniqueConjunctions(g.help)), games)
		summaryPaths = removeCoveredRequirements(compactPoints(summaryPaths, games), games)
		help := removeCoveredRequirements(compactFixtureMasks(uniqueConjunctions(g.help)), games)
		help = disjointRequirements(help)
		help = removeCoveredRequirements(compactPoints(help, games), games)
		if len(summaryPaths) <= maxScenarioSummaryPaths {
			summaryPaths = narrowCoveredSummaryMasks(summaryPaths, games)
		}
		if len(help) <= maxScenarioSummaryPaths && summaryPathCost(help) < summaryPathCost(summaryPaths) {
			summaryPaths = help
		}
		sort.Slice(summaryPaths, func(i, j int) bool { return conjunctionKey(summaryPaths[i]) < conjunctionKey(summaryPaths[j]) })
		summaryOverflow := len(summaryPaths) > options.SummaryLimit
		paths := slices.Clone(summaryPaths)
		summary := scenarioExpression{}
		if !summaryOverflow {
			for i, c := range summaryPaths {
				for j, r := range c {
					summaryPaths[i][j].Text = requirementText(r, "", teams, games)
					summaryPaths[i][j].Parts = requirementParts(r, "", teams, games)
				}
			}
			// A single shared-condition level keeps the visible summary short;
			// deeper factoring remains available in the exact-path expression.
			summary = orderScenarioExpression(factorRequirements(summaryPaths, 2))
		}
		for i, c := range help {
			for j, r := range c {
				help[i][j].Text = requirementText(r, "", teams, games)
				help[i][j].Parts = requirementParts(r, "", teams, games)
			}
		}
		views = append(views, clinchingGroupView{Heading: heading, Own: g.own, Summary: summary, SummaryOverflow: summaryOverflow, Help: orderScenarioExpression(factorRequirements(help, 0)), paths: paths})
	}
	views = combineOwnSingleResults(combineOwnPointTotals(combineOwnPairs(views, teamID, headingTeams, games), teamID, headingTeams, games), teamID, headingTeams, games)
	for i := range views {
		views[i].Tone = ownResultTone(views[i], teamID, games)
		views[i].HeadingParts = ownHeadingParts(views[i], teamID, teams, games)
		if options.RestOfSeason && !views[i].SummaryOverflow {
			views[i].GroupedSummary = groupSeasonSummary(views[i].Summary, teamID, teams, games)
		}
	}
	return views
}

// ownResultTone names the team's own result in one match: a win, or a win or
// draw, is "win"; a draw is "draw"; a loss, or a draw or loss, is "loss".
// Groups over several matches, or with no own result, get "".
func ownResultTone(group clinchingGroupView, team string, games map[string]cache.Game) string {
	if len(group.Own) != 1 || len(group.AlternativeOwn) != 0 || group.Own[0].SecondGameID != "" {
		return ""
	}
	own := group.Own[0]
	game := games[own.GameID]
	// Mask bits follow scenarioOutcomes: home win, draw, away win.
	const homeWin, draw, awayWin uint8 = 1, 2, 4
	win, loss := homeWin, awayWin
	switch team {
	case game.HomeTeamID:
	case game.AwayTeamID:
		win, loss = awayWin, homeWin
	default:
		return ""
	}
	switch own.Mask {
	case win, win | draw:
		return "win"
	case draw:
		return "draw"
	case loss, loss | draw:
		return "loss"
	}
	return ""
}

// Merge one-match own results only when their outside help is identical.
// For example, draw and loss become one "draws or loses" heading without
// adding any outcome or implying that outside help applies to a win.
func combineOwnSingleResults(groups []clinchingGroupView, team string, teams map[string]string, games map[string]cache.Game) []clinchingGroupView {
	out := []clinchingGroupView{}
	for _, group := range groups {
		if len(group.Own) != 1 || len(group.AlternativeOwn) != 0 {
			out = append(out, group)
			continue
		}
		merged := false
		for i := range out {
			prior := &out[i]
			if len(prior.Own) != 1 || len(prior.AlternativeOwn) != 0 || prior.Own[0].GameID != group.Own[0].GameID || prior.mergeKey() != group.mergeKey() {
				continue
			}
			prior.Own[0].Mask |= group.Own[0].Mask
			prior.Own[0].Text = requirementText(prior.Own[0], team, teams, games)
			prior.Own[0].Parts = requirementParts(prior.Own[0], team, teams, games)
			if prior.Own[0].Mask == 7 {
				fixtures := noHelpFixtureText(clinching.NoHelpPath{FixtureIDs: []string{prior.Own[0].GameID}}, team, games, teams)
				prior.Heading = "Regardless of " + teams[team] + "’s result " + fixtures
			} else {
				prior.Heading = "If " + requirementText(prior.Own[0], team, teams, games)
			}
			merged = true
			break
		}
		if !merged {
			out = append(out, group)
		}
	}
	return out
}

// Prefer a disjoint wording when it removes a broad result without adding
// conditions, as in "UTA win or NC win" becoming the simpler NC win after a
// separate UTA-win path. Otherwise retain the shorter overlapping summary.
func summaryPathCost(paths [][]scenarioRequirement) int {
	cost := 0
	for _, path := range paths {
		for _, requirement := range path {
			cost += 8
			if requirement.SecondGameID != "" || requirement.Score != nil {
				cost += 2
			} else {
				cost += bits.OnesCount8(requirement.Mask) - 1
			}
		}
	}
	return cost
}

// Remove a result from a broad summary condition when an earlier path already
// covers every outcome with that result. Earlier paths stay intact, so this
// never subtracts the same outcome from two mutually covering alternatives.
func narrowCoveredSummaryMasks(paths [][]scenarioRequirement, games map[string]cache.Game) [][]scenarioRequirement {
	ordered := slices.Clone(paths)
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) != len(ordered[j]) {
			return len(ordered[i]) < len(ordered[j])
		}
		return conjunctionKey(ordered[i]) < conjunctionKey(ordered[j])
	})
	narrowed := make([][]scenarioRequirement, 0, len(ordered))
	for _, path := range ordered {
		current := slices.Clone(path)
		drop := false
		for i, requirement := range current {
			if requirement.SecondGameID != "" || bits.OnesCount8(requirement.Mask) < 2 {
				continue
			}
			mask := requirement.Mask
			for bit := uint8(1); bit <= 4; bit <<= 1 {
				if mask&bit == 0 {
					continue
				}
				current[i].Mask = bit
				for _, earlier := range narrowed {
					if requirementsImply(current, earlier, games) {
						mask &^= bit
						break
					}
				}
			}
			if mask == 0 {
				drop = true
				break
			}
			current[i].Mask = mask
		}
		if !drop {
			narrowed = append(narrowed, current)
		}
	}
	return narrowed
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
	fixtureMasks := map[string]map[uint8]bool{}
	for _, c := range clauses {
		for _, r := range c {
			if r.SecondGameID == "" && r.Score == nil {
				if fixtureMasks[r.GameID] == nil {
					fixtureMasks[r.GameID] = map[uint8]bool{}
				}
				fixtureMasks[r.GameID][r.Mask] = true
			}
		}
	}
	best := scenarioRequirement{}
	bestCount, bestPriority := 1, -1
	for _, c := range clauses {
		for _, r := range c {
			key := requirementKey(r)
			counts[key]++
			if counts[key] < 2 {
				continue
			}
			priority := 0
			if counts[key] == len(clauses) {
				priority = 2
			} else if r.SecondGameID == "" && r.Score == nil && len(fixtureMasks[r.GameID]) > 1 {
				priority = 1
			}
			if priority > bestPriority || (priority == bestPriority && counts[key] > bestCount) {
				best = r
				bestCount, bestPriority = counts[key], priority
			}
		}
	}
	// Shared requirements stay on the same visible level. Pull all of them
	// out even at the nesting limit, rather than repeating them in every path.
	if bestCount == 1 || (depth >= 3 && bestPriority != 2) {
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
	// Sibling branches do not add a visible nesting level. Let each sibling
	// group one shared result, as with KC-win and SEA-win Bay FC routes.
	other := factorRequirements(without, depth)
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
			if r.Score == nil && !slices.Contains(ids, r.GameID) {
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
		if want.Score != nil {
			if !slices.ContainsFunc(source, func(have scenarioRequirement) bool { return requirementKey(have) == requirementKey(want) }) {
				return false
			}
			continue
		}
		for x := range 3 {
			for y := range 3 {
				assignment := map[string]int{want.GameID: x}
				if want.SecondGameID != "" {
					assignment[want.SecondGameID] = y
				}
				possible := true
				for _, have := range source {
					if have.Score != nil {
						continue
					}
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
	sort.SliceStable(e.Alternatives, func(i, j int) bool {
		left, right := e.Alternatives[i], e.Alternatives[j]
		if expressionSize(left) != expressionSize(right) {
			return expressionSize(left) < expressionSize(right)
		}
		return expressionDisplayKey(left) < expressionDisplayKey(right)
	})
	return e
}

// Sort equal-sized paths by the conditions people read, rather than opaque
// fixture IDs, so repeated results appear beside each other.
func expressionDisplayKey(e scenarioExpression) string {
	labels := make([]string, 0, len(e.Conditions))
	for _, condition := range e.Conditions {
		label := condition.Text
		if label == "" {
			label = requirementKey(condition)
		}
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return strings.Join(labels, "\x00")
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
				if ownPoints(prior.Own[0], team, games) != results[1] || ownPoints(prior.Own[1], team, games) != results[0] || prior.mergeKey() != group.mergeKey() {
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

// combineOwnPointTotals merges two-match result groups with identical
// outside help when together they are exactly "at least p points from the two
// matches", as in five "✓ Clinches" rows for every result but two losses.
// Every ordering of each points total must be present, so the merge adds no
// outcome.
func combineOwnPointTotals(groups []clinchingGroupView, team string, teams map[string]string, games map[string]cache.Game) []clinchingGroupView {
	pair := func(group clinchingGroupView) (string, string, bool) {
		if len(group.Own) != 2 || !singleResult(group.Own[0].Mask) || !singleResult(group.Own[1].Mask) {
			return "", "", false
		}
		return group.Own[0].GameID, group.Own[1].GameID, true
	}
	out := []clinchingGroupView{}
	used := make([]bool, len(groups))
	for i, group := range groups {
		if used[i] {
			continue
		}
		first, second, ok := pair(group)
		if !ok {
			out = append(out, group)
			continue
		}
		members := []int{i}
		for j := i + 1; j < len(groups); j++ {
			a, b, ok := pair(groups[j])
			if ok && !used[j] && a == first && b == second && groups[j].mergeKey() == group.mergeKey() {
				members = append(members, j)
			}
		}
		covered := map[[2]int]bool{}
		for _, index := range members {
			for _, own := range [][]scenarioRequirement{groups[index].Own, groups[index].AlternativeOwn} {
				if len(own) == 2 {
					covered[[2]int{ownPoints(own[0], team, games), ownPoints(own[1], team, games)}] = true
				}
			}
		}
		minimum := 6
		for results := range covered {
			minimum = min(minimum, results[0]+results[1])
		}
		exact := len(members) > 1
		for _, x := range []int{0, 1, 3} {
			for _, y := range []int{0, 1, 3} {
				if (x+y >= minimum) != covered[[2]int{x, y}] {
					exact = false
				}
			}
		}
		if !exact {
			out = append(out, group)
			continue
		}
		for _, index := range members {
			used[index] = true
		}
		group.AlternativeOwn = nil
		if minimum == 0 {
			group.UnrestrictedOwn = []string{first, second}
			group.Own = nil
			group.Heading = "Regardless of " + teams[team] + "’s results (" + noHelpFixtureText(clinching.NoHelpPath{FixtureIDs: []string{first, second}}, team, games, teams) + ")"
		} else {
			total := scenarioRequirement{GameID: first, SecondGameID: second, TeamID: team, Points: minimum}
			total.Text = requirementText(total, team, teams, games)
			total.Parts = requirementParts(total, team, teams, games)
			group.Own = []scenarioRequirement{total}
			group.Heading = "If " + total.Text
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
