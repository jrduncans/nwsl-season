package scenarios

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type Request struct {
	Evaluator        *clinching.Evaluator
	Teams            []standings.Team
	Games            []standings.Game
	Slate            Slate
	TargetTeamID     string
	Achievement      competition.Achievement
	Baseline         clinching.AchievementResult
	MaxSlateFixtures int
}

// Generate searches all three-outcome completions, pruning only statements
// which the Phase 12 points oracle has already certified.
func Generate(ctx context.Context, r Request) (Result, error) {
	started := time.Now()
	out := Result{TeamID: r.TargetTeamID, Achievement: r.Achievement.ID, TopK: r.Achievement.TopK, Clauses: []Clause{}, Necessary: []FixtureCondition{}, ProofMethods: []clinching.ProofMethod{}}
	defer func() { out.Diagnostics.ElapsedMicroseconds = time.Since(started).Microseconds() }()
	if r.Evaluator == nil || r.TargetTeamID == "" || r.Achievement.ID == "" || r.Achievement.TopK < 1 {
		return out, fmt.Errorf("invalid scenario request")
	}
	if err := r.Slate.Validate(); err != nil {
		return out, err
	}
	if r.Baseline.TeamID != r.TargetTeamID || r.Baseline.Achievement != r.Achievement.ID || r.Baseline.TopK != r.Achievement.TopK {
		return out, fmt.Errorf("scenario baseline does not match request")
	}
	if r.Baseline.Status == clinching.Clinched {
		out.State = OpportunityAlreadyClinched
		out.AlreadyClinched = true
		return out, nil
	}
	if r.Baseline.Status == clinching.Unresolved {
		out.State = OpportunityUnresolved
		out.Limitation = limitation(r.Baseline)
		return out, nil
	}
	if r.Slate.State == SlateNoUpcoming {
		out.State = OpportunityCannotClinch
		return out, nil
	}
	if r.Slate.State != SlateReady {
		out.State = OpportunityUnresolved
		out.Limitation = r.Slate.Reason
		return out, nil
	}
	if r.MaxSlateFixtures <= 0 {
		r.MaxSlateFixtures = 10
	}
	if len(r.Slate.FixtureIDs) > r.MaxSlateFixtures {
		out.State = OpportunityUnresolved
		out.Limitation = fmt.Sprintf("slate has %d fixtures; maximum is %d", len(r.Slate.FixtureIDs), r.MaxSlateFixtures)
		return out, nil
	}
	games := map[string]standings.Game{}
	for _, g := range r.Games {
		games[g.ID] = g
	}
	slateGames := make([]standings.Game, 0, len(r.Slate.FixtureIDs))
	for _, id := range r.Slate.FixtureIDs {
		g, ok := games[id]
		if !ok || g.Status != "PreMatch" {
			out.State = OpportunityUnresolved
			out.Limitation = "slate fixture is unavailable"
			return out, nil
		}
		slateGames = append(slateGames, g)
	}
	out.TotalAssignments = pow3(len(slateGames))
	tree := scenarioSearch{ctx: ctx, request: r, games: games, slateGames: slateGames, certified: make([]bool, out.TotalAssignments), unresolved: make([]bool, out.TotalAssignments)}
	tree.walk(nil, 0)
	out.Diagnostics = tree.diag
	if tree.err != nil {
		if ctx.Err() != nil {
			out.State = OpportunityUnresolved
			out.Limitation = "scenario computation budget exhausted"
			return out, nil
		}
		return out, tree.err
	}
	initial := tree.clauses
	out.Diagnostics.InitialClauses = len(initial)
	final, err := minimize(ctx, r, slateGames, initial, &out.Diagnostics)
	if err != nil {
		if ctx.Err() != nil {
			out.State = OpportunityUnresolved
			out.Limitation = "scenario computation budget exhausted"
			out.Clauses = []Clause{}
			return out, nil
		}
		return out, err
	}
	finalCoverage := make([]bool, out.TotalAssignments)
	for _, cl := range final {
		markCoverage(finalCoverage, cl.Conditions, slateGames)
	}
	for i := range tree.certified {
		if tree.certified[i] != finalCoverage[i] {
			return out, fmt.Errorf("minimized clause coverage differs from exact search")
		}
	}
	out.CertifiedAssignments = countBits(finalCoverage)
	out.UnresolvedAssignments = countBits(tree.unresolved)
	out.Diagnostics.MinimalClauses = len(final)
	out.Clauses = final
	for _, c := range final {
		out.ProofMethods = append(out.ProofMethods, c.ProofMethods...)
	}
	out.ProofMethods = sortedMethods(out.ProofMethods)
	if out.CertifiedAssignments > 0 {
		out.State = OpportunityCanClinch
		out.CanClinch = true
		if out.UnresolvedAssignments > 0 {
			out.Limitation = "additional paths may depend on score or unavailable tiebreak data; no outcome-only path is published"
		} else {
			out.Necessary = necessary(finalCoverage, slateGames)
		}
		return out, nil
	}
	if out.UnresolvedAssignments > 0 {
		out.State = OpportunityTiebreakDependent
		out.Limitation = "a clinch may depend on score or unavailable tiebreak data; no outcome-only path is published"
	} else {
		out.State = OpportunityCannotClinch
	}
	return out, nil
}

