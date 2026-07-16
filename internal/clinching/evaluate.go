package clinching

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

var ErrComputeBudget = errors.New("clinching compute budget exceeded")

// Evaluate calculates a conservative qualification result. It intentionally
// treats any unresolved score tiebreak as unresolved rather than guessing a
// score cap.
func Evaluate(ctx context.Context, request Request) (AchievementResult, error) {
	result, err := evaluateStatus(ctx, request)
	if err != nil {
		return result, err
	}
	if result.Status == Clinched {
		result.NoHelp = NoHelpPath{State: NoHelpNotApplicable, FixtureIDs: []string{}}
		return result, nil
	}
	return addNoHelp(ctx, request, result)
}

func evaluateStatus(ctx context.Context, request Request) (AchievementResult, error) {
	started := time.Now()
	if err := validateRequest(request); err != nil {
		return AchievementResult{}, err
	}
	result := AchievementResult{TeamID: request.TargetTeamID, Achievement: request.Achievement.ID, TopK: request.Achievement.TopK, BlockingWitness: []WitnessGame{}, FrontierWitness: []WitnessGame{}, NoHelp: NoHelpPath{State: NoHelpNotApplicable, FixtureIDs: []string{}}}
	finish := func() AchievementResult {
		result.Diagnostics.ElapsedMicroseconds = time.Since(started).Microseconds()
		return result
	}
	unknown := 0
	for _, g := range request.Games {
		if g.Status == "PreMatch" {
			unknown++
		}
	}
	if unknown == 0 {
		result = finishCompleted(request, result)
		result.Diagnostics.ElapsedMicroseconds = time.Since(started).Microseconds()
		return result, nil
	}
	prepared, err := prepare(request)
	if err != nil {
		return result, err
	}
	result.Diagnostics.BoundCapableTeams = prepared.capable
	// Cheap strict witness: these opponents are ahead after all target losses.
	strictAhead := 0
	for id, points := range prepared.points {
		if id != request.TargetTeamID && points > prepared.frontier {
			strictAhead++
		}
	}
	if strictAhead >= request.Achievement.TopK {
		result.Status, result.Method = NotClinched, ProofCheapBound
		result.StrictlyAhead = CountEvidence{Value: strictAhead, Kind: "lower_bound"}
		result.AtLeastLevel = CountEvidence{Value: strictAhead, Kind: "lower_bound"}
		result.BlockingWitness = completeWitness(prepared, nil)
		return finish(), nil
	}
	if prepared.capable < request.Achievement.TopK {
		result.Status, result.Method = Clinched, ProofCheapBound
		result.StrictlyAhead = CountEvidence{Value: strictAhead, Kind: "upper_bound"}
		result.AtLeastLevel = CountEvidence{Value: prepared.capable, Kind: "upper_bound"}
		return finish(), nil
	}
	strict, err := solveThreshold(ctx, prepared, prepared.frontier+1)
	mergeDiagnostics(&result.Diagnostics, strict.diag)
	if err != nil {
		if errors.Is(err, ErrComputeBudget) {
			result.Status = Unresolved
			result.Method = ProofComputeBudget
			result.StrictlyAhead = CountEvidence{0, "lower_bound"}
			result.AtLeastLevel = CountEvidence{0, "lower_bound"}
			return finish(), nil
		}
		return result, err
	}
	result.StrictlyAhead = CountEvidence{Value: strict.count, Kind: "exact"}
	if strict.count >= request.Achievement.TopK {
		result.Status = NotClinched
		result.Method = ProofPointsOptimization
		result.BlockingWitness = completeWitness(prepared, strict.outcomes)
		return finish(), nil
	}
	level, err := solveThreshold(ctx, prepared, prepared.frontier)
	mergeDiagnostics(&result.Diagnostics, level.diag)
	if err != nil {
		if errors.Is(err, ErrComputeBudget) {
			result.Status = Unresolved
			result.Method = ProofComputeBudget
			result.StrictlyAhead = CountEvidence{0, "lower_bound"}
			result.AtLeastLevel = CountEvidence{0, "lower_bound"}
			return finish(), nil
		}
		return result, err
	}
	result.AtLeastLevel = CountEvidence{Value: level.count, Kind: "exact"}
	if level.count < request.Achievement.TopK {
		result.Status = Clinched
		result.Method = ProofPointsOptimization
	} else {
		result.Status = Unresolved
		result.Method = ProofUnprovedScoreTiebreak
		result.FrontierWitness = completeWitness(prepared, level.outcomes)
		result.Reason = "a points-level completion requires an unproved score tiebreak"
	}
	return finish(), nil
}

