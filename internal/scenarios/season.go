package scenarios

import (
	"context"
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// LimitationSeasonMultiwayTie notes rest-of-season scorelines left unclaimed:
// three or more teams level through head-to-head points who still play each
// other, so head-to-head goals among them depend on those scores, or a leaf
// too large to expand.
const LimitationSeasonMultiwayTie = "some scenarios where three or more teams finish level on points, goal difference, wins, goals scored and head-to-head points are not listed"

const limitationSeasonTiebreakOnly = "a clinch may depend on unavailable disciplinary points or on head-to-head goals among three or more level teams; no scenario is published"

// maxSeasonFixtures bounds the packed outcome cube. Two matchdays of a
// 16-team league is 16 matches; postponements add a few.
const maxSeasonFixtures = 4 * cubeSlotsPerWord

// maxSeasonAlternatives bounds the "beat any m of these n rivals" expansion
// at one leaf. Larger leaves are counted as unresolved.
const maxSeasonAlternatives = 64

// SeasonRequest asks for rest-of-season scenarios for one team.
type SeasonRequest struct {
	Teams        []standings.Team
	Games        []standings.Game
	Slate        Slate
	TargetTeamID string
	Achievements []competition.Achievement
	Baselines    map[competition.AchievementID]clinching.AchievementResult
}

// GenerateSeason searches every assignment of results to the remaining
// matches. Each one is a completed season apart from scores, so a points tie
// that straddles a threshold is settled by the official tiebreakers: results
// decide wins and head-to-head points, and goal difference, goals scored and
// head-to-head goals scored become score conditions on the clause.
func GenerateSeason(ctx context.Context, r SeasonRequest) (map[competition.AchievementID]Result, error) {
	started := time.Now()
	if r.TargetTeamID == "" || len(r.Achievements) == 0 || len(r.Achievements) > 64 || r.Slate.Source != SourceRestOfSeason {
		return nil, fmt.Errorf("invalid rest-of-season request")
	}
	if err := r.Slate.Validate(); err != nil {
		return nil, err
	}
	inSlate := make(map[string]int, len(r.Slate.FixtureIDs))
	for i, id := range r.Slate.FixtureIDs {
		inSlate[id] = i
	}
	slateProblem := ""
	pendingCount := 0
	for _, game := range r.Games {
		if game.Status != fixtures.PreMatchStatus {
			continue
		}
		pendingCount++
		if _, ok := inSlate[game.ID]; !ok {
			slateProblem = "rest-of-season slate does not cover every remaining match"
		}
	}
	if pendingCount != len(r.Slate.FixtureIDs) {
		slateProblem = "rest-of-season slate does not match the remaining matches"
	}
	if len(r.Slate.FixtureIDs) > maxSeasonFixtures {
		slateProblem = "too many remaining matches for a rest-of-season search"
	}

	results := make(map[competition.AchievementID]Result, len(r.Achievements))
	members := []seasonMember{}
	total := pow3(len(r.Slate.FixtureIDs))
	for _, achievement := range r.Achievements {
		if achievement.ID == "" || achievement.TopK < 1 {
			return nil, fmt.Errorf("invalid scenario achievement")
		}
		if _, duplicate := results[achievement.ID]; duplicate {
			return nil, fmt.Errorf("duplicate scenario achievement %q", achievement.ID)
		}
		baseline, ok := r.Baselines[achievement.ID]
		if !ok || baseline.TeamID != r.TargetTeamID || baseline.Achievement != achievement.ID || baseline.TopK != achievement.TopK {
			return nil, fmt.Errorf("scenario baseline does not match achievement %q", achievement.ID)
		}
		out := emptyResult(r.TargetTeamID, achievement)
		out.TotalAssignments = total
		switch {
		case baseline.Status == clinching.Clinched:
			out.State, out.AlreadyClinched = OpportunityAlreadyClinched, true
		case baseline.Status == clinching.Unresolved && baseline.Method != clinching.ProofUnprovedScoreTiebreak:
			out.State, out.Limitation = OpportunityUnresolved, limitation(baseline)
		case slateProblem != "":
			out.State, out.Limitation = OpportunityUnresolved, slateProblem
		default:
			members = append(members, seasonMember{achievement: achievement, labels: map[string]int{}})
		}
		results[achievement.ID] = out
	}
	if len(members) > 0 {
		search, err := newSeasonSearch(ctx, r, inSlate, members)
		if err != nil {
			return nil, err
		}
		search.walk(uint64(1)<<uint(len(members)) - 1)
		if search.err != nil && ctx.Err() == nil {
			return nil, search.err
		}
		complete := search.err == nil
		var rows []rivalRow
		rowsOK := false
		if complete {
			rows, rowsOK = search.rivalRows()
		}
		for i := range search.members {
			member := &search.members[i]
			out := member.finish(ctx, results[member.achievement.ID], r.Slate.FixtureIDs, complete)
			if rowsOK && out.State == OpportunityCanClinch {
				out.Rivals = rivalSummary(rows, member.achievement.TopK)
			}
			results[out.Achievement] = out
		}
	}
	// Search elimination independently with points bounds. Unlike the general
	// slate search, this does not repeatedly invoke the qualification oracle or
	// enumerate exact coverage across the full remaining season.
	if out, ok := results[competition.AchievementPlayoffs]; ok && !out.AlreadyClinched && slateProblem == "" {
		achievement := competition.Achievement{ID: out.Achievement, TopK: out.TopK}
		search, err := newSeasonSearch(ctx, r, inSlate, []seasonMember{{achievement: achievement, labels: map[string]int{}}})
		if err != nil {
			return nil, err
		}
		search.minimum, search.pointsOnly = true, true
		search.walk(1)
		proof := search.members[0].finish(ctx, emptyResult(r.TargetTeamID, achievement), r.Slate.FixtureIDs, search.err == nil)
		for _, clause := range proof.Clauses {
			if len(clause.Conditions) == 0 {
				out.AlreadyEliminated = true
				break
			}
			out.EliminationClauses = append(out.EliminationClauses, clause)
		}
		out.CanBeEliminated = !out.AlreadyEliminated && len(out.EliminationClauses) > 0
		if search.err != nil && out.Limitation == "" {
			out.Limitation = LimitationBudgetPartial
		}
		results[out.Achievement] = out
	}
	setBatchElapsed(results, started)
	return results, nil
}

type seasonMember struct {
	achievement competition.Achievement
	labels      map[string]int
	labelSets   []seasonLabel
	clauses     []seasonClause
	// certified counts assignments with at least one published scenario;
	// unresolved counts those with none whose clinch was left unclaimed, and
	// disciplinaryOnly those with none where only disciplinary points could
	// give the clinch.
	certified, unresolved, disciplinaryOnly int
	partial, disciplinary                   bool
	diag                                    Diagnostics
}

// A label is the part of a clause that is not an outcome cube: its score
// conditions and proof method. Cubes merge only within one label, which ties
// each score condition to the results it was derived from.
type seasonLabel struct {
	conditions []ScoreCondition
	method     clinching.ProofMethod
}

type seasonClause struct {
	label int
	cube  outcomeCube
}

type seasonGame struct {
	id         string
	home, away int
	slot       int
}

type seasonSearch struct {
	// exact asks for exactly TopK rather than at most TopK.
	exact      bool
	minimum    bool
	pointsOnly bool
	ctx        context.Context
	slateSize  int
	target     int
	teamIDs    []string
	games      []seasonGame
	outcome    []int8
	// points and wins include completed matches and fixed outcomes;
	// unfixed counts each team's remaining matches not yet fixed.
	points, wins, unfixed []int
	// Completed-match values, for score conditions measured from now.
	gdNow, gfNow []int
	// h2hPoints[a][b] is the points a earned against b in completed matches.
	h2hPoints [][]int
	// h2hGoals[a][b] is the goals a scored against b in completed matches.
	h2hGoals  [][]int
	teamGames [][]int
	current   outcomeCube
	fixed     int
	members   []seasonMember
	nodes     int
	err       error
}

func newSeasonSearch(ctx context.Context, r SeasonRequest, slot map[string]int, members []seasonMember) (*seasonSearch, error) {
	index := make(map[string]int, len(r.Teams))
	teamIDs := make([]string, len(r.Teams))
	for i, team := range r.Teams {
		index[team.ID] = i
		teamIDs[i] = team.ID
	}
	target, ok := index[r.TargetTeamID]
	if !ok {
		return nil, fmt.Errorf("target team %q not found", r.TargetTeamID)
	}
	n := len(r.Teams)
	s := &seasonSearch{
		ctx: ctx, slateSize: len(r.Slate.FixtureIDs), target: target, teamIDs: teamIDs, members: members,
		points: make([]int, n), wins: make([]int, n), unfixed: make([]int, n), gdNow: make([]int, n), gfNow: make([]int, n),
		h2hPoints: make([][]int, n), h2hGoals: make([][]int, n),
		teamGames: make([][]int, n), current: allOutcomes(len(r.Slate.FixtureIDs)),
	}
	for i := range s.h2hPoints {
		s.h2hPoints[i] = make([]int, n)
		s.h2hGoals[i] = make([]int, n)
	}
	pending := []seasonGame{}
	for _, game := range r.Games {
		home, homeOK := index[game.HomeTeamID]
		away, awayOK := index[game.AwayTeamID]
		if !homeOK || !awayOK || home == away {
			return nil, fmt.Errorf("game %q has unknown teams", game.ID)
		}
		switch game.Status {
		case standings.CompletedStatus:
			if game.HomeScore == nil || game.AwayScore == nil {
				return nil, fmt.Errorf("completed game %q lacks score", game.ID)
			}
			s.addCompleted(home, away, *game.HomeScore, *game.AwayScore)
		case fixtures.PreMatchStatus:
			pending = append(pending, seasonGame{id: game.ID, home: home, away: away, slot: slot[game.ID]})
			s.unfixed[home]++
			s.unfixed[away]++
		default:
			return nil, fmt.Errorf("game %q has unsafe status %q", game.ID, game.Status)
		}
	}
	s.games = orderSeasonGames(pending, s.points, target)
	s.outcome = make([]int8, len(s.games))
	for i, game := range s.games {
		s.outcome[i] = -1
		s.teamGames[game.home] = append(s.teamGames[game.home], i)
		s.teamGames[game.away] = append(s.teamGames[game.away], i)
	}
	return s, nil
}

func (s *seasonSearch) teamID(team int) string { return s.teamIDs[team] }

func (s *seasonSearch) addCompleted(home, away, homeScore, awayScore int) {
	s.gdNow[home] += homeScore - awayScore
	s.gdNow[away] += awayScore - homeScore
	s.gfNow[home] += homeScore
	s.gfNow[away] += awayScore
	homePoints, awayPoints := 1, 1
	switch {
	case homeScore > awayScore:
		homePoints, awayPoints = 3, 0
		s.wins[home]++
	case awayScore > homeScore:
		homePoints, awayPoints = 0, 3
		s.wins[away]++
	}
	s.points[home] += homePoints
	s.points[away] += awayPoints
	s.h2hPoints[home][away] += homePoints
	s.h2hPoints[away][home] += awayPoints
	s.h2hGoals[home][away] += homeScore
	s.h2hGoals[away][home] += awayScore
}

// orderSeasonGames puts the target's matches first, then matches whose teams
// are closest to it on points, so the search settles relevant results early.
func orderSeasonGames(games []seasonGame, points []int, target int) []seasonGame {
	ordered := append([]seasonGame(nil), games...)
	rank := func(game seasonGame) (int, int) {
		if game.home == target || game.away == target {
			return 0, 0
		}
		return 1, min(absInt(points[game.home]-points[target]), absInt(points[game.away]-points[target]))
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		it, id := rank(ordered[i])
		jt, jd := rank(ordered[j])
		if it != jt {
			return it < jt
		}
		if id != jd {
			return id < jd
		}
		return ordered[i].slot < ordered[j].slot
	})
	return ordered
}

