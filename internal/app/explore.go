package app

import (
	"fmt"
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
	// Other holds the server-rendered text and league rows on that basis,
	// rendered beside the page's own and shown by CSS after a switch.
	Other exploreOtherBasisView
	query url.Values
}

// exploreOtherBasisView is what the league views render on the other basis.
// Rows carry their basis key for data-basis-only.
type exploreOtherBasisView struct {
	ActiveStatus     string
	XGIncomplete     bool
	Records          []exploreRecord
	DistributionRows []exploreDistributionSeason
	HomeAdvantage    exploreHomeAdvantageView
}

// tableLabel names a values-table disclosure by its row count, such as
// "Table: all 10 seasons", or "Table: 1 season" for a single row.
func tableLabel(rows int, noun string) string {
	if rows == 1 {
		return "Table: 1 " + noun
	}
	plural := noun + "s"
	if noun == "match" {
		plural = "matches"
	}
	return fmt.Sprintf("Table: all %d %s", rows, plural)
}

// TableLabel is tableLabel for templates.
func (explorePage) TableLabel(rows int, noun string) string { return tableLabel(rows, noun) }

// DistributionTableLabel names Goal distribution's values table on both
// bases, since a season can lack complete non-penalty data.
func (p explorePage) DistributionTableLabel() basisPair {
	return p.otherPair(func(_ history.ScoringBasis, other bool) string {
		if other {
			return tableLabel(len(p.Other.DistributionRows), "season")
		}
		return tableLabel(len(p.DistributionRows), "season")
	})
}

// HomeTableLabel names Home advantage's values table on both bases.
func (p explorePage) HomeTableLabel() basisPair {
	return p.otherPair(func(_ history.ScoringBasis, other bool) string {
		if other {
			return tableLabel(len(p.Other.HomeAdvantage.HomeAdvantageRows), "season")
		}
		return tableLabel(len(p.HomeAdvantageRows), "season")
	})
}

// basisKey names a basis in data-basis and data-basis-only.
func basisKey(basis history.ScoringBasis) string {
	if basis.ExcludesPenalties() {
		return "non-penalty"
	}
	return "all"
}

// BasisKey names the page's basis for data-basis.
func (p explorePage) BasisKey() string { return basisKey(p.Basis) }

// XGIncompleteNote is Scoring trend's incomplete-xG note on both bases.
func (p explorePage) XGIncompleteNote() basisPair {
	note := func(incomplete bool, basis history.ScoringBasis) string {
		if !incomplete {
			return ""
		}
		return basisText(basis, "Some seasons have no complete xG average; their goals remain available.")
	}
	return p.otherPair(func(basis history.ScoringBasis, other bool) string {
		if other {
			return note(p.Other.XGIncomplete, basis)
		}
		return note(p.XGIncomplete, basis)
	})
}

