package scenarios

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// SeedingResult is stored on the playoff result in the rest-of-season batch.
type SeedingResult struct {
	Seeds                []SeedFinish `json:"seeds"`
	DisciplinaryPossible bool         `json:"disciplinary_possible,omitempty"`
	Limitation           string       `json:"limitation,omitempty"`
}

// SeedFinish is one place the team can still finish. Seed zero collects every
// finish outside the playoffs and has no opponents.
type SeedFinish struct {
	Seed         int `json:"seed"`
	OpponentSeed int `json:"opponent_seed,omitempty"`
	// Clauses is the disjunction for finishing exactly at Seed, whoever the
	// opponent is. Alternatives no integer scoreline satisfies are dropped.
	Clauses   []Clause       `json:"clauses"`
	Opponents []SeedOpponent `json:"opponents,omitempty"`
	// PossibleResults is only set on seed zero; see SeedOpponent.
	PossibleResults []Clause `json:"possible_results,omitempty"`
}

// SeedOpponent is a first-round opponent the team can still draw from a seed.
type SeedOpponent struct {
	TeamID string `json:"team_id"`
	// PossibleResults is the exact union of result assignments for which at
	// least one integer scoreline puts both teams in the paired seeds. It is
	// only a filter, never a score-independent claim about a finish.
	PossibleResults []Clause `json:"possible_results"`
}

// chooseExact partitions the conditional rivals into those ahead and behind.
// Unlike a top-K proof, both sides of every comparison are required.
func (s *seasonSearch) chooseExact(simple []pairResult, extra, slots int) alternativeSet {
	behind := extra
	conditional := []ScoreCondition{}
	for _, pair := range simple {
		switch pair.verdict {
		case pairBehind:
			behind++
		case pairConditional:
			conditional = append(conditional, pair.condition)
		}
	}
	need := behind + len(conditional) - slots
	if s.minimum {
		need = slots + 1 - behind
		if need <= 0 {
			return alternativeSet{alternatives: [][]ScoreCondition{{}}}
		}
	}
	if need < 0 || need > len(conditional) {
		return alternativeSet{}
	}
	if binomial(len(conditional), need) > maxSeasonAlternatives {
		return alternativeSet{unresolved: true}
	}
	out := alternativeSet{}
	var choose func(int, int, []ScoreCondition)
	choose = func(i, left int, conditions []ScoreCondition) {
		if left < 0 || left > len(conditional)-i {
			return
		}
		if i == len(conditional) {
			out.alternatives = append(out.alternatives, slices.Clone(conditions))
			return
		}
		c := conditional[i]
		if s.minimum {
			choose(i+1, left, slices.Clone(conditions))
		} else {
			choose(i+1, left-1, append(slices.Clone(conditions), c))
		}
		target := s.target
		s.target = slices.Index(s.teamIDs, c.RivalID)
		reverse := s.pair(target)
		s.target = target
		if s.minimum {
			choose(i+1, left-1, append(slices.Clone(conditions), reverse.condition))
		} else {
			choose(i+1, left, append(slices.Clone(conditions), reverse.condition))
		}
	}
	choose(0, need, nil)
	return out
}