func finishCompleted(r Request, result AchievementResult) AchievementResult {
	table := standings.Calculate(r.Teams, r.Games, standings.OfficialTotalRules())
	target := -1
	for i, row := range table {
		if row.Team.ID == r.TargetTeamID {
			target = i
			break
		}
	}
	if target < 0 {
		return result
	}
	ahead := 0
	strictPointsAhead := 0
	targetPoints := 0
	for _, row := range table {
		if row.Team.ID == r.TargetTeamID {
			targetPoints = row.Record.Points
		}
	}
	undetermined := false
	for i, row := range table {
		if i < target {
			ahead++
		}
		if row.Team.ID != r.TargetTeamID && row.Record.Points > targetPoints {
			strictPointsAhead++
		}
		if row.Team.ID == r.TargetTeamID && row.TieBreak.Undetermined {
			undetermined = true
		}
	}
	result.StrictlyAhead = CountEvidence{Value: ahead, Kind: "exact"}
	result.AtLeastLevel = CountEvidence{Value: ahead, Kind: "exact"}
	if strictPointsAhead >= r.Achievement.TopK {
		result.Status = NotClinched
		result.Method = ProofAccessibleTiebreak
	} else if undetermined {
		result.Status = Unresolved
		result.Method = ProofMissingDisciplinary
		result.Reason = "least disciplinary points are unavailable"
	} else if ahead >= r.Achievement.TopK {
		result.Status = NotClinched
		result.Method = ProofAccessibleTiebreak
	} else {
		result.Status = Clinched
		result.Method = ProofAccessibleTiebreak
	}
	return result
}

func addNoHelp(ctx context.Context, r Request, base AchievementResult) (AchievementResult, error) {
	fixed := append([]FixedResult(nil), r.Fixed...)
	fixedSet := map[string]bool{}
	for _, f := range fixed {
		fixedSet[f.GameID] = true
	}
	prefix := []string{}
	for _, id := range r.FixtureOrder {
		g := gameByID(r.Games, id)
		if g.HomeTeamID != r.TargetTeamID && g.AwayTeamID != r.TargetTeamID || fixedSet[id] {
			continue
		}
		o := AwayWin
		if g.HomeTeamID == r.TargetTeamID {
			o = HomeWin
		}
		fixed = append(fixed, FixedResult{GameID: id, Outcome: o})
		prefix = append(prefix, id)
		probe := r
		probe.Fixed = fixed
		value, err := evaluateStatus(ctx, probe)
		if err != nil {
			return base, err
		}
		if value.Status == Clinched {
			base.NoHelp = NoHelpPath{State: NoHelpGuaranteed, FixtureIDs: append([]string(nil), prefix...)}
			return base, nil
		}
	}
	probe := r
	probe.Fixed = fixed
	value, err := evaluateStatus(ctx, probe)
	if err != nil {
		return base, err
	}
	switch value.Status {
	case NotClinched:
		base.NoHelp = NoHelpPath{State: NoHelpImpossible, FixtureIDs: []string{}, Reason: "even winning every remaining target fixture does not guarantee the achievement"}
	case Unresolved:
		base.NoHelp = NoHelpPath{State: NoHelpUnresolved, FixtureIDs: []string{}, Reason: value.Reason}
	default:
		base.NoHelp = NoHelpPath{State: NoHelpGuaranteed, FixtureIDs: append([]string(nil), prefix...)}
	}
	return base, nil
}

func gameByID(games []standings.Game, id string) standings.Game {
	for _, g := range games {
		if g.ID == id {
			return g
		}
	}
	return standings.Game{}
}
func mergeDiagnostics(to *Diagnostics, from Diagnostics) {
	to.ReducedTeams += from.ReducedTeams
	to.ReducedFixtures += from.ReducedFixtures
	to.ConnectedComponents += from.ConnectedComponents
	to.VisitedStates += from.VisitedStates
	to.MemoHits += from.MemoHits
}
func sortedIDs(values map[string]int) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