func (s *seasonSearch) set(index int, outcome int8) {
	game := s.games[index]
	s.outcome[index] = outcome
	s.unfixed[game.home]--
	s.unfixed[game.away]--
	s.current.set(game.slot, outcomeMasks[outcome])
	s.fixed++
	s.applyOutcome(game, outcome, 1)
}

func (s *seasonSearch) clear(index int) {
	game := s.games[index]
	s.applyOutcome(game, s.outcome[index], -1)
	s.outcome[index] = -1
	s.unfixed[game.home]++
	s.unfixed[game.away]++
	s.current.set(game.slot, allMask)
	s.fixed--
}

func (s *seasonSearch) applyOutcome(game seasonGame, outcome int8, sign int) {
	switch outcome {
	case 0:
		s.points[game.home] += 3 * sign
		s.wins[game.home] += sign
	case 1:
		s.points[game.home] += sign
		s.points[game.away] += sign
	case 2:
		s.points[game.away] += 3 * sign
		s.wins[game.away] += sign
	}
}

func (s *seasonSearch) walk(active uint64) {
	if s.err != nil {
		return
	}
	s.nodes++
	if s.nodes%1024 == 0 {
		if err := s.ctx.Err(); err != nil {
			s.err = err
			return
		}
	}
	t := s.target
	low, high := s.points[t], s.points[t]+3*s.unfixed[t]
	certainlyAhead, possiblyAhead := 0, 0
	for team, points := range s.points {
		if team == t {
			continue
		}
		if points > high {
			certainlyAhead++
		}
		if points+3*s.unfixed[team] >= low {
			possiblyAhead++
		}
	}
	represented := pow3(s.slateSize - s.fixed)
	for i := range s.members {
		bit := uint64(1) << uint(i)
		if active&bit == 0 {
			continue
		}
		member := &s.members[i]
		member.diag.SearchNodes++
		switch {
		case s.minimum && certainlyAhead >= member.achievement.TopK:
			member.emit(s.current, seasonLabel{method: clinching.ProofCheapBound})
			member.certified += represented
			active &^= bit
		case s.minimum && possiblyAhead < member.achievement.TopK:
			active &^= bit
		case s.minimum:
		case s.exact && (certainlyAhead >= member.achievement.TopK || possiblyAhead < member.achievement.TopK-1):
			member.diag.OpportunityPrunes++
			active &^= bit
		case s.exact && certainlyAhead == possiblyAhead && certainlyAhead == member.achievement.TopK-1:
			member.emit(s.current, seasonLabel{method: clinching.ProofCheapBound})
			member.certified += represented
			active &^= bit
		case s.exact:
			// The threshold bounds alone cannot settle this exact place.
		case certainlyAhead >= member.achievement.TopK:
			member.diag.OpportunityPrunes++
			active &^= bit
		case possiblyAhead < member.achievement.TopK:
			member.emit(s.current, seasonLabel{method: clinching.ProofCheapBound})
			member.certified += represented
			member.diag.GuaranteePrunes++
			active &^= bit
		}
	}
	if active == 0 {
		return
	}
	next := s.nextRelevant(low, high)
	if next < 0 {
		if !s.pointsOnly {
			s.leaf(active, certainlyAhead, represented)
		}
		return
	}
	for outcome := int8(0); outcome < 3; outcome++ {
		s.set(next, outcome)
		s.walk(active)
		s.clear(next)
		if s.err != nil {
			return
		}
	}
}

