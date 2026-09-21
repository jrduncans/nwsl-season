package app

import "fmt"

type exploreContextHolder struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Season string `json:"season"`
	Played int    `json:"played"`
}

type exploreContextExtreme struct {
	Value   float64                `json:"value"`
	Display string                 `json:"-"`
	Holders []exploreContextHolder `json:"holders"`
}

type exploreSeasonContext struct {
	Season  string                 `json:"season"`
	Active  bool                   `json:"active"`
	Low     *exploreContextExtreme `json:"low"`
	High    *exploreContextExtreme `json:"high"`
	Covered int                    `json:"covered"`
	Teams   int                    `json:"teams"`
}

type exploreSeriesContext struct {
	Label   string                 `json:"label"`
	Seasons []exploreSeasonContext `json:"seasons"`
	Low     *exploreContextExtreme `json:"low"`
	High    *exploreContextExtreme `json:"high"`
	Since   string                 `json:"since"`
	Partial bool                   `json:"partial"`
}

type exploreMetricContext struct {
	Goals exploreSeriesContext `json:"goals"`
	XG    exploreSeriesContext `json:"xg"`
}

// Context shares the team comparison's eligibility rules. League xG extrema
// require all compared teams to have complete xG, so missing teams cannot be
// silently excluded from a claimed season or historical record.
func exploreTeamContext(seasons []exploreTeamSeason) [3]exploreMetricContext {
	var metrics [3]exploreMetricContext
	for index, labels := range [][2]string{{"Goals scored", "xG scored"}, {"Goals allowed", "xG allowed"}, {"Goal differential", "xG differential"}} {
		metrics[index] = exploreMetricContext{
			Goals: exploreContextSeries(seasons, index, false, labels[0]),
			XG:    exploreContextSeries(seasons, index, true, labels[1]),
		}
	}
	return metrics
}

func exploreContextSeries(seasons []exploreTeamSeason, measure int, expected bool, label string) exploreSeriesContext {
	series := exploreSeriesContext{Label: label}
	for _, season := range seasons {
		row := exploreSeasonContext{Season: season.Season, Active: season.Active, Teams: len(season.Teams)}
		for _, team := range season.Teams {
			value := team.Values[measure].Actual
			if expected {
				if team.Values[measure].Expected == nil {
					continue
				}
				value = *team.Values[measure].Expected
			}
			row.Covered++
			holder := exploreContextHolder{ID: team.ID, Name: team.Name, Season: season.Season, Played: team.Played}
			row.Low = exploreContextBound(row.Low, value, holder, false)
			row.High = exploreContextBound(row.High, value, holder, true)
		}
		if row.Covered != row.Teams {
			row.Low, row.High = nil, nil
			series.Partial = true
		}
		series.Seasons = append(series.Seasons, row)
		if row.Active || row.Low == nil {
			continue
		}
		if series.Since == "" || row.Season < series.Since {
			series.Since = row.Season
		}
		for _, holder := range row.Low.Holders {
			series.Low = exploreContextBound(series.Low, row.Low.Value, holder, false)
		}
		for _, holder := range row.High.Holders {
			series.High = exploreContextBound(series.High, row.High.Value, holder, true)
		}
	}
	return series
}

func exploreContextBound(bound *exploreContextExtreme, value float64, holder exploreContextHolder, high bool) *exploreContextExtreme {
	if bound == nil || (high && value > bound.Value) || (!high && value < bound.Value) {
		return &exploreContextExtreme{Value: value, Display: fmt.Sprintf("%.2f", value), Holders: []exploreContextHolder{holder}}
	}
	if value == bound.Value {
		bound.Holders = append(bound.Holders, holder)
	}
	return bound
}
