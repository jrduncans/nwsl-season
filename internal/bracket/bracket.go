// Package bracket builds a read-only tournament view from verified catalog
// topology and cached source games. It intentionally owns no persistence and
// never fills a tournament fact that is not supplied by the format or source.
package bracket

import (
	"fmt"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

// State is the closed presentation state of a source-backed bracket.
type State string

const (
	StateEmpty          State = "empty"
	StatePartial        State = "partial"
	StateReady          State = "ready"
	StateUnresolved     State = "unresolved"
	StateFormatMismatch State = "format_mismatch"

	// TBD is the deliberately visible value for an unfilled bracket entrant.
	TBD = "TBD"
)

// Diagnostic explains why an otherwise nonfatal source observation could not
// be represented by the verified topology.
type Diagnostic struct {
	Code, Message  string
	GameID, SlotID string
}

// Entrant is either one source-backed team identity or the explicit TBD
// placeholder. No name, seed, venue, or preferred-side inference is stored.
type Entrant struct {
	TeamID string
}

func tbdEntrant() Entrant { return Entrant{TeamID: TBD} }

// Slot is one immutable tournament node. Game is copied from the cache input;
// its participants take precedence over yet-to-be-known advanced entrants.
type Slot struct {
	ID          string
	SeedPair    *[2]int
	ByeSeed     int
	SourceOrder int
	Home        Entrant
	Away        Entrant
	Winner      Entrant
	Game        *cache.Game
}

// Round preserves the catalog's source-backed order.
type Round struct {
	ID, Label string
	Slots     []Slot
}

// View is the complete, immutable output of Build. SourceGames are retained
// even for a format mismatch so callers can show the factual fallback.
type View struct {
	State       State
	Rounds      []Round
	SourceGames []cache.Game
	Diagnostics []Diagnostic
}

// Build creates a defensive, source-backed bracket view. It places games only
// by verified round order and chronological source order. It deliberately does
// not inspect matchday, venue, duration, or game status to choose a slot.
func Build(format competition.BracketFormat, games []cache.Game) View {
	view := View{SourceGames: cloneGames(games)}
	format = format.Copy()
	if err := format.Validate(); err != nil {
		view.State = StateFormatMismatch
		view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "invalid_format", Message: err.Error()})
		return view
	}
	view.Rounds = shape(format)
	sorted, diagnostics := sourceGames(games)
	view.Diagnostics = append(view.Diagnostics, diagnostics...)
	if len(diagnostics) != 0 {
		view.State = StateFormatMismatch
		return view
	}
	if len(sorted) > len(format.Slots) {
		view.State = StateFormatMismatch
		view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "game_count_conflict", Message: fmt.Sprintf("received %d source games for %d verified slots", len(sorted), len(format.Slots))})
		return view
	}
	if len(sorted) == 0 {
		view.State = StateEmpty
		return view
	}

	unresolved, mismatch := place(&view, format, sorted)
	populateFixedAdvances(&view, format)
	if mismatch {
		view.State = StateFormatMismatch
		return view
	}
	if unresolved {
		view.State = StateUnresolved
		return view
	}
	if len(sorted) == len(format.Slots) {
		view.State = StateReady
	} else {
		view.State = StatePartial
	}
	return view
}

// place first assigns games to chronological rounds by the verified number of
// slots. Within a later fixed round it uses observed participants to select
// the one connection whose known winners match, so simultaneous/reversed
// semifinal kickoffs never choose a bracket side by time alone.
func place(view *View, format competition.BracketFormat, games []cache.Game) (unresolved, mismatch bool) {
	byID := slotIndex(view.Rounds)
	connections := connectionByDestination(format)
	cursor := 0
	for roundIndex := range view.Rounds {
		round := &view.Rounds[roundIndex]
		count := len(round.Slots)
		end := cursor + count
		if end > len(games) {
			end = len(games)
		}
		roundGames := games[cursor:end]
		cursor = end
		slots := roundSlots(round)
		var roundUnresolved, roundMismatch bool
		switch {
		case hasDestinationConnection(slots, connections):
			roundUnresolved, roundMismatch = placeFixedRound(view, slots, roundGames, byID, connections, earlierParticipants(view.Rounds[:roundIndex]))
		default:
			roundUnresolved, roundMismatch = placeOpeningRound(view, slots, roundGames)
		}
		unresolved = unresolved || roundUnresolved
		mismatch = mismatch || roundMismatch
	}
	return unresolved, mismatch
}

func roundSlots(round *Round) []*Slot {
	values := make([]*Slot, len(round.Slots))
	for index := range round.Slots {
		values[index] = &round.Slots[index]
	}
	return values
}

func connectionByDestination(format competition.BracketFormat) map[string]competition.BracketConnection {
	values := make(map[string]competition.BracketConnection, len(format.Connections))
	for _, connection := range format.Connections {
		values[connection.DestinationSlotID] = connection
	}
	return values
}

func hasDestinationConnection(slots []*Slot, connections map[string]competition.BracketConnection) bool {
	for _, slot := range slots {
		if _, found := connections[slot.ID]; found {
			return true
		}
	}
	return false
}

