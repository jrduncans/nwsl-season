package app

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

type exploreTeamValues struct {
	Actual   float64  `json:"actual"`
	Expected *float64 `json:"expected"`
}

type exploreTeamRecord struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	LogoURL        string               `json:"logo"`
	Played         int                  `json:"played"`
	XGCovered      int                  `json:"xgCovered"`
	XPointsCovered int                  `json:"xPointsCovered"`
	Values         [4]exploreTeamValues `json:"values"`
	Totals         [4]exploreTeamValues `json:"totals"`
}

type exploreTeamSeason struct {
	Season string              `json:"season"`
	Active bool                `json:"active"`
	Teams  []exploreTeamRecord `json:"teams"`
}

type exploreTeamRow struct {
	Team   teamNameView
	Values []exploreTeamRowValues
	Played int
}

type exploreTeamRowValues struct {
	Actual, Expected, Gap string
}

type exploreTeamColumn struct {
	Key, Label, URL, Sort string
	Description           string
	Leading               bool
}

type exploreTeamsView struct {
	TeamSeasons                                            []exploreTeamSeason
	TeamSeason, TeamMeasure                                string
	TeamActualLabel, TeamXGLabel                           string
	TeamDisplay, TeamSort, TeamOrder, TeamUnits            string
	TeamChartURL, TeamGapURL, TeamScatterURL, TeamTableURL string
	TeamMissingXG, TeamMissingXPoints                      bool
	TeamColumns                                            []exploreTeamColumn
	TeamRows                                               []exploreTeamRow
}

