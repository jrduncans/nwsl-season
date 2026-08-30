package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	evaluationdata "github.com/jrduncans/nwsl-season/docs"
	"github.com/jrduncans/nwsl-season/internal/backtest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/strength"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultSeason   = "2026"
	defaultStage    = "Regular Season"
	remainingStatus = fixtures.PreMatchStatus

	// Forecast Lab simulates complete seasons and is deliberately constrained so
	// a few expensive requests cannot consume the entire HTTP process.
	defaultForecastConcurrency = 4
	defaultForecastTimeout     = 15 * time.Second
)

// Store reads page data from the local cache.
type Store interface {
	Status(context.Context, string, string) (cache.Status, error)
	Season(context.Context, string, string) (cache.SeasonData, error)
}

// Options contains season rules that are intentionally explicit at the HTTP boundary.
type Options struct {
	CurrentSeason       string
	Stage               string
	Rules               competition.Rules
	ForecastIterations  int
	ForecastConcurrency int
	ForecastTimeout     time.Duration
	Location            *time.Location
}

// NewHandler wires the application routes using the current-season defaults.
func NewHandler(store Store) http.Handler {
	return NewHandlerWithOptions(store, Options{})
}

// NewHandlerWithOptions wires the application routes with explicit season rules.
func NewHandlerWithOptions(store Store, options Options) http.Handler {
	return NewApplication(store, options)
}

// Application is the HTTP application plus its process-local forecast cache.
// It exposes forecast warm-up for server startup while still implementing
// http.Handler for callers that only need to serve requests.
type Application struct {
	handler http.Handler
	app     *application
}

// NewApplication wires the application routes with explicit season rules.
func NewApplication(store Store, options Options) *Application {
	return newApplicationWithForecastExecutor(store, options, nil)
}

// ServeHTTP implements http.Handler.
func (a *Application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
}

func newHandlerWithForecastExecutor(store Store, options Options, forecasts *forecastExecutor) http.Handler {
	return newApplicationWithForecastExecutor(store, options, forecasts)
}

func newApplicationWithForecastExecutor(store Store, options Options, forecasts *forecastExecutor) *Application {
	options = defaultOptions(options)
	pages := template.Must(template.ParseFS(pageFiles, "templates/*.html"))
	staticFS, err := fs.Sub(pageFiles, "static")
	if err != nil {
		panic(err)
	}

	if forecasts == nil {
		forecasts = newForecastExecutor(options.ForecastConcurrency, options.ForecastTimeout)
	}
	application := &application{store: store, options: options, pages: pages, forecasts: forecasts}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", application.root)
	mux.HandleFunc("GET /seasons", application.seasons)
	// Compatibility routes redirect to the primary public stage; rendered pages
	// always carry an explicit stage slug.
	mux.HandleFunc("GET /seasons/{season}", application.season)
	mux.HandleFunc("GET /seasons/{season}/fixtures", application.fixtures)
	mux.HandleFunc("GET /seasons/{season}/schedule-difficulty", application.scheduleDifficulty)
	mux.HandleFunc("GET /seasons/{season}/forecast", application.forecast)
	mux.HandleFunc("GET /seasons/{season}/model-evaluation", application.modelEvaluation)
	mux.HandleFunc("GET /seasons/{season}/clinching", application.clinching)
	mux.HandleFunc("GET /seasons/{season}/{stage}", application.season)
	mux.HandleFunc("GET /seasons/{season}/{stage}/fixtures", application.fixtures)
	mux.HandleFunc("GET /seasons/{season}/{stage}/schedule-difficulty", application.scheduleDifficulty)
	mux.HandleFunc("GET /seasons/{season}/{stage}/forecast", application.forecast)
	mux.HandleFunc("GET /seasons/{season}/{stage}/model-evaluation", application.modelEvaluation)
	mux.HandleFunc("GET /seasons/{season}/{stage}/clinching", application.clinching)
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /cache/status", cacheStatus(store, options.CurrentSeason, options.Stage, options.Rules.Version))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	return &Application{handler: withBasePath(mux), app: application}
}

