package history

import (
	"sort"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// HeadToHeadRecord counts official results, irrespective of expected coverage.
type HeadToHeadRecord struct {
	Played, Wins, Draws, Losses, Points int
	GoalsFor, GoalsAgainst              int64
}

// HeadToHeadMeeting preserves the season identity and official orientation.
type HeadToHeadMeeting struct {
	TeamMatch
	Season, OpponentName string
	Active               bool
	GoalsCovered         bool
}

type HeadToHeadSummary struct {
	All, Home, Away                         HeadToHeadRecord
	AllExpected, HomeExpected, AwayExpected HeadToHeadExpected
	Meetings                                []HeadToHeadMeeting
	Undated                                 bool
}

type HeadToHeadOpponent struct {
	ID, Name string
	Meetings int
}

// SummarizeHeadToHead uses all-shots season eligibility, with no active-season
// minimum beyond the first valid result. IDs never imply franchise lineage.
func SummarizeHeadToHead(inputs []cache.HistoricalSeason, teamID, opponentID string, basis ScoringBasis) (HeadToHeadSummary, error) {
	summaries, err := SummarizeScoringBasis(inputs, AllShots)
	if err != nil {
		return HeadToHeadSummary{}, err
	}
	goals := summaries
	if basis.ExcludesPenalties() {
		goals, err = SummarizeScoringBasis(inputs, basis)
		if err != nil {
			return HeadToHeadSummary{}, err
		}
	}
	return HeadToHead(summaries, goals, inputs, teamID, opponentID), nil
}

// HeadToHead reuses the scoring summaries from a coherent archive snapshot.
func HeadToHead(summaries, goals []SeasonScoring, inputs []cache.HistoricalSeason, teamID, opponentID string) HeadToHeadSummary {
	result := HeadToHeadSummary{}
	// Eligibility always follows official results. A missing penalty split
	// removes only that meeting's basis goals, never its record or points.
	basisMatches := map[string]map[string]TeamMatch{}
	nonPenalty := false
	for _, season := range goals {
		nonPenalty = season.Basis.ExcludesPenalties()
		matches := map[string]TeamMatch{}
		for _, team := range season.Teams {
			if team.TeamID == teamID {
				for _, match := range team.Matches {
					matches[match.ID] = match
				}
			}
		}
		basisMatches[season.Season] = matches
	}
	for _, season := range summaries {
		if !season.TeamComparisonEligible() {
			continue
		}
		name := opponentID
		for _, input := range inputs {
			if input.Entry.Season != season.Season {
				continue
			}
			for _, team := range input.Data.Teams {
				if team.ID == opponentID {
					name = standings.DisplayName(team)
				}
			}
		}
		for _, team := range season.Teams {
			if team.TeamID != teamID {
				continue
			}
			for _, match := range team.Matches {
				if match.OpponentID != opponentID {
					continue
				}
				basisMatch := basisMatches[season.Season][match.ID]
				covered := !nonPenalty || basisMatch.GoalsCovered
				result.Meetings = append(result.Meetings, HeadToHeadMeeting{TeamMatch: basisMatch, Season: season.Season, OpponentName: name, Active: season.Lifecycle == cache.SourceScopeActive, GoalsCovered: covered})
				result.AllExpected.add(basisMatch, covered)
				if match.Home {
					result.HomeExpected.add(basisMatch, covered)
				} else {
					result.AwayExpected.add(basisMatch, covered)
				}
				result.All.add(match)
				if match.Home {
					result.Home.add(match)
				} else {
					result.Away.add(match)
				}
				if _, err := fixtures.ParseKickoff(match.KickoffUTC); err != nil {
					result.Undated = true
				}
			}
		}
	}
	sort.Slice(result.Meetings, func(i, j int) bool {
		a, ae := fixtures.ParseKickoff(result.Meetings[i].KickoffUTC)
		b, be := fixtures.ParseKickoff(result.Meetings[j].KickoffUTC)
		if (ae == nil) != (be == nil) {
			return ae == nil
		}
		if ae == nil && !a.Equal(b) {
			return a.Before(b)
		}
		return result.Meetings[i].ID < result.Meetings[j].ID
	})
	result.AllExpected.finish(result.All.Played)
	result.HomeExpected.finish(result.Home.Played)
	result.AwayExpected.finish(result.Away.Played)
	return result
}

func (r *HeadToHeadRecord) add(m TeamMatch) {
	r.Played++
	r.Points += m.Points
	r.GoalsFor += m.ScoreFor
	r.GoalsAgainst += m.ScoreAgainst
	switch {
	case m.ScoreFor > m.ScoreAgainst:
		r.Wins++
	case m.ScoreFor == m.ScoreAgainst:
		r.Draws++
	default:
		r.Losses++
	}
}

// HeadToHeadExpected compares goals on the selected basis and official points.
// Expected totals are null unless their own counted population is complete.
// Non-penalty goals count only split-covered meetings; points count every meeting.
type HeadToHeadExpected struct {
	GoalsCovered, XGCovered, XPointsCovered int
	GoalsFor, GoalsAgainst                  int64
	XGFor, XGAgainst, XPoints               *float64
}

func (r *HeadToHeadExpected) add(m TeamMatch, covered bool) {
	if covered {
		r.GoalsCovered++
		r.GoalsFor += m.GoalsFor
		r.GoalsAgainst += m.GoalsAgainst
	}
	add := func(total **float64, value float64) {
		if *total == nil {
			*total = new(float64)
		}
		**total += value
	}
	if m.XGFor != nil && m.XGAgainst != nil {
		r.XGCovered++
		add(&r.XGFor, *m.XGFor)
		add(&r.XGAgainst, *m.XGAgainst)
	}
	if m.XPoints != nil {
		r.XPointsCovered++
		add(&r.XPoints, *m.XPoints)
	}
}

func (r *HeadToHeadExpected) finish(played int) {
	if r.XGCovered != r.GoalsCovered {
		r.XGFor, r.XGAgainst = nil, nil
	}
	if r.XPointsCovered != played {
		r.XPoints = nil
	}
}

// HeadToHeadOpponents lists only opponents met in eligible regular seasons,
// sorted by their newest recorded name and then ID.
func HeadToHeadOpponents(inputs []cache.HistoricalSeason, teamID string) ([]HeadToHeadOpponent, error) {
	summaries, err := SummarizeScoringBasis(inputs, AllShots)
	if err != nil {
		return nil, err
	}
	return HeadToHeadOpponentCounts(summaries, inputs, teamID), nil
}

// HeadToHeadOpponentCounts accepts precomputed all-shots summaries.
func HeadToHeadOpponentCounts(summaries []SeasonScoring, inputs []cache.HistoricalSeason, teamID string) []HeadToHeadOpponent {
	counts := map[string]int{}
	for _, season := range summaries {
		if !season.TeamComparisonEligible() {
			continue
		}
		for _, team := range season.Teams {
			if team.TeamID == teamID {
				for _, match := range team.Matches {
					counts[match.OpponentID]++
				}
			}
		}
	}
	var rows []HeadToHeadOpponent
	for id, count := range counts {
		name, newest := id, ""
		for _, input := range inputs {
			for _, team := range input.Data.Teams {
				if team.ID == id && input.Entry.Season > newest {
					name, newest = standings.DisplayName(team), input.Entry.Season
				}
			}
		}
		rows = append(rows, HeadToHeadOpponent{ID: id, Name: name, Meetings: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}
