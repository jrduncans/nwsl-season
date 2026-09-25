package scenarios

import (
	"context"
	"slices"
	"sort"
	"strconv"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// forcedPointsElimination asks whether every legal completion leaves at least
// topK opponents strictly above the target's best possible final points. The
// target wins every unassigned match; awarding it fewer points cannot help it.
// A tie stays possible, so this never infers elimination from a tiebreak.
func forcedPointsElimination(ctx context.Context, points map[string]int, teams []string, target string, topK int, slateRemaining, later []standings.Game) (bool, error) {
	ceiling := points[target]
	opponentGames := make([]standings.Game, 0, len(slateRemaining)+len(later))
	maximumGain := make(map[string]int, len(teams))
	for _, games := range [][]standings.Game{slateRemaining, later} {
		for _, game := range games {
			if game.HomeTeamID == target || game.AwayTeamID == target {
				ceiling += 3
				continue
			}
			opponentGames = append(opponentGames, game)
			maximumGain[game.HomeTeamID] += 3
			maximumGain[game.AwayTeamID] += 3
		}
	}
	safeNeeded := len(teams) - topK
	if safeNeeded <= 0 {
		return false, nil
	}
	alwaysSafe, possibleSafe := []string{}, []string{}
	for _, team := range teams {
		if team == target || points[team] > ceiling {
			continue
		}
		if points[team]+maximumGain[team] <= ceiling {
			alwaysSafe = append(alwaysSafe, team)
		} else {
			possibleSafe = append(possibleSafe, team)
		}
	}
	if len(alwaysSafe) >= safeNeeded {
		return false, nil
	}
	if len(alwaysSafe)+len(possibleSafe) < safeNeeded {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// Choose a set of teams to keep at or below the ceiling. Against a team
	// outside the set, that outsider can win and give the chosen team zero;
	// only fixtures with both participants inside the set constrain feasibility.
	sort.Slice(possibleSafe, func(i, j int) bool {
		left := ceiling - points[possibleSafe[i]]
		right := ceiling - points[possibleSafe[j]]
		if left != right {
			return left > right
		}
		return possibleSafe[i] < possibleSafe[j]
	})
	need := safeNeeded - len(alwaysSafe)
	chosen := slices.Clone(alwaysSafe)
	var choose func(int, int) (bool, error)
	choose = func(start, remaining int) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if remaining == 0 {
			return safeSetFeasible(ctx, points, ceiling, chosen, opponentGames)
		}
		for i := start; i <= len(possibleSafe)-remaining; i++ {
			chosen = append(chosen, possibleSafe[i])
			possible, err := choose(i+1, remaining-1)
			chosen = chosen[:len(chosen)-1]
			if err != nil || possible {
				return possible, err
			}
		}
		return false, nil
	}
	possible, err := choose(0, need)
	return !possible, err
}

type safeGame struct{ home, away int }

func safeSetFeasible(ctx context.Context, points map[string]int, ceiling int, safe []string, games []standings.Game) (bool, error) {
	index := make(map[string]int, len(safe))
	capacity := make([]int, len(safe))
	for i, team := range safe {
		index[team] = i
		capacity[i] = ceiling - points[team]
	}
	internal := []safeGame{}
	for _, game := range games {
		home, homeSafe := index[game.HomeTeamID]
		away, awaySafe := index[game.AwayTeamID]
		if homeSafe && awaySafe {
			internal = append(internal, safeGame{home: home, away: away})
		}
	}
	if len(internal) == 0 {
		return true, nil
	}
	sort.Slice(internal, func(i, j int) bool {
		left := min(capacity[internal[i].home], capacity[internal[i].away])
		right := min(capacity[internal[j].home], capacity[internal[j].away])
		return left < right
	})
	failed := map[string]bool{}
	var visit func(int) (bool, error)
	visit = func(gameIndex int) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if gameIndex == len(internal) {
			return true, nil
		}
		remainingCapacity := 0
		for _, value := range capacity {
			remainingCapacity += value
		}
		if remainingCapacity < 2*(len(internal)-gameIndex) {
			return false, nil
		}
		key := safeStateKey(gameIndex, capacity)
		if failed[key] {
			return false, nil
		}
		game := internal[gameIndex]
		for _, award := range [][2]int{{1, 1}, {3, 0}, {0, 3}} {
			if capacity[game.home] < award[0] || capacity[game.away] < award[1] {
				continue
			}
			capacity[game.home] -= award[0]
			capacity[game.away] -= award[1]
			possible, err := visit(gameIndex + 1)
			capacity[game.home] += award[0]
			capacity[game.away] += award[1]
			if err != nil || possible {
				return possible, err
			}
		}
		failed[key] = true
		return false, nil
	}
	return visit(0)
}

func safeStateKey(gameIndex int, capacity []int) string {
	key := strconv.AppendInt(nil, int64(gameIndex), 10)
	for _, value := range capacity {
		key = append(key, ':')
		key = strconv.AppendInt(key, int64(value), 10)
	}
	return string(key)
}