func (a *application) clinching(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("stage") == "" {
		a.redirectPrimaryStage(w, r)
		return
	}
	scope := a.requestScope(r)
	if !scope.Valid {
		http.NotFound(w, r)
		return
	}
	rules, verified := a.rulesForSeason(scope.Season, scope.Stage)
	if !scope.clinchingAvailable(rules, verified) {
		a.renderUnavailableFeature(w, r, scope, "Clinching scenarios")
		return
	}
	page, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	if page.Phase == seasonPhaseComplete {
		a.renderUnavailableFeatureWithNavigation(w, r, scope, "Clinching scenarios", page.Navigation)
		return
	}
	rules, _ = a.rulesForSeason(page.Season, page.Stage)
	page.Title = page.Season + " clinching scenarios"
	view := clinchingPage{seasonPage: page, Actionable: []clinchingRowView{}, NoHelp: []clinchingRowView{}, Elimination: []clinchingRowView{}, AlreadyClinched: []clinchingRowView{}, SlateGroups: []fixtureGroupView{}}
	store, ok := a.store.(interface {
		ScenarioForSnapshot(context.Context, string, string, string) (cache.ScenarioSnapshot, bool, error)
	})
	if !ok || page.Season == "" {
		view.State = "Scenario calculations are unavailable."
		a.render(w, "clinching", view)
		return
	}
	data, err := a.loadSeasonData(r.Context(), page.Season, page.Stage)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	teamLabels := map[string]string{}
	teamViews := map[string]teamNameView{}
	for _, t := range data.Teams {
		teamLabels[t.ID] = standings.DisplayName(t)
		teamViews[t.ID] = teamName(t)
	}
	standingsPositions := map[string]int{}
	for _, row := range page.Standings {
		standingsPositions[row.TeamID] = row.Position
	}
	games := map[string]cache.Game{}
	for _, g := range data.Games {
		games[g.ASAID] = g
	}
	qualification := map[string]cache.QualificationStatus{}
	if store, ok := a.store.(interface {
		QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
	}); ok {
		value, found, err := store.QualificationForSnapshot(r.Context(), data.FixtureSnapshotID, rules.Version)
		if err != nil {
			a.renderError(w, r, err)
			return
		}
		if found {
			for _, status := range value.Statuses {
				qualification[status.TeamID+"\x00"+string(status.Achievement)] = status
				if status.Status == clinching.Clinched {
					view.AlreadyClinched = append(view.AlreadyClinched, clinchingRowView{Team: teamViews[status.TeamID], Achievement: achievementPhrase(status.Achievement), AchievementRank: status.TopK, StandingsPosition: standingsPositions[status.TeamID], Clauses: []string{}, Necessary: []string{}})
				}
			}
		}
	}
	sort.Slice(view.AlreadyClinched, func(i, j int) bool { return clinchingRowLess(view.AlreadyClinched[i], view.AlreadyClinched[j]) })
	snapshot, found, err := store.ScenarioForSnapshot(r.Context(), data.FixtureSnapshotID, rules.Version, scenarios.DefinitionVersion)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	if !found {
		view.State = "Recalculation pending."
		a.render(w, "clinching", view)
		return
	}
	view.Slate = snapshot.Run.Slate
	view.SlateStartsAtUTC = snapshot.Run.Slate.StartsAtUTC.UTC().Format(time.RFC3339)
	view.SlateStartsAt = snapshot.Run.Slate.StartsAtUTC.In(a.options.Location).Format("Mon Jan 2, 3:04 PM MST")
	view.SlateLatestUTC = snapshot.Run.Slate.LatestKickoffUTC.UTC().Format(time.RFC3339)
	view.SlateLatest = snapshot.Run.Slate.LatestKickoffUTC.In(a.options.Location).Format("Mon Jan 2, 3:04 PM MST")
	view.SlateCutoffUTC = snapshot.Run.Slate.CutoffUTC.UTC().Format(time.RFC3339)
	view.SlateCutoff = snapshot.Run.Slate.CutoffUTC.In(a.options.Location).Format("Mon Jan 2, 3:04 PM MST")
	slateIDs := map[string]bool{}
	for _, id := range snapshot.Run.Slate.FixtureIDs {
		slateIDs[id] = true
	}
	slateData := data
	slateData.Games = make([]cache.Game, 0, len(snapshot.Run.Slate.FixtureIDs))
	for _, game := range data.Games {
		if slateIDs[game.ASAID] {
			slateData.Games = append(slateData.Games, game)
		}
	}
	view.SlateGroups = fixtureGroups(slateData, a.options.Location)
	for _, v := range snapshot.Results {
		team := teamViews[v.TeamID]
		achievement := achievementPhrase(v.Achievement)
		if v.AlreadyEliminated || v.CanBeEliminated {
			row := clinchingRowView{Team: team, Achievement: achievement, AchievementRank: v.TopK, StandingsPosition: standingsPositions[v.TeamID], Clauses: []string{}, Necessary: []string{}, AlreadyEliminated: v.AlreadyEliminated}
			for _, c := range v.EliminationClauses {
				row.Clauses = append(row.Clauses, clauseSentence(c, teamLabels, games))
			}
			view.Elimination = append(view.Elimination, row)
		}
		noHelp := ""
		noHelpGuaranteed := false
		noHelpPath := clinching.NoHelpPath{}
		if status, ok := qualification[v.TeamID+"\x00"+string(v.Achievement)]; ok {
			noHelpPath = status.NoHelp
			noHelp = noHelpText(status.NoHelp, team.Name, achievement)
			noHelpGuaranteed = status.NoHelp.State == clinching.NoHelpGuaranteed && len(status.NoHelp.FixtureIDs) > 0
		}
		if !v.CanClinch && !noHelpGuaranteed {
			continue
		}
		row := clinchingRowView{Team: team, Achievement: achievement, AchievementRank: v.TopK, StandingsPosition: standingsPositions[v.TeamID], NoHelp: noHelp, Clauses: []string{}, Necessary: []string{}}
		if noHelpGuaranteed {
			row.NoHelpFixtures = noHelpFixtureText(noHelpPath, v.TeamID, games, teamLabels)
			row.NoHelpFixtureCount = len(noHelpPath.FixtureIDs)
		}
		for _, c := range v.Clauses {
			row.Clauses = append(row.Clauses, clauseSentence(c, teamLabels, games))
		}
		for _, n := range v.Necessary {
			row.Necessary = append(row.Necessary, conditionText(n, teamLabels, games))
		}
		if !v.CanClinch {
			view.NoHelp = append(view.NoHelp, row)
			continue
		}
		if v.Limitation != "" {
			row.Limitation = "Some additional clinching paths may not be shown."
		}
		view.Actionable = append(view.Actionable, row)
	}
	sort.Slice(view.Actionable, func(i, j int) bool { return clinchingRowLess(view.Actionable[i], view.Actionable[j]) })
	sort.Slice(view.NoHelp, func(i, j int) bool { return clinchingNoHelpRowLess(view.NoHelp[i], view.NoHelp[j]) })
	sort.Slice(view.Elimination, func(i, j int) bool { return clinchingRowLess(view.Elimination[i], view.Elimination[j]) })
	view.NoHelpTeams = groupClinchingNoHelpRows(view.NoHelp)
	view.ClinchingTeams = clinchingTeams(view.Actionable, view.NoHelp, view.Elimination)
	a.render(w, "clinching", view)
}

func clinchingRowLess(left, right clinchingRowView) bool {
	if left.AchievementRank != right.AchievementRank {
		return left.AchievementRank < right.AchievementRank
	}
	if left.StandingsPosition != right.StandingsPosition {
		if left.StandingsPosition == 0 {
			return false
		}
		if right.StandingsPosition == 0 {
			return true
		}
		return left.StandingsPosition < right.StandingsPosition
	}
	if left.Team.Name != right.Team.Name {
		return left.Team.Name < right.Team.Name
	}
	return left.Achievement < right.Achievement
}

func clinchingNoHelpRowLess(left, right clinchingRowView) bool {
	if left.NoHelpFixtureCount != right.NoHelpFixtureCount {
		return left.NoHelpFixtureCount < right.NoHelpFixtureCount
	}
	return clinchingRowLess(left, right)
}

func groupClinchingNoHelpRows(rows []clinchingRowView) []clinchingTeamView {
	groups := []clinchingTeamView{}
	byTeam := map[string]int{}
	for _, row := range rows {
		index, ok := byTeam[row.Team.ID]
		if !ok {
			byTeam[row.Team.ID] = len(groups)
			groups = append(groups, clinchingTeamView{Team: row.Team, Paths: []clinchingRowView{}})
			index = len(groups) - 1
		}
		groups[index].Paths = append(groups[index].Paths, row)
	}
	return groups
}

func clinchingTeams(groups ...[]clinchingRowView) []teamNameView {
	byID := map[string]teamNameView{}
	for _, rows := range groups {
		for _, row := range rows {
			byID[row.Team.ID] = row.Team
		}
	}
	teams := make([]teamNameView, 0, len(byID))
	for _, team := range byID {
		teams = append(teams, team)
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].Name < teams[j].Name })
	return teams
}

type application struct {
	store     Store
	options   Options
	pages     *template.Template
	forecasts *forecastExecutor
}

