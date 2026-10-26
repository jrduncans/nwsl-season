package app

import (
	"fmt"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

// Outcome masks over the home-win, draw and away-win bits.
const (
	maskHomeWin = 1
	maskDraw    = 2
	maskAwayWin = 4
	maskNotDraw = maskHomeWin | maskAwayWin
	maskAny     = maskNotDraw | maskDraw
)

// Presentation classifies the finalized, displayed structure without expanding
// factored alternatives. Overflow keeps its existing bounded fallback.
func (g clinchingGroupView) Presentation() string {
	if g.SummaryOverflow {
		return "overflow"
	}
	if g.RivalState != "" {
		if g.RivalChoice && len(g.Rivals) > 2 {
			return "dense"
		}
		count := 0
		for _, rival := range g.Rivals {
			alternatives := len(rival.Ties)
			if alternatives == 0 || rival.Ties[0].Alternative {
				alternatives++
			}
			count += alternatives
			if alternatives > 2 {
				return "dense"
			}
			for _, tie := range rival.Ties {
				if tie.Condition != nil && tie.Condition.Score != nil {
					return "dense"
				}
			}
		}
		if count > 3 {
			return "dense"
		}
		return "compact"
	}
	if g.GroupedSummary != nil {
		return "dense"
	}
	count, dense := expressionPresentation(g.Summary)
	if count == 0 && !dense {
		return "certain"
	}
	if count > 3 || dense {
		return "dense"
	}
	return "compact"
}

func expressionPresentation(e scenarioExpression) (int, bool) {
	count, dense := len(e.Conditions), len(e.Alternatives) > 2
	for _, r := range e.Conditions {
		dense = dense || r.Score != nil
	}
	for _, child := range e.Alternatives {
		n, d := expressionPresentation(child)
		count += n
		dense = dense || d
	}
	return count, dense
}

// Own headings omit the card's subject, but retain typed opponents and venues.
func ownHeadingParts(g clinchingGroupView, team string, teams map[string]string, games map[string]cache.Game) []scenarioRequirementPart {
	if len(g.Own) == 0 {
		parts := []scenarioRequirementPart{{Text: "With any results"}}
		return append(parts, rivalFixtureParts(g.UnrestrictedOwn, team, teams, games)...)
	}
	if len(g.Own) == 2 && (len(g.AlternativeOwn) == 2 || ownPoints(g.Own[0], team, games) == ownPoints(g.Own[1], team, games)) {
		a, b := ownPoints(g.Own[0], team, games), ownPoints(g.Own[1], team, games)
		verbs := map[int]string{0: "lose", 1: "draw", 3: "win"}
		nouns := map[int]string{0: "loss", 1: "draw", 3: "win"}
		text := verbs[a] + " both matches"
		if a != b {
			text = "one " + nouns[max(a, b)] + " and one " + nouns[min(a, b)]
		}
		parts := []scenarioRequirementPart{{Text: strings.ToUpper(text[:1]) + text[1:]}}
		return append(parts, rivalFixtureParts([]string{g.Own[0].GameID, g.Own[1].GameID}, team, teams, games)...)
	}
	parts := []scenarioRequirementPart{}
	for i, r := range g.Own {
		if i > 0 {
			parts = append(parts, scenarioRequirementPart{Text: "and"})
		}
		if r.SecondGameID != "" {
			parts = append(parts, scenarioRequirementPart{Text: fmt.Sprintf("Earn at least %s across both matches", pointsText(r.Points))})
			parts = append(parts, rivalFixtureParts([]string{r.GameID, r.SecondGameID}, team, teams, games)...)
			continue
		}
		p := requirementParts(r, team, teams, games)[1:]
		win := uint8(maskHomeWin)
		if games[r.GameID].AwayTeamID == team {
			win = maskAwayWin
		}
		loss := uint8(maskNotDraw) ^ win
		switch r.Mask {
		case win:
			p[0].Text = "Win"
		case maskDraw:
			p[0].Text = "Draw"
		case loss:
			p[0].Text = "Lose"
		case win | maskDraw:
			p[0].Text = "Win or draw"
		case loss | maskDraw:
			p[0].Text = "Draw or lose"
		case maskNotDraw:
			p[0].Text = "Win or lose"
		case maskAny:
			p[0].Text = "Any result"
		}
		if i > 0 {
			p[0].Text = strings.ToLower(p[0].Text)
		}
		parts = append(parts, p...)
	}
	return parts
}
