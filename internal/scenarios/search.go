package scenarios

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type Request struct {
	Evaluator    *clinching.Evaluator
	Teams        []standings.Team
	Games        []standings.Game
	Slate        Slate
	TargetTeamID string
	Achievement  competition.Achievement
	Baseline     clinching.AchievementResult
}

// BatchRequest evaluates every achievement for one team in a shared slate
// traversal. Baselines are keyed by achievement ID.
type BatchRequest struct {
	Evaluator    *clinching.Evaluator
	Teams        []standings.Team
	Games        []standings.Game
	Slate        Slate
	TargetTeamID string
	Achievements []competition.Achievement
	Baselines    map[competition.AchievementID]clinching.AchievementResult
}

// Generate searches all three-outcome completions, pruning only statements
// which the Phase 12 points oracle has already certified.
func Generate(ctx context.Context, r Request) (Result, error) {
	values, err := GenerateBatch(ctx, BatchRequest{
		Evaluator: r.Evaluator, Teams: r.Teams, Games: r.Games, Slate: r.Slate, TargetTeamID: r.TargetTeamID,
		Achievements: []competition.Achievement{r.Achievement},
		Baselines:    map[competition.AchievementID]clinching.AchievementResult{r.Achievement.ID: r.Baseline},
	})
	return values[r.Achievement.ID], err
}

type scenarioMember struct {
	achievement           competition.Achievement
	certified, unresolved assignmentBits
	trackCoverage         bool
	unresolvedAssignments int
	clauses               []Clause
	eliminated            assignmentBits
	eliminationClauses    []Clause
	alreadyEliminated     bool
	trackElimination      bool
	skipOpportunity       bool
	diag                  Diagnostics
}

type batchScenarioSearch struct {
	ctx         context.Context
	evaluator   *clinching.Evaluator
	target      string
	slate       Slate
	slateGames  []standings.Game
	searchGames []standings.Game
	members     []scenarioMember
	points      map[string]int
	// targetSlateRemaining is the best points total the target can have by
	// this slate's cutoff. targetRemaining also includes later fixtures and is
	// used only for the season-ending elimination proof.
	targetSlateRemaining int
	targetRemaining      int
	err                  error
}

