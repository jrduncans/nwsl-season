// Package scenarios defines exact, outcome-only qualification opportunities for
// the next explicitly defined slate of fixtures.
package scenarios

import (
	"fmt"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

const DefinitionVersion = "next-slate-v3"

const (
	LimitationBudgetExhausted = "scenario computation budget exhausted"
	LimitationBudgetPartial   = "scenario computation budget exhausted; published clauses are certified, but additional paths may exist"
)

type SlateState string

const (
	SlateReady       SlateState = "ready"
	SlateNoUpcoming  SlateState = "no_upcoming_fixtures"
	SlateUnavailable SlateState = "unavailable"
)

type SlateSource string

const (
	SourceMatchday      SlateSource = "matchday"
	SourceKickoffWindow SlateSource = "kickoff_window"
)

type Slate struct {
	ID                string
	DefinitionVersion string
	State             SlateState
	Source            SlateSource
	Matchday          int
	StartsAtUTC       time.Time
	LatestKickoffUTC  time.Time
	CutoffUTC         time.Time
	FixtureIDs        []string
	Reason            string
}
type ScheduledGame struct {
	ID, Status, HomeTeamID, AwayTeamID string
	HomeScore, AwayScore               *int
	KickoffUTC                         time.Time
	Matchday                           *int
}
type OpportunityState string

const (
	OpportunityAlreadyClinched   OpportunityState = "already_clinched"
	OpportunityCanClinch         OpportunityState = "can_clinch"
	OpportunityCannotClinch      OpportunityState = "cannot_clinch"
	OpportunityTiebreakDependent OpportunityState = "tiebreak_dependent"
	OpportunityUnresolved        OpportunityState = "unresolved"
)

type FixtureCondition struct {
	GameID          string              `json:"game_id"`
	AllowedOutcomes []clinching.Outcome `json:"allowed_outcomes"`
}
type Clause struct {
	Conditions             []FixtureCondition      `json:"conditions"`
	RepresentedAssignments int                     `json:"represented_assignments"`
	ProofMethods           []clinching.ProofMethod `json:"proof_methods"`
}
type Diagnostics struct {
	SearchNodes, OracleCalls, OracleCacheHits, GuaranteePrunes, OpportunityPrunes, MinimizationProbes, CombinationProbes, InitialClauses, MinimalClauses, VisitedComplete int
	ElapsedMicroseconds                                                                                                                                                   int64
}
type Result struct {
	TeamID                                                        string
	Achievement                                                   competition.AchievementID
	TopK                                                          int
	State                                                         OpportunityState
	AlreadyClinched, CanClinch                                    bool
	Clauses                                                       []Clause
	Necessary                                                     []FixtureCondition
	ProofMethods                                                  []clinching.ProofMethod
	Limitation                                                    string
	TotalAssignments, CertifiedAssignments, UnresolvedAssignments int
	Diagnostics                                                   Diagnostics
	// Playoff elimination is intentionally a separate outcome from a clinch.
	// A clause is published only when enough opponents are already strictly
	// beyond the target's maximum possible points, so no score tiebreak is
	// being inferred. These fields are populated only for the playoffs.
	AlreadyEliminated, CanBeEliminated bool
	EliminationClauses                 []Clause
}

// BudgetLimited reports whether a result came from an incomplete search. A
// partial result may still contain sufficient clauses, but is retried so a
// later run can discover more of them.
func (r Result) BudgetLimited() bool {
	return r.Limitation == LimitationBudgetExhausted || r.Limitation == LimitationBudgetPartial
}

func canonicalOutcomes(in []clinching.Outcome) []clinching.Outcome {
	seen := map[clinching.Outcome]bool{}
	for _, v := range in {
		if v == clinching.HomeWin || v == clinching.Draw || v == clinching.AwayWin {
			seen[v] = true
		}
	}
	out := make([]clinching.Outcome, 0, len(seen))
	for _, v := range []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin} {
		if seen[v] {
			out = append(out, v)
		}
	}
	return out
}
func validSlateState(v SlateState) bool {
	return v == SlateReady || v == SlateNoUpcoming || v == SlateUnavailable
}
func validOpportunityState(v OpportunityState) bool {
	return v == OpportunityAlreadyClinched || v == OpportunityCanClinch || v == OpportunityCannotClinch || v == OpportunityTiebreakDependent || v == OpportunityUnresolved
}

