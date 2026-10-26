package scenarios

import (
	"slices"
	"sort"
	"strconv"
	"strings"
)

type pairVerdict uint8

const (
	pairBehind pairVerdict = iota
	pairAhead
	pairConditional
)

// pairResult says whether the target finishes ahead of one rival level with
// it on points but never level with it through goals scored. A conditional
// result carries a score condition that decides it exactly.
type pairResult struct {
	verdict   pairVerdict
	condition ScoreCondition
}

// infinity stands in for an unbounded margin. Sums of a few of them stay far
// from overflow and compare correctly with any real goal count.
const infinity = 1 << 40

// pair compares the target with a rival that cannot finish level with it on
// goal difference, wins and goals scored, so head-to-head never applies (see
// levelRegions for the others). The remaining matches split into the
// target's other matches (A), the rival's other matches (B) and their
// matches against each other (C). A win is a margin of 1 or more, a draw
// exactly 0 with equal goals, and a loss -1 or less, so the goal-difference
// gain g is the sum of A's margins, minus B's, plus twice C's.
func (s *seasonSearch) pair(rival int) pairResult {
	t := s.target
	dW := s.wins[t] - s.wins[rival]
	counts := s.pairCounts(rival)
	d0 := s.gdNow[t] - s.gdNow[rival]
	f0 := s.gfNow[t] - s.gfNow[rival]
	var result pairResult
	if counts.onlyHeadToHead() {
		low, high := counts.headToHeadMargins()
		result = headToHeadOnly(d0, dW, f0, low, high, func(minimum int) ScoreLevel { return s.level(t, rival, MeasureGoalDifference, minimum, false) })
	} else {
		gaps := counts.gains()
		minimum := -d0 + 1 // a goal-difference gain that finishes strictly ahead
		if dW > 0 {
			minimum = -d0 // level goal difference, then more wins
		}
		switch {
		case gaps.allAtLeast(minimum):
			result.verdict = pairAhead
		case !gaps.anyAtLeast(minimum):
			result.verdict = pairBehind
		default:
			result = pairResult{verdict: pairConditional, condition: ScoreCondition{Levels: []ScoreLevel{s.level(t, rival, MeasureGoalDifference, minimum, false)}}}
		}
	}
	result.condition.TeamID = s.teamID(t)
	result.condition.RivalID = s.teamID(rival)
	return result
}

// level builds one tiebreaker level from team's side against rival, with
// both teams' values before the remaining matches.
func (s *seasonSearch) level(team, rival int, measure ScoreMeasure, minimum int, exactOnly bool) ScoreLevel {
	level := ScoreLevel{Measure: measure, MinimumGain: minimum, ExactOnly: exactOnly}
	switch measure {
	case MeasureGoalDifference:
		level.TeamNow, level.RivalNow = s.gdNow[team], s.gdNow[rival]
	case MeasureGoalsScored:
		level.TeamNow, level.RivalNow = s.gfNow[team], s.gfNow[rival]
	case MeasureHeadToHeadGoals:
		level.TeamNow, level.RivalNow = s.h2hGoals[team][rival], s.h2hGoals[rival][team]
	}
	return level
}

func (s *seasonSearch) condition(team, rival int, levels ...ScoreLevel) []ScoreCondition {
	return []ScoreCondition{{TeamID: s.teamID(team), RivalID: s.teamID(rival), Levels: levels}}
}

// A level rival is in one of three states against the target once the
// results are fixed: the target finishes ahead of it before head-to-head,
// it finishes ahead of the target, or the two are level on goal difference,
// wins and goals scored and join the target's head-to-head group.
const (
	stateAhead = iota
	stateBehind
	stateLevel
)

// levelRival is a rival that can finish level with the target through goals
// scored. Its states split every scoreline exactly; token holds the score
// condition for each possible state, and aheadOrLevel the one condition for
// "ahead or level". Once level, the head-to-head goal difference between the
// two is h2hNow plus their remaining margins against each other, which lie
// in [h2hLow, h2hHigh].
type levelRival struct {
	rival            int
	possible         [3]bool
	token            [3][]ScoreCondition
	aheadOrLevel     []ScoreCondition
	h2hNow           int
	h2hLow, h2hHigh  int
	h2hFromRemaining bool
}

