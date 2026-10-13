package competition

import (
	_ "embed"
	"encoding/json"
)

// OutcomeOverrides contains only official facts that ASA fixtures cannot
// establish. It carries no season totals, league formats or complete standings.
type OutcomeOverrides struct {
	Awards     []OutcomeAward     `json:"awards"`
	Deductions []OutcomeDeduction `json:"deductions"`
	Ties       []OutcomeTie       `json:"ties"`
}

type OutcomeAward struct {
	Home        string `json:"home"`
	WinnerGoals int    `json:"winnerGoals"`
	LoserGoals  int    `json:"loserGoals"`
	Winner      string `json:"winner"`
	Loser       string `json:"loser"`
	Source      string `json:"source"`
}

type OutcomeDeduction struct {
	Team   string `json:"team"`
	Points int    `json:"points"`
	Source string `json:"source"`
}

type OutcomeTie struct {
	Order  []string `json:"order"`
	Source string   `json:"source"`
}

//go:embed outcome_overrides.json
var outcomeOverridesJSON []byte

// HistoricalOutcomeOverrides returns a defensive copy. Ordinary new seasons
// need no entry: ASA results and the existing competition catalog are enough.
func HistoricalOutcomeOverrides() map[string]OutcomeOverrides {
	var overrides map[string]OutcomeOverrides
	if err := json.Unmarshal(outcomeOverridesJSON, &overrides); err != nil {
		panic("invalid historical outcome overrides: " + err.Error())
	}
	return overrides
}
