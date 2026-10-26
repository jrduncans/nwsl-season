package app

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func TestScoreConditionIndividualMarginsAreExact(t *testing.T) {
	labels := map[string]string{"a": "AAA", "b": "BBB", "x": "XXX", "y": "YYY"}
	game := func(home, away string) cache.Game {
		return cache.Game{HomeTeamID: home, AwayTeamID: away, Status: "PreMatch"}
	}
	for _, tt := range []struct {
		name    string
		games   map[string]cache.Game
		fixed   map[string]uint8
		minimum int
		want    string
		margin  int
	}{
		{"one win versus a draw", map[string]cache.Game{"a": game("a", "x"), "b": game("b", "y")}, map[string]uint8{"a": 1, "b": 2}, 12, "AAA must win by at least 12 goals", 12},
		{"head to head counts twice", map[string]cache.Game{"ab": game("a", "b")}, map[string]uint8{"ab": 1}, 4, "AAA must win by at least 2 goals", 2},
		{"odd head to head threshold", map[string]cache.Game{"ab": game("a", "b")}, map[string]uint8{"ab": 1}, 5, "AAA must win by at least 3 goals", 3},
		{"both win", map[string]cache.Game{"a": game("a", "x"), "b": game("b", "y")}, map[string]uint8{"a": 1, "b": 1}, 1, "AAA must win by a larger margin than BBB", 0},
		{"both win equality sufficient", map[string]cache.Game{"a": game("a", "x"), "b": game("b", "y")}, map[string]uint8{"a": 1, "b": 1}, 0, "AAA must win by at least as large a margin as BBB", 0},
		{"unequal inventories", map[string]cache.Game{"a": game("a", "x"), "b": game("b", "y"), "b2": game("x", "b")}, map[string]uint8{"a": 1, "b": 2, "b2": 2}, 4, "AAA must win by at least 4 goals", 4},
		{"two matches with fixed draws", map[string]cache.Game{"a": game("a", "x"), "a2": game("y", "a"), "b": game("b", "x")}, map[string]uint8{"a": 1, "a2": 2, "b": 2}, 8, "AAA must win by at least 8 goals", 8},
		{"head to head and other draws", map[string]cache.Game{"ab": game("a", "b"), "a2": game("x", "a"), "b2": game("b", "y")}, map[string]uint8{"ab": 1, "a2": 2, "b2": 2}, 9, "AAA must win by at least 5 goals", 5},
		{"two target matches are aggregate", map[string]cache.Game{"a": game("a", "x"), "a2": game("y", "a"), "b": game("b", "y")}, map[string]uint8{"a": 1, "a2": 4, "b": 2}, 8, "", 0},
		{"rival loss contributes to swing", map[string]cache.Game{"a": game("a", "x"), "b": game("b", "y")}, map[string]uint8{"a": 1, "b": 4}, 4, "", 0},
		{"unknown rival result", map[string]cache.Game{"a": game("a", "x"), "b": game("b", "y")}, map[string]uint8{"a": 1}, 12, "", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := scenarios.ScoreCondition{TeamID: "a", RivalID: "b", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: tt.minimum}}}
			parts, margin := individualMargin(c, labels, tt.games, tt.fixed)
			if partsText(parts) != tt.want || margin != tt.margin {
				t.Fatalf("got %q / %d", partsText(parts), margin)
			}
			if tt.want == "" {
				return
			}
			// Enumerate actual margins rather than assuming a relative swing is a win.
			for own := 1; own <= 16; own++ {
				for other := 0; other <= 16; other++ {
					gain := own - other
					want := own >= tt.margin
					switch tt.name {
					case "head to head counts twice", "odd head to head threshold", "head to head and other draws":
						gain = 2 * own
					case "both win":
						if other == 0 {
							continue
						}
						want = own > other
					case "both win equality sufficient":
						if other == 0 {
							continue
						}
						want = own >= other
					default:
						gain = own
					}
					if c.Holds([]int{gain}) != want {
						t.Fatalf("wording is not equivalent at margins %d,%d", own, other)
					}
				}
			}
		})
	}
}

