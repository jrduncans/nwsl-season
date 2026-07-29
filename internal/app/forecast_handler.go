package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

const defaultForecastIterations = 50000

func (a *application) forecast(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	state, err := forecaststate.ParseV2(query.Get("v"), query.Get("m"), query.Get("c"), query["p"], func(id string) bool { _, ok := forecast.Lookup(id); return ok }, forecast.Default().Model.Info().ID)
	if err != nil {
		a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", err)
		return
	}
	state.ModelID = forecast.CanonicalID(state.ModelID)
	state.ComparisonModelID = forecast.CanonicalID(state.ComparisonModelID)
	data, season, err := a.forecastData(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	if err := validateForecastState(data, state); err != nil {
		a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", err)
		return
	}
	teamID := query.Get("team")
	if teamID != "" && !seasonHasTeam(data, teamID) {
		a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", fmt.Errorf("unknown team filter %q", teamID))
		return
	}

	if action := query.Get("action"); action != "" {
		if action != "add" {
			a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", fmt.Errorf("unsupported forecast action %q", action))
			return
		}
		fixtureID := query.Get("fixture")
		outcome := simulation.Outcome(query.Get("outcome"))
		if !remainingForecastFixture(data, fixtureID) {
			a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", fmt.Errorf("game is not a remaining fixture: %q", fixtureID))
			return
		}
		state, err = state.With(fixtureID, outcome)
		if err != nil {
			a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", err)
			return
		}
		redirectRelative(w, forecastURL(r.URL.Path, season, state, ""), http.StatusSeeOther)
		return
	}
	active, ok := forecast.Lookup(state.ModelID)
	if !ok {
		a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", fmt.Errorf("unsupported forecast model %q", state.ModelID))
		return
	}
	xgoals := forecastXGoals(data)
	venue := forecastVenueSample(data)
	request := simulation.Request{Teams: data.Teams, Games: standingsGames(data.Games), XGoals: xgoals, HistoricalVenue: venue, Model: active.Model, Fixed: state.Fixed, Iterations: a.options.ForecastIterations, PlayoffPlaces: playoffPlaces(a.options.Rules)}
	tasks := []forecastTask{{key: forecastResultKey(data, state, active.Model.Info().ID, a.options.ForecastIterations, playoffPlaces(a.options.Rules)), request: request}}
	if state.ComparisonModelID != "" {
		entry, _ := forecast.Lookup(state.ComparisonModelID)
		tasks = append(tasks, forecastTask{key: forecastResultKey(data, state, entry.Model.Info().ID, a.options.ForecastIterations, playoffPlaces(a.options.Rules)), request: simulation.Request{Teams: data.Teams, Games: standingsGames(data.Games), XGoals: xgoals, HistoricalVenue: venue, Model: entry.Model, Fixed: state.Fixed, Iterations: a.options.ForecastIterations, PlayoffPlaces: playoffPlaces(a.options.Rules)}})
	}
	results, err := a.forecasts.results(r.Context(), tasks)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if errors.Is(err, errForecastOverloaded) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Forecast lab is busy; please try again shortly.", http.StatusTooManyRequests)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "Forecast lab took too long; please try again shortly.", http.StatusServiceUnavailable)
			return
		}
		a.renderError(w, r, fmt.Errorf("run forecast: %w", err))
		return
	}
	result := results[0]
	var comparison *simulation.Result
	if len(results) == 2 {
		comparison = &results[1]
	}
	a.render(w, "forecast", a.forecastPage(r, data, season, state, result, comparison, teamID))
}