func defaultOptions(options Options) Options {
	if options.CurrentSeason == "" {
		options.CurrentSeason = defaultSeason
	}
	if options.Stage == "" {
		options.Stage = defaultStage
	}
	if !hasRules(options.Rules) {
		if rules, ok := competition.ForSeason(options.CurrentSeason, options.Stage); ok {
			options.Rules = rules
		}
	}
	if hasRules(options.Rules) {
		if err := options.Rules.Validate(); err != nil {
			panic(fmt.Sprintf("invalid application rules: %v", err))
		}
	}
	if options.ForecastIterations <= 0 {
		options.ForecastIterations = defaultForecastIterations
	}
	if options.ForecastConcurrency <= 0 {
		options.ForecastConcurrency = defaultForecastConcurrency
	}
	if options.ForecastTimeout <= 0 {
		options.ForecastTimeout = defaultForecastTimeout
	}
	if options.Location == nil {
		options.Location = time.Local
	}
	return options
}

// rulesForSeason returns an isolated copy of the rules for an HTTP request.
// The configured catalog scope keeps its explicit Options value for
// compatibility. Uncataloged scopes deliberately have no presentation rules.
func (a *application) rulesForSeason(season string, stages ...string) (competition.Rules, bool) {
	stage := a.options.Stage
	if len(stages) != 0 {
		stage = stages[0]
	}
	entry, ok := competition.Lookup(season, stage)
	if !ok || entry.Rules == nil {
		return competition.Rules{}, false
	}
	if season == a.options.CurrentSeason && stage == a.options.Stage && hasRules(a.options.Rules) {
		return a.options.Rules.Copy(), true
	}
	return entry.Rules.Copy(), true
}

func hasRules(rules competition.Rules) bool {
	return rules.Season != "" || rules.Stage != "" || rules.Version != "" || rules.ExpectedTeams != 0 || rules.GamesPerTeam != 0 || len(rules.Achievements) != 0
}

type requestCompetition struct {
	Season    string
	Stage     string
	Entry     competition.Entry
	Cataloged bool
	Valid     bool
}

func (a *application) requestScope(r *http.Request) requestCompetition {
	season := r.PathValue("season")
	if season == "" {
		season = a.options.CurrentSeason
	}
	slug := r.PathValue("stage")
	if slug == "" {
		if entry, ok := competition.PrimaryEntry(season); ok {
			return requestCompetition{Season: season, Stage: entry.Stage, Entry: entry, Cataloged: true, Valid: true}
		}
		return requestCompetition{Season: season, Stage: defaultStage, Entry: competition.Entry{Season: season, Stage: defaultStage, Slug: "regular-season"}, Valid: true}
	}
	entry, cataloged := competition.LookupSlug(season, slug)
	if cataloged {
		return requestCompetition{Season: season, Stage: entry.Stage, Entry: entry, Cataloged: true, Valid: true}
	}
	if len(competition.PublicEntriesForSeason(season)) != 0 {
		return requestCompetition{Season: season, Valid: false}
	}
	if slug == "regular-season" {
		return requestCompetition{Season: season, Stage: defaultStage, Entry: competition.Entry{Season: season, Stage: defaultStage, Slug: "regular-season"}, Valid: true}
	}
	return requestCompetition{Season: season, Valid: false}
}

func (s requestCompetition) supports(capability competition.Capability) bool {
	return s.Cataloged && s.Entry.Supports(capability)
}

func (s requestCompetition) fixturesAvailable() bool {
	return !s.Cataloged || s.supports(competition.CapabilityFixtures)
}

func (s requestCompetition) xgAvailable() bool {
	return !s.Cataloged || s.supports(competition.CapabilityXG)
}

func (s requestCompetition) standingsAvailable() bool {
	return s.supports(competition.CapabilityStandings)
}

func (s requestCompetition) scheduleDifficultyAvailable() bool {
	return s.supports(competition.CapabilityScheduleDifficulty)
}

func (s requestCompetition) forecastCapabilityAvailable() bool {
	return s.supports(competition.CapabilityForecast)
}

func (s requestCompetition) qualificationAvailable() bool {
	return s.supports(competition.CapabilityQualification)
}

func (s requestCompetition) scenariosAvailable() bool {
	return s.supports(competition.CapabilityScenarios)
}

func (s requestCompetition) forecastAvailable(rules competition.Rules, verified bool) bool {
	return s.forecastCapabilityAvailable() && verified && hasPlayoffAchievement(rules)
}

func (s requestCompetition) clinchingAvailable(rules competition.Rules, verified bool) bool {
	return s.qualificationAvailable() && s.scenariosAvailable() && verified && hasRules(rules)
}

func hasPlayoffAchievement(rules competition.Rules) bool {
	for _, achievement := range rules.Achievements {
		if achievement.ID == competition.AchievementPlayoffs {
			return true
		}
	}
	return false
}

func playoffPlaces(rules competition.Rules) int {
	for _, achievement := range rules.Achievements {
		if achievement.ID == competition.AchievementPlayoffs {
			return achievement.TopK
		}
	}
	panic("competition rules have no playoff achievement")
}

func freshnessValues(finishedAt time.Time, location *time.Location) (string, string) {
	return finishedAt.UTC().Format(time.RFC3339), finishedAt.In(location).Format("Jan 2, 2006 at 3:04 PM MST")
}

func (a *application) root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	entry, ok := competition.PrimaryEntry(a.options.CurrentSeason)
	if !ok {
		redirectRelative(w, "seasons/"+url.PathEscape(a.options.CurrentSeason)+"/regular-season", http.StatusSeeOther)
		return
	}
	redirectRelative(w, "seasons/"+url.PathEscape(a.options.CurrentSeason)+"/"+entry.Slug, http.StatusSeeOther)
}

func (a *application) seasons(w http.ResponseWriter, r *http.Request) {
	readinessByScope := map[string]cache.SeasonReadinessSnapshot{}
	if store, ok := a.store.(interface {
		SeasonReadinesses(context.Context) ([]cache.SeasonReadinessSnapshot, error)
	}); ok {
		readinesses, err := store.SeasonReadinesses(r.Context())
		if err != nil {
			a.renderError(w, r, fmt.Errorf("load season archive readiness: %w", err))
			return
		}
		for _, readiness := range readinesses {
			readinessByScope[readiness.Scope.Season+"\x00"+readiness.Scope.Stage] = readiness
		}
	}

	page := seasonsPage{
		Title:          "Seasons",
		HomePath:       relativeURL(r.URL.Path, "/"),
		StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"),
		ScriptPath:     relativeURL(r.URL.Path, "/static/standings.js"),
		CatalogPage:    true,
		Seasons:        seasonArchiveItems(r.URL.Path, a.options.CurrentSeason, readinessByScope),
	}
	a.render(w, "seasons", page)
}

