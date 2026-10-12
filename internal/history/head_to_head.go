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
}

type HeadToHeadSummary struct {
	All, Home, Away HeadToHeadRecord
	Meetings        []HeadToHeadMeeting
	Undated         bool
}

type HeadToHeadOpponent struct {
	ID, Name string
	Meetings int
}

// SummarizeHeadToHead uses all-shots season eligibility, with no active-season
// minimum beyond the first valid result. IDs never imply franchise lineage.
func SummarizeHeadToHead(inputs []cache.HistoricalSeason, teamID, opponentID string) (HeadToHeadSummary, error) {
	summaries, err := SummarizeScoringBasis(inputs, AllShots)
	if err != nil {
		return HeadToHeadSummary{}, err
	}
	return HeadToHead(summaries, inputs, teamID, opponentID), nil
}

// HeadToHead reuses the scoring summaries from a coherent archive snapshot.
func HeadToHead(summaries []SeasonScoring, inputs []cache.HistoricalSeason, teamID, opponentID string) HeadToHeadSummary {
	result := HeadToHeadSummary{}
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
				result.Meetings = append(result.Meetings, HeadToHeadMeeting{TeamMatch: match, Season: season.Season, OpponentName: name, Active: season.Lifecycle == cache.SourceScopeActive})
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