// levelRegions reports whether a rival can finish level with the target on
// goal difference, wins and goals scored, and if so describes the three
// states. Wins must already be equal.
func (s *seasonSearch) levelRegions(rival int) (levelRival, bool) {
	t := s.target
	if s.wins[t] != s.wins[rival] {
		return levelRival{}, false
	}
	counts := s.pairCounts(rival)
	d0 := s.gdNow[t] - s.gdNow[rival]
	f0 := s.gfNow[t] - s.gfNow[rival]
	h0 := s.h2hGoals[t][rival] - s.h2hGoals[rival][t]
	lr := levelRival{rival: rival, h2hNow: h0}
	low, high := counts.headToHeadMargins()
	if counts.onlyHeadToHead() {
		// Every gain follows the margin sum m of their matches against each
		// other: goal difference 2m and goals scored m. Level needs one m.
		if d0 != 2*f0 || -f0 < low || -f0 > high {
			return levelRival{}, false
		}
		m := -f0
		lr.possible = [3]bool{high >= m+1, low <= m-1, true}
		lr.token[stateAhead] = s.condition(t, rival, s.level(t, rival, MeasureGoalDifference, 2*m+2, false))
		lr.token[stateBehind] = s.condition(rival, t, s.level(rival, t, MeasureGoalDifference, -2*m+2, false))
		lr.token[stateLevel] = s.condition(t, rival, s.level(t, rival, MeasureGoalDifference, 2*m+1, true))
		lr.aheadOrLevel = s.condition(t, rival, s.level(t, rival, MeasureGoalDifference, 2*m, false))
		if !lr.possible[stateAhead] && !lr.possible[stateBehind] {
			lr.token[stateLevel] = nil
		}
		lr.h2hNow, lr.h2hLow, lr.h2hHigh = h0+m, h0+m, h0+m
		return lr, true
	}
	gaps := counts.gains()
	level := -d0
	if !gaps.contains(level) {
		return levelRival{}, false
	}
	goals := -f0
	aheadExact := !gaps.anyAtLeast(level + 1)
	behindExact := !gaps.anyAtMost(level - 1)
	lr.possible = [3]bool{true, true, true}
	lr.token[stateAhead] = s.condition(t, rival, s.level(t, rival, MeasureGoalDifference, level+1, aheadExact), s.level(t, rival, MeasureGoalsScored, goals+1, false))
	lr.token[stateBehind] = s.condition(rival, t, s.level(rival, t, MeasureGoalDifference, -level+1, behindExact), s.level(rival, t, MeasureGoalsScored, -goals+1, false))
	lr.token[stateLevel] = s.condition(t, rival, s.level(t, rival, MeasureGoalDifference, level+1, true), s.level(t, rival, MeasureGoalsScored, goals+1, true))
	lr.aheadOrLevel = s.condition(t, rival, s.level(t, rival, MeasureGoalDifference, level+1, aheadExact), s.level(t, rival, MeasureGoalsScored, goals, false))
	if counts.cw+counts.cl > 0 {
		lr.h2hFromRemaining = true
		lr.h2hLow, lr.h2hHigh = h0+low, h0+high
	} else {
		lr.h2hLow, lr.h2hHigh = h0, h0
	}
	return lr, true
}

// maxSeasonLevelRivals bounds the three-state expansion at one leaf.
const maxSeasonLevelRivals = 6

// alternativeSet is a list of alternatives, each a conjunction of score
// conditions, that together describe exactly the scorelines where the
// target clinches. disciplinary marks reachable scorelines that need
// disciplinary points; unresolved marks possible clinches left unclaimed.
type alternativeSet struct {
	alternatives             [][]ScoreCondition
	disciplinary, unresolved bool
}

