package app

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestForecastRowsUseOneRoundedPageWideFinishScale(t *testing.T) {
	result := simulation.Result{Teams: []simulation.TeamResult{
		{Team: standings.Team{ID: "alpha", Name: "Alpha"}, PositionProbability: []float64{.1875, .10}},
		{Team: standings.Team{ID: "bravo", Name: "Bravo"}, PositionProbability: []float64{.05, 1.0 / 16}},
	}}

	rows, scale := forecastRows(result, 4)
	if scale != "20%" {
		t.Fatalf("scale = %q, want 20%%", scale)
	}
	if got := rows[0].PositionBreakdown[0].BarWidth; got != "93.8%" {
		t.Fatalf("alpha's longest bar = %q, want 93.8%%", got)
	}
	if got := rows[1].PositionBreakdown[0].BarWidth; got != "25.0%" {
		t.Fatalf("bravo's first bar = %q, want 25.0%% on the shared scale", got)
	}
}

func TestForecastRowsScaleCertainFinishToFullWidth(t *testing.T) {
	result := simulation.Result{Teams: []simulation.TeamResult{
		{Team: standings.Team{ID: "alpha", Name: "Alpha"}, PositionProbability: []float64{1}},
	}}

	rows, scale := forecastRows(result, 1)
	if scale != "100%" {
		t.Fatalf("scale = %q, want 100%%", scale)
	}
	if got := rows[0].PositionBreakdown[0].BarWidth; got != "100.0%" {
		t.Fatalf("certain finish bar = %q, want 100.0%%", got)
	}
}
