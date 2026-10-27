package app

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestSeedingConditionalOddsUseUnroundedShares(t *testing.T) {
	row := seedingRowView{Likelihood: "<1%", probability: .004, opponentIDs: []string{"a", "b"}, opponentLikelihoods: map[string]float64{"a": .001, "b": .003}}
	if row.opponentLikelihood("a", true) != "25%" || row.opponentLikelihood("b", true) != "75%" {
		t.Fatal("conditional odds must divide unrounded joint shares by the unrounded finish share")
	}
	if row.opponentLikelihood("a", false) != "" {
		t.Fatal("unavailable forecast invented odds")
	}
	row.probability = 0
	if row.opponentLikelihood("a", true) != "" {
		t.Fatal("zero-sample finish invented conditional odds")
	}
	if likelihoodText(0, true) != "possible; odds unavailable" {
		t.Fatal("a lone possible finish does not prove any forecast samples")
	}
	row.probability, row.opponentIDs = .004, []string{"a"}
	row.opponentLikelihoods["a"] = .004
	row.opponentLikelihoods["b"] = 0
	if row.opponentLikelihood("b", true) != "possible; odds unavailable" {
		t.Fatal("unsampled opponent needs audience-facing wording")
	}
	if row.opponentLikelihood("a", true) != "100%" {
		t.Fatal("sole opponent is conditional on even a rare finish")
	}
}

func TestSeedingSummariesUseCompletePerGroupConditions(t *testing.T) {
	labels := map[string]string{"a": "AAA", "b": "BBB", "c": "CCC", "d": "DDD"}
	games := map[string]cache.Game{"own": {HomeTeamID: "a", AwayTeamID: "b"}, "other": {HomeTeamID: "c", AwayTeamID: "d"}}
	clauses := []scenarios.Clause{}
	for i := range 300 {
		clause := seedingTestClause(testScenarioCondition("own", 1))
		clause.ScoreConditions = []scenarios.ScoreCondition{{TeamID: "a", RivalID: "c", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: i + 1}}}}
		clauses = append(clauses, clause)
	}
	// This is one complete conjunction, despite the much larger other group and
	// the many assignments represented by its generalized result mask.
	draw := seedingTestClause(testScenarioCondition("own", 2), testScenarioCondition("other", 3))
	draw.RepresentedAssignments = 1000
	clauses = append(clauses, draw)
	result := scenarios.SeedingResult{Limitation: scenarios.LimitationBudgetPartial, Seeds: []scenarios.SeedFinish{{Seed: 6, Clauses: clauses}}}
	view := seedingConditions(result, 6, seedingOwnMatches{team: "a"}, labels, games)
	if len(view.Groups) != 2 || !view.Groups[0].SummaryOverflow || view.Groups[0].NoHelp || view.Groups[1].SummaryOverflow {
		t.Fatalf("group completeness lost: %+v", view.Groups)
	}
	path := view.Groups[1].Summary.Combinations()
	if len(path) != 1 || len(path[0]) != 1 || path[0][0].GameID != "other" || path[0][0].Mask != 3 {
		t.Fatalf("complete conjunction changed: %+v", path)
	}
	if guaranteedSeedingFinish(result) != nil {
		t.Fatal("partial search with one listed finish is not an inevitability proof")
	}
	noHelp := seedingConditions(scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Seed: 1, Clauses: []scenarios.Clause{seedingTestClause(testScenarioCondition("own", 1))}}}}, 1, seedingOwnMatches{team: "a"}, labels, games)
	if !noHelp.Groups[0].NoHelp || noHelp.GuaranteeState != "Guarantees 1st place" {
		t.Fatal("missing no-help guarantee")
	}
	overflow := seedingOverflow([][]scenarioRequirement{{{GameID: "other", Mask: 1}}}, labels, games)
	plain := seedingTestClause(testScenarioCondition("own", 1))
	redundant := plain
	redundant.ScoreConditions = clauses[0].ScoreConditions
	certain := seedingConditions(scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Seed: 1, Clauses: []scenarios.Clause{plain, redundant}}}}, 1, seedingOwnMatches{team: "a"}, labels, games)
	if !certain.Groups[0].NoHelp || certain.Groups[0].Overflow != nil {
		t.Fatal("a covered score alternative should not add a tiebreak caveat")
	}
	if len(overflow.MatchParts) != 0 {
		t.Fatal("fixture repeated after a complete visible requirement")
	}
}

