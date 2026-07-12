package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/whatif"
)

const (
	defaultSeason        = "2026"
	defaultStage         = "Regular Season"
	defaultPlayoffPlaces = 8
	defaultGamesPerTeam  = 30
	maxClinchingFixtures = 4
)

// Store reads page data from the local cache.
type Store interface {
	Status(context.Context) (cache.Status, error)
	Season(context.Context, string, string) (cache.SeasonData, error)
}

// Options contains season rules that are intentionally explicit at the HTTP boundary.
type Options struct {
	CurrentSeason string
	Stage         string
	PlayoffPlaces int
	GamesPerTeam  int
	Location      *time.Location
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
	mux.HandleFunc("GET /seasons/{season}/what-if", application.whatIf)
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /cache/status", cacheStatus(store))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	return mux
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
	if options.Location == nil {
		options.Location = time.Local
	}
	return options
}

func (a *application) root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/seasons/"+url.PathEscape(a.options.CurrentSeason), http.StatusSeeOther)
}

func (a *application) season(w http.ResponseWriter, r *http.Request) {
	page, err := a.loadSeasonPage(r, nil)
	if err != nil {
		a.renderError(w, err)
		return
	}
	a.render(w, "season", page)
}

func (a *application) whatIf(w http.ResponseWriter, r *http.Request) {
	if canonical, ok, err := canonicalWhatIfURL(r); err != nil {
		a.renderBadRequest(w, err)
		return
	} else if ok {
		http.Redirect(w, r, canonical, http.StatusSeeOther)
		return
	}

	selections, err := whatif.Parse(r.URL.Query().Get("v"), r.URL.Query()["p"])
	if err != nil {
		a.renderBadRequest(w, err)
		return
	}
	page, err := a.loadSeasonPage(r, selections)
	if err != nil {
		if errors.Is(err, whatif.ErrNotRemaining) {
			a.renderBadRequest(w, err)
			return
		}
		a.renderError(w, err)
		return
	}
	a.render(w, "whatif", page)
}

func canonicalWhatIfURL(r *http.Request) (string, bool, error) {
	query := r.URL.Query()
	values := url.Values{}
	values.Set("v", whatif.EncodingVersion)
	found := false
	keys := make([]string, 0)
	for key := range query {
		if strings.HasPrefix(key, "g.") {
			found = true
			keys = append(keys, key)
		}
	}
	if !found {
		return "", false, nil
	}
	sort.Strings(keys)
	for _, key := range keys {
		gameID := strings.TrimPrefix(key, "g.")
		selected := query.Get(key)
		if selected == "" {
			continue
		}
		outcome := whatif.Outcome(selected)
		if gameID == "" || !outcome.Valid() {
			return "", false, fmt.Errorf("invalid outcome for fixture %q", gameID)
		}
		values.Add("p", gameID+":"+selected)
	}
	return r.URL.Path + "?" + values.Encode(), true, nil
}

func (a *application) loadSeasonPage(r *http.Request, selections map[string]whatif.Outcome) (seasonPage, error) {
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
	page := seasonPage{
		Title:         season + " NWSL season",
		Season:        season,
		Stage:         a.options.Stage,
		Standings:     tableViews(actualTable, a.options.PlayoffPlaces, nil),
		FixtureGroups: fixtureGroups(data, selections, a.options.Location),
		WhatIfPath:    "/seasons/" + url.PathEscape(season) + "/what-if",
		SeasonPath:    "/seasons/" + url.PathEscape(season),
		Source:        "American Soccer Analysis (ASA)",
	}
	for _, game := range data.Games {
		if game.Status == whatif.RemainingStatus {
			page.Remaining++
		}
	}
	if data.LastSuccess != nil {
		page.Freshness = data.LastSuccess.FinishedAt.In(a.options.Location).Format("Jan 2, 2006 at 3:04 PM MST")
	}
	expectedGames := len(data.Teams) * a.options.GamesPerTeam / 2
	scheduleComplete := len(data.Games) == expectedGames
	if !scheduleComplete {
		page.ScheduleNote = fmt.Sprintf("The cache contains %d of %d expected regular-season fixtures.", len(data.Games), expectedGames)
	}
	page.ClinchingNote, page.Standings = clinchingViews(data.Teams, domainGames, page.Standings, a.options.PlayoffPlaces, scheduleComplete)

	if selections != nil {
		projectedGames, err := whatif.Apply(domainGames, selections)
		if err != nil {
			return seasonPage{}, err
		}
		page.Selections = len(selections)
		if len(selections) > 0 {
			projected := standings.Calculate(data.Teams, projectedGames, standings.PerGameRules())
			page.Projected = tableViews(projected, a.options.PlayoffPlaces, nil)
		}
	}
	return page, nil
}

func clinchingViews(teams []standings.Team, games []standings.Game, rows []tableRowView, playoffPlaces int, scheduleComplete bool) (string, []tableRowView) {
	if !scheduleComplete {
		return "Clinching is not evaluated until the complete regular-season schedule is cached.", rows
	}
	remaining := 0
	for _, game := range games {
		if game.Status == whatif.RemainingStatus {
			remaining++
		}
	}
	if remaining > maxClinchingFixtures {
		return fmt.Sprintf("Exact clinching is evaluated once %d or fewer fixtures remain; %d remain.", maxClinchingFixtures, remaining), rows
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
	return "Clinching indicators use exact feasible-result evaluation.", rows
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

func (a *application) renderBadRequest(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	a.render(w, "error", errorPage{Title: "Invalid what-if scenario", Message: err.Error()})
}

func (a *application) renderError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	a.render(w, "error", errorPage{Title: "Season unavailable", Message: err.Error()})
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "ok")
}

func cacheStatus(reader Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if reader == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(cacheStatusResponse{OK: false, Error: "cache status unavailable"})
			return
		}
		status, err := reader.Status(r.Context())
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
	ID            int64  `json:"id"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at"`
	Season        string `json:"season"`
	Stage         string `json:"stage"`
	Outcome       string `json:"outcome"`
	ErrorSummary  string `json:"error_summary,omitempty"`
	TeamsUpserted int    `json:"teams_upserted"`
	GamesUpserted int    `json:"games_upserted"`
	GamesDeleted  int    `json:"games_deleted"`
	GamesSeen     int    `json:"games_seen"`
}

func syncRunResponseFrom(run *cache.SyncRun) *syncRunResponse {
	return &syncRunResponse{
		ID: run.ID, StartedAt: run.StartedAt.UTC().Format(time.RFC3339), FinishedAt: run.FinishedAt.UTC().Format(time.RFC3339),
		Season: run.Season, Stage: run.Stage, Outcome: run.Outcome, ErrorSummary: run.ErrorSummary,
		TeamsUpserted: run.TeamsUpserted, GamesUpserted: run.GamesUpserted, GamesDeleted: run.GamesDeleted, GamesSeen: run.GamesSeen,
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
