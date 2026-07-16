package clinching

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

type preparedSeason struct {
	target   string
	frontier int
	points   map[string]int
	decision []standings.Game
	witness  map[string]WitnessGame
	capable  int
}

func prepare(r Request) (preparedSeason, error) {
	p := preparedSeason{target: r.TargetTeamID, points: map[string]int{}, witness: map[string]WitnessGame{}}
	for _, t := range r.Teams {
		p.points[t.ID] = 0
	}
	fixed := map[string]Outcome{}
	for _, f := range r.Fixed {
		fixed[f.GameID] = f.Outcome
	}
	for _, g := range r.Games {
		if g.Status == standings.CompletedStatus {
			applyPoints(p.points, g, *g.HomeScore, *g.AwayScore)
			continue
		}
		if o, ok := fixed[g.ID]; ok {
			p.witness[g.ID] = witnessFrom(g, o)
			applyOutcome(p.points, g, o)
			continue
		}
		if g.HomeTeamID == p.target || g.AwayTeamID == p.target {
			o := HomeWin
			if g.HomeTeamID == p.target {
				o = AwayWin
			}
			p.witness[g.ID] = witnessFrom(g, o)
			applyOutcome(p.points, g, o)
			continue
		}
		p.decision = append(p.decision, g)
	}
	p.frontier = p.points[p.target]
	for id, points := range p.points {
		if id == p.target {
			continue
		}
		n := 0
		for _, g := range p.decision {
			if g.HomeTeamID == id || g.AwayTeamID == id {
				n++
			}
		}
		if points+3*n >= p.frontier {
			p.capable++
		}
	}
	return p, nil
}
func applyPoints(points map[string]int, g standings.Game, h, a int) {
	if h > a {
		points[g.HomeTeamID] += 3
	} else if a > h {
		points[g.AwayTeamID] += 3
	} else {
		points[g.HomeTeamID]++
		points[g.AwayTeamID]++
	}
}
func applyOutcome(points map[string]int, g standings.Game, o Outcome) {
	switch o {
	case HomeWin:
		points[g.HomeTeamID] += 3
	case AwayWin:
		points[g.AwayTeamID] += 3
	case Draw:
		points[g.HomeTeamID]++
		points[g.AwayTeamID]++
	}
}
func witnessFrom(g standings.Game, o Outcome) WitnessGame {
	w := WitnessGame{GameID: g.ID, HomeTeamID: g.HomeTeamID, AwayTeamID: g.AwayTeamID, Outcome: o}
	switch o {
	case HomeWin:
		w.HomeScore = 1
	case AwayWin:
		w.AwayScore = 1
	}
	return w
}
func completeWitness(p preparedSeason, assigned map[string]Outcome) []WitnessGame {
	values := map[string]WitnessGame{}
	for id, w := range p.witness {
		values[id] = w
	}
	for _, g := range p.decision {
		o := Draw
		if assigned != nil {
			if x, ok := assigned[g.ID]; ok {
				o = x
			}
		}
		values[g.ID] = witnessFrom(g, o)
	}
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]WitnessGame, 0, len(ids))
	for _, id := range ids {
		out = append(out, values[id])
	}
	return out
}

type queryResult struct {
	count    int
	score    int
	outcomes map[string]Outcome
	diag     Diagnostics
}

// feasibleThresholdWitness quickly constructs one complete set of remaining
// results. Unlike solveThreshold it does not attempt to prove the maximum
// number of teams that can reach threshold. It is therefore safe only as a
// not-clinched witness: when it finds TopK teams at or above threshold, that
// one feasible completion is enough to disprove a clinch.
func feasibleThresholdWitness(p preparedSeason, threshold int) queryResult {
	return feasibleThresholdWitnessAtLeast(p, threshold, len(p.points)-1)
}

