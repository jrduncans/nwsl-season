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
	EncodingVersion = "1"
	// MaxFixed keeps user assumptions small enough to remain readable.
	MaxFixed = 12
)

// State is a versioned model choice and its fixed outcomes.
type State struct {
	ModelID string
	Fixed   map[string]simulation.Outcome
}

// Parse decodes v, m, and repeated p query values. An entirely empty state is
// the default forecast using supportedModelID.
func Parse(version, modelID string, values []string, supportedModelID string) (State, error) {
	if version == "" && modelID == "" && len(values) == 0 {
		return State{ModelID: supportedModelID, Fixed: map[string]simulation.Outcome{}}, nil
	}
	if version != EncodingVersion {
		return State{}, fmt.Errorf("unsupported forecast version %q", version)
	}
	if modelID != supportedModelID {
		return State{}, fmt.Errorf("unsupported forecast model %q", modelID)
	}
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
	return State{ModelID: modelID, Fixed: fixed}, nil
}

// Values returns sorted p query values.
func (s State) Values() []string {
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
	copy := s.copy()
	if _, exists := copy.Fixed[gameID]; !exists && len(copy.Fixed) >= MaxFixed {
		return State{}, fmt.Errorf("at most %d fixed results are allowed", MaxFixed)
	}
	copy.Fixed[gameID] = outcome
	return copy, nil
}

// Without returns a copied state without gameID.
func (s State) Without(gameID string) State {
	copy := s.copy()
	delete(copy.Fixed, gameID)
	return copy
}

func (s State) copy() State {
	fixed := make(map[string]simulation.Outcome, len(s.Fixed))
	for id, outcome := range s.Fixed {
		fixed[id] = outcome
	}
	return State{ModelID: s.ModelID, Fixed: fixed}
}
