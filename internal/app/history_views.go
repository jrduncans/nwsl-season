package app

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

type historyPage struct {
	Title, HomePath, StylesheetPath, ScriptPath, FormPath string
	CatalogPage                                           bool
	Navigation                                            []navigationItem
	Years                                                 []historyYearView
	Rows                                                  []historyRowView
	Selected                                              *historyRowView
	EligibleYears                                         string
	ExcludedSeasons                                       []historyExclusionView
	Chart                                                 historyChartView
}

type historyYearView struct {
	Season, Path string
	Selected     bool
}

type historyRowView struct {
	Season, Path, Status, Inventory, Exclusions, GoalsPerMatch string
	Played                                                     int
	TotalGoals                                                 int64
	Selected                                                   bool
}

type historyExclusionView struct {
	Season, Reason string
}

type historyChartView struct {
	ViewBox, TitleID, DescriptionID, Title, Description, EmptyState, Status string
	HasData                                                                 bool
	Ticks                                                                   []historyChartTickView
	Labels                                                                  []historyChartLabelView
	Segments                                                                []historyChartSegmentView
	Marks                                                                   []historyChartMarkView
}

type historyChartTickView struct {
	Y, Label string
}

type historyChartLabelView struct {
	X, Label, Note string
	Optional       bool
}

type historyChartSegmentView struct {
	X1, Y1, X2, Y2 string
	Dashed         bool
}

type historyChartMarkView struct {
	Season, Path, AccessibleName string
	X, Y, HitX, HitY, Radius     string
	HitSize                      string
	DiamondPoints                string
	Selected, Active, Unknown    bool
}

const (
	historyChartPlotLeft   = 64.0
	historyChartPlotRight  = 696.0
	historyChartPlotTop    = 30.0
	historyChartPlotBottom = 306.0
	historyChartHitSize    = 60.0
)

func historyPageFor(fromPath string, summaries []history.SeasonScoring, selectedSeason string) historyPage {
	page := historyPage{
		Title:          "Scoring by season",
		HomePath:       relativeURL(fromPath, "/"),
		StylesheetPath: relativeURL(fromPath, "/static/site.css"),
		ScriptPath:     relativeURL(fromPath, "/static/standings.js"),
		FormPath:       historyURL(fromPath, ""),
		CatalogPage:    true,
		Navigation: []navigationItem{
			{Label: "Seasons", Path: relativeURL(fromPath, "/seasons")},
			{Label: "History", Path: historyURL(fromPath, ""), Current: true},
		},
		Years:           make([]historyYearView, 0, len(summaries)),
		Rows:            make([]historyRowView, 0, len(summaries)),
		ExcludedSeasons: make([]historyExclusionView, 0, len(summaries)),
	}
	eligible := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		row := historyRow(summary, fromPath, selectedSeason)
		page.Rows = append(page.Rows, row)
		page.Years = append(page.Years, historyYearView{Season: summary.Season, Path: row.Path, Selected: row.Selected})
		if summary.PlotEligible {
			eligible = append(eligible, summary.Season)
		}
		if row.Exclusions != "" {
			page.ExcludedSeasons = append(page.ExcludedSeasons, historyExclusionView{Season: row.Season, Reason: row.Exclusions})
		}
		if row.Selected {
			selected := row
			page.Selected = &selected
		}
	}
	if len(eligible) == 0 {
		page.EligibleYears = "No seasons currently meet the comparison requirements."
	} else {
		page.EligibleYears = "Currently eligible for comparison: " + strings.Join(eligible, ", ") + "."
	}
	page.Chart = historyChartFor(fromPath, summaries, selectedSeason)
	return page
}

