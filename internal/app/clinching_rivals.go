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
	Parts []scenarioRequirementPart
	Ties  []clinchingRivalTieView
}

// clinchingRivalTieView is one level-on-points record. Condition is nil when
// the team finishes ahead whatever the scores.
type clinchingRivalTieView struct {
	Lead        string
	Condition   *scenarioRequirement
	Alternative bool
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
		for _, rival := range rivals {
			view.Rivals = append(view.Rivals, rivalView(rival, teams, games))
		}
		switch {
		case row.Need == 0:
		case len(row.Rivals) == 1:
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
	}
	return views
}

// rivalView words one rival's need. The rival's results come first ("SD
// takes 3 points or fewer (vs BOS and at BAY)"), then each level record and
// its condition ("or takes 4 points if NJY gains 1+ goal difference on SD").
func rivalView(need scenarios.RivalNeed, teams map[string]string, games map[string]cache.Game) clinchingRivalView {
	n := len(need.GameIDs)
	fixtures := noHelpFixtureText(clinching.NoHelpPath{FixtureIDs: need.GameIDs}, need.RivalID, games, teams)
	base := ""
	switch limit := need.MaxPoints; {
	case limit < 0:
	case n == 1 && limit == 0:
		base = "loses " + fixtures
	case n == 1:
		base = "draws or loses " + fixtures
	case n == 2 && limit >= 4:
		base = "doesn’t win both matches (" + fixtures + ")"
	case limit == 0:
		base = "loses " + allMatches(n) + " (" + fixtures + ")"
	default:
		base = fmt.Sprintf("takes %s or fewer (%s)", pointsText(limit), fixtures)
	}
	view := clinchingRivalView{}
	if n > 0 {
		view.Parts = []scenarioRequirementPart{{Team: &scenarioTeamToken{Code: teams[need.RivalID], LogoURL: clubLogoURL(need.RivalID)}}}
	}
	if base != "" {
		view.Parts = append(view.Parts, scenarioRequirementPart{Text: base})
	}
	stated := base != ""
	for _, tie := range need.Ties {
		record := tieRecordText(tie, n)
		switch {
		case stated:
		case n == 1:
			record += " " + fixtures
		case n > 1:
			record += " (" + fixtures + ")"
		}
		lead := record
		if stated {
			lead = "or " + record
		}
		item := clinchingRivalTieView{Lead: lead, Alternative: stated}
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
		}
		return "takes " + pointsText(tie.Points)
	}
	if tie.Points == 0 {
		return "loses " + allMatches(matches)
	}
	wins := "wins"
	if tie.Wins == 1 {
		wins = "win"
	}
	return fmt.Sprintf("takes %s with %d %s", pointsText(tie.Points), tie.Wins, wins)
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
		a, b := games[out[i].GameID], games[out[j].GameID]
		if a.KickoffUTC != b.KickoffUTC {
			return a.KickoffUTC < b.KickoffUTC
		}
		return out[i].GameID < out[j].GameID
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
