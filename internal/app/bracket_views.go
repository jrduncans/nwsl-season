package app

import (
	"fmt"

	"github.com/jrduncans/nwsl-season/internal/bracket"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

type bracketSourceView struct{ ID, Label string }

func bracketSlotLabels(format competition.BracketFormat) map[string]string {
	labels := map[string]string{}
	for _, round := range format.Rounds {
		var ids []string
		for _, slot := range format.Slots {
			if slot.RoundID == round.ID {
				ids = append(ids, slot.ID)
			}
		}
		for i, id := range ids {
			label := round.Label
			if len(ids) > 1 {
				label = fmt.Sprintf("%s %d", singularRoundLabel(round.Label), i+1)
			}
			labels[id] = label
		}
	}
	return labels
}

// Feeder paths come from the catalog. Match home/away order may reverse either
// side, so recorded participants identify their feeder when results allow.
// A bye slot's prior winner arrives on the away side until a game says
// otherwise, facing the bye seed.
func bracketSources(format competition.BracketFormat, view bracket.View) map[string][2]bracketSourceView {
	labels := bracketSlotLabels(format)
	sources := map[string][]string{}
	for _, connection := range format.Connections {
		sources[connection.DestinationSlotID] = connection.SourceSlotIDs
	}
	winners := map[string]string{}
	for _, round := range view.Rounds {
		for _, slot := range round.Slots {
			winners[slot.ID] = slot.Winner.TeamID
		}
	}
	source := func(id string) bracketSourceView {
		return bracketSourceView{ID: id, Label: "Winner of " + labels[id]}
	}
	result := map[string][2]bracketSourceView{}
	for _, round := range view.Rounds {
		for _, slot := range round.Slots {
			from := sources[slot.ID]
			entrants := [2]string{slot.Home.TeamID, slot.Away.TeamID}
			known := func(side int, feeder string) bool {
				winner := winners[feeder]
				return winner != "" && winner != bracket.TBD && entrants[side] == winner
			}
			var sides [2]bracketSourceView
			switch len(from) {
			case 1:
				feederSide := 1
				if known(0, from[0]) {
					feederSide = 0
				}
				sides[feederSide] = source(from[0])
				sides[1-feederSide] = bracketSourceView{Label: "First-round bye"}
			case 2:
				sides[0], sides[1] = source(from[0]), source(from[1])
				if known(0, from[1]) || known(1, from[0]) {
					sides[0], sides[1] = sides[1], sides[0]
				}
			}
			result[slot.ID] = sides
		}
	}
	return result
}
