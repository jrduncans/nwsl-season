package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

var seasonYearPattern = regexp.MustCompile(`^[0-9]{4}$`)

// historicalStore is deliberately optional: Explore is available only to
// cache implementations that can supply one coherent historical snapshot.
// It must not fall back to individual season reads.
type historicalStore interface {
	HistoricalRegularSeasons(context.Context) ([]cache.HistoricalSeason, error)
}

// legacyHistory redirects the retired History scoring page to Explore's
// Scoring trend, keeping the chart metric and scoring basis.
func (a *application) legacyHistory(w http.ResponseWriter, r *http.Request) {
	query := url.Values{"view": {"trend"}}
	legacy := r.URL.Query()
	for _, key := range []string{"metric", "penalties"} {
		if values, ok := legacy[key]; ok {
			query[key] = values
		}
	}
	redirectRelative(w, relativeURL(r.URL.Path, "/explore")+"?"+query.Encode(), http.StatusMovedPermanently)
}

func (a *application) explore(w http.ResponseWriter, r *http.Request) {
	basis, err := penaltiesBasisRaw(r.URL.RawQuery)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	query := r.URL.Query()
	if pointsOnlyExploreSelection(query) {
		basis = history.AllShots
		query.Del("penalties")
		r.URL.RawQuery = query.Encode()
	}
	store, ok := a.store.(historicalStore)
	if !ok {
		a.renderExploreError(w, r, http.StatusServiceUnavailable, "Explore unavailable", "The local archive cannot provide historical scoring data.")
		return
	}
	inputs, err := store.HistoricalRegularSeasons(r.Context())
	if err != nil {
		a.renderExploreError(w, r, http.StatusInternalServerError, "Explore unavailable", "Historical scoring data could not be loaded.")
		return
	}
	summaries, err := history.SummarizeScoringBasis(inputs, basis)
	if err != nil {
		a.renderExploreError(w, r, http.StatusInternalServerError, "Explore unavailable", "Historical scoring data could not be summarized.")
		return
	}
	a.renderExplore(w, r, summaries, inputs, basis)
}

func (a *application) renderExploreBadRequest(w http.ResponseWriter, r *http.Request, err error) {
	a.renderExploreError(w, r, http.StatusBadRequest, "Invalid Explore selection", err.Error())
}

func (a *application) renderExploreError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	a.render(w, "error", errorPage{
		Title: title, Message: message,
		HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: staticURL(r.URL.Path, "site.css"), ScriptPath: staticURL(r.URL.Path, "standings.js"),
		Navigation: []navigationItem{
			{Label: "Seasons", Path: relativeURL(r.URL.Path, "/seasons")},
			{Label: "Explore", Path: relativeURL(r.URL.Path, "/explore"), Current: true},
		},
		CatalogPage: true,
	})
}

// exploreScoringPage is the league-scoring part of the Explore page: the
// Scoring trend status, the goal distribution, and the scoring basis shared by
// every view.
type exploreScoringPage struct {
	Title, HomePath, StylesheetPath, ScriptPath string
	CatalogPage                                 bool
	Navigation                                  []navigationItem
	// HasTrendData is true when at least one season can be plotted on the
	// Scoring trend; ActiveStatus names plotted seasons still in progress.
	HasTrendData        bool
	ActiveStatus        string
	XGIncomplete        bool
	Distributions       []exploreDistributionSeason
	HasDistributionBars bool
	// Basis is the scoring basis of every figure on the page. PenaltyLinks are
	// the Included/Excluded choices; each keeps the rest of the selection.
	Basis        history.ScoringBasis
	NonPenalty   bool
	PenaltyLinks []exploreChoice
}

// pointsOnlyExploreSelection identifies selections where the penalty basis
// cannot change any plotted value or eligibility rule.
func pointsOnlyExploreSelection(query url.Values) bool {
	view := query.Get("view")
	if view == "" {
		view = "teams"
		if query.Has("metric") {
			view = "trend"
		}
	}
	switch view {
	case "parity":
		return true
	case "teams":
		display := query.Get("display")
		if display == "" {
			display = "chart"
		}
		measure := query.Get("measure")
		if measure == "" {
			measure = "difference"
		}
		return display != "table" && display != "quadrant" && measure == "points"
	case "team-seasons":
		measure := query.Get("team-seasons-measure")
		return measure == "" || measure == "points"
	case "team-history":
		return query.Get("measure") == "points"
	case "season-trend":
		trendView := query.Get("trend-view")
		if trendView == "points" {
			return true
		}
		return trendView == "relative" && (query.Get("average-series") == "points" || query.Get("average-series") == "xpoints")
	case "head-to-head":
		return query.Get("h2h-series") == "points"
	default:
		return false
	}
}