// GenerateSeeding reuses the season search for exact ranks, then intersects
// the two ranks named by each catalog seed pair. Score feasibility is checked
// jointly: two individually possible ranks need not be possible together.
// No request-time search is needed, including for fixed-result filters.
func GenerateSeeding(ctx context.Context, teams []standings.Team, games []standings.Game, slate Slate, pairs [][2]int) (map[string]*SeedingResult, error) {
	if err := slate.Validate(); err != nil {
		return nil, err
	}
	if slate.Source != SourceRestOfSeason || len(teams) > 64 || len(teams) < 2 {
		return nil, fmt.Errorf("invalid seeding request")
	}
	partners := map[int]int{}
	for _, p := range pairs {
		if p[0] < 1 || p[1] < 1 || p[0] > len(teams) || p[1] > len(teams) || p[0] == p[1] || partners[p[0]] != 0 || partners[p[1]] != 0 {
			return nil, fmt.Errorf("invalid seeding pairs")
		}
		partners[p[0]], partners[p[1]] = p[1], p[0]
	}
	places := len(partners)
	for i := 1; i <= places; i++ {
		if partners[i] == 0 {
			return nil, fmt.Errorf("incomplete seeding pairs")
		}
	}
	if places == 0 {
		return nil, fmt.Errorf("seeding pairs required")
	}
	slot := map[string]int{}
	for i, id := range slate.FixtureIDs {
		slot[id] = i
	}
	pending := 0
	for _, g := range games {
		if g.Status == "PreMatch" {
			pending++
			if _, ok := slot[g.ID]; !ok {
				return nil, fmt.Errorf("incomplete seeding slate")
			}
		}
	}
	if pending != len(slot) || pending > maxSeasonFixtures {
		return nil, fmt.Errorf("invalid seeding slate")
	}
	ranks := map[string][]Result{}
	misses := map[string][]Clause{}
	results := map[string]*SeedingResult{}
	for _, team := range teams {
		result := &SeedingResult{Seeds: []SeedFinish{}}
		results[team.ID] = result
		members := make([]seasonMember, places)
		for i := range members {
			members[i] = seasonMember{achievement: competition.Achievement{ID: competition.AchievementID(fmt.Sprint(i + 1)), TopK: i + 1}, labels: map[string]int{}}
		}
		search, err := newSeasonSearch(ctx, SeasonRequest{Teams: teams, Games: games, Slate: slate, TargetTeamID: team.ID}, slot, members)
		if err != nil {
			return nil, err
		}
		search.exact = true
		if ctx.Err() == nil {
			search.walk(uint64(1)<<uint(len(members)) - 1)
		} else {
			search.err = ctx.Err()
		}
		if search.err != nil && ctx.Err() == nil {
			return nil, search.err
		}
		for i := range search.members {
			member := &search.members[i]
			r := member.finish(ctx, emptyResult(team.ID, member.achievement), slate.FixtureIDs, search.err == nil)
			ranks[team.ID] = append(ranks[team.ID], r)
			result.DisciplinaryPossible = result.DisciplinaryPossible || r.DisciplinaryPossible
			if r.Limitation != "" {
				result.Limitation = r.Limitation
			}
		}
		search.exact = false
		search.minimum = true
		search.members = []seasonMember{{achievement: competition.Achievement{ID: "miss", TopK: places}, labels: map[string]int{}}}
		if ctx.Err() == nil {
			search.walk(1)
		}
		miss := search.members[0].finish(ctx, Result{}, slate.FixtureIDs, search.err == nil)
		misses[team.ID] = miss.Clauses
		result.DisciplinaryPossible = result.DisciplinaryPossible || miss.DisciplinaryPossible
		if miss.Limitation != "" {
			result.Limitation = miss.Limitation
		}
	}
	label := seasonLabel{method: clinching.ProofAccessibleTiebreak}
	for _, team := range teams {
		result := results[team.ID]
		for seed := 1; seed <= places; seed++ {
			own := feasibleSeedClauses(ctx, ranks[team.ID][seed-1].Clauses, games, slate.FixtureIDs, func() { result.Limitation = LimitationBudgetPartial })
			finish := SeedFinish{Seed: seed, OpponentSeed: partners[seed], Clauses: own}
			for _, opponent := range teams {
				if opponent.ID == team.ID || len(own) == 0 {
					continue
				}
				other := ranks[opponent.ID][partners[seed]-1]
				result.DisciplinaryPossible = result.DisciplinaryPossible || other.DisciplinaryPossible
				if other.Limitation != "" && result.Limitation != LimitationBudgetPartial {
					result.Limitation = other.Limitation
				}
				member := seasonMember{labels: map[string]int{}}
				for _, a := range own {
					if ctx.Err() != nil {
						result.Limitation = LimitationBudgetPartial
						break
					}
					for _, b := range other.Clauses {
						if ctx.Err() != nil {
							result.Limitation = LimitationBudgetPartial
							break
						}
						cube, ok := intersectSeedCubes(a, b, slate.FixtureIDs)
						if !ok {
							continue
						}
						conditions := uniqueScoreConditions(append(slices.Clone(a.ScoreConditions), b.ScoreConditions...))
						if len(conditions) == 0 {
							member.emit(cube, label)
							continue
						}
						feasibleSeedCubes(ctx, cube, conditions, games, slate.FixtureIDs, func(c outcomeCube) { member.emit(c, label) }, func() { result.Limitation = LimitationBudgetPartial })
					}
				}
				if len(member.clauses) > 0 {
					value := member.finish(ctx, Result{}, slate.FixtureIDs, true)
					finish.Opponents = append(finish.Opponents, SeedOpponent{TeamID: opponent.ID, PossibleResults: value.Clauses})
				}
			}
			if len(finish.Opponents) > 0 {
				result.Seeds = append(result.Seeds, finish)
			}
		}
		miss := feasibleSeedClauses(ctx, misses[team.ID], games, slate.FixtureIDs, func() { result.Limitation = LimitationBudgetPartial })
		if len(miss) > 0 {
			member := seasonMember{labels: map[string]int{}}
			for _, clause := range miss {
				cube, _ := intersectSeedCubes(clause, Clause{}, slate.FixtureIDs)
				if len(clause.ScoreConditions) == 0 {
					member.emit(cube, label)
				} else {
					feasibleSeedCubes(ctx, cube, clause.ScoreConditions, games, slate.FixtureIDs, func(c outcomeCube) { member.emit(c, label) }, func() { result.Limitation = LimitationBudgetPartial })
				}
			}
			if len(member.clauses) > 0 {
				support := member.finish(ctx, Result{}, slate.FixtureIDs, true)
				result.Seeds = append(result.Seeds, SeedFinish{Clauses: miss, PossibleResults: support.Clauses})
			}
		}
	}
	return results, nil
}

