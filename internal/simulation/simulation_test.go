package simulation

import (
	"context"
	"math"
	"math/rand"
	"os"
	"reflect"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type fixedModel struct{ score forecast.Scoreline }

func (m fixedModel) Info() forecast.Info { return forecast.Info{ID: "fixed-v1", Name: "Fixed"} }
func (m fixedModel) Fit(forecast.FitInput) (forecast.Predictor, error) {
	return fixedPredictor{score: m.score}, nil
}

type fixedPredictor struct{ score forecast.Scoreline }

func (p fixedPredictor) Distribution(standings.Game) (forecast.Distribution, error) {
	return fixedDistribution{score: p.score}, nil
}
func (p fixedPredictor) SeedMaterial() []byte { return nil }

type fixedDistribution struct{ score forecast.Scoreline }

func (d fixedDistribution) Sample(*rand.Rand) forecast.Scoreline { return d.score }
func (d fixedDistribution) Outcomes() forecast.OutcomeProbabilities {
	return forecast.OutcomeProbabilities{HomeWin: 1}
}

func TestRunSamplesSharedFixtureOnceAndUsesTotalStandings(t *testing.T) {
	result, err := Run(context.Background(), Request{
		Teams: []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}},
		Games: []standings.Game{{ID: "future", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b"}},
		Model: fixedModel{score: forecast.Scoreline{Home: 2, Away: 1}}, Iterations: 20, PlayoffPlaces: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Teams[0].Team.ID, "a"; got != want {
		t.Fatalf("leader = %q, want %q", got, want)
	}
	if result.Teams[0].ExpectedPoints != 3 || result.Teams[1].ExpectedPoints != 0 {
		t.Fatalf("points = %.1f, %.1f; want 3, 0", result.Teams[0].ExpectedPoints, result.Teams[1].ExpectedPoints)
	}
}

func TestRunAppliesFixedOutcome(t *testing.T) {
	result, err := Run(context.Background(), Request{
		Teams: []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}},
		Games: []standings.Game{{ID: "future", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b"}},
		Model: fixedModel{score: forecast.Scoreline{Home: 1, Away: 0}}, Fixed: map[string]Outcome{"future": HomeWin}, Iterations: 5, PlayoffPlaces: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FixedCount != 1 || result.Teams[0].ExpectedPoints != 3 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunSplitsUnknownFinalTie(t *testing.T) {
	result, err := Run(context.Background(), Request{
		Teams: []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}},
		Model: fixedModel{}, Iterations: 10, PlayoffPlaces: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range result.Teams {
		if row.ShieldProbability != 0.5 || row.PlayoffProbability != 0.5 || !reflect.DeepEqual(row.PositionProbability, []float64{0.5, 0.5}) {
			t.Fatalf("row = %#v, want equal unresolved-tie credit", row)
		}
	}
}

func TestRunReportsTopFourProbability(t *testing.T) {
	teams := []standings.Team{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	result, err := Run(context.Background(), Request{Teams: teams, Model: fixedModel{}, Iterations: 10, PlayoffPlaces: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range result.Teams {
		if row.TopFourProbability != .8 {
			t.Fatalf("top-four probability for %q = %.1f, want 0.8", row.Team.ID, row.TopFourProbability)
		}
	}
}

func TestRunIsDeterministicForSameSnapshot(t *testing.T) {
	request := Request{
		Teams: []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}},
		Games: []standings.Game{{ID: "future", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b"}},
		Model: fixedModel{score: forecast.Scoreline{Home: 1, Away: 0}}, Iterations: 10, PlayoffPlaces: 1,
	}
	first, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("first = %#v, second = %#v", first, second)
	}
}

func TestRunRejectsStaleFixedFixture(t *testing.T) {
	one, zero := 1, 0
	_, err := Run(context.Background(), Request{
		Teams: []standings.Team{{ID: "a"}, {ID: "b"}},
		Games: []standings.Game{{ID: "done", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "b", HomeScore: &one, AwayScore: &zero}},
		Model: fixedModel{}, Fixed: map[string]Outcome{"done": HomeWin}, Iterations: 1, PlayoffPlaces: 1,
	})
	if err == nil {
		t.Fatal("Run accepted a completed fixed fixture")
	}
}

func TestRunHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, Request{
		Teams: []standings.Team{{ID: "a"}, {ID: "b"}},
		Games: []standings.Game{{ID: "future", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b"}},
		Model: fixedModel{score: forecast.Scoreline{Home: 1, Away: 0}}, Iterations: 100, PlayoffPlaces: 1,
	})
	if err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestSeedIgnoresInputOrderAndChangesWithState(t *testing.T) {
	teams := []standings.Team{{ID: "b"}, {ID: "a"}}
	games := []standings.Game{{ID: "two", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b"}, {ID: "one", Status: RemainingStatus, HomeTeamID: "b", AwayTeamID: "a"}}
	first := Seed("model", teams, games, map[string]Outcome{"one": HomeWin})
	second := Seed("model", []standings.Team{{ID: "a"}, {ID: "b"}}, []standings.Game{games[1], games[0]}, map[string]Outcome{"one": HomeWin})
	if first != second {
		t.Fatalf("seed order changed: %d != %d", first, second)
	}
	if changed := Seed("model", teams, games, map[string]Outcome{"one": AwayWin}); changed == first {
		t.Fatal("seed did not change with fixed outcome")
	}
}

func BenchmarkRun16TeamSeason(b *testing.B) {
	teams, games := benchmarkSeason(120)
	request := Request{Teams: teams, Games: games, Model: forecast.NewResultsPoissonV1(), Iterations: 50000, PlayoffPlaces: 8}
	b.ResetTimer()
	for range b.N {
		if _, err := Run(context.Background(), request); err != nil {
			b.Fatal(err)
		}
	}
}

// TestConvergenceMeasurement is opt-in because its 200,000-iteration reference
// is intentionally too expensive for the ordinary unit-test suite.
func TestConvergenceMeasurement(t *testing.T) {
	if os.Getenv("NWSL_FORECAST_CONVERGENCE") != "1" {
		t.Skip("set NWSL_FORECAST_CONVERGENCE=1 to measure simulation convergence")
	}
	for _, cutoff := range []struct {
		name      string
		completed int
	}{{"early", 32}, {"mid", 120}, {"late", 224}} {
		teams, games := benchmarkSeason(cutoff.completed)
		base := Request{Teams: teams, Games: games, Model: forecast.NewResultsPoissonV1(), PlayoffPlaces: 8}
		referenceRequest := base
		referenceRequest.Iterations = 200000
		reference, err := Run(context.Background(), referenceRequest)
		if err != nil {
			t.Fatal(err)
		}
		for _, iterations := range []int{5000, 10000, 20000, 50000} {
			request := base
			request.Iterations = iterations
			result, err := Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			playoffs, shield, points := convergenceDifference(result, reference)
			t.Logf("cutoff=%s iterations=%d max_playoff_pp=%.3f max_shield_pp=%.3f max_expected_points=%.3f", cutoff.name, iterations, playoffs*100, shield*100, points)
		}
	}
}

func convergenceDifference(result, reference Result) (playoffs, shield, points float64) {
	byID := make(map[string]TeamResult, len(reference.Teams))
	for _, row := range reference.Teams {
		byID[row.Team.ID] = row
	}
	for _, row := range result.Teams {
		other := byID[row.Team.ID]
		playoffs = math.Max(playoffs, math.Abs(row.PlayoffProbability-other.PlayoffProbability))
		shield = math.Max(shield, math.Abs(row.ShieldProbability-other.ShieldProbability))
		points = math.Max(points, math.Abs(row.ExpectedPoints-other.ExpectedPoints))
	}
	return playoffs, shield, points
}

func benchmarkSeason(completedGames int) ([]standings.Team, []standings.Game) {
	teams := make([]standings.Team, 16)
	for index := range teams {
		teams[index] = standings.Team{ID: string(rune('a' + index)), Name: "Team " + string(rune('A'+index))}
	}
	games := make([]standings.Game, 0, 240)
	for index := 0; index < 240; index++ {
		home, away := index%16, (index*7+1)%16
		game := standings.Game{ID: "game-" + string(rune(1000+index)), HomeTeamID: teams[home].ID, AwayTeamID: teams[away].ID}
		if index < completedGames {
			homeGoals, awayGoals := index%4, (index/3)%3
			game.Status, game.HomeScore, game.AwayScore = standings.CompletedStatus, &homeGoals, &awayGoals
		} else {
			game.Status = RemainingStatus
		}
		games = append(games, game)
	}
	return teams, games
}
