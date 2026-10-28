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
	// fixedSeeds and actual are set only for postseason progress; the
	// remaining slots are sampled.
	fixedSeeds     []string
	actual         map[string]PostseasonMatch
	contenders     map[string]bool
	actualChampion string
}

// CheckPostseason reports whether Run would accept the request's postseason
// progress, so callers can withhold odds with an explanation instead of
// failing the forecast. It does not sample or build predictions.
func CheckPostseason(request Request) error {
	if request.Postseason == nil || request.Postseason.Unavailable != "" || request.PlayoffBracket == nil {
		return nil
	}
	_, err := playoffStructure(request)
	return err
}

func preparePlayoffs(request Request, predictor forecast.Predictor) (*playoffSimulation, error) {
	if request.Postseason != nil && request.Postseason.Unavailable != "" {
		return nil, nil
	}
	if request.PlayoffBracket == nil {
		return nil, nil
	}
	playoffs, err := playoffStructure(request)
	if err != nil {
		return nil, err
	}
	playoffs.matches = make(map[playoffPair]playoffMatch, len(request.Teams)*(len(request.Teams)-1))
	for _, home := range request.Teams {
		for _, away := range request.Teams {
			if home.ID == away.ID {
				continue
			}
			match, err := preparePlayoffMatch(predictor, home.ID, away.ID)
			if err != nil {
				return nil, err
			}

			playoffs.matches[playoffPair{home.ID, away.ID}] = match
		}
	}
	return playoffs, nil
}

// MatchOutlook contains regulation results and chances to advance, oriented to
// the supplied home and away teams. A neutral match averages both assignments.
type MatchOutlook struct {
	forecast.OutcomeProbabilities
	HomeAdvance, AwayAdvance float64
}

func tieChance(odds forecast.OutcomeProbabilities) float64 {
	if decisive := odds.HomeWin + odds.AwayWin; decisive > 0 {
		return odds.HomeWin / decisive
	}
	return .5
}

func preparePlayoffMatch(predictor forecast.Predictor, home, away string) (playoffMatch, error) {
	game := standings.Game{ID: "playoff:" + home + ":" + away, Status: RemainingStatus, HomeTeamID: home, AwayTeamID: away}
	var distribution forecast.Distribution
	var err error
	if postseason, ok := predictor.(forecast.PostseasonPredictor); ok {
		distribution, err = postseason.PostseasonDistribution(game)
	} else {
		distribution, err = predictor.Distribution(game)
	}
	if err != nil {
		return playoffMatch{}, fmt.Errorf("forecast playoff pairing %q and %q: %w", home, away, err)
	}
	return playoffMatch{distribution: distribution, tieHomeChance: tieChance(distribution.Outcomes())}, nil
}

// PlayoffMatchOutlook uses the simulator's distribution and draw resolution.
func PlayoffMatchOutlook(predictor forecast.Predictor, home, away string, neutral bool) (MatchOutlook, error) {
	match, err := preparePlayoffMatch(predictor, home, away)
	if err != nil {
		return MatchOutlook{}, err
	}
	odds := match.distribution.Outcomes()
	advance := odds.HomeWin + odds.Draw*match.tieHomeChance
	if neutral {
		reverse, err := preparePlayoffMatch(predictor, away, home)
		if err != nil {
			return MatchOutlook{}, err
		}
		other := reverse.distribution.Outcomes()
		odds = forecast.OutcomeProbabilities{HomeWin: (odds.HomeWin + other.AwayWin) / 2, Draw: (odds.Draw + other.Draw) / 2, AwayWin: (odds.AwayWin + other.HomeWin) / 2}
		advance = (advance + other.AwayWin + other.Draw*(1-reverse.tieHomeChance)) / 2
	}
	return MatchOutlook{OutcomeProbabilities: odds, HomeAdvance: advance, AwayAdvance: 1 - advance}, nil
}

