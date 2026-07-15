package forecast

import (
	"fmt"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

const currentPaceID = "current-pace-v1"

func NewCurrentPaceV1() Model { return currentPaceV1{} }

type currentPaceV1 struct{}

func (currentPaceV1) Info() Info {
	return Info{ID: currentPaceID, Name: "Current pace", Description: "Observed points pace translated into a scoring-rate multiplier.", Inputs: "Completed match scores, points, and the published fixture list.", Assumptions: "Independent Poisson scoring; all past success becomes scoring pace, without opponent defensive adjustment.", MethodPath: "/docs/11-xg-and-models.md#current-pace-v1"}
}

type paceTotals struct{ played, points int }
type pacePredictor struct {
	teams                         map[string]paceTotals
	homeRate, awayRate, leaguePPG float64
}

func (currentPaceV1) Fit(input FitInput) (Predictor, error) {
	teams, err := validateTeams(input.Teams)
	if err != nil {
		return nil, err
	}
	values := make(map[string]paceTotals, len(teams))
	for id := range teams {
		values[id] = paceTotals{}
	}
	var matches, homeGoals, awayGoals, totalPoints int
	for _, game := range input.Games {
		if game.Status != standings.CompletedStatus {
			continue
		}
		if game.HomeScore == nil || game.AwayScore == nil || *game.HomeScore < 0 || *game.AwayScore < 0 {
			return nil, fmt.Errorf("completed game %q has an invalid score", game.ID)
		}
		h, hok := values[game.HomeTeamID]
		a, aok := values[game.AwayTeamID]
		if !hok || !aok {
			return nil, fmt.Errorf("completed game %q references an unknown team", game.ID)
		}
		hp, ap := 0, 0
		if *game.HomeScore > *game.AwayScore {
			hp = 3
		} else if *game.HomeScore < *game.AwayScore {
			ap = 3
		} else {
			hp = 1
			ap = 1
		}
		h.played++
		h.points += hp
		a.played++
		a.points += ap
		values[game.HomeTeamID] = h
		values[game.AwayTeamID] = a
		matches++
		homeGoals += *game.HomeScore
		awayGoals += *game.AwayScore
		totalPoints += hp + ap
	}
	homeRate := (float64(homeGoals) + leaguePriorGames*priorHomeGoals) / (float64(matches) + leaguePriorGames)
	awayRate := (float64(awayGoals) + leaguePriorGames*priorAwayGoals) / (float64(matches) + leaguePriorGames)
	leaguePPG := (float64(totalPoints) + 40*1.35) / (2*float64(matches) + 40)
	return pacePredictor{values, homeRate, awayRate, leaguePPG}, nil
}
func (p pacePredictor) SeedMaterial() []byte { return []byte{} }
func (p pacePredictor) Distribution(game standings.Game) (Distribution, error) {
	if game.Status != "PreMatch" {
		return nil, fmt.Errorf("fixture %q is not remaining", game.ID)
	}
	h, hok := p.teams[game.HomeTeamID]
	a, aok := p.teams[game.AwayTeamID]
	if !hok || !aok {
		return nil, fmt.Errorf("fixture %q references an unknown team", game.ID)
	}
	hp := (float64(h.points) + teamPriorGames*p.leaguePPG) / (float64(h.played) + teamPriorGames)
	ap := (float64(a.points) + teamPriorGames*p.leaguePPG) / (float64(a.played) + teamPriorGames)
	return poissonDistribution{homeRate: clamp(p.homeRate * hp / p.leaguePPG), awayRate: clamp(p.awayRate * ap / p.leaguePPG)}, nil
}