func historyChartFor(fromPath string, summaries []history.SeasonScoring, selectedSeason string) historyChartView {
	chart := historyChartView{
		ViewBox:       "0 0 720 360",
		TitleID:       "history-chart-title",
		DescriptionID: "history-chart-description",
		Title:         "Goals per completed match by season",
		Description:   "Server-rendered scoring chart for eligible regular seasons in the available archive. The vertical axis shows goals per completed match and the horizontal axis shows calendar season. A point requires at least 20 completed, valid matches. The 2020 position is labeled No regular season and has no point. Select a point to view that season; excluded seasons remain available in the selector and data table.",
		EmptyState:    "No eligible seasons currently have enough complete scoring data to plot.",
		Ticks:         make([]historyChartTickView, 0),
		Labels:        make([]historyChartLabelView, 0),
		Segments:      make([]historyChartSegmentView, 0),
		Marks:         make([]historyChartMarkView, 0),
	}

	type chartPoint struct {
		season   int
		x, y     float64
		selected bool
		active   bool
		unknown  bool
	}
	eligible := make([]history.SeasonScoring, 0, len(summaries))
	years := make([]int, 0, len(summaries))
	maxRate := 0.0
	for _, summary := range summaries {
		season, err := strconv.Atoi(summary.Season)
		if err == nil {
			years = append(years, season)
		}
		if !summary.PlotEligible || summary.GoalsPerMatch == nil || !finiteChartNumber(*summary.GoalsPerMatch) || *summary.GoalsPerMatch < 0 {
			continue
		}
		eligible = append(eligible, summary)
		if *summary.GoalsPerMatch > maxRate {
			maxRate = *summary.GoalsPerMatch
		}
	}
	if len(eligible) == 0 {
		return chart
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left, _ := strconv.Atoi(eligible[i].Season)
		right, _ := strconv.Atoi(eligible[j].Season)
		return left < right
	})

	minYear, maxYear := 2020, 2020
	for _, year := range years {
		if year < minYear {
			minYear = year
		}
		if year > maxYear {
			maxYear = year
		}
	}
	if maxYear == minYear {
		maxYear = minYear + 1
	}
	yMax := math.Max(1, math.Ceil(maxRate))
	plotHeight := historyChartPlotBottom - historyChartPlotTop
	plotWidth := historyChartPlotRight - historyChartPlotLeft
	seasonX := func(year int) float64 {
		return historyChartPlotLeft + float64(year-minYear)/float64(maxYear-minYear)*plotWidth
	}
	seasonY := func(rate float64) float64 {
		return historyChartPlotBottom - rate/yMax*plotHeight
	}

	for tick := 0.0; tick <= yMax; tick++ {
		y := seasonY(tick)
		chart.Ticks = append(chart.Ticks, historyChartTickView{Y: formatChartNumber(y), Label: formatChartNumber(tick)})
	}
	for index, year := 0, minYear; year <= maxYear; index, year = index+1, year+1 {
		chart.Labels = append(chart.Labels, historyChartLabelView{
			X: formatChartNumber(seasonX(year)), Label: strconv.Itoa(year), Note: chartGapNote(year),
			Optional: year != minYear && year != maxYear && year != 2020 && index%2 == 1,
		})
	}

	points := make([]chartPoint, 0, len(eligible))
	for _, summary := range eligible {
		season, _ := strconv.Atoi(summary.Season)
		x, y := seasonX(season), seasonY(*summary.GoalsPerMatch)
		active := summary.Lifecycle == cache.SourceScopeActive
		unknown := summary.Inventory != cache.InventoryCompletenessComplete
		points = append(points, chartPoint{season: season, x: x, y: y, selected: summary.Season == selectedSeason, active: active, unknown: unknown})
		mark := historyChartMarkView{
			Season: summary.Season, Path: historyURL(fromPath, summary.Season),
			AccessibleName: historyChartAccessibleName(summary),
			X:              formatChartNumber(x), Y: formatChartNumber(y),
			HitX: formatChartNumber(x - historyChartHitSize/2), HitY: formatChartNumber(y - historyChartHitSize/2),
			Radius: formatChartNumber(7), HitSize: formatChartNumber(historyChartHitSize), Selected: summary.Season == selectedSeason, Active: active, Unknown: unknown,
		}
		mark.DiamondPoints = strings.Join([]string{
			formatChartNumber(x) + "," + formatChartNumber(y-historyChartDiamondSize),
			formatChartNumber(x+historyChartDiamondSize) + "," + formatChartNumber(y),
			formatChartNumber(x) + "," + formatChartNumber(y+historyChartDiamondSize),
			formatChartNumber(x-historyChartDiamondSize) + "," + formatChartNumber(y),
		}, " ")
		chart.Marks = append(chart.Marks, mark)
	}

	for index := 1; index < len(points); index++ {
		previous, current := points[index-1], points[index]
		if previous.season+1 != current.season || previous.active || current.active {
			continue
		}
		chart.Segments = append(chart.Segments, historyChartSegmentView{
			X1: formatChartNumber(previous.x), Y1: formatChartNumber(previous.y),
			X2: formatChartNumber(current.x), Y2: formatChartNumber(current.y), Dashed: previous.unknown || current.unknown,
		})
	}

	chart.HasData = true
	chart.Status = fmt.Sprintf("Plotted eligible seasons: %s. Values show goals per completed match; the chart does not infer a trend or conclusion.", chartSeasonList(eligible))
	return chart
}

