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
)

const (
	defaultSeason   = "2026"
	defaultStage    = "Regular Season"
	remainingStatus = fixtures.PreMatchStatus

	// Forecast Lab simulates complete seasons and is deliberately constrained so
	// a few expensive requests cannot consume the entire HTTP process.
	defaultForecastConcurrency = 2
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
	return newHandlerWithForecastExecutor(store, options, nil)
}

func newHandlerWithForecastExecutor(store Store, options Options, forecasts *forecastExecutor) http.Handler {
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
	mux.HandleFunc("GET /seasons/{season}", application.season)
	mux.HandleFunc("GET /seasons/{season}/fixtures", application.fixtures)
	mux.HandleFunc("GET /seasons/{season}/schedule-difficulty", application.scheduleDifficulty)
	mux.HandleFunc("GET /seasons/{season}/forecast", application.forecast)
	mux.HandleFunc("GET /seasons/{season}/model-evaluation", application.modelEvaluation)
	mux.HandleFunc("GET /seasons/{season}/clinching", application.clinching)
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /cache/status", cacheStatus(store, options.CurrentSeason, options.Stage, options.Rules.Version))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	return withBasePath(mux)
}

func (a *application) clinching(w http.ResponseWriter, r *http.Request) {
	page, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
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
	data, err := a.store.Season(r.Context(), page.Season, a.options.Stage)
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
		value, found, err := store.QualificationForSnapshot(r.Context(), data.FixtureSnapshotID, a.options.Rules.Version)
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
	snapshot, found, err := store.ScenarioForSnapshot(r.Context(), data.FixtureSnapshotID, a.options.Rules.Version, scenarios.DefinitionVersion)
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
	if options.Rules.Season == "" {
		if rules, ok := competition.ForSeason(options.CurrentSeason, options.Stage); ok {
			options.Rules = rules
		} else {
			options.Rules = presentationFallbackRules(options.CurrentSeason, options.Stage)
		}
	}
	if err := options.Rules.Validate(); err != nil {
		panic(fmt.Sprintf("invalid application rules: %v", err))
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

// presentationFallbackRules retains the pre-rules behavior for an unknown
// configured season. It supports page rendering but uses a distinct version,
// so no persisted qualification result can be mistaken for a verified rule set.
func presentationFallbackRules(season, stage string) competition.Rules {
	return competition.Rules{
		Season: season, Stage: stage, Version: "presentation-fallback-v1", ExpectedTeams: 16, GamesPerTeam: 30,
		Achievements: []competition.Achievement{
			{ID: competition.AchievementShield, Label: "Shield", TopK: 1},
			{ID: competition.AchievementHomePlayoff, Label: "Top-four seed", TopK: 4},
			{ID: competition.AchievementPlayoffs, Label: "Playoffs", TopK: 8},
		},
	}
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
	redirectRelative(w, "seasons/"+url.PathEscape(a.options.CurrentSeason), http.StatusSeeOther)
}

func (a *application) season(w http.ResponseWriter, r *http.Request) {
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
	a.render(w, "season", page)
}

func (a *application) fixtures(w http.ResponseWriter, r *http.Request) {
	page, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	a.render(w, "fixtures", page)
}

func (a *application) scheduleDifficulty(w http.ResponseWriter, r *http.Request) {
	page, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	a.render(w, "schedule-difficulty", page)
}

func (a *application) modelEvaluation(w http.ResponseWriter, r *http.Request) {
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
	if a.store == nil {
		return seasonPage{}, fmt.Errorf("season cache unavailable")
	}
	season := r.PathValue("season")
	if season == "" {
		season = a.options.CurrentSeason
	}
	data, err := a.store.Season(r.Context(), season, a.options.Stage)
	if err != nil {
		return seasonPage{}, fmt.Errorf("load %s season: %w", season, err)
	}
	if len(data.Games) == 0 {
		return seasonPage{}, fmt.Errorf("no cached games found for %s %s", season, a.options.Stage)
	}

	domainGames := standingsGames(data.Games)
	actualTable := standings.Calculate(data.Teams, domainGames, standings.PerGameRules())
	totalTable := standings.Calculate(data.Teams, domainGames, standings.OfficialTotalRules())
	scheduleStrength := strength.Calculate(data.Teams, domainGames)
	scheduleView := strengthViewFrom(scheduleStrength)
	standingsView := addScheduleIndicators(tableViews(actualTable, playoffPlaces(a.options.Rules)), scheduleView)
	standingsView, xgAvailable, completedMatches := addXGValues(standingsView, data)
	page := seasonPage{
		Title:                  season + " NWSL season",
		Season:                 season,
		Stage:                  a.options.Stage,
		HomePath:               relativeURL(r.URL.Path, "/"),
		StylesheetPath:         relativeURL(r.URL.Path, "/static/site.css"),
		ScriptPath:             relativeURL(r.URL.Path, "/static/standings.js"),
		Standings:              addTotalPositions(standingsView, totalTable, playoffPlaces(a.options.Rules)),
		Strength:               scheduleView,
		FixtureGroups:          fixtureGroups(data, a.options.Location),
		FixtureTeams:           fixtureTeams(data.Teams),
		ForecastPath:           relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/forecast"),
		ClinchingPath:          relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/clinching"),
		CurrentPath:            seasonURL(r.URL.Path, season),
		SeasonPath:             seasonURL(r.URL.Path, season),
		FixturesPath:           relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/fixtures"),
		ScheduleDifficultyPath: relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/schedule-difficulty"),
	}
	if completedMatches > 0 && xgAvailable < completedMatches {
		page.XGWarning = fmt.Sprintf("xG is unavailable for %d of %d completed matches, so the xG columns are incomplete.", completedMatches-xgAvailable, completedMatches)
	}
	page.Navigation = seasonNavigation(r.URL.Path, season, "/seasons/"+url.PathEscape(season)+strings.TrimPrefix(r.URL.Path, "/seasons/"+url.PathEscape(season)))
	for _, game := range data.Games {
		if game.Status == remainingStatus {
			page.Remaining++
		}
	}
	if data.LastSuccess != nil {
		page.Freshness, page.FreshnessFallback = freshnessValues(data.LastSuccess.FinishedAt, a.options.Location)
	}
	page.ScheduleNote = scheduleDifficultyNote(data, a.options.Rules)
	if qualificationStore, ok := a.store.(interface {
		QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
	}); ok && data.FixtureSnapshotID != "" && a.options.Rules.Version != "" {
		if snapshot, found, lookupErr := qualificationStore.QualificationForSnapshot(r.Context(), data.FixtureSnapshotID, a.options.Rules.Version); lookupErr == nil && found && snapshot.Run.Outcome == "complete" {
			page.Standings = qualificationViews(page.Standings, snapshot.Statuses)
		}
	}

	return page, nil
}

func scheduleDifficultyNote(data cache.SeasonData, rules competition.Rules) string {
	expectedGames := rules.ExpectedTeams * rules.GamesPerTeam / 2
	notes := make([]string, 0, 4)
	if len(data.Teams) != rules.ExpectedTeams {
		notes = append(notes, fmt.Sprintf("Cache has %d of %d expected teams.", len(data.Teams), rules.ExpectedTeams))
	}
	if len(data.Games) != expectedGames {
		notes = append(notes, fmt.Sprintf("Cache has %d of %d expected regular-season fixtures.", len(data.Games), expectedGames))
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
	for _, team := range data.Teams {
		if appearances[team.ID] != rules.GamesPerTeam {
			uneven++
		}
	}
	if uneven > 0 {
		notes = append(notes, fmt.Sprintf("%d team(s) do not have the expected %d fixtures.", uneven, rules.GamesPerTeam))
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
	if requestPath == "/" || strings.HasPrefix(requestPath, "/seasons/") || strings.HasPrefix(requestPath, "/static/") || requestPath == "/healthz" || requestPath == "/cache/status" {
		return requestPath, true
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

// seasonURL avoids a trailing-slash redirect when returning from a nested
// season page such as /seasons/{season}/fixtures.
func seasonURL(fromPath, season string) string {
	target := "/seasons/" + url.PathEscape(season)
	if path.Dir(fromPath) == target {
		return "../" + path.Base(target)
	}
	return relativeURL(fromPath, target)
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
