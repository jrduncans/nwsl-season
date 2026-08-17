// Package scenariorefresh persists one all-or-nothing scenario batch after the
// matching qualification batch has completed.
package scenariorefresh

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Store interface {
	ScenarioForSnapshot(context.Context, string, string, string) (cache.ScenarioSnapshot, bool, error)
	QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
	ReplaceScenario(context.Context, cache.ScenarioRun, []cache.ScenarioResult) (cache.ScenarioSnapshot, error)
	RecordScenarioFailure(context.Context, cache.ScenarioRun, error) error
}
type Refresher struct {
	Store    Store
	Rules    competition.Rules
	Budget   time.Duration
	Progress func(Progress)
}

// Progress reports one team/achievement scenario calculation boundary.
type Progress struct {
	Phase        string
	TeamID       string
	Achievement  competition.Achievement
	Completed    int
	Total        int
	Elapsed      time.Duration
	BatchElapsed time.Duration
	State        scenarios.OpportunityState
}

func (r Refresher) Refresh(ctx context.Context, sync cache.SyncRun, teams []cache.Team, games []cache.Game, force bool) (result cache.DerivedRefreshResult, err error) {
	budget := r.Budget
	if budget <= 0 {
		budget = 30 * time.Second
	}
	parentSpan := trace.SpanFromContext(ctx)
	recordDecisionException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, parentSpan, cause, nwslconv.SpanScenarioRefresh, errorType)
	}
	if r.Store == nil {
		return result, recordDecisionException(fmt.Errorf("scenario store is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if err := r.Rules.Validate(); err != nil {
		return result, recordDecisionException(err, telemetry.ErrorTypeInvalidArgument)
	}
	if sync.FixtureSnapshotID == "" {
		return result, recordDecisionException(fmt.Errorf("fixture snapshot ID is required"), telemetry.ErrorTypeInvalidArgument)
	}
	snapshot, ok, err := r.Store.ScenarioForSnapshot(ctx, sync.FixtureSnapshotID, r.Rules.Version, scenarios.DefinitionVersion)
	if err != nil {
		return result, recordDecisionException(err, telemetry.ErrorTypeStorageFailure)
	}
	result.SnapshotChecked = true
	result.SnapshotFound = ok
	result.RulesVersion = r.Rules.Version
	result.DefinitionVersion = scenarios.DefinitionVersion
	refreshReason := "snapshot_missing"
	if force {
		refreshReason = "forced"
	} else if ok {
		if !shouldRetryComputeBudget(snapshot) {
			result.Reason = "snapshot_current"
			return result, nil
		}
		refreshReason = "compute_budget_retry"
	}
	result.Recalculated = true
	result.Required = true
	result.Reason = refreshReason
	ctx, span := telemetry.Tracer().Start(ctx, nwslconv.SpanScenarioRefresh, trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(nwslconv.Season(sync.Season), nwslconv.Stage(sync.Stage), nwslconv.CacheFixtureSnapshotID(sync.FixtureSnapshotID), nwslconv.ScenarioTeamCount(len(teams)), nwslconv.ScenarioFixtureCount(len(games)), nwslconv.ScenarioBudgetMs(int(budget.Milliseconds())), nwslconv.ScenarioForced(force), nwslconv.ScenarioRefreshReason(refreshReason)),
	)
	recordRefreshException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, span, cause, nwslconv.SpanScenarioRefresh, errorType)
	}
	defer func() {
		span.SetAttributes(nwslconv.ScenarioRecalculated(result.Recalculated), nwslconv.ScenarioOutcome(scenarioCalculationOutcome(result.Recalculated, err)))
		if err != nil {
			telemetry.MarkError(span, err)
		}
		span.End()
	}()
	q, ok, err := r.Store.QualificationForSnapshot(ctx, sync.FixtureSnapshotID, r.Rules.Version)
	if err != nil {
		return result, recordRefreshException(err, telemetry.ErrorTypeStorageFailure)
	}
	if !ok {
		return result, recordRefreshException(fmt.Errorf("matching qualification batch is required"), telemetry.ErrorTypeInvalidData)
	}
	run := cache.ScenarioRun{FixtureSnapshotID: sync.FixtureSnapshotID, QualificationRunID: q.Run.ID, SourceSyncRunID: sync.ID, Season: sync.Season, Stage: sync.Stage, RulesVersion: r.Rules.Version, DefinitionVersion: scenarios.DefinitionVersion, StartedAt: time.Now().UTC(), ExpectedResults: r.Rules.ExpectedTeams * len(r.Rules.Achievements), WrittenResults: r.Rules.ExpectedTeams * len(r.Rules.Achievements)}
	values, err := r.calculate(ctx, teams, games, q)
	if err != nil {
		_ = r.Store.RecordScenarioFailure(context.Background(), run, err)
		return result, err
	}
	run.Slate = values.slate
	_, err = r.Store.ReplaceScenario(context.Background(), run, values.rows)
	if err != nil {
		err = recordRefreshException(err, telemetry.ErrorTypeStorageFailure)
		_ = r.Store.RecordScenarioFailure(context.Background(), run, err)
	}
	return result, err
}

