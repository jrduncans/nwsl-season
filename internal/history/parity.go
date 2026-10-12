package history

import (
	"math"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

// TeamParity is one team's points per game in a season. XPPG exists only when
// the team has ASA xPoints for every played match.
type TeamParity struct {
	TeamID string
	Played int
	Points int
	PPG    float64
	XPPG   *float64
}

// SeasonParity measures how evenly matched a season's teams were, from earned
// points per game (PPG).
//
// Spread is the population standard deviation of team PPG: every team in the
// league, not a sample. Chance is the spread that equally good teams would
// show from random results alone, under a null model in which each match is a
// draw with the season's observed draw rate and otherwise a 50/50 win or loss.
// Each team's points then vary per match by σ² = 4.5(1 − d) + d − (1.5(1 − d) + d)²,
// so the chance spread after n matches is σ / √n, with n the mean matches
// played per team.
//
// Values exist only for a PlotEligible season. XSpread additionally needs
// xPoints for every played match of every team, so its absence leaves the
// actual values in place.
type SeasonParity struct {
	Season       string
	Lifecycle    cache.SourceScopeLifecycle
	Played       int
	PlotEligible bool
	Exclusions   []string
	// Teams is ordered by exact PPG, highest first, then by team ID.
	Teams []TeamParity
	// MatchesPerTeam is the mean matches played per team; DrawRate is the
	// share of played matches that were draws.
	MatchesPerTeam, DrawRate float64
	Spread, Chance           float64
	XSpread                  *float64
	// Gap is the best PPG minus the worst. Q1 and Q3 are the PPG quartiles by
	// Quantile, so Q3 − Q1 is the interquartile range (the middle half).
	Gap, Q1, Q3 float64
	// Top and Bottom name every team tied at the best and worst exact PPG.
	Top, Bottom []string
}

// IQR is the interquartile range of team PPG: the width of the middle half.
func (s SeasonParity) IQR() float64 { return s.Q3 - s.Q1 }

// SummarizeParity calculates every season's parity from one historical
// snapshot on the chosen basis, using SummarizeScoringBasis's match and
// eligibility rules. Points never change with the basis, but the non-penalty
// basis's coverage rule still decides eligibility. It never changes the
// snapshot.
func SummarizeParity(inputs []cache.HistoricalSeason, basis ScoringBasis) ([]SeasonParity, error) {
	summaries, err := SummarizeScoringBasis(inputs, basis)
	if err != nil {
		return nil, err
	}
	return Parity(summaries), nil
}

// Parity derives each season's parity from scoring summaries that were already
// calculated, in the same order.
func Parity(summaries []SeasonScoring) []SeasonParity {
	result := make([]SeasonParity, 0, len(summaries))
	for _, summary := range summaries {
		result = append(result, paritySeason(summary))
	}
	return result
}

func paritySeason(summary SeasonScoring) SeasonParity {
	season := SeasonParity{
		Season: summary.Season, Lifecycle: summary.Lifecycle, Played: summary.Played,
		PlotEligible: summary.PlotEligible, Exclusions: summary.Exclusions,
	}
	if !summary.PlotEligible || len(summary.Teams) == 0 {
		season.PlotEligible = false
		return season
	}

	appearances, draws := 0, 0
	xComplete := true
	for _, team := range summary.Teams {
		row := TeamParity{TeamID: team.TeamID, Played: team.Played, Points: team.Points, PPG: float64(team.Points) / float64(team.Played)}
		if team.XPoints != nil {
			xppg := *team.XPoints / float64(team.Played)
			row.XPPG = &xppg
		} else {
			xComplete = false
		}
		for _, match := range team.Matches {
			if match.ScoreFor == match.ScoreAgainst {
				draws++
			}
		}
		appearances += team.Played
		season.Teams = append(season.Teams, row)
	}
	sort.SliceStable(season.Teams, func(i, j int) bool {
		a, b := season.Teams[i], season.Teams[j]
		if a.PPG != b.PPG {
			return a.PPG > b.PPG
		}
		return a.TeamID < b.TeamID
	})

	// Every match has two appearances, so the appearance share of draws is the
	// match share.
	season.DrawRate = float64(draws) / float64(appearances)
	season.MatchesPerTeam = float64(appearances) / float64(len(season.Teams))
	season.Chance = ChanceSpread(season.DrawRate, season.MatchesPerTeam)

	ppg := make([]float64, 0, len(season.Teams))
	xppg := make([]float64, 0, len(season.Teams))
	for _, team := range season.Teams {
		ppg = append(ppg, team.PPG)
		if team.XPPG != nil {
			xppg = append(xppg, *team.XPPG)
		}
	}
	season.Spread = populationSD(ppg)
	if xComplete {
		spread := populationSD(xppg)
		season.XSpread = &spread
	}

	ascending := append([]float64(nil), ppg...)
	sort.Float64s(ascending)
	best, worst := ascending[len(ascending)-1], ascending[0]
	season.Gap = best - worst
	season.Q1, season.Q3 = Quantile(ascending, .25), Quantile(ascending, .75)
	for _, team := range season.Teams {
		if team.PPG == best {
			season.Top = append(season.Top, team.TeamID)
		}
		if team.PPG == worst {
			season.Bottom = append(season.Bottom, team.TeamID)
		}
	}
	return season
}

// ChanceSpread is the PPG standard deviation that teams of equal strength would
// show after matches matches each, when a share drawRate of matches are draws
// and every other match is a 50/50 win or loss.
func ChanceSpread(drawRate, matches float64) float64 {
	decisive := 1 - drawRate
	mean := 1.5*decisive + drawRate
	variance := 4.5*decisive + drawRate - mean*mean
	return math.Sqrt(variance / matches)
}

// Quantile returns the p-quantile of ascending values by linear interpolation
// between the closest ranks: position (n − 1) × p, the default in R and NumPy.
// The Explore chart receives these values rather than computing its own.
func Quantile(ascending []float64, p float64) float64 {
	if len(ascending) == 0 {
		return math.NaN()
	}
	position := float64(len(ascending)-1) * p
	lower := int(math.Floor(position))
	if lower >= len(ascending)-1 {
		return ascending[len(ascending)-1]
	}
	return ascending[lower] + (position-float64(lower))*(ascending[lower+1]-ascending[lower])
}

// populationSD is the standard deviation of values as a whole population.
func populationSD(values []float64) float64 {
	var mean float64
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	var squares float64
	for _, value := range values {
		squares += (value - mean) * (value - mean)
	}
	return math.Sqrt(squares / float64(len(values)))
}
