package clinching

import (
	"context"
	"fmt"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// cutoffResult answers the only optimization question qualification needs:
// whether at least K opponents can reach a points threshold. It deliberately
// does not calculate the unused exact maximum.
type cutoffResult struct {
	feasible bool
	outcomes map[string]Outcome
	diag     Diagnostics
}

// solveCutoff searches candidate K-team sets. For a fixed candidate set, every
// candidate can safely be awarded a win against a non-candidate; only fixtures
// between two candidates remain coupled decisions.
func solveCutoff(ctx context.Context, p preparedSeason, threshold, k int) (cutoffResult, error) {
	result := cutoffResult{outcomes: map[string]Outcome{}}
	if k <= 0 {
		result.feasible = true
		return result, nil
	}

	incident := make(map[string]int, len(p.points))
	for _, game := range p.decision {
		incident[game.HomeTeamID]++
		incident[game.AwayTeamID]++
	}
	already := make([]string, 0, k)
	candidates := make([]string, 0, len(p.points))
	for id, points := range p.points {
		if id == p.target {
			continue
		}
		if points >= threshold {
			already = append(already, id)
			continue
		}
		if points+3*incident[id] >= threshold {
			candidates = append(candidates, id)
		}
	}
	result.diag.ReducedTeams = len(already) + len(candidates)
	if len(already) >= k {
		result.feasible = true
		for _, game := range p.decision {
			result.outcomes[game.ID] = Draw
		}
		if err := verifyCutoffWitness(p, threshold, k, result.outcomes); err != nil {
			return result, err
		}
		return result, nil
	}
	need := k - len(already)
	if len(candidates) < need {
		return result, nil
	}

	// Try the teams with the greatest individual slack first. This finds the
	// overwhelmingly common not-clinched witness without exploring other sets.
	sort.Slice(candidates, func(i, j int) bool {
		left := p.points[candidates[i]] + 3*incident[candidates[i]] - threshold
		right := p.points[candidates[j]] + 3*incident[candidates[j]] - threshold
		if left != right {
			return left > right
		}
		return candidates[i] < candidates[j]
	})
	sort.Strings(already)

	chosen := append([]string(nil), already...)
	var choose func(int, int) (bool, error)
	choose = func(start, remaining int) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("%w: %v", ErrComputeBudget, err)
		}
		if remaining == 0 {
			result.diag.SubsetProbes++
			value, err := solveCandidateSet(ctx, p, threshold, chosen)
			result.diag.VisitedStates += value.diag.VisitedStates
			result.diag.MemoHits += value.diag.MemoHits
			result.diag.IndividualPrunes += value.diag.IndividualPrunes
			result.diag.ComponentPrunes += value.diag.ComponentPrunes
			result.diag.TotalPrunes += value.diag.TotalPrunes
			result.diag.ReducedFixtures = max(result.diag.ReducedFixtures, value.diag.ReducedFixtures)
			result.diag.ConnectedComponents = max(result.diag.ConnectedComponents, value.diag.ConnectedComponents)
			if err != nil {
				return false, err
			}
			if value.feasible {
				if err := verifyCutoffWitness(p, threshold, k, value.outcomes); err != nil {
					return false, err
				}
				result.feasible = true
				result.outcomes = value.outcomes
				return true, nil
			}
			return false, nil
		}
		if len(candidates)-start < remaining {
			return false, nil
		}
		for i := start; i <= len(candidates)-remaining; i++ {
			chosen = append(chosen, candidates[i])
			ok, err := choose(i+1, remaining-1)
			chosen = chosen[:len(chosen)-1]
			if err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	}
	_, err := choose(0, need)
	return result, err
}

// verifyCutoffWitness is intentionally independent of the deficit search. A
// solver bug must never turn an incomplete or infeasible assignment into a
// published clinching claim.
func verifyCutoffWitness(p preparedSeason, threshold, k int, outcomes map[string]Outcome) error {
	if len(outcomes) != len(p.decision) {
		return fmt.Errorf("cutoff witness has %d outcomes for %d fixtures", len(outcomes), len(p.decision))
	}
	for _, game := range p.decision {
		outcome, ok := outcomes[game.ID]
		if !ok || !validOutcome(outcome) {
			return fmt.Errorf("cutoff witness has no valid outcome for fixture %q", game.ID)
		}
	}
	verified := countThresholdWitness(p, threshold, outcomes)
	if verified.count < k {
		return fmt.Errorf("cutoff witness reaches threshold for %d teams; need %d", verified.count, k)
	}
	return nil
}