// GenerateBatch traverses the slate once and evaluates every active
// achievement at each visited node. Achievement-specific coverage remains
// separate, so its results are identical to independent searches.
func GenerateBatch(ctx context.Context, r BatchRequest) (map[competition.AchievementID]Result, error) {
	started := time.Now()
	if r.Evaluator == nil || r.TargetTeamID == "" || len(r.Achievements) == 0 || len(r.Achievements) > 64 {
		return nil, fmt.Errorf("invalid scenario batch request")
	}
	if err := r.Slate.Validate(); err != nil {
		return nil, err
	}
	byGame := make(map[string]standings.Game, len(r.Games))
	for _, game := range r.Games {
		byGame[game.ID] = game
	}
	slateGames := make([]standings.Game, 0, len(r.Slate.FixtureIDs))
	slateProblem := ""
	if r.Slate.State == SlateReady {
		for _, id := range r.Slate.FixtureIDs {
			game, ok := byGame[id]
			if !ok || game.Status != fixtures.PreMatchStatus {
				slateProblem = "slate fixture is unavailable"
				break
			}
			slateGames = append(slateGames, game)
		}
	}

	results := make(map[competition.AchievementID]Result, len(r.Achievements))
	activeAchievements := []competition.Achievement{}
	members := []scenarioMember{}
	var slateBlocker *clinching.SlateBlocker
	blockerFor := func(achievement competition.Achievement) (bool, error) {
		if slateBlocker == nil {
			var err error
			slateBlocker, err = r.Evaluator.NewSlateBlocker(r.TargetTeamID, r.Slate.FixtureIDs)
			if err != nil {
				return false, err
			}
		}
		return slateBlocker.Blocks(ctx, achievement)
	}
	// The total is a property of the declared ready slate, not whether this
	// request can completely explore it. Keep it on unresolved results as well:
	// cache validation and consumers can then distinguish an incomplete search
	// from one with no possible assignments.
	totalAssignments := pow3(len(r.Slate.FixtureIDs))
	trackCoverage := canTrackCoverage(totalAssignments)
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
		if r.Slate.State == SlateReady {
			out.TotalAssignments = totalAssignments
		}
		switch {
		case baseline.Status == clinching.Clinched:
			out.State, out.AlreadyClinched = OpportunityAlreadyClinched, true
		case baseline.Status == clinching.Unresolved && baseline.Method != clinching.ProofUnprovedScoreTiebreak:
			out.State, out.Limitation = OpportunityUnresolved, limitation(baseline)
		case r.Slate.State == SlateNoUpcoming:
			out.State = OpportunityCannotClinch
		case r.Slate.State != SlateReady:
			out.State, out.Limitation = OpportunityUnresolved, r.Slate.Reason
		case slateProblem != "":
			out.State, out.Limitation = OpportunityUnresolved, slateProblem
		case r.Slate.State == SlateReady:
			blocked, err := blockerFor(achievement)
			if err != nil && !errors.Is(err, clinching.ErrComputeBudget) {
				return nil, err
			}
			if blocked {
				out.State = OpportunityCannotClinch
				// Playoff elimination still uses the slate traversal. The blocker has already
				// settled clinching, so avoid status-oracle work on every leaf.
				if achievement.ID == competition.AchievementPlayoffs {
					members = append(members, newScenarioMember(achievement, totalAssignments, trackCoverage, true, true))
				}
			} else {
				activeAchievements = append(activeAchievements, achievement)
				members = append(members, newScenarioMember(achievement, totalAssignments, trackCoverage, achievement.ID == competition.AchievementPlayoffs, false))
			}
		}
		results[achievement.ID] = out
	}
	if len(members) == 0 {
		setBatchElapsed(results, started)
		return results, nil
	}

	points := map[string]int{}
	for _, game := range r.Games {
		if game.Status == standings.CompletedStatus {
			applyOutcomePoints(points, game, scoreOutcome(*game.HomeScore, *game.AwayScore))
		}
	}
	targetSlateRemaining, targetRemaining := 0, 0
	for _, game := range r.Games {
		if game.Status != fixtures.PreMatchStatus {
			continue
		}
		if game.HomeTeamID == r.TargetTeamID || game.AwayTeamID == r.TargetTeamID {
			targetRemaining++
		}
	}
	for _, game := range slateGames {
		if game.HomeTeamID == r.TargetTeamID || game.AwayTeamID == r.TargetTeamID {
			targetSlateRemaining++
		}
	}
	search := batchScenarioSearch{
		ctx: ctx, evaluator: r.Evaluator, target: r.TargetTeamID, slate: r.Slate,
		slateGames: slateGames, searchGames: orderBatchSearchGames(r.Teams, r.Games, r.TargetTeamID, activeAchievements, slateGames),
		members: members, points: points, targetSlateRemaining: targetSlateRemaining, targetRemaining: targetRemaining,
	}
	active := uint64(1)<<uint(len(members)) - 1
	search.walk(make([]clinching.FixedResult, 0, len(search.searchGames)), 0, active)
	if search.err != nil && ctx.Err() == nil && !errors.Is(search.err, clinching.ErrComputeBudget) {
		return nil, search.err
	}
	for i := range search.members {
		out := results[search.members[i].achievement.ID]
		if search.err != nil {
			out = finishDiscoveredScenario(out, search.members[i])
			out.Limitation = LimitationBudgetPartial
		} else {
			var err error
			out, err = finishScenario(ctx, out, slateGames, search.members[i])
			if err != nil {
				if ctx.Err() == nil {
					return nil, err
				}
				out = finishDiscoveredScenario(out, search.members[i])
				out.Limitation = LimitationBudgetPartial
			}
		}
		results[out.Achievement] = out
	}
	setBatchElapsed(results, started)
	return results, nil
}

// Coverage supports exact clause minimization and exact necessary conditions.
// Searching never depends on it: a large slate still receives proof-directed
// discovery until the caller's scenario budget expires.
const maxCoverageAssignments = 8 * 1024 * 1024 * 8