// A timeout is a transient computational limitation, not a mathematical
// result. Keep normal completed batches immutable, but retry a batch that was
// only incomplete because its shared calculation budget expired. This lets a
// later sync pick up an optimizer improvement or a larger configured budget.
func shouldRetryComputeBudget(snapshot cache.ScenarioSnapshot) bool {
	for _, result := range snapshot.Results {
		if result.BudgetLimited() {
			return true
		}
	}
	return false
}

type calculated struct {
	slate scenarios.Slate
	rows  []cache.ScenarioResult
}

// calculationTelemetry summarizes the repeated per-team searches on the
// scenario.refresh span. Individual searches retain a child span only when
// their timing is useful in the waterfall or they fail.
type calculationTelemetry struct {
	teamSearches, slowTeamSearches int
	teamSearchDuration             time.Duration
	slowestTeamSearch              teamSearchTelemetry
	total                          scenarioSearchDiagnostics
	maxSearchNodes                 int
	maxSearchNodesTeamID           string
}

type teamSearchTelemetry struct {
	duration time.Duration
	teamID   string
}

type scenarioSearchDiagnostics struct {
	assignments, certifiedAssignments, unresolvedAssignments   int
	searchNodes, oracleCalls, oracleCacheHits, visitedComplete int
}

func (t *calculationTelemetry) recordTeamSearch(duration time.Duration, teamID string, values map[competition.AchievementID]scenarios.Result) {
	diagnostics := scenarioSearchDiagnosticsFor(values)
	t.teamSearches++
	t.teamSearchDuration += duration
	if duration >= telemetry.SlowOperationThreshold {
		t.slowTeamSearches++
	}
	if duration > t.slowestTeamSearch.duration {
		t.slowestTeamSearch = teamSearchTelemetry{duration: duration, teamID: teamID}
	}
	if diagnostics.searchNodes > t.maxSearchNodes {
		t.maxSearchNodes = diagnostics.searchNodes
		t.maxSearchNodesTeamID = teamID
	}
	t.total.assignments += diagnostics.assignments
	t.total.certifiedAssignments += diagnostics.certifiedAssignments
	t.total.unresolvedAssignments += diagnostics.unresolvedAssignments
	t.total.searchNodes += diagnostics.searchNodes
	t.total.oracleCalls += diagnostics.oracleCalls
	t.total.oracleCacheHits += diagnostics.oracleCacheHits
	t.total.visitedComplete += diagnostics.visitedComplete
}

