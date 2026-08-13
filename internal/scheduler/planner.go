package scheduler

import (
	"hash/fnv"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

type JobKind string

type JobClass string

const (
	JobHot  JobClass = "hot"
	JobCold JobClass = "cold"
)

const (
	JobFullGames    JobKind = "full_games"
	JobFullXG       JobKind = "full_xg"
	JobCheckedGames JobKind = "checked_games"
	JobCheckedXG    JobKind = "checked_xg"
)

type Job struct {
	Kind      JobKind
	Class     JobClass
	Operation syncer.Operation
	Reason    string
	Selection selectionMetadata
}

type selectionMetadata struct {
	Policy                  string
	PollInterval            time.Duration
	WatchWindow             time.Duration
	CandidateCount          int
	EligibleCount           int
	ExpiredCount            int
	InvalidKickoffCount     int
	MissingCandidateCount   int
	MissingEligibleCount    int
	AvailableCandidateCount int
	AvailableEligibleCount  int
	MissingPollInterval     time.Duration
	MissingWatchWindow      time.Duration
	AvailablePollInterval   time.Duration
	AvailableWatchWindow    time.Duration
	OldestKickoff           time.Time
	NewestKickoff           time.Time
}

const (
	defaultSourceRequestBudget      = 3
	defaultResultCorrectionInterval = 6 * time.Hour
	defaultMissingXGInterval        = 5 * time.Minute
	defaultXGCorrectionInterval     = 6 * time.Hour
	defaultGameCorrectionWindow     = 3 * 24 * time.Hour
	defaultMissingXGWindow          = 5 * 24 * time.Hour
	defaultXGCorrectionWindow       = 5 * 24 * time.Hour
	defaultInventoryInterval        = 7 * 24 * time.Hour
	defaultColdSweepInterval        = 30 * 24 * time.Hour
)

// Plan is pure: it selects ordered, batched jobs without making requests or
// changing a due pointer. The executor derives absolute post-observation due
// timestamps from each request's delay policy.
func Plan(snapshot cache.PlanningSnapshot, config Config, now time.Time) []Job {
	now = now.UTC()
	scopes := planningScopes(snapshot, config)
	league, playoffs := splitStageScopes(scopes)
	priorities := append(hotPriorities(league, config, now), hotPriorities(playoffs, config, now)...)
	jobs := []Job{}
	for _, priority := range priorities {
		jobs = append(jobs, priority...)
	}
	if len(jobs) > 0 {
		for i := range jobs {
			jobs[i].Class = JobHot
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
	return planColdSweep(snapshot, config, now)
}

func splitStageScopes(scopes []cache.PlanningScopeSnapshot) (league, playoffs []cache.PlanningScopeSnapshot) {
	for _, scope := range scopes {
		entry, ok := competition.Lookup(scope.Readiness.Scope.Season, scope.Readiness.Scope.Stage)
		if ok && entry.Kind == competition.StageKindKnockout {
			playoffs = append(playoffs, scope)
			continue
		}
		league = append(league, scope)
	}
	return league, playoffs
}

func hotPriorities(scopes []cache.PlanningScopeSnapshot, config Config, now time.Time) [][]Job {
	initialXG := planInitialXG(scopes)
	return [][]Job{planMissingInventory(scopes, config, now), planCheckedGames(scopes, config, now), initialXG, planCheckedXG(scopes, config, now, initialXG), planWeeklyInventory(scopes, config, now)}
}

type coldCandidate struct {
	job Job
	due time.Time
}

func planColdSweep(snapshot cache.PlanningSnapshot, config Config, now time.Time) []Job {
	candidates := make([]coldCandidate, 0)
	for _, scope := range snapshot.Scopes {
		id := scope.Readiness.Scope
		if id.Lifecycle != cache.SourceScopeCompleted || scope.Readiness.Readiness != cache.SourceReadinessAvailable || len(scope.Games) == 0 || scope.GamesFull == nil || scope.GamesFull.LastFullSuccessAt == nil {
			continue
		}
		gamesDue, gamesAt := coldDue(scope.GamesFull, id.Season, id.Stage, config, now)
		if gamesDue {
			candidates = append(candidates, coldCandidate{due: gamesAt, job: Job{Kind: JobFullGames, Class: JobCold, Reason: "archived_correction_sweep", Operation: syncer.Operation{Resource: syncer.OperationGames, Mode: syncer.OperationFull, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler, NextFullDueAfter: coldSweepInterval(config)}}})
			continue
		}
		if scope.XGFull == nil || scope.XGFull.LastFullSuccessAt == nil || scope.XGFull.LastFullSuccessAt.Before(*scope.GamesFull.LastFullSuccessAt) {
			candidates = append(candidates, coldCandidate{due: *scope.GamesFull.LastFullSuccessAt, job: Job{Kind: JobFullXG, Class: JobCold, Reason: "archived_xg_after_games", Operation: syncer.Operation{Resource: syncer.OperationGameXG, Mode: syncer.OperationFull, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler, NextFullDueAfter: coldSweepInterval(config)}}})
			continue
		}
		xgDue, xgAt := coldDue(scope.XGFull, id.Season, id.Stage, config, now)
		if xgDue {
			candidates = append(candidates, coldCandidate{due: xgAt, job: Job{Kind: JobFullGames, Class: JobCold, Reason: "archived_correction_sweep", Operation: syncer.Operation{Resource: syncer.OperationGames, Mode: syncer.OperationFull, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler, NextFullDueAfter: coldSweepInterval(config)}}})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].due.Equal(candidates[j].due) {
			return candidates[i].due.Before(candidates[j].due)
		}
		a, b := candidates[i].job.Operation, candidates[j].job.Operation
		if a.Season != b.Season {
			return a.Season > b.Season
		}
		return a.Stage < b.Stage
	})
	return []Job{candidates[0].job}
}

func coldDue(state *cache.SourceResourceScopeState, season, stage string, config Config, now time.Time) (bool, time.Time) {
	if state == nil || state.LastFullSuccessAt == nil {
		return false, time.Time{}
	}
	due := state.NextFullDueAt
	if due == nil {
		value := state.LastFullSuccessAt.Add(coldSweepOffset(season, stage, coldSweepInterval(config)))
		due = &value
	}
	return !due.After(now), *due
}

func coldSweepOffset(season, stage string, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(season))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(stage))
	// #nosec G115 -- modulo a positive Duration is strictly below MaxInt64.
	return time.Duration(hash.Sum64() % uint64(interval))
}

