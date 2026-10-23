package app

import (
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/history"
)

// Match measures retain the established payload order across Explore views.
const (
	exploreScored = iota
	exploreAllowed
	exploreMargin
	explorePoints
)

type exploreMatchRecord struct {
	GoalsCovered         bool   `json:"goalsCovered"`
	Undated              bool   `json:"undated,omitempty"`
	VenueDateUnavailable bool   `json:"venueDateUnavailable,omitempty"`
	ID                   string `json:"id"`
	Date                 string `json:"date"`
	Opponent             string `json:"opponent"`
	// OpponentID finds the opponent's logo among the season's team records,
	// which already carry each logo URL once.
	OpponentID string               `json:"opponentId"`
	Venue      string               `json:"venue"`
	Score      string               `json:"score"`
	Values     [4]exploreTeamValues `json:"values"`
	// Opponents holds Match by match's opponent-adjustment factors.
	Opponents *exploreOpponentFactors `json:"opponents,omitempty"`
}

type exploreSeasonTrendRow struct {
	Number int
	exploreMatchRecord
	// AdjustedMatch is the opponent-adjusted match, for sorting.
	AdjustedMatch exploreMatchRecord
	DisplayValues []string
}

type exploreTrendColumn struct {
	Label          string
	Measure        int
	Expected       bool
	Gap            bool
	Rolling        bool
	Adjusted       bool
	Field          string
	URL, Sort, Key string
}

type exploreSeasonTrendView struct {
	TrendMode, TrendWindow, TrendView, TrendCaption, TrendAverageSeries string
	// TrendSmoothing is the single Smoothing selection: none, 3, 5 or 10.
	// TrendLegacySmoothing marks an old trend-mode/window URL to redirect.
	TrendSmoothing                                                       string
	TrendLegacySmoothing                                                 bool
	TrendAverageReference                                                string
	TrendReferenceOptions                                                []exploreTrendReferenceOption
	TrendReferenceStatus                                                 string
	TrendColumns                                                         []exploreTrendColumn
	TrendTableColumns                                                    []exploreTrendColumn
	TrendExpanded                                                        bool
	TrendTeam                                                            teamNameView
	TrendRows                                                            []exploreSeasonTrendRow
	TrendMissingXG, TrendUndated, TrendVenueDateUnavailable, TrendActive bool
	TrendMissingXPoints                                                  bool
	TrendNote                                                            string
	TrendPointsGap                                                       bool
	TrendGoalGaps                                                        bool
	TrendBalanceBars                                                     bool
	TrendGoalGapTiles                                                    []exploreGoalGapTile
	// TrendMargins uses difference, the legacy URL value of Margins.
	TrendMargins          bool
	TrendMarginTiles      []exploreMarginTileView
	TrendPointsTiles      []explorePointsTile
	TrendSort, TrendOrder string
	// TrendOpponentsShown shows Adjust for opponents; TrendOpponents is its
	// checked state, kept while hidden; TrendAdjusted applies it.
	TrendOpponentsShown, TrendOpponents, TrendAdjusted bool
}

// exploreSegment is one choice in a segmented control. Description, when set,
// is the accessible name for a terse Label.
type exploreSegment struct {
	Value, Label, Description string
	Selected                  bool
}

// exploreSegmentGroup is a labeled segmented control, rendered by the
// "segmented" template. Its radios submit with Form when set, so a control
// in a chart card can belong to the view's form above it. A Picker chooses
// the chart and sits at the top of its card with a visually hidden legend.
// Hidden renders the group hidden for a selection that does not use it.
// Inline puts the label beside the options, as in a row of labeled selects.
type exploreSegmentGroup struct {
	Legend, Name, Form     string
	Picker, Hidden, Inline bool
	Options                []exploreSegment
}

// exploreSegments marks the option whose Value is selected.
func exploreSegments(selected string, options ...exploreSegment) []exploreSegment {
	for i := range options {
		options[i].Selected = options[i].Value == selected
	}
	return options
}

