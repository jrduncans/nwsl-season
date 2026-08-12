// Package qualification calculates and persists one complete qualification
// batch after a fixture cache refresh.
package qualification

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Store interface {
	QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
	ReplaceQualification(context.Context, cache.QualificationRun, []cache.QualificationStatus) (cache.QualificationSnapshot, error)
	RecordQualificationFailure(context.Context, cache.QualificationRun, error) error
}
type Refresher struct {
	Store    Store
	Rules    competition.Rules
	Budget   time.Duration
	Progress func(Progress)
}

// Progress reports one qualification proof boundary. Callers can use it for
// operational telemetry without coupling the proof package to a logger.
type Progress struct {
	Phase        string
	TeamID       string
	Achievement  competition.Achievement
	Completed    int
	Total        int
	Elapsed      time.Duration
	BatchElapsed time.Duration
	Status       clinching.Status
	Method       clinching.ProofMethod
	NoHelpState  clinching.NoHelpState
}

// calculationTelemetry keeps loop-level work on qualification.refresh. A
// completed leaf span is retained only when the individual operation is slow
// enough to matter in the waterfall or fails.
type calculationTelemetry struct {
	statusChecks, skippedStatusProofs, noHelpBatches, skippedNoHelpBatches int
	statusProofDuration, noHelpDuration                                    time.Duration
	slowStatusProofs, slowNoHelpBatches                                    int
	slowestStatusProof, slowestNoHelpBatch                                 calculationAttempt
	maxReducedTeams, maxReducedFixtures, maxConnectedComponents            int
	totalSubsetProbes, totalVisitedStates, totalMemoHits, totalPrunes      int
}

type calculationAttempt struct {
	duration              time.Duration
	teamID, achievementID string
}

func (t *calculationTelemetry) recordStatusProof(duration time.Duration, teamID string, achievement competition.Achievement) {
	t.statusChecks++
	t.statusProofDuration += duration
	if duration >= telemetry.SlowOperationThreshold {
		t.slowStatusProofs++
	}
	if duration > t.slowestStatusProof.duration {
		t.slowestStatusProof = calculationAttempt{duration: duration, teamID: teamID, achievementID: string(achievement.ID)}
	}
}

func (t *calculationTelemetry) recordStatusProofDiagnostics(value clinching.AchievementResult) {
	if value.Diagnostics.ReducedTeams > t.maxReducedTeams {
		t.maxReducedTeams = value.Diagnostics.ReducedTeams
	}
	if value.Diagnostics.ReducedFixtures > t.maxReducedFixtures {
		t.maxReducedFixtures = value.Diagnostics.ReducedFixtures
	}
	if value.Diagnostics.ConnectedComponents > t.maxConnectedComponents {
		t.maxConnectedComponents = value.Diagnostics.ConnectedComponents
	}
	t.totalSubsetProbes += value.Diagnostics.SubsetProbes
	t.totalVisitedStates += value.Diagnostics.VisitedStates
	t.totalMemoHits += value.Diagnostics.MemoHits
	t.totalPrunes += value.Diagnostics.TotalPrunes
}

func (t *calculationTelemetry) recordNoHelpBatch(duration time.Duration, teamID string) {
	t.noHelpBatches++
	t.noHelpDuration += duration
	if duration >= telemetry.SlowOperationThreshold {
		t.slowNoHelpBatches++
	}
	if duration > t.slowestNoHelpBatch.duration {
		t.slowestNoHelpBatch = calculationAttempt{duration: duration, teamID: teamID}
	}
}

