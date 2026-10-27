package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func rivalTestGames() map[string]cache.Game {
	return map[string]cache.Game{
		"orl-njy": {ASAID: "orl-njy", HomeTeamID: "orl", AwayTeamID: "njy", KickoffUTC: "2026-10-25T19:00:00Z"},
		"njy-uta": {ASAID: "njy-uta", HomeTeamID: "njy", AwayTeamID: "uta", KickoffUTC: "2026-11-01T19:00:00Z"},
		"sd-bos":  {ASAID: "sd-bos", HomeTeamID: "sd", AwayTeamID: "bos", KickoffUTC: "2026-10-25T23:00:00Z"},
		"bay-sd":  {ASAID: "bay-sd", HomeTeamID: "bay", AwayTeamID: "sd", KickoffUTC: "2026-11-01T23:00:00Z"},
	}
}

var rivalTestTeams = map[string]string{"njy": "NJY", "orl": "ORL", "uta": "UTA", "sd": "SD", "bos": "BOS", "bay": "BAY"}

func TestClinchingRivalGroupViewsIntersectSharedFixture(t *testing.T) {
	games := map[string]cache.Game{
		"sea-orl": {ASAID: "sea-orl", HomeTeamID: "sea", AwayTeamID: "orl"},
		"nc-den":  {ASAID: "nc-den", HomeTeamID: "nc", AwayTeamID: "den"},
		"bay-sd":  {ASAID: "bay-sd", HomeTeamID: "bay", AwayTeamID: "sd"},
	}
	teams := map[string]string{"sea": "SEA", "orl": "ORL", "nc": "NC", "den": "DEN", "sd": "SD", "bay": "BAY"}
	nc := scenarios.RivalNeed{RivalID: "nc", GameIDs: []string{"nc-den"}, MaxPoints: 1}
	den := scenarios.RivalNeed{RivalID: "den", GameIDs: []string{"nc-den"}, MaxPoints: 1}
	sd := scenarios.RivalNeed{RivalID: "sd", GameIDs: []string{"bay-sd"}, MaxPoints: 1}
	conditional := den
	conditional.Ties = []scenarios.RivalTie{{Points: 3, Condition: &scenarios.ScoreCondition{TeamID: "sea", RivalID: "den", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1}}}}}
	unconditional := den
	unconditional.MaxPoints = 0
	unconditional.Ties = []scenarios.RivalTie{{Points: 1}}
	tests := []struct {
		name   string
		need   int
		rivals []scenarios.RivalNeed
		state  string
		want   []string
	}{
		{"both must drop points", 2, []scenarios.RivalNeed{nc, den}, "Clinches if", []string{"NC draws vs DEN"}},
		{"unconditional tie", 2, []scenarios.RivalNeed{nc, unconditional}, "Clinches if", []string{"NC draws vs DEN"}},
		{"independent rival", 3, []scenarios.RivalNeed{nc, den, sd}, "Clinches if all of:", []string{"NC draws vs DEN", "SD draws or loses at BAY"}},
		{"any rival", 1, []scenarios.RivalNeed{nc, den}, "Clinches if any of:", []string{"NC draws or loses vs DEN", "DEN draws or loses at NC"}},
		{"two of three rivals", 2, []scenarios.RivalNeed{nc, den, sd}, "Clinches if at least 2 of:", []string{"NC draws or loses vs DEN", "DEN draws or loses at NC", "SD draws or loses at BAY"}},
		{"score condition", 2, []scenarios.RivalNeed{nc, conditional}, "Clinches if all of:", []string{"NC draws or loses vs DEN", "DEN draws or loses at NC or wins if [SEA’s final goal difference must exceed DEN’s]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scenarios.Result{TeamID: "sea", Rivals: &scenarios.RivalSummary{Rows: []scenarios.RivalRow{{
				Own:  []scenarios.FixtureCondition{{GameID: "sea-orl", AllowedOutcomes: []clinching.Outcome{clinching.AwayWin}}},
				Need: tt.need, Rivals: tt.rivals,
			}}}}
			before, _ := json.Marshal(result)
			views := rivalGroupViews(result, "sea", teams, teams, games, map[string]int{"nc": 1, "den": 2, "sd": 3})
			if len(views) != 1 || views[0].Heading != "If SEA loses vs ORL" || views[0].RivalState != tt.state {
				t.Fatalf("unexpected group: %+v", views)
			}
			got := []string{}
			for _, rival := range views[0].Rivals {
				got = append(got, rivalText(rival))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("rivals = %q, want %q", got, tt.want)
			}
			after, _ := json.Marshal(result)
			if string(before) != string(after) {
				t.Fatal("presentation mutated the cached result")
			}
		})
	}
}

func rivalText(view clinchingRivalView) string {
	words := []string{}
	var result strings.Builder
	for i, part := range view.Parts {
		if i > 0 && !part.NoSpaceBefore {
			result.WriteByte(' ')
		}
		if part.Team != nil {
			result.WriteString(part.Team.Code)
		} else {
			result.WriteString(part.Text)
		}
	}
	if result.Len() > 0 {
		words = append(words, result.String())
	}
	for _, tie := range view.Ties {
		if tie.Lead != "" {
			words = append(words, tie.Lead)
		}
		if tie.Condition != nil {
			words = append(words, "["+tie.Condition.Text+"]")
		}
	}
	return strings.Join(words, " ")
}