func canTrackCoverage(assignments int) bool {
	return assignments > 0 && assignments <= maxCoverageAssignments
}

func newScenarioMember(achievement competition.Achievement, assignments int, trackCoverage, trackElimination, skipOpportunity bool) scenarioMember {
	member := scenarioMember{achievement: achievement, trackCoverage: trackCoverage, trackElimination: trackElimination, skipOpportunity: skipOpportunity}
	if trackCoverage {
		member.certified = newAssignmentBits(assignments)
		member.unresolved = newAssignmentBits(assignments)
		member.eliminated = newAssignmentBits(assignments)
	}
	return member
}

func emptyResult(team string, achievement competition.Achievement) Result {
	return Result{TeamID: team, Achievement: achievement.ID, TopK: achievement.TopK, Clauses: []Clause{}, Necessary: []FixtureCondition{}, ProofMethods: []clinching.ProofMethod{}, EliminationClauses: []Clause{}}
}

func setBatchElapsed(results map[competition.AchievementID]Result, started time.Time) {
	elapsed := time.Since(started).Microseconds()
	for id, result := range results {
		result.Diagnostics.ElapsedMicroseconds = elapsed
		results[id] = result
	}
}

func finishScenario(ctx context.Context, out Result, games []standings.Game, member scenarioMember) (Result, error) {
	if !member.trackCoverage {
		return finishUntrackedScenario(out, member), nil
	}
	out.Diagnostics = member.diag
	out.Diagnostics.InitialClauses = len(member.clauses)
	methods := []clinching.ProofMethod{}
	for _, clause := range member.clauses {
		methods = append(methods, clause.ProofMethods...)
	}
	final, err := minimizeCoverage(ctx, games, member.certified, methods, &out.Diagnostics)
	if err != nil {
		return out, err
	}
	coverage := newAssignmentBits(out.TotalAssignments)
	for _, clause := range final {
		markCoverage(coverage, clause.Conditions, games)
	}
	if !member.certified.equal(coverage) {
		return out, fmt.Errorf("minimized clause coverage differs from exact search")
	}
	out.CertifiedAssignments, out.UnresolvedAssignments = coverage.count(), member.unresolved.count()
	out.Diagnostics.MinimalClauses, out.Clauses = len(final), final
	for _, clause := range final {
		out.ProofMethods = append(out.ProofMethods, clause.ProofMethods...)
	}
	out.ProofMethods = sortedMethods(out.ProofMethods)
	if out.CertifiedAssignments > 0 {
		out.State, out.CanClinch = OpportunityCanClinch, true
		if out.UnresolvedAssignments > 0 {
			out.Limitation = "additional paths may depend on score or unavailable tiebreak data; no outcome-only path is published"
		} else {
			out.Necessary = necessary(coverage, games)
		}
	} else if out.UnresolvedAssignments > 0 {
		out.State = OpportunityTiebreakDependent
		out.Limitation = "a clinch may depend on score or unavailable tiebreak data; no outcome-only path is published"
	} else {
		out.State = OpportunityCannotClinch
	}
	if member.trackElimination {
		if member.alreadyEliminated {
			out.AlreadyEliminated = true
			return out, nil
		}
		eliminationMethods := []clinching.ProofMethod{}
		for _, clause := range member.eliminationClauses {
			eliminationMethods = append(eliminationMethods, clause.ProofMethods...)
		}
		finalElimination, err := minimizeCoverage(ctx, games, member.eliminated, eliminationMethods, &out.Diagnostics)
		if err != nil {
			return out, err
		}
		coverage := newAssignmentBits(out.TotalAssignments)
		for _, clause := range finalElimination {
			markCoverage(coverage, clause.Conditions, games)
		}
		if !member.eliminated.equal(coverage) {
			return out, fmt.Errorf("minimized elimination coverage differs from exact search")
		}
		out.EliminationClauses = finalElimination
		out.CanBeEliminated = len(finalElimination) > 0
	}
	return out, nil
}

