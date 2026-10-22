package app

import (
	"cmp"
	"net/url"
	"slices"
	"strconv"

	"github.com/jrduncans/nwsl-season/internal/history"
)

// Chart series and table columns are separate: sorting never changes the
// chronological population used for rolling values or the charts.
func exploreTrendTableColumns(page exploreSeasonTrendView, basis history.ScoringBasis) []exploreTrendColumn {
	columns := []exploreTrendColumn{
		{Label: "Match", Field: "match", Key: "match"},
		{Label: "Date (venue local)", Field: "date"},
		{Label: "Venue", Field: "venue", Key: "venue"},
		{Label: "Opponent", Field: "opponent"},
		{Label: "Score", Field: "score"},
		{Label: "Result", Field: "result", Key: "result"},
		{Label: "Points", Measure: explorePoints},
	}
	if basis == history.NonPenalty {
		columns = append(columns, exploreTrendColumn{Label: "Non-penalty goals", Field: "basis-score"})
	}
	columns = append(columns,
		exploreTrendColumn{Label: basisText(basis, "xG for"), Measure: exploreScored, Expected: true},
		exploreTrendColumn{Label: basisText(basis, "xG against"), Measure: exploreAllowed, Expected: true},
		exploreTrendColumn{Label: "xPoints", Measure: explorePoints, Expected: true},
	)
	if page.TrendAdjusted {
		goals := "goals"
		if basis.ExcludesPenalties() {
			goals = "non-penalty goals"
		}
		columns = append(columns,
			exploreTrendColumn{Label: "Adjusted " + goals + " for", Measure: exploreScored, Adjusted: true, Key: "adjusted-goals-for"},
			exploreTrendColumn{Label: "Adjusted " + goals + " against", Measure: exploreAllowed, Adjusted: true, Key: "adjusted-goals-against"},
			exploreTrendColumn{Label: basisText(basis, "Adjusted xG for"), Measure: exploreScored, Expected: true, Adjusted: true, Key: "adjusted-xg-for"},
			exploreTrendColumn{Label: basisText(basis, "Adjusted xG against"), Measure: exploreAllowed, Expected: true, Adjusted: true, Key: "adjusted-xg-against"},
		)
	}
	switch page.TrendView {
	case "difference":
		columns = append(columns,
			exploreTrendColumn{Label: basisText(basis, "Goal margin"), Measure: exploreMargin, Key: "goal-margin"},
			exploreTrendColumn{Label: basisText(basis, "xG margin"), Measure: exploreMargin, Expected: true, Key: "xg-margin"},
		)
	case "compare":
		columns = append(columns,
			exploreTrendColumn{Label: basisText(basis, "Goals − xG"), Measure: exploreScored, Gap: true, Key: "scored-gap"},
			exploreTrendColumn{Label: basisText(basis, "Goals allowed − xG allowed"), Measure: exploreAllowed, Gap: true, Key: "allowed-gap"},
		)
	case "points":
		columns = append(columns, exploreTrendColumn{Label: "Points − xPoints", Measure: explorePoints, Gap: true, Key: "points-gap"})
	}
	if page.TrendMode == "rolling" {
		for _, column := range page.TrendColumns {
			column.Label += " · " + page.TrendWindow + "-match average"
			column.Rolling = true
			columns = append(columns, column)
		}
	}
	return columns
}

func exploreTrendSortLinks(columns []exploreTrendColumn, query url.Values, selected, order string) []exploreTrendColumn {
	for i := range columns {
		column := &columns[i]
		if column.Key == "" {
			continue
		}
		target := cloneQuery(query)
		if target.Get(penaltiesParam) == penaltiesInclude {
			target.Del(penaltiesParam)
		}
		target.Set("view", "season-trend")
		target.Set("trend-sort", column.Key)
		next := "asc"
		column.Sort = "none"
		if selected == column.Key {
			column.Sort = "ascending"
			next = "desc"
			if order == "desc" {
				column.Sort, next = "descending", "asc"
			}
		} else if column.Gap || column.Adjusted || column.Key == "goal-margin" || column.Key == "xg-margin" {
			next = "desc"
		}
		target.Del("trend-order")
		if next != "asc" {
			target.Set("trend-order", next)
		}
		column.URL = "?" + target.Encode()
	}
	return columns
}

