package app

import (
	"slices"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

// Match IDs break equal or unavailable kickoff ties, as in slate ordering.
// Parse both cached timestamp formats; display text never determines order.
func fixtureBefore(a, b string, games map[string]cache.Game) bool {
	x, _ := fixtures.ParseKickoff(games[a].KickoffUTC)
	y, _ := fixtures.ParseKickoff(games[b].KickoffUTC)
	if !x.Equal(y) {
		return x.Before(y)
	}
	return a < b
}

func fixtureIDsInKickoffOrder(ids []string, games map[string]cache.Game) []string {
	out := slices.Clone(ids)
	sort.SliceStable(out, func(i, j int) bool { return fixtureBefore(out[i], out[j], games) })
	return out
}

// Move whole typed requirements, including outcome masks. Scores stay within
// their conjunction; neither stored clauses nor canonical predicate keys change.
func requirementsInKickoffOrder(rs []scenarioRequirement, games map[string]cache.Game) []scenarioRequirement {
	out := slices.Clone(rs)
	first := func(r scenarioRequirement) string {
		if r.SecondGameID != "" && fixtureBefore(r.SecondGameID, r.GameID, games) {
			return r.SecondGameID
		}
		return r.GameID
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != nil || out[j].Score != nil {
			return out[i].Score == nil && out[j].Score != nil
		}
		return fixtureBefore(first(out[i]), first(out[j]), games)
	})
	return out
}

// Reorder whole requirements within a conjunction; score predicates and logical
// groups are unchanged. Kickoff order still applies within each category.
func ownRequirement(r scenarioRequirement, team string, games map[string]cache.Game) bool {
	g := games[r.GameID]
	return r.Score == nil && team != "" && (g.HomeTeamID == team || g.AwayTeamID == team)
}

func requirementsOwnFirst(rs []scenarioRequirement, team string, games map[string]cache.Game) []scenarioRequirement {
	out := requirementsInKickoffOrder(rs, games)
	sort.SliceStable(out, func(i, j int) bool {
		return ownRequirement(out[i], team, games) && !ownRequirement(out[j], team, games)
	})
	return out
}
