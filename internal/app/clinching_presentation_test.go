package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func TestClinchingPresentation(t *testing.T) {
	score := scenarioRequirement{Score: &scenarios.ScoreCondition{}}
	plain := scenarioRequirement{Text: "UTA loses at NJY"}
	leaf := scenarioExpression{Conditions: []scenarioRequirement{plain}}
	for _, tt := range []struct {
		name  string
		group clinchingGroupView
		want  string
	}{
		{"certain", clinchingGroupView{}, "certain"},
		{"simple", clinchingGroupView{Summary: leaf}, "compact"},
		{"three requirements", clinchingGroupView{Summary: scenarioExpression{Conditions: []scenarioRequirement{plain, plain, plain}}}, "compact"},
		{"four requirements", clinchingGroupView{Summary: scenarioExpression{Conditions: []scenarioRequirement{plain, plain, plain, plain}}}, "dense"},
		{"nested score", clinchingGroupView{Summary: scenarioExpression{Alternatives: []scenarioExpression{{Alternatives: []scenarioExpression{{Conditions: []scenarioRequirement{score}}}}}}}, "dense"},
		{"nested alternatives", clinchingGroupView{Summary: scenarioExpression{Alternatives: []scenarioExpression{{Alternatives: []scenarioExpression{leaf, leaf, leaf}}}}}, "dense"},
		{"count factored occurrences", clinchingGroupView{Summary: scenarioExpression{Conditions: []scenarioRequirement{plain}, Alternatives: []scenarioExpression{leaf, leaf}}}, "compact"},
		{"grouped summary", clinchingGroupView{GroupedSummary: &clinchingGroupedSummary{}}, "dense"},
		{"rival score", clinchingGroupView{RivalState: "Clinches if", Rivals: []clinchingRivalView{{Ties: []clinchingRivalTieView{{Condition: &score}}}}}, "dense"},
		{"rival alternatives", clinchingGroupView{RivalState: "Clinches if", Rivals: []clinchingRivalView{{Ties: []clinchingRivalTieView{{Alternative: true}, {Alternative: true}}}}}, "dense"},
		{"three rival choices", clinchingGroupView{RivalState: "Clinches if any of:", RivalChoice: true, Rivals: make([]clinchingRivalView, 3)}, "dense"},
		{"overflow", clinchingGroupView{SummaryOverflow: true, Help: scenarioExpression{Conditions: []scenarioRequirement{score}}}, "overflow"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.group.Presentation(); got != tt.want {
				t.Fatalf("presentation = %s, want %s", got, tt.want)
			}
		})
	}
	// Rest-of-season is a grouping option, never a density signal.
	groups := clinchingGroupViews([]scenarios.Clause{testScenarioClause(testScenarioCondition("sd-bos", 1))}, "njy", rivalTestTeams, rivalTestTeams, rivalTestGames(), clinchingGroupOptions{SummaryLimit: maxSeasonSummaryPaths, RestOfSeason: true})
	if len(groups) != 1 || groups[0].Presentation() != "compact" {
		t.Fatalf("simple season group = %+v", groups)
	}
}

func TestClinchingRivalFixtureCrests(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	teams := map[string]string{"kc": "KC", "lou": "LOU", "was": "WAS", "lou-other": "LOU"}
	games := map[string]cache.Game{
		"away":      {HomeTeamID: "lou", AwayTeamID: "kc"},
		"home":      {HomeTeamID: "kc", AwayTeamID: "was"},
		"same-code": {HomeTeamID: "lou-other", AwayTeamID: "kc"},
	}
	for _, tt := range []struct {
		name      string
		need      scenarios.RivalNeed
		want      string
		opponents []string
	}{
		{"away loss", scenarios.RivalNeed{RivalID: "kc", GameIDs: []string{"away"}, MaxPoints: 0}, "KC loses at LOU", []string{"lou"}},
		{"home result", scenarios.RivalNeed{RivalID: "kc", GameIDs: []string{"home"}, MaxPoints: 1}, "KC draws or loses vs WAS", []string{"was"}},
		{"multiple fixtures", scenarios.RivalNeed{RivalID: "kc", GameIDs: []string{"home", "away"}, MaxPoints: 3}, "KC takes 3 points or fewer (vs WAS and at LOU)", []string{"was", "lou"}},
		{"three fixtures", scenarios.RivalNeed{RivalID: "kc", GameIDs: []string{"home", "away", "same-code"}, MaxPoints: 3}, "KC takes 3 points or fewer (vs WAS, at LOU, and at LOU)", []string{"was", "lou", "lou-other"}},
		{"mandatory result", scenarios.RivalNeed{RivalID: "kc", GameIDs: []string{"away"}, MaxPoints: -1, Ties: []scenarios.RivalTie{{Points: 0}}}, "KC loses at LOU", []string{"lou"}},
		{"mandatory pair", scenarios.RivalNeed{RivalID: "kc", GameIDs: []string{"home", "away"}, MaxPoints: -1, Ties: []scenarios.RivalTie{{Points: 0}}}, "KC loses both matches (vs WAS and at LOU)", []string{"was", "lou"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := application.app.pages.ExecuteTemplate(&buf, "clinchingRival", rivalView(tt.need, teams, games)); err != nil {
				t.Fatal(err)
			}
			body := "<div class=\"rival\">" + buf.String() + "</div>"
			requireText(t, body, ".rival", tt.want)
			for _, id := range append([]string{"kc"}, tt.opponents...) {
				requireAttr(t, body, `img[src="`+clubLogoURL(id)+`"]`, "src", clubLogoURL(id))
			}
			if got := strings.Count(body, "<img"); got != len(tt.opponents)+1 {
				t.Fatalf("fixture crests missing or duplicated: %s", body)
			}
		})
	}
}

