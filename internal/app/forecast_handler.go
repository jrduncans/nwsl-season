package app

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

const defaultForecastIterations = 50000

func (a *application) forecast(w http.ResponseWriter, r *http.Request) {
	model := forecast.NewResultsPoissonV1()
	query := r.URL.Query()
	state, err := forecaststate.Parse(query.Get("v"), query.Get("m"), query["p"], model.Info().ID)
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

	result, err := simulation.Run(r.Context(), simulation.Request{
		Teams: data.Teams, Games: standingsGames(data.Games), Model: model, Fixed: state.Fixed,
		Iterations: a.options.ForecastIterations, PlayoffPlaces: a.options.PlayoffPlaces,
	})
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		a.renderError(w, r, fmt.Errorf("run forecast: %w", err))
		return
	}
	a.render(w, "forecast", a.forecastPage(r, data, season, state, result, teamID))
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

func (a *application) forecastPage(r *http.Request, data cache.SeasonData, season string, state forecaststate.State, result simulation.Result, teamID string) forecastPage {
	base := forecastURL(r.URL.Path, season, forecaststate.State{ModelID: result.Model.ID, Fixed: map[string]simulation.Outcome{}}, "")
	canonical := forecastURL(r.URL.Path, season, state, "")
	page := forecastPage{
		Title: "Forecast Lab · " + season + " NWSL season", Season: season,
		HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"),
		SeasonPath: seasonURL(r.URL.Path, season), ForecastPath: relativeURL(r.URL.Path, "/seasons/"+url.PathEscape(season)+"/forecast"),
		CanonicalPath: canonical, ResetPath: base,
		ModelName: result.Model.Name, ModelID: result.Model.ID, ModelDetail: result.Model.Description,
		Iterations: result.Iterations, FixedCount: result.FixedCount, Remaining: result.Remaining,
		Rows: forecastRows(result), Teams: forecastTeamOptions(data.Teams), FilteredTeam: teamID, HasTeamFilter: teamID != "", StateValues: state.Values(),
	}
	if data.LastSuccess != nil {
		page.DataCutoff = data.LastSuccess.FinishedAt.In(a.options.Location).Format("Jan 2, 2006 at 3:04 PM MST")
	} else {
		page.DataCutoff = "Unavailable"
	}
	expectedGames := len(data.Teams) * a.options.GamesPerTeam / 2
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
	if len(state.Fixed) > 0 || teamID != "" {
		values.Set("v", forecaststate.EncodingVersion)
		values.Set("m", state.ModelID)
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
