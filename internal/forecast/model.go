// Package forecast defines scoreline models used by the season simulator.
// It intentionally has no HTTP, database, or cache dependencies.
package forecast

import (
	"math/rand"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// Info identifies and explains a versioned forecast model.
type Info struct {
	ID          string
	Name        string
	Description string
}

// Scoreline is a sampled fixture result.
type Scoreline struct {
	Home int
	Away int
}

// Distribution can sample one scoreline using caller-owned randomness.
type Distribution interface {
	Sample(*rand.Rand) Scoreline
}

// Predictor produces a scoreline distribution for one remaining fixture after
// a model has been fitted to completed results.
type Predictor interface {
	Distribution(standings.Game) (Distribution, error)
}

// Model fits completed results and exposes distributions for future fixtures.
type Model interface {
	Info() Info
	Fit([]standings.Team, []standings.Game) (Predictor, error)
}