// nextRelevant returns the first unfixed match with a team whose points range
// overlaps the target's. A match between two teams that are certainly above or
// below the target cannot change its place, so it stays unconstrained.
func (s *seasonSearch) nextRelevant(low, high int) int {
	relevant := func(team int) bool {
		return team == s.target || (s.points[team] <= high && s.points[team]+3*s.unfixed[team] >= low)
	}
	for i, game := range s.games {
		if s.outcome[i] < 0 && (relevant(game.home) || relevant(game.away)) {
			return i
		}
	}
	return -1
}

// leaf settles a node whose relevant matches are all fixed: the target's
// points are final, and every team level with it has played out its season.
// Rivals that can finish level with the target through goals scored, and so
// reach head-to-head, are expanded state by state; the rest are compared
// pairwise.
func (s *seasonSearch) leaf(active uint64, ahead, represented int) {
	t := s.target
	simple, levels := []pairResult{}, []levelRival{}
	for team, points := range s.points {
		if team == t || points != s.points[t] {
			continue
		}
		if lr, ok := s.levelRegions(team); ok {
			levels = append(levels, lr)
		} else {
			simple = append(simple, s.pair(team))
		}
	}
	untied := len(simple)+len(levels) == 0
	for i := range s.members {
		if active&(uint64(1)<<uint(i)) == 0 {
			continue
		}
		member := &s.members[i]
		member.diag.VisitedComplete++
		slots := member.achievement.TopK - 1 - ahead
		var set alternativeSet
		if len(levels) > maxSeasonLevelRivals {
			// Count every level rival as finishing ahead: what still holds is
			// certain, and the rest is left unclaimed.
			if !s.exact && !s.minimum {
				set = s.chooseSimple(simple, len(levels), slots)
			}
			set.unresolved = true
		} else {
			set = s.levelAlternatives(levels, make([]int, 0, len(levels)), simple, slots)
		}
		member.settle(s.current, set, untied, represented)
	}
}