func intersectSeedCubes(a, b Clause, ids []string) (outcomeCube, bool) {
	cube := allOutcomes(len(ids))
	for _, conditions := range [][]FixtureCondition{a.Conditions, b.Conditions} {
		for _, c := range conditions {
			slot := slices.Index(ids, c.GameID)
			mask := uint8(0)
			for i, o := range []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin} {
				if slices.Contains(c.AllowedOutcomes, o) {
					mask |= outcomeMasks[i]
				}
			}
			cube.set(slot, cube.mask(slot)&mask)
			if cube.mask(slot) == 0 {
				return cube, false
			}
		}
	}
	return cube, true
}

func uniqueScoreConditions(in []ScoreCondition) []ScoreCondition {
	out := []ScoreCondition{}
	seen := map[string]bool{}
	for _, c := range in {
		key := scoreConditionKey(c)
		if !seen[key] {
			out = append(out, c)
			seen[key] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return scoreConditionKey(out[i]) < scoreConditionKey(out[j]) })
	return out
}

// feasibleSeedClauses drops alternatives that no integer scoreline satisfies,
// so a seed's listed conditions never include an impossible alternative.
func feasibleSeedClauses(ctx context.Context, clauses []Clause, games []standings.Game, ids []string, limited func()) []Clause {
	out := []Clause{}
	for _, clause := range clauses {
		if len(clause.ScoreConditions) == 0 {
			out = append(out, clause)
			continue
		}
		cube, ok := intersectSeedCubes(clause, Clause{}, ids)
		if !ok {
			continue
		}
		feasible := false
		feasibleSeedCubes(ctx, cube, clause.ScoreConditions, games, ids, func(outcomeCube) { feasible = true }, limited)
		if feasible {
			out = append(out, clause)
		}
	}
	return out
}

// FilterSeeding applies result assumptions to cached alternatives. Scores
// remain open, so score conditions are retained even on a fixed match. A seed
// or opponent that the assumptions make impossible is removed.
func FilterSeeding(in SeedingResult, fixed map[string]clinching.Outcome) SeedingResult {
	out := in
	out.Seeds = []SeedFinish{}
	for _, seed := range in.Seeds {
		next := seed
		next.Clauses = filterSeedClauses(seed.Clauses, fixed)
		next.PossibleResults = filterSeedClauses(seed.PossibleResults, fixed)
		next.Opponents = []SeedOpponent{}
		for _, opponent := range seed.Opponents {
			if possible := filterSeedClauses(opponent.PossibleResults, fixed); len(possible) > 0 {
				out := opponent
				out.PossibleResults = possible
				next.Opponents = append(next.Opponents, out)
			}
		}
		possible := len(next.Opponents) > 0
		if seed.Seed == 0 {
			possible = len(next.PossibleResults) > 0
		}
		if len(next.Clauses) > 0 && possible {
			out.Seeds = append(out.Seeds, next)
		}
	}
	return out
}

func (r SeedingResult) validate(team string, fixtures map[string]bool) error {
	seen := map[int]bool{}
	for _, seed := range r.Seeds {
		valid := seed.Seed >= 0 && !seen[seed.Seed] && len(seed.Clauses) > 0
		if seed.Seed == 0 {
			valid = valid && seed.OpponentSeed == 0 && len(seed.Opponents) == 0 && len(seed.PossibleResults) > 0
		} else {
			valid = valid && seed.OpponentSeed > 0 && seed.OpponentSeed != seed.Seed && len(seed.Opponents) > 0 && len(seed.PossibleResults) == 0
		}
		if !valid {
			return fmt.Errorf("invalid seed finish")
		}
		seen[seed.Seed] = true
		clauses := append(slices.Clone(seed.Clauses), seed.PossibleResults...)
		opponents := map[string]bool{}
		for _, opponent := range seed.Opponents {
			if opponent.TeamID == "" || opponent.TeamID == team || opponents[opponent.TeamID] || len(opponent.PossibleResults) == 0 {
				return fmt.Errorf("invalid seed opponent")
			}
			opponents[opponent.TeamID] = true
			clauses = append(clauses, opponent.PossibleResults...)
		}
		for _, c := range clauses {
			if c.RepresentedAssignments < 1 || len(c.ProofMethods) == 0 {
				return fmt.Errorf("invalid seeding clause")
			}
			if err := validateConditions(c.Conditions, fixtures); err != nil {
				return err
			}
			for _, score := range c.ScoreConditions {
				if score.TeamID == "" {
					return fmt.Errorf("missing score condition team")
				}
				if err := validateScoreConditions([]ScoreCondition{score}, score.TeamID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func filterSeedClauses(clauses []Clause, fixed map[string]clinching.Outcome) []Clause {
	out := []Clause{}
	for _, clause := range clauses {
		c := clause
		c.Conditions = []FixtureCondition{}
		valid := true
		for _, condition := range clause.Conditions {
			if value, ok := fixed[condition.GameID]; ok {
				if !slices.Contains(condition.AllowedOutcomes, value) {
					valid = false
					break
				}
			} else {
				c.Conditions = append(c.Conditions, condition)
			}
		}
		if valid {
			out = append(out, c)
		}
	}
	return out
}
