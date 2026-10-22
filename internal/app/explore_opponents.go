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

const opponentAdjustmentNote = "Adjusted to an average opponent at a neutral venue with even rest."

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
// (Allowed) the team; see forecast.OpponentAdjustments.
type exploreOpponentFactors struct {
	Scored  float64 `json:"scored"`
	Allowed float64 `json:"allowed"`
}

// exploreOpponentCoverage counts the season's completed matches with xG,
// which alone contribute to opponent ratings.
type exploreOpponentCoverage struct {
	XGMatches int `json:"xgMatches"`
	Completed int `json:"completed"`
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
	season.Opponents = &exploreOpponentCoverage{XGMatches: adjustment.XGMatches, Completed: adjustment.CompletedMatches}
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
			matches[index].Opponents = &exploreOpponentFactors{Scored: side.Scored, Allowed: side.Allowed}
		}
	}
}

// opponentAdjusted divides goals and xG by the match's factors. Points stay
// official. A match without factors has no adjusted values.
func (match exploreMatchRecord) opponentAdjusted() exploreMatchRecord {
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
		for measure := range exploreMargin + 1 {
			match.Values[measure] = exploreTeamValues{}
		}
		return match
	}
	scored, allowed := divide(match.Values[exploreScored], match.Opponents.Scored), divide(match.Values[exploreAllowed], match.Opponents.Allowed)
	match.Values[exploreScored], match.Values[exploreAllowed] = scored, allowed
	match.Values[exploreMargin] = exploreTeamValues{Actual: scored.Actual - allowed.Actual}
	if scored.Expected != nil && allowed.Expected != nil {
		margin := *scored.Expected - *allowed.Expected
		match.Values[exploreMargin].Expected = &margin
	}
	return match
}

func opponentAdjustedMatches(matches []exploreMatchRecord) []exploreMatchRecord {
	adjusted := make([]exploreMatchRecord, len(matches))
	for index, match := range matches {
		adjusted[index] = match.opponentAdjusted()
	}
	return adjusted
}

// adjustedLabel names an adjusted series: "Goals scored" becomes "Adjusted
// goals scored"; xG and npxG keep their case.
func adjustedLabel(label string) string {
	if !strings.HasPrefix(label, "xG") && !strings.HasPrefix(label, "npxG") {
		label = strings.ToLower(label[:1]) + label[1:]
	}
	return "Adjusted " + label
}