func placeOpeningRound(view *View, slots []*Slot, games []cache.Game) (bool, bool) {
	unresolved, mismatch := false, false
	participants := map[string]string{}
	ordered := append([]*Slot(nil), slots...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].SourceOrder == 0 || ordered[j].SourceOrder == 0 {
			return false
		}
		return ordered[i].SourceOrder < ordered[j].SourceOrder
	})
	for index, game := range games {
		if participantReuse(view, participants, game, ordered[index].ID) {
			mismatch = true
			continue
		}
		unresolved = placeGame(view, ordered[index], game) || unresolved
	}
	return unresolved, mismatch
}

// earlierParticipants lists every team placed in an earlier round. A bye
// team is the one semifinal participant absent from them.
func earlierParticipants(rounds []Round) map[string]bool {
	values := map[string]bool{}
	for _, round := range rounds {
		for _, slot := range round.Slots {
			if slot.Game != nil {
				values[slot.Game.HomeTeamID], values[slot.Game.AwayTeamID] = true, true
			}
		}
	}
	return values
}

func placeFixedRound(view *View, slots []*Slot, games []cache.Game, byID map[string]*Slot, connections map[string]competition.BracketConnection, earlier map[string]bool) (bool, bool) {
	unresolved, mismatch := false, false
	participants := map[string]string{}
	used := map[string]bool{}
	for _, game := range games {
		if participantReuse(view, participants, game, "") {
			mismatch = true
			continue
		}
		matches, unknown := matchingFixedSlots(slots, game, byID, connections, used, earlier)
		switch len(matches) {
		case 1:
			used[matches[0].ID] = true
			unresolved = placeGame(view, matches[0], game) || unresolved
		case 0:
			if unknown {
				unresolved = true
				view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "unresolved_placement", GameID: game.ASAID, Message: "later-round source game cannot be assigned until fixed prior-round winners are known"})
			} else {
				mismatch = true
				view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "impossible_participants", GameID: game.ASAID, Message: "source game participants do not match any fixed prior-round winners"})
			}
		default:
			mismatch = true
			view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "duplicate_placement", GameID: game.ASAID, Message: "source game matches more than one fixed destination slot"})
		}
	}
	return unresolved, mismatch
}

func matchingFixedSlots(slots []*Slot, game cache.Game, byID map[string]*Slot, connections map[string]competition.BracketConnection, used, earlier map[string]bool) (matches []*Slot, unknown bool) {
	for _, slot := range slots {
		if used[slot.ID] {
			continue
		}
		connection, found := connections[slot.ID]
		if !found {
			continue
		}
		winners := make([]string, 0, len(connection.SourceSlotIDs))
		for _, sourceID := range connection.SourceSlotIDs {
			winner := byID[sourceID].Winner.TeamID
			if winner == TBD {
				winners = nil
				unknown = true
				break
			}
			winners = append(winners, winner)
		}
		switch {
		case len(winners) != len(connection.SourceSlotIDs):
		case len(winners) == 1:
			if byeOpponent(game, winners[0], slot.Home.TeamID, earlier) {
				matches = append(matches, slot)
			}
		case sameParticipants(game.HomeTeamID, game.AwayTeamID, winners[0], winners[1]):
			matches = append(matches, slot)
		}
	}
	return matches, unknown
}

// byeOpponent reports whether a game pairs the prior winner with a team that
// played no earlier round, matching a known bye seed when one is supplied.
func byeOpponent(game cache.Game, winner, bye string, earlier map[string]bool) bool {
	other := game.HomeTeamID
	switch winner {
	case game.HomeTeamID:
		other = game.AwayTeamID
	case game.AwayTeamID:
	default:
		return false
	}
	return !earlier[other] && (bye == TBD || bye == other)
}

func participantReuse(view *View, participants map[string]string, game cache.Game, slotID string) bool {
	for _, team := range []string{game.HomeTeamID, game.AwayTeamID} {
		if prior, found := participants[team]; found {
			view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "participant_reuse", GameID: game.ASAID, SlotID: slotID, Message: "source participant appears in multiple games in the same round (first seen in " + prior + ")"})
			return true
		}
	}
	participants[game.HomeTeamID], participants[game.AwayTeamID] = game.ASAID, game.ASAID
	return false
}

func placeGame(view *View, slot *Slot, source cache.Game) (unresolved bool) {
	if slot.Game != nil {
		view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "duplicate_placement", GameID: source.ASAID, SlotID: slot.ID, Message: "multiple source games mapped to one verified slot"})
		return false
	}
	game := cloneGame(source)
	slot.Game = &game
	slot.Home = Entrant{TeamID: game.HomeTeamID}
	slot.Away = Entrant{TeamID: game.AwayTeamID}
	winner, unresolved, diagnostic := gameWinner(game)
	if diagnostic != nil {
		view.Diagnostics = append(view.Diagnostics, *diagnostic)
	}
	if winner != "" {
		slot.Winner = Entrant{TeamID: winner}
	}
	return unresolved
}