// Validate checks the persisted slate contract.
func (s Slate) Validate() error {
	if !validSlateState(s.State) {
		return fmt.Errorf("invalid slate state %q", s.State)
	}
	if s.FixtureIDs == nil {
		return fmt.Errorf("slate fixture IDs must be non-nil")
	}
	if s.State != SlateReady {
		if len(s.FixtureIDs) != 0 {
			return fmt.Errorf("non-ready slate has fixtures")
		}
		return nil
	}
	if s.ID == "" || s.DefinitionVersion != DefinitionVersion || len(s.FixtureIDs) == 0 || s.StartsAtUTC.IsZero() || s.LatestKickoffUTC.IsZero() || s.CutoffUTC.IsZero() {
		return fmt.Errorf("invalid ready slate")
	}
	switch s.Source {
	case SourceMatchday:
		if s.Matchday <= 0 {
			return fmt.Errorf("matchday slate lacks matchday")
		}
	case SourceKickoffWindow:
		if s.Matchday != 0 {
			return fmt.Errorf("window slate has matchday")
		}
	default:
		return fmt.Errorf("invalid slate source %q", s.Source)
	}
	seen := map[string]bool{}
	for _, id := range s.FixtureIDs {
		if id == "" || seen[id] {
			return fmt.Errorf("invalid slate fixture %q", id)
		}
		seen[id] = true
	}
	return nil
}
func (r Result) Validate(slate Slate) error {
	if !validOpportunityState(r.State) || r.TeamID == "" || r.Achievement == "" || r.TopK < 1 || r.Clauses == nil || r.Necessary == nil || r.ProofMethods == nil {
		return fmt.Errorf("invalid scenario result")
	}
	if r.State == OpportunityAlreadyClinched {
		if !r.AlreadyClinched || r.CanClinch || len(r.Clauses) > 0 {
			return fmt.Errorf("invalid already-clinched result")
		}
	} else if r.AlreadyClinched || r.CanClinch != (r.State == OpportunityCanClinch) {
		return fmt.Errorf("invalid scenario state flags")
	}
	if r.State == OpportunityCanClinch && (len(r.Clauses) == 0 || r.CertifiedAssignments <= 0) {
		return fmt.Errorf("invalid can-clinch result")
	}
	if r.CertifiedAssignments < 0 || r.UnresolvedAssignments < 0 || r.TotalAssignments < 0 || r.CertifiedAssignments+r.UnresolvedAssignments > r.TotalAssignments {
		return fmt.Errorf("invalid assignment counts")
	}
	if slate.State == SlateReady && r.State != OpportunityAlreadyClinched && r.TotalAssignments != pow3(len(slate.FixtureIDs)) {
		return fmt.Errorf("invalid total assignment count")
	}
	fixture := map[string]bool{}
	for _, id := range slate.FixtureIDs {
		fixture[id] = true
	}
	for _, c := range r.Clauses {
		if c.RepresentedAssignments <= 0 || len(c.ProofMethods) == 0 {
			return fmt.Errorf("invalid clause")
		}
		if err := validateConditions(c.Conditions, fixture); err != nil {
			return fmt.Errorf("invalid clause condition: %w", err)
		}
	}
	if err := validateConditions(r.Necessary, fixture); err != nil {
		return fmt.Errorf("invalid necessary condition: %w", err)
	}
	if r.Achievement != competition.AchievementPlayoffs && (r.AlreadyEliminated || r.CanBeEliminated || len(r.EliminationClauses) != 0) {
		return fmt.Errorf("only playoff results may carry elimination data")
	}
	if r.AlreadyEliminated && r.CanBeEliminated {
		return fmt.Errorf("already-eliminated result cannot have elimination scenarios")
	}
	if r.AlreadyClinched && (r.AlreadyEliminated || r.CanBeEliminated) {
		return fmt.Errorf("already-clinched result cannot have elimination data")
	}
	if r.AlreadyEliminated && r.CanClinch {
		return fmt.Errorf("already-eliminated result cannot have clinching scenarios")
	}
	if r.AlreadyEliminated && len(r.EliminationClauses) != 0 {
		return fmt.Errorf("already-eliminated result cannot have elimination clauses")
	}
	if r.CanBeEliminated != (len(r.EliminationClauses) > 0) {
		return fmt.Errorf("invalid elimination scenario flags")
	}
	for _, c := range r.EliminationClauses {
		if c.RepresentedAssignments <= 0 || len(c.ProofMethods) == 0 {
			return fmt.Errorf("invalid elimination clause")
		}
		if err := validateConditions(c.Conditions, fixture); err != nil {
			return fmt.Errorf("invalid elimination condition: %w", err)
		}
	}
	return nil
}

func validateConditions(conditions []FixtureCondition, fixture map[string]bool) error {
	seen := map[string]bool{}
	for _, condition := range conditions {
		if !fixture[condition.GameID] || seen[condition.GameID] {
			return fmt.Errorf("unknown or duplicate fixture %q", condition.GameID)
		}
		if len(condition.AllowedOutcomes) == 0 || len(condition.AllowedOutcomes) == 3 || len(canonicalOutcomes(condition.AllowedOutcomes)) != len(condition.AllowedOutcomes) {
			return fmt.Errorf("invalid outcomes for fixture %q", condition.GameID)
		}
		seen[condition.GameID] = true
	}
	return nil
}
func sortedMethods(values []clinching.ProofMethod) []clinching.ProofMethod {
	seen := map[clinching.ProofMethod]bool{}
	for _, v := range values {
		seen[v] = true
	}
	out := make([]clinching.ProofMethod, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