func TestClinchingEssentialScoreConditionsStayVisible(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	score := &scenarios.ScoreCondition{TeamID: "njy", RivalID: "sd", Levels: []scenarios.ScoreLevel{
		{Measure: scenarios.MeasureGoalDifference, MinimumGain: 2, TeamNow: 4, RivalNow: 5},
		{Measure: scenarios.MeasureGoalsScored, MinimumGain: 1, TeamNow: 40, RivalNow: 40},
	}}
	for _, maxPoints := range []int{-1, 0} {
		rival := rivalView(scenarios.RivalNeed{RivalID: "sd", GameIDs: []string{"sd-bos"}, MaxPoints: maxPoints, Ties: []scenarios.RivalTie{{Points: 1, Condition: score}}}, rivalTestTeams, rivalTestGames())
		presentRival(&rival, rivalTestTeams, rivalTestGames(), nil, scenarioHistory{})
		var buf bytes.Buffer
		if err := application.app.pages.ExecuteTemplate(&buf, "clinchingRival", rival); err != nil {
			t.Fatal(err)
		}
		body := buf.String()
		requireText(t, body, ".clinching-condition-score", "NJY must finish with a higher goal difference than SD", "or, if level on goal difference, NJY must finish with more goals than SD")
		requireExact(t, body, "summary", "Explanation")
		requireText(t, body, "details div", "SD currently leads by 1", "The teams are currently level on goals scored at 40")
		// Essential text is outside every disclosure, including equality cascades.
		for _, node := range find(t, body, "details") {
			if strings.Contains(text(node), "must finish") {
				t.Fatal("essential condition hidden in explanation")
			}
		}
		if strings.Count(body, "<img") < 6 {
			t.Fatal("missing score or fixture team crests")
		}
	}
	// No useful arithmetic means no disclosure.
	var buf bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&buf, "clinchingRequirement", scenarioRequirement{Text: "plain condition"}); err != nil {
		t.Fatal(err)
	}
	forbidRaw(t, buf.String(), "<details")
}

func TestSeedingOverflowCompactPresentation(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	for _, tt := range []struct {
		name string
		view seedingOverflowView
		want []string
	}{
		{"no common requirement", seedingOverflowView{Matches: []string{"KC vs WAS", "NJY vs UTA"}, Some: [][]scenarioRequirement{{{Text: "KC earns at least 2 points from these two matches: vs WAS and at LOU"}}}, SomeHigh: 1, Scores: true}, []string{"Some paths also need tiebreak scores."}},
		{"common requirement", seedingOverflowView{Matches: []string{"KC vs WAS", "KC vs LOU"}, Always: []scenarioRequirement{{Text: "KC earns at least 2 points from these two matches: vs WAS and vs LOU"}}}, []string{"Common requirements (all of):", "KC earns at least 2 points"}},
		{"positive minimum", seedingOverflowView{SomeLow: 1, SomeHigh: 2, Some: [][]scenarioRequirement{{{Text: "UTA loses at NJY"}}}}, []string{"Each way also needs 1 to 2 of these:", "UTA loses at NJY"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := application.app.pages.ExecuteTemplate(&buf, "seedingOverflow", tt.view); err != nil {
				t.Fatal(err)
			}
			body := buf.String()
			requireText(t, body, ".seeding-overflow", append(tt.want, "This summary alone does not guarantee the finish.")...)
			for _, unwanted := range []string{"Some ways also need up to"} {
				if strings.Contains(body, unwanted) {
					t.Fatalf("overflow contains %q: %s", unwanted, body)
				}
			}
			if tt.view.SomeLow == 0 && len(tt.view.Always) == 0 {
				forbidRaw(t, body, "<ul>", "KC earns at least")
			}
		})
	}
}

