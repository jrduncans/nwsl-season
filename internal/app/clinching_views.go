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
	for _, id := range path.FixtureIDs {
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

// scoreConditionText writes a score condition out in full, for example
// "POR gains 11+ goal difference on LA (LA +10 now), or exactly 10 and scores
// at least as many goals (level at 48 now)". Gains count only remaining matches.
func scoreConditionText(c scenarios.ScoreCondition, teams map[string]string) string {
	lead, tail := scoreConditionPhrases(c, teams)
	return teams[c.TeamID] + " " + lead + " " + teams[c.RivalID] + tail
}

func scoreConditionParts(c scenarios.ScoreCondition, teams map[string]string) []scenarioRequirementPart {
	lead, tail := scoreConditionPhrases(c, teams)
	return []scenarioRequirementPart{
		{Team: &scenarioTeamToken{Code: teams[c.TeamID], LogoURL: clubLogoURL(c.TeamID)}},
		{Text: lead},
		{Team: &scenarioTeamToken{Code: teams[c.RivalID], LogoURL: clubLogoURL(c.RivalID)}},
		{Text: strings.TrimPrefix(tail, " ")},
	}
}

// scoreConditionPhrases returns the words before the rival's name and those
// after it. Leading levels that leave the two level read "finishes level
// with … on goal difference and goals scored"; other levels compare gains
// in goal difference or goals scored ("scores at least 7 more goals"). A later
// level applies when the one before it is exactly one short of its minimum.
func scoreConditionPhrases(c scenarios.ScoreCondition, teams map[string]string) (string, string) {
	if len(c.Levels) == 0 {
		return "finishes ahead of", ""
	}
	team, rival := teams[c.TeamID], teams[c.RivalID]
	level := 0
	for level < len(c.Levels) && c.Levels[level].ExactOnly && finishesLevel(c.Levels[level]) {
		level++
	}
	var lead, tail string
	next := level
	if level > 0 {
		names, gaps := []string{}, []string{}
		for _, l := range c.Levels[:level] {
			names = append(names, measureNoun(l.Measure))
			gaps = append(gaps, scoreGap(l, team, rival, level > 1))
		}
		lead = "finishes level with"
		tail = " on " + strings.Join(names, " and ") + " (" + strings.Join(gaps, ", ") + ")"
		if level < len(c.Levels) {
			tail += ", and " + gainPhrase(c.Levels[level], false) + " (" + scoreGap(c.Levels[level], team, rival, false) + ")"
			next = level + 1
		}
	} else {
		lead = gainPhrase(c.Levels[0], true)
		tail = " (" + scoreGap(c.Levels[0], team, rival, false) + ")"
		next = 1
	}
	for i := next; i < len(c.Levels); i++ {
		previous, l := c.Levels[i-1], c.Levels[i]
		joiner := ", or " + exactlyPhrase(previous) + " and "
		if previous.ExactOnly {
			joiner = " and "
		}
		tail += joiner + gainPhrase(l, false) + " (" + scoreGap(l, team, rival, false) + ")"
	}
	return lead, tail
}

// finishesLevel reports whether an exact gain leaves the two level.
func finishesLevel(level scenarios.ScoreLevel) bool {
	return level.MinimumGain-1 == level.RivalNow-level.TeamNow
}

func measureNoun(measure scenarios.ScoreMeasure) string {
	switch measure {
	case scenarios.MeasureGoalsScored:
		return "goals scored"
	case scenarios.MeasureHeadToHeadGoals:
		return "head-to-head goals"
	}
	return "goal difference"
}

// gainPhrase words one level as a gain, ending in "on" or "to" when the
// rival's name follows.
func gainPhrase(level scenarios.ScoreLevel, rivalFollows bool) string {
	if level.Measure != scenarios.MeasureGoalDifference {
		return goalsPhrase(level, rivalFollows)
	}
	noun := measureNoun(level.Measure)
	on, to := "", ""
	if rivalFollows {
		on, to = " on", " to"
	}
	if level.ExactOnly {
		switch exact := level.MinimumGain - 1; {
		case exact > 0:
			return fmt.Sprintf("gains exactly %d %s%s", exact, noun, on)
		case exact < 0:
			return fmt.Sprintf("loses exactly %d %s%s", -exact, noun, to)
		}
		return "keeps the same " + noun + " gap" + to
	}
	switch n := level.MinimumGain; {
	case n > 0:
		return fmt.Sprintf("gains %d+ %s%s", n, noun, on)
	case n < 0:
		return fmt.Sprintf("loses at most %d %s%s", -n, noun, to)
	}
	return "loses no " + noun + to
}

func goalsPhrase(level scenarios.ScoreLevel, rivalFollows bool) string {
	noun := "goals"
	n := level.MinimumGain
	quantity := n
	if level.ExactOnly {
		quantity--
	}
	if quantity == 1 || quantity == -1 {
		noun = "goal"
	}
	if level.Measure == scenarios.MeasureHeadToHeadGoals {
		noun = "head-to-head " + noun
	}
	comparison := " than"
	var phrase string
	if level.ExactOnly {
		switch exact := n - 1; {
		case exact > 0:
			phrase = fmt.Sprintf("scores exactly %d more %s", exact, noun)
		case exact < 0:
			phrase = fmt.Sprintf("scores exactly %d fewer %s", -exact, noun)
		default:
			phrase, comparison = "scores as many "+noun, " as"
		}
	} else {
		switch {
		case n > 0:
			phrase = fmt.Sprintf("scores at least %d more %s", n, noun)
		case n < 0:
			phrase = fmt.Sprintf("scores no more than %d fewer %s", -n, noun)
		default:
			phrase, comparison = "scores at least as many "+noun, " as"
		}
	}
	if rivalFollows {
		phrase += comparison
	}
	return phrase
}

// exactlyPhrase names the one gain that passes to the next tiebreaker.
func exactlyPhrase(level scenarios.ScoreLevel) string {
	if level.Measure != scenarios.MeasureGoalDifference {
		level.ExactOnly = true
		return goalsPhrase(level, false)
	}
	switch exact := level.MinimumGain - 1; {
	case exact > 0:
		return fmt.Sprintf("exactly %d", exact)
	case exact < 0:
		return fmt.Sprintf("loses exactly %d", -exact)
	}
	return "none"
}

// scoreGap describes the current difference, as "level at +7 now" or
// "LA +10 now" for goal difference and "KC +6 now" or "level at 48 now" for
// goals. named adds the measure, for a list of several.
func scoreGap(level scenarios.ScoreLevel, team, rival string, named bool) string {
	gap := level.TeamNow - level.RivalNow
	value := fmt.Sprint(level.TeamNow)
	if level.Measure == scenarios.MeasureGoalDifference {
		value = signedNumber(level.TeamNow)
	}
	suffix := ""
	if named && level.Measure != scenarios.MeasureGoalDifference {
		suffix = " " + measureNoun(level.Measure)
	}
	if gap == 0 {
		return "level at " + value + suffix + " now"
	}
	leader := team
	if gap < 0 {
		leader, gap = rival, -gap
	}
	return fmt.Sprintf("%s +%d%s now", leader, gap, suffix)
}

func signedNumber(v int) string {
	if v > 0 {
		return fmt.Sprintf("+%d", v)
	}
	return fmt.Sprint(v)
}
