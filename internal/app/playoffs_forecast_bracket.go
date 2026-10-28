package app

import (
	"fmt"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

type playoffPositionTeam struct {
	Team        teamNameView
	Seed        int
	Text, Width string
	Probability float64
	Out         bool
}
type playoffPosition struct{ Teams []playoffPositionTeam }
type playoffForecastSlot struct {
	Label string
	Sides []playoffPosition
}
type playoffForecastRound struct {
	Label string
	Slots []playoffForecastSlot
}

func playoffBracketForecast(format competition.BracketFormat, state *simulation.Postseason, result simulation.Result) []playoffForecastRound {
	byID := map[string]simulation.TeamResult{}
	seeds := map[string]int{}
	for seed, id := range state.Seeds {
		seeds[id] = seed
	}
	for _, team := range result.Teams {
		byID[team.Team.ID] = team
	}
	sources := map[string][]string{}
	for _, connection := range format.Connections {
		sources[connection.DestinationSlotID] = connection.SourceSlotIDs
	}
	winners := map[string][]string{}
	// Walk catalog round order; venue assignments never change topology sides.
	var rounds []playoffForecastRound
	for ri, round := range format.Rounds {
		column := playoffForecastRound{Label: round.Label}
		index := 0
		for _, slot := range format.Slots {
			if slot.RoundID != round.ID {
				continue
			}
			index++
			sides := [2][]string{}
			if slot.SeedPair != nil {
				sides[0] = []string{state.Seeds[slot.SeedPair[0]]}
				sides[1] = []string{state.Seeds[slot.SeedPair[1]]}
			} else {
				from := sources[slot.ID]
				sides[0], sides[1] = winners[from[0]], winners[from[1]]
			}
			fixed := state.Slots[slot.ID].Winner
			if fixed == "" {
				fixed = state.Scenarios[slot.ID].Winner
			}
			if fixed != "" {
				winners[slot.ID] = []string{fixed}
			} else {
				winners[slot.ID] = append(append([]string{}, sides[0]...), sides[1]...)
			}
			if ri == 0 || state.Slots[slot.ID].Winner != "" {
				continue
			}
			label := singularRoundLabel(round.Label)
			if ri < len(format.Rounds)-1 {
				label = fmt.Sprintf("%s %d", label, index)
			}
			card := playoffForecastSlot{Label: label}
			for side, ids := range sides {
				position := playoffPosition{}
				for _, id := range ids {
					if id == "" {
						continue
					}
					team := byID[id]
					probability := result.BracketPositions[slot.ID][side][id]
					exact := team.ReachedRounds[round.ID] && probability == 1
					out := team.ChampionshipEliminated && !team.ReachedRounds[round.ID]
					text := forecastChance(probability, exact, out)
					if state.Unavailable != "" {
						text = "—"
						for _, eliminated := range state.Eliminated {
							if eliminated == id {
								text = "Out"
								out = true
							}
						}
					}
					width := percent(probability)
					if state.Unavailable != "" {
						width = ""
					}
					position.Teams = append(position.Teams, playoffPositionTeam{Team: teamName(team.Team), Seed: seeds[id], Text: text, Width: width, Probability: probability, Out: out})
				}
				sortPositionTeams(position.Teams)
				card.Sides = append(card.Sides, position)
			}
			column.Slots = append(column.Slots, card)
		}
		if len(column.Slots) > 0 {
			rounds = append(rounds, column)
		}
	}
	champion := playoffPosition{}
	for seed, id := range state.Seeds {
		if seed > len(format.Slots)+1 {
			continue
		}
		team := byID[id]
		out := team.ChampionshipEliminated
		text := forecastChance(team.ChampionshipProbability, team.ChampionshipClinched, out)
		if out {
			text = "Out"
		}
		if state.Unavailable != "" {
			text = "—"
			for _, eliminated := range state.Eliminated {
				if eliminated == id {
					text = "Out"
					out = true
				}
			}
		}
		width := percent(team.ChampionshipProbability)
		if state.Unavailable != "" {
			width = ""
		}
		champion.Teams = append(champion.Teams, playoffPositionTeam{Team: teamName(team.Team), Seed: seed, Text: text, Width: width, Probability: team.ChampionshipProbability, Out: out})
	}
	sortPositionTeams(champion.Teams)
	rounds = append(rounds, playoffForecastRound{Label: "Champion", Slots: []playoffForecastSlot{{Label: "Champion", Sides: []playoffPosition{champion}}}})
	return rounds
}

func sortPositionTeams(teams []playoffPositionTeam) {
	sort.Slice(teams, func(i, j int) bool {
		a, b := teams[i], teams[j]
		if a.Out != b.Out {
			return !a.Out
		}
		if a.Probability != b.Probability {
			return a.Probability > b.Probability
		}
		return a.Seed < b.Seed
	})
}