// exploreMeasureSegments are the team measures that Compare teams and Season
// by season compare.
func exploreMeasureSegments(selected string) []exploreSegment {
	return exploreSegments(selected, exploreSegment{Value: "difference", Label: "Goal differential"}, exploreSegment{Value: "for", Label: "Goals scored"},
		exploreSegment{Value: "against", Label: "Goals allowed"}, exploreSegment{Value: "points", Label: "Points"})
}

// exploreUnitsSegments switch team values between per-match rates and totals.
func exploreUnitsSegments(selected string) []exploreSegment {
	return exploreSegments(selected, exploreSegment{Value: "per-match", Label: "Per match"}, exploreSegment{Value: "total", Label: "Totals"})
}

// TrendViewOptions lists the Chart segments in display order.
func (page exploreSeasonTrendView) TrendViewOptions() []exploreSegment {
	return exploreSegments(page.TrendView, exploreSegment{Value: "balance", Label: "Scoring balance"}, exploreSegment{Value: "compare", Label: "Goals vs xG"}, exploreSegment{Value: "difference", Label: "Margins"}, exploreSegment{Value: "points", Label: "Points vs xPoints"}, exploreSegment{Value: "relative", Label: "Benchmarks"})
}

// TrendAverageSeriesOptions lists the Benchmarks Show segments.
func (page exploreSeasonTrendView) TrendAverageSeriesOptions() []exploreSegment {
	return exploreSegments(page.TrendAverageSeries, exploreSegment{Value: "goals", Label: "Goals"}, exploreSegment{Value: "xg", Label: "xG"}, exploreSegment{Value: "points", Label: "Points"}, exploreSegment{Value: "xpoints", Label: "xPoints"})
}

// TrendSmoothingOptions lists the Smoothing segments; the window sizes are
// labeled by number and named in full for assistive technology.
func (page exploreSeasonTrendView) TrendSmoothingOptions() []exploreSegment {
	return exploreSegments(page.TrendSmoothing, exploreSegment{Value: "none", Label: "None"}, exploreSegment{Value: "3", Label: "3", Description: "3-match average"}, exploreSegment{Value: "5", Label: "5", Description: "5-match average"}, exploreSegment{Value: "10", Label: "10", Description: "10-match average"})
}

func exploreMatches(matches []history.TeamMatch, names map[string]string) []exploreMatchRecord {
	rows := make([]exploreMatchRecord, 0, len(matches))
	for _, match := range matches {
		row := exploreMatchRecord{GoalsCovered: match.GoalsCovered, ID: match.ID, Date: "Date unavailable", Opponent: names[match.OpponentID], OpponentID: match.OpponentID, Venue: "Away", Score: fmt.Sprintf("%d–%d", match.ScoreFor, match.ScoreAgainst)}
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
		row.Values[exploreScored] = exploreTeamValues{Actual: float64(match.GoalsFor), Expected: match.XGFor}
		row.Values[exploreAllowed] = exploreTeamValues{Actual: float64(match.GoalsAgainst), Expected: match.XGAgainst}
		row.Values[exploreMargin].Actual = float64(match.GoalsFor) - float64(match.GoalsAgainst)
		row.Values[explorePoints] = exploreTeamValues{Actual: float64(match.Points), Expected: match.XPoints}
		if match.XGFor != nil && match.XGAgainst != nil {
			difference := *match.XGFor - *match.XGAgainst
			row.Values[exploreMargin].Expected = &difference
		}
		rows = append(rows, row)
	}
	return rows
}

