package app

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

// Bound the number of complete scenarios rendered, never the conditions within one.
const seedingScenariosPerPage = 4

type seedingScenarioView struct {
	Number, Requirements int
	Conditions           []scenarioRequirement
	Preview              string
	Selected, Scores     bool
}

type seedingAlternativesView struct {
	Team                       scenarioTeamToken
	Finish, Heading, BackURL   string
	Number, Total, First, Last int
	Scenarios                  []seedingScenarioView
	Previous, Next             string
	HistoryNote                string
}

func seedingAlternativeURL(query url.Values, alternative int) string {
	q := cloneQuery(query)
	q.Del("action")
	q.Del("fixture")
	q.Del("outcome")
	q.Set("alternative", strconv.Itoa(alternative))
	// Old requirement-page links now show the complete scenario.
	q.Del("part")
	return "?" + q.Encode() + "#seeding-alternative"
}

func seedingAlternatives(result scenarios.SeedingResult, finish int, team string, query url.Values, labels map[string]string, games map[string]cache.Game, fixed map[string]uint8, history scenarioHistory) (*seedingAlternativesView, error) {
	number, err := strconv.Atoi(query.Get("alternative"))
	if err != nil || number < 1 {
		return nil, fmt.Errorf("invalid scenario")
	}
	var clauses []scenarios.Clause
	for _, seed := range result.Seeds {
		if seed.Seed == finish {
			clauses = seed.Clauses
		}
	}
	if number > len(clauses) {
		return nil, fmt.Errorf("scenario is unavailable")
	}
	start := (number - 1) / seedingScenariosPerPage * seedingScenariosPerPage
	end := min(start+seedingScenariosPerPage, len(clauses))
	back := cloneQuery(query)
	back.Del("alternative")
	back.Del("part")
	view := &seedingAlternativesView{Number: number, Total: len(clauses), First: start + 1, Last: end, BackURL: "?" + back.Encode() + "#seeding-conditions", Finish: "finishes " + ordinalRank(finish), Heading: "can finish " + ordinalRank(finish)}
	if finish == 0 {
		view.Finish = "misses the playoffs"
		view.Heading = "can miss the playoffs"
	}
	for i := start; i < end; i++ {
		clause := clauses[i]
		requirements := []scenarioRequirement{}
		for _, c := range clause.Conditions {
			if mask := outcomeMask(c.AllowedOutcomes); mask != 7 {
				requirements = append(requirements, scenarioRequirement{GameID: c.GameID, Mask: mask})
			}
		}
		for _, c := range clause.ScoreConditions {
			requirements = append(requirements, scenarioRequirement{Score: &c})
		}
		card := seedingScenarioView{Number: i + 1, Requirements: len(requirements), Selected: i+1 == number, Scores: len(clause.ScoreConditions) > 0}
		assumed := addAssumptions(fixed, requirements)
		for _, r := range requirementsOwnFirst(requirements, team, games) {
			perspective := ""
			if ownRequirement(r, team, games) {
				perspective = team
			}
			r.Text = requirementText(r, perspective, labels, games)
			r.Parts = requirementParts(r, perspective, labels, games)
			presentScore(&r, labels, games, assumed, history)
			card.Conditions = append(card.Conditions, r)
		}
		view.Scenarios = append(view.Scenarios, card)
	}
	// Lead with own results, then favor conditions that differ on this page.
	frequency := map[string]int{}
	for _, card := range view.Scenarios {
		seen := map[string]bool{}
		for _, r := range card.Conditions {
			if !seen[r.Text] {
				frequency[r.Text]++
				seen[r.Text] = true
			}
		}
	}
	for i := range view.Scenarios {
		card := &view.Scenarios[i]
		preview := []string{}
		chosen := map[int]bool{}
		for j, r := range card.Conditions {
			if ownRequirement(r, team, games) && len(preview) < 2 {
				preview = append(preview, r.Text)
				chosen[j] = true
			}
		}
		for _, differing := range []bool{true, false} {
			for j, r := range card.Conditions {
				if !chosen[j] && len(preview) < 2 && (!differing || frequency[r.Text] < len(view.Scenarios)) {
					preview = append(preview, r.Text)
					chosen[j] = true
				}
			}
		}
		card.Preview = strings.Join(preview, "; ")
		if len(card.Conditions) > len(preview) {
			card.Preview += "; …"
		}
	}

	if start > 0 {
		view.Previous = seedingAlternativeURL(query, start-seedingScenariosPerPage+1)
	}
	if end < len(clauses) {
		view.Next = seedingAlternativeURL(query, end+1)
	}
	view.HistoryNote = history.Note()
	return view, nil
}
