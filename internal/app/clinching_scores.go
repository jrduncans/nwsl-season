package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type scenarioHistory struct {
	Maximum       int
	Scope, Cutoff string
}

// Note explains the missing comparison; it is empty when a scope exists.
func (h scenarioHistory) Note() string {
	if h.Scope != "" {
		return ""
	}
	return "Historical comparison unavailable: no verified complete prior regular seasons in the cache."
}

// marginSeason is what the comparison keeps of one verified complete prior
// regular season, so the archive is scanned once rather than per request.
type marginSeason struct {
	Season, Latest string
	Maximum        int
}

// marginSeasons summarizes seasons with verified official inventories and
// completed valid results, including source-backed awarded scores.
func marginSeasons(archive []cache.HistoricalSeason) []marginSeason {
	out := []marginSeason{}
	for _, season := range archive {
		if season.Entry.Stage != "Regular Season" || season.Readiness == nil || season.Readiness.Completeness != cache.InventoryCompletenessComplete || season.Readiness.Scope.Lifecycle != cache.SourceScopeCompleted || len(season.Data.Games) == 0 {
			continue
		}
		summary, valid := marginSeason{Season: season.Entry.Season}, true
		for _, g := range season.Data.Games {
			kickoff, err := fixtures.ParseKickoff(g.KickoffUTC)
			if err != nil || g.Status != standings.CompletedStatus || !g.HomeScore.Valid || !g.AwayScore.Valid || g.HomeScore.Int64 < 0 || g.AwayScore.Int64 < 0 {
				valid = false
				break
			}
			summary.Maximum = max(summary.Maximum, absInt(int(g.HomeScore.Int64-g.AwayScore.Int64)))
			summary.Latest = max(summary.Latest, kickoff.UTC().Format("2006-01-02"))
		}
		awards, complete := historicalMarginInventory(season)
		if !complete {
			continue
		}
		for _, award := range awards {
			summary.Maximum = max(summary.Maximum, award.WinnerGoals-award.LoserGoals)
		}
		if valid {
			out = append(out, summary)
		}
	}
	return out
}

// historicalMarginInventory uses the same official inventory policy as cache
// readiness, source refresh validation and season-page finality.
func historicalMarginInventory(season cache.HistoricalSeason) ([]competition.OutcomeAward, bool) {
	appearances := map[string]int{}
	for _, game := range season.Data.Games {
		appearances[game.HomeTeamID]++
		appearances[game.AwayTeamID]++
	}
	awards, err := competition.ReconcileInventory(season.Entry.Season, season.Entry.Stage, season.Entry.Inventory, len(season.Data.Games), appearances)
	return awards, err == nil
}

// marginHistoryBefore is a scoped comparison, never an all-time record: seasons
// from cutoff onward are future observations and are excluded.
func marginHistoryBefore(summaries []marginSeason, cutoff string) scenarioHistory {
	h := scenarioHistory{}
	seasons := []string{}
	for _, s := range summaries {
		if s.Season >= cutoff {
			continue
		}
		h.Maximum = max(h.Maximum, s.Maximum)
		h.Cutoff = max(h.Cutoff, s.Latest)
		seasons = append(seasons, s.Season)
	}
	slices.Sort(seasons)
	if len(seasons) > 0 {
		h.Scope = "NWSL regular seasons " + strings.Join(seasons, ", ")
	}
	return h
}

func scenarioMarginHistory(archive []cache.HistoricalSeason, cutoff string) scenarioHistory {
	return marginHistoryBefore(marginSeasons(archive), cutoff)
}

// scenarioHistoryCache holds the archive summary for all seasons. It is filled
// at startup and after a sync changes fixtures (RefreshScenarioHistory); a
// request that finds it empty fills it once. The zero value is ready to use.
type scenarioHistoryCache struct {
	mu        sync.RWMutex
	summaries []marginSeason
	loaded    bool
}

func (c *scenarioHistoryCache) get() ([]marginSeason, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.summaries, c.loaded
}

func (c *scenarioHistoryCache) set(summaries []marginSeason) {
	c.mu.Lock()
	c.summaries, c.loaded = summaries, true
	c.mu.Unlock()
}

// refreshScenarioHistory rereads the archive. A failed or unsupported read
// keeps whatever was cached before.
func (a *application) refreshScenarioHistory(ctx context.Context) error {
	store, ok := a.store.(historicalStore)
	if !ok {
		return nil
	}
	archive, err := store.HistoricalRegularSeasons(ctx)
	if err != nil {
		return fmt.Errorf("load historical margins: %w", err)
	}
	a.historyCache.set(marginSeasons(archive))
	return nil
}

