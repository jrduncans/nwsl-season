package app

import (
	"fmt"
	"net/url"
	"slices"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/history"
)

// portlandThornsTeamID is the ASA ID of Portland Thorns FC, the site owner's
// team and the default Team profile selection.
const portlandThornsTeamID = "Pk5LeeNqOW"

var exploreSeriesOptions = []string{"goals", "xg", "both"}

func ignoreInvalidExploreSeries(query url.Values) {
	if values, present := query["series"]; present && (len(values) != 1 || !slices.Contains(exploreSeriesOptions, values[0])) {
		query.Del("series")
	}
}

type exploreTeamHistoryRecord struct {
	exploreTeamRecord
	Season string
	Active bool
}

type exploreTeamHistoryRow struct {
	Season string
	Active bool
	Played int
	Values []exploreTeamRowValues
}

type exploreTeamHistoryView struct {
	HistoryTeam               teamNameView
	HistoryTeamOptions        []teamNameView
	TeamHistoryRows           []exploreTeamHistoryRow
	TeamHistoryMissingXG      bool
	TeamHistoryMissingXPoints bool
	HistorySort, HistoryOrder string
	// HistoryExpanded opens the values table when the URL carries its sort.
	HistoryExpanded               bool
	HistoryColumns                []exploreTeamColumn
	HistorySeries, HistoryContext string
	HistoryContextData            [4]exploreMetricContext
	HistoryContextDetails         []exploreSeriesContext
}

// Reuse the eligible team-season rates so history and season comparisons have
// identical coverage rules. Identity is ASA's team ID, never a name match.
func exploreTeamHistory(query url.Values, seasons []exploreTeamSeason) (exploreTeamHistoryView, error) {
	return exploreTeamHistoryFor(query, seasons, seasons)
}

// Identity choices follow the all-shots archive even when the selected basis
// has no eligible records for a team. Basis switches preserve the same choices.
func exploreTeamHistoryFor(query url.Values, seasons, identities []exploreTeamSeason) (exploreTeamHistoryView, error) {
	basis, err := penaltiesBasis(query)
	if err != nil {
		return exploreTeamHistoryView{}, err
	}
	page := exploreTeamHistoryView{HistorySort: "season", HistoryOrder: "desc", HistoryColumns: exploreTeamHistoryColumns(basis), HistorySeries: "both", HistoryContext: "off"}
	keys := make([]string, 0, len(page.HistoryColumns))
	for _, column := range page.HistoryColumns {
		keys = append(keys, column.Key)
	}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"history-sort", &page.HistorySort, keys},
		{"history-order", &page.HistoryOrder, []string{"asc", "desc"}},
		{"series", &page.HistorySeries, exploreSeriesOptions},
		{"context", &page.HistoryContext, []string{"on", "off"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
		}
	}
	page.HistoryExpanded = query.Has("history-sort") || query.Has("history-order")
	page.HistoryContextData = exploreHistoryContextData(seasons, basis)
	measure, _ := exploreTeamSortMetric(query.Get("measure") + "-actual")
	metric := page.HistoryContextData[measure]
	if page.HistorySeries != "xg" {
		page.HistoryContextDetails = append(page.HistoryContextDetails, metric.Goals)
	}
	if page.HistorySeries != "goals" {
		page.HistoryContextDetails = append(page.HistoryContextDetails, metric.XG)
	}
	byID := make(map[string]teamNameView)
	// Seasons arrive newest first; prefer the most recent recorded name.
	for _, season := range identities {
		for _, team := range season.Teams {
			if _, exists := byID[team.ID]; !exists {
				byID[team.ID] = teamNameView{ID: team.ID, Name: team.Name, LogoURL: team.LogoURL}
			}
		}
	}
	for _, team := range byID {
		page.HistoryTeamOptions = append(page.HistoryTeamOptions, team)
	}
	sort.Slice(page.HistoryTeamOptions, func(i, j int) bool {
		left, right := page.HistoryTeamOptions[i], page.HistoryTeamOptions[j]
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.ID < right.ID
	})
	if team, found := byID[portlandThornsTeamID]; found {
		page.HistoryTeam = team
	} else if len(page.HistoryTeamOptions) > 0 {
		page.HistoryTeam = page.HistoryTeamOptions[0]
	}
	if values, present := query["team"]; present {
		if len(values) != 1 || values[0] == "" {
			return page, fmt.Errorf("team must be one team from the recorded regular-season results")
		}
		team, found := byID[values[0]]
		if !found {
			return page, fmt.Errorf("team is not in the recorded regular-season results")
		}
		page.HistoryTeam = team
	}
	var records []exploreTeamHistoryRecord
	for _, season := range seasons {
		for _, team := range season.Teams {
			if team.ID != page.HistoryTeam.ID {
				continue
			}
			records = append(records, exploreTeamHistoryRecord{exploreTeamRecord: team, Season: season.Season, Active: season.Active})
		}
	}
	sortExploreTeamHistory(records, page.HistorySort, page.HistoryOrder)
	for _, team := range records {
		row := exploreTeamHistoryRow{Season: team.Season, Active: team.Active, Played: team.Played}
		for _, index := range []int{2, 0, 1, 3} {
			value := team.Values[index]
			cells := exploreTeamRowValues{Actual: fmt.Sprintf("%.2f", value.Actual), Expected: "Unavailable"}
			if value.Expected != nil {
				cells.Expected = fmt.Sprintf("%.2f", *value.Expected)
			} else {
				if index == 3 {
					page.TeamHistoryMissingXPoints = true
				} else {
					page.TeamHistoryMissingXG = true
				}
			}
			row.Values = append(row.Values, cells)
		}
		page.TeamHistoryRows = append(page.TeamHistoryRows, row)
	}
	for i := range page.HistoryColumns {
		column := &page.HistoryColumns[i]
		column.Sort = "none"
		order := "desc"
		if column.Key == page.HistorySort {
			column.Sort, order = "descending", "asc"
			if page.HistoryOrder == "asc" {
				column.Sort, order = "ascending", "desc"
			}
		}
		column.URL = exploreTeamURL(query, map[string]string{
			"view": "team-history", "team": page.HistoryTeam.ID,
			"history-sort": column.Key, "history-order": order,
		})
	}
	return page, nil
}

