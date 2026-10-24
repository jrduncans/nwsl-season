package simulation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// SeedingCount is the share of simulated seasons in which a team finished in
// Seed (zero outside the bracket) against OpponentID, with Own as its results
// in its remaining matches, fixed ones included.
type SeedingCount struct {
	Seed        int
	OpponentID  string
	Own         map[string]Outcome
	Probability float64
}

type seedingKey struct {
	team, opponent, own string
	seed                int
}

// seedingCounter tallies first-round pairings from the bracket's seed pairs.
// A finish that disciplinary points decide is not counted.
type seedingCounter struct {
	partners map[int]int
	places   int
	own      map[string][]int
	counts   map[seedingKey]float64
}

func newSeedingCounter(pairs [][2]int, teams []standings.Team, remaining []preparedFixture) (*seedingCounter, error) {
	c := &seedingCounter{partners: map[int]int{}, own: map[string][]int{}, counts: map[seedingKey]float64{}}
	for _, p := range pairs {
		if p[0] < 1 || p[1] < 1 || p[0] == p[1] || p[0] > len(teams) || p[1] > len(teams) || c.partners[p[0]] != 0 || c.partners[p[1]] != 0 {
			return nil, fmt.Errorf("invalid seed pair %v", p)
		}
		c.partners[p[0]], c.partners[p[1]] = p[1], p[0]
	}
	c.places = len(c.partners)
	for i := 1; i <= c.places; i++ {
		if c.partners[i] == 0 {
			return nil, fmt.Errorf("incomplete seed pairs")
		}
	}
	for i, fixture := range remaining {
		c.own[fixture.game.HomeTeamID] = append(c.own[fixture.game.HomeTeamID], i)
		c.own[fixture.game.AwayTeamID] = append(c.own[fixture.game.AwayTeamID], i)
	}
	return c, nil
}

// add counts one table. outcomes holds each remaining fixture's result, in
// the prepared order.
func (c *seedingCounter) add(table []standings.TableRow, outcomes []Outcome, weight float64) {
	for index, row := range table {
		if row.TieBreak.Undetermined {
			continue
		}
		key := seedingKey{team: row.Team.ID}
		if index < c.places {
			key.seed = index + 1
			opponent := table[c.partners[key.seed]-1]
			if opponent.TieBreak.Undetermined {
				continue
			}
			key.opponent = opponent.Team.ID
		}
		own := make([]byte, 0, len(c.own[key.team]))
		for _, i := range c.own[key.team] {
			own = append(own, outcomes[i][0])
		}
		key.own = string(own)
		c.counts[key] += weight
	}
}

func (c *seedingCounter) result(remaining []preparedFixture, iterations int) map[string][]SeedingCount {
	out := map[string][]SeedingCount{}
	for key, weight := range c.counts {
		own := map[string]Outcome{}
		for j, i := range c.own[key.team] {
			own[remaining[i].game.ID] = Outcome(key.own[j : j+1])
		}
		out[key.team] = append(out[key.team], SeedingCount{Seed: key.seed, OpponentID: key.opponent, Own: own, Probability: weight / float64(iterations)})
	}
	for team := range out {
		rows := out[team]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Seed != rows[j].Seed {
				return rows[i].Seed < rows[j].Seed
			}
			if rows[i].OpponentID != rows[j].OpponentID {
				return rows[i].OpponentID < rows[j].OpponentID
			}
			return ownKey(rows[i].Own) < ownKey(rows[j].Own)
		})
	}
	return out
}

func ownKey(own map[string]Outcome) string {
	parts := make([]string, 0, len(own))
	for id, o := range own {
		parts = append(parts, id+"="+string(o))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func gameOutcome(game standings.Game) Outcome {
	switch {
	case *game.HomeScore > *game.AwayScore:
		return HomeWin
	case *game.HomeScore < *game.AwayScore:
		return AwayWin
	default:
		return Draw
	}
}
