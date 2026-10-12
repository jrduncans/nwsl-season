package app

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

type explorePage struct {
	exploreScoringPage
	exploreDistributionView
	exploreHomeAdvantageView
	exploreParityView
	exploreTeamSeasonsView
	exploreHeadToHeadView
	exploreTeamsView
	exploreTeamHistoryView
	exploreSeasonTrendView
	exploreTeamRankingsView
	View                              string
	ViewGroup                         string
	Records                           []exploreRecord
	ChartData                         []exploreChartRecord
	ChartLibraryPath, ChartLabelsPath string
	// GeometryPath is the versioned link to explore-geometry.js, which must
	// load before explore.js (the page's ScriptPath).
	GeometryPath string
}

// PointsNoteShown reports whether the current view shows official points or
// ASA xPts beside the non-penalty basis, so it must carry the points note.
func (p explorePage) PointsNoteShown(panel string) bool {
	if !p.NonPenalty {
		return false
	}
	switch panel {
	case "teams":
		return p.View == "teams" && (p.TeamDisplay == "table" || (p.TeamDisplay != "quadrant" && p.TeamMeasure == "points"))
	case "team-history":
		return p.View == "team-history"
	case "season-trend":
		return p.View == "season-trend" && (p.TrendView == "points" || (p.TrendView == "relative" && (p.TrendAverageSeries == "points" || p.TrendAverageSeries == "xpoints")))
	}
	return false
}

// exploreViewLabels holds each Explore tab's visible name, keyed by view, or by
// "teams-" and display for Compare teams. Tabs and page titles share them.
var exploreViewLabels = map[string]string{
	"teams-chart": "Actual vs expected", "teams-gap": "Gap to expected", "teams-scatter": "Outlier plot",
	"teams-quadrant": "Scored vs allowed", "teams-table": "Table",
	"team-rankings": "Rankings", "season-trend": "Match by match", "team-history": "Season by season", "head-to-head": "Head to head",
	"trend": "Scoring trend", "distribution": "Goal distribution", "home-advantage": "Home advantage", "parity": "Parity", "team-seasons": "Team-seasons", "table": "Scoring table",
}

// ViewLabel returns the visible name of an Explore tab.
func (explorePage) ViewLabel(key string) string {
	return exploreViewLabels[key]
}

// exploreViewGroup names the navigation group for a view: league-wide views
// across seasons, every team in one season, or a selected team's profile.
func exploreViewGroup(view string) string {
	switch view {
	case "teams":
		return "teams"
	case "team-rankings", "season-trend", "team-history", "head-to-head":
		return "profile"
	default:
		return "league"
	}
}

type exploreChartRecord struct {
	Season string   `json:"season"`
	Played int      `json:"played"`
	Active bool     `json:"active"`
	Goals  *float64 `json:"goals"`
	XG     *float64 `json:"xg"`
	Gap    *float64 `json:"gap"`
	Bins   [5]int   `json:"bins"`
}

type exploreRecord struct {
	Season                                          string
	Played                                          int
	GoalsPerMatch, XGPerMatch, GoalsMinusXGPerMatch string
	Goals, XG, Gap                                  *float64
}

