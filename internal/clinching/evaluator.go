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
	gamesByID    map[string]standings.Game
	basePoints   map[string]int
	pending      []standings.Game
	cache        map[string]AchievementResult
	diagnostics  Diagnostics
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
	gamesByID := make(map[string]standings.Game, len(probe.Games))
	basePoints := make(map[string]int, len(probe.Teams))
	pending := make([]standings.Game, 0, len(probe.Games))
	for _, team := range probe.Teams {
		basePoints[team.ID] = 0
	}
	for _, game := range probe.Games {
		gamesByID[game.ID] = game
		if game.Status == standings.CompletedStatus {
			applyPoints(basePoints, game, *game.HomeScore, *game.AwayScore)
		} else {
			pending = append(pending, game)
		}
	}
	return &Evaluator{teams: probe.Teams, games: probe.Games, fixtureOrder: probe.FixtureOrder, teamIDs: ids, gamesByID: gamesByID, basePoints: basePoints, pending: pending, cache: map[string]AchievementResult{}}, nil
}

// EvaluateStatus runs only the qualification proof for a target and fixed
// slate outcomes.
func (e *Evaluator) EvaluateStatus(ctx context.Context, targetTeamID string, achievement competition.Achievement, fixed []FixedResult) (AchievementResult, error) {
	return e.evaluateStatus(ctx, targetTeamID, achievement, fixed, false)
}

// EvaluateStatusSummary returns the same mathematical classification without
// serializing a full-season witness. Scenario search needs only status and proof
// method at thousands of leaves.
func (e *Evaluator) EvaluateStatusSummary(ctx context.Context, targetTeamID string, achievement competition.Achievement, fixed []FixedResult) (AchievementResult, error) {
	return e.evaluateStatus(ctx, targetTeamID, achievement, fixed, true)
}

func (e *Evaluator) evaluateStatus(ctx context.Context, targetTeamID string, achievement competition.Achievement, fixed []FixedResult, omitWitness bool) (AchievementResult, error) {
	if e == nil {
		return AchievementResult{}, fmt.Errorf("clinching evaluator is required")
	}
	r := Request{Teams: e.teams, Games: e.games, FixtureOrder: e.fixtureOrder, TargetTeamID: targetTeamID, Achievement: achievement, Fixed: append([]FixedResult(nil), fixed...), validated: true, omitWitness: omitWitness}
	if err := e.validateProbe(r); err != nil {
		return AchievementResult{}, err
	}
	prepared, err := e.prepare(r)
	if err != nil {
		return AchievementResult{}, err
	}
	r.prepared = &prepared
	cacheable := !omitWitness && len(fixed) <= 4
	key := ""
	if cacheable {
		key = e.statusKey(r)
		if cached, ok := e.cache[key]; ok {
			cached.Diagnostics.MemoHits++
			e.diagnostics.MemoHits++
			return cloneAchievement(cached), nil
		}
	}
	value, err := evaluateStatusRequest(ctx, r)
	if err != nil {
		return value, err
	}
	if cacheable && (value.Status == Clinched || value.Status == NotClinched) {
		e.cache[key] = cloneAchievement(value)
	}
	e.addDiagnostics(value.Diagnostics)
	return value, nil
}

// Diagnostics returns cumulative solver work for all status probes performed
// by this evaluator. It is primarily intended for deterministic benchmarks.
func (e *Evaluator) Diagnostics() Diagnostics {
	if e == nil {
		return Diagnostics{}
	}
	return e.diagnostics
}

func (e *Evaluator) addDiagnostics(value Diagnostics) {
	e.diagnostics.ReducedTeams += value.ReducedTeams
	e.diagnostics.ReducedFixtures += value.ReducedFixtures
	e.diagnostics.ConnectedComponents += value.ConnectedComponents
	e.diagnostics.SubsetProbes += value.SubsetProbes
	e.diagnostics.VisitedStates += value.VisitedStates
	e.diagnostics.MemoHits += value.MemoHits
	e.diagnostics.IndividualPrunes += value.IndividualPrunes
	e.diagnostics.ComponentPrunes += value.ComponentPrunes
	e.diagnostics.TotalPrunes += value.TotalPrunes
	e.diagnostics.ElapsedMicroseconds += value.ElapsedMicroseconds
}

