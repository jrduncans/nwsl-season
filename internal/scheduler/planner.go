package scheduler

import (
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

type JobKind string

const (
	JobFullGames    JobKind = "full_games"
	JobFullXG       JobKind = "full_xg"
	JobCheckedGames JobKind = "checked_games"
	JobCheckedXG    JobKind = "checked_xg"
)

type Job struct {
	Kind      JobKind
	Operation syncer.Operation
	Reason    string
}

const (
	defaultSourceRequestBudget   = 3
	defaultCorrectionInterval    = 6 * time.Hour
	defaultCorrectionDaily       = 24 * time.Hour
	defaultCorrectionFastWindow  = 5 * 24 * time.Hour
	defaultCorrectionFinalWindow = 30 * 24 * time.Hour
	defaultInventoryInterval     = 7 * 24 * time.Hour
)

// Plan is pure: it selects ordered, batched jobs without making requests or
// changing a due pointer. The executor derives absolute post-observation due
// timestamps from each request's delay policy.
func Plan(snapshot cache.PlanningSnapshot, config Config, now time.Time) []Job {
	now = now.UTC()
	scopes := planningScopes(snapshot, config)
	initialXG := planInitialXG(scopes)
	priorities := [][]Job{
		planMissingInventory(scopes, config, now),
		planCheckedGames(scopes, config, now),
		initialXG,
		planCheckedXG(scopes, config, now, initialXG),
		planWeeklyInventory(scopes, config, now),
	}
	jobs := []Job{}
	for _, priority := range priorities {
		jobs = append(jobs, priority...)
	}
	budget := config.SourceRequestBudget
	if budget <= 0 {
		budget = defaultSourceRequestBudget
	}
	if len(jobs) > budget {
		jobs = jobs[:budget]
	}
	return jobs
}

func planningScopes(snapshot cache.PlanningSnapshot, config Config) []cache.PlanningScopeSnapshot {
	out := []cache.PlanningScopeSnapshot{}
	for _, scope := range snapshot.Scopes {
		id := scope.Readiness.Scope
		if id.Season == config.Season && id.Stage == config.Stage || id.Lifecycle == cache.SourceScopeUpcoming && id.Stage == "Regular Season" {
			out = append(out, scope)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Readiness.Scope, out[j].Readiness.Scope
		if a.Season != b.Season {
			return a.Season > b.Season
		}
		return a.Stage < b.Stage
	})
	return out
}

func planMissingInventory(scopes []cache.PlanningScopeSnapshot, config Config, now time.Time) []Job {
	jobs := []Job{}
	for _, scope := range scopes {
		if scope.Readiness.Readiness == cache.SourceReadinessAvailable || !fullDue(scope.GamesFull, now) {
			continue
		}
		jobs = append(jobs, fullGamesJob(scope, config, "missing_or_not_published_inventory"))
	}
	return jobs
}

func planWeeklyInventory(scopes []cache.PlanningScopeSnapshot, config Config, now time.Time) []Job {
	jobs := []Job{}
	for _, scope := range scopes {
		if scope.Readiness.Readiness != cache.SourceReadinessAvailable || scope.Readiness.Scope.Lifecycle == cache.SourceScopeCompleted || !fullDue(scope.GamesFull, now) {
			continue
		}
		jobs = append(jobs, fullGamesJob(scope, config, "weekly_inventory_audit"))
	}
	return jobs
}

func planInitialXG(scopes []cache.PlanningScopeSnapshot) []Job {
	jobs := []Job{}
	for _, scope := range scopes {
		if scope.Readiness.Readiness != cache.SourceReadinessAvailable || scope.XGFull != nil {
			continue
		}
		id := scope.Readiness.Scope
		jobs = append(jobs, Job{Kind: JobFullXG, Reason: "initial_authoritative_xg", Operation: syncer.Operation{Resource: syncer.OperationGameXG, Mode: syncer.OperationFull, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler}})
	}
	return jobs
}

func fullGamesJob(scope cache.PlanningScopeSnapshot, config Config, reason string) Job {
	id := scope.Readiness.Scope
	var expected = scope.Readiness.ExpectedInventory
	if expected == nil && id.Season == config.Season && id.Stage == config.Stage && (config.ExpectedTeams != 0 || config.GamesPerTeam != 0) {
		expected = &competition.InventoryExpectation{Teams: config.ExpectedTeams, GamesPerTeam: config.GamesPerTeam, Games: expectedFixtureCount(config.ExpectedTeams, config.GamesPerTeam)}
	}
	return Job{Kind: JobFullGames, Reason: reason, Operation: syncer.Operation{Resource: syncer.OperationGames, Mode: syncer.OperationFull, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler, NextFullDueAfter: inventoryInterval(config), Expectation: expected}}
}

func planCheckedGames(scopes []cache.PlanningScopeSnapshot, config Config, now time.Time) []Job {
	jobs := []Job{}
	for _, scope := range scopes {
		checks := map[string]cache.GameResultCheckState{}
		for _, check := range scope.ResultChecks {
			checks[check.GameID] = check
		}
		requests := []syncer.OperationGameRequest{}
		for _, game := range scope.Games {
			check, found := checks[game.ASAID]
			if !gameResultDue(game, check, found, config, now) {
				continue
			}
			normal, material := resultCadence(game, check, found, config, now)
			requests = append(requests, syncer.OperationGameRequest{GameID: game.ASAID, NextDueAfter: normal, MaterialNextDueAfter: material})
		}
		if len(requests) == 0 {
			continue
		}
		sort.Slice(requests, func(i, j int) bool { return requests[i].GameID < requests[j].GameID })
		id := scope.Readiness.Scope
		jobs = append(jobs, Job{Kind: JobCheckedGames, Reason: "due_result_checks", Operation: syncer.Operation{Resource: syncer.OperationGames, Mode: syncer.OperationTargeted, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler, Requested: requests}})
	}
	return jobs
}

func planCheckedXG(scopes []cache.PlanningScopeSnapshot, config Config, now time.Time, initial []Job) []Job {
	jobs := []Job{}
	bootstrapping := map[string]bool{}
	for _, job := range initial {
		bootstrapping[job.Operation.Season+"\x00"+job.Operation.Stage] = true
	}
	for _, scope := range scopes {
		if bootstrapping[scope.Readiness.Scope.Season+"\x00"+scope.Readiness.Scope.Stage] {
			continue
		}
		checks := map[string]cache.GameXGCheckState{}
		for _, check := range scope.XGChecks {
			checks[check.GameID] = check
		}
		resultChecks := map[string]cache.GameResultCheckState{}
		for _, check := range scope.ResultChecks {
			resultChecks[check.GameID] = check
		}
		values := map[string]cache.GameXG{}
		for _, value := range scope.XG {
			values[value.GameID] = value
		}
		requests := []syncer.OperationGameRequest{}
		for _, game := range scope.Games {
			if game.Status != fixtures.CompletedStatus || !game.HomeScore.Valid || !game.AwayScore.Valid {
				continue
			}
			check, found := checks[game.ASAID]
			resultCheck, terminalKnown := resultChecks[game.ASAID]
			value, available := values[game.ASAID]
			if !xgDue(value, available, check, found, resultCheck, terminalKnown, config, now) {
				continue
			}
			normal, material := xgCadence(value, available, check, found, resultCheck, terminalKnown, config, now)
			requests = append(requests, syncer.OperationGameRequest{GameID: game.ASAID, NextDueAfter: normal, MaterialNextDueAfter: material})
		}
		if len(requests) == 0 {
			continue
		}
		sort.Slice(requests, func(i, j int) bool { return requests[i].GameID < requests[j].GameID })
		id := scope.Readiness.Scope
		jobs = append(jobs, Job{Kind: JobCheckedXG, Reason: "due_xg_checks", Operation: syncer.Operation{Resource: syncer.OperationGameXG, Mode: syncer.OperationTargeted, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler, Requested: requests}})
	}
	return jobs
}

func fullDue(state *cache.SourceResourceScopeState, now time.Time) bool {
	return state == nil || state.NextFullDueAt == nil || !state.NextFullDueAt.After(now)
}
func gameResultDue(game cache.Game, state cache.GameResultCheckState, found bool, config Config, now time.Time) bool {
	if game.Status == fixtures.PreMatchStatus {
		kickoff, err := fixtures.ParseKickoff(game.KickoffUTC)
		if err != nil || now.Before(kickoff.Add(config.CompletionGrace)) {
			return false
		}
	}
	if game.Status == fixtures.AbandonedStatus || (game.Status == fixtures.CompletedStatus && game.HomeScore.Valid && game.AwayScore.Valid) {
		return !found || state.FirstTerminalObservedAt == nil || (correctionActive(state.FirstTerminalObservedAt, state.LastMaterialChangeAt, config, now) && (state.NextDueAt == nil || !state.NextDueAt.After(now)))
	}
	return !found || state.NextDueAt == nil || !state.NextDueAt.After(now)
}
func resultCadence(game cache.Game, state cache.GameResultCheckState, found bool, config Config, now time.Time) (time.Duration, time.Duration) {
	if game.Status != fixtures.AbandonedStatus && (game.Status != fixtures.CompletedStatus || !game.HomeScore.Valid || !game.AwayScore.Valid) {
		return config.CheckInterval, correctionInterval(config)
	}
	return correctionCadence(state.FirstTerminalObservedAt, state.LastMaterialChangeAt, config, now)
}
func xgDue(value cache.GameXG, exists bool, state cache.GameXGCheckState, found bool, terminal cache.GameResultCheckState, terminalKnown bool, config Config, now time.Time) bool {
	if !terminalKnown || terminal.FirstTerminalObservedAt == nil {
		return false
	}
	if !correctionActive(terminal.FirstTerminalObservedAt, state.LastMaterialChangeAt, config, now) {
		return false
	}
	return !found || state.NextDueAt == nil || !state.NextDueAt.After(now)
}
func xgCadence(value cache.GameXG, exists bool, state cache.GameXGCheckState, found bool, terminal cache.GameResultCheckState, terminalKnown bool, config Config, now time.Time) (time.Duration, time.Duration) {
	if !exists || value.Availability != cache.XGAvailable {
		return missingXGCadence(terminal.FirstTerminalObservedAt, now, config.CheckInterval, correctionInterval(config), correctionFastWindow(config), correctionDaily(config))
	}
	return correctionCadence(state.FirstAvailableObservedAt, state.LastMaterialChangeAt, config, now)
}
func correctionCadence(first, material *time.Time, config Config, now time.Time) (time.Duration, time.Duration) {
	if !correctionActive(first, material, config, now) {
		return 0, 0
	}
	anchor := first
	if material != nil && (anchor == nil || material.After(*anchor)) {
		anchor = material
	}
	if anchor == nil || now.Sub(*anchor) < correctionFastWindow(config) {
		return correctionInterval(config), correctionInterval(config)
	}
	return correctionDaily(config), correctionInterval(config)
}
func correctionActive(first, material *time.Time, config Config, now time.Time) bool {
	if first == nil {
		return true
	}
	anchor := first
	if material != nil && material.After(*anchor) {
		anchor = material
	}
	if now.Sub(*anchor) < correctionFastWindow(config) {
		return true
	}
	return now.Sub(*first) <= correctionFinalWindow(config)
}
func missingXGCadence(first *time.Time, now time.Time, interval, material, fast, daily time.Duration) (time.Duration, time.Duration) {
	if first == nil || now.Sub(*first) < fast {
		return interval, material
	}
	return daily, material
}
func correctionInterval(config Config) time.Duration {
	if config.CorrectionInterval > 0 {
		return config.CorrectionInterval
	}
	return defaultCorrectionInterval
}
func correctionDaily(config Config) time.Duration {
	if config.CorrectionDaily > 0 {
		return config.CorrectionDaily
	}
	return defaultCorrectionDaily
}
func correctionFastWindow(config Config) time.Duration {
	if config.CorrectionFastWindow > 0 {
		return config.CorrectionFastWindow
	}
	return defaultCorrectionFastWindow
}
func correctionFinalWindow(config Config) time.Duration {
	if config.CorrectionFinalWindow > 0 {
		return config.CorrectionFinalWindow
	}
	return defaultCorrectionFinalWindow
}
func inventoryInterval(config Config) time.Duration {
	if config.InventoryInterval > 0 {
		return config.InventoryInterval
	}
	return defaultInventoryInterval
}
