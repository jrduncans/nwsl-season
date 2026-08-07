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

func (r Refresher) Refresh(ctx context.Context, sync cache.SyncRun, teams []cache.Team, games []cache.Game, force bool) (recalculated bool, err error) {
	budget := r.Budget
	if budget <= 0 {
		budget = 30 * time.Second
	}
	ctx, span := telemetry.Tracer().Start(ctx, "scenario.refresh",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("sync.season", sync.Season),
			attribute.String("sync.stage", sync.Stage),
			attribute.String("cache.fixture_snapshot_id", sync.FixtureSnapshotID),
			attribute.Int("scenario.team_count", len(teams)),
			attribute.Int("scenario.fixture_count", len(games)),
			attribute.Int64("scenario.budget_ms", budget.Milliseconds()),
			attribute.Bool("scenario.forced", force),
		),
	)
	recordRefreshException := func(cause error, errorType string) error {
		return telemetry.RecordWarningWithType(ctx, span, cause, "scenario.refresh", errorType)
	}
	defer func() {
		span.SetAttributes(
			attribute.Bool("scenario.recalculated", recalculated),
			attribute.String("scenario.outcome", scenarioCalculationOutcome(recalculated, err)),
		)
		if err != nil {
			telemetry.MarkError(span, err)
		}
		span.End()
	}()
	if r.Store == nil {
		return false, recordRefreshException(fmt.Errorf("scenario store is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if err := r.Rules.Validate(); err != nil {
		return false, recordRefreshException(err, telemetry.ErrorTypeInvalidArgument)
	}
	if sync.FixtureSnapshotID == "" {
		return false, recordRefreshException(fmt.Errorf("fixture snapshot ID is required"), telemetry.ErrorTypeInvalidArgument)
	}
	if snapshot, ok, err := r.Store.ScenarioForSnapshot(ctx, sync.FixtureSnapshotID, r.Rules.Version, scenarios.DefinitionVersion); err != nil {
		return false, recordRefreshException(err, telemetry.ErrorTypeStorageFailure)
	} else if ok && !force && !shouldRetryComputeBudget(snapshot) {
		return false, nil
	}
	q, ok, err := r.Store.QualificationForSnapshot(ctx, sync.FixtureSnapshotID, r.Rules.Version)
	if err != nil {
		return false, recordRefreshException(err, telemetry.ErrorTypeStorageFailure)
	}
	if !ok {
		return false, recordRefreshException(fmt.Errorf("matching qualification batch is required"), telemetry.ErrorTypeInvalidData)
	}
	run := cache.ScenarioRun{FixtureSnapshotID: sync.FixtureSnapshotID, QualificationRunID: q.Run.ID, SourceSyncRunID: sync.ID, Season: sync.Season, Stage: sync.Stage, RulesVersion: r.Rules.Version, DefinitionVersion: scenarios.DefinitionVersion, StartedAt: time.Now().UTC(), ExpectedResults: r.Rules.ExpectedTeams * len(r.Rules.Achievements), WrittenResults: r.Rules.ExpectedTeams * len(r.Rules.Achievements)}
	values, err := r.calculate(ctx, teams, games, q)
	if err != nil {
		_ = r.Store.RecordScenarioFailure(context.Background(), run, err)
		return true, err
	}
	run.Slate = values.slate
	_, err = r.Store.ReplaceScenario(context.Background(), run, values.rows)
	if err != nil {
		err = recordRefreshException(err, telemetry.ErrorTypeStorageFailure)
		_ = r.Store.RecordScenarioFailure(context.Background(), run, err)
	}
	return true, err
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
	attributes := []attribute.KeyValue{
		attribute.Int("scenario.team_search_count", t.teamSearches),
		attribute.Float64("scenario.team_search.duration_total_ms", float64(t.teamSearchDuration)/float64(time.Millisecond)),
		attribute.Float64("scenario.team_search.duration_max_ms", float64(t.slowestTeamSearch.duration)/float64(time.Millisecond)),
		attribute.Int("scenario.team_search.slow_count", t.slowTeamSearches),
		attribute.Int("scenario.assignment_count.total", t.total.assignments),
		attribute.Int("scenario.certified_assignment_count.total", t.total.certifiedAssignments),
		attribute.Int("scenario.unresolved_assignment_count.total", t.total.unresolvedAssignments),
		attribute.Int("scenario.search_node_count.total", t.total.searchNodes),
		attribute.Int("scenario.search_node_count.max", t.maxSearchNodes),
		attribute.Int("scenario.oracle_call_count.total", t.total.oracleCalls),
		attribute.Int("scenario.oracle_cache_hit_count.total", t.total.oracleCacheHits),
		attribute.Int("scenario.visited_complete_count.total", t.total.visitedComplete),
	}
	if t.slowestTeamSearch.duration > 0 {
		attributes = append(attributes, attribute.String("scenario.team_search.slowest_team_id", t.slowestTeamSearch.teamID))
	}
	if t.maxSearchNodesTeamID != "" {
		attributes = append(attributes, attribute.String("scenario.search_node_count.max_team_id", t.maxSearchNodesTeamID))
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
		attributes = append(attributes, attribute.Int("scenario.result.state."+string(state)+"_count", stateCounts[state]))
	}
	return append(attributes, attribute.Int("scenario.result.budget_limited_count", budgetLimited))
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
		return telemetry.RecordWarningWithType(ctx, span, cause, "scenario.refresh", errorType)
	}
	span.SetAttributes(
		attribute.Int("scenario.input_team_count", len(teams)),
		attribute.Int("scenario.input_fixture_count", len(games)),
		attribute.Int("scenario.completed_fixture_count", scenarioCompletedFixtures(games)),
		attribute.Int("scenario.remaining_fixture_count", scenarioRemainingFixtures(games)),
		attribute.StringSlice("scenario.achievement_ids", scenarioAchievementIDs(r.Rules.Achievements)),
	)
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
	span.SetAttributes(
		attribute.String("scenario.slate_id", slate.ID),
		attribute.String("scenario.slate_state", string(slate.State)),
		attribute.String("scenario.slate_source", string(slate.Source)),
		attribute.Int("scenario.slate_fixture_count", len(slate.FixtureIDs)),
		attribute.String("scenario.slate_reason", slate.Reason),
	)
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
			telemetry.RecordCompletedWarningSpan(ctx, "scenario.generate_team", probeStarted, finished, searchAttributes, generateErr, "scenario.generate_team")
			return calculated{}, generateErr
		}
		if duration >= telemetry.SlowOperationThreshold {
			telemetry.RecordCompletedSpan(ctx, "scenario.generate_team", probeStarted, finished, append(searchAttributes, scenarioResultAttributes(teamResults)...), nil, "")
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
	return []attribute.KeyValue{
		attribute.String("scenario.team_id", teamID),
		attribute.StringSlice("scenario.achievement_ids", scenarioAchievementIDs(achievements)),
		attribute.Int("scenario.achievement_count", len(achievements)),
		attribute.Int("scenario.completed_fixture_count", scenarioCompletedFixtures(games)),
		attribute.Int("scenario.remaining_fixture_count", scenarioRemainingFixtures(games)),
		attribute.String("scenario.slate_state", string(slate.State)),
		attribute.String("scenario.slate_source", string(slate.Source)),
		attribute.Int("scenario.slate_fixture_count", len(slate.FixtureIDs)),
	}
}

func scenarioCalculationOutcome(recalculated bool, err error) string {
	if err != nil {
		return "failure"
	}
	if recalculated {
		return "recalculated"
	}
	return "current"
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
	return []attribute.KeyValue{
		attribute.Int("scenario.assignment_count", diagnostics.assignments),
		attribute.Int("scenario.certified_assignment_count", diagnostics.certifiedAssignments),
		attribute.Int("scenario.unresolved_assignment_count", diagnostics.unresolvedAssignments),
		attribute.Int("scenario.search_node_count", diagnostics.searchNodes),
		attribute.Int("scenario.oracle_call_count", diagnostics.oracleCalls),
		attribute.Int("scenario.oracle_cache_hit_count", diagnostics.oracleCacheHits),
		attribute.Int("scenario.visited_complete_count", diagnostics.visitedComplete),
	}
}

func (r Refresher) report(value Progress) {
	if r.Progress != nil {
		r.Progress(value)
	}
}