func otherBasisOf(basis history.ScoringBasis) history.ScoringBasis {
	if basis.ExcludesPenalties() {
		return history.AllShots
	}
	return history.NonPenalty
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
	// Basis is the row's basis key; see tagBasis.
	Basis                                           string
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
	page.exploreTeamHistoryView, err = exploreTeamHistoryFor(profileQuery, teams.TeamSeasons, officialTeams.TeamSeasons)
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
		otherHistory, err = exploreTeamHistoryFor(withPenalties(historyQuery, otherBasis), otherTeams.TeamSeasons, officialTeams.TeamSeasons)
	}
	var otherHome exploreHomeAdvantageView
	var otherDistribution exploreDistributionView
	otherScoring := exploreScoringPageFor(r.URL.Path, otherSummaries, otherBasis)
	if err == nil {
		otherHome, err = exploreHomeAdvantage(withPenalties(r.URL.Query(), otherBasis), history.HomeAdvantage(otherSummaries), otherBasis)
	}
	if err == nil {
		otherDistribution, err = exploreDistribution(withPenalties(r.URL.Query(), otherBasis), otherScoring.Distributions)
	}
	if err == nil {
		page.Other = exploreOtherBasisView{
			ActiveStatus: otherScoring.ActiveStatus, XGIncomplete: otherScoring.XGIncomplete,
			Records: tagBasis(exploreRecords(otherSummaries), otherBasis), DistributionRows: tagBasis(otherDistribution.DistributionRows, otherBasis),
			HomeAdvantage: otherHome,
		}
		page.Other.HomeAdvantage.HomeAdvantageRows = tagBasis(otherHome.HomeAdvantageRows, otherBasis)
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
	page.Records = tagBasis(exploreRecords(summaries), basis)
	page.DistributionRows = tagBasis(page.DistributionRows, basis)
	page.HomeAdvantageRows = tagBasis(page.HomeAdvantageRows, basis)
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

// exploreRecords is the Scoring table's rows.
func exploreRecords(summaries []history.SeasonScoring) []exploreRecord {
	var records []exploreRecord
	for _, summary := range summaries {
		if !summary.PlotEligible {
			continue
		}
		records = append(records, exploreRecord{
			Season: summary.Season, Played: summary.Played,
			GoalsPerMatch: scoringRateText(summary.GoalsPerMatch), XGPerMatch: scoringRateText(summary.XGPerMatch),
			GoalsMinusXGPerMatch: scoringRateText(summary.GoalsMinusXGPerMatch),
			Goals:                summary.GoalsPerMatch, XG: summary.XGPerMatch, Gap: summary.GoalsMinusXGPerMatch,
		})
	}
	return records
}

// basisRow is a league table row that names its scoring basis.
type basisRow interface {
	exploreRecord | exploreDistributionSeason | exploreHomeAdvantageRow
}

// tagBasis marks league table rows with their basis, so the page can hold
// both bases' rows and show one.
func tagBasis[Row basisRow](rows []Row, basis history.ScoringBasis) []Row {
	key := basisKey(basis)
	tagged := make([]Row, len(rows))
	for i, row := range rows {
		switch typed := any(&row).(type) {
		case *exploreRecord:
			typed.Basis = key
		case *exploreDistributionSeason:
			typed.Basis = key
		case *exploreHomeAdvantageRow:
			typed.Basis = key
		}
		tagged[i] = row
	}
	return tagged
}

// otherPair pairs a text built on the page's basis with the same text built
// on the other basis, which is empty when the page has no other basis.
func (p explorePage) otherPair(text func(basis history.ScoringBasis, other bool) string) basisPair {
	other := ""
	if p.OtherBasis != nil {
		other = text(otherBasisOf(p.Basis), true)
	}
	return p.Pair(text(p.Basis, false), other)
}

// ActiveStatusNote is Scoring trend's in-progress note on both bases.
func (p explorePage) ActiveStatusNote() basisPair {
	return p.otherPair(func(_ history.ScoringBasis, other bool) string {
		if other {
			return p.Other.ActiveStatus
		}
		return p.ActiveStatus
	})
}

// HomeNotes are Home advantage's coverage and in-progress notes on both
// bases.
func (p explorePage) HomeNotes() []basisPair {
	view := func(other bool) exploreHomeAdvantageView {
		if other {
			return p.Other.HomeAdvantage
		}
		return p.exploreHomeAdvantageView
	}
	return []basisPair{
		p.otherPair(func(basis history.ScoringBasis, other bool) string {
			if missing := view(other).HomeAdvantageXGMissing; missing != "" {
				return fmt.Sprintf("%s is incomplete in %s, so those seasons have no %[1]s mark.", basisText(basis, "xG"), missing)
			}
			return ""
		}),
		p.otherPair(func(_ history.ScoringBasis, other bool) string {
			if missing := view(other).HomeAdvantageXPointsMissing; missing != "" {
				return fmt.Sprintf("xPoints is incomplete in %s, so those seasons have no xPoints mark.", missing)
			}
			return ""
		}),
		p.otherPair(func(_ history.ScoringBasis, other bool) string { return view(other).HomeAdvantageActiveStatus }),
	}
}