// EvaluateCheapClinched runs only the linear upper bound needed to certify a
// clinch. Scenario search uses it at internal tree nodes; an inconclusive result
// is expanded to leaves, where EvaluateStatus supplies the exact decision.
func (e *Evaluator) EvaluateCheapClinched(targetTeamID string, achievement competition.Achievement, fixed []FixedResult) (AchievementResult, bool, error) {
	r := Request{Teams: e.teams, Games: e.games, FixtureOrder: e.fixtureOrder, TargetTeamID: targetTeamID, Achievement: achievement, Fixed: append([]FixedResult(nil), fixed...), validated: true}
	if err := e.validateProbe(r); err != nil {
		return AchievementResult{}, false, err
	}
	prepared, err := e.prepare(r)
	if err != nil {
		return AchievementResult{}, false, err
	}
	result := AchievementResult{TeamID: targetTeamID, Achievement: achievement.ID, TopK: achievement.TopK, BlockingWitness: []WitnessGame{}, FrontierWitness: []WitnessGame{}, NoHelp: NoHelpPath{State: NoHelpNotApplicable, FixtureIDs: []string{}}}
	result.Diagnostics.BoundCapableTeams = prepared.capable
	if prepared.capable >= achievement.TopK {
		return result, false, nil
	}
	strictAhead := 0
	for id, points := range prepared.points {
		if id != targetTeamID && points > prepared.frontier {
			strictAhead++
		}
	}
	result.Status, result.Method = Clinched, ProofCheapBound
	result.StrictlyAhead = CountEvidence{Value: strictAhead, Kind: "upper_bound"}
	result.AtLeastLevel = CountEvidence{Value: prepared.capable, Kind: "upper_bound"}
	return result, true, nil
}

// SlateBlocker reuses one post-slate fixture preparation across a target's
// achievements. A proof for a larger TopK also proves every smaller TopK.
type SlateBlocker struct {
	prepared  preparedSeason
	blockedAt int
	results   map[int]bool
}

// NewSlateBlocker prepares the fixtures after slateFixtureIDs and gives the
// target its maximum possible slate points. The resulting object can answer
// exact no-clinch questions for each achievement without rebuilding that
// common state.
func (e *Evaluator) NewSlateBlocker(targetTeamID string, slateFixtureIDs []string) (*SlateBlocker, error) {
	if e == nil || targetTeamID == "" || !e.teamIDs[targetTeamID] {
		return nil, fmt.Errorf("invalid universal slate blocker request")
	}
	slate := make(map[string]bool, len(slateFixtureIDs))
	targetWins := 0
	for _, id := range slateFixtureIDs {
		game, ok := e.gamesByID[id]
		if !ok || game.Status != "PreMatch" || slate[id] {
			return nil, fmt.Errorf("invalid slate fixture %q", id)
		}
		slate[id] = true
		if game.HomeTeamID == targetTeamID || game.AwayTeamID == targetTeamID {
			targetWins++
		}
	}
	postGames := make([]standings.Game, 0, len(e.games)-len(slate))
	for _, game := range e.games {
		if !slate[game.ID] {
			postGames = append(postGames, game)
		}
	}
	postOrder := make([]string, 0, len(e.fixtureOrder)-len(slate))
	for _, id := range e.fixtureOrder {
		if !slate[id] {
			postOrder = append(postOrder, id)
		}
	}
	prepared, err := prepare(Request{Teams: e.teams, Games: postGames, FixtureOrder: postOrder, TargetTeamID: targetTeamID, Achievement: competition.Achievement{ID: "slate_blocker", TopK: 1}, validated: true})
	if err != nil {
		return nil, err
	}
	prepared.points[targetTeamID] += 3 * targetWins
	prepared.frontier = prepared.points[targetTeamID]
	return &SlateBlocker{prepared: prepared, results: map[int]bool{}}, nil
}

// Blocks reports whether a completion of only the post-slate fixtures puts
// enough opponents strictly above the target's best slate total. That single
// completion defeats every slate outcome, so no such outcome can clinch the
// requested achievement.
func (b *SlateBlocker) Blocks(ctx context.Context, achievement competition.Achievement) (bool, error) {
	if b == nil || achievement.ID == "" || achievement.TopK < 1 || achievement.TopK > len(b.prepared.points) {
		return false, fmt.Errorf("invalid universal slate blocker achievement")
	}
	if b.blockedAt >= achievement.TopK {
		return true, nil
	}
	if result, ok := b.results[achievement.TopK]; ok {
		return result, nil
	}
	blocker, err := solveCutoff(ctx, b.prepared, b.prepared.frontier+1, achievement.TopK)
	if err != nil {
		return false, err
	}
	b.results[achievement.TopK] = blocker.feasible
	if blocker.feasible && achievement.TopK > b.blockedAt {
		b.blockedAt = achievement.TopK
	}
	return blocker.feasible, nil
}

