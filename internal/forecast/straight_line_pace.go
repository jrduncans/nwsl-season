package forecast

import (
	"fmt"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

const straightLinePaceID = "straight-line-pace-v1"

// NewStraightLinePaceV1 returns the deliberately simple evaluation baseline.
// It carries each team's observed points pace forward and treats every future
// fixture alike, irrespective of where it is played.
func NewStraightLinePaceV1() Model { return straightLinePaceV1{} }

type straightLinePaceV1 struct{}

func (straightLinePaceV1) Info() Info {
	return Info{
		ID:          straightLinePaceID,
		Name:        "Straight-line pace",
		Description: "A deliberately simple baseline that carries each team's points pace forward without a home-field adjustment.",
		Inputs:      "Completed match scores, points earned, and the remaining schedule.",
		Assumptions: "Every remaining fixture is treated alike. The model does not adjust for home field or separately adjust for the strength of past opponents.",
	}
}

type straightLinePacePredictor struct {
	teams               map[string]paceTotals
	leaguePPG, goalRate float64
}

func (straightLinePaceV1) Fit(input FitInput) (Predictor, error) {
	teams, err := validateTeams(input.Teams)
	if err != nil {
		return nil, err
	}
	values := make(map[string]paceTotals, len(teams))
	for id := range teams {
		values[id] = paceTotals{}
	}
	var matches, goals, totalPoints int
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
			hp, ap = 1, 1
		}
		h.played++
		h.points += hp
		a.played++
		a.points += ap
		values[game.HomeTeamID], values[game.AwayTeamID] = h, a
		matches++
		goals += *game.HomeScore + *game.AwayScore
		totalPoints += hp + ap
	}
	goalRate := (float64(goals) + leaguePriorGames*(priorHomeGoals+priorAwayGoals)) / (2*float64(matches) + 2*leaguePriorGames)
	leaguePPG := (float64(totalPoints) + 40*1.35) / (2*float64(matches) + 40)
	return straightLinePacePredictor{teams: values, leaguePPG: leaguePPG, goalRate: goalRate}, nil
}

func (p straightLinePacePredictor) SeedMaterial() []byte { return []byte{} }

func (p straightLinePacePredictor) Distribution(game standings.Game) (Distribution, error) {
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
	return poissonDistribution{homeRate: clamp(p.goalRate * hp / p.leaguePPG), awayRate: clamp(p.goalRate * ap / p.leaguePPG)}, nil
}
