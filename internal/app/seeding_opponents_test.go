package app

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestSeedingOpponentChancesKeepTheFullForecastDenominator(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}, {ID: "c", Name: "Charlie"}}
	rows := []seedingRowView{
		{seed: 4, probability: .3, opponentIDs: []string{"a", "b"}, opponentLikelihoods: map[string]float64{"a": .2, "b": .1}},
		{seed: 5, probability: .4, opponentIDs: []string{"a", "c"}, opponentLikelihoods: map[string]float64{"a": .4}},
		{seed: 0, probability: .2},
	}
	got := seedingOpponentChances(rows, teams, true)
	if len(got) != 5 {
		t.Fatalf("outlook = %+v", got)
	}
	for i, want := range []struct{ label, likelihood, width string }{
		{"Alpha", "60%", "60.000000%"},
		{"Bravo", "10%", "10.000000%"},
		{"Charlie", "possible; odds unavailable", ""},
		{"Miss playoffs", "20%", "20.000000%"},
		{"Unresolved outcome", "10%", "10.000000%"},
	} {
		if got[i].Label != want.label || got[i].Likelihood != want.likelihood || got[i].BarWidth != want.width {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want)
		}
	}
	if got[0].Team == nil || got[0].Team.ID != "a" || got[1].Other || !got[3].Other || !got[4].Other {
		t.Fatalf("team identities and non-opponent outcomes = %+v", got)
	}
	// A missing forecast must keep possibilities without fabricating odds or
	// treating the whole forecast as unresolved.
	unavailable := seedingOpponentChances(rows, teams, false)
	if len(unavailable) != 4 {
		t.Fatalf("unavailable outlook = %+v", unavailable)
	}
	for _, row := range unavailable {
		if row.Likelihood != "Odds unavailable" || row.BarWidth != "" {
			t.Errorf("unavailable forecast invented odds: %+v", row)
		}
	}
}

func TestSeedingOpponentChancesAreEmptyWithoutAPossibleOpponent(t *testing.T) {
	// An eliminated team keeps its concise answer instead of a one-bar chart.
	rows := []seedingRowView{{seed: 0, probability: 1}}
	if got := seedingOpponentChances(rows, nil, true); got != nil {
		t.Fatalf("eliminated outlook = %+v", got)
	}
}

func TestSeedingOpponentChancesDoNotTurnASolePartialShareIntoCertainty(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}}
	for _, test := range []struct {
		share float64
		want  string
		rows  int
	}{{1, "100%", 1}, {.999, ">99%", 2}, {.004, "<1%", 2}, {0, "possible; odds unavailable", 2}} {
		rows := []seedingRowView{{seed: 4, probability: test.share, opponentIDs: []string{"a"}, opponentLikelihoods: map[string]float64{"a": test.share}}}
		got := seedingOpponentChances(rows, teams, true)
		if len(got) != test.rows || got[0].Likelihood != test.want {
			t.Errorf("share %v: %+v", test.share, got)
		}
	}
}
