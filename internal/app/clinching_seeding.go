package app

import (
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
	Columns    []seedingColumnView
	Legend     string
	Rows       []seedingRowView
	Conditions *seedingConditionsView
}
type seedingColumnView struct {
	Lines []string
}
type seedingRowView struct {
	Heading, Detail, URL string
	Selected             bool
	Cells                []seedingCellView
	seed                 int
}
type seedingCellView struct {
	Possible  bool
	Opponents []string
}

// seedingConditionsView states how the team finishes in one seed, grouped by
// its own results. Groups with too many ways to list say so instead.
type seedingConditionsView struct {
	Heading, State string
	Unavailable    bool
	Groups         []clinchingGroupView
	// BigSwing notes unlisted alternatives that need a large tiebreaker swing;
	// OnlyBigSwing says every listed alternative needs one.
	BigSwing, OnlyBigSwing bool
	LongShotOnly           []string
}

// seedingMaxAlternatives bounds each own-result group's listed alternatives;
// longer lists are not readable, so the page asks for more fixed results.
const seedingMaxAlternatives = 5

// seedingBigSwing is the tiebreaker gain, in goals over the remaining
// matches, from which an alternative counts as a long shot.
const seedingBigSwing = 4

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

func (a *application) renderSeeding(w http.ResponseWriter, r *http.Request, view clinchingPage, data cache.SeasonData, snapshot cache.ScenarioSnapshot, labels map[string]string, games map[string]cache.Game) {
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
	own := newSeedingOwnMatches(teamID, snapshot.Run.Slate.FixtureIDs, fixed, games)
	seed.Columns, seed.Rows = seedingGrid(filtered, fixed, own, labels, games)
	seed.Legend = own.legend(labels, games)
	for i := range seed.Rows {
		key := seedingFinishKey(seed.Rows[i].seed)
		seed.Rows[i].URL = seedingFinishURL(r.URL.Path, selected, state, key)
		seed.Rows[i].Selected = finish != nil && *finish == seed.Rows[i].seed
	}
	if finish != nil {
		seed.Conditions = seedingConditions(filtered, *finish, own, labels, games)
		seed.Builder.CanonicalPath = seedingFinishURL(r.URL.Path, selected, state, seedingFinishKey(*finish))
	}
	a.render(w, "clinching", view)
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
		assumed := maps.Clone(fixed)
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
					cells[i].Opponents = append(cells[i].Opponents, labels[o.TeamID])
				}
				sort.Strings(cells[i].Opponents)
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
	sort.SliceStable(m.ids, func(i, j int) bool { return games[m.ids[i]].KickoffUTC < games[m.ids[j]].KickoffUTC })
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

// allows reports whether a clause permits the team's results in combo.
func (m seedingOwnMatches) allows(clause scenarios.Clause, combo []int) bool {
	for _, c := range clause.Conditions {
		if j := slices.Index(m.ids, c.GameID); j >= 0 && outcomeMask(c.AllowedOutcomes)&m.results[j][combo[j]] == 0 {
			return false
		}
	}
	return true
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
	view := &seedingConditionsView{Heading: heading, State: state}
	index := slices.IndexFunc(filtered.Seeds, func(s scenarios.SeedFinish) bool { return s.Seed == seed })
	if index < 0 {
		view.Unavailable = true
		return view
	}
	listed, big := []scenarios.Clause{}, []scenarios.Clause{}
	for _, clause := range filtered.Seeds[index].Clauses {
		if needsBigSwing(clause) {
			big = append(big, clause)
		} else {
			listed = append(listed, clause)
		}
	}
	if len(listed) == 0 {
		listed, big, view.OnlyBigSwing = big, nil, true
	}
	// Own results that only long shots reach get a heading of their own, so
	// every own result the grid shows for this seed is accounted for.
	longShotOnly := [][]int{}
	for _, combo := range own.combos {
		reaches := func(clauses []scenarios.Clause) bool {
			return slices.ContainsFunc(clauses, func(c scenarios.Clause) bool { return own.allows(c, combo) })
		}
		if !reaches(listed) && reaches(big) {
			longShotOnly = append(longShotOnly, combo)
		} else if reaches(big) {
			view.BigSwing = true
		}
	}
	if len(longShotOnly) > 0 {
		for _, line := range own.describe(longShotOnly, labels, games) {
			heading := "Regardless of " + labels[team] + "’s results"
			if line != "" {
				heading = "If " + labels[team] + " " + line
			}
			view.LongShotOnly = append(view.LongShotOnly, heading)
		}
	}
	view.Groups = clinchingGroupViews(listed, team, labels, labels, games, clinchingGroupOptions{SummaryLimit: seedingMaxAlternatives, RestOfSeason: true})
	return view
}

// needsBigSwing reports whether an alternative needs some team to gain at
// least seedingBigSwing on a rival in a tiebreaker.
func needsBigSwing(clause scenarios.Clause) bool {
	for _, score := range clause.ScoreConditions {
		if len(score.Levels) == 0 {
			continue
		}
		level := score.Levels[0]
		gain := level.MinimumGain
		if level.ExactOnly {
			gain--
		}
		if gain >= seedingBigSwing {
			return true
		}
	}
	return false
}

// BigSwingGoals is the long-shot threshold, for the page's wording.
func (seedingPage) BigSwingGoals() int { return seedingBigSwing }