func populateFixedAdvances(view *View, format competition.BracketFormat) {
	byID := slotIndex(view.Rounds)
	for _, connection := range format.Connections {
		destination := byID[connection.DestinationSlotID]
		if destination.Game != nil {
			continue
		}
		winners := make([]string, 0, len(connection.SourceSlotIDs))
		for _, sourceID := range connection.SourceSlotIDs {
			winner := byID[sourceID].Winner.TeamID
			if winner == TBD {
				winners = nil
				break
			}
			winners = append(winners, winner)
		}
		switch {
		case len(winners) != len(connection.SourceSlotIDs):
		case len(winners) == 1:
			// The bye seed hosts; any known seed stays on the home side.
			destination.Away = Entrant{TeamID: winners[0]}
		default:
			destination.Home, destination.Away = Entrant{TeamID: winners[0]}, Entrant{TeamID: winners[1]}
		}
	}
}

func shape(format competition.BracketFormat) []Round {
	rounds := make([]Round, len(format.Rounds))
	byID := make(map[string]*Round, len(rounds))
	for index, source := range format.Rounds {
		rounds[index] = Round{ID: source.ID, Label: source.Label}
		byID[source.ID] = &rounds[index]
	}
	for _, source := range format.Slots {
		slot := Slot{ID: source.ID, ByeSeed: source.ByeSeed, SourceOrder: source.SourceOrder, Home: tbdEntrant(), Away: tbdEntrant(), Winner: tbdEntrant()}
		if source.SeedPair != nil {
			pair := *source.SeedPair
			slot.SeedPair = &pair
		}
		byID[source.RoundID].Slots = append(byID[source.RoundID].Slots, slot)
	}
	return rounds
}

func sourceGames(games []cache.Game) ([]cache.Game, []Diagnostic) {
	values := cloneGames(games)
	diagnostics := []Diagnostic{}
	seen := map[string]bool{}
	for _, game := range values {
		if game.ASAID == "" || seen[game.ASAID] {
			diagnostics = append(diagnostics, Diagnostic{Code: "duplicate_source_game", GameID: game.ASAID, Message: "source games need unique nonblank IDs"})
			continue
		}
		seen[game.ASAID] = true
		if game.HomeTeamID == "" || game.AwayTeamID == "" || game.HomeTeamID == game.AwayTeamID {
			diagnostics = append(diagnostics, Diagnostic{Code: "impossible_participants", GameID: game.ASAID, Message: "source game has blank or identical participants"})
		}
		if _, err := fixtures.ParseKickoff(game.KickoffUTC); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "invalid_kickoff", GameID: game.ASAID, Message: "source game has no valid chronological kickoff"})
		}
	}
	if len(diagnostics) != 0 {
		return values, diagnostics
	}
	sort.SliceStable(values, func(i, j int) bool {
		left, _ := fixtures.ParseKickoff(values[i].KickoffUTC)
		right, _ := fixtures.ParseKickoff(values[j].KickoffUTC)
		if !left.Equal(right) {
			return left.Before(right)
		}
		return values[i].ASAID < values[j].ASAID
	})
	return values, nil
}

func gameWinner(game cache.Game) (winner string, unresolved bool, diagnostic *Diagnostic) {
	if !game.HomeScore.Valid || !game.AwayScore.Valid {
		return "", false, nil
	}
	if game.HomeScore.Int64 > game.AwayScore.Int64 {
		return game.HomeTeamID, false, nil
	}
	if game.AwayScore.Int64 > game.HomeScore.Int64 {
		return game.AwayTeamID, false, nil
	}
	if game.Penalties.Valid && game.Penalties.Bool && game.HomePenalties.Valid && game.AwayPenalties.Valid && game.HomePenalties.Int64 >= 0 && game.AwayPenalties.Int64 >= 0 {
		if game.HomePenalties.Int64 > game.AwayPenalties.Int64 {
			return game.HomeTeamID, false, nil
		}
		if game.AwayPenalties.Int64 > game.HomePenalties.Int64 {
			return game.AwayTeamID, false, nil
		}
	}
	if game.Penalties.Valid && game.Penalties.Bool && (!game.HomePenalties.Valid || !game.AwayPenalties.Valid || game.HomePenalties.Int64 < 0 || game.AwayPenalties.Int64 < 0 || game.HomePenalties.Int64 == game.AwayPenalties.Int64) {
		return "", true, &Diagnostic{Code: "unresolved_penalties", GameID: game.ASAID, Message: "tied source game has no valid unequal shootout tally"}
	}
	return "", true, nil
}

func slotIndex(rounds []Round) map[string]*Slot {
	values := map[string]*Slot{}
	for round := range rounds {
		for slot := range rounds[round].Slots {
			values[rounds[round].Slots[slot].ID] = &rounds[round].Slots[slot]
		}
	}
	return values
}

func sameParticipants(home, away, first, second string) bool {
	return (home == first && away == second) || (home == second && away == first)
}

func cloneGames(games []cache.Game) []cache.Game {
	return append([]cache.Game(nil), games...)
}

func cloneGame(game cache.Game) cache.Game { return game }