func exploreTeamHistoryColumns(basis history.ScoringBasis) []exploreTeamColumn {
	columns := []exploreTeamColumn{{Key: "season", Label: "Season", Description: "Season", Leading: true}, {Key: "played", Label: "Played", Description: "Played", Leading: true}}
	for _, group := range []struct{ key, label string }{{"difference", "Goal differential"}, {"for", "Goals scored"}, {"against", "Goals allowed"}, {"points", "Points"}} {
		for _, value := range []struct{ key, label string }{{"actual", "Goals"}, {"expected", "xG"}} {
			if group.key == "points" {
				if value.key == "actual" {
					value.label = "Points"
				} else {
					value.label = "xPoints"
				}
			}
			columns = append(columns, basisColumn(basis, group.key+"-"+value.key, value.label, group.label+": "+value.label))
		}
	}
	return columns
}

func sortExploreTeamHistory(rows []exploreTeamHistoryRecord, column, order string) {
	measure, valueColumn := exploreTeamSortMetric(column)
	value := func(row exploreTeamHistoryRecord) *float64 {
		metric := row.Values[measure]
		number := metric.Actual
		if valueColumn == "expected" {
			return metric.Expected
		}
		if column == "played" {
			number = float64(row.Played)
		}
		return &number
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if column == "season" {
			if order == "asc" {
				return rows[i].Season < rows[j].Season
			}
			return rows[i].Season > rows[j].Season
		}
		a, b := value(rows[i]), value(rows[j])
		if a == nil || b == nil {
			if (a == nil) != (b == nil) {
				return b == nil
			}
		} else if *a != *b {
			if order == "asc" {
				return *a < *b
			}
			return *a > *b
		}
		return rows[i].Season > rows[j].Season
	})
}

// exploreHistoryContextData is Season by season's league context, labeled
// for basis.
func exploreHistoryContextData(seasons []exploreTeamSeason, basis history.ScoringBasis) [4]exploreMetricContext {
	data := exploreTeamContext(seasons)
	for index := range data {
		data[index].Goals.Label = basisText(basis, data[index].Goals.Label)
		data[index].XG.Label = basisText(basis, data[index].XG.Label)
	}
	return data
}

// HistoryMeasureControl and HistorySeriesControl sit in the Season by season
// chart card and submit with its form. Show names the measure's own series.
func (p explorePage) HistoryMeasureControl() exploreSegmentGroup {
	return exploreSegmentGroup{Legend: "Compare", Name: "measure", Form: "team-history-controls", Options: exploreMeasureSegments(p.TeamMeasure)}
}

func (p explorePage) HistorySeriesControl() exploreSegmentGroup {
	actual, expected := "Goals", "xG"
	if p.TeamMeasure == "points" {
		actual, expected = "Points", "xPoints"
	}
	return exploreSegmentGroup{Legend: "Show", Name: "series", Form: "team-history-controls", Options: exploreSegments(p.HistorySeries,
		exploreSegment{Value: "both", Label: actual + " and " + expected}, exploreSegment{Value: "goals", Label: actual}, exploreSegment{Value: "xg", Label: expected})}
}