// feasibleThresholdWitnessAtLeast is the early-exit variant used by the
// status oracle. A qualification proof needs only TopK blocking opponents, so
// continuing to improve an already-sufficient witness wastes work at every
// scenario-tree node.
func feasibleThresholdWitnessAtLeast(p preparedSeason, threshold, atLeast int) queryResult {
	if len(p.decision) == 0 {
		return countThresholdWitness(p, threshold, map[string]Outcome{})
	}

	teams := make([]string, 0, len(p.points)-1)
	for id := range p.points {
		if id != p.target {
			teams = append(teams, id)
		}
	}
	sort.Strings(teams)

	// The all-draw, all-home-win, and all-away-win starts cover the obvious
	// distributions. The focused starts bias every fixture involving one team
	// toward that team and are useful when several direct rivals share fixtures.
	seeds := make([]map[string]Outcome, 0, len(teams)+3)
	for _, defaultOutcome := range []Outcome{Draw, HomeWin, AwayWin} {
		seed := map[string]Outcome{}
		for _, game := range p.decision {
			seed[game.ID] = defaultOutcome
		}
		seeds = append(seeds, seed)
	}
	for _, focus := range teams {
		seed := map[string]Outcome{}
		for _, game := range p.decision {
			switch {
			case game.HomeTeamID == focus:
				seed[game.ID] = HomeWin
			case game.AwayTeamID == focus:
				seed[game.ID] = AwayWin
			default:
				seed[game.ID] = Draw
			}
		}
		seeds = append(seeds, seed)
	}

	best := queryResult{count: -1, outcomes: map[string]Outcome{}}
	for _, seed := range seeds {
		candidate := improveThresholdWitness(p, threshold, seed)
		if candidate.count >= atLeast {
			return candidate
		}
		if betterThresholdWitness(candidate, best) {
			best = candidate
		}
	}
	return best
}

