package clinching

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestCutoffDecisionMatchesMaximumOracle(t *testing.T) {
	random := rand.New(rand.NewSource(20260721))
	teamIDs := []string{"target", "a", "b", "c", "d"}
	allGames := []standings.Game{
		{ID: "a-b", HomeTeamID: "a", AwayTeamID: "b"},
		{ID: "b-c", HomeTeamID: "b", AwayTeamID: "c"},
		{ID: "c-d", HomeTeamID: "c", AwayTeamID: "d"},
		{ID: "d-a", HomeTeamID: "d", AwayTeamID: "a"},
		{ID: "a-c", HomeTeamID: "a", AwayTeamID: "c"},
		{ID: "b-d", HomeTeamID: "b", AwayTeamID: "d"},
	}
	for trial := 0; trial < 200; trial++ {
		points := map[string]int{}
		for _, id := range teamIDs {
			points[id] = random.Intn(10)
		}
		gameCount := random.Intn(len(allGames) + 1)
		season := preparedSeason{target: "target", points: points, decision: append([]standings.Game(nil), allGames[:gameCount]...), witness: map[string]WitnessGame{}}
		for threshold := 1; threshold <= 12; threshold++ {
			maximum, err := solveThresholdMaximumOracle(context.Background(), season, threshold)
			if err != nil {
				t.Fatal(err)
			}
			for k := 1; k < len(teamIDs); k++ {
				decision, err := solveCutoff(context.Background(), season, threshold, k)
				if err != nil {
					t.Fatal(err)
				}
				want := maximum.count >= k
				if decision.feasible != want {
					t.Fatalf("trial=%d games=%d threshold=%d k=%d feasible=%v want=%v points=%v maximum=%d", trial, gameCount, threshold, k, decision.feasible, want, points, maximum.count)
				}
				if decision.feasible {
					verified := countThresholdWitness(season, threshold, decision.outcomes)
					if verified.count < k || len(decision.outcomes) != len(season.decision) {
						t.Fatalf("invalid cutoff witness: count=%d outcomes=%d games=%d", verified.count, len(decision.outcomes), len(season.decision))
					}
				}
			}
		}
	}
}

func TestNoHelpUsesShortestWinningPrefix(t *testing.T) {
	teams := []standings.Team{{ID: "target"}, {ID: "a"}, {ID: "b"}}
	zero, one := 0, 1
	games := []standings.Game{
		{ID: "played", Status: standings.CompletedStatus, HomeTeamID: "target", AwayTeamID: "a", HomeScore: &one, AwayScore: &zero},
		{ID: "target-a", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "a"},
		{ID: "target-b", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "b"},
		{ID: "a-b", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"},
	}
	evaluator, err := NewEvaluator(teams, games, []string{"target-a", "target-b", "a-b"})
	if err != nil {
		t.Fatal(err)
	}
	achievement := competition.Achievement{ID: "test", Label: "Test", TopK: 1}
	base, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, nil)
	if err != nil {
		t.Fatal(err)
	}
	path, err := evaluator.EvaluateNoHelp(context.Background(), "target", achievement, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	if path.State != NoHelpGuaranteed || fmt.Sprint(path.FixtureIDs) != "[target-a target-b]" {
		t.Fatalf("no-help path = %+v", path)
	}
	firstOnly, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, []FixedResult{{GameID: "target-a", Outcome: HomeWin}})
	if err != nil {
		t.Fatal(err)
	}
	if firstOnly.Status == Clinched {
		t.Fatalf("one-win prefix unexpectedly clinched: %+v", firstOnly)
	}
}