// levelAlternatives fixes the state of each level rival in turn and joins the
// results. A rival whose possible states all give the same alternatives
// needs no condition; "ahead" and "level" with the same alternatives share
// the one "ahead or level" condition.
func (s *seasonSearch) levelAlternatives(levels []levelRival, states []int, simple []pairResult, slots int) alternativeSet {
	i := len(states)
	if i == len(levels) {
		return s.settleLevels(levels, states, simple, slots)
	}
	lr := levels[i]
	var sub [3]alternativeSet
	out := alternativeSet{}
	keys := [3]string{}
	for state := range 3 {
		if !lr.possible[state] {
			continue
		}
		sub[state] = s.levelAlternatives(levels, append(states, state), simple, slots)
		keys[state] = alternativesKey(sub[state].alternatives)
		out.disciplinary = out.disciplinary || sub[state].disciplinary
		out.unresolved = out.unresolved || sub[state].unresolved
	}
	allSame, found := true, false
	var shared alternativeSet
	for state := range 3 {
		if !lr.possible[state] {
			continue
		}
		if !found {
			shared, found = sub[state], true
		} else if keys[state] != alternativesKey(shared.alternatives) {
			allSame = false
		}
	}
	prefix := func(token []ScoreCondition, alternatives [][]ScoreCondition) {
		for _, alternative := range alternatives {
			out.alternatives = append(out.alternatives, append(slices.Clone(token), alternative...))
		}
	}
	switch {
	case allSame:
		out.alternatives = shared.alternatives
	case lr.possible[stateAhead] && keys[stateAhead] == keys[stateLevel]:
		prefix(lr.aheadOrLevel, sub[stateAhead].alternatives)
		if lr.possible[stateBehind] {
			prefix(lr.token[stateBehind], sub[stateBehind].alternatives)
		}
	default:
		for state := range 3 {
			if lr.possible[state] {
				prefix(lr.token[state], sub[state].alternatives)
			}
		}
	}
	return out
}

