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

	rows, scale := forecastRows(result, 4, forecastCertainty{})
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

	rows, scale := forecastRows(result, 1, forecastCertainty{})
	if scale != "100%" {
		t.Fatalf("scale = %q, want 100%%", scale)
	}
	if got := rows[0].PositionBreakdown[0].BarWidth; got != "100.0%" {
		t.Fatalf("certain finish bar = %q, want 100.0%%", got)
	}
}

func TestForecastHeatmapKeepsRareAndUnsampledFinishesOnAnAbsoluteScale(t *testing.T) {
	result := simulation.Result{Teams: []simulation.TeamResult{
		{Team: standings.Team{ID: "alpha", Name: "Alpha"}, PositionProbability: []float64{.49999, .00001, .5, 0}},
		{Team: standings.Team{ID: "bravo", Name: "Bravo"}, PositionProbability: []float64{.25, .25, .25, .25}},
		{Team: standings.Team{ID: "charlie", Name: "Charlie"}, PositionProbability: []float64{.25, .25, .25, .25}},
		{Team: standings.Team{ID: "delta", Name: "Delta"}, PositionProbability: []float64{.25, .25, .25, .25}},
	}}
	rows, scale := forecastRows(result, 2, forecastCertainty{})
	if scale != "50%" {
		t.Fatalf("distribution bar scale = %q, want 50%%", scale)
	}
	for _, row := range rows {
		if len(row.Heatmap) != 4 {
			t.Fatalf("%s has %d heatmap positions, want all 4", row.Team.Name, len(row.Heatmap))
		}
	}
	alpha := rows[0].Heatmap
	if alpha[1].Probability != "<0.1%" || !alpha[1].Sampled || alpha[1].Intensity != "0.0010%" {
		t.Errorf("rare sampled finish = %+v, want a positive <0.1%% cell", alpha[1])
	}
	if alpha[3].Sampled || alpha[3].Intensity != "0.0000%" {
		t.Errorf("unsampled finish = %+v, want a distinct unsampled cell", alpha[3])
	}
	if alpha[2].Intensity != "50.0000%" || rows[1].Heatmap[2].Intensity != "25.0000%" {
		t.Errorf("heatmap intensities = %s and %s, want absolute 50%% and 25%% despite a 50%% bar ceiling", alpha[2].Intensity, rows[1].Heatmap[2].Intensity)
	}
	for i, cell := range alpha {
		if cell.Position != i+1 || cell.OutsidePlayoffs != (i >= 2) {
			t.Errorf("position %d = %+v, want the configured two-place playoff field", i+1, cell)
		}
	}
	if !alpha[1].PlayoffBoundary || alpha[3].PlayoffBoundary {
		t.Errorf("playoff boundary should follow position 2, not a hardcoded season cutoff")
	}
}