// finishDiscoveredScenario publishes the clauses already proved by the search
// without claiming that it has exhausted the slate. Clauses emitted by the
// depth-first walk cover disjoint outcome cubes, so their represented counts
// are an exact count of the certified portion discovered so far.
func finishDiscoveredScenario(out Result, member scenarioMember) Result {
	out.Diagnostics = member.diag
	out.Diagnostics.InitialClauses = len(member.clauses)
	out.Diagnostics.MinimalClauses = len(member.clauses)
	out.Clauses = append([]Clause{}, member.clauses...)
	out.CertifiedAssignments = representedClauses(member.clauses)
	out.UnresolvedAssignments = member.unresolvedAssignments
	for _, clause := range out.Clauses {
		out.ProofMethods = append(out.ProofMethods, clause.ProofMethods...)
	}
	out.ProofMethods = sortedMethods(out.ProofMethods)
	if out.CertifiedAssignments > 0 {
		out.State, out.CanClinch = OpportunityCanClinch, true
	} else {
		out.State = OpportunityUnresolved
	}
	if member.trackElimination {
		if member.alreadyEliminated {
			out.AlreadyEliminated = true
			return out
		}
		out.EliminationClauses = append([]Clause{}, member.eliminationClauses...)
		out.CanBeEliminated = len(out.EliminationClauses) > 0
	}
	return out
}

func finishUntrackedScenario(out Result, member scenarioMember) Result {
	out = finishDiscoveredScenario(out, member)
	if out.CertifiedAssignments > 0 {
		out.State, out.CanClinch = OpportunityCanClinch, true
		if out.UnresolvedAssignments > 0 {
			out.Limitation = "additional paths may depend on score or unavailable tiebreak data; no outcome-only path is published"
		}
	} else if out.UnresolvedAssignments > 0 {
		out.State = OpportunityTiebreakDependent
		out.Limitation = "a clinch may depend on score or unavailable tiebreak data; no outcome-only path is published"
	} else {
		out.State = OpportunityCannotClinch
	}
	return out
}

func representedClauses(clauses []Clause) int {
	total := 0
	for _, clause := range clauses {
		total += clause.RepresentedAssignments
	}
	return total
}

func (s *batchScenarioSearch) walk(fixed []clinching.FixedResult, depth int, active uint64) {
	if s.err != nil {
		return
	}
	if err := s.ctx.Err(); err != nil {
		s.err = err
		return
	}
	remaining := uint64(0)
	for memberIndex := range s.members {
		bit := uint64(1) << uint(memberIndex)
		if active&bit == 0 {
			continue
		}
		member := &s.members[memberIndex]
		member.diag.SearchNodes++
		if member.trackElimination && s.eliminationGuaranteed(member.achievement.TopK) {
			if depth == 0 {
				member.alreadyEliminated = true
			} else {
				clause := Clause{Conditions: fixedConditions(fixed, s.slate.FixtureIDs), ProofMethods: []clinching.ProofMethod{clinching.ProofCheapBound}}
				clause.RepresentedAssignments = represented(clause.Conditions, len(s.slateGames))
				member.eliminationClauses = append(member.eliminationClauses, clause)
				if member.trackCoverage {
					markCoverage(member.eliminated, clause.Conditions, s.slateGames)
				}
			}
			member.diag.OpportunityPrunes++
			continue
		}
		if member.skipOpportunity {
			remaining |= bit
			continue
		}
		if s.opportunityImpossible(member.achievement.TopK) {
			member.diag.OpportunityPrunes++
			continue
		}
		var value clinching.AchievementResult
		var err error
		if depth < len(s.searchGames) {
			var conclusive bool
			value, conclusive, err = s.evaluator.EvaluateCheapClinched(s.target, member.achievement, fixed)
			member.diag.OracleCalls++
			if err == nil && (!conclusive || !publishable(value)) {
				remaining |= bit
				continue
			}
		} else {
			value, err = s.evaluator.EvaluateStatusSummary(s.ctx, s.target, member.achievement, fixed)
			member.diag.OracleCalls++
			member.diag.VisitedComplete++
			if value.Diagnostics.MemoHits > 0 {
				member.diag.OracleCacheHits++
			}
			if err == nil && !publishable(value) {
				if value.Status == clinching.Unresolved {
					if member.trackCoverage {
						member.unresolved.set(assignmentIndex(fixed, s.slate.FixtureIDs))
					} else {
						member.unresolvedAssignments++
					}
				}
				continue
			}
		}
		if err != nil {
			s.err = err
			return
		}
		clause := Clause{Conditions: fixedConditions(fixed, s.slate.FixtureIDs), ProofMethods: []clinching.ProofMethod{value.Method}}
		clause.RepresentedAssignments = represented(clause.Conditions, len(s.slateGames))
		member.clauses = append(member.clauses, clause)
		if member.trackCoverage {
			markCoverage(member.certified, clause.Conditions, s.slateGames)
		}
		member.diag.GuaranteePrunes++
	}
	if depth == len(s.searchGames) || remaining == 0 {
		return
	}
	g := s.searchGames[depth]
	for _, o := range []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin} {
		s.push(g, o)
		fixed = append(fixed, clinching.FixedResult{GameID: g.ID, Outcome: o})
		s.walk(fixed, depth+1, remaining)
		fixed = fixed[:len(fixed)-1]
		s.pop(g, o)
	}
}