func seasonArchiveItems(from, currentSeason string, readinessByScope map[string]cache.SeasonReadinessSnapshot) []seasonArchiveItem {
	items := []seasonArchiveItem{}
	for _, entry := range competition.PublicEntries() {
		if !entry.Primary {
			continue
		}
		item := seasonArchiveItem{Season: entry.Season, Current: entry.Season == currentSeason}
		if item.Current {
			item.Status = "Current season"
		} else {
			item.Status = "Historical season"
		}
		if readiness, ok := readinessByScope[entry.Season+"\x00"+entry.Stage]; ok {
			switch readiness.Readiness {
			case cache.SourceReadinessNotPublished:
				item.Status += " · Not published"
			case cache.SourceReadinessUnknown:
				item.Status += " · Not loaded"
			case cache.SourceReadinessAvailable:
				if readiness.Completeness == cache.InventoryCompletenessIncomplete {
					item.Status += " · Partial data"
				}
			}
		}

		base := stageURL(entry.Season, entry.Slug)
		item.Links = append(item.Links, navigationItem{Label: "Standings", Path: relativeURL(from, base)})
		if entry.Supports(competition.CapabilityFixtures) {
			label := "Results"
			if item.Current {
				label = "Results & fixtures"
			}
			item.Links = append(item.Links, navigationItem{Label: label, Path: relativeURL(from, base+"/fixtures")})
		}
		items = append(items, item)
	}
	return items
}

func (a *application) season(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("stage") == "" {
		a.redirectPrimaryStage(w, r)
		return
	}
	scope := a.requestScope(r)
	if !scope.Valid {
		http.NotFound(w, r)
		return
	}
	view := r.URL.Query().Get("view")
	if view != "" {
		a.renderScenarioBadRequest(w, r, "Invalid season view", fmt.Errorf("unsupported season view %q", view))
		return
	}
	page, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	if scope.Entry.Kind == competition.StageKindKnockout {
		a.render(w, "fixtures", page)
		return
	}
	a.render(w, "season", page)
}

func (a *application) redirectPrimaryStage(w http.ResponseWriter, r *http.Request) {
	season := r.PathValue("season")
	entry, ok := competition.PrimaryEntry(season)
	slug := "regular-season"
	if ok {
		slug = entry.Slug
	}
	target := "/seasons/" + url.PathEscape(season) + "/" + slug
	if tail := strings.TrimPrefix(r.URL.Path, "/seasons/"+url.PathEscape(season)); tail != "" {
		target += tail
	}
	// Keep compatibility redirects relative so a reverse proxy's mount path
	// (for example, /nwsl-season/) stays in the browser URL.
	relative := relativeURL(r.URL.Path, target)
	if r.URL.RawQuery != "" {
		relative += "?" + r.URL.RawQuery
	}
	redirectRelative(w, relative, http.StatusSeeOther)
}

func (a *application) fixtures(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("stage") == "" {
		a.redirectPrimaryStage(w, r)
		return
	}
	scope := a.requestScope(r)
	if !scope.Valid {
		http.NotFound(w, r)
		return
	}
	if !scope.fixturesAvailable() {
		a.renderUnavailableFeature(w, r, scope, "Results and fixtures")
		return
	}
	var outlooks func(cache.SeasonData) map[string]fixtureOutlookView
	if scope.forecastCapabilityAvailable() {
		outlooks = fixtureOutlooks
	}
	page, err := a.loadSeasonPageFor(r, outlooks)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	a.render(w, "fixtures", page)
}

func (a *application) scheduleDifficulty(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("stage") == "" {
		a.redirectPrimaryStage(w, r)
		return
	}
	scope := a.requestScope(r)
	if !scope.Valid {
		http.NotFound(w, r)
		return
	}
	if !scope.scheduleDifficultyAvailable() {
		a.renderUnavailableFeature(w, r, scope, "Schedule difficulty")
		return
	}
	page, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	if page.Phase == seasonPhaseComplete {
		a.renderUnavailableFeatureWithNavigation(w, r, scope, "Schedule difficulty", page.Navigation)
		return
	}
	a.render(w, "schedule-difficulty", page)
}

func (a *application) modelEvaluation(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("stage") == "" {
		a.redirectPrimaryStage(w, r)
		return
	}
	scope := a.requestScope(r)
	if !scope.Valid {
		http.NotFound(w, r)
		return
	}
	rules, verified := a.rulesForSeason(scope.Season, scope.Stage)
	if !scope.forecastAvailable(rules, verified) {
		a.renderUnavailableFeature(w, r, scope, "Model evaluation")
		return
	}
	page, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	var report backtest.Report
	if err := json.Unmarshal(evaluationdata.ModelEvaluationV1, &report); err != nil {
		a.renderError(w, r, fmt.Errorf("read model evaluation evidence: %w", err))
		return
	}
	view, err := evaluationView(report)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	view.seasonPage = page
	view.Title = "Model evaluation"
	a.render(w, "model-evaluation", view)
}

func (a *application) loadSeasonPage(r *http.Request) (seasonPage, error) {
	return a.loadSeasonPageFor(r, nil)
}