func improveThresholdWitness(p preparedSeason, threshold int, outcomes map[string]Outcome) queryResult {
	current := cloneOutcomes(outcomes)
	points := make(map[string]int, len(p.points))
	for id, value := range p.points {
		points[id] = value
	}
	for _, game := range p.decision {
		applyOutcome(points, game, current[game.ID])
	}
	count, score := thresholdScore(points, p.target, threshold)
	// One deterministic coordinate-ascent pass is enough for this fast-path:
	// it needs only one valid counterexample, not the optimal assignment. More
	// passes turn a shortcut used at every scenario-tree node into a second
	// expensive solver; the exact solver below remains the fallback.
	for pass := 0; pass < 1; pass++ {
		changed := false
		for _, game := range p.decision {
			bestOutcome := current[game.ID]
			bestCount, bestScore := count, score
			for _, outcome := range []Outcome{HomeWin, Draw, AwayWin} {
				if outcome == current[game.ID] {
					continue
				}
				candidateCount, candidateScore := trialThresholdScore(points, game, current[game.ID], outcome, p.target, threshold, count, score)
				if candidateCount > bestCount || candidateCount == bestCount && candidateScore > bestScore {
					bestOutcome, bestCount, bestScore = outcome, candidateCount, candidateScore
				}
			}
			if bestOutcome != current[game.ID] {
				applyOutcomeChange(points, game, current[game.ID], bestOutcome)
				current[game.ID] = bestOutcome
				count, score = bestCount, bestScore
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return queryResult{count: count, score: score, outcomes: current}
}

func trialThresholdScore(points map[string]int, game standings.Game, from, to Outcome, target string, threshold, count, score int) (int, int) {
	if game.HomeTeamID != target {
		wasCount, wasScore := thresholdContribution(points[game.HomeTeamID], threshold)
		points[game.HomeTeamID] -= outcomePoints(from, true)
		points[game.HomeTeamID] += outcomePoints(to, true)
		nowCount, nowScore := thresholdContribution(points[game.HomeTeamID], threshold)
		count += nowCount - wasCount
		score += nowScore - wasScore
	}
	if game.AwayTeamID != target {
		wasCount, wasScore := thresholdContribution(points[game.AwayTeamID], threshold)
		points[game.AwayTeamID] -= outcomePoints(from, false)
		points[game.AwayTeamID] += outcomePoints(to, false)
		nowCount, nowScore := thresholdContribution(points[game.AwayTeamID], threshold)
		count += nowCount - wasCount
		score += nowScore - wasScore
	}
	// Restore the caller's point vector. This keeps each alternative probe
	// allocation-free and limits rescoring to the two affected teams.
	if game.HomeTeamID != target {
		points[game.HomeTeamID] -= outcomePoints(to, true)
		points[game.HomeTeamID] += outcomePoints(from, true)
	}
	if game.AwayTeamID != target {
		points[game.AwayTeamID] -= outcomePoints(to, false)
		points[game.AwayTeamID] += outcomePoints(from, false)
	}
	return count, score
}

func applyOutcomeChange(points map[string]int, game standings.Game, from, to Outcome) {
	points[game.HomeTeamID] -= outcomePoints(from, true)
	points[game.AwayTeamID] -= outcomePoints(from, false)
	points[game.HomeTeamID] += outcomePoints(to, true)
	points[game.AwayTeamID] += outcomePoints(to, false)
}

func outcomePoints(outcome Outcome, home bool) int {
	switch outcome {
	case HomeWin:
		if home {
			return 3
		}
	case AwayWin:
		if !home {
			return 3
		}
	case Draw:
		return 1
	}
	return 0
}

func countThresholdWitness(p preparedSeason, threshold int, outcomes map[string]Outcome) queryResult {
	points := make(map[string]int, len(p.points))
	for id, value := range p.points {
		points[id] = value
	}
	for _, game := range p.decision {
		applyOutcome(points, game, outcomes[game.ID])
	}
	count, progress := thresholdScore(points, p.target, threshold)
	return queryResult{count: count, score: progress, outcomes: cloneOutcomes(outcomes)}
}

func thresholdScore(points map[string]int, target string, threshold int) (count, progress int) {
	for id, value := range points {
		if id == target {
			continue
		}
		teamCount, teamScore := thresholdContribution(value, threshold)
		count += teamCount
		progress += teamScore
	}
	return count, progress
}

func thresholdContribution(points, threshold int) (count, score int) {
	if points >= threshold {
		count = 1
	}
	if points > threshold {
		points = threshold
	}
	return count, points
}

func betterThresholdWitness(left, right queryResult) bool {
	if left.count != right.count {
		return left.count > right.count
	}
	// Prefer spreading points toward the threshold when counts tie.
	return left.score > right.score
}

func cloneOutcomes(values map[string]Outcome) map[string]Outcome {
	copy := make(map[string]Outcome, len(values))
	for id, outcome := range values {
		copy[id] = outcome
	}
	return copy
}

func solveThreshold(ctx context.Context, p preparedSeason, threshold int) (queryResult, error) {
	q := queryResult{outcomes: map[string]Outcome{}}
	// First reduce fixtures against teams unable to reach this query's threshold.
	contender := map[string]bool{}
	for id, v := range p.points {
		if id == p.target {
			continue
		}
		n := 0
		for _, g := range p.decision {
			if g.HomeTeamID == id || g.AwayTeamID == id {
				n++
			}
		}
		if v+3*n >= threshold {
			contender[id] = true
		}
	}
	q.diag.ReducedTeams = len(contender)
	points := map[string]int{}
	for id, v := range p.points {
		points[id] = v
	}
	edges := []standings.Game{}
	for _, g := range p.decision {
		a, b := contender[g.HomeTeamID], contender[g.AwayTeamID]
		switch {
		case a && b:
			edges = append(edges, g)
		case a:
			applyOutcome(points, g, HomeWin)
			q.outcomes[g.ID] = HomeWin
		case b:
			applyOutcome(points, g, AwayWin)
			q.outcomes[g.ID] = AwayWin
		default:
			applyOutcome(points, g, Draw)
			q.outcomes[g.ID] = Draw
		}
	}
	q.diag.ReducedFixtures = len(edges)
	// Components only need contender-to-contender fixtures. Non-contender points cannot matter.
	adj := map[string][]string{}
	for id := range contender {
		adj[id] = nil
	}
	for _, g := range edges {
		adj[g.HomeTeamID] = append(adj[g.HomeTeamID], g.AwayTeamID)
		adj[g.AwayTeamID] = append(adj[g.AwayTeamID], g.HomeTeamID)
	}
	seen := map[string]bool{}
	fixedCount := 0
	for id := range contender {
		if len(adj[id]) == 0 {
			if points[id] >= threshold {
				fixedCount++
			}
			seen[id] = true
		}
	}
	for start := range contender {
		if seen[start] {
			continue
		}
		stack := []string{start}
		seen[start] = true
		ids := []string{}
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			ids = append(ids, x)
			for _, y := range adj[x] {
				if !seen[y] {
					seen[y] = true
					stack = append(stack, y)
				}
			}
		}
		sort.Strings(ids)
		componentGames := []standings.Game{}
		member := map[string]bool{}
		for _, id := range ids {
			member[id] = true
		}
		for _, g := range edges {
			if member[g.HomeTeamID] && member[g.AwayTeamID] {
				componentGames = append(componentGames, g)
			}
		}
		q.diag.ConnectedComponents++
		value, err := solveComponent(ctx, ids, componentGames, points, threshold)
		q.diag.VisitedStates += value.diag.VisitedStates
		q.diag.MemoHits += value.diag.MemoHits
		if err != nil {
			return q, err
		}
		fixedCount += value.count
		for id, o := range value.outcomes {
			q.outcomes[id] = o
		}
	}
	q.count = fixedCount
	return q, nil
}

type componentSolution struct {
	count    int
	outcomes map[string]Outcome
	diag     Diagnostics
}

func solveComponent(ctx context.Context, ids []string, games []standings.Game, initial map[string]int, threshold int) (componentSolution, error) {
	// Exact dynamic programming: future point totals are clipped at the threshold.
	index := map[string]int{}
	for i, id := range ids {
		index[id] = i
	}
	sort.Slice(games, func(i, j int) bool { return games[i].ID < games[j].ID })
	type state struct {
		i      int
		values string
	}
	memo := map[state]componentSolution{}
	diag := Diagnostics{}
	var walk func(int, []int) (componentSolution, error)
	walk = func(i int, vals []int) (componentSolution, error) {
		if diag.VisitedStates%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return componentSolution{diag: diag}, fmt.Errorf("%w: %v", ErrComputeBudget, err)
			}
		}
		diag.VisitedStates++
		var b strings.Builder
		for _, v := range vals {
			if v > threshold {
				v = threshold
			}
			fmt.Fprintf(&b, "%d,", v)
		}
		key := state{i, b.String()}
		if v, ok := memo[key]; ok {
			diag.MemoHits++
			return cloneSolution(v), nil
		}
		if i == len(games) {
			n := 0
			for _, v := range vals {
				if v >= threshold {
					n++
				}
			}
			return componentSolution{count: n, outcomes: map[string]Outcome{}}, nil
		}
		g := games[i]
		hi, ai := index[g.HomeTeamID], index[g.AwayTeamID]
		best := componentSolution{count: -1, outcomes: map[string]Outcome{}}
		for _, o := range []Outcome{HomeWin, AwayWin, Draw} {
			next := append([]int(nil), vals...)
			switch o {
			case HomeWin:
				next[hi] += 3
			case AwayWin:
				next[ai] += 3
			case Draw:
				next[hi]++
				next[ai]++
			}
			child, err := walk(i+1, next)
			if err != nil {
				return componentSolution{}, err
			}
			if child.count > best.count {
				child.outcomes[g.ID] = o
				best = child
			}
		}
		memo[key] = cloneSolution(best)
		return best, nil
	}
	vals := make([]int, len(ids))
	for i, id := range ids {
		vals[i] = initial[id]
	}
	answer, err := walk(0, vals)
	answer.diag = diag
	return answer, err
}
func cloneSolution(v componentSolution) componentSolution {
	o := map[string]Outcome{}
	for id, x := range v.outcomes {
		o[id] = x
	}
	v.outcomes = o
	return v
}
