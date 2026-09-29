package simulation

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type playoffPair struct{ home, away string }

type playoffMatch struct {
	distribution  forecast.Distribution
	tieHomeChance float64
}

type playoffSimulation struct {
	format      competition.BracketFormat
	matches     map[playoffPair]playoffMatch
	connections map[string][]string
	finalID     string
}

func preparePlayoffs(request Request, predictor forecast.Predictor) (*playoffSimulation, error) {
	if request.PlayoffBracket == nil {
		return nil, nil
	}
	format := request.PlayoffBracket.Copy()
	if err := format.Validate(); err != nil {
		return nil, fmt.Errorf("forecast playoff bracket: %w", err)
	}
	if format.AdvancementPolicy != competition.AdvancementFixed {
		return nil, fmt.Errorf("forecast playoff bracket needs fixed advancement")
	}
	if err := validatePlayoffBracket(format, request.PlayoffPlaces); err != nil {
		return nil, err
	}
	playoffs := &playoffSimulation{format: format, matches: make(map[playoffPair]playoffMatch, len(request.Teams)*(len(request.Teams)-1)), connections: make(map[string][]string, len(format.Connections))}
	roundIndex := make(map[string]int, len(format.Rounds))
	for index, round := range format.Rounds {
		roundIndex[round.ID] = index
	}
	sort.Slice(playoffs.format.Slots, func(i, j int) bool {
		left, right := playoffs.format.Slots[i], playoffs.format.Slots[j]
		if roundIndex[left.RoundID] != roundIndex[right.RoundID] {
			return roundIndex[left.RoundID] < roundIndex[right.RoundID]
		}
		return left.ID < right.ID
	})
	for _, connection := range format.Connections {
		playoffs.connections[connection.DestinationSlotID] = connection.SourceSlotIDs
	}
	for _, slot := range format.Slots {
		if slot.RoundID == format.Rounds[len(format.Rounds)-1].ID {
			playoffs.finalID = slot.ID
		}
	}
	for _, home := range request.Teams {
		for _, away := range request.Teams {
			if home.ID == away.ID {
				continue
			}
			game := standings.Game{ID: "playoff:" + home.ID + ":" + away.ID, Status: RemainingStatus, HomeTeamID: home.ID, AwayTeamID: away.ID}
			var distribution forecast.Distribution
			var err error
			if postseason, ok := predictor.(forecast.PostseasonPredictor); ok {
				distribution, err = postseason.PostseasonDistribution(game)
			} else {
				distribution, err = predictor.Distribution(game)
			}
			if err != nil {
				return nil, fmt.Errorf("forecast playoff pairing %q and %q: %w", home.ID, away.ID, err)
			}
			odds := distribution.Outcomes()
			tieHomeChance := .5
			if decisive := odds.HomeWin + odds.AwayWin; decisive > 0 {
				tieHomeChance = odds.HomeWin / decisive
			}
			playoffs.matches[playoffPair{home.ID, away.ID}] = playoffMatch{distribution: distribution, tieHomeChance: tieHomeChance}
		}
	}
	return playoffs, nil
}

func validatePlayoffBracket(format competition.BracketFormat, places int) error {
	seeds := make(map[int]bool, places)
	connections := make(map[string][]string, len(format.Connections))
	sources := make(map[string]bool, len(format.Slots))
	for _, slot := range format.Slots {
		if slot.SeedPair != nil {
			for _, seed := range slot.SeedPair {
				if seed > places {
					return fmt.Errorf("forecast playoff seed %d exceeds %d places", seed, places)
				}
				seeds[seed] = true
			}
		}
	}
	for _, connection := range format.Connections {
		if len(connection.SourceSlotIDs) != 2 {
			return fmt.Errorf("forecast playoff slot %q needs two prior winners", connection.DestinationSlotID)
		}
		connections[connection.DestinationSlotID] = connection.SourceSlotIDs
		for _, source := range connection.SourceSlotIDs {
			if sources[source] {
				return fmt.Errorf("forecast playoff slot %q advances more than once", source)
			}
			sources[source] = true
		}
	}
	if len(seeds) != places || len(format.Slots) != places-1 {
		return fmt.Errorf("forecast playoff bracket does not cover all %d seeds", places)
	}
	finals := 0
	for _, slot := range format.Slots {
		sourcesForSlot := connections[slot.ID]
		if (slot.SeedPair == nil) == (len(sourcesForSlot) == 0) {
			return fmt.Errorf("forecast playoff slot %q must have a seed pair or two prior winners", slot.ID)
		}
		if !sources[slot.ID] {
			finals++
			if slot.RoundID != format.Rounds[len(format.Rounds)-1].ID {
				return fmt.Errorf("forecast playoff final is outside the last round")
			}
		}
	}
	if finals != 1 {
		return fmt.Errorf("forecast playoff bracket needs one final")
	}
	return nil
}

func (p *playoffSimulation) champion(table []standings.TableRow, rng *rand.Rand) string {
	seeds := playoffSeeds(table, rng)
	seedByID := make(map[string]int, len(seeds))
	for index, id := range seeds {
		seedByID[id] = index + 1
	}
	winners := make(map[string]string, len(p.format.Slots))
	finalRound := p.format.Rounds[len(p.format.Rounds)-1].ID
	for _, slot := range p.format.Slots {
		var first, second string
		if slot.SeedPair != nil {
			first, second = seeds[slot.SeedPair[0]-1], seeds[slot.SeedPair[1]-1]
		} else {
			from := p.connections[slot.ID]
			first, second = winners[from[0]], winners[from[1]]
		}
		if seedByID[first] > seedByID[second] {
			first, second = second, first
		}
		neutral := slot.RoundID == finalRound
		winners[slot.ID] = p.winner(first, second, neutral, rng)
	}
	return winners[p.finalID]
}

func (p *playoffSimulation) winner(first, second string, neutral bool, rng *rand.Rand) string {
	home, away := first, second
	// Averaging both home assignments removes a systematic venue advantage
	// from the designated-site final without fitting a separate neutral model.
	if neutral && rng.Intn(2) == 0 {
		home, away = away, home
	}
	match := p.matches[playoffPair{home, away}]
	score := match.distribution.Sample(rng)
	if score.Home > score.Away || (score.Home == score.Away && rng.Float64() < match.tieHomeChance) {
		return home
	}
	return away
}

func playoffSeeds(table []standings.TableRow, rng *rand.Rand) []string {
	seeds := make([]string, len(table))
	groups := make(map[string][]int)
	for index, row := range table {
		seeds[index] = row.Team.ID
		if row.TieBreak.Undetermined {
			key := strings.Join(row.TieBreak.TiedTeamIDs, "\x00")
			groups[key] = append(groups[key], index)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		indices := groups[key]
		for i := len(indices) - 1; i > 0; i-- {
			j := rng.Intn(i + 1)
			seeds[indices[i]], seeds[indices[j]] = seeds[indices[j]], seeds[indices[i]]
		}
	}
	return seeds
}
