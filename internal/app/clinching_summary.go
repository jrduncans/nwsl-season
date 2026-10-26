package app

import (
	"slices"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

// A grouped summary separates independent requirements: every group must
// hold, but the reader can choose an alternative within each group separately.
type clinchingGroupedSummary struct {
	Groups []clinchingSummaryGroup
}

type clinchingSummaryGroup struct {
	Heading      string
	HeadingParts []scenarioRequirementPart
	Expression   scenarioExpression
	Rival        *clinchingRivalView
}

func groupSeasonSummary(summary scenarioExpression, team string, teams map[string]string, games map[string]cache.Game) *clinchingGroupedSummary {
	if len(summary.Alternatives) < 2 {
		return nil
	}
	paths := scenarioExpression{Alternatives: summary.Alternatives}.Combinations()
	if len(paths) > maxSeasonSummaryPaths {
		return nil
	}
	factors := independentRivalGroups(paths, team, games)
	if len(factors) < 2 {
		return nil
	}
	view := &clinchingGroupedSummary{Groups: commonSummaryGroups(summary.Conditions, team, teams, games)}
	for _, paths := range factors {
		group := clinchingSummaryGroup{Expression: orderScenarioExpression(factorRequirements(paths, 2))}
		rivals := summaryRivals(paths, team, games)
		labels := []string{}
		for _, rival := range rivals {
			labels = append(labels, teams[rival])
			if need := summaryRivalNeed(paths, team, rival, games); need != nil {
				rivalView := rivalView(*need, teams, games)
				group.Rival = &rivalView
				break
			}
		}
		group.Heading = joinConditions(labels)
		for i, id := range rivals {
			if i > 0 {
				group.HeadingParts = append(group.HeadingParts, scenarioRequirementPart{Text: "and"})
			}
			group.HeadingParts = append(group.HeadingParts, teamPart(id, teams))
		}
		view.Groups = append(view.Groups, group)
	}
	return view
}

// Common results and their score conditions also read more naturally together:
// "KC loses both matches, and UTA gains ..." rather than separate match pills.
func commonSummaryGroups(conditions []scenarioRequirement, team string, teams map[string]string, games map[string]cache.Game) []clinchingSummaryGroup {
	groups := []clinchingSummaryGroup{}
	remaining := conditions
	for len(remaining) > 0 {
		found := false
		for _, rival := range summaryRivals([][]scenarioRequirement{remaining}, team, games) {
			selected, rest := []scenarioRequirement{}, []scenarioRequirement{}
			for _, r := range remaining {
				if summaryRequirementInvolves(r, rival, games) {
					selected = append(selected, r)
				} else {
					rest = append(rest, r)
				}
			}
			if need := summaryRivalNeed([][]scenarioRequirement{selected}, team, rival, games); need != nil {
				view := rivalView(*need, teams, games)
				groups = append(groups, clinchingSummaryGroup{Expression: scenarioExpression{Conditions: selected}, Rival: &view})
				remaining, found = rest, true
				break
			}
		}
		if !found {
			groups = append(groups, clinchingSummaryGroup{Expression: scenarioExpression{Conditions: remaining}})
			break
		}
	}
	return groups
}

// Split only exact Cartesian products of the stored conjunctions. Projection
// gives each original path one distinct (left, right) pair. If the counts
// multiply back to the original count, every combination is present, so
// requiring both groups preserves precisely the original disjunction.
// Score conditions remain opaque predicates; this makes no pairwise tiebreak
// assumption and keeps dependent multiway conditions together.
func independentRivalGroups(paths [][]scenarioRequirement, team string, games map[string]cache.Game) [][][]scenarioRequirement {
	paths = uniqueConjunctions(paths)
	for _, rival := range summaryRivals(paths, team, games) {
		left, right := [][]scenarioRequirement{}, [][]scenarioRequirement{}
		for _, path := range paths {
			a, b := []scenarioRequirement{}, []scenarioRequirement{}
			for _, r := range path {
				if summaryRequirementInvolves(r, rival, games) {
					a = append(a, r)
				} else {
					b = append(b, r)
				}
			}
			left, right = append(left, a), append(right, b)
		}
		left, right = uniqueConjunctions(left), uniqueConjunctions(right)
		if len(left) <= 1 || len(right) <= 1 || len(left)*len(right) != len(paths) {
			continue
		}
		return append(independentRivalGroups(left, team, games), independentRivalGroups(right, team, games)...)
	}
	return [][][]scenarioRequirement{paths}
}

// Score conditions identify the rivals the summary is about. Without them,
// try the clubs appearing most often in the result requirements first.
func summaryRivals(paths [][]scenarioRequirement, team string, games map[string]cache.Game) []string {
	counts := map[string]int{}
	scored := map[string]bool{}
	for _, path := range paths {
		for _, r := range path {
			if r.Score != nil {
				for _, id := range []string{r.Score.TeamID, r.Score.RivalID} {
					if id != team {
						scored[id] = true
					}
				}
				continue
			}
			for _, id := range []string{r.GameID, r.SecondGameID} {
				g := games[id]
				counts[g.HomeTeamID]++
				counts[g.AwayTeamID]++
			}
		}
	}
	ids := []string{}
	for id := range counts {
		if id != "" && id != team && (len(scored) == 0 || scored[id]) {
			ids = append(ids, id)
		}
	}
	for id := range scored {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if counts[ids[i]] != counts[ids[j]] {
			return counts[ids[i]] > counts[ids[j]]
		}
		return ids[i] < ids[j]
	})
	return ids
}