func TestRivalViewWordsEachRival(t *testing.T) {
	gd := &scenarios.ScoreCondition{TeamID: "njy", RivalID: "sd", Levels: []scenarios.ScoreLevel{{Measure: scenarios.MeasureGoalDifference, MinimumGain: 1, TeamNow: 13, RivalNow: 13}}}
	tests := []struct {
		need scenarios.RivalNeed
		want string
	}{
		{scenarios.RivalNeed{RivalID: "sd", GameIDs: []string{"sd-bos", "bay-sd"}, MaxPoints: 4}, "SD doesn’t win both matches (vs BOS, at BAY)"},
		{scenarios.RivalNeed{RivalID: "sd", GameIDs: []string{"sd-bos", "bay-sd"}, MaxPoints: 3, Ties: []scenarios.RivalTie{{Points: 4, Wins: 1, Condition: gd}}},
			"SD earns at most 3 points (vs BOS, at BAY) or wins one, draws one if [NJY’s final goal difference must exceed SD’s]"},
		{scenarios.RivalNeed{RivalID: "sd", GameIDs: []string{"sd-bos", "bay-sd"}, MaxPoints: -1, Ties: []scenarios.RivalTie{{Points: 0, Condition: gd}}},
			"SD loses both matches (vs BOS and at BAY) and [NJY’s final goal difference must exceed SD’s]"},
		{scenarios.RivalNeed{RivalID: "sd", GameIDs: []string{"sd-bos"}, MaxPoints: 1}, "SD draws or loses vs BOS"},
		{scenarios.RivalNeed{RivalID: "sd", GameIDs: []string{"sd-bos"}, MaxPoints: -1, Ties: []scenarios.RivalTie{{Points: 0, Condition: gd}}},
			"SD loses vs BOS and [NJY’s final goal difference must exceed SD’s]"},
		{scenarios.RivalNeed{RivalID: "sd", GameIDs: []string{}, MaxPoints: -1, Ties: []scenarios.RivalTie{{Points: 0, Condition: gd}}},
			"[NJY’s final goal difference must exceed SD’s]"},
	}
	for _, test := range tests {
		if got := rivalText(rivalView(test.need, rivalTestTeams, rivalTestGames())); got != test.want {
			t.Errorf("rival text = %q, want %q", got, test.want)
		}
	}
}

func TestRivalGroupViewsMergeOwnResultsAndFallBackWhenWithheld(t *testing.T) {
	// NJY plays at ORL, then hosts UTA.
	own := func(orl, uta clinching.Outcome) []scenarios.FixtureCondition {
		return []scenarios.FixtureCondition{{GameID: "orl-njy", AllowedOutcomes: []clinching.Outcome{orl}}, {GameID: "njy-uta", AllowedOutcomes: []clinching.Outcome{uta}}}
	}
	win, loss := clinching.AwayWin, clinching.HomeWin
	sd := []scenarios.RivalNeed{{RivalID: "sd", GameIDs: []string{"sd-bos", "bay-sd"}, MaxPoints: 3}}
	// Two losses defer to the clauses, which need BOS to win at SD.
	clauses := []scenarios.Clause{{Conditions: append(own(loss, clinching.AwayWin), testScenarioCondition("sd-bos", 4))}}
	result := scenarios.Result{TeamID: "njy", Clauses: clauses, Rivals: &scenarios.RivalSummary{Rows: []scenarios.RivalRow{
		{Own: own(loss, clinching.HomeWin), Need: 1, Rivals: sd},
		{Own: own(win, clinching.HomeWin), Rivals: []scenarios.RivalNeed{}},
		{Own: own(loss, clinching.AwayWin), Rivals: []scenarios.RivalNeed{}, Withheld: true},
		{Own: own(win, clinching.AwayWin), Need: 1, Rivals: sd},
	}}}
	names := map[string]string{"njy": "Gotham", "orl": "Orlando", "uta": "Utah"}
	views := rivalGroupViews(result, "njy", rivalTestTeams, names, rivalTestGames(), map[string]int{"sd": 2})
	headings := []string{}
	for _, view := range views {
		headings = append(headings, view.Heading)
	}
	want := []string{
		"If Gotham wins both matches (at Orlando and vs Utah)",
		"If Gotham has one win and one loss (at Orlando and vs Utah)",
		"If Gotham loses both matches (at Orlando and vs Utah)",
	}
	if strings.Join(headings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("headings =\n%s\nwant\n%s", strings.Join(headings, "\n"), strings.Join(want, "\n"))
	}
	if views[0].RivalState != "" || len(views[0].Rivals) != 0 {
		t.Fatalf("two wins should clinch outright: %+v", views[0])
	}
	if views[1].RivalState != "Clinches if" || rivalText(views[1].Rivals[0]) != "SD earns at most 3 points (vs BOS, at BAY)" {
		t.Fatalf("one win and one loss = %q %+v", views[1].RivalState, views[1].Rivals)
	}
	if views[2].RivalState != "" || len(views[2].Summary.Conditions)+len(views[2].Summary.Alternatives) == 0 {
		t.Fatalf("withheld row should use the clause summary: %+v", views[2])
	}
}