func (t calculationTelemetry) attributes(rows []cache.ScenarioResult) []attribute.KeyValue {
	attributes := []attribute.KeyValue{nwslconv.ScenarioTeamSearchCount(t.teamSearches), nwslconv.ScenarioTeamSearchDurationTotalMs(float64(t.teamSearchDuration) / float64(time.Millisecond)), nwslconv.ScenarioTeamSearchDurationMaxMs(float64(t.slowestTeamSearch.duration) / float64(time.Millisecond)), nwslconv.ScenarioTeamSearchSlowCount(t.slowTeamSearches), nwslconv.ScenarioAssignmentCountTotal(t.total.assignments), nwslconv.ScenarioCertifiedAssignmentCountTotal(t.total.certifiedAssignments), nwslconv.ScenarioUnresolvedAssignmentCountTotal(t.total.unresolvedAssignments), nwslconv.ScenarioSearchNodeCountTotal(t.total.searchNodes), nwslconv.ScenarioSearchNodeCountMax(t.maxSearchNodes), nwslconv.ScenarioOracleCallCountTotal(t.total.oracleCalls), nwslconv.ScenarioOracleCacheHitCountTotal(t.total.oracleCacheHits), nwslconv.ScenarioVisitedCompleteCountTotal(t.total.visitedComplete)}
	if t.slowestTeamSearch.duration > 0 {
		attributes = append(attributes, nwslconv.ScenarioTeamSearchSlowestTeamID(t.slowestTeamSearch.teamID))
	}
	if t.maxSearchNodesTeamID != "" {
		attributes = append(attributes, nwslconv.ScenarioSearchNodeCountMaxTeamID(t.maxSearchNodesTeamID))
	}

	stateCounts := map[scenarios.OpportunityState]int{}
	budgetLimited := 0
	for _, row := range rows {
		stateCounts[row.State]++
		if row.BudgetLimited() {
			budgetLimited++
		}
	}
	for _, state := range []scenarios.OpportunityState{
		scenarios.OpportunityAlreadyClinched,
		scenarios.OpportunityCanClinch,
		scenarios.OpportunityCannotClinch,
		scenarios.OpportunityTiebreakDependent,
		scenarios.OpportunityUnresolved,
	} {
		attributes = append(attributes, scenarioStateCount(state, stateCounts[state]))
	}
	return append(attributes, nwslconv.ScenarioResultBudgetLimitedCount(budgetLimited))
}

func scenarioStateCount(state scenarios.OpportunityState, count int) attribute.KeyValue {
	switch state {
	case scenarios.OpportunityAlreadyClinched:
		return nwslconv.ScenarioResultStateAlreadyClinchedCount(count)
	case scenarios.OpportunityCanClinch:
		return nwslconv.ScenarioResultStateCanClinchCount(count)
	case scenarios.OpportunityCannotClinch:
		return nwslconv.ScenarioResultStateCannotClinchCount(count)
	case scenarios.OpportunityTiebreakDependent:
		return nwslconv.ScenarioResultStateTiebreakDependentCount(count)
	case scenarios.OpportunityUnresolved:
		return nwslconv.ScenarioResultStateUnresolvedCount(count)
	default:
		panic("unsupported scenario opportunity state " + state)
	}
}

func scenarioSearchDiagnosticsFor(values map[competition.AchievementID]scenarios.Result) scenarioSearchDiagnostics {
	result := scenarioSearchDiagnostics{}
	for _, value := range values {
		if value.TotalAssignments > result.assignments {
			result.assignments = value.TotalAssignments
		}
		result.certifiedAssignments += value.CertifiedAssignments
		result.unresolvedAssignments += value.UnresolvedAssignments
		result.searchNodes += value.Diagnostics.SearchNodes
		result.oracleCalls += value.Diagnostics.OracleCalls
		result.oracleCacheHits += value.Diagnostics.OracleCacheHits
		result.visitedComplete += value.Diagnostics.VisitedComplete
	}
	return result
}

