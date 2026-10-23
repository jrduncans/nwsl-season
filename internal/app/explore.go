package app

import (
	"net/http"
	"net/url"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

type explorePage struct {
	exploreScoringPage
	exploreDistributionView
	exploreHomeAdvantageView
	exploreParityView
	exploreThresholdView
	exploreTeamSeasonsView
	exploreHeadToHeadView
	exploreTeamsView
	exploreTeamHistoryView
	exploreSeasonTrendView
	exploreTeamRankingsView
	View                              string
	ViewGroup                         string
	PointsOnly                        bool
	Records                           []exploreRecord
	ChartData                         []exploreChartRecord
	ChartLibraryPath, ChartLabelsPath string
	// GeometryPath is the versioned link to explore-geometry.js, which must
	// load before explore.js (the page's ScriptPath).
	GeometryPath string
	MarginsPath  string
	// Views is the view registry, for the tabs and for explore.js.
	Views []exploreViewGroup
	// OtherBasis carries the basis-specific payloads on the basis the page
	// was not rendered on, so explore.js switches penalties in place. It is
	// nil when the selection is invalid on that basis.
	OtherBasis *exploreBasisData
	query      url.Values
}

// exploreBasisData is every payload that changes with the scoring basis.
type exploreBasisData struct {
	// DefaultTeamSeason and DefaultHistoryTeam are the selections an
	// unspecified season or team takes on this basis.
	DefaultTeamSeason  string                       `json:"defaultTeamSeason"`
	DefaultHistoryTeam string                       `json:"defaultHistoryTeam"`
	ChartData          []exploreChartRecord         `json:"chartData"`
	TeamSeasons        []exploreTeamSeason          `json:"teamSeasons"`
	HistoryContext     [4]exploreMetricContext      `json:"historyContext"`
	HomeAdvantage      []exploreHomeAdvantageRecord `json:"homeAdvantage"`
}

// explorePenaltiesSwitch is the Exclude penalties switch. Form names the
// selection form it belongs to when it sits outside that form.
type explorePenaltiesSwitch struct {
	PointsOnly, NonPenalty bool
	Form                   string
}

// PenaltiesSwitch is the data for the penaltiesSelect template.
func (p explorePage) PenaltiesSwitch(form string) explorePenaltiesSwitch {
	return explorePenaltiesSwitch{PointsOnly: p.PointsOnly, NonPenalty: p.NonPenalty, Form: form}
}

// PointsNoteShown reports whether the current view shows official points or
// ASA xPoints beside the non-penalty basis, so it must carry the points note.
func (p explorePage) PointsNoteShown(panel string) bool {
	view, _ := exploreViewFor(panel)
	return p.NonPenalty && p.View == panel && view.PointsNote.matches(p.query, view.Defaults) && !p.PointsOnly
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
	view := exploreRequestedView(r.URL.Query())
	descriptor, ok := exploreViewFor(view)
	if !ok {
		a.renderExploreBadRequest(w, r, unknownExploreViewError())
		return
	}
	teams, err := exploreTeams(r.URL.Query(), summaries, archive)
	if err != nil {
		a.renderExploreBadRequest(w, r, err)
		return
	}
	page := explorePage{exploreScoringPage: exploreScoringPageFor(r.URL.Path, summaries, basis), exploreTeamsView: teams, View: view, ViewGroup: exploreGroupOf(view), PointsOnly: pointsOnlyExploreSelection(r.URL.Query()), Views: exploreViewGroups, query: r.URL.Query()}
	// The Explore workspace keeps its own selection in the query, so the
	// penalties forms change only the penalties parameter.
	page.PenaltiesFormFields = exploreFormFields(withPenalties(r.URL.Query(), history.AllShots))
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
	if view == "head-to-head" || view == "season-trend" {
		profileQuery = cloneQuery(profileQuery)
	}
	if view == "head-to-head" {
		profileQuery.Del("team")
	}
	if view == "season-trend" {
		// `series` belongs to Season by season. Match by match carries valid
		// choices through its form, but ignores malformed carried values.
		ignoreInvalidExploreSeries(profileQuery)
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
	page.exploreThresholdView, err = exploreThresholds(r.URL.Query(), allSummaries, archive)
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
	if view == "season-trend" {
		ignoreInvalidExploreSeries(officialQuery)
	}
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
	if view == "season-trend" && r.URL.Query().Has("trend-sort") && r.URL.Query().Get("trend-sort") != page.TrendSort {
		query := r.URL.Query()
		query.Del("trend-sort")
		query.Del("trend-order")
		redirectRelative(w, "?"+query.Encode(), http.StatusFound)
		return
	}
	if view == "season-trend" && page.TrendLegacySmoothing {
		query := r.URL.Query()
		query.Del("trend-mode")
		query.Del("window")
		query.Set("smoothing", page.TrendSmoothing)
		redirectRelative(w, "?"+query.Encode(), http.StatusFound)
		return
	}
	// parseHeadToHeadSeries already normalized rendering and generated links.
	// After validating every selection, redirect explicit default/legacy URLs
	// to that same canonical state instead of only rendering it canonically.
	if view == "head-to-head" && r.URL.Query().Get("h2h-series") != "" && page.H2HSeries == "actual" {
		query := r.URL.Query()
		query.Del("h2h-series")
		redirectRelative(w, "?"+query.Encode(), http.StatusFound)
		return
	}
	// Validate selections before spending forecast capacity. Populate every
	// eligible active season once so browser selections retain one snapshot.
	// Historical thresholds never depend on forecast availability or capacity.
	if !descriptor.SkipsForecast {
		a.populateExploreForecastBenchmarks(r.Context(), &teams, archive)
	}
	if err := linkExploreTeamMatches(teams.TeamSeasons, &page.H2HData); err != nil {
		a.renderExploreError(w, r, http.StatusInternalServerError, "Explore unavailable", err.Error())
		return
	}
	otherBasis, otherSummaries, otherTeams := history.NonPenalty, npSummaries, exploreTeamsView{}
	if basis.ExcludesPenalties() {
		otherBasis, otherSummaries, otherTeams = history.AllShots, allSummaries, officialTeams
	} else {
		otherTeams, err = exploreTeams(withPenalties(r.URL.Query(), otherBasis), otherSummaries, archive)
	}
	if err == nil {
		if !descriptor.SkipsForecast {
			a.populateExploreForecastBenchmarks(r.Context(), &otherTeams, archive)
		}
		err = linkExploreTeamMatches(otherTeams.TeamSeasons, &page.H2HData)
	}
	var otherHistory exploreTeamHistoryView
	if err == nil {
		// Like the page's own attributes, the defaults name any selected team.
		historyQuery := url.Values{}
		if view != "head-to-head" && r.URL.Query().Has("team") {
			historyQuery.Set("team", r.URL.Query().Get("team"))
		}
		otherHistory, err = exploreTeamHistory(withPenalties(historyQuery, otherBasis), otherTeams.TeamSeasons)
	}
	if err == nil {
		page.OtherBasis = &exploreBasisData{
			DefaultTeamSeason: otherTeams.TeamSeason, DefaultHistoryTeam: otherHistory.HistoryTeam.ID,
			ChartData:      exploreChartRecords(otherSummaries),
			TeamSeasons:    otherTeams.TeamSeasons,
			HistoryContext: exploreHistoryContextData(otherTeams.TeamSeasons, otherBasis),
			HomeAdvantage:  exploreHomeAdvantageRecords(history.HomeAdvantage(otherSummaries)),
		}
	}
	page.exploreTeamsView = teams
	page.exploreSeasonTrendView, _ = exploreSeasonTrend(r.URL.Query(), teams, page.HistoryTeam)
	page.exploreTeamRankingsView = exploreTeamRankings(teams, page.HistoryTeam)
	label := descriptor.Label
	if len(descriptor.Displays) > 0 {
		label = descriptor.displayLabel(page.TeamDisplay)
	}
	page.Title = label + " · Explore"
	page.ScriptPath = staticURL(r.URL.Path, "explore.js")
	page.GeometryPath = staticURL(r.URL.Path, "explore-geometry.js")
	page.MarginsPath = staticURL(r.URL.Path, "explore-margins.js")
	page.ChartLibraryPath = staticURL(r.URL.Path, "vendor/chart.js-4.5.1/chart.umd.min.js")
	page.ChartLabelsPath = staticURL(r.URL.Path, "vendor/chartjs-plugin-datalabels-2.2.0/chartjs-plugin-datalabels.min.js")
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
	}
	page.ChartData = exploreChartRecords(summaries)
	a.render(w, "explore", page)
}

// exploreChartRecords is Scoring trend and Goal distribution's payload.
func exploreChartRecords(summaries []history.SeasonScoring) []exploreChartRecord {
	var records []exploreChartRecord
	for _, summary := range summaries {
		if summary.PlotEligible {
			records = append(records, exploreChartRecord{
				Season: summary.Season, Played: summary.Played, Active: summary.Lifecycle == cache.SourceScopeActive,
				Goals: summary.GoalsPerMatch, XG: summary.XGPerMatch, Gap: summary.GoalsMinusXGPerMatch, Bins: summary.GoalBins,
			})
		}
	}
	return records
}
