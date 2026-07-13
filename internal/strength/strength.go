// Package strength calculates transparent remaining strength-of-schedule
// measures from standings-domain values.
package strength

import (
	"sort"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// RemainingStatus is the upstream status used for an unplayed fixture.
const RemainingStatus = "PreMatch"

// Row contains the strength measures for one team.
type Row struct {
	Team                     standings.Team
	RemainingFixtures        int
	RemainingHome            int
	RemainingAway            int
	HomeOpponentPPG          float64
	AwayOpponentPPG          float64
	RawOpponentPPG           float64
	VenueAdjustedOpponentPPG float64
	Available                bool
}

// Result contains schedule-strength rows and the derived league context used
// to explain the venue-adjusted measure.
type Result struct {
	Rows             []Row
	CompletedMatches int
	RemainingMatches int
	HomePPG          float64
	AwayPPG          float64
	VenueGap         float64
}

type record struct {
	played int
	points int
}

// Calculate returns raw and venue-adjusted strength for remaining fixtures.
// Raw opponent PPG is the mean of each remaining opponent's current PPG,
// counting an opponent once per fixture. The venue adjustment derives the
// league home/away PPG gap from completed matches and applies half the gap in
// the direction of the fixture venue.
func Calculate(teams []standings.Team, games []standings.Game) Result {
	teamByID := make(map[string]standings.Team, len(teams))
	for _, team := range teams {
		teamByID[team.ID] = team
	}

	allRecords := make(map[string]record, len(teams))
	var homePoints, awayPoints, completed int
	for _, game := range games {
		if game.Status != standings.CompletedStatus || game.HomeScore == nil || game.AwayScore == nil {
			continue
		}
		if _, ok := teamByID[game.HomeTeamID]; !ok {
			continue
		}
		if _, ok := teamByID[game.AwayTeamID]; !ok {
			continue
		}
		home, away := points(*game.HomeScore, *game.AwayScore), points(*game.AwayScore, *game.HomeScore)
		allRecords[game.HomeTeamID] = addRecord(allRecords[game.HomeTeamID], home)
		allRecords[game.AwayTeamID] = addRecord(allRecords[game.AwayTeamID], away)
		homePoints += home
		awayPoints += away
		completed++
	}

	result := Result{CompletedMatches: completed}
	for _, game := range games {
		if game.Status == RemainingStatus {
			result.RemainingMatches++
		}
	}
	if completed > 0 {
		result.HomePPG = float64(homePoints) / float64(completed)
		result.AwayPPG = float64(awayPoints) / float64(completed)
		result.VenueGap = result.HomePPG - result.AwayPPG
	}

	rows := make([]Row, 0, len(teams))
	for _, team := range teams {
		row := Row{Team: team, Available: true}
		var rawSum, homeSum, awaySum, adjustedSum float64
		for _, game := range games {
			if game.Status != RemainingStatus {
				continue
			}
			var opponentID string
			var homeFixture bool
			switch {
			case game.HomeTeamID == team.ID:
				opponentID, homeFixture = game.AwayTeamID, true
			case game.AwayTeamID == team.ID:
				opponentID = game.HomeTeamID
			default:
				continue
			}
			row.RemainingFixtures++
			if homeFixture {
				row.RemainingHome++
			} else {
				row.RemainingAway++
			}
			opponent, ok := allRecords[opponentID]
			if !ok || opponent.played == 0 {
				row.Available = false
				continue
			}
			opponentPPG := float64(opponent.points) / float64(opponent.played)
			rawSum += opponentPPG
			if homeFixture {
				homeSum += opponentPPG
				adjustedSum += opponentPPG - result.VenueGap/2
			} else {
				awaySum += opponentPPG
				adjustedSum += opponentPPG + result.VenueGap/2
			}
		}
		if row.RemainingFixtures == 0 {
			row.Available = false
		} else if row.Available {
			row.HomeOpponentPPG = average(homeSum, row.RemainingHome)
			row.AwayOpponentPPG = average(awaySum, row.RemainingAway)
			row.RawOpponentPPG = rawSum / float64(row.RemainingFixtures)
			row.VenueAdjustedOpponentPPG = adjustedSum / float64(row.RemainingFixtures)
		}
		rows = append(rows, row)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Available != rows[j].Available {
			return rows[i].Available
		}
		if rows[i].VenueAdjustedOpponentPPG != rows[j].VenueAdjustedOpponentPPG {
			return rows[i].VenueAdjustedOpponentPPG > rows[j].VenueAdjustedOpponentPPG
		}
		if rows[i].RawOpponentPPG != rows[j].RawOpponentPPG {
			return rows[i].RawOpponentPPG > rows[j].RawOpponentPPG
		}
		if rows[i].Team.Name != rows[j].Team.Name {
			return rows[i].Team.Name < rows[j].Team.Name
		}
		return rows[i].Team.ID < rows[j].Team.ID
	})
	result.Rows = rows
	return result
}

func addRecord(value record, points int) record {
	value.played++
	value.points += points
	return value
}

func points(goalsFor, goalsAgainst int) int {
	switch {
	case goalsFor > goalsAgainst:
		return 3
	case goalsFor == goalsAgainst:
		return 1
	default:
		return 0
	}
}

func average(sum float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}