func (t calculationTelemetry) attributes(statuses []cache.QualificationStatus) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.Int("nwsl.qualification.status_check_count", t.statusChecks),
		attribute.Int("nwsl.qualification.status_proof.skipped_count", t.skippedStatusProofs),
		attribute.Float64("nwsl.qualification.status_proof.duration_total_ms", float64(t.statusProofDuration)/float64(time.Millisecond)),
		attribute.Float64("nwsl.qualification.status_proof.duration_max_ms", float64(t.slowestStatusProof.duration)/float64(time.Millisecond)),
		attribute.Int("nwsl.qualification.status_proof.slow_count", t.slowStatusProofs),
		attribute.Int("nwsl.qualification.status_proof.reduced_team_count.max", t.maxReducedTeams),
		attribute.Int("nwsl.qualification.status_proof.reduced_fixture_count.max", t.maxReducedFixtures),
		attribute.Int("nwsl.qualification.status_proof.connected_component_count.max", t.maxConnectedComponents),
		attribute.Int("nwsl.qualification.status_proof.subset_probe_count.total", t.totalSubsetProbes),
		attribute.Int("nwsl.qualification.status_proof.visited_state_count.total", t.totalVisitedStates),
		attribute.Int("nwsl.qualification.status_proof.memo_hit_count.total", t.totalMemoHits),
		attribute.Int("nwsl.qualification.status_proof.prune_count.total", t.totalPrunes),
		attribute.Int("nwsl.qualification.no_help_batch_count", t.noHelpBatches),
		attribute.Int("nwsl.qualification.no_help_batch.skipped_count", t.skippedNoHelpBatches),
		attribute.Float64("nwsl.qualification.no_help_batch.duration_total_ms", float64(t.noHelpDuration)/float64(time.Millisecond)),
		attribute.Float64("nwsl.qualification.no_help_batch.duration_max_ms", float64(t.slowestNoHelpBatch.duration)/float64(time.Millisecond)),
		attribute.Int("nwsl.qualification.no_help_batch.slow_count", t.slowNoHelpBatches),
	}
	if t.slowestStatusProof.duration > 0 {
		attributes = append(attributes,
			attribute.String("nwsl.qualification.status_proof.slowest_team_id", t.slowestStatusProof.teamID),
			attribute.String("nwsl.qualification.status_proof.slowest_achievement_id", t.slowestStatusProof.achievementID),
		)
	}
	if t.slowestNoHelpBatch.duration > 0 {
		attributes = append(attributes, attribute.String("nwsl.qualification.no_help_batch.slowest_team_id", t.slowestNoHelpBatch.teamID))
	}

	statusCounts := map[clinching.Status]int{}
	methodCounts := map[clinching.ProofMethod]int{}
	noHelpCounts := map[clinching.NoHelpState]int{}
	budgetExhausted := 0
	for _, status := range statuses {
		statusCounts[status.Status]++
		methodCounts[status.Method]++
		noHelpCounts[status.NoHelp.State]++
		if status.Method == clinching.ProofComputeBudget ||
			(status.NoHelp.State == clinching.NoHelpUnresolved && status.NoHelp.Reason == "calculation budget exhausted") {
			budgetExhausted++
		}
	}
	for _, status := range []clinching.Status{clinching.Clinched, clinching.NotClinched, clinching.Unresolved} {
		attributes = append(attributes, attribute.Int("nwsl.qualification.result.status."+string(status)+"_count", statusCounts[status]))
	}
	for _, method := range []clinching.ProofMethod{
		clinching.ProofCheapBound,
		clinching.ProofPointsOptimization,
		clinching.ProofAccessibleTiebreak,
		clinching.ProofMissingDisciplinary,
		clinching.ProofUnprovedScoreTiebreak,
		clinching.ProofComputeBudget,
		clinching.ProofIncompleteSchedule,
		clinching.ProofImplied,
	} {
		attributes = append(attributes, attribute.Int("nwsl.qualification.result.method."+string(method)+"_count", methodCounts[method]))
	}
	for _, state := range []clinching.NoHelpState{
		clinching.NoHelpNotApplicable,
		clinching.NoHelpGuaranteed,
		clinching.NoHelpImpossible,
		clinching.NoHelpUnresolved,
	} {
		attributes = append(attributes, attribute.Int("nwsl.qualification.result.no_help."+string(state)+"_count", noHelpCounts[state]))
	}
	return append(attributes, attribute.Int("nwsl.qualification.result.budget_exhausted_count", budgetExhausted))
}