func (a *application) loadSeasonPageFor(r *http.Request, outlooksFor func(cache.SeasonData) map[string]fixtureOutlookView) (seasonPage, error) {
	if a.store == nil {
		return seasonPage{}, fmt.Errorf("season cache unavailable")
	}
	scope := a.requestScope(r)
	season := scope.Season
	if !scope.Valid {
		return seasonPage{}, fmt.Errorf("unknown competition stage")
	}
	rules, rulesVerified := a.rulesForSeason(season, scope.Stage)
	data, err := a.loadSeasonData(r.Context(), season, scope.Stage)
	if err != nil {
		return seasonPage{}, fmt.Errorf("load %s season: %w", season, err)
	}
	if len(data.Games) == 0 {
		if historicalCatalogScope(scope) {
			return a.historicalLoadPage(r, scope)
		}
		if scope.Cataloged && scope.Entry.Kind == competition.StageKindKnockout {
			return a.playoffLoadPage(r, scope)
		}
		return seasonPage{}, fmt.Errorf("no cached games found for %s %s", season, scope.Stage)
	}
	presentation := classifySeasonPhase(data, scope.Entry.Inventory)
	presentation.Historical = historicalCatalogScope(scope)

	var standingsView []tableRowView
	var totalTable []standings.TableRow
	var scheduleView strengthView
	completedMatches, xgAvailable := 0, 0
	domainGames := standingsGames(data.Games)
	showScheduleDifficulty := scope.scheduleDifficultyAvailable() && phaseSupportsRemainingFeatures(presentation.Phase)
	if showScheduleDifficulty {
		venue := forecastVenueSample(data)
		scheduleStrength, scheduleErr := strength.CalculateWithVenueSampleAndScheduleLoad(data.Teams, domainGames, strength.VenueSample{Matches: venue.Matches, HomePoints: venue.HomePoints, AwayPoints: venue.AwayPoints})
		if scheduleErr != nil {
			return seasonPage{}, fmt.Errorf("calculate schedule difficulty: %w", scheduleErr)
		}
		scheduleView = strengthViewFrom(scheduleStrength)
	}
	showStandings := scope.standingsAvailable() && presentation.Phase != seasonPhaseUpcoming
	if showStandings {
		actualTable := standings.Calculate(data.Teams, domainGames, standings.PerGameRules())
		totalTable = standings.Calculate(data.Teams, domainGames, standings.OfficialTotalRules())
		playoffLine := scope.Entry.PlayoffPlaces
		standingsView = tableViews(actualTable, playoffLine)
		if showScheduleDifficulty {
			standingsView = addScheduleIndicators(standingsView, scheduleView)
		}
		if scope.xgAvailable() {
			standingsView, xgAvailable, completedMatches = addXGValues(standingsView, data)
		}
	}
	var outlooks map[string]fixtureOutlookView
	if outlooksFor != nil && phaseSupportsRemainingFeatures(presentation.Phase) {
		outlooks = outlooksFor(data)
	}
	resultFixtureGroups, upcomingFixtureGroups := fixtureGroupsByStatusWithOutlooksFor(data, a.options.Location, outlooks, scope.xgAvailable())
	hasResults := len(resultFixtureGroups) > 0
	hasUpcoming := len(upcomingFixtureGroups) > 0
	presentation.HasUpcoming = hasUpcoming
	retrospective := presentation.Phase == seasonPhaseComplete || (presentation.Historical && !hasUpcoming)
	standingsCaption := "Current standings"
	standingsXGCaption := "xG standings, ordered by xPts"
	standingsMode := "per-game"
	if presentation.Phase == seasonPhaseComplete {
		standingsMode = "total"
		if presentation.FinalStandingsSafe {
			standingsCaption = "Final standings"
			standingsXGCaption = "Final xG standings, ordered by xPts"
		} else {
			standingsCaption = "Season standings"
			standingsXGCaption = "Season xG standings, ordered by xPts"
		}
	} else if presentation.Historical {
		standingsCaption = "Historical standings"
		standingsXGCaption = "Historical xG standings, ordered by xPts"
		if !hasUpcoming {
			standingsMode = "total"
		}
	}
	fixturesHeading := "Results and fixtures"
	if presentation.Phase == seasonPhaseUpcoming {
		fixturesHeading = "Schedule"
	} else if presentation.Phase == seasonPhaseComplete || (presentation.Historical && !hasUpcoming) {
		fixturesHeading = "Results"
	}
	var featureSeasonSelector []seasonSelectorItem
	if scope.Entry.Primary {
		if featureSuffix, ok := seasonFeatureSuffix(r.URL.Path, scope.Entry.Slug); ok {
			featureSeasonSelector = seasonFeatureSelector(r.URL.Path, season, featureSuffix)
		}
	}
	page := seasonPage{
		Title:                  season + " NWSL season",
		Season:                 season,
		Stage:                  scope.Stage,
		HomePath:               relativeURL(r.URL.Path, "/"),
		StylesheetPath:         relativeURL(r.URL.Path, "/static/site.css"),
		ScriptPath:             relativeURL(r.URL.Path, "/static/standings.js"),
		SeasonsPath:            seasonArchiveURL(r.URL.Path),
		HasStandings:           showStandings,
		HasFixtures:            scope.fixturesAvailable(),
		HasXG:                  scope.xgAvailable(),
		HasScheduleDifficulty:  showScheduleDifficulty,
		HasForecast:            scope.forecastAvailable(rules, rulesVerified) && phaseSupportsRemainingFeatures(presentation.Phase),
		HasResults:             hasResults,
		HasUpcomingFixtures:    hasUpcoming,
		ShowFixtureViewToggle:  hasResults && hasUpcoming,
		ShowUpcomingSeason:     presentation.Phase == seasonPhaseUpcoming,
		FixturesHeading:        fixturesHeading,
		StandingsCaption:       standingsCaption,
		StandingsXGCaption:     standingsXGCaption,
		StandingsMode:          standingsMode,
		Phase:                  presentation.Phase,
		Standings:              addTotalPositions(standingsView, totalTable, scope.Entry.PlayoffPlaces),
		Strength:               scheduleView,
		ResultFixtureGroups:    resultFixtureGroups,
		UpcomingFixtureGroups:  upcomingFixtureGroups,
		FixtureTeams:           fixtureTeams(data.Teams),
		ForecastPath:           relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)+"/forecast"),
		ClinchingPath:          relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)+"/clinching"),
		CurrentPath:            relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)),
		SeasonSelector:         featureSeasonSelector,
		StageSelector:          stageSelector(r.URL.Path, season, scope.Stage),
		SeasonPath:             relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)),
		FixturesPath:           relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)+"/fixtures"),
		ScheduleDifficultyPath: relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)+"/schedule-difficulty"),
		HasFixtureOutlooks:     !retrospective && scope.forecastCapabilityAvailable() && len(outlooks) > 0,
	}
	if scope.xgAvailable() && completedMatches > 0 && xgAvailable < completedMatches {
		page.XGWarning = fmt.Sprintf("xG is unavailable for %d of %d completed matches, so the xG columns are incomplete.", completedMatches-xgAvailable, completedMatches)
	}
	page.Navigation = seasonNavigationForPresentation(r.URL.Path, scope, r.URL.Path, rules, rulesVerified, presentation)
	for _, game := range data.Games {
		if game.Status == remainingStatus {
			page.Remaining++
		}
	}
	if data.LastSuccess != nil {
		page.Freshness, page.FreshnessFallback = freshnessValues(data.LastSuccess.FinishedAt, a.options.Location)
	}
	if showScheduleDifficulty {
		page.ScheduleNote = scheduleDifficultyNote(data, scope.Entry.Inventory)
	}
	if !scope.Cataloged {
		page.FormatNotice = unknownFormatNotice
	} else if !scope.standingsAvailable() {
		page.FormatNotice = "Standings are unavailable for this scope."
	}
	if qualificationStore, ok := a.store.(interface {
		QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
	}); ok && scope.qualificationAvailable() && rulesVerified && presentation.Phase != seasonPhaseUpcoming && data.FixtureSnapshotID != "" && rules.Version != "" {
		if snapshot, found, lookupErr := qualificationStore.QualificationForSnapshot(r.Context(), data.FixtureSnapshotID, rules.Version); lookupErr == nil && found && snapshot.Run.Outcome == "complete" {
			page.Standings = qualificationViews(page.Standings, snapshot.Statuses)
		}
	}

	return page, nil
}

const unknownFormatNotice = "The competition format is not verified, so standings, forecasts, and clinching calculations are unavailable."

func historicalCatalogScope(scope requestCompetition) bool {
	return scope.Cataloged && scope.Entry.Public && scope.Entry.Kind == competition.StageKindLeagueTable && scope.Entry.Rules == nil
}

func seasonFeatureSuffix(requestPath, stageSlug string) (string, bool) {
	if strings.HasSuffix(requestPath, "/"+stageSlug+"/fixtures") {
		return "/fixtures", true
	}
	if strings.HasSuffix(requestPath, "/"+stageSlug) {
		return "", true
	}
	return "", false
}