func TestSeedingOneOversizedPathKeepsItsQualification(t *testing.T) {
	labels := map[string]string{"a": "AAA", "b": "BBB", "c": "CCC"}
	games := map[string]cache.Game{"own": {HomeTeamID: "a", AwayTeamID: "b"}}
	clause := seedingTestClause(testScenarioCondition("own", 1))
	for i := range 31 {
		clause.ScoreConditions = append(clause.ScoreConditions, scenarios.ScoreCondition{TeamID: "a", RivalID: "c", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: i + 1}}})
	}
	conditions := seedingConditions(scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Seed: 1, Clauses: []scenarios.Clause{clause}}}}, 1, seedingOwnMatches{team: "a"}, labels, games)
	if len(conditions.Groups) != 1 || !conditions.Groups[0].SummaryOverflow || conditions.Groups[0].Overflow.Ways != 1 || conditions.Groups[0].NoHelp {
		t.Fatalf("oversized conjunction treated as a complete preview: %+v", conditions)
	}
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	var buf bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&buf, "clinchingSeeding", clinchingPage{Seeding: &seedingPage{Conditions: conditions}}); err != nil {
		t.Fatal(err)
	}
	requireText(t, buf.String(), "#seeding-conditions", "in 1 way", "This summary alone does not guarantee the finish.")
	forbidRaw(t, buf.String(), "1 ways", "Other results don’t matter")
}

func TestSeedingInevitableOutcomesAndAssumptions(t *testing.T) {
	for _, assumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconditional", true: "assumed"}[assumed], func(t *testing.T) {
			miss := seedingTestClause()
			if assumed {
				miss = seedingTestClause(testScenarioCondition("future-4", 4))
			}
			snapshot := seasonResult()
			snapshot.Results[0].Seeding = &scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Clauses: []scenarios.Clause{miss}, PossibleResults: []scenarios.Clause{miss}}}}
			store := seasonScenarioStore(seedingTestData(), []string{"future-4"}, []string{"future-4", "future-5"}, snapshot)
			query := "?view=seeding&team=ALP"
			if assumed {
				query += "&p=future-4:a"
			}
			body := getClinching(t, store, query)
			requireText(t, body, "#seeding-conditions-title", "Eliminated")
			forbidRaw(t, body, "seeding-most-likely", "See scenarios for", "All finishes ↑", "seeding-score-guidance", `<table class="seeding-grid">`)
			if assumed {
				requireText(t, body, "#seeding-conditions", "Eliminated under these assumptions", "Assuming:")
			} else {
				requireExact(t, body, "#seeding-conditions-title", "Eliminated")
				forbidText(t, body, "#seeding-conditions", "100%", "regardless of remaining results", "ALP")
				forbidText(t, body, "#seeding-conditions", "under these assumptions")
			}
			// Old detail URLs for an inevitable outcome return the concise answer.
			detail := getClinching(t, store, query+"&finish=miss&alternative=1")
			forbidRaw(t, detail, `id="seeding-alternative"`, "More scenarios")
		})
	}
}

func TestSeedingFixedOwnResultWordingAndUnavailableOdds(t *testing.T) {
	clause := seedingTestClause(testScenarioCondition("future-4", 1), testScenarioCondition("future-5", 1))
	snapshot := seasonResult()
	snapshot.Results[0].Seeding = &scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Seed: 1, OpponentSeed: 2, Clauses: []scenarios.Clause{clause}, Opponents: []scenarios.SeedOpponent{{TeamID: "bravo", PossibleResults: []scenarios.Clause{clause}}}}}}
	data := seedingTestData()
	data.Teams = append(data.Teams, standings.Team{ID: "charlie", Name: "Charlie FC", Abbreviation: "CHA"})
	for i := range data.Games {
		if data.Games[i].ASAID == "future-5" {
			data.Games[i].HomeTeamID = "charlie"
		}
	}
	store := seasonScenarioStore(data, []string{"future-4"}, []string{"future-4", "future-5"}, snapshot)
	body := getClinching(t, store, "?view=seeding&team=ALP&finish=1&p=future-4:h")
	requireText(t, body, ".seeding-fixed-results summary", "Assume match results")
	requireText(t, body, "#seeding-conditions", "With these assumptions", "Assuming:", "Opponent odds are unavailable.")
	forbidText(t, body, "#seeding-conditions", "With any results")
	forbidRaw(t, body, "seeding-score-guidance")
	detail := getClinching(t, store, "?view=seeding&team=ALP&finish=1&p=future-4:h&alternative=1")
	requireAttr(t, detail, ".seeding-assumptions a", "href", "clinching?finish=1&p=future-4%3Ah&team=ALP&view=seeding#forecast-builder")
}

