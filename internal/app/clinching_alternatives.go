package app

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

// Four requirements per response bounds the DOM even for a long conjunction.
// Every alternative, including every score threshold, remains navigable.
const seedingDetailRequirements = 4

type seedingAlternativesView struct {
	Team                                     scenarioTeamToken
	Finish, FinishKey, BackURL               string
	Number, Total, First, Last, Requirements int
	Conditions                               []scenarioRequirement
	Previous, Next, PreviousPart, NextPart   string
	HistoryNote                              string
}

func seedingAlternativeURL(query url.Values, alternative, part int) string {
	q := cloneQuery(query)
	q.Del("action")
	q.Del("fixture")
	q.Del("outcome")
	q.Set("alternative", strconv.Itoa(alternative))
	q.Del("part")
	if part > 0 {
		q.Set("part", strconv.Itoa(part))
	}
	return "?" + q.Encode() + "#seeding-alternative"
}

func seedingAlternatives(result scenarios.SeedingResult, finish int, query url.Values, labels map[string]string, games map[string]cache.Game, fixed map[string]uint8, history scenarioHistory) (*seedingAlternativesView, error) {
	number, err := strconv.Atoi(query.Get("alternative"))
	if err != nil || number < 1 {
		return nil, fmt.Errorf("invalid alternative")
	}
	part := 0
	if query.Get("part") != "" {
		part, err = strconv.Atoi(query.Get("part"))
		if err != nil || part < 0 {
			return nil, fmt.Errorf("invalid condition page")
		}
	}
	var clauses []scenarios.Clause
	for _, seed := range result.Seeds {
		if seed.Seed == finish {
			clauses = seed.Clauses
		}
	}
	if number > len(clauses) {
		return nil, fmt.Errorf("alternative is unavailable")
	}
	clause := clauses[number-1]
	requirements := []scenarioRequirement{}
	for _, c := range clause.Conditions {
		if mask := outcomeMask(c.AllowedOutcomes); mask != 7 {
			requirements = append(requirements, scenarioRequirement{GameID: c.GameID, Mask: mask})
		}
	}
	for _, c := range clause.ScoreConditions {
		requirements = append(requirements, scenarioRequirement{Score: &c})
	}
	if part > (max(len(requirements), 1)-1)/seedingDetailRequirements {
		return nil, fmt.Errorf("condition page is unavailable")
	}
	start := part * seedingDetailRequirements
	end := min(start+seedingDetailRequirements, len(requirements))
	back := cloneQuery(query)
	back.Del("alternative")
	back.Del("part")
	view := &seedingAlternativesView{Number: number, Total: len(clauses), First: start + 1, Last: end, Requirements: len(requirements), BackURL: "?" + back.Encode() + "#seeding-conditions", Finish: "finishes " + ordinalRank(finish), FinishKey: seedingFinishKey(finish)}
	if finish == 0 {
		view.Finish = "misses the playoffs"
	}
	assumed := addAssumptions(fixed, requirements)
	for _, r := range requirements[start:end] {
		r.Text = requirementText(r, "", labels, games)
		r.Parts = requirementParts(r, "", labels, games)
		presentScore(&r, labels, games, assumed, history)
		view.Conditions = append(view.Conditions, r)
	}
	if number > 1 {
		view.Previous = seedingAlternativeURL(query, number-1, 0)
	}
	if number < len(clauses) {
		view.Next = seedingAlternativeURL(query, number+1, 0)
	}
	if part > 0 {
		view.PreviousPart = seedingAlternativeURL(query, number, part-1)
	}
	if end < len(requirements) {
		view.NextPart = seedingAlternativeURL(query, number, part+1)
	}
	view.HistoryNote = history.Note()
	return view, nil
}
