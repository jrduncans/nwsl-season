package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type scenarioHistory struct {
	Maximum       int
	Scope, Cutoff string
}

// This is a scoped comparison, never an all-time record. Unknown/incomplete
// inventories, unfinished seasons and future observations are excluded entirely.
func scenarioMarginHistory(archive []cache.HistoricalSeason, cutoff string) scenarioHistory {
	h := scenarioHistory{}
	seasons := []string{}
	for _, season := range archive {
		if season.Entry.Season >= cutoff || season.Entry.Stage != "Regular Season" || season.Readiness == nil || season.Readiness.Completeness != cache.InventoryCompletenessComplete || season.Readiness.Scope.Lifecycle != cache.SourceScopeCompleted || len(season.Data.Games) == 0 {
			continue
		}
		maximum, latest, valid := 0, "", true
		for _, g := range season.Data.Games {
			kickoff, err := fixtures.ParseKickoff(g.KickoffUTC)
			if err != nil || g.Status != standings.CompletedStatus || !g.HomeScore.Valid || !g.AwayScore.Valid || g.HomeScore.Int64 < 0 || g.AwayScore.Int64 < 0 {
				valid = false
				break
			}
			maximum = max(maximum, absInt(int(g.HomeScore.Int64-g.AwayScore.Int64)))
			latest = max(latest, kickoff.UTC().Format("2006-01-02"))
		}
		if !valid {
			continue
		}
		h.Maximum = max(h.Maximum, maximum)
		h.Cutoff = max(h.Cutoff, latest)
		seasons = append(seasons, season.Entry.Season)
	}
	slices.Sort(seasons)
	if len(seasons) > 0 {
		h.Scope = "NWSL regular seasons " + strings.Join(seasons, ", ")
	}
	return h
}

func (a *application) scenarioHistory(ctx context.Context, season string) scenarioHistory {
	if store, ok := a.store.(historicalStore); ok {
		archive, err := store.HistoricalRegularSeasons(ctx)
		if err == nil {
			return scenarioMarginHistory(archive, season)
		}
	}
	return scenarioHistory{}
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
	parts, margin := individualMargin(c, labels, games, assumed)
	if len(parts) > 0 {
		r.Parts = parts
	}
	r.Text = partsText(r.Parts)
	if margin > history.Maximum && history.Scope != "" {
		r.Historical = fmt.Sprintf("Requires a win by at least %d goals, exceeding the largest winning margin (%d) in complete cached %s, through %s. This is a comparison with that history, not an all-time record.", margin, history.Maximum, history.Scope, history.Cutoff)
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
		if headToHead {
			return nil, 0
		}
		phrase := "must win by a larger margin than"
		switch {
		case minimum == 0:
			phrase = "must win by at least as large a margin as"
		case minimum != 1:
			return nil, 0
		}
		return append(parts, scenarioRequirementPart{Text: phrase}, teamPart(c.RivalID, labels)), 0
	}
	if headToHead {
		minimum = (minimum + 1) / 2
	}
	if minimum < 1 {
		return nil, 0
	}
	return append(parts, scenarioRequirementPart{Text: fmt.Sprintf("must win by at least %d goals", minimum)}), minimum
}

func fixedScenarioMasks(fixed map[string]clinching.Outcome) map[string]uint8 {
	masks := map[string]uint8{}
	for id, o := range fixed {
		masks[id] = outcomeMask([]clinching.Outcome{o})
	}
	return masks
}