func TestEvaluatorCacheKeepsLiteralFixedWitness(t *testing.T) {
	teams := []standings.Team{{ID: "target"}, {ID: "a"}, {ID: "b"}}
	games := []standings.Game{
		{ID: "one", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"},
		{ID: "two", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"},
	}
	evaluator, err := NewEvaluator(teams, games, []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	achievement := competition.Achievement{ID: "test", Label: "Test", TopK: 1}
	first := []FixedResult{{GameID: "one", Outcome: HomeWin}, {GameID: "two", Outcome: AwayWin}}
	second := []FixedResult{{GameID: "one", Outcome: AwayWin}, {GameID: "two", Outcome: HomeWin}}
	if _, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, first); err != nil {
		t.Fatal(err)
	}
	value, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, second)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Outcome{}
	for _, witness := range value.BlockingWitness {
		got[witness.GameID] = witness.Outcome
	}
	if got["one"] != AwayWin || got["two"] != HomeWin {
		t.Fatalf("cached witness outcomes = %v", got)
	}
}

func TestUniversalSlateBlockerIgnoresEverySlateOutcome(t *testing.T) {
	teams := []standings.Team{{ID: "target"}, {ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	zero, one := 0, 1
	games := []standings.Game{
		{ID: "a-c", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "c", HomeScore: &one, AwayScore: &zero},
		{ID: "a-d", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "d", HomeScore: &one, AwayScore: &zero},
		{ID: "b-c", Status: standings.CompletedStatus, HomeTeamID: "b", AwayTeamID: "c", HomeScore: &one, AwayScore: &zero},
		{ID: "b-d", Status: standings.CompletedStatus, HomeTeamID: "b", AwayTeamID: "d", HomeScore: &one, AwayScore: &zero},
		{ID: "slate", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "c"},
	}
	evaluator, err := NewEvaluator(teams, games, []string{"slate"})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := evaluator.NewSlateBlocker("target", []string{"slate"})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := blocker.Blocks(context.Background(), competition.Achievement{ID: "playoffs", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("expected a post-slate blocker for every slate outcome")
	}
	blocked, err = blocker.Blocks(context.Background(), competition.Achievement{ID: "shield", TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("a playoff blocker must also block the smaller cutoff")
	}
}

func TestUniversalSlateBlockerLeavesPossibleSlateForScenarioSearch(t *testing.T) {
	teams := []standings.Team{{ID: "target"}, {ID: "a"}}
	game := standings.Game{ID: "slate", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "a"}
	evaluator, err := NewEvaluator(teams, []standings.Game{game}, []string{"slate"})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := evaluator.HasUniversalSlateBlocker(context.Background(), "target", competition.Achievement{ID: "test", TopK: 1}, []string{"slate"})
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatal("a possible slate clinch was incorrectly blocked")
	}
}

func TestBinaryNoHelpMatchesLinearPrefixEnumeration(t *testing.T) {
	random := rand.New(rand.NewSource(20260722))
	for trial := 0; trial < 100; trial++ {
		teams := []standings.Team{{ID: "target"}, {ID: "a"}, {ID: "b"}, {ID: "c"}}
		pairs := [][2]string{{"target", "a"}, {"b", "target"}, {"target", "c"}, {"a", "b"}, {"b", "c"}, {"c", "a"}}
		games := make([]standings.Game, len(pairs))
		order := []string{}
		for i, pair := range pairs {
			games[i] = standings.Game{ID: fmt.Sprintf("g-%d", i), Status: "PreMatch", HomeTeamID: pair[0], AwayTeamID: pair[1]}
			if random.Intn(3) == 0 {
				home, away := random.Intn(4), random.Intn(4)
				games[i].Status, games[i].HomeScore, games[i].AwayScore = standings.CompletedStatus, &home, &away
			} else {
				order = append(order, games[i].ID)
			}
		}
		if len(order) == 0 {
			continue
		}
		evaluator, err := NewEvaluator(teams, games, order)
		if err != nil {
			t.Fatal(err)
		}
		achievement := competition.Achievement{ID: "test", Label: "Test", TopK: 1 + random.Intn(3)}
		base, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, nil)
		if err != nil {
			t.Fatal(err)
		}
		if base.Status != NotClinched {
			continue
		}
		got, err := evaluator.EvaluateNoHelp(context.Background(), "target", achievement, nil, base)
		if err != nil {
			t.Fatal(err)
		}

		wins := []FixedResult{}
		fixtureIDs := []string{}
		for _, id := range order {
			game := evaluator.gamesByID[id]
			if game.HomeTeamID != "target" && game.AwayTeamID != "target" {
				continue
			}
			outcome := AwayWin
			if game.HomeTeamID == "target" {
				outcome = HomeWin
			}
			wins = append(wins, FixedResult{GameID: id, Outcome: outcome})
			fixtureIDs = append(fixtureIDs, id)
		}
		want := NoHelpPath{State: NoHelpImpossible, FixtureIDs: []string{}}
		for prefix := 1; prefix <= len(wins); prefix++ {
			value, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, wins[:prefix])
			if err != nil {
				t.Fatal(err)
			}
			if prefix == len(wins) && value.Status == Unresolved {
				want = NoHelpPath{State: NoHelpUnresolved, FixtureIDs: []string{}, Reason: value.Reason}
			}
			if value.Status == Clinched {
				want = NoHelpPath{State: NoHelpGuaranteed, FixtureIDs: append([]string(nil), fixtureIDs[:prefix]...)}
				break
			}
		}
		if got.State != want.State || fmt.Sprint(got.FixtureIDs) != fmt.Sprint(want.FixtureIDs) {
			t.Fatalf("trial=%d topK=%d no-help=%+v, linear=%+v", trial, achievement.TopK, got, want)
		}
	}
}