func (a *application) playoffLoadPage(r *http.Request, scope requestCompetition) (seasonPage, error) {
	notice := "Playoff fixtures have not been loaded yet."
	if store, ok := a.store.(interface {
		SeasonReadiness(context.Context, string, string) (cache.SeasonReadinessSnapshot, bool, error)
	}); ok {
		readiness, found, err := store.SeasonReadiness(r.Context(), scope.Season, scope.Stage)
		if err != nil {
			return seasonPage{}, fmt.Errorf("load playoff readiness: %w", err)
		}
		if found && readiness.Readiness == cache.SourceReadinessNotPublished {
			notice = "ASA has not published playoff data yet."
		}
	}
	return seasonPage{Title: scope.Season + " playoffs", Season: scope.Season, Stage: scope.Stage, HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"), SeasonPath: relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)), FixturesPath: relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)+"/fixtures"), CurrentPath: relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)), SeasonsPath: seasonArchiveURL(r.URL.Path), StageSelector: stageSelector(r.URL.Path, scope.Season, scope.Stage), FixturesHeading: "Results and fixtures", StandingsMode: "per-game", FormatNotice: notice}, nil
}

func (a *application) historicalLoadPage(r *http.Request, scope requestCompetition) (seasonPage, error) {
	notice := "This historical season isn't available in the explorer yet."
	if store, ok := a.store.(interface {
		SeasonReadiness(context.Context, string, string) (cache.SeasonReadinessSnapshot, bool, error)
	}); ok {
		readiness, found, err := store.SeasonReadiness(r.Context(), scope.Season, scope.Stage)
		if err != nil {
			return seasonPage{}, fmt.Errorf("load historical readiness: %w", err)
		}
		if found && readiness.Readiness == cache.SourceReadinessNotPublished {
			notice = "ASA has not published historical data for this season."
		}
	}
	featureSuffix, _ := seasonFeatureSuffix(r.URL.Path, scope.Entry.Slug)
	return seasonPage{
		Title:           scope.Season + " NWSL season",
		Season:          scope.Season,
		Stage:           scope.Stage,
		HomePath:        relativeURL(r.URL.Path, "/"),
		StylesheetPath:  relativeURL(r.URL.Path, "/static/site.css"),
		ScriptPath:      relativeURL(r.URL.Path, "/static/standings.js"),
		SeasonPath:      relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)),
		FixturesPath:    relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)+"/fixtures"),
		CurrentPath:     relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)),
		SeasonsPath:     seasonArchiveURL(r.URL.Path),
		SeasonSelector:  seasonFeatureSelector(r.URL.Path, scope.Season, featureSuffix),
		StageSelector:   stageSelector(r.URL.Path, scope.Season, scope.Stage),
		FormatNotice:    notice,
		FixturesHeading: "Results",
		StandingsMode:   "total",
	}, nil
}

// loadSeasonData makes local-cache latency visible as a distinct operation and
// adds the version and completeness of the data to the enclosing request span.
func (a *application) loadSeasonData(parent context.Context, season string, stages ...string) (data cache.SeasonData, err error) {
	stage := a.options.Stage
	if len(stages) != 0 {
		stage = stages[0]
	}
	ctx, span := telemetry.Tracer().Start(parent, nwslconv.SpanCacheSeasonLoad, trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(nwslconv.CacheName("season"), nwslconv.SeasonName(season), nwslconv.Stage(stage)),
	)
	defer func() {
		if err != nil {
			err = telemetry.RecordWarningWithType(ctx, span, err, nwslconv.ErrorCodeCacheSeasonLoad, telemetry.ErrorTypeStorageFailure)
		}
		span.End()
	}()
	data, err = a.store.Season(ctx, season, stage)
	if err != nil {
		return cache.SeasonData{}, err
	}
	attributes := seasonDataAttributes(data, season, stage)
	span.SetAttributes(attributes...)
	trace.SpanFromContext(parent).SetAttributes(attributes...)
	return data, nil
}

func seasonDataAttributes(data cache.SeasonData, season, stage string) []attribute.KeyValue {
	completed := 0
	remaining := 0
	for _, game := range data.Games {
		switch game.Status {
		case fixtures.CompletedStatus:
			completed++
		case remainingStatus:
			remaining++
		}
	}
	xgAvailable := 0
	xgUnavailable := 0
	for _, value := range data.XGoals {
		switch value.Availability {
		case cache.XGAvailable:
			xgAvailable++
		case cache.XGUnavailable:
			xgUnavailable++
		}
	}
	attributes := []attribute.KeyValue{nwslconv.SeasonName(season), nwslconv.Stage(stage), nwslconv.CacheFixtureSnapshotID(data.FixtureSnapshotID), nwslconv.SeasonTeamCount(len(data.Teams)), nwslconv.SeasonFixtureCount(len(data.Games)), nwslconv.SeasonCompletedFixtureCount(completed), nwslconv.SeasonRemainingFixtureCount(remaining), nwslconv.SeasonXGAvailableCount(xgAvailable), nwslconv.SeasonXGUnavailableCount(xgUnavailable)}
	if data.LastSuccess != nil {
		attributes = append(attributes, nwslconv.CacheLastSuccessAgeSeconds(time.Since(data.LastSuccess.FinishedAt).Seconds()))
	}
	return attributes
}

func scheduleDifficultyNote(data cache.SeasonData, inventory *competition.InventoryExpectation) string {
	notes := make([]string, 0, 4)
	if !historicalVenueReady(data, false) {
		notes = append(notes, "Two-season home/away history is still syncing; the venue adjustment temporarily uses this season only.")
	}
	if inventory != nil && inventory.Teams > 0 && len(data.Teams) != inventory.Teams {
		notes = append(notes, fmt.Sprintf("Cache has %d of %d expected teams.", len(data.Teams), inventory.Teams))
	}
	if inventory != nil && inventory.Games > 0 && len(data.Games) != inventory.Games {
		notes = append(notes, fmt.Sprintf("Cache has %d of %d expected regular-season fixtures.", len(data.Games), inventory.Games))
	}

	appearances := make(map[string]int, len(data.Teams))
	unsupported := 0
	for _, game := range data.Games {
		appearances[game.HomeTeamID]++
		appearances[game.AwayTeamID]++
		if game.Status != standings.CompletedStatus && game.Status != remainingStatus {
			unsupported++
		}
	}
	if unsupported > 0 {
		notes = append(notes, fmt.Sprintf("%d fixture(s) have a status excluded from schedule difficulty.", unsupported))
	}
	uneven := 0
	if inventory != nil && inventory.GamesPerTeam > 0 {
		for _, team := range data.Teams {
			if appearances[team.ID] != inventory.GamesPerTeam {
				uneven++
			}
		}
		if uneven > 0 {
			notes = append(notes, fmt.Sprintf("%d team(s) do not have the expected %d fixtures.", uneven, inventory.GamesPerTeam))
		}
	}
	return strings.Join(notes, " ")
}
func qualificationViews(rows []tableRowView, values []cache.QualificationStatus) []tableRowView {
	byTeam := map[string][]cache.QualificationStatus{}
	for _, v := range values {
		if v.Status == clinching.Clinched {
			byTeam[v.TeamID] = append(byTeam[v.TeamID], v)
		}
	}
	for i := range rows {
		values := byTeam[rows[i].TeamID]
		if len(values) == 0 {
			continue
		}
		sort.Slice(values, func(a, b int) bool { return values[a].TopK < values[b].TopK })
		rows[i].QualificationBadge = labelAchievement(values[0].Achievement)
		labels := make([]string, 0, len(values))
		for _, v := range values {
			labels = append(labels, labelAchievement(v.Achievement))
		}
		rows[i].QualificationTitle = "Guaranteed achievements: " + strings.Join(labels, ", ")
	}
	return rows
}
func labelAchievement(a competition.AchievementID) string {
	switch a {
	case competition.AchievementShield:
		return "Shield"
	case competition.AchievementHomePlayoff:
		return "Top-four seed"
	case competition.AchievementPlayoffs:
		return "Playoffs"
	}
	return string(a)
}

