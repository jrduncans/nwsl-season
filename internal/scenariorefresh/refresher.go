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

func (r Refresher) Refresh(ctx context.Context, sync cache.SyncRun, teams []cache.Team, games []cache.Game) error {
	if r.Store == nil {
		return fmt.Errorf("scenario store is required")
	}
	if err := r.Rules.Validate(); err != nil {
		return err
	}
	if sync.FixtureSnapshotID == "" {
		return fmt.Errorf("fixture snapshot ID is required")
	}
	if snapshot, ok, err := r.Store.ScenarioForSnapshot(ctx, sync.FixtureSnapshotID, r.Rules.Version, scenarios.DefinitionVersion); err != nil {
		return err
	} else if ok && !shouldRetryComputeBudget(snapshot) {
		return nil
	}
	q, ok, err := r.Store.QualificationForSnapshot(ctx, sync.FixtureSnapshotID, r.Rules.Version)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("matching qualification batch is required")
	}
	run := cache.ScenarioRun{FixtureSnapshotID: sync.FixtureSnapshotID, QualificationRunID: q.Run.ID, SourceSyncRunID: sync.ID, Season: sync.Season, Stage: sync.Stage, RulesVersion: r.Rules.Version, DefinitionVersion: scenarios.DefinitionVersion, StartedAt: time.Now().UTC(), ExpectedResults: r.Rules.ExpectedTeams * len(r.Rules.Achievements), WrittenResults: r.Rules.ExpectedTeams * len(r.Rules.Achievements)}
	values, err := r.calculate(ctx, teams, games, q)
	if err != nil {
		_ = r.Store.RecordScenarioFailure(context.Background(), run, err)
		return err
	}
	run.Slate = values.slate
	_, err = r.Store.ReplaceScenario(context.Background(), run, values.rows)
	if err != nil {
		_ = r.Store.RecordScenarioFailure(context.Background(), run, err)
	}
	return err
}

// A timeout is a transient computational limitation, not a mathematical
// result. Keep normal completed batches immutable, but retry a batch that was
// only incomplete because its shared calculation budget expired. This lets a
// later sync pick up an optimizer improvement or a larger configured budget.
func shouldRetryComputeBudget(snapshot cache.ScenarioSnapshot) bool {
	for _, result := range snapshot.Results {
		if result.State == scenarios.OpportunityUnresolved && result.Limitation == "scenario computation budget exhausted" {
			return true
		}
	}
	return false
}

type calculated struct {
	slate scenarios.Slate
	rows  []cache.ScenarioResult
}

func (r Refresher) calculate(parent context.Context, teams []cache.Team, games []cache.Game, q cache.QualificationSnapshot) (calculated, error) {
	batchStarted := time.Now()
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
			return calculated{}, fmt.Errorf("fixture references missing team %q", id)
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
			return calculated{}, err
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
		return calculated{}, err
	}
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
		return calculated{}, err
	}
	baseline := map[string]cache.QualificationStatus{}
	for _, v := range q.Statuses {
		baseline[v.TeamID+"\x00"+string(v.Achievement)] = v
	}
	budget := r.Budget
	if budget <= 0 {
		budget = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	rows := []cache.ScenarioResult{}
	completed := 0
	table := standings.Calculate(domainTeams, domainGames, standings.OfficialTotalRules())
	ach := append([]competition.Achievement(nil), r.Rules.Achievements...)
	sort.Slice(ach, func(i, j int) bool { return ach[i].TopK > ach[j].TopK })
	for _, t := range table {
		for _, a := range ach {
			b, ok := baseline[t.Team.ID+"\x00"+string(a.ID)]
			if !ok {
				return calculated{}, fmt.Errorf("qualification baseline missing for team %q achievement %q", t.Team.ID, a.ID)
			}
			base := clinching.AchievementResult{TeamID: b.TeamID, Achievement: b.Achievement, TopK: b.TopK, Status: b.Status, Method: b.Method, Reason: b.Reason, NoHelp: b.NoHelp}
			probeStarted := time.Now()
			r.report(Progress{Phase: "started", TeamID: t.Team.ID, Achievement: a, Completed: completed, Total: len(table) * len(ach), BatchElapsed: time.Since(batchStarted)})
			v, err := scenarios.Generate(ctx, scenarios.Request{Evaluator: evaluator, Teams: domainTeams, Games: domainGames, Slate: slate, TargetTeamID: t.Team.ID, Achievement: a, Baseline: base})
			if err != nil {
				return calculated{}, err
			}
			rows = append(rows, cache.ScenarioResult{Result: v})
			completed++
			r.report(Progress{Phase: "finished", TeamID: t.Team.ID, Achievement: a, Completed: completed, Total: len(table) * len(ach), Elapsed: time.Since(probeStarted), BatchElapsed: time.Since(batchStarted), State: v.State})
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

func (r Refresher) report(value Progress) {
	if r.Progress != nil {
		r.Progress(value)
	}
}