// settleLevels ranks the target once every level rival's state is fixed.
// Rivals in the behind state finish ahead of it. The rivals level with it
// form its tied group, ordered by head-to-head points among exactly that
// group; a subgroup still level on those goes to head-to-head goals scored
// among the subgroup, as in standings.Calculate.
func (s *seasonSearch) settleLevels(levels []levelRival, states []int, simple []pairResult, slots int) alternativeSet {
	t := s.target
	behind := 0
	group := []int{t}
	for i, state := range states {
		switch state {
		case stateBehind:
			behind++
		case stateLevel:
			group = append(group, levels[i].rival)
		}
	}
	points := func(team int) int {
		total := 0
		for _, other := range group {
			if other != team {
				total += s.headToHeadPoints(team, other)
			}
		}
		return total
	}
	targetPoints := points(t)
	above, tied := 0, []int{}
	for i, state := range states {
		if state != stateLevel {
			continue
		}
		switch p := points(levels[i].rival); {
		case p > targetPoints:
			above++
		case p == targetPoints:
			tied = append(tied, i)
		}
	}
	choose := func(extra int) alternativeSet { return s.chooseSimple(simple, behind+above+extra, slots) }
	// tie settles a tie that only disciplinary points (or an unpublished
	// condition) can break: claim what holds even if every tied team
	// finishes ahead, and flag what would hold if none did.
	tie := func(ahead, level int, unresolved bool) alternativeSet {
		if s.exact && level > 0 {
			for extra := ahead; extra <= ahead+level; extra++ {
				if len(choose(extra).alternatives) > 0 {
					return alternativeSet{disciplinary: !unresolved, unresolved: unresolved}
				}
			}
			return alternativeSet{}
		}
		worst, best := choose(ahead+level), choose(ahead)
		if s.minimum {
			worst, best = best, worst
		}
		if alternativesKey(worst.alternatives) != alternativesKey(best.alternatives) {
			if unresolved {
				worst.unresolved = true
			} else {
				worst.disciplinary = true
			}
		}
		return worst
	}
	switch len(tied) {
	case 0:
		return choose(0)
	case 1:
		lr := levels[tied[0]]
		switch {
		case !lr.h2hFromRemaining && lr.h2hNow > 0:
			return choose(0)
		case !lr.h2hFromRemaining && lr.h2hNow < 0:
			return choose(1)
		case !lr.h2hFromRemaining:
			return tie(0, 1, false)
		}
		// Their remaining matches decide head-to-head goals: the target
		// wins it with a head-to-head gain above -h2hNow.
		if s.exact {
			out := alternativeSet{}
			for _, branch := range []struct{ extra, team, rival, minimum int }{
				{0, t, lr.rival, -lr.h2hNow + 1},
				{1, lr.rival, t, lr.h2hNow + 1},
			} {
				set := choose(branch.extra)
				out.unresolved = out.unresolved || set.unresolved
				for _, alternative := range set.alternatives {
					out.alternatives = append(out.alternatives, append(s.condition(branch.team, branch.rival, s.level(branch.team, branch.rival, MeasureHeadToHeadGoals, branch.minimum, false)), alternative...))
				}
			}
			out.disciplinary = len(out.alternatives) > 0 && lr.h2hLow <= 0 && lr.h2hHigh >= 0
			return out
		}
		worst, best := choose(1), choose(0)
		team, rival, minimum := t, lr.rival, -lr.h2hNow+1
		if s.minimum {
			worst, best = best, worst
			team, rival, minimum = lr.rival, t, lr.h2hNow+1
		}
		out := worst
		covered := map[string]bool{}
		for _, alternative := range worst.alternatives {
			covered[conjunctionKey(alternative)] = true
		}
		extra := false
		for _, alternative := range best.alternatives {
			if covered[conjunctionKey(alternative)] {
				continue
			}
			extra = true
			out.alternatives = append(out.alternatives, append(s.condition(team, rival, s.level(team, rival, MeasureHeadToHeadGoals, minimum, false)), alternative...))
		}
		out.unresolved = out.unresolved || best.unresolved
		out.disciplinary = extra && lr.h2hLow <= 0 && lr.h2hHigh >= 0
		return out
	}
	// Three or more level on head-to-head points: head-to-head goals among
	// them are known only when none of their remaining matches is between
	// two of them.
	subgroup := []int{t}
	for _, i := range tied {
		subgroup = append(subgroup, levels[i].rival)
	}
	for _, a := range subgroup {
		for _, b := range subgroup {
			if a != b && s.pendingBetween(a, b) {
				return tie(0, len(tied), true)
			}
		}
	}
	goals := func(team int) int {
		total := 0
		for _, other := range subgroup {
			if other != team {
				total += s.h2hGoals[team][other]
			}
		}
		return total
	}
	more, level := 0, 0
	for _, rival := range subgroup[1:] {
		switch g := goals(rival); {
		case g > goals(t):
			more++
		case g == goals(t):
			level++
		}
	}
	if level == 0 {
		return choose(more)
	}
	return tie(more, level, false)
}

// chooseSimple settles the rivals that can never be level with the target:
// it must finish ahead of enough of the conditional ones that at most slots
// teams finish ahead of it, counting extra from the level rivals.
func (s *seasonSearch) chooseSimple(simple []pairResult, extra, slots int) alternativeSet {
	if s.exact || s.minimum {
		return s.chooseExact(simple, extra, slots)
	}
	behind, conditional := extra, []ScoreCondition{}
	for _, pair := range simple {
		switch pair.verdict {
		case pairBehind:
			behind++
		case pairConditional:
			conditional = append(conditional, pair.condition)
		}
	}
	need := behind + len(conditional) - slots
	switch {
	case need <= 0:
		return alternativeSet{alternatives: [][]ScoreCondition{{}}}
	case need > len(conditional):
		return alternativeSet{}
	case binomial(len(conditional), need) > maxSeasonAlternatives:
		return alternativeSet{unresolved: true}
	}
	out := alternativeSet{}
	chosen := make([]ScoreCondition, 0, need)
	var choose func(int)
	choose = func(start int) {
		if len(chosen) == need {
			out.alternatives = append(out.alternatives, slices.Clone(chosen))
			return
		}
		for i := start; i <= len(conditional)-(need-len(chosen)); i++ {
			chosen = append(chosen, conditional[i])
			choose(i + 1)
			chosen = chosen[:len(chosen)-1]
		}
	}
	choose(0)
	return out
}

