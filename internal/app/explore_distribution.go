package app

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
)

type exploreDistributionView struct {
	DistributionRows                    []historyDistributionView
	DistributionColumns                 []exploreTeamColumn
	DistributionSort, DistributionOrder string
	DistributionBin                     string
	DistributionExpanded                bool
}

func exploreDistribution(query url.Values, distributions []historyDistributionView) (exploreDistributionView, error) {
	page := exploreDistributionView{DistributionSort: "season", DistributionOrder: "desc", DistributionBin: "all"}
	if values, present := query["distribution-bin"]; present {
		if len(values) != 1 || !slices.Contains([]string{"all", "0", "1", "2", "3", "4"}, values[0]) {
			return page, fmt.Errorf("invalid distribution-bin selection")
		}
		page.DistributionBin = values[0]
	}
	columns := []exploreTeamColumn{
		{Key: "season", Label: "Season", Description: "season"},
		{Key: "matches", Label: "Matches", Description: "matches played"},
	}
	for index, label := range []string{"0 goals", "1 goal", "2 goals", "3 goals", "4+ goals"} {
		columns = append(columns, exploreTeamColumn{Key: fmt.Sprintf("bin-%d", index), Label: label, Description: "share of matches with " + label})
	}
	keys := make([]string, 0, len(columns))
	for _, column := range columns {
		keys = append(keys, column.Key)
	}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"distribution-sort", &page.DistributionSort, keys},
		{"distribution-order", &page.DistributionOrder, []string{"asc", "desc"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
			page.DistributionExpanded = true
		}
	}
	for _, distribution := range distributions {
		if distribution.GoalsEligible {
			page.DistributionRows = append(page.DistributionRows, distribution)
		}
	}
	sortExploreDistributions(page.DistributionRows, page.DistributionSort, page.DistributionOrder)
	for _, column := range columns {
		column.Sort = "none"
		order := "desc"
		if column.Key == page.DistributionSort {
			column.Sort, order = "descending", "asc"
			if page.DistributionOrder == "asc" {
				column.Sort, order = "ascending", "desc"
			}
		}
		column.URL = exploreTeamURL(query, map[string]string{
			"view": "distribution", "distribution-sort": column.Key, "distribution-order": order,
		})
		page.DistributionColumns = append(page.DistributionColumns, column)
	}
	return page, nil
}

func sortExploreDistributions(rows []historyDistributionView, column, order string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		compare := 0
		switch column {
		case "season":
			if a.Season < b.Season {
				compare = -1
			} else if a.Season > b.Season {
				compare = 1
			}
		case "matches":
			compare = a.Total - b.Total
		default:
			index := int(column[len(column)-1] - '0')
			compare = a.Segments[index].Count*b.Total - b.Segments[index].Count*a.Total
		}
		if compare != 0 {
			if order == "asc" {
				return compare < 0
			}
			return compare > 0
		}
		return a.Season > b.Season
	})
}
