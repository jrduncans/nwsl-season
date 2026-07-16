package app

import (
	"fmt"
	"net/http"
	"net/url"

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
	result, err := simulation.Run(r.Context(), simulation.Request{
		Teams: data.Teams, Games: standingsGames(data.Games), XGoals: xgoals, Model: active.Model, Fixed: state.Fixed,
		Iterations: a.options.ForecastIterations, PlayoffPlaces: playoffPlaces(a.options.Rules),
	})
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		a.renderError(w, r, fmt.Errorf("run forecast: %w", err))
		return
	}
	var comparison *simulation.Result
	if state.ComparisonModelID != "" {
		entry, _ := forecast.Lookup(state.ComparisonModelID)
		value, runErr := simulation.Run(r.Context(), simulation.Request{Teams: data.Teams, Games: standingsGames(data.Games), XGoals: xgoals, Model: entry.Model, Fixed: state.Fixed, Iterations: a.options.ForecastIterations, PlayoffPlaces: playoffPlaces(a.options.Rules)})
		if runErr != nil {
			if r.Context().Err() != nil {
				return
			}
			a.renderError(w, r, fmt.Errorf("run comparison forecast: %w", runErr))
			return
		}
		comparison = &value
	}
	a.render(w, "forecast", a.forecastPage(r, data, season, state, result, comparison, teamID))
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
		Title: "Forecast Lab · " + season + " NWSL season", Season: season,
		HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"),
		SeasonPath: seasonURL(r.URL.Path, season), ForecastPath: relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/forecast"),
		CanonicalPath: canonical, ResetPath: base,
		ModelName: result.Model.Name, ModelID: result.Model.ID, ModelDetail: result.Model.Description,
		Iterations: result.Iterations, FixedCount: result.FixedCount, Remaining: result.Remaining,
		Rows: forecastComparisonRows(result, comparison), Teams: forecastTeamOptions(data.Teams), FilteredTeam: teamID, HasTeamFilter: teamID != "", StateValues: state.Values(),
	}
	for _, entry := range forecast.Catalog() {
		page.Models = append(page.Models, forecastModelView{ID: entry.Model.Info().ID, Name: entry.Model.Info().Name, Default: entry.Default, Selected: entry.Model.Info().ID == state.ModelID, Comparison: entry.Model.Info().ID == state.ComparisonModelID, Detail: entry.Model.Info().Description, Inputs: entry.Model.Info().Inputs, Assumptions: entry.Model.Info().Assumptions})
	}
	page.HasComparison = comparison != nil
	if comparison != nil {
		page.ComparisonName = comparison.Model.Name
		page.ComparisonID = comparison.Model.ID
	}
	page.XGAvailable, page.XGCompleted = forecastXGCoverage(data)
	if page.XGCompleted > 0 {
		page.XGCoverage = fmt.Sprintf("%d of %d completed matches", page.XGAvailable, page.XGCompleted)
		page.XGWarning = float64(page.XGAvailable)/float64(page.XGCompleted) < .95
	}
	if data.LastSuccess != nil {
		page.Freshness, page.FreshnessFallback = freshnessValues(data.LastSuccess.FinishedAt, a.options.Location)
		page.DataCutoff = page.FreshnessFallback
	} else {
		page.DataCutoff = "Unavailable"
	}
	expectedGames := len(data.Teams) * a.options.Rules.GamesPerTeam / 2
	if len(data.Games) != expectedGames {
		page.ScheduleNote = fmt.Sprintf("The cache contains %d of %d expected regular-season fixtures. This forecast includes only fixtures currently in the cache.", len(data.Games), expectedGames)
	}
	// Keep every fixture in the page so the browser can update the selector
	// immediately when the team filter changes.
	page.Fixtures = forecastFixtures(data, state, a.options.Location)
	page.CanAdd = len(page.Fixtures) > 0
	if page.CanAdd {
		page.DefaultHomeTeam = page.Fixtures[0].Home.Name
		page.DefaultAwayTeam = page.Fixtures[0].Away.Name
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
