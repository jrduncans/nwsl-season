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
	Metric                                                historyMetric
	MetricName                                            string
	GoalsPath                                             string
	MetricLinks                                           []historyMetricView
	Years                                                 []historyYearView
	Rows                                                  []historyRowView
	Selected                                              *historyRowView
	EligibleYears                                         string
	ExcludedSeasons                                       []historyExclusionView
	MetricExcludedSeasons                                 []historyExclusionView
	Distributions                                         []historyDistributionView
	HasDistributionBars                                   bool
	Chart                                                 historyChartView
	ComparisonChart                                       historyChartView
	Compare                                               bool
	// Basis is the scoring basis of every figure on the page. PenaltyLinks are
	// the Included/Excluded choices; each keeps the rest of the selection.
	Basis        history.ScoringBasis
	NonPenalty   bool
	PenaltyLinks []historyMetricView
}

// PointsNote says that official points and ASA xPts keep penalties.
func (historyPage) PointsNote() string { return pointsBasisNote }

// GoalsWord names the goals being counted: "goals" or "non-penalty goals".
func (p historyPage) GoalsWord() string {
	if p.NonPenalty {
		return "non-penalty goals"
	}
	return "goals"
}

// T rewrites a label for the page's scoring basis.
func (p historyPage) T(text string) string { return basisText(p.Basis, text) }

type historyMetricView struct {
	Label, Path string
	Selected    bool
}

type historyYearView struct {
	Season, Path string
	Selected     bool
}

type historyRowView struct {
	Season, Path, Status, Inventory, Exclusions     string
	GoalsPerMatch, XGPerMatch, GoalsMinusXGPerMatch string
	XGCoverage, XPointsCoverage, XGStatus           string
	Played                                          int
	TotalGoals                                      int64
	Selected                                        bool
}

type historyExclusionView struct {
	Season, Reason string
}