func (r Refresher) calculate(ctx context.Context, teams []cache.Team, games []cache.Game, q cache.QualificationSnapshot) (result calculated, err error) {
	batchStarted := time.Now()
	calculation := calculationTelemetry{}
	span := trace.SpanFromContext(ctx)
	recordCalculationException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, span, cause, nwslconv.SpanScenarioRefresh, errorType)
	}
	span.SetAttributes(nwslconv.ScenarioInputTeamCount(len(teams)), nwslconv.ScenarioInputFixtureCount(len(games)), nwslconv.ScenarioCompletedFixtureCount(scenarioCompletedFixtures(games)), nwslconv.ScenarioRemainingFixtureCount(scenarioRemainingFixtures(games)), nwslconv.ScenarioAchievementIds(scenarioAchievementIDs(r.Rules.Achievements)))
	defer func() {
		span.SetAttributes(calculation.attributes(result.rows)...)
	}()
	// The cache's team table is shared across seasons and can include former
	// clubs. Qualification scopes the snapshot to fixture participants; the
	// scenario evaluator must use that same participant set so its baseline
	// keys and official table are identical.
	participants := map[string]bool{}
	for _, g := range games {
		participants[g.HomeTeamID] = true
		participants[g.AwayTeamID] = true
	}
	byID := map[string]cache.Team{}
	for _, t := range teams {
		byID[t.ASAID] = t
	}
	domainTeams := []standings.Team{}
	for id := range participants {
		t, ok := byID[id]
		if !ok {
			return calculated{}, recordCalculationException(fmt.Errorf("fixture references missing team %q", id), telemetry.ErrorTypeInvalidData)
		}
		domainTeams = append(domainTeams, standings.Team{ID: t.ASAID, Name: t.Name, ShortName: t.ShortName, Abbreviation: t.Abbreviation})
	}
	sort.Slice(domainTeams, func(i, j int) bool { return domainTeams[i].ID < domainTeams[j].ID })
	domainGames := []standings.Game{}
	scheduled := []scenarios.ScheduledGame{}
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
		k, err := fixtures.ParseKickoff(g.KickoffUTC)
		if err != nil {
			return calculated{}, recordCalculationException(err, telemetry.ErrorTypeInvalidData)
		}
		sg := scenarios.ScheduledGame{ID: g.ASAID, Status: g.Status, HomeTeamID: g.HomeTeamID, AwayTeamID: g.AwayTeamID, HomeScore: d.HomeScore, AwayScore: d.AwayScore, KickoffUTC: k}
		if g.Matchday.Valid {
			x := int(g.Matchday.Int64)
			sg.Matchday = &x
		}
		scheduled = append(scheduled, sg)
	}
	slate, err := scenarios.DefineSlate(scheduled)
	if err != nil {
		return calculated{}, recordCalculationException(err, telemetry.ErrorTypeInvalidData)
	}
	span.SetAttributes(nwslconv.ScenarioSlateID(slate.ID), nwslconv.ScenarioSlateState(string(slate.State)), nwslconv.ScenarioSlateSource(string(slate.Source)), nwslconv.ScenarioSlateFixtureCount(len(slate.FixtureIDs)), nwslconv.ScenarioSlateReason(slate.Reason))
	order := []string{}
	pending := append([]scenarios.ScheduledGame(nil), scheduled...)
	sort.Slice(pending, func(i, j int) bool { return pending[i].KickoffUTC.Before(pending[j].KickoffUTC) })
	for _, g := range pending {
		if g.Status == fixtures.PreMatchStatus {
			order = append(order, g.ID)
		}
	}
	evaluator, err := clinching.NewEvaluator(domainTeams, domainGames, order)
	if err != nil {
		return calculated{}, recordCalculationException(err, telemetry.ErrorTypeCalculationFailure)
	}
	baseline := map[string]cache.QualificationStatus{}
	for _, v := range q.Statuses {
		baseline[v.TeamID+"\x00"+string(v.Achievement)] = v
	}
	budget := r.Budget
	if budget <= 0 {
		budget = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	rows := []cache.ScenarioResult{}
	completed := 0
	table := standings.Calculate(domainTeams, domainGames, standings.OfficialTotalRules())
	ach := append([]competition.Achievement(nil), r.Rules.Achievements...)
	sort.Slice(ach, func(i, j int) bool { return ach[i].TopK > ach[j].TopK })
	for _, t := range table {
		bases := make(map[competition.AchievementID]clinching.AchievementResult, len(ach))
		for _, a := range ach {
			b, ok := baseline[t.Team.ID+"\x00"+string(a.ID)]
			if !ok {
				return calculated{}, recordCalculationException(fmt.Errorf("qualification baseline missing for team %q achievement %q", t.Team.ID, a.ID), telemetry.ErrorTypeInvalidData)
			}
			bases[a.ID] = clinching.AchievementResult{TeamID: b.TeamID, Achievement: b.Achievement, TopK: b.TopK, Status: b.Status, Method: b.Method, Reason: b.Reason, NoHelp: b.NoHelp}
			r.report(Progress{Phase: "started", TeamID: t.Team.ID, Achievement: a, Completed: completed, Total: len(table) * len(ach), BatchElapsed: time.Since(batchStarted)})
		}
		probeStarted := time.Now()
		searchAttributes := scenarioGenerateTeamAttributes(t.Team.ID, ach, games, slate)
		teamResults, generateErr := scenarios.GenerateBatch(ctx, scenarios.BatchRequest{Evaluator: evaluator, Teams: domainTeams, Games: domainGames, Slate: slate, TargetTeamID: t.Team.ID, Achievements: ach, Baselines: bases})
		finished := time.Now()
		duration := finished.Sub(probeStarted)
		calculation.recordTeamSearch(duration, t.Team.ID, teamResults)
		if generateErr != nil {
			generateErr = telemetry.ClassifyError(generateErr, telemetry.ErrorTypeCalculationFailure)
			telemetry.RecordCompletedWarningSpan(ctx, nwslconv.SpanScenarioGenerateTeam, probeStarted, finished, searchAttributes, generateErr, nwslconv.SpanScenarioGenerateTeam)
			return calculated{}, generateErr
		}
		if duration >= telemetry.SlowOperationThreshold {
			telemetry.RecordCompletedSpan(ctx, nwslconv.SpanScenarioGenerateTeam, probeStarted, finished, append(searchAttributes, scenarioResultAttributes(teamResults)...), nil, "")
		}
		for _, a := range ach {
			v := teamResults[a.ID]
			rows = append(rows, cache.ScenarioResult{Result: v})
			completed++
			r.report(Progress{Phase: "finished", TeamID: t.Team.ID, Achievement: a, Completed: completed, Total: len(table) * len(ach), Elapsed: duration, BatchElapsed: time.Since(batchStarted), State: v.State})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TeamID == rows[j].TeamID {
			return rows[i].Achievement < rows[j].Achievement
		}
		return rows[i].TeamID < rows[j].TeamID
	})
	return calculated{slate: slate, rows: rows}, nil
}