type candidateResult struct {
	feasible bool
	outcomes map[string]Outcome
	diag     Diagnostics
}

type deficitState struct {
	game uint16
	lo   uint64
	hi   uint64
}

type componentCapacities struct {
	team      []int
	remaining [][]uint16
}

func solveCandidateSet(ctx context.Context, p preparedSeason, threshold int, selectedIDs []string) (candidateResult, error) {
	result := candidateResult{outcomes: make(map[string]Outcome, len(p.decision))}
	selected := make(map[string]int, len(selectedIDs))
	points := make([]int, len(selectedIDs))
	for i, id := range selectedIDs {
		selected[id] = i
		points[i] = p.points[id]
	}

	internal := make([]standings.Game, 0)
	for _, game := range p.decision {
		home, homeSelected := selected[game.HomeTeamID]
		away, awaySelected := selected[game.AwayTeamID]
		switch {
		case homeSelected && awaySelected:
			internal = append(internal, game)
		case homeSelected:
			points[home] += 3
			result.outcomes[game.ID] = HomeWin
		case awaySelected:
			points[away] += 3
			result.outcomes[game.ID] = AwayWin
		default:
			result.outcomes[game.ID] = Draw
		}
	}
	result.diag.ReducedFixtures = len(internal)

	deficits := make([]int, len(selectedIDs))
	pressure := make([]int, len(selectedIDs))
	for i := range deficits {
		deficits[i] = max(0, threshold-points[i])
		pressure[i] = deficits[i]
	}
	// Put fixtures involving the most constrained teams first so individual
	// remaining-capacity bounds become decisive early.
	sort.Slice(internal, func(i, j int) bool {
		li := pressure[selected[internal[i].HomeTeamID]] + pressure[selected[internal[i].AwayTeamID]]
		lj := pressure[selected[internal[j].HomeTeamID]] + pressure[selected[internal[j].AwayTeamID]]
		if li != lj {
			return li > lj
		}
		return internal[i].ID < internal[j].ID
	})

	remaining := make([][]uint16, len(internal)+1)
	remaining[len(internal)] = make([]uint16, len(selectedIDs))
	for i := len(internal) - 1; i >= 0; i-- {
		remaining[i] = append([]uint16(nil), remaining[i+1]...)
		remaining[i][selected[internal[i].HomeTeamID]]++
		remaining[i][selected[internal[i].AwayTeamID]]++
	}
	components := buildComponentCapacities(internal, selected, len(selectedIDs))
	result.diag.ConnectedComponents = len(components.remaining[0])
	memo := map[deficitState]bool{}
	wideMemo := map[string]bool{}
	choices := make([]Outcome, len(internal))
	var walk func(int) (bool, error)
	walk = func(gameIndex int) (bool, error) {
		if result.diag.VisitedStates%512 == 0 {
			if err := ctx.Err(); err != nil {
				return false, fmt.Errorf("%w: %v", ErrComputeBudget, err)
			}
		}
		result.diag.VisitedStates++
		complete, totalDeficit := true, 0
		for i, deficit := range deficits {
			if deficit > 0 {
				complete = false
			}
			if deficit > 3*int(remaining[gameIndex][i]) {
				result.diag.IndividualPrunes++
				return false, nil
			}
			totalDeficit += deficit
		}
		// Fixtures in disconnected components cannot transfer points across the
		// graph. Each component therefore needs enough local three-point game
		// capacity to cover its own remaining deficits.
		for component, games := range components.remaining[gameIndex] {
			componentDeficit := 0
			for team, deficit := range deficits {
				if components.team[team] == component {
					componentDeficit += deficit
				}
			}
			if componentDeficit > 3*int(games) {
				result.diag.ComponentPrunes++
				return false, nil
			}
		}
		if complete {
			for i := gameIndex; i < len(choices); i++ {
				choices[i] = Draw
			}
			return true, nil
		}
		if gameIndex == len(internal) || totalDeficit > 3*(len(internal)-gameIndex) {
			result.diag.TotalPrunes++
			return false, nil
		}
		key, packed := packDeficitState(gameIndex, deficits)
		wideKey := ""
		if packed {
			if memo[key] {
				result.diag.MemoHits++
				return false, nil
			}
		} else {
			// Current NWSL states always fit the allocation-free packed key. The
			// wide fallback preserves exactness for generated leagues with more
			// than 18 selected teams, unusually long schedules, or >65k games.
			wideKey = fmt.Sprintf("%d:%v", gameIndex, deficits)
			if wideMemo[wideKey] {
				result.diag.MemoHits++
				return false, nil
			}
		}

		game := internal[gameIndex]
		home, away := selected[game.HomeTeamID], selected[game.AwayTeamID]
		outcomes := []Outcome{HomeWin, AwayWin, Draw}
		sort.SliceStable(outcomes, func(i, j int) bool {
			return deficitReduction(deficits, home, away, outcomes[i]) > deficitReduction(deficits, home, away, outcomes[j])
		})
		for _, outcome := range outcomes {
			homePoints, awayPoints := outcomePoints(outcome, true), outcomePoints(outcome, false)
			oldHome, oldAway := deficits[home], deficits[away]
			deficits[home] = max(0, deficits[home]-homePoints)
			deficits[away] = max(0, deficits[away]-awayPoints)
			ok, err := walk(gameIndex + 1)
			deficits[home], deficits[away] = oldHome, oldAway
			if err != nil {
				return false, err
			}
			if ok {
				choices[gameIndex] = outcome
				return true, nil
			}
		}
		if packed {
			memo[key] = true
		} else {
			wideMemo[wideKey] = true
		}
		return false, nil
	}

	ok, err := walk(0)
	if err != nil || !ok {
		return result, err
	}
	for i, game := range internal {
		result.outcomes[game.ID] = choices[i]
	}
	result.feasible = true
	return result, nil
}