func conjunctionKey(conditions []ScoreCondition) string {
	keys := make([]string, len(conditions))
	for i, condition := range conditions {
		keys[i] = scoreConditionKey(condition)
	}
	sort.Strings(keys)
	return strings.Join(keys, "&")
}

// alternativesKey identifies a list of alternatives; no alternative and one
// unconditional alternative get different keys.
func alternativesKey(alternatives [][]ScoreCondition) string {
	keys := make([]string, len(alternatives))
	for i, alternative := range alternatives {
		keys[i] = "(" + conjunctionKey(alternative) + ")"
	}
	sort.Strings(keys)
	return strings.Join(keys, "|")
}

// joinHeadToHead folds a head-to-head goals condition into the level
// condition for the same rival, so each rival has one cascade: level on goal
// difference and goals scored, then more head-to-head goals.
func joinHeadToHead(conditions []ScoreCondition) []ScoreCondition {
	out := make([]ScoreCondition, 0, len(conditions))
	for _, condition := range conditions {
		if len(condition.Levels) == 1 && condition.Levels[0].Measure == MeasureHeadToHeadGoals {
			joined := false
			for _, other := range conditions {
				if other.TeamID == condition.TeamID && other.RivalID == condition.RivalID && other.Levels[0].Measure != MeasureHeadToHeadGoals {
					joined = true
				}
			}
			if !joined {
				out = append(out, condition)
			}
			continue
		}
		for _, other := range conditions {
			if len(other.Levels) == 1 && other.Levels[0].Measure == MeasureHeadToHeadGoals && other.TeamID == condition.TeamID && other.RivalID == condition.RivalID {
				condition.Levels = append(slices.Clone(condition.Levels), other.Levels[0])
			}
		}
		out = append(out, condition)
	}
	return out
}

func scoreConditionKey(condition ScoreCondition) string {
	var b strings.Builder
	b.WriteString(condition.TeamID + ">" + condition.RivalID)
	for _, level := range condition.Levels {
		b.WriteString(":" + string(level.Measure) + "=" + strconv.Itoa(level.MinimumGain))
		if level.ExactOnly {
			b.WriteString("!")
		}
	}
	return b.String()
}

// pendingBetween reports whether a and b still play each other.
func (s *seasonSearch) pendingBetween(a, b int) bool {
	for _, index := range s.teamGames[a] {
		if game := s.games[index]; game.home == b || game.away == b {
			return true
		}
	}
	return false
}

// headToHeadPoints is the points team earned against opponent in completed
// matches and fixed outcomes.
func (s *seasonSearch) headToHeadPoints(team, opponent int) int {
	points := s.h2hPoints[team][opponent]
	for _, index := range s.teamGames[team] {
		game := s.games[index]
		if (game.home == opponent || game.away == opponent) && s.outcome[index] >= 0 {
			points += []int{0, 1, 3}[teamResult(game, s.outcome[index], team)+1]
		}
	}
	return points
}

// pairCounts tallies the target's and a rival's fixed remaining results:
// the target's other matches (a), the rival's other matches (b) and their
// matches against each other (c), as wins, losses and draws from the
// target's or rival's side.
type pairCounts struct {
	aw, al, ad, bw, bl, bd, cw, cl, cd int
}

func (s *seasonSearch) pairCounts(rival int) pairCounts {
	t := s.target
	c := pairCounts{}
	for _, index := range s.teamGames[t] {
		game := s.games[index]
		result := teamResult(game, s.outcome[index], t)
		if game.home == rival || game.away == rival {
			switch result {
			case 1:
				c.cw++
			case -1:
				c.cl++
			default:
				c.cd++
			}
			continue
		}
		switch result {
		case 1:
			c.aw++
		case -1:
			c.al++
		default:
			c.ad++
		}
	}
	for _, index := range s.teamGames[rival] {
		game := s.games[index]
		if game.home == t || game.away == t {
			continue
		}
		switch teamResult(game, s.outcome[index], rival) {
		case 1:
			c.bw++
		case -1:
			c.bl++
		default:
			c.bd++
		}
	}
	return c
}

