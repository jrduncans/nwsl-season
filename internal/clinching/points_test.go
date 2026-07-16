package clinching

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestFeasibleThresholdWitnessBuildsCompleteBlockingAssignment(t *testing.T) {
	p := preparedSeason{
		target:   "target",
		frontier: 10,
		points:   map[string]int{"target": 10, "a": 8, "b": 8, "c": 8},
		decision: []standings.Game{
			{ID: "a-b", HomeTeamID: "a", AwayTeamID: "b"},
			{ID: "b-c", HomeTeamID: "b", AwayTeamID: "c"},
			{ID: "c-a", HomeTeamID: "c", AwayTeamID: "a"},
		},
	}

	witness := feasibleThresholdWitness(p, 11)
	if witness.count != 3 {
		t.Fatalf("teams at threshold = %d, want 3", witness.count)
	}
	if len(witness.outcomes) != len(p.decision) {
		t.Fatalf("witness has %d outcomes, want %d", len(witness.outcomes), len(p.decision))
	}
	verified := countThresholdWitness(p, 11, witness.outcomes)
	if verified.count != witness.count {
		t.Fatalf("witness count = %d, recomputed count = %d", witness.count, verified.count)
	}
}

func TestFeasibleThresholdWitnessStopsAfterRequiredBlockers(t *testing.T) {
	p := preparedSeason{
		target: "target",
		points: map[string]int{"target": 10, "a": 8, "b": 8, "c": 8},
		decision: []standings.Game{
			{ID: "a-b", HomeTeamID: "a", AwayTeamID: "b"},
			{ID: "b-c", HomeTeamID: "b", AwayTeamID: "c"},
			{ID: "c-a", HomeTeamID: "c", AwayTeamID: "a"},
		},
	}

	witness := feasibleThresholdWitnessAtLeast(p, 11, 2)
	if witness.count < 2 {
		t.Fatalf("teams at threshold = %d, want at least 2", witness.count)
	}
	if len(witness.outcomes) != len(p.decision) {
		t.Fatalf("witness has %d outcomes, want %d", len(witness.outcomes), len(p.decision))
	}
	verified := countThresholdWitness(p, 11, witness.outcomes)
	if verified.count != witness.count {
		t.Fatalf("witness count = %d, recomputed count = %d", witness.count, verified.count)
	}
}