func (r Refresher) Refresh(ctx context.Context, syncRun cache.SyncRun, teams []cache.Team, games []cache.Game, force bool) (result cache.DerivedRefreshResult, err error) {
	budget := r.Budget
	if budget <= 0 {
		budget = 5 * time.Second
	}
	parentSpan := trace.SpanFromContext(ctx)
	recordDecisionException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, parentSpan, cause, "qualification.refresh", errorType)
	}
	if r.Store == nil {
		return result, recordDecisionException(fmt.Errorf("qualification store is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if err := r.Rules.Validate(); err != nil {
		return result, recordDecisionException(err, telemetry.ErrorTypeInvalidArgument)
	}
	if syncRun.FixtureSnapshotID == "" {
		return result, recordDecisionException(fmt.Errorf("fixture snapshot ID is required"), telemetry.ErrorTypeInvalidArgument)
	}
	r.Budget = budget
	snapshot, ok, err := r.Store.QualificationForSnapshot(ctx, syncRun.FixtureSnapshotID, r.Rules.Version)
	if err != nil {
		return result, recordDecisionException(err, telemetry.ErrorTypeStorageFailure)
	}
	result.SnapshotChecked = true
	result.SnapshotFound = ok
	result.RulesVersion = r.Rules.Version
	refreshReason := "snapshot_missing"
	if force {
		refreshReason = "forced"
	} else if ok {
		retryKickoffOrder := shouldRetryKickoffOrder(snapshot, games)
		retryComputeBudget := shouldRetryComputeBudget(snapshot)
		switch {
		case retryKickoffOrder && retryComputeBudget:
			refreshReason = "kickoff_order_and_compute_budget_retry"
		case retryKickoffOrder:
			refreshReason = "kickoff_order_retry"
		case retryComputeBudget:
			refreshReason = "compute_budget_retry"
		default:
			result.Reason = "snapshot_current"
			return result, nil
		}
	}
	result.Recalculated = true
	result.Required = true
	result.Reason = refreshReason
	ctx, span := telemetry.Tracer().Start(ctx, "qualification.refresh",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("nwsl.season", syncRun.Season),
			attribute.String("nwsl.stage", syncRun.Stage),
			attribute.String("nwsl.cache.fixture_snapshot_id", syncRun.FixtureSnapshotID),
			attribute.Int("nwsl.qualification.team_count", len(teams)),
			attribute.Int("nwsl.qualification.fixture_count", len(games)),
			attribute.Int64("nwsl.qualification.budget_ms", budget.Milliseconds()),
			attribute.Bool("nwsl.qualification.forced", force),
			attribute.String("nwsl.qualification.refresh_reason", refreshReason),
		),
	)
	recordRefreshException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, span, cause, "qualification.refresh", errorType)
	}
	defer func() {
		span.SetAttributes(
			attribute.Bool("nwsl.qualification.recalculated", result.Recalculated),
			attribute.String("nwsl.qualification.outcome", calculationOutcome(result.Recalculated, err)),
		)
		if err != nil {
			telemetry.MarkError(span, err)
		}
		span.End()
	}()
	run := cache.QualificationRun{FixtureSnapshotID: syncRun.FixtureSnapshotID, SourceSyncRunID: syncRun.ID, Season: syncRun.Season, Stage: syncRun.Stage, RulesVersion: r.Rules.Version, StartedAt: time.Now().UTC(), ExpectedStatuses: r.Rules.ExpectedTeams * len(r.Rules.Achievements), WrittenStatuses: r.Rules.ExpectedTeams * len(r.Rules.Achievements)}
	values, err := r.calculate(ctx, teams, games)
	if err != nil {
		_ = r.Store.RecordQualificationFailure(context.Background(), run, err)
		return result, err
	}
	_, err = r.Store.ReplaceQualification(context.Background(), run, values)
	if err != nil {
		err = recordRefreshException(err, telemetry.ErrorTypeStorageFailure)
		_ = r.Store.RecordQualificationFailure(context.Background(), run, err)
		return result, err
	}
	return result, nil
}

// Older batches could be marked complete after the refresher rejected ASA's
// legacy "YYYY-MM-DD HH:MM:SS UTC" timestamp form. Once the parser accepts the
// current schedule, retry that narrowly identified batch instead of treating
// its unresolved rows as a valid prerequisite forever.
func shouldRetryKickoffOrder(snapshot cache.QualificationSnapshot, games []cache.Game) bool {
	if len(snapshot.Statuses) == 0 {
		return false
	}
	for _, status := range snapshot.Statuses {
		if status.Method != clinching.ProofIncompleteSchedule || status.Reason != "fixture kickoff order is invalid" {
			return false
		}
	}
	_, err := fixtureOrder(games)
	return err == nil
}

