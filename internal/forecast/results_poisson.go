package forecast

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	resultsPoissonID = "results-poisson-v1"

	priorHomeGoals   = 1.50
	priorAwayGoals   = 1.20
	leaguePriorGames = 20.0
	teamPriorGames   = 8.0
	minimumRate      = 0.20
	maximumRate      = 4.50
)

// NewResultsPoissonV1 returns the Phase 10 results-based score model.
func NewResultsPoissonV1() Model { return resultsPoissonV1{} }

type resultsPoissonV1 struct{}

func (resultsPoissonV1) Info() Info {
	return Info{
		ID:          resultsPoissonID,
		Name:        "Results Poisson",
		Description: "Completed goals with league and team shrinkage plus observed home advantage.",
	}
}

type teamTotals struct {
	played   int
	forGoals int
	against  int
}

type resultsPredictor struct {
	teams      map[string]teamTotals
	homeRate   float64
	awayRate   float64
	leagueRate float64
}

func (resultsPoissonV1) Fit(teams []standings.Team, games []standings.Game) (Predictor, error) {
	teamTotalsByID := make(map[string]teamTotals, len(teams))
	for _, team := range teams {
		if team.ID == "" {
			return nil, fmt.Errorf("forecast team has an empty ID")
		}
		if _, exists := teamTotalsByID[team.ID]; exists {
			return nil, fmt.Errorf("duplicate forecast team %q", team.ID)
		}
		teamTotalsByID[team.ID] = teamTotals{}
	}

	var completed, homeGoals, awayGoals int
	for _, game := range games {
		if game.Status != standings.CompletedStatus {
			continue
		}
		if game.HomeScore == nil || game.AwayScore == nil {
			return nil, fmt.Errorf("completed game %q has no score", game.ID)
		}
		if *game.HomeScore < 0 || *game.AwayScore < 0 {
			return nil, fmt.Errorf("completed game %q has a negative score", game.ID)
		}
		home, homeKnown := teamTotalsByID[game.HomeTeamID]
		away, awayKnown := teamTotalsByID[game.AwayTeamID]
		if !homeKnown || !awayKnown {
			return nil, fmt.Errorf("completed game %q references an unknown team", game.ID)
		}
		home.played++
		home.forGoals += *game.HomeScore
		home.against += *game.AwayScore
		away.played++
		away.forGoals += *game.AwayScore
		away.against += *game.HomeScore
		teamTotalsByID[game.HomeTeamID] = home
		teamTotalsByID[game.AwayTeamID] = away
		completed++
		homeGoals += *game.HomeScore
		awayGoals += *game.AwayScore
	}

	homeRate := (float64(homeGoals) + leaguePriorGames*priorHomeGoals) / (float64(completed) + leaguePriorGames)
	awayRate := (float64(awayGoals) + leaguePriorGames*priorAwayGoals) / (float64(completed) + leaguePriorGames)
	return resultsPredictor{
		teams:      teamTotalsByID,
		homeRate:   homeRate,
		awayRate:   awayRate,
		leagueRate: (homeRate + awayRate) / 2,
	}, nil
}

func (p resultsPredictor) Distribution(game standings.Game) (Distribution, error) {
	if game.Status != "PreMatch" {
		return nil, fmt.Errorf("fixture %q is not remaining", game.ID)
	}
	home, homeKnown := p.teams[game.HomeTeamID]
	away, awayKnown := p.teams[game.AwayTeamID]
	if !homeKnown || !awayKnown {
		return nil, fmt.Errorf("fixture %q references an unknown team", game.ID)
	}
	homeAttack, homeDefence := strengths(home, p.leagueRate)
	awayAttack, awayDefence := strengths(away, p.leagueRate)
	return poissonDistribution{
		homeRate: clamp(p.homeRate * homeAttack * awayDefence),
		awayRate: clamp(p.awayRate * awayAttack * homeDefence),
	}, nil
}

func strengths(t teamTotals, leagueRate float64) (attack, defence float64) {
	attack = ((float64(t.forGoals) + teamPriorGames*leagueRate) / (float64(t.played) + teamPriorGames)) / leagueRate
	defence = ((float64(t.against) + teamPriorGames*leagueRate) / (float64(t.played) + teamPriorGames)) / leagueRate
	return attack, defence
}

func clamp(value float64) float64 {
	return math.Max(minimumRate, math.Min(maximumRate, value))
}

type poissonDistribution struct {
	homeRate float64
	awayRate float64
}

func (d poissonDistribution) Sample(rng *rand.Rand) Scoreline {
	return Scoreline{Home: poisson(rng, d.homeRate), Away: poisson(rng, d.awayRate)}
}

// poisson uses Knuth's algorithm. Phase 10 clamps rates to a small value, so
// the simple algorithm remains quick and easy to audit.
func poisson(rng *rand.Rand, rate float64) int {
	limit := math.Exp(-rate)
	product := 1.0
	count := 0
	for product > limit {
		count++
		product *= rng.Float64()
	}
	return count - 1
}