// Adjusted columns read the opponent-adjusted matches; the rest read official.
func exploreTrendCell(column exploreTrendColumn, official, adjusted []exploreMatchRecord, index, window int) string {
	matches := official
	if column.Adjusted {
		matches = adjusted
	}
	match := matches[index]
	switch column.Field {
	case "match":
		return strconv.Itoa(index + 1)
	case "date":
		return match.Date
	case "venue":
		return match.Venue
	case "opponent":
		return match.Opponent
	case "score":
		return match.Score
	case "result":
		switch match.Values[explorePoints].Actual {
		case 3:
			return "Win"
		case 1:
			return "Draw"
		default:
			return "Loss"
		}
	case "basis-score":
		if !match.GoalsCovered {
			return "Unavailable"
		}
		return strconv.Itoa(int(match.Values[exploreScored].Actual)) + "–" + strconv.Itoa(int(match.Values[exploreAllowed].Actual))
	}
	value := exploreTrendTableValue(column, matches, index, window)
	if value == nil {
		return "Unavailable"
	}
	if column.Gap {
		return h2hSignedMargin(*value)
	}
	if !column.Expected && !column.Rolling && !column.Adjusted {
		return strconv.Itoa(int(*value))
	}
	return exploreTrendNumber(value)
}

func exploreTrendTableValue(column exploreTrendColumn, matches []exploreMatchRecord, index, window int) *float64 {
	match := matches[index]
	if column.Gap {
		if column.Measure == explorePoints {
			return pointsGap(match.Values[explorePoints])
		}
		return goalGap(match, column.Measure)
	}
	value := match.Values[column.Measure]
	if column.Rolling {
		value = exploreRollingValue(matches, index, column.Measure, window)
	}
	if column.Expected {
		return value.Expected
	}
	if column.Measure != explorePoints {
		start := index
		if column.Rolling {
			start = max(0, index+1-window)
		}
		for _, row := range matches[start : index+1] {
			if !row.GoalsCovered {
				return nil
			}
		}
	}
	return &value.Actual
}

func sortExploreTrendRows(rows []exploreSeasonTrendRow, column, order string) {
	value := func(row exploreSeasonTrendRow) *float64 {
		switch column {
		case "result":
			return &row.Values[explorePoints].Actual
		case "goal-margin":
			if !row.GoalsCovered {
				return nil
			}
			return &row.Values[exploreMargin].Actual
		case "xg-margin":
			return row.Values[exploreMargin].Expected
		case "points-gap":
			return pointsGap(row.Values[explorePoints])
		case "scored-gap":
			return goalGap(row.exploreMatchRecord, exploreScored)
		case "allowed-gap":
			return goalGap(row.exploreMatchRecord, exploreAllowed)
		case "adjusted-goals-for", "adjusted-goals-against":
			if !row.AdjustedMatch.GoalsCovered {
				return nil
			}
			if column == "adjusted-goals-for" {
				return &row.AdjustedMatch.Values[exploreScored].Actual
			}
			return &row.AdjustedMatch.Values[exploreAllowed].Actual
		case "adjusted-xg-for":
			return row.AdjustedMatch.Values[exploreScored].Expected
		case "adjusted-xg-against":
			return row.AdjustedMatch.Values[exploreAllowed].Expected
		default:
			return new(float64(row.Number))
		}
	}
	slices.SortStableFunc(rows, func(a, b exploreSeasonTrendRow) int {
		if a.Undated != b.Undated {
			if a.Undated {
				return 1
			}
			return -1
		}
		comparison := 0
		if column == "venue" {
			comparison = cmp.Compare(a.Venue, b.Venue)
		} else {
			x, y := value(a), value(b)
			if x == nil || y == nil {
				if x != nil {
					return -1
				}
				if y != nil {
					return 1
				}
			} else {
				comparison = cmp.Compare(*x, *y)
			}
		}
		if comparison == 0 {
			return cmp.Compare(b.Number, a.Number)
		}
		if order == "desc" {
			return -comparison
		}
		return comparison
	})
}
