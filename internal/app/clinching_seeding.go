package app

import (
	"context"
	"fmt"
	"maps"
	"math/bits"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

type seedingPage struct {
	Team         teamNameView
	TeamCode     string
	Teams        []forecastTeamOption
	Builder      forecastPage
	Limitation   string
	Disciplinary bool
	// The grid has a row per reachable seed and a column per group of the
	// team's own results that leads to the same possible finishes.
	Columns     []seedingColumnView
	Legend      string
	LegendParts []scenarioRequirementPart
	// LikelihoodModel names the forecast behind the likelihoods; without one,
	// LikelihoodNote says why there are none.
	LikelihoodModel, LikelihoodNote string
	Rows                            []seedingRowView
	Conditions                      *seedingConditionsView
	Details                         *seedingAlternativesView
	HistoryNote                     string
}
type seedingColumnView struct {
	Lines      []string
	TokenLines [][]scenarioRequirementPart
	Likelihood string
	combos     [][]int
}
type seedingRowView struct {
	Heading, Detail, URL string
	Likelihood           string
	Selected             bool
	Cells                []seedingCellView
	seed                 int
	probability          float64
	opponentIDs          []string
	opponentLikelihoods  map[string]float64
}
type seedingCellView struct {
	Possible      bool
	Opponents     []string
	OpponentLogos []string
	Likelihood    string
	// opponentIDs parallels Opponents.
	opponentIDs []string
}

// seedingConditionsView states how the team finishes in one seed, grouped by
// its own results. Groups with too many ways to list say so instead.
type seedingConditionsView struct {
	Heading, State string
	// MostLikely marks a finish chosen because it is the likeliest.
	MostLikely  bool
	Unavailable bool
	// Opponents lists the seed's possible opponents, likeliest first.
	Opponents        []seedingOpponentView
	Groups           []seedingGroupView
	AlternativesURL  string
	Team             scenarioTeamToken
	FinishText       string
	ScenarioLinkText string
}

type seedingOpponentView struct {
	Code, Likelihood string
	Team             scenarioTeamToken
}

// seedingGroupView adds a summary to a group with too many ways to list.
type seedingGroupView struct {
	clinchingGroupView
	Overflow *seedingOverflowView
}

// seedingOverflowView summarizes a group's ways without listing them: results
// every way needs, then results each way needs some of. Each result is the
// union of what the ways that mention its match allow, so the statements are
// necessary conditions, not a recipe.
type seedingOverflowView struct {
	Ways   int
	Always []scenarioRequirement
	// Some holds one requirement per entry, so each renders on its own.
	Some [][]scenarioRequirement
	// SomeLow and SomeHigh bound how many of Some each way needs.
	SomeLow, SomeHigh int
	// Depends names matches whose needed result differs between ways.
	Depends []string
	// Matches names every fixture involved, including optional requirements
	// omitted from the compact summary when their minimum count is zero.
	Matches     []string
	Scores      bool
	MatchParts  [][]scenarioRequirementPart
	MoreMatches bool
}

// seedingMaxAlternatives bounds each own-result group's listed alternatives;
// longer lists are not readable, so the page asks for more fixed results.
const seedingMaxAlternatives = 5

// seedingOwnMatchLimit bounds the grid's columns: the team's later matches
// stay open, which keeps every cell exact.
const seedingOwnMatchLimit = 3

func seedingURL(path, team string, state forecaststate.State) string {
	q := url.Values{"view": {"seeding"}, "team": {team}}
	for _, value := range state.Values() {
		q.Add("p", value)
	}
	return relativeURL(path, path) + "?" + q.Encode()
}

func (a *application) renderSeeding(w http.ResponseWriter, r *http.Request, view clinchingPage, rules competition.Rules, data cache.SeasonData, snapshot cache.ScenarioSnapshot, labels map[string]string, games map[string]cache.Game) {
	q := r.URL.Query()
	selected := q.Get("team")
	teamID := ""
	options := []forecastTeamOption{}
	for _, team := range data.Teams {
		if _, ok := labels[team.ID]; !ok {
			continue
		}
		// Scope the picker to teams in this season's table.
		if !slices.ContainsFunc(view.Standings, func(row tableRowView) bool { return row.TeamID == team.ID }) {
			continue
		}
		code := labels[team.ID]
		options = append(options, forecastTeamOption{ID: code, Name: displayName(team)})
		if selected == code {
			teamID = team.ID
		}
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	if selected == "" && len(view.Standings) > 0 {
		teamID = view.Standings[0].TeamID
		selected = labels[teamID]
	}
	if teamID == "" {
		a.renderScenarioBadRequest(w, r, "Invalid seeding scenario", fmt.Errorf("unknown seeding team"))
		return
	}
	state, err := forecaststate.ParseV2("2", "seeding", "", q["p"], func(id string) bool { return id == "seeding" }, "seeding")
	if err == nil {
		for id := range state.Fixed {
			g, ok := games[id]
			if !ok || g.Status != simulation.RemainingStatus {
				err = fmt.Errorf("fixed result must name a remaining match")
				break
			}
		}
	}
	if err == nil && q.Get("action") != "" {
		if q.Get("action") != "add" {
			err = fmt.Errorf("unknown scenario action")
		} else {
			game, ok := games[q.Get("fixture")]
			if !ok || game.Status != simulation.RemainingStatus {
				err = fmt.Errorf("choose a remaining match")
			} else {
				state, err = state.With(game.ASAID, simulation.Outcome(q.Get("outcome")))
			}
		}
		if err == nil {
			redirectRelative(w, seedingURL(r.URL.Path, selected, state)+"#forecast-builder", http.StatusSeeOther)
			return
		}
	}
	if err != nil {
		a.renderScenarioBadRequest(w, r, "Invalid seeding scenario", err)
		return
	}
	seed := &seedingPage{TeamCode: selected, Teams: options}
	for _, team := range data.Teams {
		if team.ID == teamID {
			seed.Team = teamName(team)
		}
	}
	view.Seeding = seed
	builder := forecastPage{SeedingTeam: selected, ForecastPath: relativeURL(r.URL.Path, r.URL.Path), CanonicalPath: seedingURL(r.URL.Path, selected, state), ResetPath: seedingURL(r.URL.Path, selected, forecaststate.State{}), StateValues: state.Values(), Teams: forecastTeamOptions(data.Teams)}
	builder.AllFixtures = forecastFixtures(data, state, a.options.Location, "")
	builder.Fixtures = builder.AllFixtures
	builder.AtFixedLimit = len(state.Fixed) >= forecaststate.MaxFixed
	builder.CanAdd = len(builder.Fixtures) > 0 && !builder.AtFixedLimit
	builder.Remaining = len(snapshot.Run.Slate.FixtureIDs)
	builder.FixedCount = len(state.Fixed)
	if builder.CanAdd {
		builder.DefaultHomeTeam = builder.Fixtures[0].Home.Name
		builder.DefaultAwayTeam = builder.Fixtures[0].Away.Name
	}
	builder.Assumptions = forecastAssumptions(data, state, func(id string) string {
		return seedingURL(r.URL.Path, selected, state.Without(id)) + "#forecast-builder"
	}, a.options.Location)
	seed.Builder = builder
	finish, err := seedingFinish(q.Get("finish"))
	if err != nil {
		a.renderScenarioBadRequest(w, r, "Invalid seeding scenario", err)
		return
	}
	var cached *scenarios.SeedingResult
	for _, row := range snapshot.Results {
		if row.TeamID == teamID && row.Seeding != nil {
			cached = row.Seeding
			break
		}
	}
	if cached == nil {
		view.State = "Recalculation pending."
		a.render(w, "clinching", view)
		return
	}
	fixed := map[string]clinching.Outcome{}
	for id, o := range state.Fixed {
		switch o {
		case simulation.HomeWin:
			fixed[id] = clinching.HomeWin
		case simulation.Draw:
			fixed[id] = clinching.Draw
		case simulation.AwayWin:
			fixed[id] = clinching.AwayWin
		}
	}
	filtered := scenarios.FilterSeeding(*cached, fixed)
	seed.Disciplinary = filtered.DisciplinaryPossible
	if filtered.Limitation != "" {
		seed.Limitation = "Additional scenarios may exist. Some finishes could not be resolved within the calculation limits."
	}
	if filtered.Limitation == scenarios.LimitationBudgetPartial {
		seed.Limitation = "Scenario search was incomplete; additional scenarios may exist."
	}
	if q.Get("alternative") != "" {
		if finish == nil {
			a.renderScenarioBadRequest(w, r, "Invalid seeding scenario", fmt.Errorf("choose a finish"))
			return
		}
		details, err := seedingAlternatives(filtered, *finish, q, labels, games, fixedScenarioMasks(fixed), a.scenarioHistory(r.Context(), view.Season))
		if err != nil {
			a.renderScenarioBadRequest(w, r, "Invalid seeding scenario", err)
			return
		}
		details.Team = *teamPart(teamID, labels).Team
		seed.Details = details
		a.render(w, "seedingAlternatives", view)
		return
	}
	own := newSeedingOwnMatches(teamID, snapshot.Run.Slate.FixtureIDs, fixed, games)
	seed.Columns, seed.Rows = seedingGrid(filtered, fixed, own, labels, games)
	seed.Legend = own.legend(labels, games)
	if seed.Legend != "" {
		seed.LegendParts = append([]scenarioRequirementPart{{Text: "Columns show your results"}}, rivalFixtureParts(own.ids, teamID, labels, games)...)
		seed.LegendParts = append(seed.LegendParts, scenarioRequirementPart{Text: ", in that order: W win, D draw, L loss.", NoSpaceBefore: true})
	}
	counts, model := a.seedingLikelihoods(r.Context(), data, view.Season, rules, state)
	if counts != nil {
		seed.LikelihoodModel = model
		applySeedingLikelihoods(seed.Columns, seed.Rows, counts[teamID], own)
	} else if len(seed.Rows) > 0 {
		seed.LikelihoodNote = "Likelihoods are unavailable right now."
	}
	chosen, mostLikely := finish, false
	if chosen == nil && counts != nil && len(seed.Rows) > 0 {
		chosen, mostLikely = &seed.Rows[mostLikelyRow(seed.Rows)].seed, true
	}
	for i := range seed.Rows {
		key := seedingFinishKey(seed.Rows[i].seed)
		seed.Rows[i].URL = seedingFinishURL(r.URL.Path, selected, state, key)
		seed.Rows[i].Selected = chosen != nil && *chosen == seed.Rows[i].seed
	}
	if chosen != nil {
		seed.Conditions = seedingConditions(filtered, *chosen, own, labels, games)
		seed.Conditions.MostLikely = mostLikely
		detailQuery := r.URL.Query()
		detailQuery.Set("finish", seedingFinishKey(*chosen))
		seed.Conditions.AlternativesURL = seedingAlternativeURL(detailQuery, 1)
		history := a.scenarioHistory(r.Context(), view.Season)
		seed.HistoryNote = history.Note()
		for i := range seed.Conditions.Groups {
			groups := []clinchingGroupView{seed.Conditions.Groups[i].clinchingGroupView}
			presentScenarioGroups(groups, labels, games, fixedScenarioMasks(fixed), history)
			seed.Conditions.Groups[i].clinchingGroupView = groups[0]
		}
		for _, row := range seed.Rows {
			if row.seed != *chosen {
				continue
			}
			ids := slices.Clone(row.opponentIDs)
			sort.SliceStable(ids, func(a, b int) bool { return row.opponentLikelihoods[ids[a]] > row.opponentLikelihoods[ids[b]] })
			for _, id := range ids {
				seed.Conditions.Opponents = append(seed.Conditions.Opponents, seedingOpponentView{Code: labels[id], Team: *teamPart(id, labels).Team, Likelihood: row.opponentLikelihood(id, counts != nil)})
			}
		}
	}
	if finish != nil {
		seed.Builder.CanonicalPath = seedingFinishURL(r.URL.Path, selected, state, seedingFinishKey(*finish))
	}
	a.render(w, "clinching", view)
}

// seedingLikelihoods runs the default Forecast Lab model with the bracket's
// seed pairs and the fixed results, through the shared forecast executor and
// its cache. It returns nil when no forecast is available, so the page still
// renders without likelihoods.
func (a *application) seedingLikelihoods(ctx context.Context, data cache.SeasonData, season string, rules competition.Rules, state forecaststate.State) (map[string][]simulation.SeedingCount, string) {
	entry, ok := competition.Lookup(season, "Playoffs")
	if !ok || entry.BracketFormat == nil || a.forecasts == nil {
		return nil, ""
	}
	pairs := [][2]int{}
	for _, slot := range entry.BracketFormat.Slots {
		if slot.SeedPair != nil {
			pairs = append(pairs, *slot.SeedPair)
		}
	}
	places := playoffPlaces(rules)
	if len(pairs) == 0 || places != 2*len(pairs) {
		return nil, ""
	}
	model := forecast.Default().Model
	request := simulation.Request{Teams: data.Teams, Games: standingsGames(data.Games), XGoals: forecastXGoals(data), HistoricalVenue: forecastVenueSample(data), Model: model, Fixed: state.Fixed, Iterations: a.options.ForecastIterations, PlayoffPlaces: places, SeedPairs: pairs}
	key := forecastResultKey(data, state, model.Info().ID, a.options.ForecastIterations, places) + "\x00seeding"
	results, err := a.forecasts.results(withForecastTrigger(ctx, "http"), []forecastTask{{key: key, request: request}})
	if err != nil {
		return nil, ""
	}
	return results[0].Seedings, model.Info().Name
}

// applySeedingLikelihoods fills likelihoods from the simulated seasons and
// orders each cell's opponents likeliest first. Simulated pairings the exact
// search rules out are ignored.
func applySeedingLikelihoods(columns []seedingColumnView, rows []seedingRowView, counts []simulation.SeedingCount, m seedingOwnMatches) {
	column := func(c simulation.SeedingCount) int {
		combo := make([]int, len(m.ids))
		for j, id := range m.ids {
			mask := outcomeMask([]clinching.Outcome{clinchingOutcome(c.Own[id])})
			combo[j] = slices.Index(m.results[j][:], mask)
			if combo[j] < 0 {
				return -1
			}
		}
		for i, col := range columns {
			if slices.ContainsFunc(col.combos, func(other []int) bool { return slices.Equal(other, combo) }) {
				return i
			}
		}
		return -1
	}
	only := len(rows) == 1 && len(columns) == 1
	columnTotals := make([]float64, len(columns))
	for i := range rows {
		row := &rows[i]
		byOpponent := map[string]float64{}
		cellTotals := make([]float64, len(columns))
		cellOpponents := make([]map[string]float64, len(columns))
		for _, c := range counts {
			if c.Seed != row.seed || (row.seed > 0 && !slices.Contains(row.opponentIDs, c.OpponentID)) {
				continue
			}
			col := column(c)
			if col < 0 || (row.seed > 0 && !slices.Contains(row.Cells[col].opponentIDs, c.OpponentID)) || !row.Cells[col].Possible {
				continue
			}
			row.probability += c.Probability
			byOpponent[c.OpponentID] += c.Probability
			cellTotals[col] += c.Probability
			columnTotals[col] += c.Probability
			if cellOpponents[col] == nil {
				cellOpponents[col] = map[string]float64{}
			}
			cellOpponents[col][c.OpponentID] += c.Probability
		}
		row.Likelihood = likelihoodText(row.probability, len(rows) == 1)
		row.opponentLikelihoods = byOpponent
		for col := range row.Cells {
			cell := &row.Cells[col]
			if !cell.Possible {
				continue
			}
			cell.Likelihood = likelihoodText(cellTotals[col], only)
			order := slices.Clone(cell.opponentIDs)
			sort.SliceStable(order, func(a, b int) bool { return cellOpponents[col][order[a]] > cellOpponents[col][order[b]] })
			labels := map[string]string{}
			for k, id := range cell.opponentIDs {
				labels[id] = cell.Opponents[k]
			}
			cell.opponentIDs = order
			cell.Opponents = cell.Opponents[:0]
			cell.OpponentLogos = nil
			for _, id := range order {
				cell.Opponents = append(cell.Opponents, labels[id])
				cell.OpponentLogos = append(cell.OpponentLogos, clubLogoURL(id))
			}
		}
	}
	for i := range columns {
		columns[i].Likelihood = likelihoodText(columnTotals[i], len(columns) == 1)
	}
}

// mostLikelyRow picks the likeliest finish, the better seed on a tie.
func mostLikelyRow(rows []seedingRowView) int {
	best := 0
	for i, row := range rows {
		if row.probability > rows[best].probability {
			best = i
		}
	}
	return best
}

func clinchingOutcome(o simulation.Outcome) clinching.Outcome {
	switch o {
	case simulation.HomeWin:
		return clinching.HomeWin
	case simulation.Draw:
		return clinching.Draw
	default:
		return clinching.AwayWin
	}
}

// likelihoodText rounds a simulated share for a finish the exact search says
// is possible: never "0%", and "100%" only when it is the only finish.
func likelihoodText(p float64, only bool) string {
	switch {
	case only:
		return "100%"
	case p < 0.005:
		return "<1%"
	case p >= 0.995:
		return ">99%"
	default:
		return fmt.Sprintf("%.0f%%", p*100)
	}
}

func (row seedingRowView) opponentLikelihood(id string, available bool) string {
	if !available {
		return ""
	}
	return likelihoodText(row.opponentLikelihoods[id], len(row.opponentIDs) == 1 && row.Likelihood == "100%")
}

func seedingFinishKey(seed int) string {
	if seed == 0 {
		return "miss"
	}
	return strconv.Itoa(seed)
}

// seedingFinish parses the selected seed: nil when none is selected, and zero
// for missing the playoffs.
func seedingFinish(value string) (*int, error) {
	if value == "" {
		return nil, nil
	}
	if value == "miss" {
		zero := 0
		return &zero, nil
	}
	seed, err := strconv.Atoi(value)
	if err != nil || seed < 1 || seed > 32 {
		return nil, fmt.Errorf("invalid seeding finish")
	}
	return &seed, nil
}

func seedingFinishURL(path, team string, state forecaststate.State, finish string) string {
	u, _ := url.Parse(seedingURL(path, team, state))
	q := u.Query()
	q.Set("finish", finish)
	u.RawQuery = q.Encode()
	return u.String() + "#seeding-conditions"
}

func seedingHeading(seed, opponentSeed int) (string, string) {
	switch {
	case seed == 0:
		return "Misses the playoffs", ""
	case seed < opponentSeed:
		return ordinalRank(seed), "hosts " + ordinalRank(opponentSeed)
	default:
		return ordinalRank(seed), "at " + ordinalRank(opponentSeed)
	}
}

// seedingGrid narrows the filtered result by each combination of the team's
// own remaining results, then merges combinations with the same possible
// finishes into one column.
func seedingGrid(filtered scenarios.SeedingResult, fixed map[string]clinching.Outcome, m seedingOwnMatches, labels map[string]string, games map[string]cache.Game) ([]seedingColumnView, []seedingRowView) {
	seeds := slices.Clone(filtered.Seeds)
	sort.Slice(seeds, func(i, j int) bool {
		if (seeds[i].Seed == 0) != (seeds[j].Seed == 0) {
			return seeds[j].Seed == 0
		}
		return seeds[i].Seed < seeds[j].Seed
	})
	type column struct {
		combos [][]int
		cells  []seedingCellView
	}
	columns := []column{}
	index := map[string]int{}
	for _, combo := range m.combos {
		assumed := map[string]clinching.Outcome{}
		maps.Copy(assumed, fixed)
		for j, r := range combo {
			assumed[m.ids[j]] = scenarioOutcomes[bits.TrailingZeros8(m.results[j][r])]
		}
		narrowed := scenarios.FilterSeeding(filtered, assumed)
		cells := make([]seedingCellView, len(seeds))
		for i, s := range seeds {
			for _, n := range narrowed.Seeds {
				if n.Seed != s.Seed {
					continue
				}
				cells[i].Possible = true
				for _, o := range n.Opponents {
					cells[i].opponentIDs = append(cells[i].opponentIDs, o.TeamID)
				}
				sort.Slice(cells[i].opponentIDs, func(a, b int) bool { return labels[cells[i].opponentIDs[a]] < labels[cells[i].opponentIDs[b]] })
				for _, id := range cells[i].opponentIDs {
					cells[i].Opponents = append(cells[i].Opponents, labels[id])
					cells[i].OpponentLogos = append(cells[i].OpponentLogos, clubLogoURL(id))
				}
			}
		}
		key := fmt.Sprint(cells)
		at, ok := index[key]
		if !ok {
			at = len(columns)
			index[key] = at
			columns = append(columns, column{cells: cells})
		}
		columns[at].combos = append(columns[at].combos, combo)
	}
	views := make([]seedingColumnView, len(columns))
	for i, c := range columns {
		views[i].combos = c.combos
		if len(m.ids) == 1 {
			for _, cube := range seedingCover(c.combos, 1) {
				mask := uint8(0)
				for r := range 3 {
					if cube[0]&(1<<r) != 0 {
						mask |= m.results[0][r]
					}
				}
				views[i].TokenLines = append(views[i].TokenLines, ownHeadingParts(clinchingGroupView{Own: []scenarioRequirement{{GameID: m.ids[0], Mask: mask}}}, m.team, labels, games))
			}
		}
		lines := m.describe(c.combos, labels, games)
		if len(m.ids) > 1 {
			lines = m.shorthand(c.combos)
		}
		for _, line := range lines {
			if line == "" {
				line = "any result"
				if len(m.ids) == 0 {
					line = "with these results"
				}
			}
			views[i].Lines = append(views[i].Lines, strings.ToUpper(line[:1])+line[1:])
		}
	}
	rows := make([]seedingRowView, len(seeds))
	for i, s := range seeds {
		rows[i].seed = s.Seed
		rows[i].Heading, rows[i].Detail = seedingHeading(s.Seed, s.OpponentSeed)
		for _, o := range s.Opponents {
			rows[i].opponentIDs = append(rows[i].opponentIDs, o.TeamID)
		}
		for _, c := range columns {
			rows[i].Cells = append(rows[i].Cells, c.cells[i])
		}
	}
	return views, rows
}

// seedingOwnMatches are the team's first unfixed remaining matches. Later
// matches stay open, which keeps every statement about these exact.
type seedingOwnMatches struct {
	team string
	ids  []string
	// results[j] lists the team's win, draw and loss in ids[j] as outcome masks.
	results [][3]uint8
	// combos lists every combination of the team's results, wins first.
	combos [][]int
}

func newSeedingOwnMatches(team string, fixtureIDs []string, fixed map[string]clinching.Outcome, games map[string]cache.Game) seedingOwnMatches {
	m := seedingOwnMatches{team: team, combos: [][]int{{}}}
	for _, id := range fixtureIDs {
		g := games[id]
		if _, ok := fixed[id]; !ok && (g.HomeTeamID == team || g.AwayTeamID == team) {
			m.ids = append(m.ids, id)
		}
	}
	m.ids = fixtureIDsInKickoffOrder(m.ids, games)
	m.ids = m.ids[:min(len(m.ids), seedingOwnMatchLimit)]
	for _, id := range m.ids {
		results := [3]uint8{1, 2, 4}
		if games[id].AwayTeamID == team {
			results = [3]uint8{4, 2, 1}
		}
		m.results = append(m.results, results)
		next := [][]int{}
		for _, combo := range m.combos {
			for r := range 3 {
				next = append(next, append(slices.Clone(combo), r))
			}
		}
		m.combos = next
	}
	return m
}

// describe writes a set of combinations as lowercase phrases ("wins or draws
// at HOU"), one per product; "" stands for any result.
func (m seedingOwnMatches) describe(combos [][]int, labels map[string]string, games map[string]cache.Game) []string {
	cubes := seedingCover(combos, len(m.ids))
	if len(cubes) > 1 {
		if phrase, ok := m.pointsPhrase(combos); ok {
			return []string{phrase}
		}
	}
	lines := []string{}
	for _, cube := range cubes {
		words := []string{}
		for j, set := range cube {
			if set == 7 {
				continue
			}
			mask := uint8(0)
			for r := range 3 {
				if set&(1<<r) != 0 {
					mask |= m.results[j][r]
				}
			}
			text := requirementText(scenarioRequirement{GameID: m.ids[j], Mask: mask}, m.team, labels, games)
			words = append(words, strings.TrimPrefix(text, labels[m.team]+" "))
		}
		lines = append(lines, joinConditions(words))
	}
	return lines
}

// shorthand writes combinations for a column heading over several matches:
// a points range when exact, else products in W/D/L letters per match in
// kickoff order ("W/D–L"), with "any" for an open match.
func (m seedingOwnMatches) shorthand(combos [][]int) []string {
	cubes := seedingCover(combos, len(m.ids))
	if len(cubes) > 1 {
		if phrase, ok := m.pointsPhrase(combos); ok {
			return []string{strings.TrimPrefix(phrase, "takes ")}
		}
	}
	lines := []string{}
	for _, cube := range cubes {
		parts := []string{}
		for _, set := range cube {
			letters := []string{}
			for r, letter := range []string{"W", "D", "L"} {
				if set&(1<<r) != 0 {
					letters = append(letters, letter)
				}
			}
			part := strings.Join(letters, "/")
			if set == 7 {
				part = "any"
			}
			parts = append(parts, part)
		}
		lines = append(lines, strings.Join(parts, "–"))
	}
	return lines
}

// legend names the matches in kickoff order for shorthand headings.
func (m seedingOwnMatches) legend(labels map[string]string, games map[string]cache.Game) string {
	if len(m.ids) < 2 {
		return ""
	}
	matches := []string{}
	for _, id := range m.ids {
		g := games[id]
		if g.HomeTeamID == m.team {
			matches = append(matches, "vs "+labels[g.AwayTeamID])
		} else {
			matches = append(matches, "at "+labels[g.HomeTeamID])
		}
	}
	return "Columns show " + labels[m.team] + "’s results " + joinConditions(matches) + ", in that order: W win, D draw, L loss."
}

// pointsPhrase words a set of combinations as a points range ("takes 1 to 3
// points") when it is exactly every combination with a total in that range.
func (m seedingOwnMatches) pointsPhrase(combos [][]int) (string, bool) {
	points := func(combo []int) int {
		total := 0
		for _, r := range combo {
			total += []int{3, 1, 0}[r]
		}
		return total
	}
	low, high := 3*len(m.ids), 0
	inSet := map[string]bool{}
	for _, c := range combos {
		low, high = min(low, points(c)), max(high, points(c))
		inSet[fmt.Sprint(c)] = true
	}
	for _, c := range m.combos {
		if p := points(c); (p >= low && p <= high) != inSet[fmt.Sprint(c)] {
			return "", false
		}
	}
	unit := func(n int) string {
		if n == 1 {
			return "1 point"
		}
		return strconv.Itoa(n) + " points"
	}
	switch {
	case low == high:
		return "takes " + unit(low), true
	case high == 3*len(m.ids):
		return "takes " + unit(low) + " or more", true
	case low == 0:
		return "takes " + unit(high) + " or fewer", true
	default:
		return fmt.Sprintf("takes %d to %s", low, unit(high)), true
	}
}

// seedingCover writes a set of own-result combinations as a few products,
// each a set of the team's results (bit 0 win, 1 draw, 2 loss) per match.
// Products may overlap; together they cover exactly the set.
func seedingCover(combos [][]int, matches int) [][]uint8 {
	inSet := map[string]bool{}
	for _, c := range combos {
		inSet[fmt.Sprint(c)] = true
	}
	contains := func(cube []uint8) bool {
		all := true
		var walk func(int, []int)
		walk = func(j int, combo []int) {
			if !all {
				return
			}
			if j == matches {
				all = inSet[fmt.Sprint(combo)]
				return
			}
			for r := range 3 {
				if cube[j]&(1<<r) != 0 {
					walk(j+1, append(combo, r))
				}
			}
		}
		walk(0, []int{})
		return all
	}
	covered := map[string]bool{}
	cubes := [][]uint8{}
	for _, c := range combos {
		if covered[fmt.Sprint(c)] {
			continue
		}
		cube := make([]uint8, matches)
		for j, r := range c {
			cube[j] = 1 << r
		}
		for j := range matches {
			for r := range 3 {
				wider := slices.Clone(cube)
				wider[j] |= 1 << r
				if contains(wider) {
					cube = wider
				}
			}
		}
		for _, other := range combos {
			inside := true
			for j, r := range other {
				inside = inside && cube[j]&(1<<r) != 0
			}
			if inside {
				covered[fmt.Sprint(other)] = true
			}
		}
		cubes = append(cubes, cube)
	}
	return cubes
}

func seedingConditions(filtered scenarios.SeedingResult, seed int, own seedingOwnMatches, labels map[string]string, games map[string]cache.Game) *seedingConditionsView {
	team := own.team
	state := "Finishes " + ordinalRank(seed)
	heading := "How " + labels[team] + " finishes " + ordinalRank(seed)
	if seed == 0 {
		state = "Misses the playoffs"
		heading = "How " + labels[team] + " misses the playoffs"
	}
	view := &seedingConditionsView{Heading: heading, State: state, Team: *teamPart(team, labels).Team, FinishText: "finishes " + ordinalRank(seed), ScenarioLinkText: "finishing " + ordinalRank(seed)}
	if seed == 0 {
		view.FinishText = "misses the playoffs"
		view.ScenarioLinkText = "missing the playoffs"
	}
	index := slices.IndexFunc(filtered.Seeds, func(s scenarios.SeedFinish) bool { return s.Seed == seed })
	if index < 0 {
		view.Unavailable = true
		return view
	}
	listed := filtered.Seeds[index].Clauses
	limit := seedingMaxAlternatives
	if len(listed) > seedingMaxAlternatives {
		limit = 0
	}
	for _, group := range clinchingGroupViews(listed, team, labels, labels, games, clinchingGroupOptions{SummaryLimit: limit, RestOfSeason: true}) {
		if len(group.paths) == 1 && len(group.paths[0]) == 0 {
			group.SummaryOverflow = false
		}
		g := seedingGroupView{clinchingGroupView: group}
		if group.SummaryOverflow {
			g.Overflow = seedingOverflow(group.paths, labels, games)
		}
		view.Groups = append(view.Groups, g)
	}
	return view
}

// seedingOverflow summarizes ways too many to list. A match's result is the
// union of what the ways that mention it allow; a match whose union is every
// result is only named in Depends. Points totals across two matches are kept
// as they are, and score conditions are only noted.
func seedingOverflow(paths [][]scenarioRequirement, labels map[string]string, games map[string]cache.Game) *seedingOverflowView {
	view := &seedingOverflowView{Ways: len(paths)}
	type item struct {
		requirement scenarioRequirement
		ways        int
	}
	items := map[string]*item{}
	order := []string{}
	keys := make([][]string, len(paths))
	for i, path := range paths {
		seen := map[string]bool{}
		for _, r := range path {
			if r.Score != nil {
				view.Scores = true
				continue
			}
			key := r.GameID
			if r.SecondGameID != "" {
				key = requirementKey(r)
			}
			it, ok := items[key]
			if !ok {
				it = &item{requirement: scenarioRequirement{GameID: r.GameID, SecondGameID: r.SecondGameID, TeamID: r.TeamID, Points: r.Points}}
				items[key] = it
				order = append(order, key)
			}
			if r.SecondGameID == "" {
				it.requirement.Mask |= r.Mask
			}
			if !seen[key] {
				seen[key] = true
				it.ways++
				keys[i] = append(keys[i], key)
			}
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return fixtureBefore(items[order[a]].requirement.GameID, items[order[b]].requirement.GameID, games)
	})
	some := map[string]bool{}
	seenMatches := map[string]bool{}
	for _, key := range order {
		it := items[key]
		r := it.requirement
		for _, id := range []string{r.GameID, r.SecondGameID} {
			if id == "" || seenMatches[id] {
				continue
			}
			seenMatches[id] = true
			g := games[id]
			view.Matches = append(view.Matches, labels[g.HomeTeamID]+" vs "+labels[g.AwayTeamID])
			if len(view.MatchParts) < 4 {
				view.MatchParts = append(view.MatchParts, []scenarioRequirementPart{teamPart(g.HomeTeamID, labels), {Text: "vs"}, teamPart(g.AwayTeamID, labels)})
			} else {
				view.MoreMatches = true
			}
		}
		if r.SecondGameID == "" && r.Mask == 7 {
			g := games[r.GameID]
			view.Depends = append(view.Depends, labels[g.HomeTeamID]+" vs "+labels[g.AwayTeamID])
			continue
		}
		r.Text = requirementText(r, "", labels, games)
		r.Parts = requirementParts(r, "", labels, games)
		if it.ways == len(paths) {
			view.Always = append(view.Always, r)
		} else {
			view.Some = append(view.Some, []scenarioRequirement{r})
			some[key] = true
		}
	}
	view.SomeLow = len(view.Some)
	for _, path := range keys {
		n := 0
		for _, key := range path {
			if some[key] {
				n++
			}
		}
		view.SomeLow, view.SomeHigh = min(view.SomeLow, n), max(view.SomeHigh, n)
	}
	return view
}

// SomeText introduces the results each way needs some of.
func (v seedingOverflowView) SomeText() string {
	if v.SomeLow == 0 {
		return ""
	}
	if v.SomeLow == v.SomeHigh {
		return fmt.Sprintf("Each way also needs %d of these:", v.SomeLow)
	}
	return fmt.Sprintf("Each way also needs %d to %d of these:", v.SomeLow, v.SomeHigh)
}

func (v seedingOverflowView) PreviewAlways() []scenarioRequirement {
	return v.Always[:min(3, len(v.Always))]
}
func (v seedingOverflowView) PreviewSome() [][]scenarioRequirement {
	if len(v.Some) > 3 || v.SomeLow == 0 {
		return nil
	}
	return v.Some
}