// HasUniversalSlateBlocker is a single-achievement convenience wrapper.
func (e *Evaluator) HasUniversalSlateBlocker(ctx context.Context, targetTeamID string, achievement competition.Achievement, slateFixtureIDs []string) (bool, error) {
	blocker, err := e.NewSlateBlocker(targetTeamID, slateFixtureIDs)
	if err != nil {
		return false, err
	}
	return blocker.Blocks(ctx, achievement)
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
	r := Request{Teams: e.teams, Games: e.games, FixtureOrder: e.fixtureOrder, TargetTeamID: targetTeamID, Achievement: achievement, Fixed: append([]FixedResult(nil), fixed...), validated: true}
	if err := e.validateProbe(r); err != nil {
		return NoHelpPath{}, err
	}
	value, _, err := e.addNoHelp(ctx, r, base, 0)
	if err != nil {
		return NoHelpPath{}, err
	}
	return value.NoHelp, nil
}

// EvaluateNoHelpBatch calculates all requested achievement paths for one team.
// Easier achievements are evaluated first; their shortest prefix is a safe
// lower bound for every stronger achievement.
func (e *Evaluator) EvaluateNoHelpBatch(ctx context.Context, targetTeamID string, achievements []competition.Achievement, fixed []FixedResult, bases map[competition.AchievementID]AchievementResult) (map[competition.AchievementID]NoHelpPath, error) {
	ordered := append([]competition.Achievement(nil), achievements...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].TopK > ordered[j].TopK })
	result := make(map[competition.AchievementID]NoHelpPath, len(ordered))
	lower := 0
	for _, achievement := range ordered {
		base, ok := bases[achievement.ID]
		if !ok {
			return nil, fmt.Errorf("missing no-help baseline for achievement %q", achievement.ID)
		}
		if base.Status == Clinched {
			result[achievement.ID] = NoHelpPath{State: NoHelpNotApplicable, FixtureIDs: []string{}}
			continue
		}
		r := Request{Teams: e.teams, Games: e.games, FixtureOrder: e.fixtureOrder, TargetTeamID: targetTeamID, Achievement: achievement, Fixed: append([]FixedResult(nil), fixed...), validated: true}
		if err := e.validateProbe(r); err != nil {
			return nil, err
		}
		value, prefix, err := e.addNoHelp(ctx, r, base, lower)
		if err != nil {
			return nil, err
		}
		result[achievement.ID] = value.NoHelp
		if value.NoHelp.State == NoHelpGuaranteed {
			lower = prefix
		}
	}
	return result, nil
}

func (e *Evaluator) addNoHelp(ctx context.Context, r Request, base AchievementResult, lower int) (AchievementResult, int, error) {
	fixed := append([]FixedResult(nil), r.Fixed...)
	fixedSet := map[string]bool{}
	for _, f := range fixed {
		fixedSet[f.GameID] = true
	}
	wins := []FixedResult{}
	fixtureIDs := []string{}
	for _, id := range r.FixtureOrder {
		g := e.gamesByID[id]
		if (g.HomeTeamID != r.TargetTeamID && g.AwayTeamID != r.TargetTeamID) || fixedSet[id] {
			continue
		}
		o := AwayWin
		if g.HomeTeamID == r.TargetTeamID {
			o = HomeWin
		}
		wins = append(wins, FixedResult{GameID: id, Outcome: o})
		fixtureIDs = append(fixtureIDs, id)
	}
	probe := func(count int) (AchievementResult, error) {
		values := make([]FixedResult, 0, len(fixed)+count)
		values = append(values, fixed...)
		values = append(values, wins[:count]...)
		return e.EvaluateStatus(ctx, r.TargetTeamID, r.Achievement, values)
	}
	endpoint, err := probe(len(wins))
	if err != nil {
		return base, 0, err
	}
	if endpoint.Status == Unresolved && endpoint.Method == ProofComputeBudget {
		base.NoHelp = NoHelpPath{State: NoHelpUnresolved, FixtureIDs: []string{}, Reason: "calculation budget exhausted"}
		return base, 0, nil
	}
	if endpoint.Status != Clinched {
		switch endpoint.Status {
		case NotClinched:
			base.NoHelp = NoHelpPath{State: NoHelpImpossible, FixtureIDs: []string{}, Reason: "even winning every remaining target fixture does not guarantee the achievement"}
		case Unresolved:
			base.NoHelp = NoHelpPath{State: NoHelpUnresolved, FixtureIDs: []string{}, Reason: endpoint.Reason}
		}
		return base, 0, nil
	}
	if lower < 0 {
		lower = 0
	}
	if lower > len(wins) {
		lower = len(wins)
	}
	// lower is a known lower bound, not necessarily a known failure. Check it
	// before binary search so callers outside the batch retain exact behavior.
	low := lower
	if low > 0 {
		value, err := probe(low)
		if err != nil {
			return base, 0, err
		}
		if value.Status == Unresolved && value.Method == ProofComputeBudget {
			base.NoHelp = NoHelpPath{State: NoHelpUnresolved, FixtureIDs: []string{}, Reason: "calculation budget exhausted"}
			return base, 0, nil
		}
		if value.Status == Clinched {
			low = 0
		}
	}
	high := len(wins)
	for low+1 < high {
		mid := low + (high-low)/2
		value, err := probe(mid)
		if err != nil {
			return base, 0, err
		}
		if value.Status == Unresolved && value.Method == ProofComputeBudget {
			base.NoHelp = NoHelpPath{State: NoHelpUnresolved, FixtureIDs: []string{}, Reason: "calculation budget exhausted"}
			return base, 0, nil
		}
		if value.Status == Clinched {
			high = mid
		} else {
			low = mid
		}
	}
	if high == 0 {
		base.NoHelp = NoHelpPath{State: NoHelpNotApplicable, FixtureIDs: []string{}}
		return base, 0, nil
	}
	base.NoHelp = NoHelpPath{State: NoHelpGuaranteed, FixtureIDs: append([]string(nil), fixtureIDs[:high]...)}
	return base, high, nil
}