func exploreSeasonTrend(query url.Values, teams exploreTeamsView, selected teamNameView) (exploreSeasonTrendView, error) {
	basis, err := penaltiesBasis(query)
	if err != nil {
		return exploreSeasonTrendView{}, err
	}
	page := exploreSeasonTrendView{TrendMode: "rolling", TrendWindow: "5", TrendSmoothing: "5", TrendView: "balance", TrendAverageReference: "league", TrendTeam: selected, TrendSort: "match", TrendOrder: "asc"}
	page.TrendAverageSeries = "goals"
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"trend-view", &page.TrendView, []string{"balance", "compare", "difference", "points", "relative"}},
		{"trend-sort", &page.TrendSort, []string{"match", "venue", "result", "goal-margin", "xg-margin", "points-gap", "scored-gap", "allowed-gap", "adjusted-goals-for", "adjusted-goals-against", "adjusted-xg-for", "adjusted-xg-against", "adjusted-points", "adjusted-xpoints"}},
		{"trend-order", &page.TrendOrder, []string{"asc", "desc"}},
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
	if err := parseTrendSmoothing(query, &page); err != nil {
		return page, err
	}
	if page.TrendOpponents, err = opponentsSelection(query); err != nil {
		return page, err
	}
	var coverage *exploreOpponentCoverage
	for _, season := range teams.TeamSeasons {
		if season.Season == teams.TeamSeason {
			coverage = season.Opponents
		}
	}
	// Every view's payload carries Match by match, so apply its rule whatever
	// view the request names.
	trendView, _ := exploreViewFor("season-trend")
	page.TrendOpponentsShown = coverage != nil && trendView.OpponentAdjustment.matches(query, trendView.Defaults)
	page.TrendAdjusted = page.TrendOpponentsShown && page.TrendOpponents
	// Scoring balance always includes actual and xG. The shared series query is
	// retained for Season by season, but has no effect on Match by match.
	series := "both"
	if page.TrendView == "relative" {
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
		labels := []string{"Goals scored", "Goals allowed", "Goal margin", "Points"}
		if expected {
			labels = []string{"xG scored", "xG allowed", "xG margin", "xPoints"}
		}
		for _, measure := range measures {
			column := exploreTrendColumn{Label: basisText(basis, labels[measure]), Measure: measure, Expected: expected}
			if page.TrendAdjusted {
				column.Label, column.Adjusted = adjustedLabel(column.Label), true
			}
			page.TrendColumns = append(page.TrendColumns, column)
		}
	}
	if page.TrendView == "compare" || page.TrendView == "relative" {
		// Keep the actual/xG pair for each panel adjacent in the fallback table.
		slices.SortStableFunc(page.TrendColumns, func(a, b exploreTrendColumn) int { return a.Measure - b.Measure })
	}
	page.TrendPointsGap = page.TrendView == "points" && page.TrendMode == "match"
	page.TrendMargins = page.TrendView == "difference" && page.TrendMode == "match"
	page.TrendBalanceBars = page.TrendView == "balance" && page.TrendMode == "match"
	page.TrendGoalGaps = page.TrendView == "compare" && page.TrendMode == "match"
	page.TrendTableColumns = exploreTrendTableColumns(page, basis)
	if !slices.ContainsFunc(page.TrendTableColumns, func(column exploreTrendColumn) bool { return column.Key == page.TrendSort }) {
		page.TrendSort, page.TrendOrder = "match", "asc"
	}
	page.TrendExpanded = query.Has("trend-sort") || query.Has("trend-order")
	page.TrendTableColumns = exploreTrendSortLinks(page.TrendTableColumns, query, page.TrendSort, page.TrendOrder)
	page.TrendCaption = "Individual match values. Scores are shown from this team's perspective."
	window, _ := strconv.Atoi(page.TrendWindow)
	if page.TrendMode == "rolling" {
		page.TrendCaption = fmt.Sprintf("Individual match values, followed by %s-match trailing averages, per match; matches 1–%d use all matches played so far. Scores are shown from this team's perspective.", page.TrendWindow, window-1)
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
			if page.TrendGoalGaps {
				page.TrendGoalGapTiles = exploreGoalGapTiles(team.Matches, basis, "match")
			}
			adjusted := team.Matches
			if page.TrendAdjusted {
				adjusted = opponentAdjustedMatches(team.Matches, coverage.AveragePoints)
			}
			if page.TrendMargins || page.TrendBalanceBars {
				tiles := exploreMarginTiles(team.Matches)
				if page.TrendAdjusted {
					tiles = exploreAdjustedMarginTiles(team.Matches, adjusted)
				}
				page.TrendMarginTiles = formatMarginTiles(tiles, basis, "match")
			}
			if page.TrendPointsGap {
				page.TrendPointsTiles = explorePointsTiles(team.Matches, "match")
				if page.TrendAdjusted {
					page.TrendPointsTiles = exploreAdjustedPointsTiles(adjusted)
				}
			}
			page.TrendTeam = teamNameView{ID: team.ID, Name: team.Name, LogoURL: team.LogoURL}
			if page.TrendView == "relative" {
				page.TrendReferenceStatus = exploreTrendUnavailableReference(season, page.TrendColumns, page.TrendAverageReference)
			}
			for index, match := range team.Matches {
				row := exploreSeasonTrendRow{Number: index + 1, exploreMatchRecord: match, AdjustedMatch: adjusted[index]}
				for _, column := range page.TrendTableColumns {
					row.DisplayValues = append(row.DisplayValues, exploreTrendCell(column, team.Matches, adjusted, index, window))
				}
				page.TrendRows = append(page.TrendRows, row)
				page.TrendMissingXG = page.TrendMissingXG || (measures[0] != 3 && series != "goals" && match.Values[0].Expected == nil)
				page.TrendMissingXPoints = page.TrendMissingXPoints || (measures[0] == 3 && series != "points" && match.Values[3].Expected == nil)
				page.TrendUndated = page.TrendUndated || match.Undated
				page.TrendVenueDateUnavailable = page.TrendVenueDateUnavailable || match.VenueDateUnavailable
			}
		}
	}
	sortExploreTrendRows(page.TrendRows, page.TrendSort, page.TrendOrder)
	page.TrendNote = exploreSeasonTrendNote(page, coverage)
	return page, nil
}

