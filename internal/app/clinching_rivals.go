package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

// clinchingRivalView is one rival in a rest-of-season group: the rival's
// results that keep the team ahead of it, then each record that leaves the
// two level on points, with the score condition that decides it.
type clinchingRivalView struct {
	Conjoined bool // an additional required group, not a separate path
	Parts     []scenarioRequirementPart
	GameIDs   []string
	RivalID   string
	Ties      []clinchingRivalTieView
}

// clinchingRivalTieView is one level-on-points record. Condition is nil when
// the team finishes ahead whatever the scores.
type clinchingRivalTieView struct {
	Lead        string
	Result      string // record and fixtures, without a conjunction or score prose
	ResultParts []scenarioRequirementPart
	// FullParts repeats the rival and fixtures for a standalone alternative.
	FullParts   []scenarioRequirementPart
	Condition   *scenarioRequirement
	Alternative bool
	Points      int
}

// rivalGroupViews builds rest-of-season groups from a rival summary: one per
// row of the team's own results, ordered and merged as clause groups are. A
// withheld row falls back to the clauses for those own results.
func rivalGroupViews(result scenarios.Result, teamID string, teams, headingTeams map[string]string, games map[string]cache.Game, positions map[string]int) []clinchingGroupView {
	rows := slices.Clone(result.Rivals.Rows)
	for i := range rows {
		rows[i].Own = ownInKickoffOrder(rows[i].Own, games)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return ownResultsKey(rows[i].Own, teamID, games) < ownResultsKey(rows[j].Own, teamID, games)
	})
	views := []clinchingGroupView{}
	for _, row := range rows {
		if row.Withheld {
			if clauses := clausesForOwn(result.Clauses, row.Own); len(clauses) > 0 {
				views = append(views, clinchingGroupViews(clauses, teamID, teams, headingTeams, games, clinchingGroupOptions{SummaryLimit: maxSeasonSummaryPaths, RestOfSeason: true})...)
			}
			continue
		}
		view := clinchingGroupView{MergeKey: rivalRowKey(row)}
		words := []string{}
		for _, condition := range row.Own {
			r := scenarioRequirement{GameID: condition.GameID, Mask: outcomeMask(condition.AllowedOutcomes)}
			r.Text = requirementText(r, teamID, headingTeams, games)
			r.Parts = requirementParts(r, teamID, teams, games)
			view.Own = append(view.Own, r)
			words = append(words, r.Text)
		}
		view.Heading = "Regardless of " + headingTeams[teamID] + "’s results"
		if len(words) > 0 {
			view.Heading = "If " + joinConditions(words)
		}
		rivals := slices.Clone(row.Rivals)
		sort.SliceStable(rivals, func(i, j int) bool { return positions[rivals[i].RivalID] < positions[rivals[j].RivalID] })
		view.Rivals = rivalViews(rivals, row.Need == len(rivals), teams, games)
		view.RivalChoice = row.Need > 0 && row.Need < len(rivals)
		switch {
		case row.Need == 0:
		case len(view.Rivals) == 1:
			view.RivalState = "Clinches if"
		case row.Need == len(row.Rivals):
			view.RivalState = "Clinches if all of:"
		case row.Need == 1:
			view.RivalState = "Clinches if any of:"
		default:
			view.RivalState = fmt.Sprintf("Clinches if at least %d of:", row.Need)
		}
		views = append(views, view)
	}
	views = combineOwnSingleResults(combineOwnPointTotals(combineOwnPairs(views, teamID, headingTeams, games), teamID, headingTeams, games), teamID, headingTeams, games)
	for i := range views {
		views[i].Tone = ownResultTone(views[i], teamID, games)
		views[i].HeadingParts = ownHeadingParts(views[i], teamID, teams, games)
	}
	return views
}

// rivalViews intersects outcome-only needs for the same fixture when every
// rival's need must hold. Counting rivals in an any-of or at-least-N row must
// retain separate needs; score conditions and multi-match records stay intact.
func rivalViews(rivals []scenarios.RivalNeed, all bool, teams map[string]string, games map[string]cache.Game) []clinchingRivalView {
	views := []clinchingRivalView{}
	used := make([]bool, len(rivals))
	for i, rival := range rivals {
		if used[i] {
			continue
		}
		mask, ok := rivalFixtureMask(rival, games)
		if !all || !ok {
			views = append(views, rivalView(rival, teams, games))
			continue
		}
		matches := []int{}
		for j := i + 1; j < len(rivals); j++ {
			other, ok := rivalFixtureMask(rivals[j], games)
			if !used[j] && ok && rivals[j].GameIDs[0] == rival.GameIDs[0] {
				mask &= other
				matches = append(matches, j)
			}
		}
		if len(matches) == 0 || mask == 0 {
			views = append(views, rivalView(rival, teams, games))
			continue
		}
		for _, j := range matches {
			used[j] = true
		}
		r := scenarioRequirement{GameID: rival.GameIDs[0], Mask: mask}
		views = append(views, clinchingRivalView{Parts: requirementParts(r, rival.RivalID, teams, games)})
	}
	return views
}