type scenarioSearch struct {
	ctx                   context.Context
	request               Request
	games                 map[string]standings.Game
	slateGames            []standings.Game
	certified, unresolved []bool
	clauses               []Clause
	diag                  Diagnostics
	err                   error
}

func (s *scenarioSearch) walk(fixed []clinching.FixedResult, depth int) {
	if s.err != nil {
		return
	}
	if err := s.ctx.Err(); err != nil {
		s.err = err
		return
	}
	s.diag.SearchNodes++
	if opportunityImpossible(s.request.TargetTeamID, s.request.Achievement.TopK, s.request.Games, s.slateGames, fixed) {
		s.diag.OpportunityPrunes++
		return
	}
	v, err := s.request.Evaluator.EvaluateStatus(s.ctx, s.request.TargetTeamID, s.request.Achievement, fixed)
	s.diag.OracleCalls++
	if err != nil {
		s.err = err
		return
	}
	if v.Diagnostics.MemoHits > 0 {
		s.diag.OracleCacheHits++
	}
	if publishable(v) {
		c := Clause{Conditions: fixedConditions(fixed, s.request.Slate.FixtureIDs), ProofMethods: []clinching.ProofMethod{v.Method}}
		c.RepresentedAssignments = represented(c.Conditions, len(s.slateGames))
		s.clauses = append(s.clauses, c)
		markCoverage(s.certified, c.Conditions, s.slateGames)
		s.diag.GuaranteePrunes++
		return
	}
	if depth == len(s.slateGames) {
		s.diag.VisitedComplete++
		idx := assignmentIndex(fixed, s.request.Slate.FixtureIDs)
		if v.Status == clinching.Unresolved {
			s.unresolved[idx] = true
		}
		return
	}
	g := s.slateGames[depth]
	for _, o := range []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin} {
		next := append(append([]clinching.FixedResult(nil), fixed...), clinching.FixedResult{GameID: g.ID, Outcome: o})
		s.walk(next, depth+1)
	}
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
func markCoverage(bits []bool, c []FixtureCondition, games []standings.Game) {
	by := map[string]map[clinching.Outcome]bool{}
	for _, f := range c {
		m := map[clinching.Outcome]bool{}
		for _, o := range f.AllowedOutcomes {
			m[o] = true
		}
		by[f.GameID] = m
	}
	for i := range bits {
		v := i
		good := true
		out := make([]clinching.Outcome, len(games))
		for j := len(games) - 1; j >= 0; j-- {
			z := v % 3
			v /= 3
			out[j] = []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin}[z]
		}
		for j, g := range games {
			if allowed, ok := by[g.ID]; ok && !allowed[out[j]] {
				good = false
				break
			}
		}
		if good {
			bits[i] = true
		}
	}
}
func countBits(v []bool) int {
	n := 0
	for _, x := range v {
		if x {
			n++
		}
	}
	return n
}
func necessary(bits []bool, games []standings.Game) []FixtureCondition {
	seen := make([]map[clinching.Outcome]bool, len(games))
	for i := range seen {
		seen[i] = map[clinching.Outcome]bool{}
	}
	for i, ok := range bits {
		if !ok {
			continue
		}
		v := i
		for j := len(games) - 1; j >= 0; j-- {
			z := v % 3
			v /= 3
			seen[j][[]clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin}[z]] = true
		}
	}
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

func opportunityImpossible(target string, topK int, games, slate []standings.Game, fixed []clinching.FixedResult) bool {
	points := map[string]int{}
	assigned := map[string]clinching.Outcome{}
	for _, f := range fixed {
		assigned[f.GameID] = f.Outcome
	}
	for _, g := range games {
		if g.Status == standings.CompletedStatus {
			applyOutcomePoints(points, g, scoreOutcome(*g.HomeScore, *g.AwayScore))
			continue
		}
		if o, ok := assigned[g.ID]; ok {
			applyOutcomePoints(points, g, o)
		}
	}
	ceiling := points[target]
	for _, g := range slate {
		if _, ok := assigned[g.ID]; !ok && (g.HomeTeamID == target || g.AwayTeamID == target) {
			ceiling += 3
		}
	}
	n := 0
	for id, p := range points {
		if id != target && p > ceiling {
			n++
		}
	}
	return n >= topK
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

func sortConditions(c []FixtureCondition, order []string) {
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	sort.Slice(c, func(i, j int) bool { return pos[c[i].GameID] < pos[c[j].GameID] })
}
