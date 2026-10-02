package app

import (
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/history"
)

type exploreMatchRecord struct {
	Undated              bool                 `json:"undated,omitempty"`
	VenueDateUnavailable bool                 `json:"venueDateUnavailable,omitempty"`
	ID                   string               `json:"id"`
	Date                 string               `json:"date"`
	Opponent             string               `json:"opponent"`
	Venue                string               `json:"venue"`
	Score                string               `json:"score"`
	Values               [4]exploreTeamValues `json:"values"`
}

type exploreSeasonTrendRow struct {
	Number int
	exploreMatchRecord
	DisplayValues []string
}

type exploreTrendColumn struct {
	Label    string
	Measure  int
	Expected bool
}

type exploreSeasonTrendView struct {
	TrendMode, TrendWindow, TrendView, TrendCaption, TrendAverageSeries string
	TrendAverageReference                                               string
	TrendReferenceOptions                                               []exploreTrendReferenceOption
	TrendReferences                                                     []exploreTrendReferenceView
	TrendReferenceStatus                                                string
	TrendColumns                                                        []exploreTrendColumn
	TrendTeam                                                           teamNameView
	TrendRows                                                           []exploreSeasonTrendRow
	TrendMissingXG, TrendUndated, TrendVenueUnknown, TrendActive        bool
	TrendMissingXPoints                                                 bool
}

func exploreMatches(matches []history.TeamMatch, names map[string]string) []exploreMatchRecord {
	rows := make([]exploreMatchRecord, 0, len(matches))
	for _, match := range matches {
		row := exploreMatchRecord{ID: match.ID, Date: "Date unavailable", Opponent: names[match.OpponentID], Venue: "Away", Score: fmt.Sprintf("%d–%d", match.GoalsFor, match.GoalsAgainst)}
		if row.Opponent == "" {
			row.Opponent = match.OpponentID
		}
		if match.Home {
			row.Venue = "Home"
		}
		kickoff, err := fixtures.ParseKickoff(match.KickoffUTC)
		if err != nil {
			row.Undated = true
		} else if location, err := fixtures.VenueLocation(match.StadiumID, match.ID); err == nil {
			row.Date = kickoff.In(location).Format("Jan 2, 2006")
		} else {
			row.Date, row.VenueDateUnavailable = "Venue date unavailable", true
		}
		row.Values[0] = exploreTeamValues{Actual: float64(match.GoalsFor), Expected: match.XGFor}
		row.Values[1] = exploreTeamValues{Actual: float64(match.GoalsAgainst), Expected: match.XGAgainst}
		row.Values[2].Actual = float64(match.GoalsFor) - float64(match.GoalsAgainst)
		row.Values[3] = exploreTeamValues{Actual: float64(match.Points), Expected: match.XPoints}
		if match.XGFor != nil && match.XGAgainst != nil {
			difference := *match.XGFor - *match.XGAgainst
			row.Values[2].Expected = &difference
		}
		rows = append(rows, row)
	}
	return rows
}