func planningScopes(snapshot cache.PlanningSnapshot, config Config) []cache.PlanningScopeSnapshot {
	out := []cache.PlanningScopeSnapshot{}
	for _, scope := range snapshot.Scopes {
		id := scope.Readiness.Scope
		if id.Lifecycle == cache.SourceScopeCompleted {
			continue
		}
		entry, cataloged := competition.Lookup(id.Season, id.Stage)
		currentCatalogStage := id.Season == config.Season && id.Lifecycle == cache.SourceScopeActive && cataloged && entry.SourceAvailable
		if (id.Season == config.Season && id.Stage == config.Stage) || currentCatalogStage || id.Lifecycle == cache.SourceScopeUpcoming && id.Stage == "Regular Season" {
			out = append(out, scope)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Readiness.Scope, out[j].Readiness.Scope
		if a.Season != b.Season {
			return a.Season > b.Season
		}
		if a.Season == config.Season {
			aEntry, aOK := competition.Lookup(a.Season, a.Stage)
			bEntry, bOK := competition.Lookup(b.Season, b.Stage)
			if aOK && bOK && aEntry.Kind != bEntry.Kind {
				return aEntry.Kind == competition.StageKindLeagueTable
			}
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
		selection := selectionMetadata{Policy: "kickoff_window", PollInterval: resultCorrectionInterval(config), WatchWindow: gameCorrectionWindow(config)}
		for _, game := range scope.Games {
			check, found := checks[game.ASAID]
			if terminalGame(game) {
				selection.CandidateCount++
				due, reason, kickoff := terminalPollDecision(game, check, found, selection.WatchWindow, selection.PollInterval, now)
				if !kickoff.IsZero() {
					selection.OldestKickoff, selection.NewestKickoff = updateKickoffBounds(selection.OldestKickoff, selection.NewestKickoff, kickoff)
				}
				switch reason {
				case "window_expired":
					selection.ExpiredCount++
				case "invalid_kickoff":
					selection.InvalidKickoffCount++
				}
				if !due {
					continue
				}
			} else if !gameResultDue(game, check, found, config, now) {
				continue
			}
			selection.EligibleCount++
			normal, material := resultCadence(game, config)
			requests = append(requests, syncer.OperationGameRequest{GameID: game.ASAID, NextDueAfter: normal, MaterialNextDueAfter: material})
		}
		if len(requests) == 0 {
			continue
		}
		sort.Slice(requests, func(i, j int) bool { return requests[i].GameID < requests[j].GameID })
		id := scope.Readiness.Scope
		jobs = append(jobs, Job{Kind: JobCheckedGames, Reason: "kickoff_window_result_poll", Selection: selection, Operation: syncer.Operation{Resource: syncer.OperationGames, Mode: syncer.OperationTargeted, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler, Requested: requests}})
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
		values := map[string]cache.GameXG{}
		for _, value := range scope.XG {
			values[value.GameID] = value
		}
		requests := []syncer.OperationGameRequest{}
		selection := selectionMetadata{
			Policy:                "kickoff_window",
			MissingPollInterval:   missingXGInterval(config),
			MissingWatchWindow:    missingXGWindow(config),
			AvailablePollInterval: xGCorrectionInterval(config),
			AvailableWatchWindow:  xGCorrectionWindow(config),
		}
		for _, game := range scope.Games {
			if game.Status != fixtures.CompletedStatus || !game.HomeScore.Valid || !game.AwayScore.Valid {
				continue
			}
			value, available := values[game.ASAID]
			isAvailable := available && value.Availability == cache.XGAvailable
			if isAvailable {
				selection.AvailableCandidateCount++
			} else {
				selection.MissingCandidateCount++
			}
			selection.CandidateCount++
			kickoff, kickoffErr := fixtures.ParseKickoff(game.KickoffUTC)
			if kickoffErr != nil {
				selection.InvalidKickoffCount++
				continue
			}
			selection.OldestKickoff, selection.NewestKickoff = updateKickoffBounds(selection.OldestKickoff, selection.NewestKickoff, kickoff)
			watchWindow := missingXGWindow(config)
			pollInterval := missingXGInterval(config)
			if isAvailable {
				watchWindow = xGCorrectionWindow(config)
				pollInterval = xGCorrectionInterval(config)
			}
			if !now.Before(kickoff.Add(watchWindow)) {
				selection.ExpiredCount++
				continue
			}
			check, found := checks[game.ASAID]
			if !xgPollDue(check, found, pollInterval, now) {
				continue
			}
			if isAvailable {
				selection.AvailableEligibleCount++
			} else {
				selection.MissingEligibleCount++
			}
			selection.EligibleCount++
			normal, material := xgCadence(pollInterval)
			requests = append(requests, syncer.OperationGameRequest{GameID: game.ASAID, NextDueAfter: normal, MaterialNextDueAfter: material})
		}
		if len(requests) == 0 {
			continue
		}
		sort.Slice(requests, func(i, j int) bool { return requests[i].GameID < requests[j].GameID })
		id := scope.Readiness.Scope
		jobs = append(jobs, Job{Kind: JobCheckedXG, Reason: "kickoff_window_xg_poll", Selection: selection, Operation: syncer.Operation{Resource: syncer.OperationGameXG, Mode: syncer.OperationTargeted, Season: id.Season, Stage: id.Stage, Trigger: cache.SourceTriggerScheduler, Requested: requests}})
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
	if terminalGame(game) {
		due, _, _ := terminalPollDecision(game, state, found, gameCorrectionWindow(config), resultCorrectionInterval(config), now)
		return due
	}
	return !found || state.NextDueAt == nil || !state.NextDueAt.After(now)
}

func terminalGame(game cache.Game) bool {
	return game.Status == fixtures.AbandonedStatus || (game.Status == fixtures.CompletedStatus && game.HomeScore.Valid && game.AwayScore.Valid)
}

func terminalPollDecision(game cache.Game, state cache.GameResultCheckState, found bool, window, interval time.Duration, now time.Time) (bool, string, time.Time) {
	kickoff, err := fixtures.ParseKickoff(game.KickoffUTC)
	if err != nil {
		return false, "invalid_kickoff", time.Time{}
	}
	if !now.Before(kickoff.Add(window)) {
		return false, "window_expired", kickoff
	}
	if !found || state.LastCheckedAt.IsZero() {
		return true, "due", kickoff
	}
	if now.Before(state.LastCheckedAt.Add(interval)) {
		return false, "not_due", kickoff
	}
	return true, "due", kickoff
}

func xgPollDue(state cache.GameXGCheckState, found bool, interval time.Duration, now time.Time) bool {
	if !found || state.LastCheckedAt.IsZero() {
		return true
	}
	return !now.Before(state.LastCheckedAt.Add(interval))
}

func updateKickoffBounds(oldest, newest, kickoff time.Time) (time.Time, time.Time) {
	if oldest.IsZero() || kickoff.Before(oldest) {
		oldest = kickoff
	}
	if newest.IsZero() || kickoff.After(newest) {
		newest = kickoff
	}
	return oldest, newest
}

func resultCadence(game cache.Game, config Config) (time.Duration, time.Duration) {
	if !terminalGame(game) {
		return config.CheckInterval, resultCorrectionInterval(config)
	}
	return resultCorrectionInterval(config), resultCorrectionInterval(config)
}
func xgCadence(interval time.Duration) (time.Duration, time.Duration) {
	return interval, interval
}
func resultCorrectionInterval(config Config) time.Duration {
	if config.ResultCorrectionInterval > 0 {
		return config.ResultCorrectionInterval
	}
	return defaultResultCorrectionInterval
}
func missingXGInterval(config Config) time.Duration {
	if config.MissingXGInterval > 0 {
		return config.MissingXGInterval
	}
	return defaultMissingXGInterval
}
func xGCorrectionInterval(config Config) time.Duration {
	if config.XGCorrectionInterval > 0 {
		return config.XGCorrectionInterval
	}
	return defaultXGCorrectionInterval
}
func gameCorrectionWindow(config Config) time.Duration {
	if config.GameCorrectionWindow > 0 {
		return config.GameCorrectionWindow
	}
	return defaultGameCorrectionWindow
}
func xGCorrectionWindow(config Config) time.Duration {
	if config.XGCorrectionWindow > 0 {
		return config.XGCorrectionWindow
	}
	return defaultXGCorrectionWindow
}
func missingXGWindow(config Config) time.Duration {
	if config.MissingXGWindow > 0 {
		return config.MissingXGWindow
	}
	return defaultMissingXGWindow
}
func inventoryInterval(config Config) time.Duration {
	if config.InventoryInterval > 0 {
		return config.InventoryInterval
	}
	return defaultInventoryInterval
}
func coldSweepInterval(config Config) time.Duration {
	if config.ColdSweepInterval > 0 {
		return config.ColdSweepInterval
	}
	return defaultColdSweepInterval
}
