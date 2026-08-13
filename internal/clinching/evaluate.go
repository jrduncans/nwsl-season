package clinching

import (
	"context"
	"errors"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

var ErrComputeBudget = errors.New("clinching compute budget exceeded")

// Evaluate calculates a conservative qualification result. It intentionally
// treats any unresolved score tiebreak as unresolved rather than guessing a
// score cap.
func Evaluate(ctx context.Context, request Request) (AchievementResult, error) {
	evaluator, err := NewEvaluator(request.Teams, request.Games, request.FixtureOrder)
	if err != nil {
		return AchievementResult{}, err
	}
	return evaluator.Evaluate(ctx, request.TargetTeamID, request.Achievement, request.Fixed)
}

func evaluateStatusRequest(ctx context.Context, request Request) (AchievementResult, error) {
	started := time.Now()
	if !request.validated {
		if err := validateRequest(request); err != nil {
			return AchievementResult{}, err
		}
	}
	result := AchievementResult{TeamID: request.TargetTeamID, Achievement: request.Achievement.ID, TopK: request.Achievement.TopK, BlockingWitness: []WitnessGame{}, FrontierWitness: []WitnessGame{}, NoHelp: NoHelpPath{State: NoHelpNotApplicable, FixtureIDs: []string{}}}
	finish := func() AchievementResult {
		result.Diagnostics.ElapsedMicroseconds = time.Since(started).Microseconds()
		return result
	}
	unknown := 0
	if request.prepared != nil {
		unknown = len(request.prepared.decision) + len(request.prepared.witness)
	} else {
		for _, g := range request.Games {
			if g.Status == "PreMatch" {
				unknown++
			}
		}
	}
	if unknown == 0 {
		result = finishCompleted(request, result)
		result.Diagnostics.ElapsedMicroseconds = time.Since(started).Microseconds()
		return result, nil
	}
	var prepared preparedSeason
	if request.prepared != nil {
		prepared = *request.prepared
	} else {
		var err error
		prepared, err = prepare(request)
		if err != nil {
			return result, err
		}
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
		if !request.omitWitness {
			result.BlockingWitness = completeWitness(prepared, nil)
		}
		return finish(), nil
	}
	if prepared.capable < request.Achievement.TopK {
		result.Status, result.Method = Clinched, ProofCheapBound
		result.StrictlyAhead = CountEvidence{Value: strictAhead, Kind: "upper_bound"}
		result.AtLeastLevel = CountEvidence{Value: prepared.capable, Kind: "upper_bound"}
		return finish(), nil
	}
	// A concrete feasible completion is sufficient to establish that the
	// target has not clinched. Try this inexpensive witness before invoking the
	// exact component optimizer, which is only needed when no easy blocker is
	// found.
	if witness := feasibleThresholdWitnessAtLeast(prepared, prepared.frontier+1, request.Achievement.TopK); witness.count >= request.Achievement.TopK {
		result.Status = NotClinched
		result.Method = ProofPointsOptimization
		result.StrictlyAhead = CountEvidence{Value: request.Achievement.TopK, Kind: "lower_bound"}
		result.AtLeastLevel = CountEvidence{Value: request.Achievement.TopK, Kind: "lower_bound"}
		if !request.omitWitness {
			verified := countThresholdWitness(prepared, prepared.frontier+1, witness.outcomes)
			if verified.count < request.Achievement.TopK {
				return result, errors.New("constructed blocking witness failed independent verification")
			}
			result.BlockingWitness = completeWitness(prepared, witness.outcomes)
		}
		return finish(), nil
	}
	strict, err := solveCutoff(ctx, prepared, prepared.frontier+1, request.Achievement.TopK)
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
	if strict.feasible {
		result.StrictlyAhead = CountEvidence{Value: request.Achievement.TopK, Kind: "lower_bound"}
		result.AtLeastLevel = CountEvidence{Value: request.Achievement.TopK, Kind: "lower_bound"}
		result.Status = NotClinched
		result.Method = ProofPointsOptimization
		if !request.omitWitness {
			result.BlockingWitness = completeWitness(prepared, strict.outcomes)
		}
		return finish(), nil
	}
	result.StrictlyAhead = CountEvidence{Value: request.Achievement.TopK - 1, Kind: "upper_bound"}
	level, err := solveCutoff(ctx, prepared, prepared.frontier, request.Achievement.TopK)
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
	if !level.feasible {
		result.AtLeastLevel = CountEvidence{Value: request.Achievement.TopK - 1, Kind: "upper_bound"}
		result.Status = Clinched
		result.Method = ProofPointsOptimization
	} else {
		result.AtLeastLevel = CountEvidence{Value: request.Achievement.TopK, Kind: "lower_bound"}
		result.Status = Unresolved
		result.Method = ProofUnprovedScoreTiebreak
		if !request.omitWitness {
			result.FrontierWitness = completeWitness(prepared, level.outcomes)
		}
		result.Reason = "a points-level completion requires an unproved score tiebreak"
	}
	return finish(), nil
}

func finishCompleted(r Request, result AchievementResult) AchievementResult {
	table := standings.Calculate(r.Teams, r.Games, standings.OfficialTotalRules())
	targetIndex := -1
	var targetRow standings.TableRow
	for i, row := range table {
		if row.Team.ID == r.TargetTeamID {
			targetIndex = i
			targetRow = row
			break
		}
	}
	if targetIndex < 0 {
		return result
	}

	if !targetRow.TieBreak.Undetermined {
		result.StrictlyAhead = CountEvidence{Value: targetIndex, Kind: "exact"}
		result.AtLeastLevel = CountEvidence{Value: targetIndex, Kind: "exact"}
		if targetIndex >= r.Achievement.TopK {
			result.Status = NotClinched
		} else {
			result.Status = Clinched
		}
		result.Method = ProofAccessibleTiebreak
		return result
	}

	tied := make(map[string]bool, len(targetRow.TieBreak.TiedTeamIDs))
	for _, id := range targetRow.TieBreak.TiedTeamIDs {
		tied[id] = true
	}
	tied[r.TargetTeamID] = true
	definitelyAhead := 0
	for i := 0; i < targetIndex; i++ {
		if !tied[table[i].Team.ID] {
			definitelyAhead++
		}
	}
	possiblyAhead := definitelyAhead + len(tied) - 1
	result.StrictlyAhead = CountEvidence{Value: definitelyAhead, Kind: "lower_bound"}
	result.AtLeastLevel = CountEvidence{Value: possiblyAhead, Kind: "upper_bound"}
	switch {
	case definitelyAhead >= r.Achievement.TopK:
		result.Status = NotClinched
		result.Method = ProofAccessibleTiebreak
	case possiblyAhead < r.Achievement.TopK:
		result.Status = Clinched
		result.Method = ProofAccessibleTiebreak
	default:
		result.Status = Unresolved
		result.Method = ProofMissingDisciplinary
		result.Reason = "least disciplinary points are unavailable"
	}
	return result
}

func mergeDiagnostics(to *Diagnostics, from Diagnostics) {
	to.ReducedTeams += from.ReducedTeams
	to.ReducedFixtures += from.ReducedFixtures
	to.ConnectedComponents += from.ConnectedComponents
	to.SubsetProbes += from.SubsetProbes
	to.VisitedStates += from.VisitedStates
	to.MemoHits += from.MemoHits
	to.IndividualPrunes += from.IndividualPrunes
	to.ComponentPrunes += from.ComponentPrunes
	to.TotalPrunes += from.TotalPrunes
}
