package simulation

import (
	"context"
	"math"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/forecast"
)

func TestRunCountsSeedPairingsConsistentlyWithPositions(t *testing.T) {
	teams, games := benchmarkSeason(225)
	fixedID := ""
	for _, g := range games {
		if g.Status == RemainingStatus {
			fixedID = g.ID
			break
		}
	}
	pairs := [][2]int{{1, 8}, {2, 7}, {3, 6}, {4, 5}}
	result, err := Run(context.Background(), Request{Teams: teams, Games: games, Model: forecast.NewResultsPoissonV1(), Fixed: map[string]Outcome{fixedID: Draw}, Iterations: 2000, PlayoffPlaces: 8, SeedPairs: pairs})
	if err != nil {
		t.Fatal(err)
	}
	partner := map[int]int{}
	for _, p := range pairs {
		partner[p[0]], partner[p[1]] = p[1], p[0]
	}
	for _, team := range result.Teams {
		id := team.Team.ID
		bySeed := map[int]float64{}
		for _, c := range result.Seedings[id] {
			bySeed[c.Seed] += c.Probability
			if c.Seed > 0 {
				// The opponent sees the same pairing from the partner seed.
				mirror := 0.0
				for _, o := range result.Seedings[c.OpponentID] {
					if o.Seed == partner[c.Seed] && o.OpponentID == id {
						mirror += o.Probability
					}
				}
				pair := 0.0
				for _, o := range result.Seedings[id] {
					if o.Seed == c.Seed && o.OpponentID == c.OpponentID {
						pair += o.Probability
					}
				}
				if math.Abs(pair-mirror) > 1e-9 {
					t.Fatalf("%s seed %d vs %s: %.4f, mirror %.4f", id, c.Seed, c.OpponentID, pair, mirror)
				}
			}
			for game, outcome := range c.Own {
				if game == fixedID && outcome != Draw {
					t.Fatalf("fixed result counted as %s", outcome)
				}
			}
		}
		// Finishes that disciplinary points decide are left out, so a seed is
		// never likelier than the position, and nearly every season counts.
		total := 0.0
		for seed, p := range bySeed {
			total += p
			if seed > 0 && p > team.PositionProbability[seed-1]+1e-9 {
				t.Fatalf("%s seed %d: %.4f, position %.4f", id, seed, p, team.PositionProbability[seed-1])
			}
		}
		if total > 1+1e-9 || total < 0.8 {
			t.Fatalf("%s seedings total %.4f", id, total)
		}
	}
}
