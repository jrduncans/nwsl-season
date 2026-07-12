// Package whatif turns user-selected fixture outcomes into synthetic standings
// games. It deliberately has no HTTP, SQL, or template dependencies.
package whatif

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

const EncodingVersion = "1"

// RemainingStatus is the upstream status for a selectable future fixture.
const RemainingStatus = "PreMatch"

// ErrNotRemaining is returned when state references a fixture that cannot be
// changed, including stale IDs and completed matches.
var ErrNotRemaining = errors.New("game is not a remaining fixture")

// Outcome is a selected result for an unplayed fixture.
type Outcome string

const (
	HomeWin Outcome = "h"
	Draw    Outcome = "d"
	AwayWin Outcome = "a"
)

// Parse decodes repeated URL values in the form game-id:outcome.
func Parse(version string, encoded []string) (map[string]Outcome, error) {
	if version == "" && len(encoded) == 0 {
		return map[string]Outcome{}, nil
	}
	if version != EncodingVersion {
		return nil, fmt.Errorf("unsupported what-if version %q", version)
	}

	selections := make(map[string]Outcome, len(encoded))
	for _, value := range encoded {
		gameID, code, ok := strings.Cut(value, ":")
		outcome := Outcome(code)
		if !ok || gameID == "" || !outcome.Valid() {
			return nil, fmt.Errorf("invalid what-if selection %q", value)
		}
		if _, exists := selections[gameID]; exists {
			return nil, fmt.Errorf("duplicate what-if selection for game %q", gameID)
		}
		selections[gameID] = outcome
	}
	return selections, nil
}

// Valid reports whether the outcome can be simulated.
func (o Outcome) Valid() bool {
	return o == HomeWin || o == Draw || o == AwayWin
}

// Apply returns a copy of games with selected remaining fixtures replaced by
// canonical synthetic scores: 1-0, 0-0, or 0-1.
func Apply(games []standings.Game, selections map[string]Outcome) ([]standings.Game, error) {
	projected := append([]standings.Game(nil), games...)
	remaining := make(map[string]int)
	for index, game := range projected {
		if game.Status == RemainingStatus {
			remaining[game.ID] = index
		}
	}

	for gameID, outcome := range selections {
		index, ok := remaining[gameID]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrNotRemaining, gameID)
		}
		home, away := canonicalScore(outcome)
		projected[index].Status = standings.CompletedStatus
		projected[index].HomeScore = &home
		projected[index].AwayScore = &away
	}
	return projected, nil
}

func canonicalScore(outcome Outcome) (int, int) {
	switch outcome {
	case HomeWin:
		return 1, 0
	case AwayWin:
		return 0, 1
	default:
		return 0, 0
	}
}
