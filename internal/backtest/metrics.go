// Package backtest contains deterministic, dependency-free scoring helpers for
// evaluating forecast distributions. It deliberately has no cache or HTTP code.
package backtest

import (
	"math"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

const probabilityFloor = 1e-15

// OutcomeLogLoss returns the proper three-way log score for an observed result.
func OutcomeLogLoss(probabilities forecast.OutcomeProbabilities, observed string) float64 {
	p := probabilities.Draw
	if observed == "h" {
		p = probabilities.HomeWin
	}
	if observed == "a" {
		p = probabilities.AwayWin
	}
	if p < probabilityFloor {
		p = probabilityFloor
	}
	return -math.Log(p)
}
func Brier(probability float64, observed bool) float64 {
	target := 0.0
	if observed {
		target = 1
	}
	return (probability - target) * (probability - target)
}

// DiscreteCRPS scores a complete integer-valued probability distribution.
func DiscreteCRPS(values []simulation.PointsProbability, observed int) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]simulation.PointsProbability(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Points < ordered[j].Points })
	min, max := ordered[0].Points, ordered[len(ordered)-1].Points
	if observed < min {
		min = observed
	}
	if observed > max {
		max = observed
	}
	cdf := 0.0
	index := 0
	sum := 0.0
	for point := min; point <= max; point++ {
		for index < len(ordered) && ordered[index].Points <= point {
			cdf += ordered[index].Probability
			index++
		}
		indicator := 0.0
		if point >= observed {
			indicator = 1
		}
		diff := cdf - indicator
		sum += diff * diff
	}
	return sum
}

// RankedProbabilityScore scores an ordered categorical distribution.
func RankedProbabilityScore(probabilities []float64, observedPosition int) float64 {
	if len(probabilities) < 2 || observedPosition < 1 || observedPosition > len(probabilities) {
		return 0
	}
	cdf := 0.0
	sum := 0.0
	for i := 0; i < len(probabilities)-1; i++ {
		cdf += probabilities[i]
		target := 0.0
		if i+1 >= observedPosition {
			target = 1
		}
		d := cdf - target
		sum += d * d
	}
	return sum / float64(len(probabilities)-1)
}

type CalibrationBin struct {
	Lower, Upper, MeanPrediction, ObservedFrequency float64
	Count                                           int
}

// Calibration returns ten fixed deciles, including empty bins for stable reports.
func Calibration(predictions []float64, observed []bool) []CalibrationBin {
	bins := make([]CalibrationBin, 10)
	sums := make([]float64, 10)
	hits := make([]int, 10)
	for i := range bins {
		bins[i].Lower = float64(i) / 10
		bins[i].Upper = float64(i+1) / 10
	}
	for i, p := range predictions {
		if i >= len(observed) {
			break
		}
		index := int(p * 10)
		if index > 9 {
			index = 9
		}
		if index < 0 {
			index = 0
		}
		bins[index].Count++
		sums[index] += p
		if observed[i] {
			hits[index]++
		}
	}
	for i := range bins {
		if bins[i].Count > 0 {
			bins[i].MeanPrediction = sums[i] / float64(bins[i].Count)
			bins[i].ObservedFrequency = float64(hits[i]) / float64(bins[i].Count)
		}
	}
	return bins
}