func (m *seasonMember) settle(cube outcomeCube, set alternativeSet, untied bool, represented int) {
	m.disciplinary = m.disciplinary || set.disciplinary
	if len(set.alternatives) == 0 {
		switch {
		case set.unresolved:
			m.unresolved += represented
		case set.disciplinary:
			m.disciplinaryOnly += represented
		}
		return
	}
	for _, conditions := range set.alternatives {
		method := clinching.ProofAccessibleTiebreak
		if untied {
			method = clinching.ProofPointsOptimization
		}
		m.emit(cube, seasonLabel{conditions: joinHeadToHead(conditions), method: method})
	}
	m.certified += represented
	m.partial = m.partial || set.unresolved
}

func (m *seasonMember) emit(cube outcomeCube, label seasonLabel) {
	key := labelKey(label)
	id, ok := m.labels[key]
	if !ok {
		id = len(m.labelSets)
		m.labels[key] = id
		m.labelSets = append(m.labelSets, label)
	}
	m.clauses = append(m.clauses, seasonClause{label: id, cube: cube})
}

func labelKey(label seasonLabel) string {
	keys := make([]string, len(label.conditions))
	for i, condition := range label.conditions {
		keys[i] = scoreConditionKey(condition)
	}
	return string(label.method) + "|" + strings.Join(keys, "|")
}

