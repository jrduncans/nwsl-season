package competition

import (
	"fmt"
	"strings"
)

// AdvancementPolicy describes how a stage advances teams between bracket
// rounds. The closed set is intentional: a catalog entry must not silently
// acquire an interpretation that has not been verified.
type AdvancementPolicy string

const (
	AdvancementPolicyFixed                        AdvancementPolicy = "fixed"
	AdvancementPolicyHistoricallyObservedReseeded AdvancementPolicy = "historically_observed_reseeded"
	AdvancementPolicySingleFinal                  AdvancementPolicy = "single_final"

	// Short aliases make the policy names convenient at call sites while the
	// AdvancementPolicy-prefixed names remain unambiguous in API docs.
	AdvancementFixed                        = AdvancementPolicyFixed
	AdvancementHistoricallyObservedReseeded = AdvancementPolicyHistoricallyObservedReseeded
	AdvancementSingleFinal                  = AdvancementPolicySingleFinal
)

const bracketFormatVersion = 1

type BracketRound struct {
	ID    string
	Label string
}

type BracketSlot struct {
	ID          string
	RoundID     string
	SeedPair    *[2]int
	SourceOrder int
}

// BracketConnection groups all source slots feeding one destination slot.
// Grouping the sources keeps a destination unique while representing the two
// winners that normally feed a later knockout slot.
type BracketConnection struct {
	SourceSlotIDs     []string
	DestinationSlotID string
}

type BracketFormat struct {
	Version           int
	Rounds            []BracketRound
	Slots             []BracketSlot
	Connections       []BracketConnection
	AdvancementPolicy AdvancementPolicy
}

func (f BracketFormat) Validate() error {
	if f.Version != bracketFormatVersion {
		return fmt.Errorf("bracket format version %d is unsupported", f.Version)
	}
	switch f.AdvancementPolicy {
	case AdvancementPolicyFixed, AdvancementPolicyHistoricallyObservedReseeded, AdvancementPolicySingleFinal:
	default:
		return fmt.Errorf("unknown advancement policy %q", f.AdvancementPolicy)
	}
	if len(f.Rounds) == 0 {
		return fmt.Errorf("bracket format has no rounds")
	}
	roundIndex := make(map[string]int, len(f.Rounds))
	for i, round := range f.Rounds {
		if strings.TrimSpace(round.ID) == "" || strings.TrimSpace(round.Label) == "" {
			return fmt.Errorf("bracket round has blank id or label")
		}
		if _, exists := roundIndex[round.ID]; exists {
			return fmt.Errorf("duplicate bracket round id %q", round.ID)
		}
		roundIndex[round.ID] = i
	}
	if len(f.Slots) == 0 {
		return fmt.Errorf("bracket format has no slots")
	}
	slotIndex := make(map[string]BracketSlot, len(f.Slots))
	for _, slot := range f.Slots {
		if strings.TrimSpace(slot.ID) == "" || strings.TrimSpace(slot.RoundID) == "" {
			return fmt.Errorf("bracket slot has blank id or round")
		}
		if _, exists := slotIndex[slot.ID]; exists {
			return fmt.Errorf("duplicate bracket slot id %q", slot.ID)
		}
		if _, exists := roundIndex[slot.RoundID]; !exists {
			return fmt.Errorf("bracket slot %q references missing round %q", slot.ID, slot.RoundID)
		}
		if slot.SeedPair != nil {
			a, b := slot.SeedPair[0], slot.SeedPair[1]
			if a < 1 || b < 1 || a == b {
				return fmt.Errorf("bracket slot %q has invalid seed pair", slot.ID)
			}
		}
		slotIndex[slot.ID] = slot
	}
	seenSeeds := map[int]string{}
	openingSlots, openingOrders := 0, map[int]string{}
	for _, slot := range f.Slots {
		if slot.SourceOrder < 0 {
			return fmt.Errorf("bracket slot %q has negative source order", slot.ID)
		}
		if slot.SourceOrder != 0 {
			if roundIndex[slot.RoundID] != 0 {
				return fmt.Errorf("bracket slot %q has source order outside the opening round", slot.ID)
			}
			if prior, exists := openingOrders[slot.SourceOrder]; exists {
				return fmt.Errorf("source order %d is assigned to slots %q and %q", slot.SourceOrder, prior, slot.ID)
			}
			openingOrders[slot.SourceOrder] = slot.ID
		}
		if roundIndex[slot.RoundID] == 0 {
			openingSlots++
		}
		if slot.SeedPair == nil {
			continue
		}
		for _, seed := range slot.SeedPair {
			if prior, exists := seenSeeds[seed]; exists {
				return fmt.Errorf("seed %d is assigned to slots %q and %q", seed, prior, slot.ID)
			}
			seenSeeds[seed] = slot.ID
		}
	}
	if len(openingOrders) != 0 {
		if len(openingOrders) != openingSlots {
			return fmt.Errorf("opening round source order must cover every slot")
		}
		for order := 1; order <= openingSlots; order++ {
			if _, found := openingOrders[order]; !found {
				return fmt.Errorf("opening round source order must be contiguous from 1")
			}
		}
	}
	seenDestinations := make(map[string]bool, len(f.Connections))
	for _, connection := range f.Connections {
		if len(connection.SourceSlotIDs) == 0 || strings.TrimSpace(connection.DestinationSlotID) == "" {
			return fmt.Errorf("bracket connection has blank source or destination")
		}
		if seenDestinations[connection.DestinationSlotID] {
			return fmt.Errorf("duplicate connection destination %q", connection.DestinationSlotID)
		}
		destination, exists := slotIndex[connection.DestinationSlotID]
		if !exists {
			return fmt.Errorf("bracket connection references missing destination %q", connection.DestinationSlotID)
		}
		seenSources := make(map[string]bool, len(connection.SourceSlotIDs))
		for _, sourceID := range connection.SourceSlotIDs {
			if strings.TrimSpace(sourceID) == "" || seenSources[sourceID] {
				return fmt.Errorf("bracket connection has duplicate or blank source")
			}
			seenSources[sourceID] = true
			source, exists := slotIndex[sourceID]
			if !exists {
				return fmt.Errorf("bracket connection references missing source %q", sourceID)
			}
			if sourceID == connection.DestinationSlotID || roundIndex[source.RoundID] >= roundIndex[destination.RoundID] {
				return fmt.Errorf("bracket connection %q to %q does not advance to a later round", sourceID, connection.DestinationSlotID)
			}
		}
		seenDestinations[connection.DestinationSlotID] = true
	}
	if f.AdvancementPolicy == AdvancementPolicySingleFinal && (len(f.Rounds) != 1 || len(f.Slots) != 1 || len(f.Connections) != 0) {
		return fmt.Errorf("single-final bracket must contain one unconnected slot")
	}
	return nil
}

func (f BracketFormat) Copy() BracketFormat {
	clone := f
	clone.Rounds = append([]BracketRound(nil), f.Rounds...)
	clone.Slots = make([]BracketSlot, len(f.Slots))
	for i, slot := range f.Slots {
		clone.Slots[i] = slot
		if slot.SeedPair != nil {
			pair := *slot.SeedPair
			clone.Slots[i].SeedPair = &pair
		}
	}
	clone.Connections = make([]BracketConnection, len(f.Connections))
	for i, connection := range f.Connections {
		clone.Connections[i] = connection
		clone.Connections[i].SourceSlotIDs = append([]string(nil), connection.SourceSlotIDs...)
	}
	return clone
}