// Compute-budget rows are transient: a later run may be configured with a
// larger budget after profiling or deployment configuration changes. Retry
// such a batch instead of permanently treating its unresolved rows as the
// current qualification baseline.
func shouldRetryComputeBudget(snapshot cache.QualificationSnapshot) bool {
	for _, status := range snapshot.Statuses {
		if status.Method == clinching.ProofComputeBudget ||
			(status.NoHelp.State == clinching.NoHelpUnresolved && status.NoHelp.Reason == "calculation budget exhausted") {
			return true
		}
	}
	return false
}
func (r Refresher) calculate(ctx context.Context, teams []cache.Team, games []cache.Game) (statuses []cache.QualificationStatus, err error) {
	batchStarted := time.Now()
	calculation := calculationTelemetry{}
	span := trace.SpanFromContext(ctx)
	recordCalculationException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, span, cause, "qualification.refresh", errorType)
	}
	span.SetAttributes(
		attribute.Int("nwsl.qualification.input_team_count", len(teams)),
		attribute.Int("nwsl.qualification.input_fixture_count", len(games)),
		attribute.Int("nwsl.qualification.completed_fixture_count", completedFixtures(games)),
		attribute.Int("nwsl.qualification.remaining_fixture_count", remainingFixtures(games)),
		attribute.StringSlice("nwsl.qualification.achievement_ids", achievementIDs(r.Rules.Achievements)),
	)
	defer func() {
		span.SetAttributes(calculation.attributes(statuses)...)
	}()
	participants := map[string]bool{}
	for _, g := range games {
		participants[g.HomeTeamID] = true
		participants[g.AwayTeamID] = true
	}
	domainTeams := make([]standings.Team, 0, len(participants))
	byID := map[string]cache.Team{}
	for _, t := range teams {
		byID[t.ASAID] = t
	}
	for id := range participants {
		t, ok := byID[id]
		if !ok {
			return nil, recordCalculationException(fmt.Errorf("fixture references missing team %q", id), telemetry.ErrorTypeInvalidData)
		}
		domainTeams = append(domainTeams, standings.Team{ID: t.ASAID, Name: t.Name, ShortName: t.ShortName, Abbreviation: t.Abbreviation})
	}
	sort.Slice(domainTeams, func(i, j int) bool { return domainTeams[i].ID < domainTeams[j].ID })
	domainGames := make([]standings.Game, 0, len(games))
	for _, g := range games {
		d := standings.Game{ID: g.ASAID, Status: g.Status, HomeTeamID: g.HomeTeamID, AwayTeamID: g.AwayTeamID}
		if g.HomeScore.Valid {
			x := int(g.HomeScore.Int64)
			d.HomeScore = &x
		}
		if g.AwayScore.Valid {
			x := int(g.AwayScore.Int64)
			d.AwayScore = &x
		}
		domainGames = append(domainGames, d)
	}
	if !completeInventory(r.Rules, domainTeams, domainGames) || !safeFixtureStates(games) {
		return unresolvedRows(domainTeams, r.Rules, clinching.ProofIncompleteSchedule, "fixture inventory is incomplete"), nil
	}
	order, err := fixtureOrder(games)
	if err != nil {
		return unresolvedRows(domainTeams, r.Rules, clinching.ProofIncompleteSchedule, "fixture kickoff order is invalid"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.Budget)
	defer cancel()
	evaluator, err := clinching.NewEvaluator(domainTeams, domainGames, order)
	if err != nil {
		return nil, recordCalculationException(err, telemetry.ErrorTypeCalculationFailure)
	}
	table := standings.Calculate(domainTeams, domainGames, standings.OfficialTotalRules())
	achievements := append([]competition.Achievement(nil), r.Rules.Achievements...)
	sort.Slice(achievements, func(i, j int) bool { return achievements[i].TopK > achievements[j].TopK })
	results := map[string]map[competition.AchievementID]cache.QualificationStatus{}
	completed := 0
	total := len(table) * len(achievements)
	for _, row := range table {
		results[row.Team.ID] = map[competition.AchievementID]cache.QualificationStatus{}
		for _, a := range achievements {
			if ctx.Err() != nil {
				results[row.Team.ID][a.ID] = unresolved(row.Team.ID, a, clinching.ProofComputeBudget, "calculation budget exhausted")
				calculation.skippedStatusProofs++
				completed++
				r.report(Progress{Phase: "skipped", TeamID: row.Team.ID, Achievement: a, Completed: completed, Total: total, BatchElapsed: time.Since(batchStarted), Status: clinching.Unresolved, Method: clinching.ProofComputeBudget})
				continue
			}
			probeStarted := time.Now()
			r.report(Progress{Phase: "status_started", TeamID: row.Team.ID, Achievement: a, Completed: completed, Total: total, BatchElapsed: time.Since(batchStarted)})
			proofAttributes := qualificationStatusProofAttributes(row.Team.ID, a, games)
			value, evaluateErr := evaluator.EvaluateStatus(ctx, row.Team.ID, a, nil)
			finished := time.Now()
			duration := finished.Sub(probeStarted)
			calculation.recordStatusProof(duration, row.Team.ID, a)
			if evaluateErr != nil {
				evaluateErr = telemetry.ClassifyError(evaluateErr, telemetry.ErrorTypeCalculationFailure)
				telemetry.RecordCompletedWarningSpan(ctx, "qualification.status_proof", probeStarted, finished, proofAttributes, evaluateErr, "qualification.status_proof")
				return nil, evaluateErr
			}
			calculation.recordStatusProofDiagnostics(value)
			if duration >= telemetry.SlowOperationThreshold {
				telemetry.RecordCompletedSpan(ctx, "qualification.status_proof", probeStarted, finished, append(proofAttributes, qualificationStatusProofResultAttributes(value)...), nil, "")
			}
			results[row.Team.ID][a.ID] = toCache(value)
			completed++
			r.report(Progress{Phase: "status_finished", TeamID: row.Team.ID, Achievement: a, Completed: completed, Total: total, Elapsed: duration, BatchElapsed: time.Since(batchStarted), Status: value.Status, Method: value.Method})
		}
	}
	// Stronger guarantees imply weaker guarantees, never the reverse.
	for id, byAchievement := range results {
		for i, strong := range r.Rules.Achievements {
			if byAchievement[strong.ID].Status != clinching.Clinched {
				continue
			}
			for _, weak := range r.Rules.Achievements[i+1:] {
				v := byAchievement[weak.ID]
				if v.Status != clinching.Clinched {
					v.Status = clinching.Clinched
					v.Method = clinching.ProofImplied
					v.Reason = "implied by " + string(strong.ID)
					v.NoHelp = clinching.NoHelpPath{State: clinching.NoHelpNotApplicable, FixtureIDs: []string{}}
					byAchievement[weak.ID] = v
				}
			}
		}
		results[id] = byAchievement
	}
	noHelpTotal := 0
	for _, byAchievement := range results {
		for _, value := range byAchievement {
			if value.Status == clinching.NotClinched {
				noHelpTotal++
			}
		}
	}
	noHelpCompleted := 0
	for _, row := range table {
		teamAchievements := []competition.Achievement{}
		bases := map[competition.AchievementID]clinching.AchievementResult{}
		for _, a := range achievements {
			value := results[row.Team.ID][a.ID]
			if value.Status != clinching.NotClinched {
				continue
			}
			if ctx.Err() != nil {
				value.NoHelp = clinching.NoHelpPath{State: clinching.NoHelpUnresolved, FixtureIDs: []string{}, Reason: "calculation budget exhausted"}
				results[row.Team.ID][a.ID] = value
				calculation.skippedNoHelpBatches++
				noHelpCompleted++
				r.report(Progress{Phase: "no_help_skipped", TeamID: row.Team.ID, Achievement: a, Completed: noHelpCompleted, Total: noHelpTotal, BatchElapsed: time.Since(batchStarted), Status: value.Status, Method: value.Method, NoHelpState: value.NoHelp.State})
				continue
			}
			teamAchievements = append(teamAchievements, a)
			bases[a.ID] = clinching.AchievementResult{TeamID: value.TeamID, Achievement: value.Achievement, TopK: value.TopK, Status: value.Status, Method: value.Method, Reason: value.Reason}
		}
		if len(teamAchievements) == 0 {
			continue
		}
		probeStarted := time.Now()
		noHelpAttributes := qualificationNoHelpAttributes(row.Team.ID, teamAchievements, games)
		paths, evaluateErr := evaluator.EvaluateNoHelpBatch(ctx, row.Team.ID, teamAchievements, nil, bases)
		finished := time.Now()
		duration := finished.Sub(probeStarted)
		calculation.recordNoHelpBatch(duration, row.Team.ID)
		if evaluateErr != nil {
			evaluateErr = telemetry.ClassifyError(evaluateErr, telemetry.ErrorTypeCalculationFailure)
			telemetry.RecordCompletedWarningSpan(ctx, "qualification.no_help_batch", probeStarted, finished, noHelpAttributes, evaluateErr, "qualification.no_help_batch")
			return nil, evaluateErr
		}
		if duration >= telemetry.SlowOperationThreshold {
			telemetry.RecordCompletedSpan(ctx, "qualification.no_help_batch", probeStarted, finished, noHelpAttributes, nil, "")
		}
		for _, a := range teamAchievements {
			value := results[row.Team.ID][a.ID]
			value.NoHelp = paths[a.ID]
			results[row.Team.ID][a.ID] = value
			noHelpCompleted++
			r.report(Progress{Phase: "no_help_finished", TeamID: row.Team.ID, Achievement: a, Completed: noHelpCompleted, Total: noHelpTotal, Elapsed: duration, BatchElapsed: time.Since(batchStarted), Status: value.Status, Method: value.Method, NoHelpState: value.NoHelp.State})
		}
	}
	out := []cache.QualificationStatus{}
	ids := make([]string, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, a := range r.Rules.Achievements {
			out = append(out, results[id][a.ID])
		}
	}
	return out, nil
}

