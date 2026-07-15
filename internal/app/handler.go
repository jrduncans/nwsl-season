package app

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/strength"
)

const (
	defaultSeason        = "2026"
	defaultStage         = "Regular Season"
	defaultPlayoffPlaces = 8
	defaultGamesPerTeam  = 30
	maxClinchingFixtures = 4
	remainingStatus      = "PreMatch"
)

// Store reads page data from the local cache.
type Store interface {
	Status(context.Context, string, string) (cache.Status, error)
	Season(context.Context, string, string) (cache.SeasonData, error)
}

// Options contains season rules that are intentionally explicit at the HTTP boundary.
type Options struct {
	CurrentSeason      string
	Stage              string
	PlayoffPlaces      int
	GamesPerTeam       int
	ForecastIterations int
	Location           *time.Location
}

// NewHandler wires the application routes using the current-season defaults.
func NewHandler(stores ...Store) http.Handler {
	return NewHandlerWithOptions(firstStore(stores), Options{})
}

// NewHandlerWithOptions wires the application routes with explicit season rules.
func NewHandlerWithOptions(store Store, options Options) http.Handler {
	options = defaultOptions(options)
	pages := template.Must(template.ParseFS(pageFiles, "templates/*.html"))
	staticFS, err := fs.Sub(pageFiles, "static")
	if err != nil {
		panic(err)
	}

	application := &application{store: store, options: options, pages: pages}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", application.root)
	mux.HandleFunc("GET /seasons/{season}", application.season)
	mux.HandleFunc("GET /seasons/{season}/fixtures", application.fixtures)
	mux.HandleFunc("GET /seasons/{season}/schedule-difficulty", application.scheduleDifficulty)
	mux.HandleFunc("GET /seasons/{season}/forecast", application.forecast)
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /cache/status", cacheStatus(store, options.CurrentSeason, options.Stage))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	return withBasePath(mux)
}

type application struct {
	store   Store
	options Options
	pages   *template.Template
}

func firstStore(stores []Store) Store {
	if len(stores) == 0 {
		return nil
	}
	return stores[0]
}

func defaultOptions(options Options) Options {
	if options.CurrentSeason == "" {
		options.CurrentSeason = defaultSeason
	}
	if options.Stage == "" {
		options.Stage = defaultStage
	}
	if options.PlayoffPlaces <= 0 {
		options.PlayoffPlaces = defaultPlayoffPlaces
	}
	if options.GamesPerTeam <= 0 {
		options.GamesPerTeam = defaultGamesPerTeam
	}
	if options.ForecastIterations <= 0 {
		options.ForecastIterations = defaultForecastIterations
	}
	if options.Location == nil {
		options.Location = time.Local
	}
	return options
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
	standingsView := addScheduleIndicators(tableViews(actualTable, a.options.PlayoffPlaces, nil), scheduleView)
	page := seasonPage{
		Title:                  season + " NWSL season",
		Season:                 season,
		Stage:                  a.options.Stage,
		HomePath:               relativeURL(r.URL.Path, "/"),
		StylesheetPath:         relativeURL(r.URL.Path, "/static/site.css"),
		ScriptPath:             relativeURL(r.URL.Path, "/static/standings.js"),
		Standings:              addTotalPositions(standingsView, totalTable, a.options.PlayoffPlaces),
		Strength:               scheduleView,
		FixtureGroups:          fixtureGroups(data, a.options.Location),
		ForecastPath:           relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/forecast"),
		SeasonPath:             seasonURL(r.URL.Path, season),
		FixturesPath:           relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/fixtures"),
		ScheduleDifficultyPath: relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/schedule-difficulty"),
	}
	for _, game := range data.Games {
		if game.Status == remainingStatus {
			page.Remaining++
		}
	}
	if data.LastSuccess != nil {
		page.Freshness, page.FreshnessFallback = freshnessValues(data.LastSuccess.FinishedAt, a.options.Location)
	}
	expectedGames := len(data.Teams) * a.options.GamesPerTeam / 2
	scheduleComplete := len(data.Games) == expectedGames
	if !scheduleComplete {
		page.ScheduleNote = fmt.Sprintf("The cache contains %d of %d expected regular-season fixtures.", len(data.Games), expectedGames)
	}
	page.Standings = clinchingViews(data.Teams, domainGames, page.Standings, a.options.PlayoffPlaces, scheduleComplete)

	return page, nil
}

func clinchingViews(teams []standings.Team, games []standings.Game, rows []tableRowView, playoffPlaces int, scheduleComplete bool) []tableRowView {
	if !scheduleComplete {
		return rows
	}
	remaining := 0
	for _, game := range games {
		if game.Status == remainingStatus {
			remaining++
		}
	}
	if remaining > maxClinchingFixtures {
		return rows
	}
	byTeam := make(map[string]clinching.Result, len(teams))
	for _, team := range teams {
		result, err := clinching.Evaluate(teams, games, team.ID, playoffPlaces)
		if err == nil {
			byTeam[team.ID] = result
		}
	}
	for index := range rows {
		if result, ok := byTeam[rows[index].TeamID]; ok && result.Clinched {
			rows[index].Clinched = true
		}
	}
	return rows
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.pages.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
	}
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
	if len(parts) == 3 && parts[0] == "seasons" && parts[1] != "" && (parts[2] == "fixtures" || parts[2] == "schedule-difficulty" || parts[2] == "forecast") {
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

func cacheStatus(reader Store, season, stage string) http.HandlerFunc {
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
		_ = json.NewEncoder(w).Encode(response)
	}
}

type cacheStatusResponse struct {
	OK          bool             `json:"ok"`
	Error       string           `json:"error,omitempty"`
	LastAttempt *syncRunResponse `json:"last_attempt,omitempty"`
	LastSuccess *syncRunResponse `json:"last_success,omitempty"`
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

func parseKickoff(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05 MST"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse kickoff %q", value)
}

func intText(value int) string { return strconv.Itoa(value) }
