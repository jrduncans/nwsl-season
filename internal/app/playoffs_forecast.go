package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/trace"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func playoffsForecastAvailable(season string, data cache.SeasonData, rules competition.Rules, verified bool) bool {
	entry, ok := competition.Lookup(season, "Regular Season")
	scope := requestCompetition{Season: season, Stage: entry.Stage, Entry: entry, Cataloged: ok}
	return scope.forecastAvailable(rules, verified) && finalStandingsSafe(classifySeasonPhase(data, entry.Inventory)) && forecastPlayoffBracket(season, playoffPlaces(rules)) != nil
}

func (a *application) playoffsForecast(w http.ResponseWriter, r *http.Request, scope requestCompetition) {
	rules, verified := a.rulesForSeason(scope.Season, "Regular Season")
	// Preserve capability gating before cache access for unsupported seasons.
	regular, ok := competition.Lookup(scope.Season, "Regular Season")
	regularScope := requestCompetition{Season: scope.Season, Stage: "Regular Season", Entry: regular, Cataloged: ok}
	if !regularScope.forecastAvailable(rules, verified) {
		a.renderUnavailableFeature(w, r, scope, "Forecast lab")
		return
	}
	data, err := a.loadSeasonData(r.Context(), scope.Season, "Regular Season")
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	if !playoffsForecastAvailable(scope.Season, data, rules, verified) {
		a.renderUnavailableFeature(w, r, scope, "Forecast lab")
		return
	}
	query := r.URL.Query()
	state, err := forecaststate.ParsePlayoffs(query.Get("v"), query.Get("m"), query.Get("c"), query["p"], func(id string) bool { _, ok := forecast.Lookup(id); return ok }, forecast.Default().Model.Info().ID)
	if err != nil {
		a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", err)
		return
	}
	state.ModelID, state.ComparisonModelID = forecast.CanonicalID(state.ModelID), forecast.CanonicalID(state.ComparisonModelID)
	trace.SpanFromContext(r.Context()).SetAttributes(nwslconv.ForecastTrigger("http"), nwslconv.ForecastModelID(state.ModelID), nwslconv.ForecastComparisonRequested(state.ComparisonModelID != ""), nwslconv.ForecastFixedAssumptionCount(len(state.PlayoffWinners)))
	places := playoffPlaces(rules)
	postseason, err := a.forecastPostseason(r.Context(), scope.Season, data, places)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	format := forecastPlayoffBracket(scope.Season, places)
	eligible := playoffScenarioSlots(*format, postseason)
	validate := func(state forecaststate.State) bool {
		for id, match := range state.PlayoffWinners {
			current, ok := eligible[id]
			samePair := (current.Home == match.Home && current.Away == match.Away) || (current.Home == match.Away && current.Away == match.Home)
			if !ok || !samePair || (match.Winner != current.Home && match.Winner != current.Away) {
				return false
			}
		}
		return true
	}
	if !validate(state) {
		a.renderStaleForecast(w, r, scope, state)
		return
	}
	if action := query.Get("action"); action != "" {
		if action != "add" {
			a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", fmt.Errorf("unsupported forecast action %q", action))
			return
		}
		selection, err := forecaststate.ParsePlayoffs("2", state.ModelID, state.ComparisonModelID, []string{query.Get("selection")}, func(id string) bool { _, ok := forecast.Lookup(id); return ok }, state.ModelID)
		if err != nil || !validate(selection) {
			a.renderStaleForecast(w, r, scope, state)
			return
		}
		for id, match := range selection.PlayoffWinners {
			state = state.WithPlayoffWinner(id, match)
		}
		redirectRelative(w, forecastURL(r.URL.Path, scope.Season, "playoffs", state, query.Get("team")), http.StatusSeeOther)
		return
	}
	teamID := query.Get("team")
	if teamID != "" && !slices.ContainsFunc(playoffQualifiers(scope.Season, data, places), func(team playoffQualifier) bool { return team.ID == teamID }) {
		a.renderScenarioBadRequest(w, r, "Invalid forecast scenario", fmt.Errorf("unknown playoff team %q", teamID))
		return
	}
	if len(state.PlayoffWinners) > 0 {
		postseason.Scenarios = state.PlayoffWinners
	}
	tasks := a.forecastTasks(data, state, places, postseason)
	if err := simulation.CheckPostseason(tasks[0].request); err != nil {
		a.renderStaleForecast(w, r, scope, state)
		return
	}
	results, err := a.playoffForecastResults(r.Context(), tasks, postseason)
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
		a.renderError(w, r, err)
		return
	}
	var comparison *simulation.Result
	if len(results) > 1 {
		comparison = &results[1]
	}
	base, err := a.forecastPage(r, data, scope.Season, rules, state, results[0], comparison, teamID)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	metadata, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	base.Navigation = metadata.Navigation
	base.ModelEvaluationPath = relativeURL(r.URL.Path, stageURL(scope.Season, "regular-season")+"/model-evaluation")
	base.FixedCount = len(state.PlayoffWinners)
	page := playoffsForecastView(base, data, postseason, *format, results[0], comparison)
	labels := bracketSlotLabels(*format)
	for _, slot := range format.Slots {
		match, ok := eligible[slot.ID]
		if !ok {
			continue
		}
		if fixed, ok := state.PlayoffWinners[slot.ID]; ok {
			page.PlayoffAssumptions = append(page.PlayoffAssumptions, forecastAssumptionView{Fixture: labels[slot.ID] + ": " + playoffTeamName(data, fixed.Winner) + " advances", Remove: forecastURL(r.URL.Path, scope.Season, "playoffs", state.WithoutPlayoffWinner(slot.ID), teamID)})
			continue
		}
		if teamID != "" && match.Home != teamID && match.Away != teamID {
			continue
		}
		choice := playoffScenarioMatch{ID: slot.ID, Label: labels[slot.ID], Fixture: playoffTeamName(data, match.Home) + " vs " + playoffTeamName(data, match.Away)}
		for _, winner := range []string{match.Home, match.Away} {
			match.Winner = winner
			selected := forecaststate.State{PlayoffWinners: map[string]simulation.PostseasonMatch{slot.ID: match}}
			choice.Options = append(choice.Options, playoffScenarioOption{Value: selected.Values()[0], Label: playoffTeamName(data, winner)})
		}
		page.ScenarioMatches = append(page.ScenarioMatches, choice)
	}
	a.render(w, "playoffs-forecast", page)
}

