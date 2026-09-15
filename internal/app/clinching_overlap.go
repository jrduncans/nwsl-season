package app

import (
	"cmp"
	"slices"
)

const (
	maxDisjointPaths        = 256
	maxDisjointSubtractions = 65536
)

// disjointRequirements assigns shared outcomes to the simplest path first.
// Each later path retains only outcomes not already shown. This runs on fixture
// masks before points compaction, which then preserves the disjoint union.
// Bound both expansion and work; on overflow retain the original exact union.
func disjointRequirements(clauses [][]scenarioRequirement) [][]scenarioRequirement {
	if len(clauses) > maxDisjointPaths {
		return clauses
	}
	ordered := slices.Clone(clauses)
	slices.SortStableFunc(ordered, func(a, b []scenarioRequirement) int {
		if order := cmp.Compare(len(a), len(b)); order != 0 {
			return order
		}
		return cmp.Compare(conjunctionKey(a), conjunctionKey(b))
	})
	kept := [][]scenarioRequirement{}
	work := 0
	for _, clause := range ordered {
		remaining := [][]scenarioRequirement{clause}
		for _, prior := range kept {
			next := [][]scenarioRequirement{}
			for _, part := range remaining {
				work++
				if work > maxDisjointSubtractions {
					return clauses
				}
				next = append(next, subtractRequirements(part, prior)...)
				if len(kept)+len(next) > maxDisjointPaths {
					return clauses
				}
			}
			remaining = next
			if len(remaining) == 0 {
				break
			}
		}
		kept = append(kept, remaining...)
	}
	return kept
}

// subtractRequirements returns source AND NOT covered as disjoint paths. For
// example, S AND NOT (A AND B) = (S AND NOT A) OR (S AND A AND NOT B).
// A missing fixture condition allows all three outcomes. Work on copies so
// narrowing one path cannot alter another or the cached proof.
func subtractRequirements(source, covered []scenarioRequirement) [][]scenarioRequirement {
	for _, want := range covered {
		if fixtureRequirementMask(source, want.GameID)&want.Mask == 0 {
			return [][]scenarioRequirement{source}
		}
	}
	remaining := slices.Clone(source)
	parts := [][]scenarioRequirement{}
	for _, want := range covered {
		mask := fixtureRequirementMask(remaining, want.GameID)
		if outside := mask &^ want.Mask; outside != 0 {
			part := withFixtureRequirement(remaining, want.GameID, outside)
			parts = append(parts, part)
		}
		remaining = withFixtureRequirement(remaining, want.GameID, mask&want.Mask)
	}
	return parts
}

func fixtureRequirementMask(conditions []scenarioRequirement, gameID string) uint8 {
	for _, r := range conditions {
		if r.GameID == gameID {
			return r.Mask
		}
	}
	return 7
}

func withFixtureRequirement(conditions []scenarioRequirement, gameID string, mask uint8) []scenarioRequirement {
	result := slices.Clone(conditions)
	for i, r := range result {
		if r.GameID == gameID {
			result[i].Mask = mask
			return result
		}
	}
	return append(result, scenarioRequirement{GameID: gameID, Mask: mask})
}