// forecastResultKey identifies a deterministic simulation result. Fixture
// snapshots cover teams and match state; xG values are included because they
// are refreshed independently and affect the xG model without changing the
// fixture snapshot.
func forecastResultKey(data cache.SeasonData, state forecaststate.State, modelID string, iterations, playoffPlaces int) string {
	parts := []string{"forecast-result-v1", data.FixtureSnapshotID, modelID, strconv.Itoa(iterations), strconv.Itoa(playoffPlaces)}
	// A database Season result always supplies FixtureSnapshotID. Include the
	// simulator's actual inputs as well so alternate Store implementations
	// cannot accidentally share results when that field is absent.
	for _, team := range data.Teams {
		// Results retain the full Team value for rendering, so presentation
		// changes must invalidate a cached result even though they do not alter
		// the simulation's random seed.
		parts = append(parts, team.ID, team.Name, team.ShortName, team.Abbreviation)
	}
	for _, game := range standingsGames(data.Games) {
		parts = append(parts, game.ID, game.Status, game.HomeTeamID, game.AwayTeamID)
		if game.HomeScore != nil {
			parts = append(parts, strconv.Itoa(*game.HomeScore))
		} else {
			parts = append(parts, "")
		}
		if game.AwayScore != nil {
			parts = append(parts, strconv.Itoa(*game.AwayScore))
		} else {
			parts = append(parts, "")
		}
	}
	parts = append(parts, state.Values()...)
	xgoals := forecastXGoals(data)
	ids := make([]string, 0, len(xgoals))
	for id := range xgoals {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		value := xgoals[id]
		parts = append(parts, id, strconv.FormatFloat(value.Home, 'g', -1, 64), strconv.FormatFloat(value.Away, 'g', -1, 64))
	}
	for _, summary := range data.VenueHistory {
		parts = append(parts, summary.Season, strconv.FormatBool(summary.FixtureReady), strconv.FormatBool(summary.XGReady),
			strconv.Itoa(summary.Matches), strconv.Itoa(summary.HomeGoals), strconv.Itoa(summary.AwayGoals),
			strconv.Itoa(summary.HomePoints), strconv.Itoa(summary.AwayPoints), strconv.Itoa(summary.XGMatches),
			strconv.FormatFloat(summary.HomeXG, 'g', -1, 64), strconv.FormatFloat(summary.AwayXG, 'g', -1, 64))
	}
	encoded, _ := json.Marshal(parts) // Marshaling a []string cannot fail.
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (a *application) forecastData(r *http.Request) (data cache.SeasonData, season string, err error) {
	if a.store == nil {
		return cache.SeasonData{}, "", fmt.Errorf("season cache unavailable")
	}
	season = r.PathValue("season")
	if season == "" {
		season = a.options.CurrentSeason
	}
	data, err = a.store.Season(r.Context(), season, a.options.Stage)
	if err != nil {
		return cache.SeasonData{}, "", fmt.Errorf("load %s season: %w", season, err)
	}
	if len(data.Games) == 0 {
		return cache.SeasonData{}, "", fmt.Errorf("no cached games found for %s %s", season, a.options.Stage)
	}
	return data, season, nil
}

func (a *application) forecastPage(r *http.Request, data cache.SeasonData, season string, state forecaststate.State, result simulation.Result, comparison *simulation.Result, teamID string) forecastPage {
	base := forecastURL(r.URL.Path, season, forecaststate.State{ModelID: result.Model.ID, ComparisonModelID: state.ComparisonModelID, Fixed: map[string]simulation.Outcome{}}, "")
	canonical := forecastURL(r.URL.Path, season, state, "")
	page := forecastPage{
		Title: "Forecast lab · " + season + " NWSL season", Season: season,
		HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"),
		SeasonPath: seasonURL(r.URL.Path, season), ForecastPath: relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/forecast"),
		Navigation: seasonNavigation(r.URL.Path, season, "/seasons/"+url.PathEscape(season)+"/forecast"), ModelEvaluationPath: relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/model-evaluation"),
		CanonicalPath: canonical, ResetPath: base,
		ModelName: result.Model.Name, ModelID: result.Model.ID, ModelDetail: result.Model.Description,
		Iterations: result.Iterations, FixedCount: result.FixedCount, Remaining: result.Remaining,
		Rows: forecastComparisonRows(result, comparison, playoffPlaces(a.options.Rules)), Teams: forecastTeamOptions(data.Teams), FilteredTeam: teamID, HasTeamFilter: teamID != "", StateValues: state.Values(), PlayoffPlaces: playoffPlaces(a.options.Rules),
	}
	for _, entry := range forecast.Catalog() {
		page.Models = append(page.Models, forecastModelView{ID: entry.Model.Info().ID, Name: entry.Model.Info().Name, Default: entry.Default, Selected: entry.Model.Info().ID == state.ModelID, Comparison: entry.Model.Info().ID == state.ComparisonModelID, Detail: entry.Model.Info().Description, Inputs: entry.Model.Info().Inputs, Assumptions: entry.Model.Info().Assumptions})
	}
	page.HasComparison = comparison != nil
	if comparison != nil {
		page.ComparisonName = comparison.Model.Name
		page.ComparisonID = comparison.Model.ID
	}
	page.ShowXGCoverage = strings.HasPrefix(result.Model.ID, "xg-poisson-") || (comparison != nil && strings.HasPrefix(comparison.Model.ID, "xg-poisson-"))
	page.XGAvailable, page.XGCompleted = forecastXGCoverage(data)
	if page.XGCompleted > 0 {
		page.XGCoverage = fmt.Sprintf("%d of %d completed matches", page.XGAvailable, page.XGCompleted)
		page.XGWarning = float64(page.XGAvailable)/float64(page.XGCompleted) < .95
	}
	if page.ShowXGCoverage {
		page.XGFreshness, page.XGFreshnessFallback, page.XGRefreshWarning = forecastXGFreshness(data, a.options.Location)
	}
	if data.LastSuccess != nil {
		page.Freshness, page.FreshnessFallback = freshnessValues(data.LastSuccess.FinishedAt, a.options.Location)
		page.DataCutoff = page.FreshnessFallback
	} else {
		page.DataCutoff = "Unavailable"
	}
	usesHistoricalVenue := strings.HasPrefix(result.Model.ID, "results-poisson-") || strings.HasPrefix(result.Model.ID, "xg-poisson-") ||
		(comparison != nil && (strings.HasPrefix(comparison.Model.ID, "results-poisson-") || strings.HasPrefix(comparison.Model.ID, "xg-poisson-")))
	page.ScheduleNote = forecastScheduleNote(data, a.options.Rules.GamesPerTeam, usesHistoricalVenue, page.ShowXGCoverage)
	// The rendered selector is filtered for a useful no-JavaScript fallback.
	// The complete list remains in a template for immediate client-side changes.
	page.AllFixtures = forecastFixtures(data, state, a.options.Location, "")
	page.Fixtures = forecastFixtures(data, state, a.options.Location, teamID)
	page.CanAdd = len(page.AllFixtures) > 0
	if len(page.Fixtures) > 0 {
		page.DefaultHomeTeam = page.Fixtures[0].Home.Name
		page.DefaultAwayTeam = page.Fixtures[0].Away.Name
	} else if page.CanAdd {
		page.DefaultHomeTeam = page.AllFixtures[0].Home.Name
		page.DefaultAwayTeam = page.AllFixtures[0].Away.Name
	}
	page.Assumptions = forecastAssumptions(data, state, func(gameID string) string {
		return forecastURL(r.URL.Path, season, state.Without(gameID), "")
	}, a.options.Location)
	return page
}

func forecastXGoals(data cache.SeasonData) map[string]forecast.ExpectedGoals {
	values := map[string]forecast.ExpectedGoals{}
	for _, value := range data.XGoals {
		if value.Availability == cache.XGAvailable && value.HomeXG.Valid && value.AwayXG.Valid {
			values[value.GameID] = forecast.ExpectedGoals{GameID: value.GameID, Home: value.HomeXG.Float64, Away: value.AwayXG.Float64}
		}
	}
	return values
}

func forecastVenueSample(data cache.SeasonData) forecast.VenueSample {
	var sample forecast.VenueSample
	fixturesReady, xgReady := historicalVenueReady(data, false), historicalVenueReady(data, true)
	for _, summary := range data.VenueHistory {
		if fixturesReady {
			sample.Matches += summary.Matches
			sample.HomeGoals += float64(summary.HomeGoals)
			sample.AwayGoals += float64(summary.AwayGoals)
			sample.HomePoints += summary.HomePoints
			sample.AwayPoints += summary.AwayPoints
		}
		if xgReady {
			sample.XGMatches += summary.XGMatches
			sample.HomeXG += summary.HomeXG
			sample.AwayXG += summary.AwayXG
		}
	}
	return sample
}

func historicalVenueReady(data cache.SeasonData, requireXG bool) bool {
	if len(data.VenueHistory) != 2 {
		return false
	}
	for _, summary := range data.VenueHistory {
		if !summary.FixtureReady || (requireXG && !summary.XGReady) {
			return false
		}
	}
	return true
}
func forecastXGCoverage(data cache.SeasonData) (available, completed int) {
	for _, game := range data.Games {
		if game.Status == fixtures.CompletedStatus {
			completed++
		}
	}
	for _, value := range data.XGoals {
		if value.Availability == cache.XGAvailable {
			available++
		}
	}
	return
}

func forecastXGFreshness(data cache.SeasonData, location *time.Location) (freshness, fallback string, warning bool) {
	if success := data.XGStatus.LastSuccess; success != nil {
		freshness, fallback = freshnessValues(success.FinishedAt, location)
	}
	attempt, success := data.XGStatus.LastAttempt, data.XGStatus.LastSuccess
	return freshness, fallback, attempt != nil && (success == nil || attempt.FinishedAt.After(success.FinishedAt)) && attempt.Outcome != "success"
}

func forecastScheduleNote(data cache.SeasonData, gamesPerTeam int, usesHistoricalVenue, requireXG bool) string {
	expectedGames := len(data.Teams) * gamesPerTeam / 2
	notes := make([]string, 0, 3)
	if usesHistoricalVenue && !historicalVenueReady(data, requireXG) {
		notes = append(notes, "Two-season home/away history is still syncing; venue rates temporarily use this season only.")
	}
	if len(data.Games) != expectedGames {
		notes = append(notes, fmt.Sprintf("Cache has %d of %d expected regular-season fixtures.", len(data.Games), expectedGames))
	}

	appearances := make(map[string]int, len(data.Teams))
	unsupported := 0
	for _, game := range data.Games {
		appearances[game.HomeTeamID]++
		appearances[game.AwayTeamID]++
		if game.Status != fixtures.CompletedStatus && game.Status != simulation.RemainingStatus {
			unsupported++
		}
	}
	if unsupported > 0 {
		notes = append(notes, fmt.Sprintf("%d fixture(s) have a status that cannot be simulated and are excluded.", unsupported))
	}
	teamsWithUnexpectedCounts := 0
	for _, team := range data.Teams {
		if appearances[team.ID] != gamesPerTeam {
			teamsWithUnexpectedCounts++
		}
	}
	if teamsWithUnexpectedCounts > 0 {
		notes = append(notes, fmt.Sprintf("%d team(s) do not have the expected %d fixtures.", teamsWithUnexpectedCounts, gamesPerTeam))
	}
	return strings.Join(notes, " ")
}

func validateForecastState(data cache.SeasonData, state forecaststate.State) error {
	for id := range state.Fixed {
		if !remainingForecastFixture(data, id) {
			return fmt.Errorf("game is not a remaining fixture: %q", id)
		}
	}
	return nil
}

func remainingForecastFixture(data cache.SeasonData, id string) bool {
	for _, game := range data.Games {
		if game.ASAID == id {
			return game.Status == simulation.RemainingStatus
		}
	}
	return false
}

func seasonHasTeam(data cache.SeasonData, id string) bool {
	for _, team := range data.Teams {
		if team.ID == id {
			return true
		}
	}
	return false
}

func forecastURL(fromPath, season string, state forecaststate.State, teamID string) string {
	target := "/seasons/" + url.PathEscape(season) + "/forecast"
	path := relativeURL(fromPath, target)
	values := url.Values{}
	// Generated scenario URLs are always explicit v2 state, including the
	// selected model when no assumptions have been made.
	if state.ModelID != "" {
		values.Set("v", forecaststate.EncodingVersion)
		values.Set("m", state.ModelID)
		if state.ComparisonModelID != "" {
			values.Set("c", state.ComparisonModelID)
		}
		for _, value := range state.Values() {
			values.Add("p", value)
		}
	}
	if teamID != "" {
		values.Set("team", teamID)
	}
	if encoded := values.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func (a *application) renderScenarioBadRequest(w http.ResponseWriter, r *http.Request, title string, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	a.render(w, "error", errorPage{Title: title, Message: err.Error(), HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js")})
}
