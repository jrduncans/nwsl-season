package scenarios

import (
	"context"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestMinimizeCoverageDerivesMaximalOutcomeCube(t *testing.T) {
	games := []standings.Game{{ID: "first"}, {ID: "second"}}
	certified := newAssignmentBits(9)
	// Any decisive result in the first fixture, regardless of the second.
	for first := 0; first < 3; first++ {
		for second := 0; second < 3; second++ {
			if first != 1 {
				certified.set(first*3 + second)
			}
		}
	}
	diagnostics := Diagnostics{}
	clauses, err := minimizeCoverage(context.Background(), games, certified, []clinching.ProofMethod{clinching.ProofPointsOptimization}, &diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if len(clauses) != 1 || len(clauses[0].Conditions) != 1 {
		t.Fatalf("clauses = %+v", clauses)
	}
	condition := clauses[0].Conditions[0]
	if condition.GameID != "first" || len(condition.AllowedOutcomes) != 2 || condition.AllowedOutcomes[0] != clinching.HomeWin || condition.AllowedOutcomes[1] != clinching.AwayWin {
		t.Fatalf("condition = %+v", condition)
	}
	coverage := newAssignmentBits(certified.size)
	for _, clause := range clauses {
		markCoverage(coverage, clause.Conditions, games)
	}
	if !certified.equal(coverage) {
		t.Fatalf("coverage differs: got %v want %v", coverage.words, certified.words)
	}
}

func TestMinimizeCoverageFindsOverlappingPrimeClauses(t *testing.T) {
	games := []standings.Game{{ID: "a"}, {ID: "b"}}
	certified := newAssignmentBits(9)
	// a home win OR b away win.
	for a := 0; a < 3; a++ {
		for b := 0; b < 3; b++ {
			if a == 0 || b == 2 {
				certified.set(a*3 + b)
			}
		}
	}
	clauses, err := minimizeCoverage(context.Background(), games, certified, []clinching.ProofMethod{clinching.ProofCheapBound}, &Diagnostics{})
	if err != nil {
		t.Fatal(err)
	}
	if len(clauses) != 2 {
		t.Fatalf("clauses = %+v, want two prime clauses", clauses)
	}
}

func TestSubsumesUsesOutcomeSetInclusionInTheRightDirection(t *testing.T) {
	broad := []FixtureCondition{{GameID: "a", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin, clinching.Draw}}}
	narrow := []FixtureCondition{{GameID: "a", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin}}}
	if !subsumes(broad, narrow) {
		t.Fatal("home-or-draw should subsume home-only")
	}
	if subsumes(narrow, broad) {
		t.Fatal("home-only must not subsume home-or-draw")
	}
}
