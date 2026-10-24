// Package scenarios defines exact, outcome-only qualification opportunities for
// the next explicitly defined slate of fixtures.
package scenarios

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

// DefinitionVersion changes when scenario result semantics change so a prior
// completed snapshot cannot preserve an obsolete conclusion.
const DefinitionVersion = "next-slate-v6"

// SeasonDefinitionVersion identifies rest-of-season results, which cover every
// remaining match and may carry score conditions. They are stored as a
// separate batch beside the next-slate batch for the same snapshot.
const SeasonDefinitionVersion = "rest-of-season-v7"

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
	SourceRestOfSeason  SlateSource = "rest_of_season"
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
	Conditions []FixtureCondition `json:"conditions"`
	// ScoreConditions are added to the results in Conditions; every one must
	// hold. Only rest-of-season clauses carry them.
	ScoreConditions        []ScoreCondition        `json:"score_conditions,omitempty"`
	RepresentedAssignments int                     `json:"represented_assignments"`
	ProofMethods           []clinching.ProofMethod `json:"proof_methods"`
}

// ScoreMeasure is a tiebreaker that depends on scores rather than results.
type ScoreMeasure string

const (
	MeasureGoalDifference  ScoreMeasure = "goal_difference"
	MeasureGoalsScored     ScoreMeasure = "goals_scored"
	MeasureHeadToHeadGoals ScoreMeasure = "head_to_head_goals"
)

// ScoreCondition says TeamID finishes ahead of RivalID, who ends level with it
// on points. Gains count only the remaining matches: the goal-difference gain
// is TeamID's goal difference in them minus RivalID's, and likewise for goals
// scored; the head-to-head gain is TeamID's goals minus RivalID's in their
// remaining matches against each other.
//
// TeamID may be the rival of the team whose scenario it is: "UTA finishes
// ahead of POR" excludes the scorelines where the two would be level.
//
// Levels form a cascade. A level holds when its gain is at least MinimumGain,
// or exactly MinimumGain-1 and the next level holds; the last level has no
// fallback. An ExactOnly level omits the "at least" branch, either because
// the results make a larger gain impossible or because the condition is that
// the two finish level on it; an ExactOnly last level holds at exactly
// MinimumGain-1, so a condition whose levels are all ExactOnly says the two
// finish level on every measure.
type ScoreCondition struct {
	TeamID  string       `json:"team_id"`
	RivalID string       `json:"rival_id"`
	Levels  []ScoreLevel `json:"levels"`
}

// ScoreLevel is one tiebreaker in a ScoreCondition. TeamNow and RivalNow are
// each team's value before the remaining matches, for display.
type ScoreLevel struct {
	Measure     ScoreMeasure `json:"measure"`
	MinimumGain int          `json:"minimum_gain"`
	ExactOnly   bool         `json:"exact_only,omitempty"`
	TeamNow     int          `json:"team_now"`
	RivalNow    int          `json:"rival_now"`
}

// Holds reports whether gains, indexed like Levels, satisfy the cascade.
func (c ScoreCondition) Holds(gains []int) bool {
	for i, level := range c.Levels {
		last := i == len(c.Levels)-1
		if !level.ExactOnly && gains[i] >= level.MinimumGain {
			return true
		}
		if gains[i] != level.MinimumGain-1 {
			return false
		}
		if last {
			return level.ExactOnly
		}
	}
	return false
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
	// A clause is published only when every legal completion leaves enough
	// opponents strictly beyond the target's maximum possible points. Future
	// head-to-head fixtures can force the final opponent across that ceiling.
	// These fields are populated only for the playoffs.
	AlreadyEliminated, CanBeEliminated bool
	EliminationClauses                 []Clause
	// DisciplinaryPossible marks a rest-of-season outcome where teams could
	// finish identical through head-to-head goals scored, so unavailable
	// disciplinary points may decide. That case is never claimed.
	DisciplinaryPossible bool
	// Rivals restates a rest-of-season clinch one rival at a time, when that
	// is exact. It describes the same scorelines as Clauses.
	Seeding *SeedingResult
	Rivals  *RivalSummary
}

// BudgetLimited reports whether a result came from an incomplete search. A
// partial result may still contain sufficient clauses, but is retried so a
// later run can discover more of them.
func (r Result) BudgetLimited() bool {
	return r.Limitation == LimitationBudgetExhausted || r.Limitation == LimitationBudgetPartial || (r.Seeding != nil && r.Seeding.Limitation == LimitationBudgetPartial)
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
	wantVersion := DefinitionVersion
	if s.Source == SourceRestOfSeason {
		wantVersion = SeasonDefinitionVersion
	}
	if s.ID == "" || s.DefinitionVersion != wantVersion || len(s.FixtureIDs) == 0 || s.StartsAtUTC.IsZero() || s.LatestKickoffUTC.IsZero() || s.CutoffUTC.IsZero() {
		return fmt.Errorf("invalid ready slate")
	}
	switch s.Source {
	case SourceMatchday:
		if s.Matchday <= 0 {
			return fmt.Errorf("matchday slate lacks matchday")
		}
	case SourceKickoffWindow, SourceRestOfSeason:
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
		if len(c.ScoreConditions) > 0 && slate.Source != SourceRestOfSeason {
			return fmt.Errorf("only rest-of-season clauses may carry score conditions")
		}
		if err := validateScoreConditions(c.ScoreConditions, r.TeamID); err != nil {
			return fmt.Errorf("invalid score condition: %w", err)
		}
	}
	if err := validateConditions(r.Necessary, fixture); err != nil {
		return fmt.Errorf("invalid necessary condition: %w", err)
	}
	if r.DisciplinaryPossible && slate.Source != SourceRestOfSeason {
		return fmt.Errorf("only rest-of-season results may note disciplinary points")
	}
	if r.Seeding != nil {
		if slate.Source != SourceRestOfSeason || r.Achievement != competition.AchievementPlayoffs {
			return fmt.Errorf("only season playoff results may carry seeding")
		}
		if err := r.Seeding.validate(r.TeamID, fixture); err != nil {
			return err
		}
	}
	if r.Rivals != nil {
		if slate.Source != SourceRestOfSeason || r.State != OpportunityCanClinch {
			return fmt.Errorf("only rest-of-season clinches may carry a rival summary")
		}
		if err := r.Rivals.validate(r.TeamID, fixture); err != nil {
			return err
		}
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

var scoreMeasureOrder = []ScoreMeasure{MeasureGoalDifference, MeasureGoalsScored, MeasureHeadToHeadGoals}

func validateScoreConditions(conditions []ScoreCondition, team string) error {
	rivals := map[string]bool{}
	for _, condition := range conditions {
		rival := condition.RivalID
		if condition.RivalID == team {
			rival = condition.TeamID
		} else if condition.TeamID != team {
			rival = ""
		}
		if rival == "" || rival == team || rivals[rival] {
			return fmt.Errorf("invalid teams %q and %q", condition.TeamID, condition.RivalID)
		}
		rivals[rival] = true
		if len(condition.Levels) == 0 {
			return fmt.Errorf("condition for %q has no levels", condition.RivalID)
		}
		next := 0
		for _, level := range condition.Levels {
			index := slices.Index(scoreMeasureOrder, level.Measure)
			if index < next {
				return fmt.Errorf("invalid measure order for %q", condition.RivalID)
			}
			next = index + 1
		}
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
