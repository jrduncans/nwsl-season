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
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type Store interface {
	QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
	ReplaceQualification(context.Context, cache.QualificationRun, []cache.QualificationStatus) (cache.QualificationSnapshot, error)
	RecordQualificationFailure(context.Context, cache.QualificationRun, error) error
}
type Refresher struct {
	Store  Store
	Rules  competition.Rules
	Budget time.Duration
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
	if _, ok, err := r.Store.QualificationForSnapshot(ctx, syncRun.FixtureSnapshotID, r.Rules.Version); err != nil {
		return err
	} else if ok {
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
func (r Refresher) calculate(parent context.Context, teams []cache.Team, games []cache.Game) ([]cache.QualificationStatus, error) {
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
	table := standings.Calculate(domainTeams, domainGames, standings.OfficialTotalRules())
	achievements := append([]competition.Achievement(nil), r.Rules.Achievements...)
	sort.Slice(achievements, func(i, j int) bool { return achievements[i].TopK > achievements[j].TopK })
	results := map[string]map[competition.AchievementID]cache.QualificationStatus{}
	for _, row := range table {
		results[row.Team.ID] = map[competition.AchievementID]cache.QualificationStatus{}
		for _, a := range achievements {
			if ctx.Err() != nil {
				results[row.Team.ID][a.ID] = unresolved(row.Team.ID, a, clinching.ProofComputeBudget, "calculation budget exhausted")
				continue
			}
			value, err := clinching.Evaluate(ctx, clinching.Request{Teams: domainTeams, Games: domainGames, FixtureOrder: order, TargetTeamID: row.Team.ID, Achievement: a})
			if err != nil {
				return nil, err
			}
			results[row.Team.ID][a.ID] = toCache(value)
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
func safeFixtureStates(games []cache.Game) bool {
	for _, g := range games {
		switch g.Status {
		case "FullTime":
			if !g.HomeScore.Valid || !g.AwayScore.Valid {
				return false
			}
		case "PreMatch":
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
		if g.Status == "PreMatch" {
			if _, err := time.Parse(time.RFC3339, g.KickoffUTC); err != nil {
				return nil, err
			}
			pending = append(pending, g)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339, pending[i].KickoffUTC)
		b, _ := time.Parse(time.RFC3339, pending[j].KickoffUTC)
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