func summaryRequirementInvolves(r scenarioRequirement, rival string, games map[string]cache.Game) bool {
	if r.Score != nil {
		return r.Score.TeamID == rival || r.Score.RivalID == rival
	}
	for _, id := range []string{r.GameID, r.SecondGameID} {
		g := games[id]
		if g.HomeTeamID == rival || g.AwayTeamID == rival {
			return true
		}
	}
	return false
}

// Restate an independent one-rival group as a points limit and optional score
// condition. Check every result of its one or two matches; a points total is
// used only if every ordering at that total has exactly the same requirements.
func summaryRivalNeed(paths [][]scenarioRequirement, team, rival string, games map[string]cache.Game) *scenarios.RivalNeed {
	ids := []string{}
	for _, path := range paths {
		for _, r := range path {
			if r.Score != nil {
				if r.Score.TeamID != team || r.Score.RivalID != rival {
					return nil
				}
				continue
			}
			for _, id := range []string{r.GameID, r.SecondGameID} {
				if id == "" {
					continue
				}
				g := games[id]
				if g.HomeTeamID != rival && g.AwayTeamID != rival {
					return nil
				}
				if !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
		}
	}
	if len(ids) == 0 || len(ids) > 2 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool {
		if games[ids[i]].KickoffUTC != games[ids[j]].KickoffUTC {
			return games[ids[i]].KickoffUTC < games[ids[j]].KickoffUTC
		}
		return ids[i] < ids[j]
	})
	type record struct {
		key       string // empty: not covered; "*": covered regardless of scores
		wins      int
		condition *scenarios.ScoreCondition
	}
	records := map[int]record{}
	for x := range 3 {
		for y := range 3 {
			assignment := map[string]int{ids[0]: x}
			points := scenarioPoints(x, rival, games[ids[0]])
			wins := 0
			if points == 3 {
				wins++
			}
			if len(ids) == 2 {
				assignment[ids[1]] = y
				p := scenarioPoints(y, rival, games[ids[1]])
				points += p
				if p == 3 {
					wins++
				}
			}
			keys := map[string]*scenarios.ScoreCondition{}
			for _, path := range paths {
				matches, key := true, "*"
				var score *scenarios.ScoreCondition
				for _, r := range path {
					if r.Score != nil {
						if score != nil {
							return nil
						}
						score, key = r.Score, scoreConditionKey(*r.Score)
					} else {
						matches = matches && requirementMatches(r, assignment, games)
					}
				}
				if matches {
					keys[key] = score
				}
			}
			current := record{wins: wins}
			if _, ok := keys["*"]; ok {
				current.key = "*"
			} else {
				if len(keys) > 1 {
					return nil
				}
				for key, condition := range keys {
					current.key, current.condition = key, condition
				}
			}
			if previous, ok := records[points]; ok && previous.key != current.key {
				return nil
			}
			records[points] = current
		}
	}
	need := &scenarios.RivalNeed{RivalID: rival, GameIDs: ids, MaxPoints: -1}
	for points, record := range records {
		if record.key == "*" {
			need.MaxPoints = max(need.MaxPoints, points)
		} else if record.condition != nil {
			need.Ties = append(need.Ties, scenarios.RivalTie{Points: points, Wins: record.wins, Condition: record.condition})
		}
	}
	if len(need.Ties) > 1 || (len(need.Ties) == 0 && need.MaxPoints < 0) || need.MaxPoints == 3*len(ids) {
		return nil
	}
	if len(need.Ties) == 1 {
		need.MaxPoints = need.Ties[0].Points - 1
	}
	for points, record := range records {
		if (points <= need.MaxPoints) != (record.key == "*") {
			return nil
		}
	}
	return need
}
