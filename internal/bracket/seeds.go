package bracket

import (
	"fmt"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

// BuildWithSeeds adds expectations from resolved official final seeds. Seeds
// must be certified by the caller against a complete regular-season inventory.
// Expectations never become games or winners. Source games are assigned only
// when their participants match a verified pair or known prior-round winners;
// partial publication therefore cannot move a game into the wrong seed slot.
func BuildWithSeeds(format competition.BracketFormat, games []cache.Game, seeds map[int]string) View {
	view := Build(format, nil)
	view.SourceGames = cloneGames(games)
	if view.State == StateFormatMismatch {
		return view
	}
	for r := range view.Rounds {
		for s := range view.Rounds[r].Slots {
			slot := &view.Rounds[r].Slots[s]
			if slot.SeedPair != nil {
				if id := seeds[slot.SeedPair[0]]; id != "" {
					slot.Home = Entrant{TeamID: id}
				}
				if id := seeds[slot.SeedPair[1]]; id != "" {
					slot.Away = Entrant{TeamID: id}
				}
			}
			if id := seeds[slot.ByeSeed]; slot.ByeSeed > 0 && id != "" {
				slot.Home = Entrant{TeamID: id}
			}
		}
	}
	// Blank participant rows are source placeholders, not identified matchups.
	concrete := make([]cache.Game, 0, len(games))
	for _, game := range games {
		if game.HomeTeamID != "" && game.AwayTeamID != "" {
			concrete = append(concrete, game)
		}
	}
	seen := map[string]bool{}
	for _, game := range concrete {
		if game.ASAID == "" || seen[game.ASAID] || game.HomeTeamID == game.AwayTeamID {
			view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "invalid_source_game", GameID: game.ASAID, Message: "published matches need distinct participants and unique nonblank source IDs"})
		}
		seen[game.ASAID] = true
	}
	if len(view.Diagnostics) > 0 {
		view.State = StateFormatMismatch
		return view
	}
	used, ambiguous := map[string]bool{}, map[string]bool{}
	unresolved := false
	for r := range view.Rounds {
		for _, game := range concrete {
			if used[game.ASAID] {
				continue
			}
			for s := range view.Rounds[r].Slots {
				slot := &view.Rounds[r].Slots[s]
				if slot.Home.TeamID == TBD || slot.Away.TeamID == TBD || !sameParticipants(game.HomeTeamID, game.AwayTeamID, slot.Home.TeamID, slot.Away.TeamID) {
					continue
				}
				if slot.Game != nil {
					view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "duplicate_placement", GameID: game.ASAID, SlotID: slot.ID, Message: "multiple published games match the same expected pairing"})
					continue
				}
				unresolved = placeGame(&view, slot, game) || unresolved
				used[game.ASAID] = true
				break
			}
		}
		// A game naming an unresolved seed is placed only when exactly one
		// seed slot can hold it. Several candidates leave it unplaced, which
		// is unresolved rather than a disagreement with the expected bracket.
		for _, game := range concrete {
			if used[game.ASAID] {
				continue
			}
			var candidates []*Slot
			for s := range view.Rounds[r].Slots {
				slot := &view.Rounds[r].Slots[s]
				if slot.SeedPair != nil && slot.Game == nil && fitsUnresolvedSeedSlot(game, *slot, seeds) {
					candidates = append(candidates, slot)
				}
			}
			switch len(candidates) {
			case 0:
			case 1:
				unresolved = placeGame(&view, candidates[0], game) || unresolved
				used[game.ASAID] = true
			default:
				ambiguous[game.ASAID] = true
			}
		}
		populateFixedAdvances(&view, format)
	}
	for _, game := range concrete {
		if ambiguous[game.ASAID] && !used[game.ASAID] {
			unresolved = true
		} else if !used[game.ASAID] {
			view.Diagnostics = append(view.Diagnostics, Diagnostic{Code: "seed_source_disagreement", GameID: game.ASAID, Message: fmt.Sprintf("Published game %s cannot be matched to resolved final seeds or known advancement. It may disagree with the expected bracket or require unresolved participants/results; view the published matches.", game.ASAID)})
		}
	}
	switch {
	case len(view.Diagnostics) > 0:
		view.State = StateFormatMismatch
	case unresolved:
		view.State = StateUnresolved
	case len(used) == len(format.Slots):
		view.State = StateReady
	case len(games) > 0:
		view.State = StatePartial
	default:
		view.State = StateEmpty
	}
	return view
}

// fitsUnresolvedSeedSlot reports whether a published game is consistent with a
// seed slot awaiting at least one unresolved seed: every resolved side of the
// slot takes part, and any other participant holds no resolved seed.
func fitsUnresolvedSeedSlot(game cache.Game, slot Slot, seeds map[int]string) bool {
	if slot.Home.TeamID != TBD && slot.Away.TeamID != TBD {
		return false
	}
	resolved := make(map[string]bool, len(seeds))
	for _, id := range seeds {
		resolved[id] = true
	}
	for _, participant := range []string{game.HomeTeamID, game.AwayTeamID} {
		if participant != slot.Home.TeamID && participant != slot.Away.TeamID && resolved[participant] {
			return false
		}
	}
	for _, side := range []string{slot.Home.TeamID, slot.Away.TeamID} {
		if side != TBD && side != game.HomeTeamID && side != game.AwayTeamID {
			return false
		}
	}
	return true
}
