package backtest

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func TestScoringHelpers(t *testing.T) {
	if got := OutcomeLogLoss(forecast.OutcomeProbabilities{HomeWin: .5, Draw: .25, AwayWin: .25}, "h"); math.Abs(got-math.Log(2)) > 1e-12 {
		t.Fatalf("log loss = %f, want log(2)", got)
	}
	if got := Brier(.7, true); math.Abs(got-.09) > 1e-12 {
		t.Fatalf("brier = %f, want .09", got)
	}
	if got := DiscreteCRPS([]simulation.PointsProbability{{Points: 1, Probability: .5}, {Points: 2, Probability: .5}}, 1); math.Abs(got-.25) > 1e-12 {
		t.Fatalf("crps = %f, want .25", got)
	}
	if got := RankedProbabilityScore([]float64{.2, .3, .5}, 2); math.Abs(got-.145) > 1e-12 {
		t.Fatalf("rps = %f, want .145", got)
	}
}

func TestCalibrationAndBootstrapAreDeterministic(t *testing.T) {
	bins := Calibration([]float64{.01, .11, .99}, []bool{false, true, true})
	if bins[0].Count != 1 || bins[1].ObservedFrequency != 1 || bins[9].MeanPrediction != .99 {
		t.Fatalf("bins = %+v", bins)
	}
	firstLow, firstHigh := PairedBootstrap([]float64{-1, 1, 2}, 100, 42)
	secondLow, secondHigh := PairedBootstrap([]float64{-1, 1, 2}, 100, 42)
	if !reflect.DeepEqual([]float64{firstLow, firstHigh}, []float64{secondLow, secondHigh}) || firstLow > firstHigh {
		t.Fatalf("bootstrap intervals = %f, %f and %f, %f", firstLow, firstHigh, secondLow, secondHigh)
	}
}

func TestReportStatesIncompleteEvaluation(t *testing.T) {
	report := Report{Status: "not_run", CurrentDefaultModel: "results-poisson-v1", Limitations: []string{"historical evaluation is pending"}}
	if got := Markdown(report); !strings.Contains(got, "not_run") || !strings.Contains(got, "results-poisson-v1") || !strings.Contains(got, "Development results") || !strings.Contains(got, "Final-test results") || !strings.Contains(got, "Pooled results") {
		t.Fatalf("markdown = %q", got)
	}
}
