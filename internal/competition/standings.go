package competition

import (
	"fmt"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// OfficialStandings includes source-backed awards and deductions without
// changing cached fixtures. Awards reconcile against the observed full-season
// game count, as in historical outcome derivation; already counted awards are
// skipped. Inconsistent counts are reported rather than guessed.
func OfficialStandings(season string, teams []standings.Team, games []standings.Game, order standings.Order) ([]standings.TableRow, error) {
	rules := standings.RulesForSeason(season, order)
	override := HistoricalOutcomeOverrides()[season]
	games = append([]standings.Game(nil), games...)
	rows := standings.Calculate(teams, games, rules)
	played := map[string]int{}
	expected := 0
	for _, row := range rows {
		played[row.Team.ID] = row.Record.Played
		expected = max(expected, row.Record.Played)
	}
	for _, award := range override.Awards {
		winner, winOK := played[award.Winner]
		loser, loseOK := played[award.Loser]
		if !winOK || !loseOK {
			return nil, fmt.Errorf("awarded-match participants are missing")
		}
		if winner == expected {
			continue
		}
		if winner >= expected || loser >= expected {
			return nil, fmt.Errorf("awarded-match game counts do not reconcile")
		}
		home, away := award.Winner, award.Loser
		homeScore, awayScore := award.WinnerGoals, award.LoserGoals
		if award.Home == award.Loser {
			home, away = away, home
			homeScore, awayScore = awayScore, homeScore
		}
		games = append(games, standings.Game{Status: standings.CompletedStatus, HomeTeamID: home, AwayTeamID: away, HomeScore: &homeScore, AwayScore: &awayScore})
		played[award.Winner]++
		played[award.Loser]++
	}
	if len(override.Awards) > 0 {
		for _, count := range played {
			if count != expected || count == 0 {
				return nil, fmt.Errorf("game counts do not reconcile after awarded matches")
			}
		}
	}
	rows = standings.Calculate(teams, games, rules)
	for _, deduction := range override.Deductions {
		found := false
		for i := range rows {
			if rows[i].Team.ID == deduction.Team {
				rows[i].Record.Points -= deduction.Points
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("point-deduction participant is missing")
		}
	}
	return standings.Rank(rows, games, rules), nil
}
