package app

import (
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/history"
)

var teamSeasonsMeasures = []string{"for", "against", "difference", "points"}
var teamSeasonsRankBys = []string{"actual", "expected", "gap"}
var teamSeasonsDisplays = []string{"ranking", "scatter"}
var teamSeasonsSortKeys = []string{"rank", "team", "season", "played", "actual", "expected", "gap"}
var teamSeasonsMeasureLabels = []string{"Goals scored", "Goals allowed", "Goal differential", "Points"}

type exploreTeamSeasonRecord struct {
	history.TeamSeason
	LogoURL string `json:"logo"`
}

type exploreTeamSeasonsPayload struct {
	All        []exploreTeamSeasonRecord `json:"all"`
	NonPenalty []exploreTeamSeasonRecord `json:"nonPenalty"`
}

type exploreTeamSeasonRow struct {
	exploreTeamSeasonRecord
	Rank                  int
	Actual, Expected, Gap *float64
	Reason                string
}

type exploreTeamSeasonBar struct {
	exploreTeamSeasonRow
	Companion                                                *float64
	Left, Width, Marker                                      string
	Negative, Zero, ShowCompanion                            bool
	DisplayValue, CompanionLabel, MissingLabel, ZeroPosition string
}

type exploreTeamSeasonsView struct {
	TeamSeasonsDisplay                                                          string
	TeamSeasonsScatterDescription, TeamSeasonsScatterMeaning                    string
	TeamSeasonsData                                                             exploreTeamSeasonsPayload
	TeamSeasonsRows                                                             []exploreTeamSeasonRow
	TeamSeasonsBars                                                             []exploreTeamSeasonBar
	TeamSeasonsColumns                                                          []exploreTeamColumn
	TeamSeasonsMeasure, TeamSeasonsRankBy, TeamSeasonsSort, TeamSeasonsOrder    string
	TeamSeasonsGoals, TeamSeasonsNonPenalty, TeamSeasonsPointsNote              bool
	TeamSeasonsMore, TeamSeasonsSplit                                           int
	TeamSeasonsExpanded                                                         bool
	TeamSeasonsMissing, TeamSeasonsHeading, TeamSeasonsHigh, TeamSeasonsLow     string
	TeamSeasonsActualLabel, TeamSeasonsExpectedLabel, TeamSeasonsCompanionLabel string
	TeamSeasonsScaleMin, TeamSeasonsScaleMax                                    float64
	TeamSeasonsZero                                                             string
}