func binomial(n, k int) int {
	value := 1
	for i := range k {
		value = value * (n - i) / (i + 1)
	}
	return value
}

func (m *seasonMember) finish(ctx context.Context, out Result, slate []string, complete bool) Result {
	out.Diagnostics = m.diag
	out.Diagnostics.InitialClauses = len(m.clauses)
	merged := mergeSeasonClauses(ctx, m.clauses, len(slate))
	out.Diagnostics.MinimalClauses = len(merged)
	out.Clauses = make([]Clause, 0, len(merged))
	union := allOutcomes(len(slate))
	for i := range slate {
		union.set(i, 0)
	}
	for _, clause := range merged {
		label := m.labelSets[clause.label]
		conditions := []FixtureCondition{}
		represented := 1
		for i, id := range slate {
			mask := clause.cube.mask(i)
			union.set(i, union.mask(i)|mask)
			represented *= bits.OnesCount8(mask)
			if mask != allMask {
				conditions = append(conditions, FixtureCondition{GameID: id, AllowedOutcomes: maskOutcomes(mask)})
			}
		}
		out.Clauses = append(out.Clauses, Clause{Conditions: conditions, ScoreConditions: label.conditions, RepresentedAssignments: represented, ProofMethods: []clinching.ProofMethod{label.method}})
		out.ProofMethods = append(out.ProofMethods, label.method)
	}
	sort.SliceStable(out.Clauses, func(i, j int) bool {
		left, right := out.Clauses[i], out.Clauses[j]
		if len(left.Conditions)+len(left.ScoreConditions) != len(right.Conditions)+len(right.ScoreConditions) {
			return len(left.Conditions)+len(left.ScoreConditions) < len(right.Conditions)+len(right.ScoreConditions)
		}
		return clauseKey(left.Conditions) < clauseKey(right.Conditions)
	})
	out.ProofMethods = sortedMethods(out.ProofMethods)
	out.CertifiedAssignments, out.UnresolvedAssignments = m.certified, m.unresolved+m.disciplinaryOnly
	out.DisciplinaryPossible = m.disciplinary && (m.certified > 0 || m.unresolved > 0 || m.disciplinaryOnly > 0)
	switch {
	case !complete:
		out.Limitation = LimitationBudgetPartial
		if m.certified > 0 {
			out.State, out.CanClinch = OpportunityCanClinch, true
		} else {
			out.State = OpportunityUnresolved
		}
	case m.certified > 0:
		out.State, out.CanClinch = OpportunityCanClinch, true
		if m.partial || m.unresolved > 0 {
			out.Limitation = LimitationSeasonMultiwayTie
		} else {
			for i, id := range slate {
				if mask := union.mask(i); mask != allMask {
					out.Necessary = append(out.Necessary, FixtureCondition{GameID: id, AllowedOutcomes: maskOutcomes(mask)})
				}
			}
		}
	case m.unresolved > 0 || m.disciplinaryOnly > 0:
		out.State, out.Limitation = OpportunityTiebreakDependent, limitationSeasonTiebreakOnly
	default:
		out.State = OpportunityCannotClinch
	}
	return out
}