func (e *Evaluator) statusKey(r Request) string {
	fixed := append([]FixedResult(nil), r.Fixed...)
	sort.Slice(fixed, func(i, j int) bool { return fixed[i].GameID < fixed[j].GameID })
	points := map[string]int{}
	for _, f := range fixed {
		g := e.gamesByID[f.GameID]
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
		b.WriteByte('=')
		b.WriteString(string(f.Outcome))
		b.WriteByte(0)
	}
	for _, id := range ids {
		fmt.Fprintf(&b, "%s=%d\x00", id, points[id])
	}
	return b.String()
}

func (e *Evaluator) validateProbe(r Request) error {
	if r.TargetTeamID == "" || !e.teamIDs[r.TargetTeamID] || r.Achievement.ID == "" || r.Achievement.TopK < 1 || r.Achievement.TopK > len(e.teams) {
		return fmt.Errorf("invalid achievement request")
	}
	seen := make(map[string]bool, len(r.Fixed))
	for _, fixed := range r.Fixed {
		game, ok := e.gamesByID[fixed.GameID]
		if !ok || game.Status == standings.CompletedStatus || seen[fixed.GameID] || !validOutcome(fixed.Outcome) {
			return fmt.Errorf("invalid fixed result for game %q", fixed.GameID)
		}
		seen[fixed.GameID] = true
	}
	return nil
}

func (e *Evaluator) prepare(r Request) (preparedSeason, error) {
	p := preparedSeason{target: r.TargetTeamID, points: make(map[string]int, len(e.basePoints)), witness: map[string]WitnessGame{}}
	for id, value := range e.basePoints {
		p.points[id] = value
	}
	fixed := make(map[string]Outcome, len(r.Fixed))
	for _, value := range r.Fixed {
		fixed[value.GameID] = value.Outcome
	}
	incident := make(map[string]int, len(e.teamIDs))
	for _, game := range e.pending {
		if outcome, ok := fixed[game.ID]; ok {
			p.witness[game.ID] = witnessFrom(game, outcome)
			applyOutcome(p.points, game, outcome)
			continue
		}
		if game.HomeTeamID == p.target || game.AwayTeamID == p.target {
			outcome := HomeWin
			if game.HomeTeamID == p.target {
				outcome = AwayWin
			}
			p.witness[game.ID] = witnessFrom(game, outcome)
			applyOutcome(p.points, game, outcome)
			continue
		}
		p.decision = append(p.decision, game)
		incident[game.HomeTeamID]++
		incident[game.AwayTeamID]++
	}
	p.frontier = p.points[p.target]
	for id, points := range p.points {
		if id != p.target && points+3*incident[id] >= p.frontier {
			p.capable++
		}
	}
	return p, nil
}

func cloneAchievement(v AchievementResult) AchievementResult {
	v.BlockingWitness = append([]WitnessGame(nil), v.BlockingWitness...)
	v.FrontierWitness = append([]WitnessGame(nil), v.FrontierWitness...)
	v.NoHelp.FixtureIDs = append([]string(nil), v.NoHelp.FixtureIDs...)
	return v
}
