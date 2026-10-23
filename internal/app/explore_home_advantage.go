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

// exploreHomeAdvantageView is the Home advantage tab: each eligible season's
// home-minus-away gap per played match, as chart records and a sortable table.
type exploreHomeAdvantageView struct {
	HomeAdvantageRows                                   []exploreHomeAdvantageRow
	HomeAdvantageData                                   []exploreHomeAdvantageRecord
	HomeAdvantageColumns                                []exploreTeamColumn
	HomeAdvantageSort, HomeAdvantageOrder               string
	HomeAdvantageExpanded                               bool
	HomeAdvantageActiveStatus                           string
	HomeAdvantageXGMissing, HomeAdvantageXPointsMissing string
}

// exploreHomeAdvantageRecord is one season's chart values. Every gap is home
// minus away per played match; a nil gap is a gap in the chart.
type exploreHomeAdvantageRecord struct {
	Season   string   `json:"season"`
	Played   int      `json:"played"`
	Active   bool     `json:"active"`
	Goals    *float64 `json:"goals"`
	XG       *float64 `json:"xg"`
	Points   *float64 `json:"points"`
	XPoints  *float64 `json:"xpoints"`
	HomeWins int      `json:"homeWins"`
	Draws    int      `json:"draws"`
	AwayWins int      `json:"awayWins"`
}

type exploreHomeAdvantageRow struct {
	// Basis is the row's basis key; see tagBasis.
	Basis                                          string
	Season                                         string
	Played                                         int
	Goals, XG, Points, XPoints                     string
	GoalsValue, XGValue, PointsValue, XPointsValue *float64
	Results                                        string
	HomeWins, Draws, AwayWins                      int
	// HomeWinShare is the home win share of played matches, for sorting.
	HomeWinShare float64
}

var exploreHomeAdvantageKeys = []string{"season", "matches", "goals", "xg", "points", "xpoints", "results"}

func exploreHomeAdvantage(query url.Values, seasons []history.SeasonHomeAdvantage, basis history.ScoringBasis) (exploreHomeAdvantageView, error) {
	page := exploreHomeAdvantageView{HomeAdvantageSort: "season", HomeAdvantageOrder: "desc"}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"home-sort", &page.HomeAdvantageSort, exploreHomeAdvantageKeys},
		{"home-order", &page.HomeAdvantageOrder, []string{"asc", "desc"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
			page.HomeAdvantageExpanded = true
		}
	}

	var active, missingXG, missingXPoints []string
	for _, season := range seasons {
		if !season.PlotEligible || season.Goals == nil {
			continue
		}
		page.HomeAdvantageRows = append(page.HomeAdvantageRows, exploreHomeAdvantageRowFor(season))
		if season.Lifecycle == cache.SourceScopeActive {
			active = append(active, fmt.Sprintf("%s: in progress through %d matches", season.Season, season.Played))
		}
		if season.XG == nil {
			missingXG = append(missingXG, season.Season)
		}
		if season.XPoints == nil {
			missingXPoints = append(missingXPoints, season.Season)
		}
	}
	page.HomeAdvantageData = exploreHomeAdvantageRecords(seasons)
	page.HomeAdvantageActiveStatus = strings.Join(active, "; ")
	page.HomeAdvantageXGMissing = strings.Join(missingXG, ", ")
	page.HomeAdvantageXPointsMissing = strings.Join(missingXPoints, ", ")

	sortExploreHomeAdvantage(page.HomeAdvantageRows, page.HomeAdvantageSort, page.HomeAdvantageOrder)
	columns := []exploreTeamColumn{
		{Key: "season", Label: "Season", Description: "season"},
		{Key: "matches", Label: "Matches", Description: "matches played"},
		basisColumn(basis, "goals", "Goals", "goals, home minus away per match"),
		basisColumn(basis, "xg", "xG", "xG, home minus away per match"),
		{Key: "points", Label: "Points", Description: "points, home minus away per match"},
		{Key: "xpoints", Label: "xPoints", Description: "xPoints, home minus away per match"},
		{Key: "results", Label: "Home W–D–L", Description: "home win share"},
	}
	for _, column := range columns {
		column.Sort = "none"
		order := "desc"
		if column.Key == page.HomeAdvantageSort {
			column.Sort, order = "descending", "asc"
			if page.HomeAdvantageOrder == "asc" {
				column.Sort, order = "ascending", "desc"
			}
		}
		column.URL = exploreTeamURL(query, map[string]string{
			"view": "home-advantage", "home-sort": column.Key, "home-order": order,
		})
		page.HomeAdvantageColumns = append(page.HomeAdvantageColumns, column)
	}
	return page, nil
}

func exploreHomeAdvantageRowFor(season history.SeasonHomeAdvantage) exploreHomeAdvantageRow {
	row := exploreHomeAdvantageRow{
		Season: season.Season, Played: season.Played,
		Goals: homeGapText(season.Goals), XG: homeGapText(season.XG), Points: homeGapText(season.Points), XPoints: homeGapText(season.XPoints),
		GoalsValue: season.Goals, XGValue: season.XG, PointsValue: season.Points, XPointsValue: season.XPoints,
		HomeWins: season.HomeWins, Draws: season.Draws, AwayWins: season.AwayWins,
	}
	if season.Played > 0 {
		played := float64(season.Played)
		row.HomeWinShare = float64(season.HomeWins) / played
		row.Results = fmt.Sprintf("%.0f%% – %.0f%% – %.0f%%", float64(season.HomeWins)/played*100, float64(season.Draws)/played*100, float64(season.AwayWins)/played*100)
	}
	return row
}

// homeGapText formats a home-minus-away rate with an explicit sign.
func homeGapText(value *float64) string {
	if value == nil {
		return "Unavailable"
	}
	if *value > -.005 && *value < .005 {
		return "0.00"
	}
	return fmt.Sprintf("%+.2f", *value)
}

// sortExploreHomeAdvantage orders rows from unrounded values. Unavailable
// values stay last in both directions, and ties show the newest season first.
func sortExploreHomeAdvantage(rows []exploreHomeAdvantageRow, column, order string) {
	value := func(row exploreHomeAdvantageRow) *float64 {
		switch column {
		case "goals":
			return row.GoalsValue
		case "xg":
			return row.XGValue
		case "points":
			return row.PointsValue
		case "xpoints":
			return row.XPointsValue
		case "matches":
			played := float64(row.Played)
			return &played
		case "results":
			return &row.HomeWinShare
		}
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if column != "season" {
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
		} else if a.Season != b.Season {
			if order == "asc" {
				return a.Season < b.Season
			}
			return a.Season > b.Season
		}
		return a.Season > b.Season
	})
}

// exploreHomeAdvantageRecords is the chart payload: every eligible season
// with goals, in order.
func exploreHomeAdvantageRecords(seasons []history.SeasonHomeAdvantage) []exploreHomeAdvantageRecord {
	var records []exploreHomeAdvantageRecord
	for _, season := range seasons {
		if !season.PlotEligible || season.Goals == nil {
			continue
		}
		records = append(records, exploreHomeAdvantageRecord{
			Season: season.Season, Played: season.Played, Active: season.Lifecycle == cache.SourceScopeActive,
			Goals: season.Goals, XG: season.XG, Points: season.Points, XPoints: season.XPoints,
			HomeWins: season.HomeWins, Draws: season.Draws, AwayWins: season.AwayWins,
		})
	}
	return records
}
