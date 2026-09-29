package simulation

import (
	"context"
	"math"
	"math/rand"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func testPlayoffTeams() []standings.Team {
	teams := make([]standings.Team, 8)
	for i := range teams {
		teams[i] = standings.Team{ID: string(rune('a' + i))}
	}
	return teams
}

func testPlayoffBracket(t *testing.T) *competition.BracketFormat {
	t.Helper()
	entry, ok := competition.Lookup("2026", "Playoffs")
	if !ok || entry.BracketFormat == nil {
		t.Fatal("2026 playoff bracket unavailable")
	}
	return entry.BracketFormat
}

func TestPlayoffBracketAdvancesHigherSeedsAndTreatsFinalAsNeutral(t *testing.T) {
	teams := testPlayoffTeams()
	predictor, err := (fixedModel{score: forecast.Scoreline{Home: 1}}).Fit(forecast.FitInput{})
	if err != nil {
		t.Fatal(err)
	}
	playoffs, err := preparePlayoffs(Request{Teams: teams, PlayoffPlaces: 8, PlayoffBracket: testPlayoffBracket(t)}, predictor)
	if err != nil {
		t.Fatal(err)
	}
	table := make([]standings.TableRow, len(teams))
	for i, team := range teams {
		table[i].Team = team
	}
	rng := rand.New(rand.NewSource(42)) // #nosec G404 -- reproducible test sampling.
	counts := map[string]int{}
	for range 1000 {
		counts[playoffs.champion(table, rng)]++
	}
	if counts["a"] < 400 || counts["b"] < 400 || len(counts) != 2 {
		t.Fatalf("champions = %v; expected the two bracket finalists to split the neutral final", counts)
	}
}

func TestRunChampionshipOddsSumToOneAndRespectPlayoffQualification(t *testing.T) {
	teams := testPlayoffTeams()
	request := Request{Teams: teams, Model: forecast.NewXGPoissonScheduleLoadV1(), Iterations: 200, PlayoffPlaces: 8, PlayoffBracket: testPlayoffBracket(t)}
	first, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ChampionshipSimulated || first.Seed != second.Seed {
		t.Fatalf("forecast metadata = %+v", first)
	}
	total := 0.0
	for i, row := range first.Teams {
		if row.ChampionshipProbability != second.Teams[i].ChampionshipProbability {
			t.Fatal("same snapshot produced different championship odds")
		}
		if row.ChampionshipProbability > row.PlayoffProbability {
			t.Fatalf("%s championship chance exceeds playoff chance", row.Team.ID)
		}
		total += row.ChampionshipProbability
	}
	if math.Abs(total-1) > 1e-9 {
		t.Fatalf("championship probabilities sum to %f, want 1", total)
	}
}

type drawDistribution struct{}

func (drawDistribution) Sample(*rand.Rand) forecast.Scoreline { return forecast.Scoreline{} }
func (drawDistribution) Outcomes() forecast.OutcomeProbabilities {
	return forecast.OutcomeProbabilities{HomeWin: .6, Draw: .2, AwayWin: .2}
}

func TestPlayoffDrawAdvancementUsesRelativeWinChances(t *testing.T) {
	playoffs := playoffSimulation{matches: map[playoffPair]playoffMatch{{"a", "b"}: {distribution: drawDistribution{}, tieHomeChance: .75}}}
	rng := rand.New(rand.NewSource(7)) // #nosec G404 -- reproducible test sampling.
	homeWins := 0
	for range 1000 {
		if playoffs.winner("a", "b", false, rng) == "a" {
			homeWins++
		}
	}
	if homeWins < 700 || homeWins > 800 {
		t.Fatalf("home wins = %d, want approximately 750", homeWins)
	}
}

func TestRunRejectsMismatchedPlayoffBracket(t *testing.T) {
	_, err := Run(context.Background(), Request{Teams: testPlayoffTeams(), Model: fixedModel{}, Iterations: 1, PlayoffPlaces: 4, PlayoffBracket: testPlayoffBracket(t)})
	if err == nil {
		t.Fatal("accepted an eight-seed bracket with four playoff places")
	}
}