func exploreTeamSeasons(query url.Values, all, np []history.TeamSeason) (exploreTeamSeasonsView, error) {
	page := exploreTeamSeasonsView{TeamSeasonsDisplay: "ranking", TeamSeasonsMeasure: "points", TeamSeasonsRankBy: "actual", TeamSeasonsSort: "rank", TeamSeasonsOrder: "asc"}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"team-seasons-measure", &page.TeamSeasonsMeasure, teamSeasonsMeasures},
		{"team-seasons-display", &page.TeamSeasonsDisplay, teamSeasonsDisplays},
		{"team-seasons-rank-by", &page.TeamSeasonsRankBy, teamSeasonsRankBys},
		{"team-seasons-sort", &page.TeamSeasonsSort, teamSeasonsSortKeys},
		{"team-seasons-order", &page.TeamSeasonsOrder, []string{"asc", "desc"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
		}
	}
	basis, err := penaltiesBasis(query)
	if err != nil {
		return page, err
	}
	page.TeamSeasonsGoals = page.TeamSeasonsMeasure != "points"
	page.TeamSeasonsNonPenalty = page.TeamSeasonsGoals && basis.ExcludesPenalties()
	page.TeamSeasonsPointsNote = !page.TeamSeasonsGoals && basis.ExcludesPenalties()
	page.TeamSeasonsExpanded = query.Has("team-seasons-sort") || query.Has("team-seasons-order")
	record := func(rows []history.TeamSeason) []exploreTeamSeasonRecord {
		data := make([]exploreTeamSeasonRecord, 0, len(rows))
		for _, row := range rows {
			data = append(data, exploreTeamSeasonRecord{TeamSeason: row, LogoURL: clubLogoURL(row.TeamID)})
		}
		return data
	}
	page.TeamSeasonsData = exploreTeamSeasonsPayload{All: record(all), NonPenalty: record(np)}
	data := page.TeamSeasonsData.All
	labelBasis := history.AllShots
	if page.TeamSeasonsNonPenalty {
		data = page.TeamSeasonsData.NonPenalty
		labelBasis = history.NonPenalty
	}
	measure := history.TeamSeasonMeasure(slices.Index(teamSeasonsMeasures, page.TeamSeasonsMeasure))
	by := history.TeamSeasonRankBy(slices.Index(teamSeasonsRankBys, page.TeamSeasonsRankBy))
	if page.TeamSeasonsDisplay == "scatter" {
		by = history.TeamSeasonActual
	}
	missing := 0
	for _, record := range data {
		v := record.Values[measure]
		row := exploreTeamSeasonRow{exploreTeamSeasonRecord: record, Rank: v.Ranks[by], Actual: v.Actual, Expected: v.Expected, Gap: v.Gap}
		if row.Rank == 0 {
			row.Reason = v.Reason
			if row.Reason == "" {
				row.Reason = teamSeasonsMissingLabel(measure, labelBasis)
			}
		}
		if !v.Eligible {
			continue
		}
		page.TeamSeasonsRows = append(page.TeamSeasonsRows, row)
		if row.Rank == 0 || page.TeamSeasonsDisplay == "scatter" && row.Expected == nil {
			missing++
		}
	}
	missingLabel := teamSeasonsMissingLabel(measure, labelBasis)
	if missing > 0 {
		page.TeamSeasonsMissing = fmt.Sprintf("%d team-seasons have %s and are not ranked. Their actual values appear last in the table.", missing, missingLabel)
		if page.TeamSeasonsDisplay == "scatter" {
			page.TeamSeasonsMissing = fmt.Sprintf("%d team-seasons have %s and cannot be plotted. Their actual values remain in the table.", missing, missingLabel)
		}
	}
	ranked := append([]exploreTeamSeasonRow(nil), page.TeamSeasonsRows...)
	sortExploreTeamSeasons(ranked, "rank", "asc")
	ranked = slices.DeleteFunc(ranked, func(row exploreTeamSeasonRow) bool { return row.Rank == 0 })
	page.TeamSeasonsMore = max(0, len(ranked)-20)
	page.TeamSeasonsActualLabel = basisText(labelBasis, teamSeasonsMeasureLabels[measure])
	page.TeamSeasonsExpectedLabel = basisText(labelBasis, []string{"xG", "xG allowed", "xGD", "xPoints"}[measure])
	page.TeamSeasonsCompanionLabel = page.TeamSeasonsExpectedLabel
	if by == history.TeamSeasonExpected {
		page.TeamSeasonsCompanionLabel = page.TeamSeasonsActualLabel
	}
	headingMeasure := page.TeamSeasonsActualLabel
	switch by {
	case history.TeamSeasonExpected:
		headingMeasure = "Expected " + strings.ToLower(headingMeasure)
	case history.TeamSeasonGap:
		headingMeasure += " compared with expected,"
	}
	page.TeamSeasonsHeading = headingMeasure + " per match, since 2016"
	if page.TeamSeasonsDisplay == "scatter" {
		page.TeamSeasonsHeading = page.TeamSeasonsActualLabel + " vs expected per match, since 2016"
	}
	page.TeamSeasonsScatterDescription = fmt.Sprintf("Eligible regular-season team-seasons since 2016. One dot per team-season with complete expected data. Horizontal axis: %s per match (expected). Vertical axis: %s per match (actual). Dashed diagonal: actual matches expected. Hollow dots are in-progress seasons.", page.TeamSeasonsExpectedLabel, page.TeamSeasonsActualLabel)
	more, fewer := "more", "fewer"
	if measure == history.TeamSeasonDifference {
		more, fewer = "higher", "lower"
	}
	page.TeamSeasonsScatterMeaning = fmt.Sprintf("One dot per team-season; hollow dots are in progress. Dashed diagonal: actual matches expected. Above: %s %s than %s; below: %s.", more, strings.ToLower(page.TeamSeasonsActualLabel), page.TeamSeasonsExpectedLabel, fewer)
	page.TeamSeasonsHigh, page.TeamSeasonsLow = teamSeasonsEnds(measure, by, labelBasis)
	extent := 0.0
	for _, row := range ranked {
		v := row.Values[measure]
		extent = math.Max(extent, math.Abs(*v.Value(by)))
		if by != history.TeamSeasonGap {
			for _, value := range []*float64{v.Actual, v.Expected} {
				if value != nil {
					extent = math.Max(extent, math.Abs(*value))
				}
			}
		}
	}
	page.TeamSeasonsScaleMax = teamSeasonsExtent(extent, measure == history.TeamSeasonPoints && by != history.TeamSeasonGap)
	if by == history.TeamSeasonGap || measure == history.TeamSeasonDifference {
		page.TeamSeasonsScaleMin = -page.TeamSeasonsScaleMax
	}
	position := func(v float64) float64 {
		return (v - page.TeamSeasonsScaleMin) / (page.TeamSeasonsScaleMax - page.TeamSeasonsScaleMin) * 100
	}
	zero := position(0)
	page.TeamSeasonsZero = fmt.Sprintf("%.4f%%", zero)
	for index, row := range ranked {
		if len(ranked) > 20 && index >= 10 && index < len(ranked)-10 {
			continue
		}
		value := *row.Values[measure].Value(by)
		bar := exploreTeamSeasonBar{exploreTeamSeasonRow: row, Negative: value < 0, Zero: value == 0, ShowCompanion: by != history.TeamSeasonGap}
		p := position(value)
		bar.Left, bar.Width = fmt.Sprintf("%.4f%%", math.Min(p, zero)), fmt.Sprintf("%.4f%%", math.Abs(p-zero))
		if by != history.TeamSeasonGap {
			bar.Companion = row.Expected
			if by == history.TeamSeasonExpected {
				bar.Companion = row.Actual
			}
			if bar.Companion != nil {
				bar.Marker = fmt.Sprintf("%.4f%%", position(*bar.Companion))
			}
		}
		bar.DisplayValue = page.TeamSeasonsBarValue(value)
		bar.CompanionLabel, bar.MissingLabel, bar.ZeroPosition = page.TeamSeasonsCompanionLabel, missingLabel, page.TeamSeasonsZero
		page.TeamSeasonsBars = append(page.TeamSeasonsBars, bar)
	}
	page.TeamSeasonsSplit = min(10, (len(page.TeamSeasonsBars)+1)/2)
	sortExploreTeamSeasons(page.TeamSeasonsRows, page.TeamSeasonsSort, page.TeamSeasonsOrder)
	for _, column := range []exploreTeamColumn{
		{Key: "rank", Label: "Rank"}, {Key: "team", Label: "Team"}, {Key: "season", Label: "Season"}, {Key: "played", Label: "Matches"},
		{Key: "actual", Label: "Actual"}, {Key: "expected", Label: "Expected"}, {Key: "gap", Label: "Gap"},
	} {
		column.Description = column.Label
		column.Sort = "none"
		order := "asc"
		if column.Key == page.TeamSeasonsSort {
			column.Sort, order = "ascending", "desc"
			if page.TeamSeasonsOrder == "desc" {
				column.Sort, order = "descending", "asc"
			}
		}
		column.URL = teamSeasonsURL(query, map[string]string{"view": "team-seasons", "team-seasons-sort": column.Key, "team-seasons-order": order})
		page.TeamSeasonsColumns = append(page.TeamSeasonsColumns, column)
	}
	return page, nil
}

