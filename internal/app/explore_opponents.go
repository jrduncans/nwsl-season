package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/forecast"
)

// opponentsParam selects Match by match's opponent adjustment. Absent is
// official values; "adjusted" rescales them to an average opponent.
const (
	opponentsParam    = "opponents"
	opponentsAdjusted = "adjusted"
)

// opponentAdjustmentNote describes the switch, so the note reads the same, and
// keeps its length, whether the switch is on or off.
const opponentAdjustmentNote = "Adjust for opponents rescales values to an average opponent at a neutral venue with even rest."

var errOpponentsSelection = fmt.Errorf("opponents must be adjusted")

func opponentsSelection(query url.Values) (bool, error) {
	values, present := query[opponentsParam]
	if !present {
		return false, nil
	}
	if len(values) != 1 || values[0] != opponentsAdjusted {
		return false, errOpponentsSelection
	}
	return true, nil
}

// exploreOpponentFactors divide a match's values for (Scored) and against
// (Allowed) the team; ExpectedPoints, less the season's AveragePoints, is
// subtracted from its points and xPoints. See forecast.OpponentAdjustments.
type exploreOpponentFactors struct {
	Scored         float64 `json:"scored"`
	Allowed        float64 `json:"allowed"`
	ExpectedPoints float64 `json:"expectedPoints"`
}

// exploreOpponentCoverage counts the season's completed matches with xG,
// which alone contribute to opponent ratings. AveragePoints is an average
// team's expected points in the season's average match.
type exploreOpponentCoverage struct {
	XGMatches     int     `json:"xgMatches"`
	Completed     int     `json:"completed"`
	AveragePoints float64 `json:"averagePoints"`
}

// Note returns the coverage clause, or nothing when coverage is complete.
func (coverage exploreOpponentCoverage) Note() string {
	if coverage.XGMatches == coverage.Completed {
		return ""
	}
	return fmt.Sprintf("Opponent ratings use %d of %d matches with xG.", coverage.XGMatches, coverage.Completed)
}

// populateExploreOpponentFactors gives each match its team's factors. A season
// without xG, or whose ratings cannot be fitted, has no coverage, so it offers
// no adjustment.
func populateExploreOpponentFactors(season *exploreTeamSeason, data cache.SeasonData) {
	adjustment, err := forecast.OpponentAdjustments(forecast.FitInput{
		Teams: data.Teams, Games: standingsGames(data.Games),
		XGoals: forecastXGoals(data), HistoricalVenue: forecastVenueSample(data),
	})
	if err != nil || adjustment.XGMatches == 0 {
		return
	}
	season.Opponents = &exploreOpponentCoverage{XGMatches: adjustment.XGMatches, Completed: adjustment.CompletedMatches, AveragePoints: adjustment.AveragePoints}
	for teamIndex := range season.Teams {
		matches := season.Teams[teamIndex].Matches
		for index := range matches {
			fixture, ok := adjustment.Fixtures[matches[index].ID]
			if !ok {
				continue
			}
			side := fixture.Away
			if matches[index].Venue == "Home" {
				side = fixture.Home
			}
			matches[index].Opponents = &exploreOpponentFactors{Scored: side.Scored, Allowed: side.Allowed, ExpectedPoints: side.ExpectedPoints}
		}
	}
}

// opponentAdjusted divides goals and xG by the match's factors, and shifts
// points and xPoints by how many more points an average team would expect
// here than in the season's average match. A match without factors has no
// adjusted values; see adjustedPointsCovered.
func (match exploreMatchRecord) opponentAdjusted(averagePoints float64) exploreMatchRecord {
	divide := func(value exploreTeamValues, factor float64) exploreTeamValues {
		adjusted := exploreTeamValues{Actual: value.Actual / factor}
		if value.Expected != nil {
			expected := *value.Expected / factor
			adjusted.Expected = &expected
		}
		return adjusted
	}
	if match.Opponents == nil {
		match.GoalsCovered = false
		for measure := range match.Values {
			match.Values[measure] = exploreTeamValues{}
		}
		return match
	}
	shift := match.Opponents.ExpectedPoints - averagePoints
	points := exploreTeamValues{Actual: match.Values[explorePoints].Actual - shift}
	if expected := match.Values[explorePoints].Expected; expected != nil {
		points.Expected = new(*expected - shift)
	}
	match.Values[explorePoints] = points
	scored, allowed := divide(match.Values[exploreScored], match.Opponents.Scored), divide(match.Values[exploreAllowed], match.Opponents.Allowed)
	match.Values[exploreScored], match.Values[exploreAllowed] = scored, allowed
	match.Values[exploreMargin] = exploreTeamValues{Actual: scored.Actual - allowed.Actual}
	if scored.Expected != nil && allowed.Expected != nil {
		margin := *scored.Expected - *allowed.Expected
		match.Values[exploreMargin].Expected = &margin
	}
	return match
}

func opponentAdjustedMatches(matches []exploreMatchRecord, averagePoints float64) []exploreMatchRecord {
	adjusted := make([]exploreMatchRecord, len(matches))
	for index, match := range matches {
		adjusted[index] = match.opponentAdjusted(averagePoints)
	}
	return adjusted
}

// adjustedPointsCovered reports whether an adjusted match has adjusted
// points: points, unlike goals, have no coverage flag of their own.
func adjustedPointsCovered(match exploreMatchRecord) bool {
	return match.Opponents != nil
}

// adjustedLabel names an adjusted series: "Goals scored" becomes "Adjusted
// goals scored"; xG and npxG keep their case.
func adjustedLabel(label string) string {
	if !strings.HasPrefix(label, "xG") && !strings.HasPrefix(label, "npxG") {
		label = strings.ToLower(label[:1]) + label[1:]
	}
	return "Adjusted " + label
}