func rivalFixtureMask(need scenarios.RivalNeed, games map[string]cache.Game) (uint8, bool) {
	if len(need.GameIDs) != 1 {
		return 0, false
	}
	game, ok := games[need.GameIDs[0]]
	if !ok || (game.HomeTeamID != need.RivalID && game.AwayTeamID != need.RivalID) {
		return 0, false
	}
	for _, tie := range need.Ties {
		if tie.Condition != nil {
			return 0, false
		}
	}
	var mask uint8
	for outcome := range scenarioOutcomes {
		points := scenarioPoints(outcome, need.RivalID, game)
		if points <= need.MaxPoints || slices.ContainsFunc(need.Ties, func(tie scenarios.RivalTie) bool { return tie.Points == points }) {
			mask |= 1 << outcome
		}
	}
	return mask, true
}

// rivalView words one rival's need. The rival's results come first ("SD
// earns at most 3 points (vs BOS and at BAY)"), then each level record and
// its condition ("or earns 4 points if NJY gains 1+ goal difference on SD").
func rivalView(need scenarios.RivalNeed, teams map[string]string, games map[string]cache.Game) clinchingRivalView {
	n := len(need.GameIDs)
	fixtureParts := rivalFixtureParts(need.GameIDs, need.RivalID, teams, games)
	fixtures := partsText(fixtureParts)
	base := ""
	switch limit := need.MaxPoints; {
	case limit < 0:
	case n == 1 && limit == 0:
		base = "loses"
	case n == 1:
		base = "draws or loses"
	case n == 2 && limit >= 4:
		base = "doesn’t win both matches"
	case limit == 0:
		base = "loses " + allMatches(n)
	default:
		base = fmt.Sprintf("earns at most %s", pointsText(limit))
	}
	view := clinchingRivalView{GameIDs: need.GameIDs, RivalID: need.RivalID}
	if n > 0 {
		view.Parts = []scenarioRequirementPart{{Team: &scenarioTeamToken{Code: teams[need.RivalID], LogoURL: clubLogoURL(need.RivalID)}}}
	}
	if base != "" {
		view.Parts = append(view.Parts, scenarioRequirementPart{Text: base})
		view.Parts = append(view.Parts, fixtureParts...)
	}
	stated := base != ""
	for _, tie := range need.Ties {
		record := tieRecordText(tie, n)
		var resultParts []scenarioRequirementPart
		if record != "" {
			resultParts = []scenarioRequirementPart{{Text: record}}
		}
		if !stated {
			resultParts = append(resultParts, fixtureParts...)
		}
		if !stated && n > 0 {
			record += " " + fixtures
		}
		lead := record
		if stated {
			lead = "or " + record
		}
		item := clinchingRivalTieView{Lead: lead, Result: record, ResultParts: resultParts, Alternative: stated, Points: tie.Points}
		if n > 0 {
			item.FullParts = append(item.FullParts, teamPart(need.RivalID, teams))
		}
		if result := tieRecordText(tie, n); result != "" {
			item.FullParts = append(item.FullParts, scenarioRequirementPart{Text: result})
		}
		item.FullParts = append(item.FullParts, fixtureParts...)
		if tie.Condition != nil {
			switch {
			case stated:
				item.Lead += " if"
			case n > 0:
				item.Lead += " and"
			}
			condition := scenarioRequirement{Score: tie.Condition}
			condition.Text = requirementText(condition, "", teams, games)
			condition.Parts = requirementParts(condition, "", teams, games)
			item.Condition = &condition
		}
		view.Ties = append(view.Ties, item)
		stated = true
	}
	return view
}

