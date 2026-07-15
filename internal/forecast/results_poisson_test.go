package forecast

import (
	"math"
	"math/rand"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestResultsPoissonUsesPriorsWithoutCompletedGames(t *testing.T) {
	predictor, err := NewResultsPoissonV1().Fit(teams(), nil)
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := predictor.Distribution(standings.Game{ID: "future", Status: "PreMatch", HomeTeamID: "alpha", AwayTeamID: "bravo"})
	if err != nil {
		t.Fatal(err)
	}
	poisson := distribution.(poissonDistribution)
	if poisson.homeRate != priorHomeGoals || poisson.awayRate != priorAwayGoals {
		t.Fatalf("rates = %.2f, %.2f; want priors %.2f, %.2f", poisson.homeRate, poisson.awayRate, priorHomeGoals, priorAwayGoals)
	}
}

func TestResultsPoissonStrengthsAndClamp(t *testing.T) {
	goals := 20
	zero := 0
	predictor, err := NewResultsPoissonV1().Fit(teams(), []standings.Game{
		{ID: "done", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: &goals, AwayScore: &zero},
	})
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := predictor.Distribution(standings.Game{ID: "future", Status: "PreMatch", HomeTeamID: "alpha", AwayTeamID: "bravo"})
	if err != nil {
		t.Fatal(err)
	}
	poisson := distribution.(poissonDistribution)
	if poisson.homeRate != maximumRate {
		t.Fatalf("home rate = %.2f, want clamp %.2f", poisson.homeRate, maximumRate)
	}
	if poisson.awayRate <= minimumRate || poisson.awayRate >= priorAwayGoals {
		t.Fatalf("away rate = %.2f, want a shrunk value between %.2f and prior %.2f", poisson.awayRate, minimumRate, priorAwayGoals)
	}
}

func TestResultsPoissonRejectsMalformedCompletedGame(t *testing.T) {
	_, err := NewResultsPoissonV1().Fit(teams(), []standings.Game{{ID: "bad", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo"}})
	if err == nil {
		t.Fatal("Fit accepted a completed game without scores")
	}
}

func TestPoissonSampleIsDeterministicForSeed(t *testing.T) {
	distribution := poissonDistribution{homeRate: 1.5, awayRate: 1.2}
	first, second := rand.New(rand.NewSource(44)), rand.New(rand.NewSource(44))
	for range 20 {
		if got, want := distribution.Sample(first), distribution.Sample(second); got != want {
			t.Fatalf("sample = %#v, want %#v", got, want)
		}
	}
}

func TestStrengthsAreNeutralAtPrior(t *testing.T) {
	attack, defence := strengths(teamTotals{}, 1.35)
	if math.Abs(attack-1) > 1e-12 || math.Abs(defence-1) > 1e-12 {
		t.Fatalf("strengths = %.12f, %.12f; want neutral", attack, defence)
	}
}

func teams() []standings.Team {
	return []standings.Team{{ID: "alpha", Name: "Alpha"}, {ID: "bravo", Name: "Bravo"}}
}
