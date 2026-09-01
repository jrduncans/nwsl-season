package competition

func seedPair(a, b int) *[2]int {
	pair := [2]int{a, b}
	return &pair
}

func playoffBracket(season string) *BracketFormat {
	format := bracketWithRounds(
		BracketRound{ID: "semifinals", Label: "Semifinals"},
		BracketRound{ID: "final", Label: "Final"},
	)
	format.Slots = []BracketSlot{
		{ID: "semifinal-1", RoundID: "semifinals", SeedPair: seedPair(1, 4)},
		{ID: "semifinal-2", RoundID: "semifinals", SeedPair: seedPair(2, 3)},
		{ID: "final", RoundID: "final"},
	}
	format.Connections = []BracketConnection{{SourceSlotIDs: []string{"semifinal-1", "semifinal-2"}, DestinationSlotID: "final"}}
	if season == "2021" || season == "2022" || season == "2023" {
		format.AdvancementPolicy = AdvancementPolicyHistoricallyObservedReseeded
		format.Rounds = []BracketRound{{ID: "quarterfinals", Label: "Quarterfinals"}, {ID: "semifinals", Label: "Semifinals"}, {ID: "final", Label: "Final"}}
		format.Slots = []BracketSlot{
			{ID: "quarterfinal-1", RoundID: "quarterfinals", SeedPair: seedPair(3, 6)},
			{ID: "quarterfinal-2", RoundID: "quarterfinals", SeedPair: seedPair(4, 5)},
		}
		format.Slots = append(format.Slots, laterSlots("semifinals", "semifinal-1", "semifinal-2")...)
		format.Slots = append(format.Slots, openingSlots("final", "final")...)
		format.Connections = []BracketConnection{
			{SourceSlotIDs: []string{"semifinal-1", "semifinal-2"}, DestinationSlotID: "final"},
		}
		// Reseeding means the source-backed construction chooses the semifinal
		// pairing later; the slots remain factual placeholders here.
		return &format
	}
	if season == "2024" || season == "2025" || season == "2026" {
		format = bracketWithRounds(
			BracketRound{ID: "quarterfinals", Label: "Quarterfinals"},
			BracketRound{ID: "semifinals", Label: "Semifinals"},
			BracketRound{ID: "final", Label: "Final"},
		)
		format.AdvancementPolicy = AdvancementPolicyFixed
		quarterfinalPairs := [][2]int{{1, 8}, {4, 5}, {2, 7}, {3, 6}}
		quarterfinalOrder := []int{1, 2, 3, 4}
		if season == "2025" {
			// The published 2025 kickoff sequence was slots 2, 3, 1, then 4.
			// Slot order remains the verified seed/advancement topology.
			quarterfinalOrder = []int{3, 1, 2, 4}
		}
		format.Slots = []BracketSlot{
			{ID: "quarterfinal-1", RoundID: "quarterfinals", SeedPair: seedPair(quarterfinalPairs[0][0], quarterfinalPairs[0][1]), SourceOrder: quarterfinalOrder[0]},
			{ID: "quarterfinal-2", RoundID: "quarterfinals", SeedPair: seedPair(quarterfinalPairs[1][0], quarterfinalPairs[1][1]), SourceOrder: quarterfinalOrder[1]},
			{ID: "quarterfinal-3", RoundID: "quarterfinals", SeedPair: seedPair(quarterfinalPairs[2][0], quarterfinalPairs[2][1]), SourceOrder: quarterfinalOrder[2]},
			{ID: "quarterfinal-4", RoundID: "quarterfinals", SeedPair: seedPair(quarterfinalPairs[3][0], quarterfinalPairs[3][1]), SourceOrder: quarterfinalOrder[3]},
			{ID: "semifinal-1", RoundID: "semifinals"}, {ID: "semifinal-2", RoundID: "semifinals"}, {ID: "final", RoundID: "final"},
		}
		format.Connections = []BracketConnection{
			{SourceSlotIDs: []string{"quarterfinal-1", "quarterfinal-2"}, DestinationSlotID: "semifinal-1"},
			{SourceSlotIDs: []string{"quarterfinal-3", "quarterfinal-4"}, DestinationSlotID: "semifinal-2"},
			{SourceSlotIDs: []string{"semifinal-1", "semifinal-2"}, DestinationSlotID: "final"},
		}
	}
	return &format
}

func challengeCupKnockoutBracket(season string) *BracketFormat {
	if season == "2021" {
		return singleFinalBracket()
	}
	if season == "2022" || season == "2023" {
		format := bracketWithRounds(
			BracketRound{ID: "semifinals", Label: "Semifinals"},
			BracketRound{ID: "final", Label: "Final"},
		)
		format.Slots = append(laterSlots("semifinals", "semifinal-1", "semifinal-2"), openingSlots("final", "final")...)
		format.Connections = []BracketConnection{{SourceSlotIDs: []string{"semifinal-1", "semifinal-2"}, DestinationSlotID: "final"}}
		return &format
	}
	format := bracketWithRounds(
		BracketRound{ID: "quarterfinals", Label: "Quarterfinals"},
		BracketRound{ID: "semifinals", Label: "Semifinals"},
		BracketRound{ID: "final", Label: "Final"},
	)
	format.Slots = []BracketSlot{
		{ID: "quarterfinal-1", RoundID: "quarterfinals", SeedPair: seedPair(1, 8)},
		{ID: "quarterfinal-2", RoundID: "quarterfinals", SeedPair: seedPair(4, 5)},
		{ID: "quarterfinal-3", RoundID: "quarterfinals", SeedPair: seedPair(2, 7)},
		{ID: "quarterfinal-4", RoundID: "quarterfinals", SeedPair: seedPair(3, 6)},
	}
	format.Slots = append(format.Slots, laterSlots("semifinals", "semifinal-1", "semifinal-2")...)
	format.Slots = append(format.Slots, openingSlots("final", "final")...)
	format.Connections = []BracketConnection{
		{SourceSlotIDs: []string{"quarterfinal-1", "quarterfinal-2"}, DestinationSlotID: "semifinal-1"},
		{SourceSlotIDs: []string{"quarterfinal-3", "quarterfinal-4"}, DestinationSlotID: "semifinal-2"},
		{SourceSlotIDs: []string{"semifinal-1", "semifinal-2"}, DestinationSlotID: "final"},
	}
	return &format
}

func singleFinalBracket() *BracketFormat {
	format := bracketWithRounds(BracketRound{ID: "final", Label: "Final"})
	format.AdvancementPolicy = AdvancementPolicySingleFinal
	format.Slots = []BracketSlot{{ID: "final", RoundID: "final"}}
	return &format
}

func bracketWithRounds(rounds ...BracketRound) BracketFormat {
	return BracketFormat{
		Version:           bracketFormatVersion,
		AdvancementPolicy: AdvancementPolicyFixed,
		Rounds:            append([]BracketRound(nil), rounds...),
	}
}

func openingSlots(round string, ids ...string) []BracketSlot {
	slots := make([]BracketSlot, len(ids))
	for i, id := range ids {
		slots[i] = BracketSlot{ID: id, RoundID: round}
	}
	return slots
}

func laterSlots(round string, ids ...string) []BracketSlot {
	return openingSlots(round, ids...)
}