func achievementPhrase(a competition.AchievementID) string {
	switch a {
	case competition.AchievementShield:
		return "the Shield"
	case competition.AchievementHomePlayoff:
		return "a top-four seed"
	case competition.AchievementPlayoffs:
		return "the playoffs"
	}
	return "the " + strings.ToLower(string(a))
}

func standingsGames(games []cache.Game) []standings.Game {
	values := make([]standings.Game, 0, len(games))
	for _, game := range games {
		value := standings.Game{
			ID:         game.ASAID,
			Status:     game.Status,
			HomeTeamID: game.HomeTeamID,
			AwayTeamID: game.AwayTeamID,
		}
		if kickoff, err := fixtures.ParseKickoff(game.KickoffUTC); err == nil {
			value.Kickoff = kickoff
		}
		if game.HomeScore.Valid {
			score := int(game.HomeScore.Int64)
			value.HomeScore = &score
		}
		if game.AwayScore.Valid {
			score := int(game.AwayScore.Int64)
			value.AwayScore = &score
		}
		values = append(values, value)
	}
	return values
}

func (a *application) render(w http.ResponseWriter, name string, data any) {
	var body bytes.Buffer
	if err := a.pages.ExecuteTemplate(&body, name, data); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body.Bytes())
}

func (a *application) renderError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	a.render(w, "error", errorPage{
		Title: "Season unavailable", Message: err.Error(),
		HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"),
		SeasonSelector: seasonSelector(r.URL.Path, a.requestScope(r).Season),
		StageSelector:  stageSelector(r.URL.Path, a.requestScope(r).Season, a.requestScope(r).Stage),
	})
}

func (a *application) renderUnavailableFeature(w http.ResponseWriter, r *http.Request, scope requestCompetition, feature string) {
	rules, verified := a.rulesForSeason(scope.Season, scope.Stage)
	a.renderUnavailableFeatureWithNavigation(w, r, scope, feature, seasonNavigation(r.URL.Path, scope, "", rules, verified))
}

func (a *application) renderUnavailableFeatureWithNavigation(w http.ResponseWriter, r *http.Request, scope requestCompetition, feature string, navigation []navigationItem) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	a.render(w, "error", errorPage{
		Title: feature + " unavailable", Message: fmt.Sprintf("%s is unavailable for %s %s.", feature, scope.Season, scope.Stage),
		HomePath: relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"),
		Navigation:     navigation,
		SeasonSelector: seasonSelector(r.URL.Path, scope.Season),
		StageSelector:  stageSelector(r.URL.Path, scope.Season, scope.Stage),
	})
}

// withBasePath lets a reverse proxy forward its mount path unchanged. The
// application routes remain rooted internally, while links and redirects are
// relative so the browser retains that mount path.
func withBasePath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalPath, ok := stripBasePath(r.URL.Path)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if canonicalPath, ok := trimRouteTrailingSlash(internalPath); ok {
			location := "../" + path.Base(canonicalPath)
			if r.URL.RawQuery != "" {
				location += "?" + r.URL.RawQuery
			}
			redirectRelative(w, location, http.StatusSeeOther)
			return
		}
		if internalPath == r.URL.Path {
			next.ServeHTTP(w, r)
			return
		}

		request := r.Clone(r.Context())
		request.URL.Path = internalPath
		request.URL.RawPath = ""
		request.RequestURI = request.URL.RequestURI()
		next.ServeHTTP(w, request)
	})
}

func trimRouteTrailingSlash(requestPath string) (string, bool) {
	if requestPath == "/" || !strings.HasSuffix(requestPath, "/") {
		return requestPath, false
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(requestPath, "/"), "/"), "/")
	if len(parts) == 2 && parts[0] == "seasons" && parts[1] != "" {
		return "/" + strings.Join(parts, "/"), true
	}
	if len(parts) == 3 && parts[0] == "seasons" && parts[1] != "" && (parts[2] == "fixtures" || parts[2] == "schedule-difficulty" || parts[2] == "forecast" || parts[2] == "clinching") {
		return "/" + strings.Join(parts, "/"), true
	}
	return requestPath, false
}

func stripBasePath(requestPath string) (string, bool) {
	if requestPath == "/" || requestPath == "/seasons" || strings.HasPrefix(requestPath, "/seasons/") || strings.HasPrefix(requestPath, "/static/") || requestPath == "/healthz" || requestPath == "/cache/status" {
		return requestPath, true
	}
	if strings.HasSuffix(requestPath, "/seasons") {
		return "/seasons", true
	}
	for _, suffix := range []string{"/seasons/", "/static/"} {
		if index := strings.Index(requestPath, suffix); index > 0 {
			return requestPath[index:], true
		}
	}
	for _, suffix := range []string{"/healthz", "/cache/status"} {
		if strings.HasSuffix(requestPath, suffix) {
			return suffix, true
		}
	}
	if strings.HasSuffix(requestPath, "/") {
		return "/", true
	}
	return requestPath, false
}

func relativeURL(fromPath, targetPath string) string {
	// A stage's canonical route has no trailing slash. From a nested page, a
	// plain "." resolves to the stage directory instead, which does not match a
	// route. Step up and name the stage explicitly in that one case.
	if targetPath != "/" && path.Dir(fromPath) == targetPath {
		return "../" + path.Base(targetPath)
	}
	fromParts := urlPathParts(path.Dir(fromPath))
	targetParts := urlPathParts(targetPath)
	common := 0
	for common < len(fromParts) && common < len(targetParts) && fromParts[common] == targetParts[common] {
		common++
	}
	parts := make([]string, 0, len(fromParts)-common+len(targetParts)-common)
	for range fromParts[common:] {
		parts = append(parts, "..")
	}
	parts = append(parts, targetParts[common:]...)
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

func seasonArchiveURL(fromPath string) string {
	return path.Join(relativeURL(fromPath, "/seasons"), "..", "seasons")
}

func redirectRelative(w http.ResponseWriter, location string, status int) {
	w.Header().Set("Location", location)
	w.WriteHeader(status)
}

func urlPathParts(value string) []string {
	value = path.Clean(value)
	if value == "." || value == "/" {
		return nil
	}
	return strings.Split(strings.Trim(value, "/"), "/")
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "ok")
}