func (a *application) scenarioHistory(ctx context.Context, season string) scenarioHistory {
	summaries, loaded := a.historyCache.get()
	if !loaded {
		if a.refreshScenarioHistory(ctx) != nil {
			return scenarioHistory{}
		}
		summaries, _ = a.historyCache.get()
	}
	return marginHistoryBefore(summaries, season)
}

func addAssumptions(base map[string]uint8, requirements []scenarioRequirement) map[string]uint8 {
	out := maps.Clone(base)
	if out == nil {
		out = map[string]uint8{}
	}
	for _, r := range requirements {
		if r.Score != nil || r.SecondGameID != "" {
			continue
		}
		if prior, ok := out[r.GameID]; ok {
			out[r.GameID] = prior & r.Mask
		} else {
			out[r.GameID] = r.Mask
		}
	}
	return out
}

// Enrich presentation only; the predicate and all outcome unions stay intact.
func presentScenarioGroups(groups []clinchingGroupView, labels map[string]string, games map[string]cache.Game, fixed map[string]uint8, history scenarioHistory) {
	for i := range groups {
		g := &groups[i]
		assumed := fixed
		if len(g.AlternativeOwn) == 0 {
			assumed = addAssumptions(fixed, g.Own)
		}
		presentExpression(&g.Summary, labels, games, assumed, history)
		presentExpression(&g.Help, labels, games, assumed, history)
		if g.GroupedSummary != nil {
			for j := range g.GroupedSummary.Groups {
				part := &g.GroupedSummary.Groups[j]
				presentExpression(&part.Expression, labels, games, assumed, history)
				if part.Rival != nil {
					presentRival(part.Rival, labels, games, assumed, history)
				}
			}
		}
		for j := range g.Rivals {
			presentRival(&g.Rivals[j], labels, games, assumed, history)
		}
	}
}
func presentExpression(e *scenarioExpression, labels map[string]string, games map[string]cache.Game, assumed map[string]uint8, history scenarioHistory) {
	e.Conditions = requirementsInKickoffOrder(e.Conditions, games)
	assumed = addAssumptions(assumed, e.Conditions)
	for i := range e.Conditions {
		presentScore(&e.Conditions[i], labels, games, assumed, history)
	}
	for i := range e.Alternatives {
		presentExpression(&e.Alternatives[i], labels, games, assumed, history)
	}
}
func presentRival(v *clinchingRivalView, labels map[string]string, games map[string]cache.Game, assumed map[string]uint8, history scenarioHistory) {
	for i := range v.Ties {
		tie := &v.Ties[i]
		if tie.Condition == nil {
			continue
		}
		local := maps.Clone(assumed)
		if local == nil {
			local = map[string]uint8{}
		}
		// A one-match rival record fixes the outcome, not the score.
		if len(v.GameIDs) == 1 {
			game := games[v.GameIDs[0]]
			for j := range scenarioOutcomes {
				if scenarioPoints(j, v.RivalID, game) == tie.Points {
					local[v.GameIDs[0]] = 1 << j
				}
			}
		}
		presentScore(tie.Condition, labels, games, local, history)
	}
}