// Eligible slots use recorded advancement only; one assumption cannot make
// another match eligible before its participants are established by the cache.
func playoffScenarioSlots(format competition.BracketFormat, state *simulation.Postseason) map[string]simulation.PostseasonMatch {
	result := map[string]simulation.PostseasonMatch{}
	if state == nil || state.Unavailable != "" {
		return result
	}
	sources := map[string][]string{}
	for _, connection := range format.Connections {
		sources[connection.DestinationSlotID] = connection.SourceSlotIDs
	}
	for _, slot := range format.Slots {
		actual := state.Slots[slot.ID]
		if actual.Winner != "" {
			continue
		}
		match := actual
		if match.Home == "" {
			if slot.SeedPair != nil {
				match.Home, match.Away = state.Seeds[slot.SeedPair[0]], state.Seeds[slot.SeedPair[1]]
			} else {
				from := sources[slot.ID]
				match.Home, match.Away = state.Slots[from[0]].Winner, state.Slots[from[1]].Winner
			}
		}
		if match.Home != "" && match.Away != "" {
			result[slot.ID] = match
		}
	}
	return result
}

type playoffQualifier struct {
	ID   string
	Seed int
}

func playoffQualifiers(season string, data cache.SeasonData, places int) []playoffQualifier {
	table, err := competition.OfficialStandings(season, data.Teams, standingsGames(data.Games), standings.OrderOfficialTotal)
	if err != nil {
		return nil
	}
	var result []playoffQualifier
	for i, row := range table {
		if i >= places {
			break
		}
		safe := true
		if row.TieBreak.Undetermined {
			for j, other := range table {
				if j >= places && slices.Contains(row.TieBreak.TiedTeamIDs, other.Team.ID) {
					safe = false
				}
			}
		}
		if safe {
			result = append(result, playoffQualifier{ID: row.Team.ID, Seed: i + 1})
		}
	}
	return result
}

func playoffTeamName(data cache.SeasonData, id string) string {
	for _, team := range data.Teams {
		if team.ID == id {
			return teamName(team).Name
		}
	}
	return id
}

type playoffScenarioOption struct{ Value, Label string }
type playoffScenarioMatch struct {
	ID, Label, Fixture string
	Options            []playoffScenarioOption
}
type playoffForecastCell struct{ Text, Intensity string }
type playoffForecastRow struct {
	Team                 teamNameView
	Seed                 int
	SeedText             string
	Rounds               []string
	Ends                 []playoffForecastCell
	Champion, Comparison string
	Out                  bool
	Elimination          int
	Chance               float64
}
type playoffsForecastPage struct {
	forecastPage
	PlayoffRows        []playoffForecastRow
	RoundColumns       []string
	EndColumns         []string
	Warning            string
	ScenarioMatches    []playoffScenarioMatch
	PlayoffAssumptions []forecastAssumptionView
	BracketRounds      []playoffForecastRound
}

