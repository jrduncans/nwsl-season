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
	"sync"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/trace"
)

const defaultForecastIterations = 50000

// PrecacheForecasts calculates the baseline (zero fixed assumptions) result
// for every Forecast Lab model and stores each result in this application's
// process-local forecast cache. It is intended for server startup; individual
// model failures do not prevent later models from being attempted.
func (a *Application) PrecacheForecasts(ctx context.Context) error {
	return a.PrecacheForecastsWithTrigger(ctx, "unspecified")
}

// PrecacheForecastsWithTrigger warms baseline forecasts and identifies the
// operation that requested the warm-up (for example, startup or post_sync).
func (a *Application) PrecacheForecastsWithTrigger(ctx context.Context, trigger string) error {
	ctx = withForecastTrigger(ctx, trigger)
	return a.app.precacheForecasts(ctx, forecastTrigger(ctx))
}

func (a *application) precacheForecasts(ctx context.Context, trigger string) (err error) {
	ctx, span := telemetry.Tracer().Start(ctx, nwslconv.SpanForecastPrecache, trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(nwslconv.ForecastPreload(true), nwslconv.ForecastTrigger(trigger), nwslconv.SeasonName(a.options.CurrentSeason), nwslconv.Stage(a.options.Stage)),
	)
	modelCount := 0
	failedModels := 0
	workerCount := 0
	recordPrecacheException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, span, cause, nwslconv.ErrorCodeForecastPrecache, errorType)
	}
	defer func() {
		outcome := nwslconv.ForecastPrecacheOutcomeComplete
		if err != nil {
			outcome = nwslconv.ForecastPrecacheOutcomeFailure
			telemetry.MarkError(span, err)
		}
		span.SetAttributes(nwslconv.ForecastModelCount(modelCount), nwslconv.ForecastFailedModelCount(failedModels), nwslconv.ForecastPrecacheWorkerCount(workerCount), nwslconv.ForecastPrecacheOutcome(outcome))
		span.End()
	}()
	rules, verified := a.rulesForSeason(a.options.CurrentSeason)
	scope := requestCompetition{Season: a.options.CurrentSeason, Stage: a.options.Stage}
	if entry, ok := competition.Lookup(scope.Season, scope.Stage); ok {
		scope.Entry, scope.Cataloged = entry, true
	}
	if !scope.forecastAvailable(rules, verified) {
		return nil
	}
	if a.store == nil {
		return recordPrecacheException(fmt.Errorf("season cache unavailable"), telemetry.ErrorTypeInvalidArgument)
	}
	data, err := a.loadSeasonData(ctx, a.options.CurrentSeason)
	if err != nil {
		return fmt.Errorf("load %s season: %w", a.options.CurrentSeason, err)
	}
	if len(data.Games) == 0 {
		return recordPrecacheException(fmt.Errorf("no cached games found for %s %s", a.options.CurrentSeason, a.options.Stage), telemetry.ErrorTypeInvalidData)
	}

	state := forecaststate.State{Fixed: map[string]simulation.Outcome{}}
	xgoals := forecastXGoals(data)
	venue := forecastVenueSample(data)
	games := standingsGames(data.Games)
	places := playoffPlaces(rules)
	entries := forecast.Catalog()
	modelCount = len(entries)
	// Warm the model selected by a bare Forecast Lab URL first, so a startup
	// time budget still prioritizes the most common first request.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Default && !entries[j].Default })

	tasks := make([]forecastTask, 0, len(entries))
	for _, entry := range entries {
		request := simulation.Request{
			Teams: data.Teams, Games: games, XGoals: xgoals, HistoricalVenue: venue,
			Model: entry.Model, Fixed: state.Fixed, Iterations: a.options.ForecastIterations, PlayoffPlaces: places,
		}
		tasks = append(tasks, forecastTask{
			key:     forecastResultKey(data, state, entry.Model.Info().ID, a.options.ForecastIterations, places),
			request: request,
		})
	}

	// Use the same capacity as live Forecast Lab work. Each model keeps its own
	// forecast deadline, while parallel workers avoid serially extending server
	// startup by one timeout for every model.
	workers := min(len(tasks), cap(a.forecasts.slots))
	workerCount = workers
	work := make(chan forecastTask)
	errs := make(chan error, len(tasks))
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for task := range work {
				if _, err := a.forecasts.results(ctx, []forecastTask{task}); err != nil {
					errs <- fmt.Errorf("warm forecast model %s: %w", task.request.Model.Info().ID, err)
				}
			}
		}()
	}
	for _, task := range tasks {
		work <- task
	}
	close(work)
	group.Wait()
	close(errs)
	values := errorsFrom(errs)
	failedModels = len(values)
	return errors.Join(values...)
}

