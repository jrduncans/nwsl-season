package history

import (
	"slices"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

// SeasonHomeAdvantage is one season's home-minus-away gap per played match.
// "Home" is ASA's home designation: ASA has no neutral-site flag, so an
// alternate-venue match counts as a home match. A gap is a difference between
// the two sides' results, not a cause.
//
// Rates exist only for a PlotEligible season. Each expected rate needs complete
// coverage of its own metric, so a missing xG or xPoints rate leaves the
// season's actual rates in place.
type SeasonHomeAdvantage struct {
	Season       string
	Lifecycle    cache.SourceScopeLifecycle
	Basis        ScoringBasis
	Played       int
	PlotEligible bool
	Exclusions   []string
	// HomeWins, Draws and AwayWins count official results; they sum to Played.
	HomeWins, Draws, AwayWins int
	// Goals and XG follow Basis; Points and XPoints never change with it.
	Goals, XG, Points, XPoints *float64
}

// SummarizeHomeAdvantage calculates every season's home-minus-away gap from one
// historical snapshot on the chosen basis, using SummarizeScoringBasis's
// match, coverage and eligibility rules. It never changes the snapshot.
func SummarizeHomeAdvantage(inputs []cache.HistoricalSeason, basis ScoringBasis) ([]SeasonHomeAdvantage, error) {
	summaries, err := SummarizeScoringBasis(inputs, basis)
	if err != nil {
		return nil, err
	}
	return HomeAdvantage(summaries), nil
}

// HomeAdvantage derives the home-minus-away gap from scoring summaries that
// were already calculated, in the same order.
func HomeAdvantage(summaries []SeasonScoring) []SeasonHomeAdvantage {
	result := make([]SeasonHomeAdvantage, 0, len(summaries))
	for _, summary := range summaries {
		result = append(result, homeAdvantageSeason(summary))
	}
	return result
}

// homeAdvantageMatch joins the two sides' records of one played match.
type homeAdvantageMatch struct {
	home        TeamMatch
	awayXPoints *float64
}

func homeAdvantageSeason(summary SeasonScoring) SeasonHomeAdvantage {
	season := SeasonHomeAdvantage{
		Season: summary.Season, Lifecycle: summary.Lifecycle, Basis: summary.Basis,
		Played: summary.Played, PlotEligible: summary.PlotEligible, Exclusions: summary.Exclusions,
	}
	if !summary.PlotEligible {
		return season
	}

	matches := make(map[string]*homeAdvantageMatch, summary.Played)
	for _, team := range summary.Teams {
		for _, match := range team.Matches {
			joined := matches[match.ID]
			if joined == nil {
				joined = &homeAdvantageMatch{}
				matches[match.ID] = joined
			}
			if match.Home {
				joined.home = match
			} else {
				joined.awayXPoints = match.XPoints
			}
		}
	}

	var goals, points int64
	var xg, xPoints float64
	// Sum in fixture ID order so the floating-point totals are deterministic.
	ids := make([]string, 0, len(matches))
	for id := range matches {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		match := matches[id]
		home := match.home
		goals += home.GoalsFor - home.GoalsAgainst
		switch {
		case home.ScoreFor > home.ScoreAgainst:
			season.HomeWins++
			points += 3
		case home.ScoreFor == home.ScoreAgainst:
			season.Draws++
		default:
			season.AwayWins++
			points -= 3
		}
		if home.XGFor != nil && home.XGAgainst != nil {
			xg += *home.XGFor - *home.XGAgainst
		}
		if home.XPoints != nil && match.awayXPoints != nil {
			xPoints += *home.XPoints - *match.awayXPoints
		}
	}

	played := float64(summary.Played)
	perMatch := func(total float64) *float64 {
		rate := total / played
		return &rate
	}
	season.Goals = perMatch(float64(goals))
	season.Points = perMatch(float64(points))
	if summary.XGCovered == summary.Played {
		season.XG = perMatch(xg)
	}
	if summary.XPointsCovered == summary.Played {
		season.XPoints = perMatch(xPoints)
	}
	return season
}
