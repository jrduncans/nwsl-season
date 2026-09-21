package app

import (
	"fmt"
	"net/http"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

type explorePage struct {
	historyPage
	exploreTeamsView
	exploreTeamHistoryView
	View                              string
	Records                           []exploreRecord
	ChartData                         []exploreChartRecord
	ChartLibraryPath, ChartLabelsPath string
}

type exploreChartRecord struct {
	Season string   `json:"season"`
	Played int      `json:"played"`
	Goals  *float64 `json:"goals"`
	XG     *float64 `json:"xg"`
	Bins   [5]int   `json:"bins"`
}

type exploreRecord struct {
	historyRowView
	Goals, XG, Gap *float64
}

func (a *application) renderExplore(w http.ResponseWriter, r *http.Request, summaries []history.SeasonScoring, archive []cache.HistoricalSeason) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "trend"
	}
	if view != "trend" && view != "distribution" && view != "table" && view != "teams" && view != "team-history" {
		a.renderHistoryBadRequest(w, r, fmt.Errorf("view must be trend, distribution, table, teams, or team-history"))
		return
	}
	teams, err := exploreTeams(r.URL.Query(), summaries, archive)
	if err != nil {
		a.renderHistoryBadRequest(w, r, err)
		return
	}
	page := explorePage{historyPage: historyPageForMetric(r.URL.Path, summaries, "", historyMetricCompare), exploreTeamsView: teams, View: view}
	page.exploreTeamHistoryView, err = exploreTeamHistory(r.URL.Query(), teams.TeamSeasons)
	if err != nil {
		a.renderHistoryBadRequest(w, r, err)
		return
	}
	page.Title = "Explore"
	page.ScriptPath = relativeURL(r.URL.Path, "/static/explore.js")
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
			historyRowView: historyRow(summary, r.URL.Path, "", historyMetricCompare),
			Goals:          summary.GoalsPerMatch, XG: summary.XGPerMatch, Gap: summary.GoalsMinusXGPerMatch,
		})
		page.ChartData = append(page.ChartData, exploreChartRecord{
			Season: summary.Season, Played: summary.Played,
			Goals: summary.GoalsPerMatch, XG: summary.XGPerMatch, Bins: summary.GoalBins,
		})
	}
	a.render(w, "explore", page)
}