func remainingTeamGames(team string, games map[string]cache.Game) []string {
	ids := []string{}
	for id, g := range games {
		if g.Status == simulation.RemainingStatus && (g.HomeTeamID == team || g.AwayTeamID == team) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func presentScore(r *scenarioRequirement, labels map[string]string, games map[string]cache.Game, assumed map[string]uint8, history scenarioHistory) {
	if r.Score == nil || len(r.Score.Levels) == 0 {
		return
	}
	r.Historical = ""
	c := *r.Score
	r.Parts = scoreConditionParts(c, labels)
	r.Explanation = scoreExplanation(c, labels, games)
	// Exact levels keep their full wording; other rows move it into the disclosure.
	if !c.Levels[0].ExactOnly {
		r.Explanation = append(append(r.Parts, scenarioRequirementPart{Text: ".", NoSpaceBefore: true}), r.Explanation...)
		r.Parts = []scenarioRequirementPart{teamPart(c.TeamID, labels), {Text: "finishes ahead of"}, teamPart(c.RivalID, labels), {Text: "on tiebreakers"}}
	}
	parts, margin := individualMargin(c, labels, games, assumed)
	if len(parts) > 0 {
		r.Parts = parts
	}
	// An equality branch can still prove a minimum individual win even when
	// the complete cascade must stay visible rather than becoming a margin sentence.
	if margin == 0 && c.Levels[0].Measure == scenarios.MeasureGoalDifference && (len(c.Levels) > 1 || c.Levels[0].ExactOnly) {
		level := c.Levels[0]
		level.MinimumGain-- // The equality branch is one below the strict threshold.
		level.ExactOnly = false
		bound := c
		bound.Levels = []scenarios.ScoreLevel{level}
		_, margin = individualMargin(bound, labels, games, assumed)
	}
	r.Text = partsText(r.Parts)
	if margin > history.Maximum && history.Scope != "" {
		r.Historical = fmt.Sprintf("A win by %d goals exceeds the largest margin in our data, %d.", margin, history.Maximum)
	}
}

func scoreExplanation(c scenarios.ScoreCondition, labels map[string]string, games map[string]cache.Game) []scenarioRequirementPart {
	parts := []scenarioRequirementPart{}
	for i, l := range c.Levels {
		if i > 0 {
			parts = append(parts, scenarioRequirementPart{Text: ".", NoSpaceBefore: true})
		}
		gap := l.RivalNow - l.TeamNow
		switch {
		case gap == 0:
			parts = append(parts, scenarioRequirementPart{Text: fmt.Sprintf("The teams are currently level on %s at %d", measureNoun(l.Measure), l.TeamNow)})
		case gap > 0:
			parts = append(parts, teamPart(c.RivalID, labels), scenarioRequirementPart{Text: fmt.Sprintf("currently leads by %d on %s", gap, measureNoun(l.Measure))})
		default:
			parts = append(parts, teamPart(c.TeamID, labels), scenarioRequirementPart{Text: fmt.Sprintf("currently leads by %d on %s", -gap, measureNoun(l.Measure))})
		}
	}
	remaining := func(team string) scenarioRequirementPart {
		count := len(remainingTeamGames(team, games))
		unit := "matches"
		if count == 1 {
			unit = "match"
		}
		return scenarioRequirementPart{Text: fmt.Sprintf("has %d %s remaining", count, unit)}
	}
	parts = append(parts, scenarioRequirementPart{Text: ".", NoSpaceBefore: true}, teamPart(c.TeamID, labels), remaining(c.TeamID), scenarioRequirementPart{Text: ";", NoSpaceBefore: true}, teamPart(c.RivalID, labels), remaining(c.RivalID), scenarioRequirementPart{Text: ".", NoSpaceBefore: true})
	return parts
}

// Return a winning-margin sentence only for a single GD threshold and a
// provably equivalent fixture configuration. Multiple open margins or unknown outcomes
// retain the final-table wording. No score search or display-string parsing.
func individualMargin(c scenarios.ScoreCondition, labels map[string]string, games map[string]cache.Game, assumed map[string]uint8) ([]scenarioRequirementPart, int) {
	if len(c.Levels) != 1 || c.Levels[0].Measure != scenarios.MeasureGoalDifference || c.Levels[0].ExactOnly {
		return nil, 0
	}
	team, rival := remainingTeamGames(c.TeamID, games), remainingTeamGames(c.RivalID, games)
	ownWin := ""
	for _, id := range team {
		if assumed[id] == 2 {
			continue
		}
		g := games[id]
		win := uint8(1)
		if g.AwayTeamID == c.TeamID {
			win = 4
		}
		if assumed[id] != win || ownWin != "" {
			return nil, 0
		}
		ownWin = id
	}
	if ownWin == "" {
		return nil, 0
	}
	minimum := c.Levels[0].MinimumGain
	parts := []scenarioRequirementPart{teamPart(c.TeamID, labels)}
	headToHead, rivalWinID := false, ""
	for _, id := range rival {
		if id == ownWin {
			headToHead = true
			continue
		}
		if assumed[id] == 2 {
			continue
		}
		g := games[id]
		win := uint8(1)
		if g.AwayTeamID == c.RivalID {
			win = 4
		}
		if assumed[id] != win || rivalWinID != "" {
			return nil, 0
		}
		rivalWinID = id
	}
	if rivalWinID != "" {
		return nil, 0
	}
	if headToHead {
		minimum = (minimum + 1) / 2
	}
	if minimum < 1 {
		return nil, 0
	}
	return append(parts, scenarioRequirementPart{Text: fmt.Sprintf("wins by %d+", minimum)}), minimum
}

func fixedScenarioMasks(fixed map[string]clinching.Outcome) map[string]uint8 {
	masks := map[string]uint8{}
	for id, o := range fixed {
		masks[id] = outcomeMask([]clinching.Outcome{o})
	}
	return masks
}