func TestScoreConditionCascadesAndRemainingCounts(t *testing.T) {
	labels := map[string]string{"a": "AAA", "b": "BBB"}
	c := scenarios.ScoreCondition{TeamID: "a", RivalID: "b", Levels: []scenarios.ScoreLevel{
		{Measure: scenarios.MeasureGoalDifference, TeamNow: 2, RivalNow: 14, MinimumGain: 13},
		{Measure: scenarios.MeasureGoalsScored, TeamNow: 30, RivalNow: 35, MinimumGain: 6},
		{Measure: scenarios.MeasureHeadToHeadGoals, TeamNow: 1, RivalNow: 2, MinimumGain: 1},
	}}
	want := "AAA must finish with a higher goal difference than BBB or, if level on goal difference, AAA must finish with more goals than BBB or, if level on goal difference and level on goals scored, AAA must finish with at least as many head-to-head goals as BBB"
	if got := scoreConditionText(c, labels); got != want {
		t.Fatalf("%q", got)
	}
	// Final-table interpretation independently checks the cascade for one and
	// two remaining matches per team: a head-to-head game, then an optional
	// extra x–0 target win and 0–0 rival draw against other opponents.
	for _, n := range []int{1, 2} {
		for a := 0; a <= 15; a++ {
			for b := 0; b <= 4; b++ {
				for x := 0; x <= 2; x++ {
					gdGain := 2*(a-b) + (n-1)*x
					gfGain := a - b + (n-1)*x
					gd := 2 + gdGain - 14
					gf := 30 + gfGain - 35
					h2h := 1 + a - (2 + b)
					expected := gd > 0 || gd == 0 && (gf > 0 || gf == 0 && h2h >= 0)
					gains := []int{gdGain, gfGain, a - b}
					if c.Holds(gains) != expected {
						t.Fatalf("cascade changed for %d matches", n)
					}
				}
			}
		}
	}
	games := map[string]cache.Game{"ab": {HomeTeamID: "a", AwayTeamID: "b", Status: "PreMatch"}, "a2": {HomeTeamID: "a", AwayTeamID: "x", Status: "PreMatch"}}
	r := scenarioRequirement{Score: &c}
	presentScore(&r, labels, games, map[string]uint8{"ab": 1, "a2": 1}, scenarioHistory{Maximum: 6, Scope: "test history"})
	if r.Text != want || r.Historical != "" {
		t.Fatal("cascade was hidden or incorrectly simplified")
	}
	explanation := partsText(r.Explanation)
	if strings.Contains(explanation, "fixed") || strings.Contains(explanation, "open scores") {
		t.Fatalf("shared explanation refers to a seeding-only concept: %s", explanation)
	}
	for _, text := range []string{"BBB currently leads by 12", "AAA has 2 matches remaining", "BBB has 1 match remaining"} {
		if !strings.Contains(explanation, text) {
			t.Fatalf("missing %q in %s", text, explanation)
		}
	}
	c.Levels[0].MinimumGain = 12
	c.Levels = c.Levels[:1]
	if got := scoreConditionText(c, labels); got != "AAA must finish with at least the goal difference of BBB" {
		t.Fatal(got)
	}
}

func TestScoreHistoricalAnnotationRequiresIndividualMarginAndCompleteHistory(t *testing.T) {
	season := cache.HistoricalSeason{Entry: competition.Entry{Season: "2024", Stage: "Regular Season"}, Readiness: &cache.SeasonReadinessSnapshot{Completeness: cache.InventoryCompletenessComplete, Scope: cache.SourceScope{Lifecycle: cache.SourceScopeCompleted}}, Data: cache.SeasonData{Games: []cache.Game{{Status: "FullTime", HomeScore: sql.NullInt64{Int64: 6, Valid: true}, AwayScore: sql.NullInt64{Int64: 0, Valid: true}, KickoffUTC: "2024-10-01 19:00:00 UTC"}}}}
	history := scenarioMarginHistory([]cache.HistoricalSeason{season}, "2026")
	if history.Maximum != 6 || history.Scope != "NWSL regular seasons 2024" || history.Cutoff != "2024-10-01" {
		t.Fatalf("history: %+v", history)
	}
	labels := map[string]string{"a": "AAA", "b": "BBB", "x": "XXX"}
	c := scenarios.ScoreCondition{TeamID: "a", RivalID: "b", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 8}}}
	games := map[string]cache.Game{"a": {HomeTeamID: "a", AwayTeamID: "x", Status: "PreMatch"}, "b": {HomeTeamID: "b", AwayTeamID: "x", Status: "PreMatch"}}
	r := scenarioRequirement{Score: &c}
	presentScore(&r, labels, games, map[string]uint8{"a": 1, "b": 2}, history)
	if !strings.Contains(r.Historical, "at least 8 goals") || !strings.Contains(r.Historical, "2024") || !strings.Contains(r.Historical, "not an all-time record") {
		t.Fatalf("annotation: %s", r.Historical)
	}
	for _, masks := range []map[string]uint8{{"a": 1, "b": 4}, {"a": 1, "b": 1}, {"a": 1}} {
		r = scenarioRequirement{Score: &c}
		presentScore(&r, labels, games, masks, history)
		if r.Historical != "" {
			t.Fatal("aggregate swing received individual-margin annotation")
		}
	}
	// An eight-goal relative gain in a head-to-head match requires only four.
	games = map[string]cache.Game{"ab": {HomeTeamID: "a", AwayTeamID: "b", Status: "PreMatch"}}
	r = scenarioRequirement{Score: &c}
	presentScore(&r, labels, games, map[string]uint8{"ab": 1}, history)
	if r.Historical != "" {
		t.Fatal("head-to-head swing was treated as a winning margin")
	}
	for _, modify := range []func(*cache.HistoricalSeason){
		func(s *cache.HistoricalSeason) { s.Readiness = nil },
		func(s *cache.HistoricalSeason) { s.Readiness.Completeness = cache.InventoryCompletenessUnknown },
		func(s *cache.HistoricalSeason) { s.Readiness.Scope.Lifecycle = cache.SourceScopeActive },
		func(s *cache.HistoricalSeason) { s.Data.Games[0].HomeScore.Valid = false },
	} {
		sample := season
		readiness := *season.Readiness
		sample.Readiness = &readiness
		sample.Data.Games = append([]cache.Game(nil), season.Data.Games...)
		modify(&sample)
		if got := scenarioMarginHistory([]cache.HistoricalSeason{sample}, "2026"); got.Scope != "" {
			t.Fatalf("partial history accepted: %+v", got)
		}
	}
}

