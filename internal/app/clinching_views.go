package app

import (
	"fmt"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func joinConditions(v []string) string {
	if len(v) == 0 {
		return ""
	}
	if len(v) == 1 {
		return v[0]
	}
	if len(v) == 2 {
		return v[0] + " and " + v[1]
	}
	return strings.Join(v[:len(v)-1], ", ") + ", and " + v[len(v)-1]
}
func conditionText(c scenarios.FixtureCondition, teams map[string]string, games map[string]cache.Game) string {
	g := games[c.GameID]
	home, away := teams[g.HomeTeamID], teams[g.AwayTeamID]
	os := map[clinching.Outcome]bool{}
	for _, o := range c.AllowedOutcomes {
		os[o] = true
	}
	if os[clinching.HomeWin] && os[clinching.Draw] && len(os) == 2 {
		return home + " wins or draws vs " + away
	}
	if os[clinching.Draw] && os[clinching.AwayWin] && len(os) == 2 {
		return away + " wins or draws at " + home
	}
	if os[clinching.HomeWin] && os[clinching.AwayWin] && len(os) == 2 {
		return home + " does not draw vs " + away
	}
	if os[clinching.HomeWin] {
		return home + " wins vs " + away
	}
	if os[clinching.AwayWin] {
		return away + " wins at " + home
	}
	return home + " draws vs " + away
}

func noHelpText(path clinching.NoHelpPath, team, achievement string) string {
	switch path.State {
	case clinching.NoHelpGuaranteed:
		wins := len(path.FixtureIDs)
		if wins == 0 {
			return ""
		}
		if wins == 1 {
			return fmt.Sprintf("%s can clinch %s with a win.", team, achievement)
		}
		return fmt.Sprintf("%s can clinch %s with %d wins.", team, achievement, wins)
	case clinching.NoHelpImpossible:
		return "Requires results elsewhere."
	case clinching.NoHelpUnresolved:
		return ""
	default:
		return ""
	}
}

func noHelpFixtureText(path clinching.NoHelpPath, teamID string, games map[string]cache.Game, teams map[string]string) string {
	fixtures := make([]string, 0, len(path.FixtureIDs))
	for _, id := range fixtureIDsInKickoffOrder(path.FixtureIDs, games) {
		game, ok := games[id]
		if !ok {
			continue
		}
		if game.HomeTeamID == teamID {
			fixtures = append(fixtures, "vs "+teams[game.AwayTeamID])
		} else if game.AwayTeamID == teamID {
			fixtures = append(fixtures, "at "+teams[game.HomeTeamID])
		}
	}
	return joinConditions(fixtures)
}

// scoreConditionKey identifies a score condition for de-duplication and
// factoring.
func scoreConditionKey(c scenarios.ScoreCondition) string {
	parts := []string{c.TeamID, c.RivalID}
	for _, level := range c.Levels {
		parts = append(parts, fmt.Sprintf("%s=%d/%t", level.Measure, level.MinimumGain, level.ExactOnly))
	}
	return strings.Join(parts, "|")
}

// Score wording compares final totals. The threshold is translated algebraically,
// including nonstandard thresholds retained by the general fallback.
func scoreConditionText(c scenarios.ScoreCondition, teams map[string]string) string {
	return partsText(scoreConditionParts(c, teams))
}

func partsText(parts []scenarioRequirementPart) string {
	var out strings.Builder
	for i, p := range parts {
		if i > 0 && !p.NoSpaceBefore {
			out.WriteByte(' ')
		}
		if p.Team != nil {
			out.WriteString(p.Team.Code)
		} else {
			out.WriteString(p.Text)
		}
	}
	return out.String()
}

func teamPart(id string, teams map[string]string) scenarioRequirementPart {
	return scenarioRequirementPart{Team: &scenarioTeamToken{Code: teams[id], LogoURL: clubLogoURL(id)}}
}

// Enumerate accepting cascade branches, keeping every preceding equality in
// that branch. ExactOnly skips the strict branch; it never drops the equality.
func scoreConditionParts(c scenarios.ScoreCondition, teams map[string]string) []scenarioRequirementPart {
	if len(c.Levels) == 2 {
		gd, goals := c.Levels[0], c.Levels[1]
		if gd.Measure == scenarios.MeasureGoalDifference && !gd.ExactOnly && levelDelta(gd) == 1 && goals.Measure == scenarios.MeasureGoalsScored && !goals.ExactOnly && (levelDelta(goals) == 0 || levelDelta(goals) == 1) {
			need := "must have at least as many total goals scored as"
			if levelDelta(goals) == 1 {
				need = "must have more total goals scored than"
			}
			return []scenarioRequirementPart{teamPart(c.TeamID, teams), {Text: "’s final goal difference must exceed", NoSpaceBefore: true}, teamPart(c.RivalID, teams), {Text: "’s; if equal,", NoSpaceBefore: true}, teamPart(c.TeamID, teams), {Text: need}, teamPart(c.RivalID, teams)}
		}
	}
	if len(c.Levels) > 1 && allLevel(c.Levels) {
		measures := []string{}
		for _, l := range c.Levels {
			measures = append(measures, measureNoun(l.Measure))
		}
		return []scenarioRequirementPart{teamPart(c.TeamID, teams), {Text: "must finish level with"}, teamPart(c.RivalID, teams), {Text: "on " + joinConditions(measures)}}
	}
	parts := []scenarioRequirementPart{}
	for i, level := range c.Levels {
		if level.ExactOnly && i < len(c.Levels)-1 {
			continue
		}
		hadBranch := len(parts) > 0
		if hadBranch {
			parts = append(parts, scenarioRequirementPart{Text: "or,", BreakBefore: true})
		}
		if i > 0 && hadBranch {
			previous := []string{}
			for _, l := range c.Levels[:i] {
				previous = append(previous, equalityDescription(l))
			}
			parts = append(parts, scenarioRequirementPart{Text: "if " + joinConditions(previous) + ","})
		}
		if !hadBranch && i > 0 && allLevel(c.Levels[:i]) {
			measures := []string{}
			for _, l := range c.Levels[:i] {
				measures = append(measures, measureNoun(l.Measure))
			}
			parts = append(parts, teamPart(c.TeamID, teams), scenarioRequirementPart{Text: "must finish level with"}, teamPart(c.RivalID, teams), scenarioRequirementPart{Text: "on " + joinConditions(measures) + map[bool]string{true: ", and", false: " and"}[len(measures) > 1]})
		} else if !hadBranch {
			for _, previous := range c.Levels[:i] {
				previous.ExactOnly = true
				parts = append(parts, comparisonParts(c, previous, teams)...)
				parts = append(parts, scenarioRequirementPart{Text: "and"})
			}
		}
		parts = append(parts, comparisonParts(c, level, teams)...)
	}
	return parts
}

// comparisonParts words one cascade level. Goal difference that only has to
// match or beat the rival's reads as a possessive comparison; the rest use
// finalComparison.
// allLevel reports whether every level only has to finish exactly level.
func allLevel(levels []scenarios.ScoreLevel) bool {
	for _, l := range levels {
		if !l.ExactOnly || levelDelta(l) != 1 {
			return false
		}
	}
	return true
}

func levelDelta(l scenarios.ScoreLevel) int { return l.TeamNow - l.RivalNow + l.MinimumGain }

func comparisonParts(c scenarios.ScoreCondition, l scenarios.ScoreLevel, teams map[string]string) []scenarioRequirementPart {
	if l.Measure == scenarios.MeasureGoalDifference && !l.ExactOnly {
		verb := map[int]string{0: "must equal or exceed", 1: "must exceed"}[levelDelta(l)]
		if verb != "" {
			return []scenarioRequirementPart{teamPart(c.TeamID, teams), {Text: "’s final goal difference " + verb, NoSpaceBefore: true}, teamPart(c.RivalID, teams), {Text: "’s", NoSpaceBefore: true}}
		}
	}
	return []scenarioRequirementPart{teamPart(c.TeamID, teams), {Text: finalComparison(l)}, teamPart(c.RivalID, teams)}
}

func measureNoun(measure scenarios.ScoreMeasure) string {
	switch measure {
	case scenarios.MeasureGoalsScored:
		return "goals scored"
	case scenarios.MeasureHeadToHeadGoals:
		return "head-to-head goals"
	default:
		return "goal difference"
	}
}

func equalityDescription(l scenarios.ScoreLevel) string {
	delta := l.TeamNow - l.RivalNow + l.MinimumGain - 1
	if delta == 0 {
		return "level on " + measureNoun(l.Measure)
	}
	return fmt.Sprintf("the final %s difference is %d", measureNoun(l.Measure), delta)
}

func finalComparison(l scenarios.ScoreLevel) string {
	noun := measureNoun(l.Measure)
	if l.Measure == scenarios.MeasureGoalsScored {
		noun = "goals"
	}
	delta := l.TeamNow - l.RivalNow + l.MinimumGain
	if l.ExactOnly {
		delta--
		if delta == 0 {
			return "must finish level on " + measureNoun(l.Measure) + " with"
		}
		if l.Measure == scenarios.MeasureGoalDifference {
			return fmt.Sprintf("must finish with a goal difference exactly %d %s than", absInt(delta), map[bool]string{true: "higher", false: "lower"}[delta > 0])
		}
		return fmt.Sprintf("must finish with exactly %d %s %s", absInt(delta), map[bool]string{true: "more", false: "fewer"}[delta > 0], noun) + " than"
	}
	if l.Measure == scenarios.MeasureGoalDifference {
		switch delta {
		case 0:
			return "must finish with at least the goal difference of"
		case 1:
			return "must finish with a higher goal difference than"
		default:
			if delta > 0 {
				return fmt.Sprintf("must finish with a goal difference at least %d higher than", delta)
			}
			return fmt.Sprintf("must finish with a goal difference no more than %d lower than", -delta)
		}
	}
	switch {
	case delta == 0:
		return "must finish with at least as many " + noun + " as"
	case delta == 1:
		return "must finish with more " + noun + " than"
	case delta > 0:
		return fmt.Sprintf("must finish with at least %d more %s than", delta, noun)
	default:
		return fmt.Sprintf("must finish with no more than %d fewer %s than", -delta, noun)
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
