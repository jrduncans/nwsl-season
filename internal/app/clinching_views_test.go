package app

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func TestClinchingPageBoundsVisiblePathsAndPreservesAlternatives(t *testing.T) {
	for _, elimination := range []bool{false, true} {
		name := "clinching"
		if elimination {
			name = "elimination"
		}
		t.Run(name, func(t *testing.T) {
			data := testSeasonData()
			data.FixtureSnapshotID = "snapshot"
			clauses := make([]scenarios.Clause, 1000)
			for i := range clauses {
				clauses[i].Conditions = []scenarios.FixtureCondition{{GameID: "future-1", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin}}}
			}
			result := scenarios.Result{TeamID: "alpha", Achievement: competition.AchievementPlayoffs, TopK: 8, State: scenarios.OpportunityCanClinch, CanClinch: true, Clauses: clauses, Limitation: scenarios.LimitationBudgetPartial}
			if elimination {
				result.State, result.CanClinch, result.Clauses = scenarios.OpportunityCannotClinch, false, nil
				result.CanBeEliminated, result.EliminationClauses = true, clauses
			}
			store := fullFakeStore{
				fakeStore: fakeStore{season: data},
				scenario: cache.ScenarioSnapshot{
					Run:     cache.ScenarioRun{Slate: scenarios.Slate{State: scenarios.SlateReady, FixtureIDs: []string{"future-1"}}},
					Results: []cache.ScenarioResult{{Result: result}},
				},
			}
			response := httptest.NewRecorder()
			NewHandlerWithOptions(store, Options{CurrentSeason: "2026", Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/clinching", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			body := response.Body.String()
			before, after, found := strings.Cut(body, `<details class="clinching-more">`)
			if !found || strings.Count(before, `class="clinching-path-option"`) != 3 {
				t.Fatal("expected exactly three paths before a closed disclosure")
			}
			if !strings.Contains(after, "View 997 more confirmed paths") || strings.Count(after, `class="clinching-path-option"`) != 997 {
				t.Fatal("additional paths were lost or their count is incorrect")
			}
			if !strings.Contains(before, "Other paths may exist") || !strings.Contains(before, "Every result in that path must happen") {
				t.Fatal("path semantics or incomplete-result notice missing from preview")
			}
			if strings.Contains(before, `id="clinching-matches"`) || !strings.Contains(after, `id="clinching-matches"`) {
				t.Fatal("included schedule must follow the opportunities")
			}
			if strings.Contains(body, "<script>alert(1)</script>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
				t.Fatal("team names must remain escaped in structured conditions")
			}
		})
	}
}

func TestClinchingClauseViewsPreserveConditionsAndPrioritizeShortPaths(t *testing.T) {
	teams := map[string]string{"a": "Alpha", "b": "Bravo", "c": "Charlie"}
	games := map[string]cache.Game{
		"other": {HomeTeamID: "b", AwayTeamID: "c"},
		"own":   {HomeTeamID: "b", AwayTeamID: "a"},
	}
	other := scenarios.FixtureCondition{GameID: "other", AllowedOutcomes: []clinching.Outcome{clinching.Draw}}
	own := scenarios.FixtureCondition{GameID: "own", AllowedOutcomes: []clinching.Outcome{clinching.Draw, clinching.AwayWin}}
	clauses := []scenarios.Clause{{Conditions: []scenarios.FixtureCondition{other, own}}, {Conditions: []scenarios.FixtureCondition{own}}, {Conditions: []scenarios.FixtureCondition{}}}
	got := clauseViews(clauses, "a", teams, games)
	want := []clinchingClauseView{
		{Number: 1, Conditions: []string{}},
		{Number: 2, Conditions: []string{"Alpha wins or draws against Bravo"}},
		{Number: 3, Conditions: []string{"Alpha wins or draws against Bravo", "Bravo draws with Charlie"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("views = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(clauses[0].Conditions, []scenarios.FixtureCondition{other, own}) {
		t.Fatal("presentation changed the stored clause")
	}
}
