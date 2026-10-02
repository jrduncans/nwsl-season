package history

import (
	"encoding/json"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
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
	Matches                   []TeamMatch
}

// TeamMatch retains the validated observations behind a team's season totals.
// Expected values are available per match, independently of season coverage.
type TeamMatch struct {
	ID, KickoffUTC, OpponentID, StadiumID string
	Home                                  bool
	GoalsFor, GoalsAgainst                int64
	XGFor, XGAgainst                      *float64
}

type teamScoringAccumulator struct {
	TeamScoring
	xgFor, xgAgainst float64
	xPoints          float64
}

type teamScoringTotals map[string]*teamScoringAccumulator

func (teams teamScoringTotals) addResult(game cache.Game) {
	var venue struct {
		StadiumID string `json:"stadium_id"`
	}
	if err := json.Unmarshal([]byte(game.RawJSON), &venue); err != nil {
		venue.StadiumID = ""
	}
	for _, id := range []string{game.HomeTeamID, game.AwayTeamID} {
		if teams[id] == nil {
			teams[id] = &teamScoringAccumulator{TeamScoring: TeamScoring{TeamID: id}}
		}
		teams[id].Played++
		match := TeamMatch{ID: game.ASAID, KickoffUTC: game.KickoffUTC, StadiumID: venue.StadiumID, Home: id == game.HomeTeamID}
		match.OpponentID, match.GoalsFor, match.GoalsAgainst = game.AwayTeamID, game.HomeScore.Int64, game.AwayScore.Int64
		if !match.Home {
			match.OpponentID, match.GoalsFor, match.GoalsAgainst = game.HomeTeamID, game.AwayScore.Int64, game.HomeScore.Int64
		}
		teams[id].Matches = append(teams[id].Matches, match)
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
	homeMatch := &home.Matches[len(home.Matches)-1]
	awayMatch := &away.Matches[len(away.Matches)-1]
	homeMatch.XGFor, homeMatch.XGAgainst = &observation.HomeXG.Float64, &observation.AwayXG.Float64
	awayMatch.XGFor, awayMatch.XGAgainst = homeMatch.XGAgainst, homeMatch.XGFor
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
		// Compare instants, not timestamp strings: offsets can differ. Undated
		// results follow dated results and use fixture ID for a stable order.
		sort.Slice(row.Matches, func(i, j int) bool {
			a, aErr := fixtures.ParseKickoff(row.Matches[i].KickoffUTC)
			b, bErr := fixtures.ParseKickoff(row.Matches[j].KickoffUTC)
			if (aErr == nil) != (bErr == nil) {
				return aErr == nil
			}
			if aErr == nil && !a.Equal(b) {
				return a.Before(b)
			}
			return row.Matches[i].ID < row.Matches[j].ID
		})
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