func TestSeedingCompleteAlternativeRequirementPaging(t *testing.T) {
	labels := map[string]string{"a": "AAA", "b": "BBB"}
	conditions := []scenarios.FixtureCondition{}
	games := map[string]cache.Game{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		games[id] = cache.Game{HomeTeamID: "a", AwayTeamID: "b", Status: "PreMatch"}
		conditions = append(conditions, testScenarioCondition(id, 1))
	}
	result := scenarios.SeedingResult{Seeds: []scenarios.SeedFinish{{Seed: 1, Clauses: []scenarios.Clause{{Conditions: conditions}}}}}
	query := url.Values{"view": {"seeding"}, "team": {"AAA"}, "finish": {"1"}, "alternative": {"1"}, "p": {"fixed:h"}}
	seen := 0
	for part := 0; part < 3; part++ {
		query.Set("part", string(rune('0'+part)))
		detail, err := seedingAlternatives(result, 1, query, labels, games, nil, scenarioHistory{})
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Conditions) > seedingDetailRequirements || detail.Requirements != 9 {
			t.Fatalf("unbounded detail: %+v", detail)
		}
		seen += len(detail.Conditions)
		for _, link := range []string{detail.BackURL, detail.NextPart, detail.PreviousPart} {
			if link != "" && (!strings.Contains(link, "p=fixed%3Ah") || strings.HasPrefix(link, "/")) {
				t.Fatalf("lost state or relative URL: %s", link)
			}
		}
	}
	if seen != 9 {
		t.Fatalf("lost requirements: %d", seen)
	}
	for _, value := range []string{"-1", "3", "99999999999999999999"} {
		query.Set("part", value)
		if _, err := seedingAlternatives(result, 1, query, labels, games, nil, scenarioHistory{}); err == nil {
			t.Fatalf("invalid part %s accepted", value)
		}
	}
}

func TestScenarioHistoryReadsArchiveOncePerRefresh(t *testing.T) {
	store := &archiveHTTPStore{}
	app := &application{store: store}
	ctx := context.Background()

	app.scenarioHistory(ctx, "2026")
	app.scenarioHistory(ctx, "2025")
	app.scenarioHistory(ctx, "2026")
	if store.archiveCalls != 1 {
		t.Fatalf("archive reads = %d, want 1 shared by every season", store.archiveCalls)
	}
	if err := app.refreshScenarioHistory(ctx); err != nil || store.archiveCalls != 2 {
		t.Fatalf("refresh = %v after %d reads, want one more read", err, store.archiveCalls)
	}

	store.err = errors.New("boom")
	if err := app.refreshScenarioHistory(ctx); err == nil {
		t.Fatal("failed refresh returned nil")
	}
	if _, loaded := app.historyCache.get(); !loaded {
		t.Fatal("failed refresh discarded the previous summary")
	}
}

func TestScenarioHistoryRetriesAfterFailedFirstLoad(t *testing.T) {
	store := &archiveHTTPStore{err: errors.New("boom")}
	app := &application{store: store}
	ctx := context.Background()

	if got := app.scenarioHistory(ctx, "2026"); got.Scope != "" {
		t.Fatalf("history = %+v, want none after a failed read", got)
	}
	app.scenarioHistory(ctx, "2026")
	if store.archiveCalls != 2 {
		t.Fatalf("archive reads = %d, want a failed load retried, not cached", store.archiveCalls)
	}
}