func qualificationStatusProofAttributes(teamID string, achievement competition.Achievement, games []cache.Game) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("nwsl.qualification.team_id", teamID),
		attribute.String("nwsl.qualification.achievement_id", string(achievement.ID)),
		attribute.Int("nwsl.qualification.top_k", achievement.TopK),
		attribute.Int("nwsl.qualification.completed_fixture_count", completedFixtures(games)),
		attribute.Int("nwsl.qualification.remaining_fixture_count", remainingFixtures(games)),
	}
}

func qualificationStatusProofResultAttributes(value clinching.AchievementResult) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("nwsl.qualification.status", string(value.Status)),
		attribute.String("nwsl.qualification.method", string(value.Method)),
		attribute.Int("nwsl.qualification.reduced_team_count", value.Diagnostics.ReducedTeams),
		attribute.Int("nwsl.qualification.reduced_fixture_count", value.Diagnostics.ReducedFixtures),
		attribute.Int("nwsl.qualification.connected_component_count", value.Diagnostics.ConnectedComponents),
		attribute.Int("nwsl.qualification.subset_probe_count", value.Diagnostics.SubsetProbes),
		attribute.Int("nwsl.qualification.visited_state_count", value.Diagnostics.VisitedStates),
		attribute.Int("nwsl.qualification.memo_hit_count", value.Diagnostics.MemoHits),
		attribute.Int("nwsl.qualification.prune_count", value.Diagnostics.TotalPrunes),
	}
}