func TestSeedingChanceMeaningAppearsInHeaders(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	view := clinchingPage{Seeding: &seedingPage{LikelihoodModel: "Test model", Columns: []seedingColumnView{{Lines: []string{"Wins at HOU"}, Likelihood: "48%"}}, Rows: []seedingRowView{{Heading: "4th", Likelihood: "2%", Cells: []seedingCellView{{Possible: true, Likelihood: "2%"}}}}}}
	var buf bytes.Buffer
	if err := application.app.pages.ExecuteTemplate(&buf, "clinchingSeeding", view); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	requireText(t, body, ".seeding-grid thead", "Overall chance", "Results: 48%", "Finish + these results")
	forbidText(t, body, ".seeding-grid tbody", "Finish + these results", "Overall:")
	if strings.Count(body, "Finish + these results") != 1 || strings.Contains(body, "seeding-chance-key") {
		t.Fatal("chance explanation repeated outside headers")
	}
	requireText(t, body, ".seeding-chance-help", "Fix results", "among counted forecasts", "AND opponent", "venue availability")
}

func TestClinchingCompactGuaranteedRowsAcrossOutcomes(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	for _, outcome := range []string{"clinching", "elimination", "seeding"} {
		t.Run(outcome, func(t *testing.T) {
			groups := []clinchingGroupView{{Heading: "Win at HOU", Tone: "win"}}
			name := "clinchingClauses"
			var value any = clinchingRowView{Groups: groups, Elimination: outcome == "elimination"}
			if outcome == "seeding" {
				name = "clinchingSeeding"
				value = clinchingPage{Seeding: &seedingPage{Conditions: &seedingConditionsView{State: "Finishes 4th", Groups: []seedingGroupView{{clinchingGroupView: groups[0]}}}}}
			}
			var buf bytes.Buffer
			if err := application.app.pages.ExecuteTemplate(&buf, name, value); err != nil {
				t.Fatal(err)
			}
			body := buf.String()
			requireExact(t, body, ".clinching-result-header", "Win at HOU")
			forbidRaw(t, body, "✓", "clinching-result-state-certain", "No help needed")
			nodes := find(t, body, ".clinching-presentation-certain")
			if len(nodes) != 1 {
				t.Fatal("missing compact row")
			}
			children := 0
			for child := nodes[0].FirstChild; child != nil; child = child.NextSibling {
				if child.Type == 3 {
					children++
				}
			}
			if children != 1 {
				t.Fatalf("guaranteed row has an empty body: %d elements", children)
			}
		})
	}
}

func TestSeedingScoreGuidanceFollowsSelectedResults(t *testing.T) {
	application := newApplicationWithForecastExecutor(nil, Options{Location: time.UTC}, nil)
	for _, fixed := range []int{0, 1} {
		seeding := &seedingPage{Builder: forecastPage{FixedCount: fixed}, Conditions: &seedingConditionsView{}, Details: &seedingAlternativesView{}}
		for _, name := range []string{"clinchingSeeding", "seedingAlternatives"} {
			var buf bytes.Buffer
			if err := application.app.pages.ExecuteTemplate(&buf, name, clinchingPage{Seeding: seeding}); err != nil {
				t.Fatal(err)
			}
			want := "Score requirements apply across all remaining matches."
			if fixed > 0 {
				want += " Choosing a win, draw or loss does not set the final score."
			}
			if name == "clinchingSeeding" {
				requireExact(t, buf.String(), ".seeding-score-guidance", want)
			} else {
				forbidRaw(t, buf.String(), "Score requirements apply across all remaining matches.")
				if fixed > 0 {
					requireText(t, buf.String(), "body", "These scenarios include your selected results. Choosing a win, draw or loss does not set the final score.")
				}
			}
			forbidRaw(t, buf.String(), "Fixed results still have open scores.", "Scores remain open.")
		}
	}
}