// rivalFixtureParts keeps fixture identity from IDs, including repeated
// opponents. Punctuation follows the same list wording as noHelpFixtureText.
func rivalFixtureParts(ids []string, rival string, teams map[string]string, games map[string]cache.Game) []scenarioRequirementPart {
	parts := []scenarioRequirementPart{}
	for _, id := range fixtureIDsInKickoffOrder(ids, games) {
		game, ok := games[id]
		if !ok {
			continue
		}
		opponent, venue := "", ""
		switch rival {
		case game.HomeTeamID:
			opponent, venue = game.AwayTeamID, "vs"
		case game.AwayTeamID:
			opponent, venue = game.HomeTeamID, "at"
		default:
			continue
		}
		parts = append(parts, scenarioRequirementPart{Text: venue}, scenarioRequirementPart{Team: &scenarioTeamToken{Code: teams[opponent], LogoURL: clubLogoURL(opponent)}})
	}
	count := len(parts) / 2
	for i := range count {
		switch {
		case i == 0 && len(ids) > 1:
			parts[0].Text = "(" + parts[0].Text
		case i > 0:
			parts[i*2].Text = ", " + parts[i*2].Text
			parts[i*2].NoSpaceBefore = true
		}
	}
	if count > 0 && len(ids) > 1 {
		parts = append(parts, scenarioRequirementPart{Text: ")", NoSpaceBefore: true})
	}
	return parts
}

// tieRecordText names a record from the rival's side: a result for one
// match, a points total for two, and a points total with its wins for more.
func tieRecordText(tie scenarios.RivalTie, matches int) string {
	switch matches {
	case 0:
		return ""
	case 1:
		return map[int]string{3: "wins", 1: "draws", 0: "loses"}[tie.Points]
	case 2:
		switch tie.Points {
		case 6:
			return "wins both matches"
		case 0:
			return "loses both matches"
		case 4:
			return "wins one, draws one"
		case 3:
			return "wins one, loses one"
		case 2:
			return "draws both"
		case 1:
			return "draws one, loses one"
		}
		return "earns " + pointsText(tie.Points)
	}
	if tie.Points == 0 {
		return "loses " + allMatches(matches)
	}
	wins := "wins"
	if tie.Wins == 1 {
		wins = "win"
	}
	return fmt.Sprintf("earns %s with %d %s", pointsText(tie.Points), tie.Wins, wins)
}

func pointsText(points int) string {
	if points == 1 {
		return "1 point"
	}
	return fmt.Sprintf("%d points", points)
}

func allMatches(n int) string {
	if n == 2 {
		return "both matches"
	}
	return fmt.Sprintf("all %d matches", n)
}

// rivalRowKey identifies a row's rival needs, so own results with the same
// needs merge under one heading. The prefix keeps it apart from clause
// groups' keys.
func rivalRowKey(row scenarios.RivalRow) string {
	key, _ := json.Marshal(struct {
		Need   int
		Rivals []scenarios.RivalNeed
	}{row.Need, row.Rivals})
	return "rivals:" + string(key)
}

func ownInKickoffOrder(own []scenarios.FixtureCondition, games map[string]cache.Game) []scenarios.FixtureCondition {
	out := slices.Clone(own)
	sort.SliceStable(out, func(i, j int) bool {
		return fixtureBefore(out[i].GameID, out[j].GameID, games)
	})
	return out
}

// ownResultsKey orders rows from wins through draws to losses, match by
// match in kickoff order.
func ownResultsKey(own []scenarios.FixtureCondition, teamID string, games map[string]cache.Game) string {
	key := ""
	for _, condition := range own {
		points := scenarioPoints(slices.Index(scenarioOutcomes, condition.AllowedOutcomes[0]), teamID, games[condition.GameID])
		key += map[int]string{3: "0", 1: "1", 0: "2"}[points]
	}
	return key
}

// clausesForOwn keeps the clauses that allow a row's own results and pins
// each own match to that result, so they group as exactly that row.
func clausesForOwn(clauses []scenarios.Clause, own []scenarios.FixtureCondition) []scenarios.Clause {
	pinned := map[string]clinching.Outcome{}
	for _, condition := range own {
		pinned[condition.GameID] = condition.AllowedOutcomes[0]
	}
	out := []scenarios.Clause{}
	for _, clause := range clauses {
		allowed := true
		conditions := slices.Clone(own)
		for _, condition := range clause.Conditions {
			outcome, isOwn := pinned[condition.GameID]
			if !isOwn {
				conditions = append(conditions, condition)
				continue
			}
			allowed = allowed && slices.Contains(condition.AllowedOutcomes, outcome)
		}
		if allowed {
			clause.Conditions = conditions
			out = append(out, clause)
		}
	}
	return out
}