type historyChartView struct {
	ViewBox, TitleID, DescriptionID, Title, AxisTitle, Description, EmptyState, Status string
	HasData                                                                            bool
	ActiveStatus                                                                       string
	Ticks                                                                              []historyChartTickView
	Labels                                                                             []historyChartLabelView
	Segments                                                                           []historyChartSegmentView
	Marks                                                                              []historyChartMarkView
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

type historyDistributionView struct {
	Season, Path, AccessibleName string
	GoalsName                    string
	Total                        int
	GoalsEligible                bool
	Selected                     bool
	Segments                     []historyDistributionSegmentView
}

type historyDistributionSegmentView struct {
	Index    int
	Label    string
	Count    int
	Percent  string
	X, Width string
	HasWidth bool
}

const (
	historyChartPlotLeft   = 64.0
	historyChartPlotRight  = 696.0
	historyChartPlotTop    = 30.0
	historyChartPlotBottom = 306.0
	historyChartHitSize    = 60.0
)

func historyPageFor(fromPath string, summaries []history.SeasonScoring, selectedSeason string) historyPage {
	return historyPageForMetric(fromPath, summaries, selectedSeason, historyMetricGoals)
}

func historyPageForMetric(fromPath string, summaries []history.SeasonScoring, selectedSeason string, metric historyMetric) historyPage {
	return historyPageForBasis(fromPath, summaries, selectedSeason, metric, history.AllShots)
}

func historyPageForBasis(fromPath string, summaries []history.SeasonScoring, selectedSeason string, metric historyMetric, basis history.ScoringBasis) historyPage {
	t := func(text string) string { return basisText(basis, text) }
	page := historyPage{
		Basis:      basis,
		NonPenalty: basis.ExcludesPenalties(),
		PenaltyLinks: []historyMetricView{
			{Label: "Included", Path: historyURL(fromPath, selectedSeason, metric, history.AllShots), Selected: !basis.ExcludesPenalties()},
			{Label: "Excluded", Path: historyURL(fromPath, selectedSeason, metric, history.NonPenalty), Selected: basis.ExcludesPenalties()},
		},
		Title:          "Scoring by season",
		HomePath:       relativeURL(fromPath, "/"),
		StylesheetPath: staticURL(fromPath, "site.css"),
		ScriptPath:     staticURL(fromPath, "standings.js"),
		FormPath:       historyURL(fromPath, "", metric, basis),
		CatalogPage:    true,
		Metric:         metric,
		MetricName:     t(map[historyMetric]string{historyMetricGoals: "Goals", historyMetricXG: "xG", historyMetricCompare: "Goals and xG"}[metric]),
		GoalsPath:      historyURL(fromPath, selectedSeason, historyMetricGoals, basis),
		Navigation: []navigationItem{
			{Label: "Seasons", Path: relativeURL(fromPath, "/seasons")},
			{Label: "History", Path: historyURL(fromPath, selectedSeason, metric, basis), Current: true},
		},
		MetricLinks: []historyMetricView{
			{Label: t("Goals"), Path: historyURL(fromPath, selectedSeason, historyMetricGoals, basis), Selected: metric == historyMetricGoals},
			{Label: t("Expected goals (xG)"), Path: historyURL(fromPath, selectedSeason, historyMetricXG, basis), Selected: metric == historyMetricXG},
			{Label: "Compare", Path: historyURL(fromPath, selectedSeason, historyMetricCompare, basis), Selected: metric == historyMetricCompare},
		},
		Years:                 make([]historyYearView, 0, len(summaries)),
		Rows:                  make([]historyRowView, 0, len(summaries)),
		ExcludedSeasons:       make([]historyExclusionView, 0, len(summaries)),
		MetricExcludedSeasons: make([]historyExclusionView, 0, len(summaries)),
		Distributions:         make([]historyDistributionView, 0, len(summaries)),
	}
	eligible := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		row := historyRow(summary, fromPath, selectedSeason, metric)
		page.Rows = append(page.Rows, row)
		page.Years = append(page.Years, historyYearView{Season: summary.Season, Path: row.Path, Selected: row.Selected})
		if summary.PlotEligible && (metric != historyMetricXG || summary.XGPerMatch != nil) {
			eligible = append(eligible, summary.Season)
		}
		if row.Exclusions != "" {
			page.ExcludedSeasons = append(page.ExcludedSeasons, historyExclusionView{Season: row.Season, Reason: row.Exclusions})
		}
		if metric != historyMetricGoals && summary.PlotEligible && summary.XGPerMatch == nil {
			page.MetricExcludedSeasons = append(page.MetricExcludedSeasons, historyExclusionView{Season: summary.Season, Reason: historyMetricExclusion(summary)})
		}
		distribution := historyDistribution(summary, fromPath, selectedSeason, metric)
		page.Distributions = append(page.Distributions, distribution)
		if distribution.GoalsEligible {
			page.HasDistributionBars = true
		}
		if row.Selected {
			selected := row
			page.Selected = &selected
		}
	}
	if len(eligible) == 0 {
		if metric == historyMetricXG {
			page.EligibleYears = t("No seasons currently meet the xG comparison requirements.")
		} else {
			page.EligibleYears = "No seasons currently meet the comparison requirements."
		}
	} else {
		label := "comparison"
		if metric == historyMetricXG {
			label = t("xG comparison")
		}
		page.EligibleYears = "Currently eligible for " + label + ": " + strings.Join(eligible, ", ") + "."
	}
	page.Chart = historyChartForBasis(fromPath, summaries, selectedSeason, metric, basis)
	if metric == historyMetricCompare {
		page.Compare = true
		page.ComparisonChart = historyChartForBasis(fromPath, summaries, selectedSeason, historyMetricXG, basis)
		page.Chart.Title = t("Goals and expected goals per completed match by season")
		page.Chart.AxisTitle = t("Goals and xG per match")
		page.Chart.Description += t(" Expected goals are shown as a separate series on the same scale; only seasons with complete xG coverage appear in that series.")
		for i := range page.ComparisonChart.Marks {
			page.ComparisonChart.Marks[i].Path = historyURL(fromPath, page.ComparisonChart.Marks[i].Season, metric, basis)
		}
	}
	return page
}

func historyChartFor(fromPath string, summaries []history.SeasonScoring, selectedSeason string) historyChartView {
	return historyChartForMetric(fromPath, summaries, selectedSeason, historyMetricGoals)
}

func historyChartForMetric(fromPath string, summaries []history.SeasonScoring, selectedSeason string, metric historyMetric) historyChartView {
	return historyChartForBasis(fromPath, summaries, selectedSeason, metric, history.AllShots)
}