// PointsNote says that official points and ASA xPts keep penalties.
func (exploreScoringPage) PointsNote() string { return pointsBasisNote }

// GoalsWord names the goals being counted: "goals" or "non-penalty goals".
func (p exploreScoringPage) GoalsWord() string {
	if p.NonPenalty {
		return "non-penalty goals"
	}
	return "goals"
}

// T rewrites a label for the page's scoring basis.
func (p exploreScoringPage) T(text string) string { return basisText(p.Basis, text) }

type exploreChoice struct {
	Label, Path string
	Selected    bool
}

type exploreDistributionSeason struct {
	Season, AccessibleName string
	GoalsName              string
	Total                  int
	GoalsEligible          bool
	Segments               []exploreDistributionSegment
}

type exploreDistributionSegment struct {
	Index    int
	Label    string
	Count    int
	Percent  string
	X, Width string
	HasWidth bool
}

func exploreScoringPageFor(fromPath string, summaries []history.SeasonScoring, basis history.ScoringBasis) exploreScoringPage {
	page := exploreScoringPage{
		HomePath:       relativeURL(fromPath, "/"),
		StylesheetPath: staticURL(fromPath, "site.css"),
		CatalogPage:    true,
		Basis:          basis,
		NonPenalty:     basis.ExcludesPenalties(),
		PenaltyLinks: []exploreChoice{
			{Label: "Included", Selected: !basis.ExcludesPenalties()},
			{Label: "Excluded", Selected: basis.ExcludesPenalties()},
		},
		Distributions: make([]exploreDistributionSeason, 0, len(summaries)),
	}
	active := []string{}
	for _, summary := range summaries {
		if summary.PlotEligible && summary.GoalsPerMatch != nil && !math.IsNaN(*summary.GoalsPerMatch) && !math.IsInf(*summary.GoalsPerMatch, 0) && *summary.GoalsPerMatch >= 0 {
			page.HasTrendData = true
			if summary.Lifecycle == cache.SourceScopeActive {
				active = append(active, fmt.Sprintf("%s: in progress through %d matches", summary.Season, summary.Played))
			}
		}
		if summary.PlotEligible && summary.XGPerMatch == nil {
			page.XGIncomplete = true
		}
		distribution := exploreDistributionSeasonFor(summary)
		page.Distributions = append(page.Distributions, distribution)
		if distribution.GoalsEligible {
			page.HasDistributionBars = true
		}
	}
	page.ActiveStatus = strings.Join(active, "; ")
	return page
}

func scoringRateText(value *float64) string {
	if value == nil {
		return "Unavailable"
	}
	return fmt.Sprintf("%.2f", *value)
}

func exploreDistributionSeasonFor(summary history.SeasonScoring) exploreDistributionSeason {
	segments := make([]exploreDistributionSegment, 0, len(summary.GoalBins))
	parts := make([]string, 0, len(summary.GoalBins))
	position := 0.0
	// Non-penalty bins count only matches with a penalty breakdown.
	covered := summary.Played
	goalsName := "goals"
	if summary.Basis.ExcludesPenalties() {
		covered = summary.GoalsCovered
		goalsName = "non-penalty goals"
	}
	for index, count := range summary.GoalBins {
		label := goalBinLabel(index)
		percent := binPercent(count, covered)
		width := "0"
		if covered > 0 {
			width = formatChartNumber(float64(count) / float64(covered) * 100)
		}
		segments = append(segments, exploreDistributionSegment{Index: index, Label: label, Count: count, Percent: percent, X: formatChartNumber(position), Width: width, HasWidth: count > 0})
		parts = append(parts, fmt.Sprintf("%s %s: %d matches (%s)", label, goalsName, count, percent))
		if covered > 0 {
			position += float64(count) / float64(covered) * 100
		}
	}
	return exploreDistributionSeason{
		Season:         summary.Season,
		AccessibleName: fmt.Sprintf("%s: %s; %d completed matches total", summary.Season, strings.Join(parts, ", "), covered),
		Total:          covered, GoalsEligible: summary.PlotEligible && covered > 0, Segments: segments,
		GoalsName: goalsName,
	}
}

func formatChartNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func goalBinLabel(index int) string {
	if index >= 4 {
		return "4+"
	}
	return fmt.Sprint(index)
}

func binPercent(count, played int) string {
	if played == 0 {
		return "Unavailable"
	}
	return fmt.Sprintf("%.1f%%", float64(count)/float64(played)*100)
}
