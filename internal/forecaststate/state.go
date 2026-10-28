// Package forecaststate owns the versioned, shareable Forecast Lab scenario
// encoding. It deliberately has no HTTP dependencies.
package forecaststate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/simulation"
)

const (
	// EncodingVersion identifies the Forecast Lab URL format.
	EncodingVersion       = "2"
	LegacyEncodingVersion = "1"
	// MaxFixed keeps user assumptions small enough to remain readable.
	MaxFixed = 12
)

// State is a versioned model choice and its fixed outcomes.
type State struct {
	ModelID           string
	ComparisonModelID string
	Fixed             map[string]simulation.Outcome
	PlayoffWinners    map[string]simulation.PostseasonMatch
}

// ParseV2 decodes the model-comparison format using a caller-owned catalog.
func ParseV2(version, modelID, comparisonID string, values []string, supported func(string) bool, recommended string) (State, error) {
	if version == "" && modelID == "" && comparisonID == "" && len(values) == 0 {
		return State{ModelID: recommended, Fixed: map[string]simulation.Outcome{}}, nil
	}
	if version == LegacyEncodingVersion {
		if comparisonID != "" || modelID != "results-poisson-v1" {
			return State{}, fmt.Errorf("unsupported forecast model %q", modelID)
		}
		return parseFixed(modelID, "", values)
	}
	if version != EncodingVersion {
		return State{}, fmt.Errorf("unsupported forecast version %q", version)
	}
	if modelID == "" || !supported(modelID) {
		return State{}, fmt.Errorf("unsupported forecast model %q", modelID)
	}
	if comparisonID != "" && (!supported(comparisonID) || comparisonID == modelID) {
		return State{}, fmt.Errorf("invalid comparison forecast model %q", comparisonID)
	}
	return parseFixed(modelID, comparisonID, values)
}

func parseFixed(modelID, comparisonID string, values []string) (State, error) {
	if len(values) > MaxFixed {
		return State{}, fmt.Errorf("at most %d fixed results are allowed", MaxFixed)
	}
	fixed := make(map[string]simulation.Outcome, len(values))
	for _, value := range values {
		gameID, encoded, ok := strings.Cut(value, ":")
		outcome := simulation.Outcome(encoded)
		if !ok || gameID == "" || !outcome.Valid() {
			return State{}, fmt.Errorf("invalid forecast selection %q", value)
		}
		if _, exists := fixed[gameID]; exists {
			return State{}, fmt.Errorf("duplicate forecast selection for game %q", gameID)
		}
		fixed[gameID] = outcome
	}
	return State{ModelID: modelID, ComparisonModelID: comparisonID, Fixed: fixed}, nil
}

// Values returns sorted p query values.
func (s State) Values() []string {
	if len(s.PlayoffWinners) > 0 {
		ids := make([]string, 0, len(s.PlayoffWinners))
		for id := range s.PlayoffWinners {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		values := make([]string, 0, len(ids))
		for _, id := range ids {
			match := s.PlayoffWinners[id]
			values = append(values, strings.Join([]string{id, match.Winner, match.Home, match.Away}, ":"))
		}
		return values
	}
	ids := make([]string, 0, len(s.Fixed))
	for id := range s.Fixed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, id+":"+string(s.Fixed[id]))
	}
	return values
}

// With returns a copied state with one assumption added or replaced.
func (s State) With(gameID string, outcome simulation.Outcome) (State, error) {
	if gameID == "" || !outcome.Valid() {
		return State{}, fmt.Errorf("invalid forecast assumption")
	}
	clone := s.copy()
	if _, exists := clone.Fixed[gameID]; !exists && len(clone.Fixed) >= MaxFixed {
		return State{}, fmt.Errorf("at most %d fixed results are allowed", MaxFixed)
	}
	clone.Fixed[gameID] = outcome
	return clone, nil
}

// Without returns a copied state without gameID.
func (s State) Without(gameID string) State {
	clone := s.copy()
	delete(clone.Fixed, gameID)
	return clone
}

func (s State) copy() State {
	fixed := make(map[string]simulation.Outcome, len(s.Fixed))
	for id, outcome := range s.Fixed {
		fixed[id] = outcome
	}
	winners := make(map[string]simulation.PostseasonMatch, len(s.PlayoffWinners))
	for id, match := range s.PlayoffWinners {
		winners[id] = match
	}
	return State{ModelID: s.ModelID, ComparisonModelID: s.ComparisonModelID, Fixed: fixed, PlayoffWinners: winners}
}

func (s State) WithModel(id string) State {
	c := s.copy()
	c.ModelID = id
	if c.ComparisonModelID == id {
		c.ComparisonModelID = ""
	}
	return c
}
func (s State) WithComparison(id string) State { c := s.copy(); c.ComparisonModelID = id; return c }
func (s State) WithoutComparison() State       { c := s.copy(); c.ComparisonModelID = ""; return c }

// ParsePlayoffs preserves v2 model handling and decodes slot, winner and both
// participants. Keeping the pair makes changed opponents invalidate old links.
func ParsePlayoffs(version, modelID, comparisonID string, values []string, supported func(string) bool, recommended string) (State, error) {
	if len(values) > 0 && version != EncodingVersion {
		return State{}, fmt.Errorf("unsupported playoff forecast version %q", version)
	}
	state, err := ParseV2(version, modelID, comparisonID, nil, supported, recommended)
	if err != nil {
		return State{}, err
	}
	if len(values) > MaxFixed {
		return State{}, fmt.Errorf("at most %d fixed results are allowed", MaxFixed)
	}
	state.PlayoffWinners = map[string]simulation.PostseasonMatch{}
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) != 4 || parts[0] == "" || parts[2] == "" || parts[3] == "" || parts[2] == parts[3] || (parts[1] != parts[2] && parts[1] != parts[3]) {
			return State{}, fmt.Errorf("invalid playoff selection %q", value)
		}
		if _, exists := state.PlayoffWinners[parts[0]]; exists {
			return State{}, fmt.Errorf("duplicate playoff selection %q", parts[0])
		}
		home, away := parts[2], parts[3]
		if home > away {
			home, away = away, home
		}
		state.PlayoffWinners[parts[0]] = simulation.PostseasonMatch{Home: home, Away: away, Winner: parts[1]}
	}
	return state, nil
}

func (s State) WithPlayoffWinner(slot string, match simulation.PostseasonMatch) State {
	clone := s.copy()
	if match.Home > match.Away {
		match.Home, match.Away = match.Away, match.Home
	}
	clone.PlayoffWinners[slot] = match
	return clone
}

func (s State) WithoutPlayoffWinner(slot string) State {
	clone := s.copy()
	delete(clone.PlayoffWinners, slot)
	return clone
}
