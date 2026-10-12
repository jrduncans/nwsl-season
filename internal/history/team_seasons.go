package history

import (
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// MinimumTeamSeasonMatches applies to every archive ranking of an active season.
const MinimumTeamSeasonMatches = 10

// TeamSeasonMeasure uses the same order as Compare teams' scoring values.
type TeamSeasonMeasure int

const (
	TeamSeasonFor TeamSeasonMeasure = iota
	TeamSeasonAgainst
	TeamSeasonDifference
	TeamSeasonPoints
)

// TeamSeasonRankBy selects the value that determines a competition rank.
type TeamSeasonRankBy int

const (
	TeamSeasonActual TeamSeasonRankBy = iota
	TeamSeasonExpected
	TeamSeasonGap
)

// TeamSeasonValues holds per-match values and ranks for one measure. A nil
// expected value never hides actual results. Gap always means actual − expected.
type TeamSeasonValues struct {
	Actual   *float64 `json:"actual"`
	Expected *float64 `json:"expected"`
	Gap      *float64 `json:"gap"`
	Ranks    [3]int   `json:"ranks"`
	Eligible bool     `json:"eligible"`
	Reason   string   `json:"reason"`
}

// Value returns the unrounded value for a ranking mode.
func (v TeamSeasonValues) Value(by TeamSeasonRankBy) *float64 {
	switch by {
	case TeamSeasonExpected:
		return v.Expected
	case TeamSeasonGap:
		return v.Gap
	default:
		return v.Actual
	}
}

// TeamSeason keeps one ASA identity in one season, including unrankable rows so
// an explicit selection can explain its exclusions without substituting a row.
type TeamSeason struct {
	Season string              `json:"season"`
	TeamID string              `json:"id"`
	Name   string              `json:"name"`
	Active bool                `json:"active"`
	Played int                 `json:"played"`
	Values [4]TeamSeasonValues `json:"values"`
}

// SummarizeTeamSeasons ranks all four measures by actual, expected and gap.
// Points always use all-shots eligibility, even when goals use NonPenalty.
func SummarizeTeamSeasons(inputs []cache.HistoricalSeason, basis ScoringBasis) ([]TeamSeason, error) {
	all, err := SummarizeScoringBasis(inputs, AllShots)
	if err != nil {
		return nil, err
	}
	goals := all
	if basis.ExcludesPenalties() {
		goals, err = SummarizeScoringBasis(inputs, basis)
		if err != nil {
			return nil, err
		}
	}
	return TeamSeasons(all, goals, inputs), nil
}

// TeamSeasons reuses scoring summaries. all supplies official points and their
// eligibility; goals supplies the chosen goals basis and its integrity checks.
func TeamSeasons(all, goals []SeasonScoring, inputs []cache.HistoricalSeason) []TeamSeason {
	goalSeasons := map[string]SeasonScoring{}
	for _, season := range goals {
		goalSeasons[season.Season] = season
	}
	var rows []TeamSeason
	for _, summary := range all {
		names := map[string]string{}
		for _, input := range inputs {
			if input.Entry.Season == summary.Season {
				for _, team := range input.Data.Teams {
					names[team.ID] = standings.DisplayName(team)
				}
			}
		}
		eligible := summary.TeamComparisonEligible()
		reason := ""
		if !eligible {
			reason = "Season is not eligible: " + strings.Join(summary.Exclusions, ", ")
		}
		active := summary.Lifecycle == cache.SourceScopeActive
		if active && eligible {
			played := map[string]int{}
			for _, team := range summary.Teams {
				played[team.TeamID] = team.Played
			}
			for id := range names {
				if played[id] < MinimumTeamSeasonMatches {
					eligible = false
				}
			}
			for _, count := range played {
				if count < MinimumTeamSeasonMatches {
					eligible = false
				}
			}
			if !eligible {
				reason = "In progress below the 10-match gate: every team must have played at least 10 valid matches"
			}
		}
		goalSeason := goalSeasons[summary.Season]
		goalTeams := map[string]TeamScoring{}
		for _, team := range goalSeason.Teams {
			goalTeams[team.TeamID] = team
		}
		teams := append([]TeamScoring(nil), summary.Teams...)
		recorded := map[string]bool{}
		for _, team := range teams {
			recorded[team.TeamID] = true
		}
		for id := range names {
			if !recorded[id] {
				teams = append(teams, TeamScoring{TeamID: id})
			}
		}
		for _, team := range teams {
			row := TeamSeason{Season: summary.Season, TeamID: team.TeamID, Name: names[team.TeamID], Active: active, Played: team.Played}
			if row.Name == "" {
				row.Name = row.TeamID
			}
			if team.Played == 0 {
				emptyReason := reason
				if emptyReason == "" {
					emptyReason = "No valid played matches for this team-season"
				}
				for index := range row.Values {
					row.Values[index].Reason = emptyReason
				}
				rows = append(rows, row)
				continue
			}
			n := float64(team.Played)
			ppg := float64(team.Points) / n
			points := TeamSeasonValues{Actual: &ppg, Eligible: eligible, Reason: reason}
			if team.XPoints != nil {
				expected := *team.XPoints / n
				points.Expected = &expected
			}
			row.Values[TeamSeasonPoints] = withTeamSeasonGap(points)
			gt := goalTeams[team.TeamID]
			for _, measure := range []TeamSeasonMeasure{TeamSeasonFor, TeamSeasonAgainst, TeamSeasonDifference} {
				value := TeamSeasonValues{Eligible: eligible && goalSeason.TeamComparisonEligible(), Reason: reason}
				if eligible && !goalSeason.TeamComparisonEligible() {
					value.Reason = "Season is not eligible: " + strings.Join(goalSeason.Exclusions, ", ")
				}
				actual := float64(gt.GoalsFor) / n
				expected := gt.XGFor
				switch measure {
				case TeamSeasonAgainst:
					actual, expected = float64(gt.GoalsAgainst)/n, gt.XGAgainst
				case TeamSeasonDifference:
					actual = float64(gt.GoalsFor-gt.GoalsAgainst) / n
					expected = nil
					if gt.XGFor != nil && gt.XGAgainst != nil {
						diff := *gt.XGFor - *gt.XGAgainst
						expected = &diff
					}
				}
				if gt.GoalsCovered == team.Played {
					value.Actual = &actual
				}
				if expected != nil {
					rate := *expected / n
					value.Expected = &rate
				}
				row.Values[measure] = withTeamSeasonGap(value)
			}
			rows = append(rows, row)
		}
	}
	// Stable tie order is shared by all twelve rankings and the app's table.
	sort.SliceStable(rows, func(i, j int) bool { return TeamSeasonLess(rows[i], rows[j]) })
	for measure := TeamSeasonFor; measure <= TeamSeasonPoints; measure++ {
		for by := TeamSeasonActual; by <= TeamSeasonGap; by++ {
			indices := make([]int, 0, len(rows))
			for i, row := range rows {
				v := row.Values[measure]
				if v.Eligible && v.Value(by) != nil {
					indices = append(indices, i)
				}
			}
			sort.SliceStable(indices, func(i, j int) bool {
				a, b := *rows[indices[i]].Values[measure].Value(by), *rows[indices[j]].Values[measure].Value(by)
				if measure == TeamSeasonAgainst {
					return a < b
				}
				return a > b
			})
			rank := 0
			var previous float64
			for i, index := range indices {
				value := *rows[index].Values[measure].Value(by)
				if i == 0 || value != previous {
					rank = i + 1
				}
				rows[index].Values[measure].Ranks[by], previous = rank, value
			}
		}
	}
	return rows
}

func withTeamSeasonGap(v TeamSeasonValues) TeamSeasonValues {
	if v.Actual != nil && v.Expected != nil {
		gap := *v.Actual - *v.Expected
		v.Gap = &gap
	}
	return v
}

// TeamSeasonLess orders tied rows newest first, then by team name and ASA ID.
func TeamSeasonLess(a, b TeamSeason) bool {
	if a.Season != b.Season {
		return a.Season > b.Season
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.TeamID < b.TeamID
}