func errorsFrom(values <-chan error) []error {
	errs := make([]error, 0, cap(values))
	for err := range values {
		errs = append(errs, err)
	}
	return errs
}

func (a *application) forecast(w http.ResponseWriter, r *http.Request) {
	// Direct callers (not the ServeMux) may provide a canonical URL without
	// path values. Preserve that narrow compatibility seam for tests and
	// embedded callers; routed legacy URLs still redirect below.
	if r.PathValue("stage") == "" && strings.Contains(r.URL.Path, "/regular-season/") {
		r.SetPathValue("season", strings.Split(strings.TrimPrefix(r.URL.Path, "/seasons/"), "/")[0])
		r.SetPathValue("stage", "regular-season")
	}
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
		a.renderUnavailableFeature(w, r, scope, "Forecast lab")
		return
	}
	query := r.URL.Query()
	state, err := forecaststate.ParseV2(query.Get("v"), query.Get("m"), query.Get("c"), query["p"], func(id string) bool { _, ok := forecast.Lookup(id); return ok }, forecast.Default().Model.Info().ID)
	if err != nil {
		a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", err)
		return
	}
	state.ModelID = forecast.CanonicalID(state.ModelID)
	state.ComparisonModelID = forecast.CanonicalID(state.ComparisonModelID)
	trace.SpanFromContext(r.Context()).SetAttributes(nwslconv.ForecastTrigger("http"), nwslconv.ForecastModelID(state.ModelID), nwslconv.ForecastComparisonRequested(state.ComparisonModelID != ""), nwslconv.ForecastFixedAssumptionCount(len(state.Fixed)))
	data, season, rules, err := a.forecastData(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	presentation := classifySeasonPhase(data, scope.Entry.Inventory)
	if presentation.Phase == seasonPhaseComplete {
		a.renderUnavailableFeatureWithNavigation(w, r, scope, "Forecast lab", seasonNavigationForPresentation(r.URL.Path, scope, "", rules, verified, presentation))
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
		redirectRelative(w, forecastURL(r.URL.Path, season, scope.Entry.Slug, state, ""), http.StatusSeeOther)
		return
	}
	active, ok := forecast.Lookup(state.ModelID)
	if !ok {
		a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", fmt.Errorf("unsupported forecast model %q", state.ModelID))
		return
	}
	xgoals := forecastXGoals(data)
	venue := forecastVenueSample(data)
	places := playoffPlaces(rules)
	request := simulation.Request{Teams: data.Teams, Games: standingsGames(data.Games), XGoals: xgoals, HistoricalVenue: venue, Model: active.Model, Fixed: state.Fixed, Iterations: a.options.ForecastIterations, PlayoffPlaces: places}
	tasks := []forecastTask{{key: forecastResultKey(data, state, active.Model.Info().ID, a.options.ForecastIterations, places), request: request}}
	if state.ComparisonModelID != "" {
		entry, _ := forecast.Lookup(state.ComparisonModelID)
		tasks = append(tasks, forecastTask{key: forecastResultKey(data, state, entry.Model.Info().ID, a.options.ForecastIterations, places), request: simulation.Request{Teams: data.Teams, Games: standingsGames(data.Games), XGoals: xgoals, HistoricalVenue: venue, Model: entry.Model, Fixed: state.Fixed, Iterations: a.options.ForecastIterations, PlayoffPlaces: places}})
	}
	results, err := a.forecasts.results(withForecastTrigger(r.Context(), "http"), tasks)
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
	a.render(w, "forecast", a.forecastPage(r, data, season, rules, state, result, comparison, teamID))
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
		parts = append(parts, game.ID, game.Status, game.HomeTeamID, game.AwayTeamID, game.Kickoff.UTC().Format(time.RFC3339Nano))
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

func (a *application) forecastData(r *http.Request) (data cache.SeasonData, season string, rules competition.Rules, err error) {
	if a.store == nil {
		return cache.SeasonData{}, "", competition.Rules{}, fmt.Errorf("season cache unavailable")
	}
	scope := a.requestScope(r)
	season = scope.Season
	var verified bool
	rules, verified = a.rulesForSeason(season, scope.Stage)
	if !scope.forecastAvailable(rules, verified) {
		return cache.SeasonData{}, "", competition.Rules{}, fmt.Errorf("forecast is unavailable for %s %s", season, scope.Stage)
	}
	data, err = a.loadSeasonData(r.Context(), season, scope.Stage)
	if err != nil {
		return cache.SeasonData{}, "", competition.Rules{}, fmt.Errorf("load %s season: %w", season, err)
	}
	if len(data.Games) == 0 {
		return cache.SeasonData{}, "", competition.Rules{}, fmt.Errorf("no cached games found for %s %s", season, a.options.Stage)
	}
	return data, season, rules, nil
}

func (a *application) forecastPage(r *http.Request, data cache.SeasonData, season string, rules competition.Rules, state forecaststate.State, result simulation.Result, comparison *simulation.Result, teamID string) forecastPage {
	scope := a.requestScope(r)
	_, verified := a.rulesForSeason(season, scope.Stage)
	presentation := classifySeasonPhase(data, scope.Entry.Inventory)
	base := forecastURL(r.URL.Path, season, scope.Entry.Slug, forecaststate.State{ModelID: result.Model.ID, ComparisonModelID: state.ComparisonModelID, Fixed: map[string]simulation.Outcome{}}, "")
	canonical := forecastURL(r.URL.Path, season, scope.Entry.Slug, state, "")
	page := forecastPage{
		Title: "Forecast lab · " + season + " NWSL season", Season: season, Stage: scope.Stage,
		HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"),
		SeasonPath: relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)), ForecastPath: relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)+"/forecast"),
		Navigation: seasonNavigationForPresentation(r.URL.Path, scope, r.URL.Path, rules, verified, presentation), ModelEvaluationPath: relativeURL(r.URL.Path, stageURL(season, scope.Entry.Slug)+"/model-evaluation"),
		CanonicalPath: canonical, ResetPath: base,
		ModelName: result.Model.Name, ModelID: result.Model.ID, ModelDetail: result.Model.Description,
		Iterations: result.Iterations, FixedCount: result.FixedCount, Remaining: result.Remaining,
		Rows: forecastComparisonRows(result, comparison, playoffPlaces(rules)), Teams: forecastTeamOptions(data.Teams), FilteredTeam: teamID, HasTeamFilter: teamID != "", StateValues: state.Values(), PlayoffPlaces: playoffPlaces(rules),
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
	page.ScheduleNote = forecastScheduleNote(data, scope.Entry.Inventory, usesHistoricalVenue, page.ShowXGCoverage)
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
		return forecastURL(r.URL.Path, season, scope.Entry.Slug, state.Without(gameID), "")
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