func chartGapNote(year int) string {
	if year == 2020 {
		return "No regular season"
	}
	return ""
}

const historyChartDiamondSize = 8.0

func historyChartAccessibleName(summary history.SeasonScoring) string {
	rate := "Unavailable"
	if summary.GoalsPerMatch != nil {
		rate = fmt.Sprintf("%.2f", *summary.GoalsPerMatch)
	}
	coverage := "Inventory verified"
	if summary.Inventory != cache.InventoryCompletenessComplete {
		coverage = "Inventory unverified"
	}
	name := fmt.Sprintf("%s: %s goals per completed match, %d completed matches, %s", summary.Season, rate, summary.Played, coverage)
	if summary.Lifecycle == cache.SourceScopeActive {
		name += fmt.Sprintf(", active season through %d matches", summary.Played)
	}
	return name
}

func chartSeasonList(summaries []history.SeasonScoring) string {
	seasons := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		seasons = append(seasons, summary.Season)
	}
	return strings.Join(seasons, ", ")
}

func finiteChartNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func formatChartNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func historyRow(summary history.SeasonScoring, fromPath, selectedSeason string) historyRowView {
	goalsPerMatch := "Unavailable"
	if summary.GoalsPerMatch != nil {
		goalsPerMatch = fmt.Sprintf("%.2f", *summary.GoalsPerMatch)
	}
	return historyRowView{
		Season: summary.Season, Path: historyURL(fromPath, summary.Season), Selected: summary.Season == selectedSeason,
		Played: summary.Played, TotalGoals: summary.TotalGoals, GoalsPerMatch: goalsPerMatch,
		Status: historyStatus(summary), Inventory: historyInventory(summary), Exclusions: exclusionText(summary.Exclusions),
	}
}

func historyStatus(summary history.SeasonScoring) string {
	lifecycle := "Season status unavailable"
	switch summary.Lifecycle {
	case cache.SourceScopeUpcoming:
		lifecycle = "Upcoming"
	case cache.SourceScopeActive:
		lifecycle = fmt.Sprintf("Active through %d matches", summary.Played)
	case cache.SourceScopeCompleted:
		lifecycle = "Completed"
	}
	if summary.Readiness == cache.SourceReadinessAvailable {
		return lifecycle
	}
	availability := "Source data unavailable"
	if summary.Readiness == cache.SourceReadinessNotPublished {
		availability = "Not published"
	}
	return lifecycle + "; " + availability
}

func historyInventory(summary history.SeasonScoring) string {
	switch summary.Inventory {
	case cache.InventoryCompletenessComplete:
		return "Fixture inventory complete"
	case cache.InventoryCompletenessIncomplete:
		return "Known fixture inventory incomplete"
	default:
		if summary.Readiness == cache.SourceReadinessAvailable {
			return "Cached matches; inventory unverified"
		}
		return "Inventory unavailable"
	}
}