func (a *application) renderExplore(w http.ResponseWriter, r *http.Request, summaries []history.SeasonScoring, archive []cache.HistoricalSeason, basis history.ScoringBasis) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "teams"
		// Scoring trend was once the default view, so its metric links omitted it.
		if r.URL.Query().Has("metric") {
			view = "trend"
		}
	}
	if !slices.Contains([]string{"trend", "distribution", "home-advantage", "parity", "team-seasons", "table", "teams", "team-history", "season-trend", "team-rankings", "head-to-head"}, view) {
		a.renderExploreBadRequest(w, r, fmt.Errorf("view must be trend, distribution, home-advantage, parity, team-seasons, table, teams, team-history, season-trend, team-rankings, or head-to-head"))
		return
	}
	teams, err := exploreTeams(r.URL.Query(), summaries, archive)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	page := explorePage{exploreScoringPage: exploreScoringPageFor(r.URL.Path, summaries, basis), exploreTeamsView: teams, View: view, ViewGroup: exploreViewGroup(view)}
	// The Explore workspace keeps its own selection in the query, so the basis
	// links change only the penalties parameter.
	for index, choice := range []history.ScoringBasis{history.AllShots, history.NonPenalty} {
		page.PenaltyLinks[index].Path = "?" + withPenalties(r.URL.Query(), choice).Encode()
	}
	page.exploreDistributionView, err = exploreDistribution(r.URL.Query(), page.Distributions)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	page.exploreHomeAdvantageView, err = exploreHomeAdvantage(r.URL.Query(), history.HomeAdvantage(summaries), basis)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	page.exploreParityView, err = exploreParity(r.URL.Query(), history.Parity(summaries), teams.TeamSeasons)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	// Head to head validates its shared team against official eligibility below.
	// Keep the other views' payload on the requested basis, including when they
	// are reached by an in-place tab switch from Head to head.
	profileQuery := r.URL.Query()
	if view == "head-to-head" {
		profileQuery = cloneQuery(profileQuery)
		profileQuery.Del("team")
	}
	page.exploreTeamHistoryView, err = exploreTeamHistory(profileQuery, teams.TeamSeasons)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	// Both bases share this archive snapshot, allowing Team-seasons to switch
	// penalties in place. Points always keep all-shots eligibility.
	allSummaries, npSummaries := summaries, summaries
	if basis.ExcludesPenalties() {
		allSummaries, err = history.SummarizeScoringBasis(archive, history.AllShots)
	} else {
		npSummaries, err = history.SummarizeScoringBasis(archive, history.NonPenalty)
	}
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	page.exploreTeamSeasonsView, err = exploreTeamSeasons(r.URL.Query(), history.TeamSeasons(allSummaries, allSummaries, archive), history.TeamSeasons(allSummaries, npSummaries, archive))
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	officialQuery := cloneQuery(r.URL.Query())
	officialQuery.Del("penalties")
	officialTeams, err := exploreTeams(officialQuery, allSummaries, archive)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	officialProfile, err := exploreTeamHistory(officialQuery, officialTeams.TeamSeasons)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	page.exploreHeadToHeadView, err = exploreHeadToHead(r.URL.Query(), allSummaries, npSummaries, archive, officialProfile.HistoryTeam, officialProfile.HistoryTeamOptions)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	page.exploreSeasonTrendView, err = exploreSeasonTrend(r.URL.Query(), teams, page.HistoryTeam)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	// Validate selections before spending forecast capacity. Populate every
	// eligible active season once so browser selections retain one snapshot.
	a.populateExploreForecastBenchmarks(r.Context(), &teams, archive)
	page.exploreTeamsView = teams
	page.exploreSeasonTrendView, _ = exploreSeasonTrend(r.URL.Query(), teams, page.HistoryTeam)
	page.exploreTeamRankingsView = exploreTeamRankings(teams, page.HistoryTeam)
	label := exploreViewLabels[view]
	if view == "teams" {
		label = exploreViewLabels["teams-"+page.TeamDisplay]
	}
	page.Title = label + " · Explore"
	page.ScriptPath = staticURL(r.URL.Path, "explore.js")
	page.GeometryPath = staticURL(r.URL.Path, "explore-geometry.js")
	page.ChartLibraryPath = relativeURL(r.URL.Path, "/static/vendor/chart.js-4.5.1/chart.umd.min.js")
	page.ChartLabelsPath = relativeURL(r.URL.Path, "/static/vendor/chartjs-plugin-datalabels-2.2.0/chartjs-plugin-datalabels.min.js")
	scope := a.requestScope(r)
	rules, verified := a.rulesForSeason(scope.Season, scope.Stage)
	presentation := seasonPresentation{Phase: seasonPhaseActive}
	for _, season := range archive {
		if season.Entry.Season == scope.Season && season.Entry.Stage == scope.Stage {
			presentation = classifySeasonPhase(season.Data, scope.Entry.Inventory)
			presentation.Historical = historicalCatalogScope(scope)
			break
		}
	}
	page.Navigation = seasonNavigationForPresentation(r.URL.Path, scope, "/explore", rules, verified, presentation)

	// Retain the existing calculation and eligibility rules, while keeping the
	// public workspace independent of historical selection and inventory styling.
	for _, summary := range summaries {
		if !summary.PlotEligible {
			continue
		}
		page.Records = append(page.Records, exploreRecord{
			Season: summary.Season, Played: summary.Played,
			GoalsPerMatch: scoringRateText(summary.GoalsPerMatch), XGPerMatch: scoringRateText(summary.XGPerMatch),
			GoalsMinusXGPerMatch: scoringRateText(summary.GoalsMinusXGPerMatch),
			Goals:                summary.GoalsPerMatch, XG: summary.XGPerMatch, Gap: summary.GoalsMinusXGPerMatch,
		})
		page.ChartData = append(page.ChartData, exploreChartRecord{
			Season: summary.Season, Played: summary.Played, Active: summary.Lifecycle == cache.SourceScopeActive,
			Goals: summary.GoalsPerMatch, XG: summary.XGPerMatch, Gap: summary.GoalsMinusXGPerMatch, Bins: summary.GoalBins,
		})
	}
	a.render(w, "explore", page)
}
