package clinching

import (
	"reflect"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestEvaluateClinchedWhenNoScenarioCanPushEnoughTeamsAhead(t *testing.T) {
	teams := testTeams("target", "bravo", "charlie", "delta")
	games := []standings.Game{
		completedGame("target", "bravo", 3, 0),
		completedGame("target", "charlie", 3, 0),
		completedGame("target", "delta", 3, 0),
		remaining("last", "bravo", "charlie"),
	}

	result, err := Evaluate(teams, games, "target", 1)
	if err != nil {
		t.Fatal(err)
	}

	if !result.Clinched {
		t.Fatalf("clinched = false, result = %+v", result)
	}
	if result.Method != MethodClinched {
		t.Fatalf("method = %q, want %q", result.Method, MethodClinched)
	}
	if result.MaxTeamsAhead != 0 {
		t.Fatalf("max teams ahead = %d, want 0", result.MaxTeamsAhead)
	}
	if result.BlockingScenario != nil {
		t.Fatalf("blocking scenario = %+v, want nil", result.BlockingScenario)
	}
}

func TestEvaluateNotClinchedReturnsWitnessScenario(t *testing.T) {
	teams := testTeams("target", "bravo", "sink")
	games := []standings.Game{
		completedGame("target", "sink", 1, 0),
		remaining("bravo-sink", "bravo", "sink"),
	}

	result, err := Evaluate(teams, games, "target", 1)
	if err != nil {
		t.Fatal(err)
	}

	if result.Clinched {
		t.Fatalf("clinched = true, result = %+v", result)
	}
	if result.Method != MethodNotClinched {
		t.Fatalf("method = %q, want %q", result.Method, MethodNotClinched)
	}
	if result.BlockingScenario == nil || len(result.BlockingScenario.Games) != 1 {
		t.Fatalf("blocking scenario = %+v, want one game", result.BlockingScenario)
	}
	witness := result.BlockingScenario.Games[0]
	if witness.GameID != "bravo-sink" || witness.HomeTeamID != "bravo" || witness.HomeScore <= witness.AwayScore {
		t.Fatalf("witness = %+v, want bravo win", witness)
	}
}

func TestEvaluateHandlesCoupledRemainingFixture(t *testing.T) {
	teams := testTeams("target", "bravo", "charlie", "sink")
	games := []standings.Game{
		completedGame("target", "sink", 4, 0),
		completedGame("bravo", "sink", 1, 0),
		completedGame("charlie", "sink", 1, 0),
		completedGame("target", "sink", 0, 0),
		remaining("coupled", "bravo", "charlie"),
	}

	result, err := Evaluate(teams, games, "target", 2)
	if err != nil {
		t.Fatal(err)
	}

	if !result.Clinched {
		t.Fatalf("clinched = false, result = %+v", result)
	}
	if result.MaxTeamsAhead != 1 {
		t.Fatalf("max teams ahead = %d, want 1", result.MaxTeamsAhead)
	}
}

func TestEvaluateUsesAccessibleTiebreakersForPointsTie(t *testing.T) {
	teams := testTeams("target", "bravo", "sink")
	games := []standings.Game{
		completedGame("target", "sink", 8, 0),
		remaining("bravo-sink", "bravo", "sink"),
	}

	result, err := Evaluate(teams, games, "target", 2)
	if err != nil {
		t.Fatal(err)
	}

	if !result.Clinched {
		t.Fatalf("clinched = false, result = %+v", result)
	}
	if result.MaxTeamsAhead != 0 {
		t.Fatalf("max teams ahead = %d, want 0", result.MaxTeamsAhead)
	}
}

func TestEvaluateUsesHeadToHeadTiebreaker(t *testing.T) {
	teams := testTeams("target", "bravo", "sink")
	games := []standings.Game{
		completedGame("target", "bravo", 1, 0),
		completedGame("bravo", "sink", 1, 0),
	}

	result, err := Evaluate(teams, games, "target", 1)
	if err != nil {
		t.Fatal(err)
	}

	if !result.Clinched {
		t.Fatalf("clinched = false, result = %+v", result)
	}
	if result.MaxTeamsAhead != 0 {
		t.Fatalf("max teams ahead = %d, want 0", result.MaxTeamsAhead)
	}
}

func TestEvaluateConservativelyBlocksUnresolvedDisciplinaryTie(t *testing.T) {
	teams := testTeams("target", "bravo", "sink-1", "sink-2")
	games := []standings.Game{
		completedGame("target", "sink-1", 1, 0),
		completedGame("sink-2", "target", 1, 0),
		completedGame("bravo", "sink-1", 1, 0),
		completedGame("sink-2", "bravo", 1, 0),
	}

	result, err := Evaluate(teams, games, "target", 2)
	if err != nil {
		t.Fatal(err)
	}

	if result.Clinched {
		t.Fatalf("clinched = true, result = %+v", result)
	}
	if result.Method != MethodBlockedByDisciplinaryTieBreak {
		t.Fatalf("method = %q, want %q", result.Method, MethodBlockedByDisciplinaryTieBreak)
	}
	if result.DisciplinaryTieNote == "" {
		t.Fatalf("disciplinary tie note is empty")
	}
	if !reflect.DeepEqual(result.DisciplinaryTieTeamID, []string{"bravo"}) {
		t.Fatalf("disciplinary tie teams = %+v, want bravo", result.DisciplinaryTieTeamID)
	}
}

func TestEvaluateSimulatesTargetRemainingGamesAsLossesOnly(t *testing.T) {
	teams := testTeams("target", "bravo")
	games := []standings.Game{
		remaining("target-bravo", "target", "bravo"),
	}

	result, err := Evaluate(teams, games, "target", 1)
	if err != nil {
		t.Fatal(err)
	}

	if result.Clinched {
		t.Fatalf("clinched = true, result = %+v", result)
	}
	for _, game := range result.BlockingScenario.Games {
		if game.HomeTeamID == "target" && game.HomeScore >= game.AwayScore {
			t.Fatalf("target home result = %+v, want target loss", game)
		}
		if game.AwayTeamID == "target" && game.AwayScore >= game.HomeScore {
			t.Fatalf("target away result = %+v, want target loss", game)
		}
	}
}

func TestOptimizedEvaluateMatchesBruteForceOnSmallSchedules(t *testing.T) {
	teams := testTeams("target", "bravo", "charlie", "delta")
	scorelines := []Scoreline{{Home: 1, Away: 0}, {Home: 0, Away: 1}, {Home: 0, Away: 0}}
	fixtures := [][]standings.Game{
		{
			completedGame("target", "delta", 2, 0),
			remaining("b-c", "bravo", "charlie"),
			remaining("b-d", "bravo", "delta"),
		},
		{
			completedGame("target", "bravo", 1, 0),
			completedGame("charlie", "delta", 1, 0),
			remaining("target-charlie", "target", "charlie"),
			remaining("bravo-delta", "bravo", "delta"),
		},
		{
			completedGame("bravo", "delta", 1, 0),
			remaining("target-bravo", "target", "bravo"),
			remaining("charlie-delta", "charlie", "delta"),
		},
	}

	for _, games := range fixtures {
		optimized, err := Evaluate(teams, games, "target", 2, WithScorelines(scorelines))
		if err != nil {
			t.Fatal(err)
		}
		brute, err := Evaluate(teams, games, "target", 2, WithScorelines(scorelines), WithBruteForce())
		if err != nil {
			t.Fatal(err)
		}
		if optimized.Clinched != brute.Clinched ||
			optimized.Method != brute.Method ||
			optimized.MaxTeamsAhead != brute.MaxTeamsAhead {
			t.Fatalf("optimized = %+v, brute = %+v", optimized, brute)
		}
	}
}

func BenchmarkEvaluateLateSeason(b *testing.B) {
	teams := testTeams("target", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel")
	games := []standings.Game{
		completedGame("target", "hotel", 3, 0),
		completedGame("target", "golf", 2, 0),
		completedGame("bravo", "hotel", 2, 0),
		completedGame("charlie", "hotel", 1, 0),
		completedGame("delta", "golf", 1, 0),
		completedGame("echo", "foxtrot", 1, 1),
		completedGame("target", "foxtrot", 0, 0),
		completedGame("bravo", "golf", 0, 0),
		remaining("target-bravo", "target", "bravo"),
		remaining("charlie-delta", "charlie", "delta"),
		remaining("echo-golf", "echo", "golf"),
		remaining("foxtrot-hotel", "foxtrot", "hotel"),
		remaining("bravo-charlie", "bravo", "charlie"),
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Evaluate(teams, games, "target", 4); err != nil {
			b.Fatal(err)
		}
	}
}

func testTeams(ids ...string) []standings.Team {
	teams := make([]standings.Team, 0, len(ids))
	for _, id := range ids {
		teams = append(teams, standings.Team{ID: id, Name: id})
	}
	return teams
}

func completedGame(homeID, awayID string, homeScore, awayScore int) standings.Game {
	return standings.Game{
		Status:     standings.CompletedStatus,
		HomeTeamID: homeID,
		AwayTeamID: awayID,
		HomeScore:  intPtr(homeScore),
		AwayScore:  intPtr(awayScore),
	}
}

func remaining(id, homeID, awayID string) standings.Game {
	return standings.Game{
		ID:         id,
		Status:     "PreMatch",
		HomeTeamID: homeID,
		AwayTeamID: awayID,
	}
}