func teamSeasonsMissingLabel(measure history.TeamSeasonMeasure, basis history.ScoringBasis) string {
	if measure == history.TeamSeasonPoints {
		return "xPoints incomplete"
	}
	return basisText(basis, "xG incomplete")
}

func teamSeasonsEnds(measure history.TeamSeasonMeasure, by history.TeamSeasonRankBy, basis history.ScoringBasis) (string, string) {
	high := []string{"Most goals scored per match", "Fewest goals allowed per match", "Highest goal differential per match", "Highest points per match"}[measure]
	low := []string{"Fewest goals scored per match", "Most goals allowed per match", "Lowest goal differential per match", "Lowest points per match"}[measure]
	if by == history.TeamSeasonExpected {
		high = "Expected: " + high
		low = "Expected: " + low
	}
	if by == history.TeamSeasonGap {
		high, low = "Most above expected", "Most below expected"
		if measure == history.TeamSeasonAgainst {
			high, low = "Fewest goals allowed relative to expected", "Most goals allowed relative to expected"
		}
	}
	// Lowercase 'goals' also needs a basis label outside count phrases.
	if basis.ExcludesPenalties() {
		high = strings.ReplaceAll(high, "goals", "non-penalty goals")
		low = strings.ReplaceAll(low, "goals", "non-penalty goals")
	}
	return basisText(basis, high), basisText(basis, low)
}

func teamSeasonsURL(query url.Values, changes map[string]string) string {
	values := url.Values{}
	for key, value := range query {
		values[key] = value
	}
	for key, value := range changes {
		values.Set(key, value)
	}
	if values.Get("team-seasons-measure") == "" || values.Get("team-seasons-measure") == "points" || values.Get("penalties") == "include" {
		values.Del("penalties")
	}
	return exploreTeamURL(values, nil)
}

// teamSeasonsExtent matches rankingDomain's rounded chart bounds in the browser.
func teamSeasonsExtent(extent float64, points bool) float64 {
	if points {
		return 3
	}
	extent = math.Max(.5, extent*1.05)
	unit := math.Pow(10, math.Floor(math.Log10(extent)))
	for _, step := range []float64{1, 2, 2.5, 3, 4, 5, 6, 8, 10} {
		if step*unit >= extent {
			return math.Round(step*unit*100) / 100
		}
	}
	return 10 * unit
}

