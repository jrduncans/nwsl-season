package scenarios

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestGenerateFindsMinimalOutcomeOnlyClause(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}}
	game := standings.Game{ID: "g1", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"}
	slate, err := DefineSlate([]ScheduledGame{{ID: "g1", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC), Matchday: intPtr(4)}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, []standings.Game{game}, []string{"g1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(context.Background(), Request{
		Evaluator: evaluator, Teams: teams, Games: []standings.Game{game}, Slate: slate,
		TargetTeamID: "a", Achievement: competition.Achievement{ID: competition.AchievementPlayoffs, TopK: 1},
		Baseline: clinching.AchievementResult{TeamID: "a", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.NotClinched},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != OpportunityCanClinch || !result.CanClinch {
		t.Fatalf("result = %+v, want can clinch", result)
	}
	if len(result.Clauses) != 1 || len(result.Clauses[0].Conditions) != 1 {
		t.Fatalf("clauses = %+v, want one single-fixture clause", result.Clauses)
	}
	condition := result.Clauses[0].Conditions[0]
	if condition.GameID != "g1" || len(condition.AllowedOutcomes) != 1 || condition.AllowedOutcomes[0] != clinching.HomeWin {
		t.Fatalf("condition = %+v, want g1 home win", condition)
	}
	if result.TotalAssignments != 3 || result.CertifiedAssignments != 1 || result.Diagnostics.ElapsedMicroseconds <= 0 {
		t.Fatalf("result diagnostics = %+v, want completed timing and one certified assignment", result)
	}
}

func TestGenerateSearchesTiebreakOnlyUnresolvedBaseline(t *testing.T) {
	teams := []standings.Team{{ID: "target"}, {ID: "rival"}, {ID: "sink-a"}, {ID: "sink-b"}}
	zero, one := 0, 1
	games := []standings.Game{
		{ID: "target-played", Status: standings.CompletedStatus, HomeTeamID: "target", AwayTeamID: "sink-a", HomeScore: &one, AwayScore: &zero},
		{ID: "rival-played", Status: standings.CompletedStatus, HomeTeamID: "rival", AwayTeamID: "sink-b", HomeScore: &one, AwayScore: &zero},
		{ID: "slate", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "sink-a"},
	}
	slate, err := DefineSlate([]ScheduledGame{{
		ID: "slate", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "sink-a",
		KickoffUTC: time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC), Matchday: intPtr(4),
	}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, games, []string{"slate"})
	if err != nil {
		t.Fatal(err)
	}
	achievement := competition.Achievement{ID: competition.AchievementHomePlayoff, TopK: 2}
	baseline, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, nil)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Status != clinching.Unresolved || baseline.Method != clinching.ProofUnprovedScoreTiebreak {
		t.Fatalf("baseline = %+v, want unresolved score-tiebreak frontier", baseline)
	}

	result, err := Generate(context.Background(), Request{
		Evaluator: evaluator, Teams: teams, Games: games, Slate: slate,
		TargetTeamID: "target", Achievement: achievement, Baseline: baseline,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != OpportunityCanClinch || !result.CanClinch || result.CertifiedAssignments != 2 || result.UnresolvedAssignments != 1 {
		t.Fatalf("result = %+v, want win-or-draw points clinch plus one unresolved loss", result)
	}
	if len(result.Clauses) != 1 || len(result.Clauses[0].Conditions) != 1 {
		t.Fatalf("clauses = %+v, want one merged condition", result.Clauses)
	}
	condition := result.Clauses[0].Conditions[0]
	if condition.GameID != "slate" || fmt.Sprint(condition.AllowedOutcomes) != fmt.Sprint([]clinching.Outcome{clinching.HomeWin, clinching.Draw}) {
		t.Fatalf("condition = %+v, want target win or draw", condition)
	}
}

func TestGenerateFindsPlayoffEliminationClause(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}}
	game := standings.Game{ID: "g1", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"}
	slate, err := DefineSlate([]ScheduledGame{{ID: "g1", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC), Matchday: intPtr(4)}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, []standings.Game{game}, []string{"g1"})
	if err != nil {
		t.Fatal(err)
	}
	achievement := competition.Achievement{ID: competition.AchievementPlayoffs, TopK: 1}
	baseline, err := evaluator.EvaluateStatus(context.Background(), "a", achievement, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(context.Background(), Request{Evaluator: evaluator, Teams: teams, Games: []standings.Game{game}, Slate: slate, TargetTeamID: "a", Achievement: achievement, Baseline: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyEliminated || !result.CanBeEliminated {
		t.Fatalf("elimination result = %+v, want one upcoming elimination path", result)
	}
	if len(result.EliminationClauses) != 1 || len(result.EliminationClauses[0].Conditions) != 1 {
		t.Fatalf("elimination clauses = %+v, want one single-fixture clause", result.EliminationClauses)
	}
	condition := result.EliminationClauses[0].Conditions[0]
	if condition.GameID != "g1" || len(condition.AllowedOutcomes) != 1 || condition.AllowedOutcomes[0] != clinching.AwayWin {
		t.Fatalf("elimination condition = %+v, want g1 away win", condition)
	}
	if got := result.EliminationClauses[0].ProofMethods; len(got) != 1 || got[0] != clinching.ProofCheapBound {
		t.Fatalf("elimination proof methods = %+v, want cheap points bound", got)
	}
}

func TestGenerateMarksAlreadyPointsEliminatedFromPlayoffs(t *testing.T) {
	teams := []standings.Team{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	zero, one := 0, 1
	games := []standings.Game{
		{ID: "played", Status: standings.CompletedStatus, HomeTeamID: "b", AwayTeamID: "a", HomeScore: &one, AwayScore: &zero},
		{ID: "g1", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "c"},
	}
	slate, err := DefineSlate([]ScheduledGame{{ID: "g1", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "c", KickoffUTC: time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC), Matchday: intPtr(4)}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, games, []string{"g1"})
	if err != nil {
		t.Fatal(err)
	}
	achievement := competition.Achievement{ID: competition.AchievementPlayoffs, TopK: 1}
	baseline, err := evaluator.EvaluateStatus(context.Background(), "a", achievement, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(context.Background(), Request{Evaluator: evaluator, Teams: teams, Games: games, Slate: slate, TargetTeamID: "a", Achievement: achievement, Baseline: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyEliminated || result.CanBeEliminated || len(result.EliminationClauses) != 0 {
		t.Fatalf("elimination result = %+v, want already eliminated", result)
	}
}

func TestPlayoffEliminationUsesTheFullSeasonPointsCeiling(t *testing.T) {
	teams := []standings.Team{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	games := []standings.Game{
		{ID: "slate", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "c"},
		{ID: "later", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "c"},
	}
	slate, err := DefineSlate([]ScheduledGame{
		{ID: "slate", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "c", KickoffUTC: time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC), Matchday: intPtr(4)},
		{ID: "later", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "c", KickoffUTC: time.Date(2026, 8, 8, 20, 0, 0, 0, time.UTC), Matchday: intPtr(5)},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, games, []string{"slate", "later"})
	if err != nil {
		t.Fatal(err)
	}
	achievement := competition.Achievement{ID: competition.AchievementPlayoffs, TopK: 1}
	baseline, err := evaluator.EvaluateStatus(context.Background(), "a", achievement, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(context.Background(), Request{Evaluator: evaluator, Teams: teams, Games: games, Slate: slate, TargetTeamID: "a", Achievement: achievement, Baseline: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyEliminated || result.CanBeEliminated {
		t.Fatalf("elimination result = %+v, a later target win must keep elimination unproved", result)
	}
}

func TestDefineSlateUsesKickoffWindowWhenMatchdayIsUnavailable(t *testing.T) {
	start := time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC)
	slate, err := DefineSlate([]ScheduledGame{
		{ID: "near", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: start},
		{ID: "inside", Status: "PreMatch", HomeTeamID: "c", AwayTeamID: "d", KickoffUTC: start.Add(119 * time.Hour)},
		{ID: "outside", Status: "PreMatch", HomeTeamID: "e", AwayTeamID: "f", KickoffUTC: start.Add(120 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if slate.Source != SourceKickoffWindow || len(slate.FixtureIDs) != 2 || slate.FixtureIDs[0] != "near" || slate.FixtureIDs[1] != "inside" {
		t.Fatalf("slate = %+v, want deterministic 120-hour kickoff window", slate)
	}
}

func TestResultValidateRejectsEmptyConditionOutcomes(t *testing.T) {
	slate := Slate{ID: "s", DefinitionVersion: DefinitionVersion, State: SlateReady, Source: SourceKickoffWindow, StartsAtUTC: time.Unix(1, 0), LatestKickoffUTC: time.Unix(1, 0), CutoffUTC: time.Unix(2, 0), FixtureIDs: []string{"g"}}
	result := Result{TeamID: "a", Achievement: competition.AchievementPlayoffs, TopK: 1, State: OpportunityCanClinch, CanClinch: true, TotalAssignments: 3, CertifiedAssignments: 1, Clauses: []Clause{{Conditions: []FixtureCondition{{GameID: "g", AllowedOutcomes: []clinching.Outcome{}}}, RepresentedAssignments: 1, ProofMethods: []clinching.ProofMethod{clinching.ProofCheapBound}}}, Necessary: []FixtureCondition{}, ProofMethods: []clinching.ProofMethod{}}
	if err := result.Validate(slate); err == nil {
		t.Fatal("empty allowed outcomes were accepted")
	}
}

func intPtr(value int) *int { return &value }

func TestGenerateMatchesNaiveAssignmentTruthTable(t *testing.T) {
	random := rand.New(rand.NewSource(20260722))
	checked := 0
	for trial := 0; trial < 80 && checked < 25; trial++ {
		teams := []standings.Team{{ID: "target"}, {ID: "a"}, {ID: "b"}, {ID: "c"}}
		completedPairs := [][2]string{{"target", "a"}, {"b", "target"}, {"target", "c"}, {"a", "b"}, {"b", "c"}, {"c", "a"}}
		games := make([]standings.Game, 0, len(completedPairs)+3)
		for i, pair := range completedPairs {
			home, away := random.Intn(4), random.Intn(4)
			games = append(games, standings.Game{ID: fmt.Sprintf("played-%d", i), Status: standings.CompletedStatus, HomeTeamID: pair[0], AwayTeamID: pair[1], HomeScore: &home, AwayScore: &away})
		}
		pending := []standings.Game{
			{ID: "slate-0", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "a"},
			{ID: "slate-1", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "target"},
			{ID: "slate-2", Status: "PreMatch", HomeTeamID: "c", AwayTeamID: "a"},
		}
		games = append(games, pending...)
		scheduled := make([]ScheduledGame, len(pending))
		for i, game := range pending {
			scheduled[i] = ScheduledGame{ID: game.ID, Status: game.Status, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID, KickoffUTC: time.Date(2026, 8, 1, 20+i, 0, 0, 0, time.UTC), Matchday: intPtr(4)}
		}
		slate, err := DefineSlate(scheduled)
		if err != nil {
			t.Fatal(err)
		}
		order := []string{"slate-0", "slate-1", "slate-2"}
		evaluator, err := clinching.NewEvaluator(teams, games, order)
		if err != nil {
			t.Fatal(err)
		}
		achievement := competition.Achievement{ID: competition.AchievementPlayoffs, TopK: 1 + random.Intn(3)}
		baseline, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, nil)
		if err != nil {
			t.Fatal(err)
		}
		if baseline.Status != clinching.NotClinched {
			continue
		}
		checked++
		got, err := Generate(context.Background(), Request{Evaluator: evaluator, Teams: teams, Games: games, Slate: slate, TargetTeamID: "target", Achievement: achievement, Baseline: baseline})
		if err != nil {
			t.Fatal(err)
		}

		certified, unresolved := newAssignmentBits(27), newAssignmentBits(27)
		for index := 0; index < 27; index++ {
			cube := assignmentCube(index, len(pending))
			fixed := make([]clinching.FixedResult, len(pending))
			for i, game := range pending {
				fixed[i] = clinching.FixedResult{GameID: game.ID, Outcome: maskOutcomes(cube[i])[0]}
			}
			value, err := evaluator.EvaluateStatusSummary(context.Background(), "target", achievement, fixed)
			if err != nil {
				t.Fatal(err)
			}
			if publishable(value) {
				certified.set(index)
			} else if value.Status == clinching.Unresolved {
				unresolved.set(index)
			}
		}
		gotCoverage := newAssignmentBits(27)
		for _, clause := range got.Clauses {
			markCoverage(gotCoverage, clause.Conditions, pending)
		}
		if !gotCoverage.equal(certified) || got.UnresolvedAssignments != unresolved.count() {
			t.Fatalf("trial=%d optimized coverage=%v/%d naive=%v/%d", trial, gotCoverage.words, got.UnresolvedAssignments, certified.words, unresolved.count())
		}
		if certified.count() > 0 && unresolved.count() == 0 && clauseKey(got.Necessary) != clauseKey(necessary(certified, pending)) {
			t.Fatalf("trial=%d necessary=%v naive=%v", trial, got.Necessary, necessary(certified, pending))
		}
		wantClauses := naiveMaximalClauseKeys(certified, pending)
		gotKeys := make([]string, len(got.Clauses))
		for i, clause := range got.Clauses {
			gotKeys[i] = clauseKey(clause.Conditions)
		}
		sort.Strings(gotKeys)
		if fmt.Sprint(gotKeys) != fmt.Sprint(wantClauses) {
			t.Fatalf("trial=%d clauses=%v naive=%v", trial, gotKeys, wantClauses)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d not-clinched randomized states were checked", checked)
	}
}

func naiveMaximalClauseKeys(certified assignmentBits, games []standings.Game) []string {
	positive := [][]uint8{}
	var enumerate func([]uint8)
	enumerate = func(cube []uint8) {
		if len(cube) == len(games) {
			if cubeCertified(cube, certified) {
				positive = append(positive, append([]uint8(nil), cube...))
			}
			return
		}
		for mask := uint8(1); mask <= allMask; mask++ {
			enumerate(append(cube, mask))
		}
	}
	enumerate(nil)
	keys := []string{}
	for i, cube := range positive {
		maximal := true
		for j, other := range positive {
			if i == j {
				continue
			}
			broader, strict := true, false
			for fixture := range cube {
				if other[fixture]|cube[fixture] != other[fixture] {
					broader = false
					break
				}
				strict = strict || other[fixture] != cube[fixture]
			}
			if broader && strict {
				maximal = false
				break
			}
		}
		if !maximal {
			continue
		}
		conditions := []FixtureCondition{}
		for fixture, mask := range cube {
			if mask != allMask {
				conditions = append(conditions, FixtureCondition{GameID: games[fixture].ID, AllowedOutcomes: maskOutcomes(mask)})
			}
		}
		keys = append(keys, clauseKey(conditions))
	}
	sort.Strings(keys)
	return keys
}

func TestGenerateBatchMatchesIndependentAchievementSearches(t *testing.T) {
	teams := []standings.Team{{ID: "target"}, {ID: "a"}, {ID: "b"}, {ID: "c"}}
	zero, one, two := 0, 1, 2
	games := []standings.Game{
		{ID: "played-0", Status: standings.CompletedStatus, HomeTeamID: "target", AwayTeamID: "c", HomeScore: &two, AwayScore: &zero},
		{ID: "played-1", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "b", HomeScore: &one, AwayScore: &one},
		{ID: "slate-0", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "a"},
		{ID: "slate-1", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "target"},
		{ID: "slate-2", Status: "PreMatch", HomeTeamID: "c", AwayTeamID: "a"},
	}
	pending := games[2:]
	scheduled := make([]ScheduledGame, len(pending))
	for i, game := range pending {
		scheduled[i] = ScheduledGame{ID: game.ID, Status: game.Status, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID, KickoffUTC: time.Date(2026, 8, 1, 20+i, 0, 0, 0, time.UTC), Matchday: intPtr(4)}
	}
	slate, err := DefineSlate(scheduled)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, games, []string{"slate-0", "slate-1", "slate-2"})
	if err != nil {
		t.Fatal(err)
	}
	achievements := []competition.Achievement{
		{ID: competition.AchievementPlayoffs, TopK: 3},
		{ID: competition.AchievementHomePlayoff, TopK: 2},
		{ID: competition.AchievementShield, TopK: 1},
	}
	baselines := map[competition.AchievementID]clinching.AchievementResult{}
	for _, achievement := range achievements {
		baseline, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, nil)
		if err != nil {
			t.Fatal(err)
		}
		baselines[achievement.ID] = baseline
	}
	batch, err := GenerateBatch(context.Background(), BatchRequest{Evaluator: evaluator, Teams: teams, Games: games, Slate: slate, TargetTeamID: "target", Achievements: achievements, Baselines: baselines})
	if err != nil {
		t.Fatal(err)
	}
	for _, achievement := range achievements {
		if batch[achievement.ID].Diagnostics.SearchNodes > 40 || batch[achievement.ID].Diagnostics.OracleCalls > 40 {
			t.Fatalf("achievement=%s deterministic search ceiling exceeded: %+v", achievement.ID, batch[achievement.ID].Diagnostics)
		}
		single, err := Generate(context.Background(), Request{Evaluator: evaluator, Teams: teams, Games: games, Slate: slate, TargetTeamID: "target", Achievement: achievement, Baseline: baselines[achievement.ID]})
		if err != nil {
			t.Fatal(err)
		}
		if scenarioSemanticKey(batch[achievement.ID]) != scenarioSemanticKey(single) {
			t.Fatalf("achievement=%s batch=%s single=%s", achievement.ID, scenarioSemanticKey(batch[achievement.ID]), scenarioSemanticKey(single))
		}
	}
}

func TestGenerateDiscardsPartialCoverageAfterCancellation(t *testing.T) {
	teams := []standings.Team{{ID: "target"}, {ID: "a"}}
	game := standings.Game{ID: "g", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "a"}
	slate, err := DefineSlate([]ScheduledGame{{ID: "g", Status: "PreMatch", HomeTeamID: "target", AwayTeamID: "a", KickoffUTC: time.Date(2026, 8, 1, 20, 0, 0, 0, time.UTC), Matchday: intPtr(4)}})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, []standings.Game{game}, []string{"g"})
	if err != nil {
		t.Fatal(err)
	}
	achievement := competition.Achievement{ID: competition.AchievementShield, TopK: 1}
	baseline, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Generate(ctx, Request{Evaluator: evaluator, Teams: teams, Games: []standings.Game{game}, Slate: slate, TargetTeamID: "target", Achievement: achievement, Baseline: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != OpportunityUnresolved || result.Limitation != "scenario computation budget exhausted" || len(result.Clauses) != 0 || result.CertifiedAssignments != 0 {
		t.Fatalf("canceled result published partial work: %+v", result)
	}
}

func scenarioSemanticKey(result Result) string {
	clauses := make([]string, len(result.Clauses))
	for i, clause := range result.Clauses {
		clauses[i] = clauseKey(clause.Conditions)
	}
	sort.Strings(clauses)
	elimination := make([]string, len(result.EliminationClauses))
	for i, clause := range result.EliminationClauses {
		elimination[i] = clauseKey(clause.Conditions)
	}
	sort.Strings(elimination)
	return fmt.Sprintf("%s|%t|%t|%d|%d|%s|%v|%t|%t|%v|%s", result.State, result.AlreadyClinched, result.CanClinch, result.CertifiedAssignments, result.UnresolvedAssignments, clauseKey(result.Necessary), clauses, result.AlreadyEliminated, result.CanBeEliminated, elimination, result.Limitation)
}