// mergeSeasonClauses joins cubes with the same label that differ in one
// match. The search emits disjoint cubes, and a merge keeps them disjoint
// while preserving their exact union. An expired context returns the clauses
// merged so far, which remain a valid, if longer, description.
func mergeSeasonClauses(ctx context.Context, clauses []seasonClause, slots int) []seasonClause {
	type key struct {
		label int
		cube  outcomeCube
	}
	for changed := true; changed; {
		changed = false
		for slot := range slots {
			if ctx.Err() != nil {
				return clauses
			}
			index := make(map[key]int, len(clauses))
			out := make([]seasonClause, 0, len(clauses))
			for _, clause := range clauses {
				k := key(clause)
				k.cube.set(slot, allMask)
				if i, ok := index[k]; ok {
					out[i].cube.set(slot, out[i].cube.mask(slot)|clause.cube.mask(slot))
					changed = true
					continue
				}
				index[k] = len(out)
				out = append(out, clause)
			}
			clauses = out
		}
	}
	return clauses
}

const cubeSlotsPerWord = 21

// outcomeCube packs one three-bit outcome mask per slate fixture.
type outcomeCube [4]uint64

func allOutcomes(slots int) outcomeCube {
	var cube outcomeCube
	for i := range slots {
		cube.set(i, allMask)
	}
	return cube
}

// outcomeMasks maps an outcome index (home win, draw, away win) to its bit.
var outcomeMasks = [3]uint8{homeMask, drawMask, awayMask}

func (c outcomeCube) mask(slot int) uint8 {
	return uint8(c[slot/cubeSlotsPerWord] >> (uint(slot%cubeSlotsPerWord) * 3) & uint64(allMask))
}

func (c *outcomeCube) set(slot int, mask uint8) {
	shift := uint(slot%cubeSlotsPerWord) * 3
	word := &c[slot/cubeSlotsPerWord]
	*word = *word&^(uint64(allMask)<<shift) | uint64(mask)<<shift
}