func forecastScheduleNote(data cache.SeasonData, inventory *competition.InventoryExpectation, usesHistoricalVenue, requireXG bool) string {
	notes := make([]string, 0, 3)
	if usesHistoricalVenue && !historicalVenueReady(data, requireXG) {
		notes = append(notes, "Two-season home/away history is still syncing; venue rates temporarily use this season only.")
	}
	if inventory != nil && inventory.Games > 0 && len(data.Games) != inventory.Games {
		notes = append(notes, fmt.Sprintf("Cache has %d of %d expected regular-season fixtures.", len(data.Games), inventory.Games))
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
	if inventory != nil && inventory.GamesPerTeam > 0 {
		for _, team := range data.Teams {
			if appearances[team.ID] != inventory.GamesPerTeam {
				teamsWithUnexpectedCounts++
			}
		}
		if teamsWithUnexpectedCounts > 0 {
			notes = append(notes, fmt.Sprintf("%d team(s) do not have the expected %d fixtures.", teamsWithUnexpectedCounts, inventory.GamesPerTeam))
		}
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

func forecastURL(fromPath, season, stageSlug string, state forecaststate.State, teamID string) string {
	target := stageURL(season, stageSlug) + "/forecast"
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
	a.render(w, "error", errorPage{Title: title, Message: err.Error(), HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"), SeasonSelector: seasonSelector(r.URL.Path, a.requestScope(r).Season)})
}