func exploreSeasonTrend(query url.Values, teams exploreTeamsView, selected teamNameView) (exploreSeasonTrendView, error) {
	page := exploreSeasonTrendView{TrendMode: "rolling", TrendWindow: "5", TrendView: "balance", TrendAverageReference: "league", TrendTeam: selected}
	page.TrendAverageSeries = "goals"
	if query.Get("series") == "xg" {
		page.TrendAverageSeries = "xg"
	}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"trend-mode", &page.TrendMode, []string{"match", "rolling"}},
		{"trend-view", &page.TrendView, []string{"balance", "compare", "difference", "points", "relative"}},
		{"window", &page.TrendWindow, []string{"3", "5", "10"}},
		{"average-series", &page.TrendAverageSeries, []string{"goals", "xg", "points", "xpoints"}},
		{"average-reference", &page.TrendAverageReference, []string{"team", "league", "playoff", "top-four", "shield", "best"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
		}
	}
	series := query.Get("series")
	if series == "" {
		series = "both"
	}
	// Comparison views always include actual and xG. The query still retains
	// the Data preference for returning to scoring balance or another team view.
	switch page.TrendView {
	case "compare", "difference", "points":
		series = "both"
	case "relative":
		series = page.TrendAverageSeries
	}
	measures := []int{0, 1}
	if page.TrendView == "difference" {
		measures = []int{2}
	}
	if page.TrendView == "points" || series == "points" || series == "xpoints" {
		measures = []int{3}
	}
	for _, expected := range []bool{false, true} {
		if ((series == "goals" || series == "points") && expected) || ((series == "xg" || series == "xpoints") && !expected) {
			continue
		}
		labels := []string{"Goals scored", "Goals allowed", "Goal differential", "Points"}
		if expected {
			labels = []string{"xG scored", "xG allowed", "xG differential", "xPoints"}
		}
		for _, measure := range measures {
			page.TrendColumns = append(page.TrendColumns, exploreTrendColumn{Label: labels[measure], Measure: measure, Expected: expected})
		}
	}
	if page.TrendView == "compare" || page.TrendView == "relative" {
		// Keep the actual/xG pair for each panel adjacent in the fallback table.
		slices.SortStableFunc(page.TrendColumns, func(a, b exploreTrendColumn) int { return a.Measure - b.Measure })
	}
	page.TrendCaption = "Individual match values. Scores are shown from this team's perspective."
	window, _ := strconv.Atoi(page.TrendWindow)
	if page.TrendMode == "rolling" {
		page.TrendCaption = fmt.Sprintf("%s-match trailing averages, per match; matches 1–%d show individual match values. Scores are shown from this team's perspective.", page.TrendWindow, window-1)
	}
	for _, season := range teams.TeamSeasons {
		if season.Season != teams.TeamSeason {
			continue
		}
		page.TrendActive = season.Active
		page.TrendReferenceOptions = exploreTrendReferenceOptions(season)
		for _, team := range season.Teams {
			if team.ID != selected.ID {
				continue
			}
			page.TrendTeam = teamNameView{ID: team.ID, Name: team.Name, LogoURL: team.LogoURL}
			if page.TrendView == "relative" {
				page.TrendReferences = exploreTrendReferenceViews(season, team, page.TrendColumns, page.TrendAverageReference)
				page.TrendReferenceStatus = exploreTrendUnavailableReference(season, page.TrendColumns, page.TrendAverageReference)
			}
			for index, match := range team.Matches {
				row := exploreSeasonTrendRow{Number: index + 1, exploreMatchRecord: match}
				for _, column := range page.TrendColumns {
					value := match.Values[column.Measure]
					if page.TrendMode == "rolling" && index+1 >= window {
						value = exploreRollingValue(team.Matches, index, column.Measure, window)
					}
					display := exploreTrendNumber(&value.Actual)
					if column.Expected {
						display = exploreTrendNumber(value.Expected)
					}
					row.DisplayValues = append(row.DisplayValues, display)
				}
				page.TrendRows = append(page.TrendRows, row)
				page.TrendMissingXG = page.TrendMissingXG || (measures[0] != 3 && series != "goals" && match.Values[0].Expected == nil)
				page.TrendMissingXPoints = page.TrendMissingXPoints || (measures[0] == 3 && series != "points" && match.Values[3].Expected == nil)
				page.TrendUndated = page.TrendUndated || match.Undated
				page.TrendVenueUnknown = page.TrendVenueUnknown || match.VenueDateUnavailable
			}
		}
	}
	return page, nil
}

// A trailing window uses consecutive played matches, including the endpoint.
// Missing xG withholds the window instead of skipping matches or imputing zero.
func exploreRollingValue(matches []exploreMatchRecord, end, measure, window int) exploreTeamValues {
	value := exploreTeamValues{}
	if end+1 < window {
		return value
	}
	expected, covered := 0.0, true
	for _, match := range matches[end+1-window : end+1] {
		metric := match.Values[measure]
		value.Actual += metric.Actual / float64(window)
		if metric.Expected == nil {
			covered = false
		} else {
			expected += *metric.Expected / float64(window)
		}
	}
	if covered {
		value.Expected = &expected
	}
	return value
}

func exploreTrendNumber(value *float64) string {
	if value == nil {
		return "Unavailable"
	}
	if math.Abs(*value) < .005 {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", *value)
}