func scenarioGenerateTeamAttributes(teamID string, achievements []competition.Achievement, games []cache.Game, slate scenarios.Slate) []attribute.KeyValue {
	return []attribute.KeyValue{nwslconv.ScenarioTeamID(teamID), nwslconv.ScenarioAchievementIds(scenarioAchievementIDs(achievements)), nwslconv.ScenarioAchievementCount(len(achievements)), nwslconv.ScenarioCompletedFixtureCount(scenarioCompletedFixtures(games)), nwslconv.ScenarioRemainingFixtureCount(scenarioRemainingFixtures(games)), nwslconv.ScenarioSlateState(string(slate.State)), nwslconv.ScenarioSlateSource(string(slate.Source)), nwslconv.ScenarioSlateFixtureCount(len(slate.FixtureIDs))}
}

func scenarioCalculationOutcome(recalculated bool, err error) string {
	if err != nil {
		return nwslconv.ScenarioOutcomeFailure
	}
	if recalculated {
		return nwslconv.ScenarioOutcomeRecalculated
	}
	return nwslconv.ScenarioOutcomeCurrent
}

func scenarioCompletedFixtures(games []cache.Game) int {
	count := 0
	for _, game := range games {
		if game.Status == fixtures.CompletedStatus {
			count++
		}
	}
	return count
}

func scenarioRemainingFixtures(games []cache.Game) int {
	count := 0
	for _, game := range games {
		if game.Status == fixtures.PreMatchStatus {
			count++
		}
	}
	return count
}

func scenarioAchievementIDs(values []competition.Achievement) []string {
	ids := make([]string, 0, len(values))
	for _, value := range values {
		ids = append(ids, string(value.ID))
	}
	return ids
}

func scenarioResultAttributes(values map[competition.AchievementID]scenarios.Result) []attribute.KeyValue {
	diagnostics := scenarioSearchDiagnosticsFor(values)
	return []attribute.KeyValue{nwslconv.ScenarioAssignmentCount(diagnostics.assignments), nwslconv.ScenarioCertifiedAssignmentCount(diagnostics.certifiedAssignments), nwslconv.ScenarioUnresolvedAssignmentCount(diagnostics.unresolvedAssignments), nwslconv.ScenarioSearchNodeCount(diagnostics.searchNodes), nwslconv.ScenarioOracleCallCount(diagnostics.oracleCalls), nwslconv.ScenarioOracleCacheHitCount(diagnostics.oracleCacheHits), nwslconv.ScenarioVisitedCompleteCount(diagnostics.visitedComplete)}
}

func (r Refresher) report(value Progress) {
	if r.Progress != nil {
		r.Progress(value)
	}
}