// One Smoothing control covers both the mode and the rolling window. Old
// trend-mode and window links still validate; the handler redirects them to
// the equivalent smoothing value, which wins when both are present.
func parseTrendSmoothing(query url.Values, page *exploreSeasonTrendView) error {
	legacy := struct{ mode, window string }{"rolling", "5"}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"trend-mode", &legacy.mode, []string{"match", "rolling"}},
		{"window", &legacy.window, []string{"3", "5", "10"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
			page.TrendLegacySmoothing = true
		}
	}
	page.TrendSmoothing = legacy.window
	if legacy.mode == "match" {
		page.TrendSmoothing = "none"
	}
	if values, present := query["smoothing"]; present {
		if len(values) != 1 || !slices.Contains([]string{"none", "3", "5", "10"}, values[0]) {
			return fmt.Errorf("invalid smoothing selection")
		}
		page.TrendSmoothing = values[0]
	}
	page.TrendMode, page.TrendWindow = "rolling", page.TrendSmoothing
	if page.TrendSmoothing == "none" {
		page.TrendMode, page.TrendWindow = "match", "5"
	}
	return nil
}

// The note carries only what the controls, status line and panel titles do not
// already say.
func exploreSeasonTrendNote(page exploreSeasonTrendView, coverage *exploreOpponentCoverage) string {
	var parts []string
	if page.TrendMode == "rolling" {
		parts = append(parts, fmt.Sprintf("Each point averages the last %s matches, including that one.", page.TrendWindow))
	}
	if page.TrendView == "relative" {
		parts = append(parts, "Reference lines show the team’s season average and the selected benchmark.")
		if page.TrendMissingXG || page.TrendMissingXPoints {
			parts = append(parts, "Expected means use available matches.")
		}
	}
	if page.TrendView == "points" {
		parts = append(parts, "xPoints: ASA’s expected points for each match.")
	}
	if page.TrendOpponentsShown {
		parts = append(parts, opponentAdjustmentNote)
		if note := coverage.Note(); note != "" {
			parts = append(parts, note)
		}
	}
	return strings.Join(parts, " ")
}

// A trailing window uses consecutive played matches, including the endpoint.
// Before it fills, the window uses all matches played so far.
// Missing expected data withholds the window instead of skipping matches or imputing zero.
func exploreRollingValue(matches []exploreMatchRecord, end, measure, window int) exploreTeamValues {
	value := exploreTeamValues{}
	window = min(end+1, window)
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