// playoffStructure validates the bracket and any postseason progress without
// building match predictions.
func playoffStructure(request Request) (*playoffSimulation, error) {
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
	playoffs := &playoffSimulation{format: format, connections: make(map[string][]string, len(format.Connections))}
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
	if err := playoffs.applyPostseason(request); err != nil {
		return nil, err
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
	return p.run(table, rng, nil, nil)
}

func (p *playoffSimulation) run(table []standings.TableRow, rng *rand.Rand, counts map[string]*accumulator, positions map[string][2]map[string]float64) string {
	seeds := p.fixedSeeds
	if seeds == nil {
		seeds = playoffSeeds(table, rng)
	}
	seedByID := make(map[string]int, len(seeds))
	for index, id := range seeds {
		seedByID[id] = index + 1
	}
	winners := make(map[string]string, len(p.format.Slots))
	finalRound := p.format.Rounds[len(p.format.Rounds)-1].ID
	for _, slot := range p.format.Slots {
		actual := p.actual[slot.ID]
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
		if actual.Home != "" {
			first, second = actual.Home, actual.Away
		}
		winner := actual.Winner
		if winner == "" {
			winner = p.winner(first, second, neutral, rng)
		}
		winners[slot.ID] = winner
		if counts != nil {
			counts[first].reach[slot.RoundID]++
			counts[second].reach[slot.RoundID]++
			loser := first
			if loser == winner {
				loser = second
			}
			counts[loser].ends[slot.RoundID]++
			if slot.ID == p.finalID {
				counts[winner].ends["champion"]++
			}
			sides := positions[slot.ID]
			// A position follows topology, independent of the published venue.
			if slot.SeedPair != nil {
				sides[0][seeds[slot.SeedPair[0]-1]]++
				sides[1][seeds[slot.SeedPair[1]-1]]++
			} else {
				from := p.connections[slot.ID]
				sides[0][winners[from[0]]]++
				sides[1][winners[from[1]]]++
			}
		}
	}
	return winners[p.finalID]
}

func (p *playoffSimulation) applyPostseason(request Request) error {
	state := request.Postseason
	if state == nil {
		return nil
	}
	for _, game := range request.Games {
		if game.Status != standings.CompletedStatus {
			return fmt.Errorf("postseason requires final regular-season results")
		}
	}
	known := map[string]bool{}
	for _, team := range request.Teams {
		known[team.ID] = true
	}
	p.contenders = map[string]bool{}
	if len(state.Seeds) != request.PlayoffPlaces {
		return fmt.Errorf("postseason needs all final playoff seeds")
	}
	for seed := 1; seed <= request.PlayoffPlaces; seed++ {
		id := state.Seeds[seed]
		if !known[id] || p.contenders[id] {
			return fmt.Errorf("postseason needs distinct resolved final seeds")
		}
		p.contenders[id] = true
	}
	// Validate progress against actual prior winners before any sampling.
	winners := map[string]string{}
	seen := map[string]bool{}
	for _, slot := range p.format.Slots {
		actual, published := state.Slots[slot.ID]
		if published {
			seen[slot.ID] = true
		}
		var first, second string
		if slot.SeedPair != nil {
			first, second = state.Seeds[slot.SeedPair[0]], state.Seeds[slot.SeedPair[1]]
		} else {
			from := p.connections[slot.ID]
			first, second = winners[from[0]], winners[from[1]]
		}
		samePair := (actual.Home == first && actual.Away == second) || (actual.Home == second && actual.Away == first)
		if published && (first == "" || second == "" || !samePair) {
			return fmt.Errorf("postseason slot %q disagrees with final seeds or actual advancement", slot.ID)
		}
		if actual.Winner != "" {
			if actual.Winner != first && actual.Winner != second {
				return fmt.Errorf("postseason slot %q has an invalid winner", slot.ID)
			}
			winners[slot.ID] = actual.Winner
			loser := first
			if loser == actual.Winner {
				loser = second
			}
			delete(p.contenders, loser)
		}
	}
	if len(seen) != len(state.Slots) {
		return fmt.Errorf("postseason includes unknown bracket slots")
	}
	// Scenarios cannot unlock another scenario: only source-backed or
	// seed-derived participants establish an eligible upcoming slot.
	actualSlots := make(map[string]PostseasonMatch, len(state.Slots)+len(state.Scenarios))
	for id, match := range state.Slots {
		actualSlots[id] = match
	}
	scenarioSeen := 0
	for _, slot := range p.format.Slots {
		scenario, ok := state.Scenarios[slot.ID]
		if !ok {
			continue
		}
		scenarioSeen++
		actual := state.Slots[slot.ID]
		var first, second string
		if slot.SeedPair != nil {
			first, second = state.Seeds[slot.SeedPair[0]], state.Seeds[slot.SeedPair[1]]
		} else {
			from := p.connections[slot.ID]
			first, second = winners[from[0]], winners[from[1]]
		}
		samePair := (scenario.Home == first && scenario.Away == second) || (scenario.Home == second && scenario.Away == first)
		if actual.Winner != "" || first == "" || second == "" || !samePair || (scenario.Winner != first && scenario.Winner != second) {
			return fmt.Errorf("postseason scenario %q is stale or invalid", slot.ID)
		}
		if actual.Home != "" {
			scenario.Home, scenario.Away = actual.Home, actual.Away
		}
		actualSlots[slot.ID] = scenario
		loser := first
		if loser == scenario.Winner {
			loser = second
		}
		delete(p.contenders, loser)
	}
	if scenarioSeen != len(state.Scenarios) {
		return fmt.Errorf("postseason scenarios include unknown bracket slots")
	}
	p.fixedSeeds = make([]string, request.PlayoffPlaces)
	for seed, id := range state.Seeds {
		p.fixedSeeds[seed-1] = id
	}
	p.actual = actualSlots
	p.actualChampion = actualSlots[p.finalID].Winner
	return nil
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
