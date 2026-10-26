package app

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

// A rendered requirement costs one unit, each score cascade level another,
// and each nested alternative adds a unit. This bounds the actual tree, not
// the number of raw clauses (which may factor into a short summary).
const oversizedConditionLimit = 30

func oversizedExpression(e scenarioExpression) int {
	n := len(e.Conditions) + len(e.Alternatives)
	for _, r := range e.Conditions {
		if r.Score != nil {
			n += len(r.Score.Levels)
		}
	}
	for _, child := range e.Alternatives {
		n += oversizedExpression(child)
	}
	return n
}
func oversizedRival(r clinchingRivalView) int {
	n := 1 + len(r.Ties)
	for _, t := range r.Ties {
		if t.Condition != nil && t.Condition.Score != nil {
			n += len(t.Condition.Score.Levels)
		}
	}
	return n
}
func oversizedGroup(g clinchingGroupView) bool {
	n := 0
	switch {
	case g.RivalState != "":
		for _, r := range g.Rivals {
			n += oversizedRival(r)
		}
	case g.GroupedSummary != nil:
		for _, part := range g.GroupedSummary.Groups {
			n++
			if part.Rival != nil {
				n += oversizedRival(*part.Rival)
			} else {
				n += oversizedExpression(part.Expression)
			}
		}
	case g.SummaryOverflow:
		n = oversizedExpression(g.Help)
	default:
		n = oversizedExpression(g.Summary)
	}
	return n > oversizedConditionLimit
}

type clinchingDetailsView struct {
	Team, Achievement, Limitation string
	Disciplinary                  bool
	Group                         clinchingGroupView
	Cards                         *seedingAlternativesView
}

// Expand only the team's (at most two) result combinations. Pinning them to
// each cached clause makes each displayed scenario independently sufficient.
func clausesForGroup(clauses []scenarios.Clause, group clinchingGroupView, team string, games map[string]cache.Game) []scenarios.Clause {
	ids := []string{}
	for _, r := range group.Own {
		ids = append(ids, r.GameID)
		if r.SecondGameID != "" {
			ids = append(ids, r.SecondGameID)
		}
	}
	if len(ids) == 0 {
		ids = append(ids, group.UnrestrictedOwn...)
	}
	if len(ids) == 0 {
		return clauses
	}
	matches := func(own []scenarioRequirement, values []uint8) bool {
		if len(own) == 0 {
			return true
		}
		for _, r := range own {
			idx := -1
			for i, id := range ids {
				if id == r.GameID {
					idx = i
					break
				}
			}
			if idx < 0 {
				return false
			}
			if r.SecondGameID != "" {
				j := -1
				for i, id := range ids {
					if id == r.SecondGameID {
						j = i
						break
					}
				}
				if j < 0 || ownPoints(scenarioRequirement{GameID: r.GameID, Mask: values[idx]}, team, games)+ownPoints(scenarioRequirement{GameID: r.SecondGameID, Mask: values[j]}, team, games) < r.Points {
					return false
				}
			} else if r.Mask&values[idx] == 0 {
				return false
			}
		}
		return true
	}
	var out []scenarios.Clause
	var visit func([]uint8)
	visit = func(values []uint8) {
		if len(values) < len(ids) {
			for _, mask := range []uint8{1, 2, 4} {
				visit(append(values, mask))
			}
			return
		}
		if !matches(group.Own, values) && (len(group.AlternativeOwn) == 0 || !matches(group.AlternativeOwn, values)) {
			return
		}
		own := make([]scenarios.FixtureCondition, len(ids))
		for i, id := range ids {
			var outcome clinching.Outcome
			for j := range scenarioOutcomes {
				if values[i] == 1<<j {
					outcome = scenarioOutcomes[j]
				}
			}
			own[i] = scenarios.FixtureCondition{GameID: id, AllowedOutcomes: []clinching.Outcome{outcome}}
		}
		out = append(out, clausesForOwn(clauses, own)...)
	}
	visit(nil)
	return out
}

func clinchingScenarioURL(query url.Values, team, achievement string, group, alternative int) string {
	q := cloneQuery(query)
	q.Set("view", clinchingViewRestOfSeason)
	q.Set("scenario_team", team)
	q.Set("achievement", achievement)
	q.Set("group", strconv.Itoa(group))
	q.Set("alternative", strconv.Itoa(alternative))
	return "?" + q.Encode() + "#clinching-scenarios"
}
func parseGroup(q url.Values, count int) (int, error) {
	n, err := strconv.Atoi(q.Get("group"))
	if err != nil || n < 1 || n > count {
		return 0, fmt.Errorf("invalid scenario group")
	}
	return n - 1, nil
}