func exploreTeams(query url.Values, summaries []history.SeasonScoring, archive []cache.HistoricalSeason) (exploreTeamsView, error) {
	page := exploreTeamsView{TeamMeasure: "difference", TeamDisplay: "chart", TeamSort: "difference-gap", TeamOrder: "desc", TeamUnits: "per-match"}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"display", &page.TeamDisplay, []string{"chart", "gap", "scatter", "table"}},
		{"team-sort", &page.TeamSort, []string{"name", "played", "actual", "expected", "gap", "difference-actual", "difference-expected", "difference-gap", "for-actual", "for-expected", "for-gap", "against-actual", "against-expected", "against-gap", "points-actual", "points-expected", "points-gap"}},
		{"team-order", &page.TeamOrder, []string{"asc", "desc"}},
		{"units", &page.TeamUnits, []string{"per-match", "total"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
		}
	}
	if values, present := query["measure"]; present {
		if len(values) != 1 || (values[0] != "for" && values[0] != "against" && values[0] != "difference" && values[0] != "points") {
			return page, fmt.Errorf("measure must be for, against, difference, or points")
		}
		page.TeamMeasure = values[0]
	}
	// Preserve links to the former metric-specific table. New links name their
	// metric explicitly so chart selection cannot change table ordering.
	if slices.Contains([]string{"actual", "expected", "gap"}, page.TeamSort) {
		page.TeamSort = page.TeamMeasure + "-" + page.TeamSort
	}
	names := make(map[string]map[string]string, len(archive))
	for _, season := range archive {
		names[season.Entry.Season] = make(map[string]string, len(season.Data.Teams))
		for _, team := range season.Data.Teams {
			names[season.Entry.Season][team.ID] = displayName(team)
		}
	}
	// Newest first, including unavailable seasons so an explicit selection never
	// silently substitutes another year's data.
	for i := len(summaries) - 1; i >= 0; i-- {
		summary := summaries[i]
		season := exploreTeamSeason{Season: summary.Season, Active: summary.Lifecycle == cache.SourceScopeActive}
		if summary.TeamComparisonEligible() {
			for _, team := range summary.Teams {
				row := exploreTeamRecord{ID: team.TeamID, Name: names[summary.Season][team.TeamID], LogoURL: clubLogoURL(team.TeamID), Played: team.Played, XGCovered: team.XGCovered, XPointsCovered: team.XPointsCovered}
				if row.Name == "" {
					row.Name = team.TeamID
				}
				played := float64(team.Played)
				row.Totals[0].Actual = float64(team.GoalsFor)
				row.Totals[1].Actual = float64(team.GoalsAgainst)
				row.Totals[2].Actual = float64(team.GoalsFor - team.GoalsAgainst)
				row.Totals[3].Actual = float64(team.Points)
				if team.XGFor != nil && team.XGAgainst != nil {
					difference := *team.XGFor - *team.XGAgainst
					row.Totals[0].Expected, row.Totals[1].Expected, row.Totals[2].Expected = team.XGFor, team.XGAgainst, &difference
				}
				row.Totals[3].Expected = team.XPoints
				for index, total := range row.Totals {
					row.Values[index].Actual = total.Actual / played
					if total.Expected != nil {
						expected := *total.Expected / played
						row.Values[index].Expected = &expected
					}
				}
				season.Teams = append(season.Teams, row)
			}
			if page.TeamSeason == "" {
				page.TeamSeason = summary.Season
			}
		}
		page.TeamSeasons = append(page.TeamSeasons, season)
	}
	if page.TeamSeason == "" && len(page.TeamSeasons) > 0 {
		page.TeamSeason = page.TeamSeasons[0].Season
	}
	if values, present := query["season"]; present {
		if len(values) != 1 || !historySeasonPattern.MatchString(values[0]) {
			return page, fmt.Errorf("season must be one supported four-digit year")
		}
		found := false
		for _, season := range page.TeamSeasons {
			found = found || season.Season == values[0]
		}
		if !found {
			return page, fmt.Errorf("season %s is not in the regular-season catalog", values[0])
		}
		page.TeamSeason = values[0]
	}
	page.TeamActualLabel, page.TeamXGLabel = "Goals", "xG"
	switch page.TeamMeasure {
	case "against":
		page.TeamActualLabel, page.TeamXGLabel = "Goals allowed", "xG allowed"
	case "difference":
		page.TeamActualLabel, page.TeamXGLabel = "Goal differential", "xG differential"
	case "points":
		page.TeamActualLabel, page.TeamXGLabel = "Points", "xPts"
	}
	selection := url.Values{"view": {"teams"}, "season": {page.TeamSeason}, "measure": {page.TeamMeasure}, "display": {page.TeamDisplay}, "team-sort": {page.TeamSort}, "team-order": {page.TeamOrder}, "units": {page.TeamUnits}}
	page.TeamChartURL = exploreTeamURL(selection, map[string]string{"display": "chart"})
	page.TeamGapURL = exploreTeamURL(selection, map[string]string{"display": "gap"})
	page.TeamScatterURL = exploreTeamURL(selection, map[string]string{"display": "scatter"})
	page.TeamTableURL = exploreTeamURL(selection, map[string]string{"display": "table"})
	columns := []exploreTeamColumn{{Key: "name", Label: "Team", Description: "Team", Leading: true}, {Key: "played", Label: "Played", Description: "Played", Leading: true}}
	for _, group := range []struct{ key, label string }{{"difference", "Goal differential"}, {"for", "Goals scored"}, {"against", "Goals allowed"}, {"points", "Points"}} {
		for _, value := range []struct{ key, label string }{{"actual", "Actual"}, {"expected", "xG"}, {"gap", "Gap"}} {
			if group.key == "points" && value.key == "expected" {
				value.label = "xPts"
			}
			columns = append(columns, exploreTeamColumn{Key: group.key + "-" + value.key, Label: value.label, Description: group.label + ": " + value.label})
		}
	}
	for _, column := range columns {
		column.Sort = "none"
		order := "desc"
		if column.Key == "name" {
			order = "asc"
		}
		if column.Key == page.TeamSort {
			column.Sort = "descending"
			order = "asc"
			if page.TeamOrder == "asc" {
				column.Sort, order = "ascending", "desc"
			}
		}
		column.URL = exploreTeamURL(selection, map[string]string{"display": "table", "team-sort": column.Key, "team-order": order})
		page.TeamColumns = append(page.TeamColumns, column)
	}
	for _, season := range page.TeamSeasons {
		if season.Season != page.TeamSeason {
			continue
		}
		rows := append([]exploreTeamRecord(nil), season.Teams...)
		sortExploreTeams(rows, page.TeamSort, page.TeamOrder, page.TeamUnits)
		for _, row := range rows {
			view := exploreTeamRow{Team: teamNameView{ID: row.ID, Name: row.Name, LogoURL: row.LogoURL}, Played: row.Played}
			for _, index := range []int{2, 0, 1, 3} {
				value := row.Values[index]
				if page.TeamUnits == "total" {
					value = row.Totals[index]
				}
				actual := fmt.Sprintf("%.2f", value.Actual)
				if page.TeamUnits == "total" {
					actual = fmt.Sprintf("%.0f", value.Actual)
				}
				cells := exploreTeamRowValues{Actual: actual, Expected: "Unavailable", Gap: "Unavailable"}
				if value.Expected != nil {
					cells.Expected = fmt.Sprintf("%.2f", *value.Expected)
					cells.Gap = fmt.Sprintf("%+.2f", value.Actual-*value.Expected)
				} else {
					if index == 3 {
						page.TeamMissingXPoints = true
					} else {
						page.TeamMissingXG = true
					}
				}
				view.Values = append(view.Values, cells)
			}
			page.TeamRows = append(page.TeamRows, view)
		}
	}
	return page, nil
}

func exploreTeamURL(selection url.Values, changes map[string]string) string {
	values := make(url.Values, len(selection))
	for key, value := range selection {
		values[key] = value
	}
	for key, value := range changes {
		values.Set(key, value)
	}
	return "?" + values.Encode()
}

func sortExploreTeams(rows []exploreTeamRecord, column, order string, units ...string) {
	measure, valueColumn := exploreTeamSortMetric(column)
	value := func(row exploreTeamRecord) *float64 {
		metric := row.Values[measure]
		if len(units) > 0 && units[0] == "total" {
			metric = row.Totals[measure]
		}
		number := metric.Actual
		switch valueColumn {
		case "played":
			number = float64(row.Played)
		case "expected":
			return metric.Expected
		case "gap":
			if metric.Expected == nil {
				return nil
			}
			number -= *metric.Expected
		}
		return &number
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if column == "name" && rows[i].Name != rows[j].Name {
			if order == "asc" {
				return rows[i].Name < rows[j].Name
			}
			return rows[i].Name > rows[j].Name
		}
		if column != "name" {
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
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].ID < rows[j].ID
	})
}

func exploreTeamSortMetric(column string) (int, string) {
	for index, name := range []string{"for", "against", "difference", "points"} {
		if suffix, ok := strings.CutPrefix(column, name+"-"); ok {
			return index, suffix
		}
	}
	return 2, column
}