func qualificationNoHelpAttributes(teamID string, achievements []competition.Achievement, games []cache.Game) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("nwsl.qualification.team_id", teamID),
		attribute.StringSlice("nwsl.qualification.achievement_ids", achievementIDs(achievements)),
		attribute.Int("nwsl.qualification.achievement_count", len(achievements)),
		attribute.Int("nwsl.qualification.completed_fixture_count", completedFixtures(games)),
		attribute.Int("nwsl.qualification.remaining_fixture_count", remainingFixtures(games)),
	}
}

func calculationOutcome(recalculated bool, err error) string {
	if err != nil {
		return "failure"
	}
	if recalculated {
		return "recalculated"
	}
	return "current"
}

func completedFixtures(games []cache.Game) int {
	count := 0
	for _, game := range games {
		if game.Status == fixtures.CompletedStatus {
			count++
		}
	}
	return count
}

func remainingFixtures(games []cache.Game) int {
	count := 0
	for _, game := range games {
		if game.Status == fixtures.PreMatchStatus {
			count++
		}
	}
	return count
}

func achievementIDs(values []competition.Achievement) []string {
	ids := make([]string, 0, len(values))
	for _, value := range values {
		ids = append(ids, string(value.ID))
	}
	return ids
}

func (r Refresher) report(value Progress) {
	if r.Progress != nil {
		r.Progress(value)
	}
}
func safeFixtureStates(games []cache.Game) bool {
	for _, g := range games {
		switch g.Status {
		case fixtures.CompletedStatus:
			if !g.HomeScore.Valid || !g.AwayScore.Valid {
				return false
			}
		case fixtures.PreMatchStatus:
			if g.HomeScore.Valid || g.AwayScore.Valid {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func completeInventory(r competition.Rules, teams []standings.Team, games []standings.Game) bool {
	if len(teams) != r.ExpectedTeams || len(games) != r.ExpectedTeams*r.GamesPerTeam/2 {
		return false
	}
	n := map[string]int{}
	directed := map[[2]string]int{}
	for _, g := range games {
		n[g.HomeTeamID]++
		n[g.AwayTeamID]++
		directed[[2]string{g.HomeTeamID, g.AwayTeamID}]++
	}
	for _, t := range teams {
		if n[t.ID] != r.GamesPerTeam {
			return false
		}
	}
	// A 2*(N-1)-game format is a double round robin: every pair must appear
	// once in each direction. Per-team degree alone cannot detect a duplicated
	// matchup that replaces two other scheduled fixtures.
	if r.GamesPerTeam == 2*(r.ExpectedTeams-1) {
		for _, home := range teams {
			for _, away := range teams {
				if home.ID != away.ID && directed[[2]string{home.ID, away.ID}] != 1 {
					return false
				}
			}
		}
	}
	return true
}
func fixtureOrder(games []cache.Game) ([]string, error) {
	pending := []cache.Game{}
	for _, g := range games {
		if g.Status == fixtures.PreMatchStatus {
			if _, err := fixtures.ParseKickoff(g.KickoffUTC); err != nil {
				return nil, err
			}
			pending = append(pending, g)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		a, _ := fixtures.ParseKickoff(pending[i].KickoffUTC)
		b, _ := fixtures.ParseKickoff(pending[j].KickoffUTC)
		if a.Equal(b) {
			return pending[i].ASAID < pending[j].ASAID
		}
		return a.Before(b)
	})
	out := make([]string, len(pending))
	for i, g := range pending {
		out[i] = g.ASAID
	}
	return out, nil
}

func unresolvedRows(teams []standings.Team, r competition.Rules, m clinching.ProofMethod, reason string) []cache.QualificationStatus {
	out := []cache.QualificationStatus{}
	for _, t := range teams {
		for _, a := range r.Achievements {
			out = append(out, unresolved(t.ID, a, m, reason))
		}
	}
	return out
}
func unresolved(team string, a competition.Achievement, m clinching.ProofMethod, reason string) cache.QualificationStatus {
	return cache.QualificationStatus{TeamID: team, Achievement: a.ID, TopK: a.TopK, Status: clinching.Unresolved, Method: m, Reason: reason, StrictlyAhead: clinching.CountEvidence{Value: 0, Kind: "lower_bound"}, AtLeastLevel: clinching.CountEvidence{Value: 0, Kind: "lower_bound"}, BlockingWitness: []clinching.WitnessGame{}, FrontierWitness: []clinching.WitnessGame{}, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, FixtureIDs: []string{}, Reason: reason}}
}
func toCache(v clinching.AchievementResult) cache.QualificationStatus {
	return cache.QualificationStatus{TeamID: v.TeamID, Achievement: v.Achievement, TopK: v.TopK, Status: v.Status, Method: v.Method, Reason: v.Reason, StrictlyAhead: v.StrictlyAhead, AtLeastLevel: v.AtLeastLevel, BlockingWitness: v.BlockingWitness, FrontierWitness: v.FrontierWitness, NoHelp: v.NoHelp, Diagnostics: v.Diagnostics}
}
