package competition

import (
	"fmt"
	"maps"
)

// ReconcileInventory verifies the official fixture inventory using observed
// source counts plus known awards. Returned awards fill only explained deficits;
// the input counts and cached fixtures remain unchanged. A nil expectation
// cannot prove completeness. Match status and score validity are separate checks.
func ReconcileInventory(season, stage string, expected *InventoryExpectation, games int, appearances map[string]int) ([]OutcomeAward, error) {
	if expected == nil {
		return nil, fmt.Errorf("inventory expectation is unknown")
	}
	if err := expected.Validate(); err != nil {
		return nil, err
	}
	counts := maps.Clone(appearances)
	var awards []OutcomeAward
	if stage == "Regular Season" && expected.GamesPerTeam > 0 {
		var err error
		awards, err = MissingOutcomeAwards(counts, expected.GamesPerTeam, HistoricalOutcomeOverrides()[season].Awards)
		if err != nil {
			return nil, err
		}
		for _, award := range awards {
			counts[award.Winner]++
			counts[award.Loser]++
		}
	}
	want := expected.Games
	if want == 0 {
		want = expected.Teams * expected.GamesPerTeam / 2
	}
	if want > 0 && games+len(awards) != want {
		return nil, fmt.Errorf("inventory has %d games, want %d", games+len(awards), want)
	}
	if expected.Teams > 0 && len(counts) != expected.Teams {
		return nil, fmt.Errorf("inventory has %d teams, want %d", len(counts), expected.Teams)
	}
	if expected.GamesPerTeam > 0 {
		for id, count := range counts {
			if count != expected.GamesPerTeam {
				return nil, fmt.Errorf("team %q has %d appearances, want %d", id, count, expected.GamesPerTeam)
			}
		}
	}
	return awards, nil
}

// Validate checks that a verified expectation is internally consistent.
func (i InventoryExpectation) Validate() error {
	if i.Teams < 0 || i.GamesPerTeam < 0 || i.Games < 0 {
		return fmt.Errorf("inventory cannot contain negative values")
	}
	if i.Teams == 0 && i.GamesPerTeam == 0 && i.Games == 0 {
		return fmt.Errorf("inventory expectation cannot be all zero")
	}
	if (i.Teams == 0) != (i.GamesPerTeam == 0) {
		return fmt.Errorf("inventory must set Teams and GamesPerTeam together")
	}
	if i.Teams > 0 {
		product := i.Teams * i.GamesPerTeam
		if product%2 != 0 {
			return fmt.Errorf("inventory Teams * GamesPerTeam must be even")
		}
		if i.Games != 0 && i.Games != product/2 {
			return fmt.Errorf("inventory Games disagrees with team fields")
		}
	}
	return nil
}
