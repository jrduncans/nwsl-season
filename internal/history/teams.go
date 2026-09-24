package history

import (
	"sort"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

// TeamScoring describes recorded results within one regular season. Each
// expected total needs complete coverage of its own metric for that team's
// valid played matches.
type TeamScoring struct {
	TeamID                    string
	Played, Points            int
	XGCovered, XPointsCovered int
	GoalsFor, GoalsAgainst    int64
	XGFor, XGAgainst, XPoints *float64
}

type teamScoringAccumulator struct {
	TeamScoring
	xgFor, xgAgainst float64
	xPoints          float64
}

type teamScoringTotals map[string]*teamScoringAccumulator

func (teams teamScoringTotals) addResult(game cache.Game) {
	for _, id := range []string{game.HomeTeamID, game.AwayTeamID} {
		if teams[id] == nil {
			teams[id] = &teamScoringAccumulator{TeamScoring: TeamScoring{TeamID: id}}
		}
		teams[id].Played++
	}
	teams[game.HomeTeamID].GoalsFor += game.HomeScore.Int64
	teams[game.HomeTeamID].GoalsAgainst += game.AwayScore.Int64
	teams[game.AwayTeamID].GoalsFor += game.AwayScore.Int64
	teams[game.AwayTeamID].GoalsAgainst += game.HomeScore.Int64
	switch {
	case game.HomeScore.Int64 > game.AwayScore.Int64:
		teams[game.HomeTeamID].Points += 3
	case game.HomeScore.Int64 < game.AwayScore.Int64:
		teams[game.AwayTeamID].Points += 3
	default:
		teams[game.HomeTeamID].Points++
		teams[game.AwayTeamID].Points++
	}
}

func (teams teamScoringTotals) addXG(game cache.Game, observation cache.GameXG) {
	home, away := teams[game.HomeTeamID], teams[game.AwayTeamID]
	home.XGCovered++
	away.XGCovered++
	home.xgFor += observation.HomeXG.Float64
	home.xgAgainst += observation.AwayXG.Float64
	away.xgFor += observation.AwayXG.Float64
	away.xgAgainst += observation.HomeXG.Float64
}

func (teams teamScoringTotals) addXPoints(game cache.Game, observation cache.GameXG) {
	home, away := teams[game.HomeTeamID], teams[game.AwayTeamID]
	home.XPointsCovered++
	away.XPointsCovered++
	home.xPoints += observation.HomeXPoints.Float64
	away.xPoints += observation.AwayXPoints.Float64
}

func (teams teamScoringTotals) summaries() []TeamScoring {
	rows := make([]TeamScoring, 0, len(teams))
	for _, team := range teams {
		row := team.TeamScoring
		if row.XGCovered == row.Played {
			row.XGFor, row.XGAgainst = &team.xgFor, &team.xgAgainst
		}
		if row.XPointsCovered == row.Played {
			row.XPoints = &team.xPoints
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TeamID < rows[j].TeamID })
	return rows
}

// TeamComparisonEligible uses the scoring integrity checks, but allows a
// within-season comparison from the first result instead of the league trend's
// 20-match minimum. Counts remain visible so small samples are explicit.
func (s SeasonScoring) TeamComparisonEligible() bool {
	if s.Played == 0 {
		return false
	}
	for _, exclusion := range s.Exclusions {
		if exclusion != "below_minimum_matches" {
			return false
		}
	}
	return true
}
