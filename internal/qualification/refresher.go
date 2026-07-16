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

func (r Refresher) Refresh(ctx context.Context, syncRun cache.SyncRun, teams []cache.Team, games []cache.Game) error {
	if r.Store == nil {
		return fmt.Errorf("qualification store is required")
	}
	if err := r.Rules.Validate(); err != nil {
		return err
	}
	if syncRun.FixtureSnapshotID == "" {
		return fmt.Errorf("fixture snapshot ID is required")
	}
	if r.Budget <= 0 {
		r.Budget = 5 * time.Second
	}
	if snapshot, ok, err := r.Store.QualificationForSnapshot(ctx, syncRun.FixtureSnapshotID, r.Rules.Version); err != nil {
		return err
	} else if ok && !shouldRetryKickoffOrder(snapshot, games) && !shouldRetryComputeBudget(snapshot) {
		return nil
	}
	run := cache.QualificationRun{FixtureSnapshotID: syncRun.FixtureSnapshotID, SourceSyncRunID: syncRun.ID, Season: syncRun.Season, Stage: syncRun.Stage, RulesVersion: r.Rules.Version, StartedAt: time.Now().UTC(), ExpectedStatuses: r.Rules.ExpectedTeams * len(r.Rules.Achievements), WrittenStatuses: r.Rules.ExpectedTeams * len(r.Rules.Achievements)}
	values, err := r.calculate(ctx, teams, games)
	if err != nil {
		_ = r.Store.RecordQualificationFailure(context.Background(), run, err)
		return err
	}
	_, err = r.Store.ReplaceQualification(context.Background(), run, values)
	if err != nil {
		_ = r.Store.RecordQualificationFailure(context.Background(), run, err)
		return err
	}
	return nil
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
		if status.Method == clinching.ProofComputeBudget {
			return true
		}
	}
	return false
}
func (r Refresher) calculate(parent context.Context, teams []cache.Team, games []cache.Game) ([]cache.QualificationStatus, error) {
	batchStarted := time.Now()
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
			return nil, fmt.Errorf("fixture references missing team %q", id)
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
	ctx, cancel := context.WithTimeout(parent, r.Budget)
	defer cancel()
	evaluator, err := clinching.NewEvaluator(domainTeams, domainGames, order)
	if err != nil {
		return nil, err
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
				completed++
				r.report(Progress{Phase: "skipped", TeamID: row.Team.ID, Achievement: a, Completed: completed, Total: total, BatchElapsed: time.Since(batchStarted), Status: clinching.Unresolved, Method: clinching.ProofComputeBudget})
				continue
			}
			probeStarted := time.Now()
			r.report(Progress{Phase: "status_started", TeamID: row.Team.ID, Achievement: a, Completed: completed, Total: total, BatchElapsed: time.Since(batchStarted)})
			value, err := evaluator.EvaluateStatus(ctx, row.Team.ID, a, nil)
			if err != nil {
				return nil, err
			}
			results[row.Team.ID][a.ID] = toCache(value)
			completed++
			r.report(Progress{Phase: "status_finished", TeamID: row.Team.ID, Achievement: a, Completed: completed, Total: total, Elapsed: time.Since(probeStarted), BatchElapsed: time.Since(batchStarted), Status: value.Status, Method: value.Method})
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
		for _, a := range achievements {
			value := results[row.Team.ID][a.ID]
			if value.Status != clinching.NotClinched {
				continue
			}
			if ctx.Err() != nil {
				value.NoHelp = clinching.NoHelpPath{State: clinching.NoHelpUnresolved, FixtureIDs: []string{}, Reason: "calculation budget exhausted"}
				results[row.Team.ID][a.ID] = value
				noHelpCompleted++
				r.report(Progress{Phase: "no_help_skipped", TeamID: row.Team.ID, Achievement: a, Completed: noHelpCompleted, Total: noHelpTotal, BatchElapsed: time.Since(batchStarted), Status: value.Status, Method: value.Method, NoHelpState: value.NoHelp.State})
				continue
			}
			probeStarted := time.Now()
			r.report(Progress{Phase: "no_help_started", TeamID: row.Team.ID, Achievement: a, Completed: noHelpCompleted, Total: noHelpTotal, BatchElapsed: time.Since(batchStarted), Status: value.Status, Method: value.Method})
			base := clinching.AchievementResult{TeamID: value.TeamID, Achievement: value.Achievement, TopK: value.TopK, Status: value.Status, Method: value.Method, Reason: value.Reason}
			path, err := evaluator.EvaluateNoHelp(ctx, row.Team.ID, a, nil, base)
			if err != nil {
				return nil, err
			}
			value.NoHelp = path
			results[row.Team.ID][a.ID] = value
			noHelpCompleted++
			r.report(Progress{Phase: "no_help_finished", TeamID: row.Team.ID, Achievement: a, Completed: noHelpCompleted, Total: noHelpTotal, Elapsed: time.Since(probeStarted), BatchElapsed: time.Since(batchStarted), Status: value.Status, Method: value.Method, NoHelpState: path.State})
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
	for _, g := range games {
		n[g.HomeTeamID]++
		n[g.AwayTeamID]++
	}
	for _, t := range teams {
		if n[t.ID] != r.GamesPerTeam {
			return false
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
