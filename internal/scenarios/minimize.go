package scenarios

import (
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/clinching"
)

func clauseKey(conditions []FixtureCondition) string {
	parts := make([]string, len(conditions))
	for i, condition := range conditions {
		outcomes := canonicalOutcomes(condition.AllowedOutcomes)
		values := make([]string, len(outcomes))
		for j, outcome := range outcomes {
			values[j] = string(outcome)
		}
		parts[i] = condition.GameID + "=" + strings.Join(values, "|")
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// subsumes reports whether every assignment represented by b is also
// represented by a.
func subsumes(a, b []FixtureCondition) bool {
	byGame := map[string]map[clinching.Outcome]bool{}
	for _, condition := range b {
		outcomes := map[clinching.Outcome]bool{}
		for _, outcome := range condition.AllowedOutcomes {
			outcomes[outcome] = true
		}
		byGame[condition.GameID] = outcomes
	}
	for _, condition := range a {
		outcomes, ok := byGame[condition.GameID]
		if !ok {
			return false
		}
		allowedByA := map[clinching.Outcome]bool{}
		for _, outcome := range condition.AllowedOutcomes {
			allowedByA[outcome] = true
		}
		for outcome := range outcomes {
			if !allowedByA[outcome] {
				return false
			}
		}
	}
	return true
}
