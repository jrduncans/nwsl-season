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
	// GoalsCovered counts the valid played matches whose goals are included in
	// GoalsFor and GoalsAgainst. It equals Played unless the non-penalty basis
	// lacks a penalty split for some match. Points always use official scores.
	GoalsCovered              int
	GoalsFor, GoalsAgainst    int64
	XGFor, XGAgainst, XPoints *float64
	Matches                   []TeamMatch
}

// TeamMatch retains the validated observations behind a team's season totals.
// Expected values are available per match, independently of season coverage.
type TeamMatch struct {
	ID, KickoffUTC, OpponentID, StadiumID string
	Home                                  bool
	// GoalsFor and GoalsAgainst follow the scoring basis. ScoreFor and
	// ScoreAgainst are the official result, which determines Points.
	GoalsFor, GoalsAgainst int64
	ScoreFor, ScoreAgainst int64
	XGFor, XGAgainst       *float64
	Points                 int
	XPoints                *float64
}

type teamScoringAccumulator struct {
	TeamScoring
	xgFor, xgAgainst float64
	xPoints          float64
}

type teamScoringTotals map[string]*teamScoringAccumulator

// addResult records one valid completed game. homeGoals and awayGoals follow
// the scoring basis; when goalsCovered is false they are unknown, so the match
// keeps the official score and the totals leave it out.
func (teams teamScoringTotals) addResult(game cache.Game, homeGoals, awayGoals int64, goalsCovered bool) {
	var venue struct {
		StadiumID string `json:"stadium_id"`
	}
	if err := json.Unmarshal([]byte(game.RawJSON), &venue); err != nil {
		venue.StadiumID = ""
	}
	if !goalsCovered {
		homeGoals, awayGoals = game.HomeScore.Int64, game.AwayScore.Int64
	}
	for _, id := range []string{game.HomeTeamID, game.AwayTeamID} {
		if teams[id] == nil {
			teams[id] = &teamScoringAccumulator{TeamScoring: TeamScoring{TeamID: id}}
		}
		teams[id].Played++
		match := TeamMatch{ID: game.ASAID, KickoffUTC: game.KickoffUTC, StadiumID: venue.StadiumID, Home: id == game.HomeTeamID}
		match.OpponentID = game.AwayTeamID
		match.GoalsFor, match.GoalsAgainst = homeGoals, awayGoals
		match.ScoreFor, match.ScoreAgainst = game.HomeScore.Int64, game.AwayScore.Int64
		if !match.Home {
			match.OpponentID = game.HomeTeamID
			match.GoalsFor, match.GoalsAgainst = awayGoals, homeGoals
			match.ScoreFor, match.ScoreAgainst = game.AwayScore.Int64, game.HomeScore.Int64
		}
		switch {
		case match.ScoreFor > match.ScoreAgainst:
			match.Points = 3
		case match.ScoreFor == match.ScoreAgainst:
			match.Points = 1
		}
		teams[id].Points += match.Points
		teams[id].Matches = append(teams[id].Matches, match)
	}
	if goalsCovered {
		teams[game.HomeTeamID].GoalsCovered++
		teams[game.AwayTeamID].GoalsCovered++
		teams[game.HomeTeamID].GoalsFor += homeGoals
		teams[game.HomeTeamID].GoalsAgainst += awayGoals
		teams[game.AwayTeamID].GoalsFor += awayGoals
		teams[game.AwayTeamID].GoalsAgainst += homeGoals
	}
}

func (teams teamScoringTotals) addXG(game cache.Game, homeXG, awayXG float64) {
	home, away := teams[game.HomeTeamID], teams[game.AwayTeamID]
	home.XGCovered++
	away.XGCovered++
	home.xgFor += homeXG
	home.xgAgainst += awayXG
	away.xgFor += awayXG
	away.xgAgainst += homeXG
	homeMatch := &home.Matches[len(home.Matches)-1]
	awayMatch := &away.Matches[len(away.Matches)-1]
	homeMatch.XGFor, homeMatch.XGAgainst = &homeXG, &awayXG
	awayMatch.XGFor, awayMatch.XGAgainst = homeMatch.XGAgainst, homeMatch.XGFor
}

func (teams teamScoringTotals) addXPoints(game cache.Game, observation cache.GameXG) {
	home, away := teams[game.HomeTeamID], teams[game.AwayTeamID]
	home.XPointsCovered++
	away.XPointsCovered++
	home.xPoints += observation.HomeXPoints.Float64
	away.xPoints += observation.AwayXPoints.Float64
	home.Matches[len(home.Matches)-1].XPoints = &observation.HomeXPoints.Float64
	away.Matches[len(away.Matches)-1].XPoints = &observation.AwayXPoints.Float64
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
