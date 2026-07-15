package backtest

import (
	"math/rand"
	"sort"
)

// PairedBootstrap resamples whole paired blocks and returns a sorted interval.
func PairedBootstrap(differences []float64, resamples int, seed int64) (float64, float64) {
	if len(differences) == 0 || resamples <= 0 {
		return 0, 0
	}
	rng := rand.New(rand.NewSource(seed))
	values := make([]float64, resamples)
	for i := range values {
		sum := 0.0
		for range differences {
			sum += differences[rng.Intn(len(differences))]
		}
		values[i] = sum / float64(len(differences))
	}
	sort.Float64s(values)
	return values[int(.025*float64(resamples))], values[int(.975*float64(resamples-1))]
}
