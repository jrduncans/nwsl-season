// Package forecast defines scoreline models used by the season simulator.
// It intentionally has no HTTP, database, or cache dependencies.
package forecast

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

// Info identifies and explains a versioned forecast model.
type Info struct {
	ID          string
	Name        string
	Description string
	Inputs      string
	Assumptions string
}

// ExpectedGoals is the normalized team-model xG observation for one game.
type ExpectedGoals struct {
	GameID     string
	Home, Away float64
}

// FitInput keeps forecast-only inputs distinct from official standings.
type FitInput struct {
	Teams  []standings.Team
	Games  []standings.Game
	XGoals map[string]ExpectedGoals
}

type OutcomeProbabilities struct{ HomeWin, Draw, AwayWin float64 }

// Scoreline is a sampled fixture result.
type Scoreline struct {
	Home int
	Away int
}

// Distribution can sample one scoreline using caller-owned randomness.
type Distribution interface {
	Sample(*rand.Rand) Scoreline
	Outcomes() OutcomeProbabilities
}

// Predictor produces a scoreline distribution for one remaining fixture after
// a model has been fitted to completed results.
type Predictor interface {
	Distribution(standings.Game) (Distribution, error)
	SeedMaterial() []byte
}

// Model fits completed results and exposes distributions for future fixtures.
type Model interface {
	Info() Info
	Fit(FitInput) (Predictor, error)
}

func validateTeams(teams []standings.Team) (map[string]struct{}, error) {
	known := make(map[string]struct{}, len(teams))
	for _, team := range teams {
		if team.ID == "" {
			return nil, fmt.Errorf("forecast team has an empty ID")
		}
		if _, exists := known[team.ID]; exists {
			return nil, fmt.Errorf("duplicate forecast team %q", team.ID)
		}
		known[team.ID] = struct{}{}
	}
	return known, nil
}

func validXG(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 }
