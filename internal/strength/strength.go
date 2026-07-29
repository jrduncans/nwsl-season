// Package strength calculates transparent remaining strength-of-schedule
// measures from standings-domain values.
package strength

import (
	"math"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// RemainingStatus is the upstream status used for an unplayed fixture.
const RemainingStatus = fixtures.PreMatchStatus

const (
	// LabelHarder is used when a team's remaining opponents are meaningfully
	// above the league baseline.
	LabelHarder = "Harder"
	// LabelNearAverage is used when a team's remaining opponents are close to
	// the league baseline.
	LabelNearAverage = "Near average"
	// LabelEasier is used when a team's remaining opponents are meaningfully
	// below the league baseline.
	LabelEasier = "Easier"

	// QualitativeThreshold is the smallest absolute baseline delta that gets a
	// qualitative label other than Near average.
	QualitativeThreshold = 0.10
)

// Fixture contains the opponent and venue contribution for one remaining
// fixture. Available is false when the opponent has no completed-match
// history, so callers can explain missing values without treating zero as a
// real estimate.
type Fixture struct {
	ID                       string
	Opponent                 standings.Team
	Home                     bool
	OpponentPPG              float64
	VenueAdjustedOpponentPPG float64
	Available                bool
}

// Row contains the strength measures for one team.
type Row struct {
	Team                     standings.Team
	RemainingFixtures        int
	RemainingHome            int
	RemainingAway            int
	Fixtures                 []Fixture
	HomeOpponentPPG          float64
	AwayOpponentPPG          float64
	RawOpponentPPG           float64
	VenueAdjustedOpponentPPG float64
	DeltaFromBaseline        float64
	ScheduleLabel            string
	UnavailableReason        UnavailableReason
	Available                bool
}

// UnavailableReason explains why a row cannot supply an aggregate estimate.
type UnavailableReason string

const (
	UnavailableNoRemainingFixtures UnavailableReason = "no_remaining_fixtures"
	UnavailableNoOpponentHistory   UnavailableReason = "no_opponent_history"
)

// Result contains schedule-strength rows and the derived league context used
// to explain the venue-adjusted measure.
type Result struct {
	Rows             []Row
	CompletedMatches int
	RemainingMatches int
	AvailableRows    int
	ComparableRows   int
	Baseline         float64
	HomePPG          float64
	AwayPPG          float64
	VenueGap         float64
}

// VenueSample contains already-aggregated completed matches from prior
// seasons. It affects only the league venue gap; opponent PPG remains based on
// the current season.
type VenueSample struct {
	Matches                int
	HomePoints, AwayPoints int
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
	return CalculateWithVenueSample(teams, games, VenueSample{})
}

// CalculateWithVenueSample pools prior-season and current-season venue points
// while retaining current-season opponent records.
func CalculateWithVenueSample(teams []standings.Team, games []standings.Game, venue VenueSample) Result {
	teamByID := make(map[string]standings.Team, len(teams))
	for _, team := range teams {
		teamByID[team.ID] = team
	}

	allRecords := make(map[string]record, len(teams))
	homePoints, awayPoints, completed := venue.HomePoints, venue.AwayPoints, venue.Matches
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
			opponentTeam, opponentKnown := teamByID[opponentID]
			if !opponentKnown {
				opponentTeam = standings.Team{ID: opponentID}
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
				row.UnavailableReason = UnavailableNoOpponentHistory
				row.Fixtures = append(row.Fixtures, Fixture{ID: game.ID, Opponent: opponentTeam, Home: homeFixture})
				continue
			}
			opponentPPG := float64(opponent.points) / float64(opponent.played)
			adjustedPPG := opponentPPG
			if homeFixture {
				adjustedPPG -= result.VenueGap / 2
			} else {
				adjustedPPG += result.VenueGap / 2
			}
			row.Fixtures = append(row.Fixtures, Fixture{
				ID: game.ID, Opponent: opponentTeam, Home: homeFixture,
				OpponentPPG: opponentPPG, VenueAdjustedOpponentPPG: adjustedPPG, Available: true,
			})
			rawSum += opponentPPG
			if homeFixture {
				homeSum += opponentPPG
				adjustedSum += adjustedPPG
			} else {
				awaySum += opponentPPG
				adjustedSum += adjustedPPG
			}
		}
		if row.RemainingFixtures == 0 {
			row.Available = false
			row.UnavailableReason = UnavailableNoRemainingFixtures
		} else if row.Available {
			row.HomeOpponentPPG = average(homeSum, row.RemainingHome)
			row.AwayOpponentPPG = average(awaySum, row.RemainingAway)
			row.RawOpponentPPG = rawSum / float64(row.RemainingFixtures)
			row.VenueAdjustedOpponentPPG = adjustedSum / float64(row.RemainingFixtures)
			result.AvailableRows++
		}
		if row.RemainingFixtures > 0 {
			result.ComparableRows++
		}
		rows = append(rows, row)
	}
	// A league baseline must be based on every team that still has a fixture.
	// Otherwise the omitted teams silently change every displayed comparison.
	if result.ComparableRows > 1 && result.AvailableRows == result.ComparableRows {
		for _, row := range rows {
			if !row.Available {
				continue
			}
			result.Baseline += row.VenueAdjustedOpponentPPG
		}
		result.Baseline /= float64(result.AvailableRows)
		for index := range rows {
			if !rows[index].Available {
				continue
			}
			rows[index].DeltaFromBaseline = rows[index].VenueAdjustedOpponentPPG - result.Baseline
			rows[index].ScheduleLabel = LabelForDelta(rows[index].DeltaFromBaseline)
		}
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

// LabelForDelta maps a signed difference from the league baseline to a plain
// language label. Exact deltas remain available to prevent the label from
// overstating small differences.
func LabelForDelta(delta float64) string {
	// Labels and deltas are both shown to two decimal places. Classifying the
	// same rounded value avoids displaying "+0.10" as both Near average and
	// Harder depending on invisible precision.
	delta = math.Round(delta*100) / 100
	switch {
	case delta > QualitativeThreshold:
		return LabelHarder
	case delta < -QualitativeThreshold:
		return LabelEasier
	default:
		return LabelNearAverage
	}
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