func playoffsForecastView(base forecastPage, data cache.SeasonData, state *simulation.Postseason, format competition.BracketFormat, result simulation.Result, comparison *simulation.Result) playoffsForecastPage {
	page := playoffsForecastPage{forecastPage: base, Warning: state.Unavailable}
	page.ScheduleNote = ""
	rows := map[string]simulation.TeamResult{}
	comparisons := map[string]simulation.TeamResult{}
	for _, row := range result.Teams {
		rows[row.Team.ID] = row
	}
	if comparison != nil {
		for _, row := range comparison.Teams {
			comparisons[row.Team.ID] = row
		}
	}
	roundNames := map[string]string{}
	var columns []string
	for ri, round := range format.Rounds {
		name := singularRoundLabel(round.Label)
		roundNames[round.ID] = strings.ToLower(name)
		page.EndColumns = append(page.EndColumns, "Out in "+strings.ToLower(name))
		complete := true
		for _, slot := range format.Slots {
			if slot.RoundID == round.ID && state.Slots[slot.ID].Winner == "" {
				complete = false
			}
		}
		if ri > 0 && !complete {
			page.RoundColumns = append(page.RoundColumns, "Reach "+strings.ToLower(name))
			columns = append(columns, round.ID)
		}
	}
	// Champion follows Team, so later rounds read outward from it.
	slices.Reverse(page.RoundColumns)
	slices.Reverse(columns)
	page.EndColumns[len(page.EndColumns)-1] = "Runner-up"
	page.EndColumns = append(page.EndColumns, "Champion")
	for _, qualifier := range playoffQualifiers(base.Season, data, len(format.Slots)+1) {
		row := rows[qualifier.ID]
		item := playoffForecastRow{Team: teamName(row.Team), Seed: qualifier.Seed, Chance: row.ChampionshipProbability, Out: row.ChampionshipEliminated || slices.Contains(state.Eliminated, qualifier.ID), Elimination: -1}
		item.SeedText = "—"
		for seed, id := range state.Seeds {
			if id == qualifier.ID {
				item.SeedText = strconv.Itoa(seed)
			}
		}
		eliminatedRound := row.EliminatedRound
		if eliminatedRound == "" {
			eliminatedRound = state.EliminatedRounds[qualifier.ID]
		}
		eliminated := "Out"
		if eliminatedRound != "" {
			eliminated = "Out in " + roundNames[eliminatedRound]
		}
		for ri, round := range format.Rounds {
			if eliminatedRound == round.ID {
				item.Elimination = ri
			}
		}
		for _, round := range columns {
			value := "—"
			switch {
			case state.Unavailable != "":
				if item.Out {
					value = "Out"
				}
			case row.ReachedRounds[round]:
				value = "Through"
			case item.Out:
				value = eliminated
			default:
				value = forecastChance(row.RoundReach[round], false, false)
			}
			item.Rounds = append(item.Rounds, value)
		}
		item.Champion = forecastChance(row.ChampionshipProbability, row.ChampionshipClinched, row.ChampionshipEliminated)
		item.Comparison = forecastChance(comparisons[qualifier.ID].ChampionshipProbability, row.ChampionshipClinched, row.ChampionshipEliminated)
		if state.Unavailable != "" {
			item.Champion = "—"
			item.Comparison = "—"
			if item.Out {
				item.Champion = "Out"
				item.Comparison = "Out"
			}
		}
		for ri, round := range format.Rounds {
			probability := row.RunEnds[round.ID]
			exact := row.EliminatedRound != "" || row.ChampionshipClinched || (ri+1 < len(format.Rounds) && row.ReachedRounds[format.Rounds[ri+1].ID])
			item.Ends = append(item.Ends, playoffEndCell(probability, exact, state.Unavailable != "", item.Out))
		}
		item.Ends = append(item.Ends, playoffEndCell(row.ChampionshipProbability, row.ChampionshipClinched || row.ChampionshipEliminated, state.Unavailable != "", item.Out))
		page.PlayoffRows = append(page.PlayoffRows, item)
	}
	sort.Slice(page.PlayoffRows, func(i, j int) bool {
		a, b := page.PlayoffRows[i], page.PlayoffRows[j]
		if a.Out != b.Out {
			return !a.Out
		}
		if a.Out && a.Elimination != b.Elimination {
			return a.Elimination > b.Elimination
		}
		if !a.Out && a.Chance != b.Chance {
			return a.Chance > b.Chance
		}
		return a.Seed < b.Seed
	})
	page.BracketRounds = playoffBracketForecast(format, state, result)
	return page
}

func playoffEndCell(probability float64, exact, withheld, out bool) playoffForecastCell {
	if withheld {
		if out {
			return playoffForecastCell{Text: "Out"}
		}
		return playoffForecastCell{Text: "—"}
	}
	return playoffForecastCell{Text: forecastChance(probability, exact && probability == 1, exact && probability == 0), Intensity: fmt.Sprintf("%.4f%%", probability*100)}
}

// Withheld pages show facts without requiring a model fit through an unsafe
// snapshot. Available forecasts reuse the same executor as baseline warming.
func (a *application) playoffForecastResults(ctx context.Context, tasks []forecastTask, state *simulation.Postseason) ([]simulation.Result, error) {
	if state.Unavailable == "" {
		return a.forecasts.results(withForecastTrigger(ctx, "http"), tasks)
	}
	results := make([]simulation.Result, 0, len(tasks))
	for _, task := range tasks {
		result := simulation.Result{Model: task.request.Model.Info()}
		for _, team := range task.request.Teams {
			result.Teams = append(result.Teams, simulation.TeamResult{Team: team})
		}
		results = append(results, result)
	}
	return results, nil
}