func (s *batchScenarioSearch) push(game standings.Game, outcome clinching.Outcome) {
	applyOutcomePoints(s.points, game, outcome)
	if game.HomeTeamID == s.target || game.AwayTeamID == s.target {
		s.targetSlateRemaining--
		s.targetRemaining--
	}
}

func (s *batchScenarioSearch) pop(game standings.Game, outcome clinching.Outcome) {
	removeOutcomePoints(s.points, game, outcome)
	if game.HomeTeamID == s.target || game.AwayTeamID == s.target {
		s.targetSlateRemaining++
		s.targetRemaining++
	}
}

func (s *batchScenarioSearch) eliminationGuaranteed(topK int) bool {
	ceiling := s.points[s.target] + 3*s.targetRemaining
	ahead := 0
	for team, points := range s.points {
		if team != s.target && points > ceiling {
			ahead++
		}
	}
	return ahead >= topK
}

func (s *batchScenarioSearch) opportunityImpossible(topK int) bool {
	ceiling := s.points[s.target] + 3*s.targetSlateRemaining
	ahead := 0
	for team, points := range s.points {
		if team != s.target && points > ceiling {
			ahead++
		}
	}
	return ahead >= topK
}

func orderBatchSearchGames(teams []standings.Team, season []standings.Game, target string, achievements []competition.Achievement, games []standings.Game) []standings.Game {
	ordered := append([]standings.Game(nil), games...)
	table := standings.Calculate(teams, season, standings.OfficialTotalRules())
	position, points := map[string]int{}, map[string]int{}
	for i, row := range table {
		position[row.Team.ID] = i + 1
		points[row.Team.ID] = row.Record.Points
	}
	original := map[string]int{}
	for i, game := range games {
		original[game.ID] = i
	}
	cutoffDistance := func(team string) int {
		best := len(teams)
		for _, achievement := range achievements {
			value := absInt(position[team] - achievement.TopK)
			if value < best {
				best = value
			}
		}
		return best
	}
	rank := func(game standings.Game) (int, int, int) {
		targetRank := 1
		if game.HomeTeamID == target || game.AwayTeamID == target {
			targetRank = 0
		}
		cutoff := min(cutoffDistance(game.HomeTeamID), cutoffDistance(game.AwayTeamID))
		distance := min(absInt(points[game.HomeTeamID]-points[target]), absInt(points[game.AwayTeamID]-points[target]))
		return targetRank, cutoff, distance
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		it, ic, id := rank(ordered[i])
		jt, jc, jd := rank(ordered[j])
		if it != jt {
			return it < jt
		}
		if ic != jc {
			return ic < jc
		}
		if id != jd {
			return id < jd
		}
		return original[ordered[i].ID] < original[ordered[j].ID]
	})
	return ordered
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
func publishable(v clinching.AchievementResult) bool {
	return v.Status == clinching.Clinched && (v.Method == clinching.ProofCheapBound || v.Method == clinching.ProofPointsOptimization)
}
func limitation(v clinching.AchievementResult) string {
	if v.Reason != "" {
		return v.Reason
	}
	return string(v.Method)
}
func pow3(n int) int {
	x := 1
	for range n {
		x *= 3
	}
	return x
}
func fixedConditions(fixed []clinching.FixedResult, order []string) []FixtureCondition {
	by := map[string]clinching.Outcome{}
	for _, v := range fixed {
		by[v.GameID] = v.Outcome
	}
	out := []FixtureCondition{}
	for _, id := range order {
		if v, ok := by[id]; ok {
			out = append(out, FixtureCondition{GameID: id, AllowedOutcomes: []clinching.Outcome{v}})
		}
	}
	return out
}
func represented(c []FixtureCondition, n int) int {
	x := pow3(n - len(c))
	for _, f := range c {
		x *= len(f.AllowedOutcomes)
	}
	return x
}
func assignmentIndex(fixed []clinching.FixedResult, order []string) int {
	by := map[string]clinching.Outcome{}
	for _, f := range fixed {
		by[f.GameID] = f.Outcome
	}
	x := 0
	for _, id := range order {
		x *= 3
		switch by[id] {
		case clinching.Draw:
			x += 1
		case clinching.AwayWin:
			x += 2
		}
	}
	return x
}
func markCoverage(bits assignmentBits, c []FixtureCondition, games []standings.Game) {
	by := map[string]uint8{}
	for _, f := range c {
		var mask uint8
		for _, o := range f.AllowedOutcomes {
			mask |= outcomeMask(o)
		}
		by[f.GameID] = mask
	}
	var walk func(int, int)
	walk = func(fixture, index int) {
		if fixture == len(games) {
			bits.set(index)
			return
		}
		mask, ok := by[games[fixture].ID]
		if !ok {
			mask = allMask
		}
		for outcome, bit := range []uint8{homeMask, drawMask, awayMask} {
			if mask&bit != 0 {
				walk(fixture+1, index*3+outcome)
			}
		}
	}
	walk(0, 0)
}
func outcomeMask(outcome clinching.Outcome) uint8 {
	switch outcome {
	case clinching.HomeWin:
		return homeMask
	case clinching.Draw:
		return drawMask
	case clinching.AwayWin:
		return awayMask
	}
	return 0
}
func necessary(bits assignmentBits, games []standings.Game) []FixtureCondition {
	seen := make([]map[clinching.Outcome]bool, len(games))
	for i := range seen {
		seen[i] = map[clinching.Outcome]bool{}
	}
	bits.each(func(i int) {
		v := i
		for j := len(games) - 1; j >= 0; j-- {
			z := v % 3
			v /= 3
			seen[j][[]clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin}[z]] = true
		}
	})
	out := []FixtureCondition{}
	for i, g := range games {
		os := []clinching.Outcome{}
		for _, o := range []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin} {
			if seen[i][o] {
				os = append(os, o)
			}
		}
		if len(os) > 0 && len(os) < 3 {
			out = append(out, FixtureCondition{GameID: g.ID, AllowedOutcomes: os})
		}
	}
	return out
}

func removeOutcomePoints(points map[string]int, game standings.Game, outcome clinching.Outcome) {
	switch outcome {
	case clinching.HomeWin:
		points[game.HomeTeamID] -= 3
	case clinching.AwayWin:
		points[game.AwayTeamID] -= 3
	case clinching.Draw:
		points[game.HomeTeamID]--
		points[game.AwayTeamID]--
	}
}

func scoreOutcome(h, a int) clinching.Outcome {
	if h > a {
		return clinching.HomeWin
	}
	if a > h {
		return clinching.AwayWin
	}
	return clinching.Draw
}
func applyOutcomePoints(points map[string]int, g standings.Game, o clinching.Outcome) {
	switch o {
	case clinching.HomeWin:
		points[g.HomeTeamID] += 3
	case clinching.AwayWin:
		points[g.AwayTeamID] += 3
	case clinching.Draw:
		points[g.HomeTeamID]++
		points[g.AwayTeamID]++
	}
}