func (r exploreTeamSeasonRecord) Team() teamNameView {
	return teamNameView{ID: r.TeamID, Name: r.Name, LogoURL: r.LogoURL}
}
func (exploreTeamSeasonsView) TeamSeasonsValue(value *float64, missing string) string {
	if value == nil {
		return missing
	}
	return fmt.Sprintf("%.2f", *value)
}
func (exploreTeamSeasonsView) TeamSeasonsSigned(value *float64, missing string) string {
	if value == nil {
		return missing
	}
	v := *value
	if math.Abs(v) < .005 {
		v = 0
	}
	if v > 0 {
		return fmt.Sprintf("+%.2f", v)
	}
	return fmt.Sprintf("%.2f", v)
}
func (p exploreTeamSeasonsView) TeamSeasonsBarValue(value float64) string {
	if p.TeamSeasonsRankBy == "gap" {
		return p.TeamSeasonsSigned(&value, "")
	}
	return p.TeamSeasonsValue(&value, "")
}
func (p exploreTeamSeasonsView) TeamSeasonsMissingLabel() string {
	measure := history.TeamSeasonMeasure(slices.Index(teamSeasonsMeasures, p.TeamSeasonsMeasure))
	basis := history.AllShots
	if p.TeamSeasonsNonPenalty {
		basis = history.NonPenalty
	}
	return teamSeasonsMissingLabel(measure, basis)
}

func sortExploreTeamSeasons(rows []exploreTeamSeasonRow, column, order string) {
	value := func(row exploreTeamSeasonRow) *float64 {
		switch column {
		case "rank":
			v := float64(row.Rank)
			return &v
		case "played":
			v := float64(row.Played)
			return &v
		case "actual":
			return row.Actual
		case "expected":
			return row.Expected
		default:
			return row.Gap
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.Rank == 0) != (b.Rank == 0) {
			return a.Rank != 0
		}
		if column == "team" || column == "season" {
			x, y := a.Name, b.Name
			if column == "season" {
				x, y = a.Season, b.Season
			}
			if x != y {
				if order == "asc" {
					return x < y
				}
				return x > y
			}
		} else {
			x, y := value(a), value(b)
			if (x == nil) != (y == nil) {
				return x != nil
			}
			if x != nil && *x != *y {
				if order == "asc" {
					return *x < *y
				}
				return *x > *y
			}
		}
		return history.TeamSeasonLess(a.TeamSeason, b.TeamSeason)
	})
}

func (p exploreTeamSeasonsView) TeamSeasonsTop() []exploreTeamSeasonBar {
	return p.TeamSeasonsBars[:p.TeamSeasonsSplit]
}
func (p exploreTeamSeasonsView) TeamSeasonsBottom() []exploreTeamSeasonBar {
	return p.TeamSeasonsBars[p.TeamSeasonsSplit:]
}
func (b exploreTeamSeasonBar) CompanionValue() string {
	if b.Companion == nil {
		return b.MissingLabel
	}
	return fmt.Sprintf("%s %.2f", b.CompanionLabel, *b.Companion)
}

// Team-seasons' chart card controls submit with the form above them. Rank by
// is unused by the scatter, and penalties by Points.
func (p exploreTeamSeasonsView) TeamSeasonsDisplayControl() exploreSegmentGroup {
	return exploreSegmentGroup{Legend: "Chart", Name: "team-seasons-display", Form: "team-seasons-controls", Picker: true, Options: exploreSegments(p.TeamSeasonsDisplay,
		exploreSegment{Value: "ranking", Label: "Ranking"}, exploreSegment{Value: "scatter", Label: "Scatter"})}
}

func (p exploreTeamSeasonsView) TeamSeasonsMeasureControl() exploreSegmentGroup {
	return exploreSegmentGroup{Legend: "Measure", Name: "team-seasons-measure", Form: "team-seasons-controls", Options: exploreSegments(p.TeamSeasonsMeasure,
		exploreSegment{Value: "points", Label: "Points"}, exploreSegment{Value: "for", Label: "Goals scored"},
		exploreSegment{Value: "against", Label: "Goals allowed"}, exploreSegment{Value: "difference", Label: "Goal differential"})}
}

func (p exploreTeamSeasonsView) TeamSeasonsRankByControl() exploreSegmentGroup {
	return exploreSegmentGroup{Legend: "Rank by", Name: "team-seasons-rank-by", Form: "team-seasons-controls", Hidden: p.TeamSeasonsDisplay == "scatter", Options: exploreSegments(p.TeamSeasonsRankBy,
		exploreSegment{Value: "actual", Label: "Actual"}, exploreSegment{Value: "expected", Label: "Expected"}, exploreSegment{Value: "gap", Label: "Gap"})}
}
