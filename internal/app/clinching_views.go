package app

import (
	"fmt"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func clauseSentence(c scenarios.Clause, teams map[string]string, games map[string]cache.Game) string {
	parts := []string{}
	for _, v := range c.Conditions {
		parts = append(parts, conditionText(v, teams, games))
	}
	if len(parts) == 0 {
		return "With any results in the included slate."
	}
	return "With " + joinConditions(parts) + "."
}
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
		return home + " does not lose to " + away
	}
	if os[clinching.Draw] && os[clinching.AwayWin] && len(os) == 2 {
		return home + " does not win against " + away
	}
	if os[clinching.HomeWin] {
		return home + " beats " + away
	}
	if os[clinching.AwayWin] {
		return away + " beats " + home
	}
	return home + " draws with " + away
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
