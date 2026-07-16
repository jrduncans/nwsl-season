package clinching

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// Evaluator reuses one validated fixture snapshot for sequential qualification
// probes. It is intentionally not safe for concurrent use.
type Evaluator struct {
	teams        []standings.Team
	games        []standings.Game
	fixtureOrder []string
	teamIDs      map[string]bool
	cache        map[string]AchievementResult
}

// NewEvaluator validates and defensively copies a fixture snapshot.
func NewEvaluator(teams []standings.Team, games []standings.Game, fixtureOrder []string) (*Evaluator, error) {
	if len(teams) == 0 {
		return nil, fmt.Errorf("at least one team is required")
	}
	ids := make(map[string]bool, len(teams))
	for _, team := range teams {
		if team.ID == "" || ids[team.ID] {
			return nil, fmt.Errorf("duplicate or empty team ID %q", team.ID)
		}
		ids[team.ID] = true
	}
	// validateRequest owns the complete snapshot validation. A harmless
	// one-team request lets construction retain that single authority.
	probe := Request{Teams: append([]standings.Team(nil), teams...), Games: append([]standings.Game(nil), games...), FixtureOrder: append([]string(nil), fixtureOrder...), TargetTeamID: teams[0].ID, Achievement: competition.Achievement{ID: "snapshot", TopK: 1}}
	if err := validateRequest(probe); err != nil {
		return nil, err
	}
	return &Evaluator{teams: probe.Teams, games: probe.Games, fixtureOrder: probe.FixtureOrder, teamIDs: ids, cache: map[string]AchievementResult{}}, nil
}

// EvaluateStatus runs only the qualification proof for a target and fixed
// slate outcomes.
func (e *Evaluator) EvaluateStatus(ctx context.Context, targetTeamID string, achievement competition.Achievement, fixed []FixedResult) (AchievementResult, error) {
	if e == nil {
		return AchievementResult{}, fmt.Errorf("clinching evaluator is required")
	}
	r := Request{Teams: e.teams, Games: e.games, FixtureOrder: e.fixtureOrder, TargetTeamID: targetTeamID, Achievement: achievement, Fixed: append([]FixedResult(nil), fixed...)}
	if err := validateRequest(r); err != nil {
		return AchievementResult{}, err
	}
	key := e.statusKey(r)
	if cached, ok := e.cache[key]; ok {
		cached.Diagnostics.MemoHits++
		return cloneAchievement(cached), nil
	}
	value, err := evaluateStatusRequest(ctx, r)
	if err != nil {
		return value, err
	}
	if value.Status == Clinched || value.Status == NotClinched {
		e.cache[key] = cloneAchievement(value)
	}
	return value, nil
}

// Evaluate runs the status proof followed by the conservative no-help path.
func (e *Evaluator) Evaluate(ctx context.Context, targetTeamID string, achievement competition.Achievement, fixed []FixedResult) (AchievementResult, error) {
	value, err := e.EvaluateStatus(ctx, targetTeamID, achievement, fixed)
	if err != nil {
		return value, err
	}
	noHelp, err := e.EvaluateNoHelp(ctx, targetTeamID, achievement, fixed, value)
	if err != nil {
		return value, err
	}
	value.NoHelp = noHelp
	return value, nil
}

// EvaluateNoHelp calculates only the conservative no-help path for an
// already-proven status. Refreshers can run it after core status proofs so a
// slow no-help calculation does not block scenario generation.
func (e *Evaluator) EvaluateNoHelp(ctx context.Context, targetTeamID string, achievement competition.Achievement, fixed []FixedResult, base AchievementResult) (NoHelpPath, error) {
	if base.Status == Clinched {
		return NoHelpPath{State: NoHelpNotApplicable, FixtureIDs: []string{}}, nil
	}
	r := Request{Teams: e.teams, Games: e.games, FixtureOrder: e.fixtureOrder, TargetTeamID: targetTeamID, Achievement: achievement, Fixed: append([]FixedResult(nil), fixed...)}
	if err := validateRequest(r); err != nil {
		return NoHelpPath{}, err
	}
	value, err := e.addNoHelp(ctx, r, base)
	if err != nil {
		return NoHelpPath{}, err
	}
	return value.NoHelp, nil
}

func (e *Evaluator) addNoHelp(ctx context.Context, r Request, base AchievementResult) (AchievementResult, error) {
	fixed := append([]FixedResult(nil), r.Fixed...)
	fixedSet := map[string]bool{}
	for _, f := range fixed {
		fixedSet[f.GameID] = true
	}
	prefix := []string{}
	for _, id := range r.FixtureOrder {
		g := gameByID(r.Games, id)
		if (g.HomeTeamID != r.TargetTeamID && g.AwayTeamID != r.TargetTeamID) || fixedSet[id] {
			continue
		}
		o := AwayWin
		if g.HomeTeamID == r.TargetTeamID {
			o = HomeWin
		}
		fixed = append(fixed, FixedResult{GameID: id, Outcome: o})
		prefix = append(prefix, id)
		value, err := e.EvaluateStatus(ctx, r.TargetTeamID, r.Achievement, fixed)
		if err != nil {
			return base, err
		}
		if value.Status == Clinched {
			base.NoHelp = NoHelpPath{State: NoHelpGuaranteed, FixtureIDs: append([]string(nil), prefix...)}
			return base, nil
		}
	}
	value, err := e.EvaluateStatus(ctx, r.TargetTeamID, r.Achievement, fixed)
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

func (e *Evaluator) statusKey(r Request) string {
	fixed := append([]FixedResult(nil), r.Fixed...)
	sort.Slice(fixed, func(i, j int) bool { return fixed[i].GameID < fixed[j].GameID })
	points := map[string]int{}
	for _, f := range fixed {
		g := gameByID(e.games, f.GameID)
		switch f.Outcome {
		case HomeWin:
			points[g.HomeTeamID] += 3
		case AwayWin:
			points[g.AwayTeamID] += 3
		case Draw:
			points[g.HomeTeamID]++
			points[g.AwayTeamID]++
		}
	}
	ids := make([]string, 0, len(e.teamIDs))
	for id := range e.teamIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\x00%s\x00%d\x00", r.TargetTeamID, r.Achievement.ID, r.Achievement.TopK)
	for _, f := range fixed {
		b.WriteString(f.GameID)
		b.WriteByte(0)
	}
	for _, id := range ids {
		fmt.Fprintf(&b, "%s=%d\x00", id, points[id])
	}
	return b.String()
}

func cloneAchievement(v AchievementResult) AchievementResult {
	v.BlockingWitness = append([]WitnessGame(nil), v.BlockingWitness...)
	v.FrontierWitness = append([]WitnessGame(nil), v.FrontierWitness...)
	v.NoHelp.FixtureIDs = append([]string(nil), v.NoHelp.FixtureIDs...)
	return v
}