func buildComponentCapacities(games []standings.Game, selected map[string]int, teams int) componentCapacities {
	parent := make([]int, teams)
	for team := range parent {
		parent[team] = team
	}
	var root func(int) int
	root = func(team int) int {
		if parent[team] != team {
			parent[team] = root(parent[team])
		}
		return parent[team]
	}
	for _, game := range games {
		home, away := root(selected[game.HomeTeamID]), root(selected[game.AwayTeamID])
		if home != away {
			parent[away] = home
		}
	}
	componentByRoot := map[int]int{}
	teamComponent := make([]int, teams)
	for team := range teams {
		teamRoot := root(team)
		component, ok := componentByRoot[teamRoot]
		if !ok {
			component = len(componentByRoot)
			componentByRoot[teamRoot] = component
		}
		teamComponent[team] = component
	}
	remaining := make([][]uint16, len(games)+1)
	remaining[len(games)] = make([]uint16, len(componentByRoot))
	for gameIndex := len(games) - 1; gameIndex >= 0; gameIndex-- {
		remaining[gameIndex] = append([]uint16(nil), remaining[gameIndex+1]...)
		component := teamComponent[selected[games[gameIndex].HomeTeamID]]
		remaining[gameIndex][component]++
	}
	return componentCapacities{team: teamComponent, remaining: remaining}
}

func packDeficitState(game int, deficits []int) (deficitState, bool) {
	if game > int(^uint16(0)) || len(deficits) > 18 {
		return deficitState{}, false
	}
	var lo, hi uint64
	for i, deficit := range deficits {
		if deficit < 0 || deficit > 127 {
			return deficitState{}, false
		}
		value := uint64(deficit)
		shift := uint((i % 9) * 7)
		if i < 9 {
			lo |= value << shift
		} else {
			hi |= value << shift
		}
	}
	return deficitState{game: uint16(game), lo: lo, hi: hi}, true
}

func deficitReduction(deficits []int, home, away int, outcome Outcome) int {
	return min(deficits[home], outcomePoints(outcome, true)) + min(deficits[away], outcomePoints(outcome, false))
}