func historyChartForBasis(fromPath string, summaries []history.SeasonScoring, selectedSeason string, metric historyMetric, basis history.ScoringBasis) historyChartView {
	t := func(text string) string { return basisText(basis, text) }
	metricName := t("Goals")
	rateName := "goals"
	if basis.ExcludesPenalties() {
		rateName = "non-penalty goals"
	}
	if metric == historyMetricXG {
		metricName = t("Expected goals")
		rateName = t("expected goals")
	}
	chart := historyChartView{
		ViewBox:       "0 0 720 360",
		TitleID:       "history-chart-title",
		DescriptionID: "history-chart-description",
		Title:         metricName + " per completed match by season",
		AxisTitle:     metricName + " per match",
		Description: "Server-rendered scoring chart for eligible regular seasons in the available archive. The vertical axis shows " + rateName + " per completed match and the horizontal axis shows calendar season. A point requires at least 20 completed, valid matches" + func() string {
			if metric == historyMetricXG {
				return " and complete xG coverage"
			}
			return ""
		}() + ". The 2020 position is labeled No regular season and has no point. Select a point to view that season; excluded seasons remain available in the selector and data table.",
		EmptyState: func() string {
			if metric == historyMetricXG {
				return "No eligible seasons currently have complete xG coverage to plot."
			}
			return "No eligible seasons currently have enough complete scoring data to plot."
		}(),
		Ticks:    make([]historyChartTickView, 0),
		Labels:   make([]historyChartLabelView, 0),
		Segments: make([]historyChartSegmentView, 0),
		Marks:    make([]historyChartMarkView, 0),
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

	for _, summary := range summaries {
		season, err := strconv.Atoi(summary.Season)
		if err == nil {
			years = append(years, season)
		}
		rate := historyMetricRate(summary, metric)
		if !summary.PlotEligible || rate == nil || !finiteChartNumber(*rate) || *rate < 0 {
			continue
		}
		eligible = append(eligible, summary)
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
	yMin, yMax, yStep := historyChartScale(summaries)
	plotHeight := historyChartPlotBottom - historyChartPlotTop
	plotWidth := historyChartPlotRight - historyChartPlotLeft
	seasonX := func(year int) float64 {
		return historyChartPlotLeft + float64(year-minYear)/float64(maxYear-minYear)*plotWidth
	}
	seasonY := func(rate float64) float64 {
		return historyChartPlotBottom - (rate-yMin)/(yMax-yMin)*plotHeight
	}

	for index := 0; index <= int(math.Round((yMax-yMin)/yStep)); index++ {
		tick := math.Round((yMin+float64(index)*yStep)*1e9) / 1e9
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
		rate := historyMetricRate(summary, metric)
		x, y := seasonX(season), seasonY(*rate)
		active := summary.Lifecycle == cache.SourceScopeActive
		unknown := summary.Inventory != cache.InventoryCompletenessComplete
		points = append(points, chartPoint{season: season, x: x, y: y, selected: summary.Season == selectedSeason, active: active, unknown: unknown})
		mark := historyChartMarkView{
			Season: summary.Season, Path: historyURL(fromPath, summary.Season, metric, basis),
			AccessibleName: historyChartAccessibleName(summary, metric),
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
		if active {
			if chart.ActiveStatus != "" {
				chart.ActiveStatus += "; "
			}
			chart.ActiveStatus += fmt.Sprintf("%s: in progress through %d matches", summary.Season, summary.Played)
		}
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
	chart.Status = t(fmt.Sprintf("Both teams combined. Select a point to view a season. Shared Goals/xG scale: %s–%s per match.", formatChartNumber(yMin), formatChartNumber(yMax)))
	return chart
}

func chartGapNote(year int) string {
	if year == 2020 {
		return "No regular season"
	}
	return ""
}

const historyChartDiamondSize = 8.0

func historyChartAccessibleName(summary history.SeasonScoring, metric historyMetric) string {
	rate := "Unavailable"
	if value := historyMetricRate(summary, metric); value != nil {
		rate = fmt.Sprintf("%.2f", *value)
	}
	coverage := "Inventory verified"
	if summary.Inventory != cache.InventoryCompletenessComplete {
		coverage = "Inventory unverified"
	}
	rateName := "goals"
	if metric == historyMetricXG {
		rateName = "expected goals"
	}
	rateName = basisText(summary.Basis, rateName)
	if summary.Basis.ExcludesPenalties() && metric != historyMetricXG {
		rateName = "non-penalty goals"
	}
	name := fmt.Sprintf("%s: %s %s per completed match, %d completed matches, %s", summary.Season, rate, rateName, summary.Played, coverage)
	if summary.Lifecycle == cache.SourceScopeActive {
		name += fmt.Sprintf(", active season through %d matches", summary.Played)
	}
	return name
}

// Both metric views use the same domain, including in comparison mode. Expand
// the usual scoring range for unusual data rather than clipping valid points.
func historyChartScale(summaries []history.SeasonScoring) (float64, float64, float64) {
	low, high := 2.0, 3.2
	for _, summary := range summaries {
		if !summary.PlotEligible {
			continue
		}
		for _, rate := range []*float64{summary.GoalsPerMatch, summary.XGPerMatch} {
			if rate == nil || !finiteChartNumber(*rate) || *rate < 0 {
				continue
			}
			low = math.Min(low, math.Max(0, *rate-0.1))
			high = math.Max(high, *rate+0.1)
		}
	}
	step := 0.2
	for (high-low)/step > 8 {
		step *= 2
	}
	return math.Round(math.Floor(low/step)*step*1e9) / 1e9, math.Round(math.Ceil(high/step)*step*1e9) / 1e9, step
}

func finiteChartNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func formatChartNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func historyRow(summary history.SeasonScoring, fromPath, selectedSeason string, metric historyMetric) historyRowView {
	return historyRowView{
		Season: summary.Season, Path: historyURL(fromPath, summary.Season, metric, summary.Basis), Selected: summary.Season == selectedSeason,
		Played: summary.Played, TotalGoals: summary.TotalGoals,
		GoalsPerMatch: historyRateText(summary.GoalsPerMatch), XGPerMatch: historyRateText(summary.XGPerMatch),
		GoalsMinusXGPerMatch: historyRateText(summary.GoalsMinusXGPerMatch),
		XGCoverage:           fmt.Sprintf("%d/%d", summary.XGCovered, summary.Played),
		XPointsCoverage:      fmt.Sprintf("%d/%d", summary.XPointsCovered, summary.Played),
		XGStatus:             historyMetricStatus(summary, metric),
		Status:               historyStatus(summary), Inventory: historyInventory(summary), Exclusions: exclusionText(summary.Exclusions),
	}
}

func historyMetricRate(summary history.SeasonScoring, metric historyMetric) *float64 {
	if metric == historyMetricXG {
		return summary.XGPerMatch
	}
	return summary.GoalsPerMatch
}

func historyRateText(value *float64) string {
	if value == nil {
		return "Unavailable"
	}
	return fmt.Sprintf("%.2f", *value)
}

func historyMetricStatus(summary history.SeasonScoring, metric historyMetric) string {
	return basisText(summary.Basis, historyMetricStatusText(summary, metric))
}

func historyMetricStatusText(summary history.SeasonScoring, metric historyMetric) string {
	if summary.Played == 0 {
		return "No completed matches are available for an xG average."
	}
	if summary.XGPerMatch == nil {
		return fmt.Sprintf("xG available for %d of %d completed matches; a season average requires %d of %d.", summary.XGCovered, summary.Played, summary.Played, summary.Played)
	}
	if metric == historyMetricXG && !summary.PlotEligible {
		return "Complete xG coverage; this season is excluded from the comparison for the reasons shown."
	}
	return "Complete xG coverage."
}

func historyMetricExclusion(summary history.SeasonScoring) string {
	if summary.XGPerMatch == nil {
		return historyMetricStatus(summary, historyMetricXG)
	}
	if reason := exclusionText(summary.Exclusions); reason != "" {
		return reason
	}
	return "not eligible for xG comparison"
}

func historyDistribution(summary history.SeasonScoring, fromPath, selectedSeason string, metric historyMetric) historyDistributionView {
	segments := make([]historyDistributionSegmentView, 0, len(summary.GoalBins))
	parts := make([]string, 0, len(summary.GoalBins))
	position := 0.0
	// Non-penalty bins count only matches with a penalty breakdown.
	covered := historyGoalsCovered(summary)
	goalsName := "goals"
	if summary.Basis.ExcludesPenalties() {
		goalsName = "non-penalty goals"
	}
	for index, count := range summary.GoalBins {
		label := historyGoalBinLabel(index)
		percent := historyBinPercent(count, covered)
		width := "0"
		hasWidth := count > 0
		if covered > 0 {
			width = formatChartNumber(float64(count) / float64(covered) * 100)
		}
		segments = append(segments, historyDistributionSegmentView{Index: index, Label: label, Count: count, Percent: percent, X: formatChartNumber(position), Width: width, HasWidth: hasWidth})
		parts = append(parts, fmt.Sprintf("%s %s: %d matches (%s)", label, goalsName, count, percent))
		if covered > 0 {
			position += float64(count) / float64(covered) * 100
		}
	}
	return historyDistributionView{
		Season: summary.Season, Path: historyURL(fromPath, summary.Season, metric, summary.Basis),
		AccessibleName: fmt.Sprintf("%s: %s; %d completed matches total", summary.Season, strings.Join(parts, ", "), covered),
		Selected:       summary.Season == selectedSeason,
		Total:          covered, GoalsEligible: summary.PlotEligible && covered > 0, Segments: segments,
		GoalsName: goalsName,
	}
}

// historyGoalsCovered is the number of played matches behind the goal bins: all
// of them unless the non-penalty basis lacks a penalty breakdown for some.
func historyGoalsCovered(summary history.SeasonScoring) int {
	if summary.Basis.ExcludesPenalties() {
		return summary.GoalsCovered
	}
	return summary.Played
}

func historyGoalBinLabel(index int) string {
	switch index {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	default:
		return "4+"
	}
}

func historyBinPercent(count, played int) string {
	if played == 0 {
		return "Unavailable"
	}
	return fmt.Sprintf("%.1f%%", float64(count)/float64(played)*100)
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