func cacheStatus(reader Store, season, stage, rulesVersion string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if reader == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(cacheStatusResponse{OK: false, Error: "cache status unavailable"})
			return
		}
		status, err := reader.Status(r.Context(), season, stage)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(cacheStatusResponse{OK: false, Error: err.Error()})
			return
		}
		response := cacheStatusResponse{OK: true}
		if status.LastAttempt != nil {
			response.LastAttempt = syncRunResponseFrom(status.LastAttempt)
		}
		if status.LastSuccess != nil {
			response.LastSuccess = syncRunResponseFrom(status.LastSuccess)
		}
		if qualificationStore, ok := reader.(interface {
			QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
		}); ok && status.LastSuccess != nil && status.LastSuccess.FixtureSnapshotID != "" && rulesVersion != "" {
			qualification, found, err := qualificationStore.QualificationForSnapshot(r.Context(), status.LastSuccess.FixtureSnapshotID, rulesVersion)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(cacheStatusResponse{OK: false, Error: err.Error()})
				return
			}
			response.Qualification = &qualificationStatusResponse{FixtureSnapshotID: status.LastSuccess.FixtureSnapshotID, RulesVersion: rulesVersion, MatchesCurrent: found}
			if found {
				response.Qualification.Outcome = qualification.Run.Outcome
				response.Qualification.FinishedAt = qualification.Run.FinishedAt
				response.Qualification.WrittenStatuses = qualification.Run.WrittenStatuses
			}
		}
		if xgReader, ok := reader.(interface {
			XGStatus(context.Context, string, string) (cache.XGStatus, error)
		}); ok {
			xg, err := xgReader.XGStatus(r.Context(), season, stage)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(cacheStatusResponse{OK: false, Error: err.Error()})
				return
			}
			response.XG = &xgStatusResponse{}
			if xg.LastAttempt != nil {
				response.XG.LastAttempt = xgRunResponseFrom(xg.LastAttempt)
			}
			if xg.LastSuccess != nil {
				response.XG.LastSuccess = xgRunResponseFrom(xg.LastSuccess)
			}
		}
		_ = json.NewEncoder(w).Encode(response)
	}
}

type cacheStatusResponse struct {
	OK            bool                         `json:"ok"`
	Error         string                       `json:"error,omitempty"`
	LastAttempt   *syncRunResponse             `json:"last_attempt,omitempty"`
	LastSuccess   *syncRunResponse             `json:"last_success,omitempty"`
	XG            *xgStatusResponse            `json:"xg,omitempty"`
	Qualification *qualificationStatusResponse `json:"qualification,omitempty"`
}
type qualificationStatusResponse struct {
	FixtureSnapshotID string    `json:"fixture_snapshot_id"`
	RulesVersion      string    `json:"rules_version"`
	Outcome           string    `json:"outcome,omitempty"`
	FinishedAt        time.Time `json:"finished_at,omitempty"`
	WrittenStatuses   int       `json:"written_statuses,omitempty"`
	MatchesCurrent    bool      `json:"matches_current"`
}
type xgStatusResponse struct {
	LastAttempt *xgRunResponse `json:"last_attempt,omitempty"`
	LastSuccess *xgRunResponse `json:"last_success,omitempty"`
}
type xgRunResponse struct {
	ID               int64     `json:"id"`
	FinishedAt       time.Time `json:"finished_at"`
	Outcome          string    `json:"outcome"`
	ErrorSummary     string    `json:"error_summary"`
	RowsSeen         int64     `json:"rows_seen"`
	AvailableGames   int64     `json:"available_games"`
	UnavailableGames int64     `json:"unavailable_games"`
	RowsInserted     int64     `json:"rows_inserted"`
	RowsUpdated      int64     `json:"rows_updated"`
	RowsUnchanged    int64     `json:"rows_unchanged"`
}

func xgRunResponseFrom(run *cache.XGSyncRun) *xgRunResponse {
	return &xgRunResponse{ID: run.ID, FinishedAt: run.FinishedAt, Outcome: run.Outcome, ErrorSummary: run.ErrorSummary, RowsSeen: run.RowsSeen, AvailableGames: run.AvailableGames, UnavailableGames: run.UnavailableGames, RowsInserted: run.RowsInserted, RowsUpdated: run.RowsUpdated, RowsUnchanged: run.RowsUnchanged}
}

type syncRunResponse struct {
	ID             int64  `json:"id"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	Season         string `json:"season"`
	Stage          string `json:"stage"`
	Outcome        string `json:"outcome"`
	ErrorSummary   string `json:"error_summary,omitempty"`
	TeamsUpserted  int    `json:"teams_upserted"`
	GamesUpserted  int    `json:"games_upserted"`
	GamesDeleted   int    `json:"games_deleted"`
	GamesSeen      int    `json:"games_seen"`
	DurationMS     int64  `json:"duration_ms"`
	TeamsInserted  int    `json:"teams_inserted"`
	TeamsUpdated   int    `json:"teams_updated"`
	TeamsUnchanged int    `json:"teams_unchanged"`
	GamesInserted  int    `json:"games_inserted"`
	GamesUpdated   int    `json:"games_updated"`
	GamesUnchanged int    `json:"games_unchanged"`
}

func syncRunResponseFrom(run *cache.SyncRun) *syncRunResponse {
	return &syncRunResponse{
		ID: run.ID, StartedAt: run.StartedAt.UTC().Format(time.RFC3339), FinishedAt: run.FinishedAt.UTC().Format(time.RFC3339),
		Season: run.Season, Stage: run.Stage, Outcome: run.Outcome, ErrorSummary: run.ErrorSummary,
		TeamsUpserted: run.TeamsUpserted, GamesUpserted: run.GamesUpserted, GamesDeleted: run.GamesDeleted, GamesSeen: run.GamesSeen,
		DurationMS:    run.FinishedAt.Sub(run.StartedAt).Milliseconds(),
		TeamsInserted: run.TeamsInserted, TeamsUpdated: run.TeamsUpdated, TeamsUnchanged: run.TeamsUnchanged,
		GamesInserted: run.GamesInserted, GamesUpdated: run.GamesUpdated, GamesUnchanged: run.GamesUnchanged,
	}
}

func intText(value int) string { return strconv.Itoa(value) }
