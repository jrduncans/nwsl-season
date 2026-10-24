package scenarios

import (
	"context"
	"math/big"
	"slices"
	"strconv"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// Score feasibility is an integer linear problem over the remaining goals.
// Fourier-Motzkin elimination proves infeasibility over the rationals; integer
// branching handles parity (a head-to-head margin counts twice in GD). All
// arithmetic used for proofs is exact. Work and coefficient bounds leave a
// case unpublished with a limitation rather than guessing feasibility.
type scoreInequality struct {
	a []int64
	b int64
}
type scoreFeasibility uint8

const (
	scoreUnknown scoreFeasibility = iota
	scoreImpossible
	scorePossible
)
const maxScoreConstraints = 1024
const maxScoreCoefficient int64 = 1 << 28

func feasibleSeedCubes(ctx context.Context, cube outcomeCube, conditions []ScoreCondition, games []standings.Game, ids []string, emit func(outcomeCube), limited func()) {
	involved := map[string]bool{}
	for _, c := range conditions {
		involved[c.TeamID] = true
		involved[c.RivalID] = true
	}
	relevant := []standings.Game{}
	slots := []int{}
	for _, g := range games {
		if g.Status == "PreMatch" && (involved[g.HomeTeamID] || involved[g.AwayTeamID]) {
			relevant = append(relevant, g)
			slots = append(slots, slices.Index(ids, g.ID))
		}
	}
	var walk func(int, outcomeCube)
	walk = func(i int, c outcomeCube) {
		if ctx.Err() != nil {
			limited()
			return
		}
		if i < len(slots) {
			slot := slots[i]
			for _, mask := range outcomeMasks {
				if c.mask(slot)&mask != 0 {
					next := c
					next.set(slot, mask)
					walk(i+1, next)
				}
			}
			return
		}
		constraints := []scoreInequality{}
		n := 2 * len(relevant)
		for j, slot := range slots {
			for _, side := range []int{2 * j, 2*j + 1} {
				a := make([]int64, n)
				a[side] = -1
				constraints = append(constraints, scoreInequality{a, 0})
			}
			a := make([]int64, n)
			a[2*j], a[2*j+1] = 1, -1
			switch c.mask(slot) {
			case homeMask:
				constraints = append(constraints, scoreInequality{negateScoreCoefficients(a), -1})
			case awayMask:
				constraints = append(constraints, scoreInequality{a, -1})
			case drawMask:
				constraints = append(constraints, scoreInequality{a, 0}, scoreInequality{negateScoreCoefficients(a), 0})
			}
		}
		var cascade func(int, []scoreInequality) scoreFeasibility
		cascade = func(index int, rows []scoreInequality) scoreFeasibility {
			if ctx.Err() != nil {
				return scoreUnknown
			}
			if index == len(conditions) {
				budget := 128
				return integerScores(ctx, rows, n, &budget)
			}
			condition := conditions[index]
			prefix := slices.Clone(rows)
			unknown := false
			for i, l := range condition.Levels {
				a := scoreCoefficients(condition, l.Measure, relevant)
				if !l.ExactOnly {
					branch := append(slices.Clone(prefix), scoreInequality{negateScoreCoefficients(a), -int64(l.MinimumGain)})
					switch cascade(index+1, branch) {
					case scorePossible:
						return scorePossible
					case scoreUnknown:
						unknown = true
					}
				}
				exact := int64(l.MinimumGain - 1)
				prefix = append(prefix, scoreInequality{a, exact}, scoreInequality{negateScoreCoefficients(a), -exact})
				if i == len(condition.Levels)-1 && l.ExactOnly {
					switch cascade(index+1, prefix) {
					case scorePossible:
						return scorePossible
					case scoreUnknown:
						unknown = true
					}
				}
			}
			if unknown {
				return scoreUnknown
			}
			return scoreImpossible
		}
		switch cascade(0, constraints) {
		case scorePossible:
			emit(c)
		case scoreUnknown:
			limited()
		}
	}
	walk(0, cube)
}

func scoreCoefficients(c ScoreCondition, measure ScoreMeasure, games []standings.Game) []int64 {
	a := make([]int64, 2*len(games))
	for i, g := range games {
		headToHead := (g.HomeTeamID == c.TeamID && g.AwayTeamID == c.RivalID) || (g.HomeTeamID == c.RivalID && g.AwayTeamID == c.TeamID)
		if measure == MeasureHeadToHeadGoals && !headToHead {
			continue
		}
		for _, side := range []struct {
			id   string
			sign int64
		}{{c.TeamID, 1}, {c.RivalID, -1}} {
			own, other := 2*i, 2*i+1
			if g.AwayTeamID == side.id {
				own, other = other, own
			} else if g.HomeTeamID != side.id {
				continue
			}
			a[own] += side.sign
			if measure == MeasureGoalDifference {
				a[other] -= side.sign
			}
		}
	}
	return a
}
func negateScoreCoefficients(a []int64) []int64 {
	out := make([]int64, len(a))
	for i, v := range a {
		out[i] = -v
	}
	return out
}

func integerScores(ctx context.Context, rows []scoreInequality, n int, budget *int) scoreFeasibility {
	if *budget <= 0 || ctx.Err() != nil {
		return scoreUnknown
	}
	*budget--
	values, state := rationalScores(ctx, rows, n)
	if state != scorePossible {
		return state
	}
	for i, v := range values {
		if v.IsInt() {
			continue
		}
		floor := new(big.Int).Div(v.Num(), v.Denom())
		if !floor.IsInt64() {
			return scoreUnknown
		}
		bound := floor.Int64()
		if bound <= -maxScoreCoefficient || bound >= maxScoreCoefficient {
			return scoreUnknown
		}
		a := make([]int64, n)
		a[i] = 1
		left := integerScores(ctx, append(slices.Clone(rows), scoreInequality{a, bound}), n, budget)
		if left == scorePossible {
			return left
		}
		a = make([]int64, n)
		a[i] = -1
		right := integerScores(ctx, append(slices.Clone(rows), scoreInequality{a, -bound - 1}), n, budget)
		if right == scorePossible {
			return right
		}
		if left == scoreUnknown || right == scoreUnknown {
			return scoreUnknown
		}
		return scoreImpossible
	}
	return scorePossible
}

func rationalScores(ctx context.Context, rows []scoreInequality, n int) ([]*big.Rat, scoreFeasibility) {
	type step struct {
		variable int
		rows     []scoreInequality
	}
	steps := []step{}
	remaining := make([]bool, n)
	for i := range remaining {
		remaining[i] = true
	}
	current := slices.Clone(rows)
	for _, r := range current {
		if len(r.a) != n || r.b < -maxScoreCoefficient || r.b > maxScoreCoefficient {
			return nil, scoreUnknown
		}
		for _, a := range r.a {
			if a < -maxScoreCoefficient || a > maxScoreCoefficient {
				return nil, scoreUnknown
			}
		}
	}
	for range n {
		if ctx.Err() != nil {
			return nil, scoreUnknown
		}
		variable, cost := -1, int(^uint(0)>>1)
		for i, active := range remaining {
			if !active {
				continue
			}
			pos, neg := 0, 0
			for _, r := range current {
				if r.a[i] > 0 {
					pos++
				}
				if r.a[i] < 0 {
					neg++
				}
			}
			if pos*neg < cost {
				variable, cost = i, pos*neg
			}
		}
		if cost > maxScoreConstraints {
			return nil, scoreUnknown
		}
		remaining[variable] = false
		positive, negative, zero := []scoreInequality{}, []scoreInequality{}, []scoreInequality{}
		for _, r := range current {
			switch {
			case r.a[variable] > 0:
				positive = append(positive, r)
			case r.a[variable] < 0:
				negative = append(negative, r)
			default:
				zero = append(zero, r)
			}
		}
		steps = append(steps, step{variable, append(slices.Clone(positive), negative...)})
		next := zero
		for _, p := range positive {
			for _, m := range negative {
				pa, ma := -m.a[variable], p.a[variable]
				divisor := scoreGCD(pa, ma)
				pa /= divisor
				ma /= divisor
				a := make([]int64, n)
				b := pa*p.b + ma*m.b
				gcd := absScore(b)
				for i := range a {
					a[i] = pa*p.a[i] + ma*m.a[i]
					gcd = scoreGCD(gcd, absScore(a[i]))
				}
				if gcd == 0 {
					continue
				}
				b /= gcd
				if absScore(b) > maxScoreCoefficient {
					return nil, scoreUnknown
				}
				allZero := true
				for i := range a {
					a[i] /= gcd
					if absScore(a[i]) > maxScoreCoefficient {
						return nil, scoreUnknown
					}
					allZero = allZero && a[i] == 0
				}
				if allZero {
					if b < 0 {
						return nil, scoreImpossible
					}
					continue
				}
				next = append(next, scoreInequality{a, b})
			}
		}
		// Duplicate halfspaces otherwise grow exponentially during elimination.
		unique := map[string]int{}
		current = nil
		for _, r := range next {
			key := scoreVectorKey(r.a)
			if j, ok := unique[key]; ok {
				if r.b < current[j].b {
					current[j] = r
				}
			} else {
				unique[key] = len(current)
				current = append(current, r)
			}
		}
		if len(current) > maxScoreConstraints {
			return nil, scoreUnknown
		}
	}
	for _, r := range current {
		if r.b < 0 {
			return nil, scoreImpossible
		}
	}
	values := make([]*big.Rat, n)
	for i := range values {
		values[i] = new(big.Rat)
	}
	for k := len(steps) - 1; k >= 0; k-- {
		st := steps[k]
		var low, high *big.Rat
		for _, r := range st.rows {
			bound := new(big.Rat).SetInt64(r.b)
			for i, a := range r.a {
				if i != st.variable && a != 0 {
					bound.Sub(bound, new(big.Rat).Mul(new(big.Rat).SetInt64(a), values[i]))
				}
			}
			bound.Quo(bound, new(big.Rat).SetInt64(r.a[st.variable]))
			if r.a[st.variable] > 0 {
				if high == nil || bound.Cmp(high) < 0 {
					high = bound
				}
			} else if low == nil || bound.Cmp(low) > 0 {
				low = bound
			}
		}
		if low != nil && high != nil && low.Cmp(high) > 0 {
			return nil, scoreImpossible
		}
		if low != nil && low.Sign() > 0 {
			values[st.variable] = low
		} else if high != nil && high.Sign() < 0 {
			values[st.variable] = high
		}
	}
	return values, scorePossible
}

func scoreGCD(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
func absScore(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func scoreVectorKey(a []int64) string {
	b := []byte{}
	for _, v := range a {
		b = strconv.AppendInt(b, v, 10)
		b = append(b, ',')
	}
	return string(b)
}
