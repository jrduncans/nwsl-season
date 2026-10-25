package app

import (
	"slices"
	"sort"
)

const maxFixtureMergePaths = 256

// Merge results of one fixture only when every other requirement is identical:
// (A AND win) OR (A AND draw) = A AND (win OR draw). Score and points conditions
// stay opaque. Each merge reduces the path count, bounding repeated passes.
func compactFixtureMasks(clauses [][]scenarioRequirement) [][]scenarioRequirement {
	if len(clauses) > maxFixtureMergePaths {
		return clauses
	}
	ids := []string{}
	for _, clause := range clauses {
		for _, r := range clause {
			if r.Score == nil && r.SecondGameID == "" && !slices.Contains(ids, r.GameID) {
				ids = append(ids, r.GameID)
			}
		}
	}
	sort.Strings(ids)
	for {
		before := len(clauses)
		for _, id := range ids {
			merged := [][]scenarioRequirement{}
			indices := map[string]int{}
			for _, clause := range clauses {
				rest := []scenarioRequirement{}
				mask := uint8(7)
				for _, r := range clause {
					if r.Score == nil && r.SecondGameID == "" && r.GameID == id {
						mask &= r.Mask
					} else {
						rest = append(rest, r)
					}
				}
				key := conjunctionKey(rest)
				if index, ok := indices[key]; ok {
					for j, r := range merged[index] {
						if r.Score != nil || r.SecondGameID != "" || r.GameID != id {
							continue
						}
						combined := slices.Clone(merged[index])
						combined[j].Mask |= mask
						if combined[j].Mask == 7 {
							combined = slices.Delete(combined, j, j+1)
						}
						merged[index] = combined
						break
					}
				} else {
					indices[key] = len(merged)
					merged = append(merged, clause)
				}
			}
			clauses = merged
		}
		if len(clauses) == before {
			return clauses
		}
	}
}
