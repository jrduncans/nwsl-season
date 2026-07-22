package clinching

import (
	"fmt"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type Outcome string

const (
	HomeWin Outcome = "home_win"
	Draw    Outcome = "draw"
	AwayWin Outcome = "away_win"
)

type Status string

const (
	Clinched    Status = "clinched"
	NotClinched Status = "not_clinched"
	Unresolved  Status = "unresolved"
)

type ProofMethod string

const (
	ProofCheapBound            ProofMethod = "cheap_bound"
	ProofPointsOptimization    ProofMethod = "points_optimization"
	ProofAccessibleTiebreak    ProofMethod = "accessible_tiebreak"
	ProofMissingDisciplinary   ProofMethod = "missing_disciplinary_rule"
	ProofUnprovedScoreTiebreak ProofMethod = "unproved_score_tiebreak"
	ProofComputeBudget         ProofMethod = "compute_budget"
	ProofIncompleteSchedule    ProofMethod = "incomplete_schedule"
	ProofImplied               ProofMethod = "implied_achievement"
)

type FixedResult struct {
	GameID  string
	Outcome Outcome
}
type WitnessGame struct {
	GameID     string  `json:"game_id"`
	HomeTeamID string  `json:"home_team_id"`
	AwayTeamID string  `json:"away_team_id"`
	Outcome    Outcome `json:"outcome"`
	HomeScore  int     `json:"home_score"`
	AwayScore  int     `json:"away_score"`
}
type CountEvidence struct {
	Value int    `json:"value"`
	Kind  string `json:"kind"`
}
type NoHelpState string

const (
	NoHelpNotApplicable NoHelpState = "not_applicable"
	NoHelpGuaranteed    NoHelpState = "guaranteed"
	NoHelpImpossible    NoHelpState = "impossible"
	NoHelpUnresolved    NoHelpState = "unresolved"
)

type NoHelpPath struct {
	State      NoHelpState `json:"state"`
	FixtureIDs []string    `json:"fixture_ids"`
	Reason     string      `json:"reason"`
}
type Diagnostics struct {
	BoundCapableTeams   int   `json:"bound_capable_teams"`
	ReducedTeams        int   `json:"reduced_teams"`
	ReducedFixtures     int   `json:"reduced_fixtures"`
	ConnectedComponents int   `json:"connected_components"`
	SubsetProbes        int   `json:"subset_probes"`
	VisitedStates       int   `json:"visited_states"`
	MemoHits            int   `json:"memo_hits"`
	IndividualPrunes    int   `json:"individual_prunes"`
	ComponentPrunes     int   `json:"component_prunes"`
	TotalPrunes         int   `json:"total_prunes"`
	ElapsedMicroseconds int64 `json:"elapsed_microseconds"`
}
type AchievementResult struct {
	TeamID          string
	Achievement     competition.AchievementID
	TopK            int
	Status          Status
	Method          ProofMethod
	Reason          string
	StrictlyAhead   CountEvidence
	AtLeastLevel    CountEvidence
	BlockingWitness []WitnessGame
	FrontierWitness []WitnessGame
	NoHelp          NoHelpPath
	Diagnostics     Diagnostics
}
type Request struct {
	Teams        []standings.Team
	Games        []standings.Game
	FixtureOrder []string
	TargetTeamID string
	Achievement  competition.Achievement
	Fixed        []FixedResult
	validated    bool
	prepared     *preparedSeason
	omitWitness  bool
}

func validOutcome(o Outcome) bool { return o == HomeWin || o == Draw || o == AwayWin }
func validStatus(s Status) bool   { return s == Clinched || s == NotClinched || s == Unresolved }
func validMethod(m ProofMethod) bool {
	switch m {
	case ProofCheapBound, ProofPointsOptimization, ProofAccessibleTiebreak, ProofMissingDisciplinary, ProofUnprovedScoreTiebreak, ProofComputeBudget, ProofIncompleteSchedule, ProofImplied:
		return true
	}
	return false
}
func validEvidence(e CountEvidence) bool {
	return e.Value >= 0 && (e.Kind == "exact" || e.Kind == "lower_bound" || e.Kind == "upper_bound")
}

func ValidStatus(s Status) bool          { return validStatus(s) }
func ValidMethod(m ProofMethod) bool     { return validMethod(m) }
func ValidEvidence(e CountEvidence) bool { return validEvidence(e) }
func validateRequest(r Request) error {
	if r.TargetTeamID == "" || r.Achievement.ID == "" || r.Achievement.TopK < 1 || r.Achievement.TopK > len(r.Teams) {
		return fmt.Errorf("invalid achievement request")
	}
	teams := map[string]bool{}
	for _, t := range r.Teams {
		if t.ID == "" || teams[t.ID] {
			return fmt.Errorf("duplicate or empty team ID %q", t.ID)
		}
		teams[t.ID] = true
	}
	if !teams[r.TargetTeamID] {
		return fmt.Errorf("target team %q not found", r.TargetTeamID)
	}
	games := map[string]standings.Game{}
	unfinished := map[string]bool{}
	for _, g := range r.Games {
		if g.ID == "" {
			return fmt.Errorf("game ID is required")
		}
		if _, ok := games[g.ID]; ok {
			return fmt.Errorf("duplicate game ID %q", g.ID)
		}
		if !teams[g.HomeTeamID] || !teams[g.AwayTeamID] || g.HomeTeamID == g.AwayTeamID {
			return fmt.Errorf("game %q has unknown teams", g.ID)
		}
		games[g.ID] = g
		switch g.Status {
		case standings.CompletedStatus:
			if g.HomeScore == nil || g.AwayScore == nil {
				return fmt.Errorf("completed game %q lacks score", g.ID)
			}
		case "PreMatch":
			if g.HomeScore != nil || g.AwayScore != nil {
				return fmt.Errorf("prematch game %q has score", g.ID)
			}
			unfinished[g.ID] = true
		default:
			return fmt.Errorf("game %q has unsafe status %q", g.ID, g.Status)
		}
	}
	if len(r.FixtureOrder) != len(unfinished) {
		return fmt.Errorf("fixture order must contain each unfinished game")
	}
	ordered := map[string]bool{}
	for _, id := range r.FixtureOrder {
		if !unfinished[id] || ordered[id] {
			return fmt.Errorf("invalid fixture order game %q", id)
		}
		ordered[id] = true
	}
	fixed := map[string]bool{}
	for _, f := range r.Fixed {
		if !unfinished[f.GameID] || fixed[f.GameID] || !validOutcome(f.Outcome) {
			return fmt.Errorf("invalid fixed result %q", f.GameID)
		}
		fixed[f.GameID] = true
	}
	return nil
}