// onlyHeadToHead reports whether the pair's only remaining matches are
// against each other.
func (c pairCounts) onlyHeadToHead() bool {
	return c.aw+c.al+c.ad+c.bw+c.bl+c.bd == 0
}

// headToHeadMargins bounds the sum of the target's margins against the rival.
func (c pairCounts) headToHeadMargins() (int, int) {
	low, high := c.cw, -c.cl
	if c.cl > 0 {
		low = -infinity
	}
	if c.cw > 0 {
		high = infinity
	}
	return low, high
}

// otherMargins bounds the target's margins in its other matches minus the
// rival's in theirs; the sum is fixed at 0 when all of them are draws.
func (c pairCounts) otherMargins() (low, high int, variable bool) {
	low, high = c.aw+c.bl, -c.al-c.bw
	if c.al > 0 || c.bw > 0 {
		low = -infinity
	}
	if c.aw > 0 || c.bl > 0 {
		high = infinity
	}
	return low, high, c.aw+c.al+c.bw+c.bl > 0
}

// gains is the set of feasible goal-difference gains for the target.
func (c pairCounts) gains() gainSet {
	loC, hiC := c.headToHeadMargins()
	loAB, hiAB, variable := c.otherMargins()
	if !variable {
		return gainSet{low: 2 * loC, high: 2 * hiC}
	}
	return gainSet{variable: true, low: loAB + 2*loC, high: hiAB + 2*hiC}
}

// headToHeadOnly handles a pair whose only remaining matches are against each
// other and who cannot finish level. Every gain then follows the margin sum m
// of those matches: goal difference 2m and goals scored m. Finishing ahead is
// monotone in m, so the condition is one goal-difference threshold.
func headToHeadOnly(d0, dW, f0, low, high int, gdLevel func(int) ScoreLevel) pairResult {
	window := absInt(d0) + absInt(f0) + 4
	low, high = max(low, -window), min(high, window)
	sign := func(v int) int {
		switch {
		case v > 0:
			return 1
		case v < 0:
			return -1
		}
		return 0
	}
	threshold, anyBehind := high+1, false
	for m := low; m <= high; m++ {
		verdict := 0
		for _, value := range []int{d0 + 2*m, dW, f0 + m} {
			if verdict = sign(value); verdict != 0 {
				break
			}
		}
		if verdict > 0 {
			threshold = min(threshold, m)
		} else {
			anyBehind = true
			threshold = high + 1
		}
	}
	switch {
	case threshold > high:
		return pairResult{verdict: pairBehind}
	case !anyBehind:
		return pairResult{verdict: pairAhead}
	}
	return pairResult{verdict: pairConditional, condition: ScoreCondition{Levels: []ScoreLevel{gdLevel(2 * threshold)}}}
}

// gainSet is the feasible goal-difference gains: every integer in
// [low, high], or only the even ones when just head-to-head margins vary.
type gainSet struct {
	variable  bool
	low, high int
}

func (g gainSet) contains(v int) bool {
	if v < g.low || v > g.high {
		return false
	}
	return g.variable || v%2 == 0
}

func (g gainSet) allAtLeast(v int) bool { return g.low >= v }

func (g gainSet) anyAtMost(v int) bool {
	if g.low > v {
		return false
	}
	if g.variable {
		return true
	}
	last := min(v, g.high)
	if last%2 != 0 {
		last--
	}
	return last >= g.low
}

func (g gainSet) anyAtLeast(v int) bool {
	if g.high < v {
		return false
	}
	if g.variable {
		return true
	}
	first := max(v, g.low)
	if first%2 != 0 {
		first++
	}
	return first <= g.high
}

// teamResult is 1 for a win, 0 for a draw and -1 for a loss by team.
func teamResult(game seasonGame, outcome int8, team int) int {
	switch {
	case outcome == 1:
		return 0
	case (outcome == 0) == (game.home == team):
		return 1
	}
	return -1
}