func TestSeedingScenarioPreviewsLeadWithOwnResult(t *testing.T) {
	games := map[string]cache.Game{
		"early": {HomeTeamID: "c", AwayTeamID: "d", KickoffUTC: "2026-10-01T19:00:00Z"},
		"own":   {HomeTeamID: "b", AwayTeamID: "a", KickoffUTC: "2026-10-02T19:00:00Z"},
	}
	labels := map[string]string{"a": "AAA", "b": "BBB", "c": "CCC", "d": "DDD"}
	clauses := []scenarios.Clause{}
	for _, mask := range []uint8{1, 2} {
		clauses = append(clauses, seedingTestClause(testScenarioCondition("early", mask), testScenarioCondition("own", 4)))
	}
	view, err := seedingAlternatives(scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Seed: 1, Clauses: clauses}}}, 1, "a", url.Values{"alternative": {"1"}}, labels, games, nil, scenarioHistory{})
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range view.Scenarios {
		if card.Conditions[0].GameID != "own" || !strings.HasPrefix(card.Preview, "AAA wins at BBB;") {
			t.Fatalf("own result not first: %+v", card)
		}
	}
}

func TestSeedingScoreGuidanceAbsentWithoutScoreConditions(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	var buf bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&buf, "clinchingSeeding", clinchingPage{Seeding: &seedingPage{Conditions: &seedingConditionsView{}}}); err != nil {
		t.Fatal(err)
	}
	forbidRaw(t, buf.String(), "seeding-score-guidance")
}

func TestSeedingFocusOnlyForMultipleColumns(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	for _, columns := range []int{1, 2} {
		var buf bytes.Buffer
		view := clinchingPage{Seeding: &seedingPage{Columns: make([]seedingColumnView, columns), Rows: []seedingRowView{{Heading: "6th"}}}}
		if err := application.app.pages.ExecuteTemplate(&buf, "clinchingSeeding", view); err != nil {
			t.Fatal(err)
		}
		if columns == 1 {
			forbidRaw(t, buf.String(), "seeding-focus-control", "seeding-focus-help", "Display only;")
		} else {
			requireText(t, buf.String(), ".seeding-focus-control", "Team results", "Display only; assumptions and odds stay the same.")
		}
	}
}

func TestSeedingAdditionalGroupsSayAndOneOf(t *testing.T) {
	labels := map[string]string{"a": "AAA", "b": "BBB", "c": "CCC", "d": "DDD", "e": "EEE", "f": "FFF"}
	games := map[string]cache.Game{"one": {HomeTeamID: "a", AwayTeamID: "b"}, "two": {HomeTeamID: "c", AwayTeamID: "d"}, "three": {HomeTeamID: "e", AwayTeamID: "f"}}
	paths := [][]scenarioRequirement{}
	for _, first := range []uint8{1, 2} {
		for _, second := range []uint8{1, 2} {
			paths = append(paths, []scenarioRequirement{{GameID: "one", Mask: 1}, {GameID: "two", Mask: first}, {GameID: "three", Mask: second}})
		}
	}
	// Different score predicates preserve the alternatives in each independent group.
	for i := range paths {
		paths[i] = append(paths[i], scenarioRequirement{Score: &scenarios.ScoreCondition{TeamID: "a", RivalID: "c", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: int(paths[i][1].Mask)}}}}, scenarioRequirement{Score: &scenarios.ScoreCondition{TeamID: "a", RivalID: "e", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: int(paths[i][2].Mask)}}}})
		for j := range paths[i] {
			paths[i][j].Text = requirementText(paths[i][j], "", labels, games)
		}
	}
	grouped := groupSeasonSummary(factorRequirements(paths, 2), "a", labels, games)
	if grouped == nil || len(grouped.Groups) != 3 {
		t.Fatalf("expected three independent required groups: %+v", grouped)
	}
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	var buf bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&buf, "clinchingGroupedSummary", grouped); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "And one of:") != 2 {
		t.Fatalf("additional groups lost their conjunction: %s", buf.String())
	}
}
