package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestXGPoissonRecentFormWeightsOlderMatchesExponentially(t *testing.T) {
	cutoff := time.Date(2026, 7, 1, 20, 0, 0, 0, time.UTC)
	predictor, err := NewXGPoissonRecentFormV1().Fit(FitInput{
		Teams: []standings.Team{{ID: "alpha"}, {ID: "bravo"}},
		Games: []standings.Game{
			{ID: "old", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: cutoff.Add(-2 * recentFormHalfLife)},
			{ID: "latest", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: cutoff},
		},
		XGoals: map[string]ExpectedGoals{
			"old":    {GameID: "old", Home: 4, Away: 0},
			"latest": {GameID: "latest", Home: 0, Away: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	alpha := predictor.(recentFormXGPredictor).teams["alpha"]
	if math.Abs(alpha.weight-1.25) > 1e-12 || math.Abs(alpha.forGoals-1) > 1e-12 || math.Abs(alpha.against-2) > 1e-12 {
		t.Fatalf("weighted alpha totals = %+v, want weight 1.25, for 1, against 2", alpha)
	}
}

func TestXGPoissonRecentFormKeepsPriorWithoutCompletedMatches(t *testing.T) {
	predictor, err := NewXGPoissonRecentFormV1().Fit(FitInput{Teams: teams()})
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := predictor.Distribution(standings.Game{ID: "future", Status: "PreMatch", HomeTeamID: "alpha", AwayTeamID: "bravo"})
	if err != nil {
		t.Fatal(err)
	}
	poisson := distribution.(poissonDistribution)
	if poisson.homeRate != priorHomeGoals || poisson.awayRate != priorAwayGoals {
		t.Fatalf("rates = %.2f/%.2f, want priors %.2f/%.2f", poisson.homeRate, poisson.awayRate, priorHomeGoals, priorAwayGoals)
	}
}

func TestXGPoissonRecentFormSeedChangesWithKickoff(t *testing.T) {
	start := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	input := FitInput{
		Teams: []standings.Team{{ID: "alpha"}, {ID: "bravo"}},
		Games: []standings.Game{
			{ID: "done", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: start},
		},
		XGoals: map[string]ExpectedGoals{"done": {GameID: "done", Home: 1, Away: 1}},
	}
	first, err := NewXGPoissonRecentFormV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Games[0].Kickoff = start.Add(-time.Hour)
	second, err := NewXGPoissonRecentFormV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.SeedMaterial()) == string(second.SeedMaterial()) {
		t.Fatal("seed material did not change when the completed-match kickoff changed")
	}
}
