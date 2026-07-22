package scenarios

import (
	"context"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	homeMask uint8 = 1 << iota
	drawMask
	awayMask
	allMask = homeMask | drawMask | awayMask
)

// minimizeCoverage derives maximal positive cubes directly from the completed
// truth table. Unlike the older deletion/merge pass, it never calls the season
// solver again: a cube is sufficient exactly when every assignment it covers
// was already certified.
func minimizeCoverage(ctx context.Context, games []standings.Game, certified assignmentBits, methods []clinching.ProofMethod, diagnostics *Diagnostics) ([]Clause, error) {
	positive := certified.count()
	if positive == 0 {
		return []Clause{}, nil
	}
	if positive == certified.size {
		return []Clause{{Conditions: []FixtureCondition{}, RepresentedAssignments: certified.size, ProofMethods: sortedMethods(methods)}}, nil
	}

	visited := map[string]bool{}
	maximal := map[string][]uint8{}
	var expand func([]uint8) error
	expand = func(cube []uint8) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := string(cube)
		if visited[key] {
			return nil
		}
		visited[key] = true
		expanded := false
		for fixture := range cube {
			for _, bit := range []uint8{homeMask, drawMask, awayMask} {
				if cube[fixture]&bit != 0 {
					continue
				}
				next := append([]uint8(nil), cube...)
				next[fixture] |= bit
				diagnostics.MinimizationProbes++
				if cubeCertified(next, certified) {
					expanded = true
					if err := expand(next); err != nil {
						return err
					}
				}
			}
		}
		if !expanded {
			maximal[key] = append([]uint8(nil), cube...)
		}
		return nil
	}

	var expansionErr error
	certified.each(func(index int) {
		if expansionErr != nil {
			return
		}
		cube := assignmentCube(index, len(games))
		if err := expand(cube); err != nil {
			expansionErr = err
		}
	})
	if expansionErr != nil {
		return nil, expansionErr
	}

	clauses := make([]Clause, 0, len(maximal))
	proofMethods := sortedMethods(methods)
	for _, cube := range maximal {
		conditions := make([]FixtureCondition, 0, len(cube))
		represented := 1
		for i, mask := range cube {
			allowed := maskOutcomes(mask)
			represented *= len(allowed)
			if mask != allMask {
				conditions = append(conditions, FixtureCondition{GameID: games[i].ID, AllowedOutcomes: allowed})
			}
		}
		clauses = append(clauses, Clause{Conditions: conditions, RepresentedAssignments: represented, ProofMethods: append([]clinching.ProofMethod(nil), proofMethods...)})
	}
	// Defensive semantic dominance in case different expansion paths produced
	// equivalent coverage encodings.
	keep := clauses[:0]
	for i, clause := range clauses {
		dominated := false
		for j, other := range clauses {
			if i != j && subsumes(other.Conditions, clause.Conditions) && !subsumes(clause.Conditions, other.Conditions) {
				dominated = true
				break
			}
		}
		if !dominated {
			keep = append(keep, clause)
		}
	}
	sort.Slice(keep, func(i, j int) bool {
		if len(keep[i].Conditions) != len(keep[j].Conditions) {
			return len(keep[i].Conditions) < len(keep[j].Conditions)
		}
		if keep[i].RepresentedAssignments != keep[j].RepresentedAssignments {
			return keep[i].RepresentedAssignments > keep[j].RepresentedAssignments
		}
		return clauseKey(keep[i].Conditions) < clauseKey(keep[j].Conditions)
	})
	return keep, nil
}

func assignmentCube(index, fixtures int) []uint8 {
	cube := make([]uint8, fixtures)
	for i := fixtures - 1; i >= 0; i-- {
		cube[i] = []uint8{homeMask, drawMask, awayMask}[index%3]
		index /= 3
	}
	return cube
}

func cubeCertified(cube []uint8, certified assignmentBits) bool {
	var walk func(int, int) bool
	walk = func(fixture, index int) bool {
		if fixture == len(cube) {
			return certified.has(index)
		}
		for outcome, bit := range []uint8{homeMask, drawMask, awayMask} {
			if cube[fixture]&bit != 0 && !walk(fixture+1, index*3+outcome) {
				return false
			}
		}
		return true
	}
	return walk(0, 0)
}

func maskOutcomes(mask uint8) []clinching.Outcome {
	result := []clinching.Outcome{}
	if mask&homeMask != 0 {
		result = append(result, clinching.HomeWin)
	}
	if mask&drawMask != 0 {
		result = append(result, clinching.Draw)
	}
	if mask&awayMask != 0 {
		result = append(result, clinching.AwayWin)
	}
	return result
}
