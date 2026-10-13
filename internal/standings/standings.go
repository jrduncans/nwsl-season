package standings

import (
	"sort"
	"strconv"
	"time"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

const CompletedStatus = fixtures.CompletedStatus

// Team is a standings-domain team value.
type Team struct {
	ID           string
	Name         string
	ShortName    string
	Abbreviation string
}

// Game is a standings-domain game value.
type Game struct {
	ID         string
	Status     string
	HomeTeamID string
	AwayTeamID string
	HomeScore  *int
	AwayScore  *int
	// Kickoff is optional for standings calculations. Forecast candidates that
	// use schedule context need it to be the UTC kickoff time.
	Kickoff time.Time
}

// Record contains accumulated match results for a team.
type Record struct {
	Played       int
	Wins         int
	Draws        int
	Losses       int
	GoalsFor     int
	GoalsAgainst int
	Points       int
}

// GoalDifference returns goals for minus goals against.
func (r Record) GoalDifference() int {
	return r.GoalsFor - r.GoalsAgainst
}

// TableRow combines a team with its accumulated record.
type TableRow struct {
	Team     Team
	Record   Record
	TieBreak TieBreakStatus
}

// Rules contains the ordering policy for a standings table.
type Rules struct {
	Order  Order
	legacy bool
	Less   func(a, b TableRow) bool
}

// Order identifies a built-in standings ordering policy.
type Order int

const (
	OrderOfficialTotal Order = iota
	OrderOfficialPerGame
)

// TieBreakStatus describes whether official rules fully determined a row's rank.
type TieBreakStatus struct {
	Undetermined bool
	Rule         string
	Reason       string
	TiedTeamIDs  []string
}

// OfficialTotalRules returns the 2026 NWSL regular-season table order by total points.
func OfficialTotalRules() Rules {
	return Rules{Order: OrderOfficialTotal}
}

// PerGameRules returns an in-season table order using official criteria per game played.
func PerGameRules() Rules {
	return Rules{Order: OrderOfficialPerGame}
}

// RulesForSeason selects the regular-season tiebreak policy. Before 2022,
// head-to-head came first; from 2022 onward, goal difference comes first.
// Per-game ordering is the application's normalized in-season presentation.
func RulesForSeason(season string, order Order) Rules {
	year, err := strconv.Atoi(season)
	return Rules{Order: order, legacy: err == nil && year >= 2013 && year <= 2021}
}

// DisplayName returns the most useful available club name.
func DisplayName(team Team) string {
	for _, value := range []string{team.Name, team.ShortName, team.Abbreviation, team.ID} {
		if value != "" {
			return value
		}
	}
	return "Unknown team"
}

// Calculate derives a table from teams and games.
func Calculate(teams []Team, games []Game, rules Rules) []TableRow {
	rows := make([]TableRow, len(teams))
	byTeamID := make(map[string]int, len(teams))
	for i, team := range teams {
		rows[i] = TableRow{Team: team}
		byTeamID[team.ID] = i
	}

	for _, game := range games {
		if game.Status != CompletedStatus || game.HomeScore == nil || game.AwayScore == nil {
			continue
		}
		home, ok := byTeamID[game.HomeTeamID]
		if !ok {
			continue
		}
		away, ok := byTeamID[game.AwayTeamID]
		if !ok {
			continue
		}
		// A self-fixture is invalid source data. Sync validation rejects it, but
		// ignore it here as a defense-in-depth measure for direct callers.
		if home == away {
			continue
		}
		applyResult(&rows[home].Record, *game.HomeScore, *game.AwayScore)
		applyResult(&rows[away].Record, *game.AwayScore, *game.HomeScore)
	}

	return Rank(rows, games, rules)
}

// Rank orders accumulated records, allowing callers to apply official points
// deductions before ranking. Games must include awarded scores for tiebreaks.
// The input rows are copied; unresolved official ties remain explicitly marked.
func Rank(rows []TableRow, games []Game, rules Rules) []TableRow {
	rows = append([]TableRow(nil), rows...)
	for i := range rows {
		rows[i].TieBreak = TieBreakStatus{}
	}
	less := rules.Less
	if less != nil {
		sort.SliceStable(rows, func(i, j int) bool {
			return less(rows[i], rows[j])
		})
		return rows
	}
	return officialOrder(rows, games, rules)
}

func applyResult(record *Record, goalsFor, goalsAgainst int) {
	record.Played++
	record.GoalsFor += goalsFor
	record.GoalsAgainst += goalsAgainst
	switch {
	case goalsFor > goalsAgainst:
		record.Wins++
		record.Points += 3
	case goalsFor < goalsAgainst:
		record.Losses++
	default:
		record.Draws++
		record.Points++
	}
}

type tieBreakCriterion struct {
	value func(TableRow, []TableRow, []Game) rankingValue
}

type rankingValue struct {
	numerator   int
	denominator int
}

func wholeValue(value int) rankingValue {
	return rankingValue{numerator: value, denominator: 1}
}

func perGameValue(value, played int) rankingValue {
	if played == 0 {
		return wholeValue(0)
	}
	return rankingValue{numerator: value, denominator: played}
}

func compareRankingValues(left, right rankingValue) int {
	switch {
	case left.denominator == 0 && right.denominator == 0:
		return 0
	case left.denominator == 0:
		return -1
	case right.denominator == 0:
		return 1
	}

	leftScaled := left.numerator * right.denominator
	rightScaled := right.numerator * left.denominator
	switch {
	case leftScaled > rightScaled:
		return 1
	case leftScaled < rightScaled:
		return -1
	default:
		return 0
	}
}

func officialOrder(rows []TableRow, games []Game, rules Rules) []TableRow {
	order := rules.Order
	ordered := make([]TableRow, len(rows))
	copy(ordered, rows)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := primaryRankingValue(ordered[i], order)
		right := primaryRankingValue(ordered[j], order)
		if comparison := compareRankingValues(left, right); comparison != 0 {
			return comparison > 0
		}
		return deterministicLess(ordered[i], ordered[j])
	})

	result := make([]TableRow, 0, len(ordered))
	for _, group := range splitByValue(ordered, func(row TableRow) rankingValue {
		return primaryRankingValue(row, order)
	}) {
		if len(group) == 1 {
			result = append(result, group...)
			continue
		}
		result = append(result, resolveTieGroup(group, games, criteriaForRules(rules), 0, rules.legacy)...)
	}
	return result
}

func primaryRankingValue(row TableRow, order Order) rankingValue {
	if order == OrderOfficialPerGame {
		return perGameValue(row.Record.Points, row.Record.Played)
	}
	return wholeValue(row.Record.Points)
}

func defaultTieBreakCriteria(order Order) []tieBreakCriterion {
	if order == OrderOfficialPerGame {
		return perGameTieBreakCriteria()
	}
	return totalTieBreakCriteria()
}

func totalTieBreakCriteria() []tieBreakCriterion {
	return []tieBreakCriterion{
		{
			value: func(row TableRow, _ []TableRow, _ []Game) rankingValue {
				return wholeValue(row.Record.GoalDifference())
			},
		},
		{
			value: func(row TableRow, _ []TableRow, _ []Game) rankingValue {
				return wholeValue(row.Record.Wins)
			},
		},
		{
			value: func(row TableRow, _ []TableRow, _ []Game) rankingValue {
				return wholeValue(row.Record.GoalsFor)
			},
		},
		{
			value: func(row TableRow, group []TableRow, games []Game) rankingValue {
				return wholeValue(headToHeadRecord(row.Team.ID, group, games).Points)
			},
		},
		{
			value: func(row TableRow, group []TableRow, games []Game) rankingValue {
				return wholeValue(headToHeadRecord(row.Team.ID, group, games).GoalsFor)
			},
		},
	}
}

func perGameTieBreakCriteria() []tieBreakCriterion {
	return []tieBreakCriterion{
		{
			value: func(row TableRow, _ []TableRow, _ []Game) rankingValue {
				return perGameValue(row.Record.GoalDifference(), row.Record.Played)
			},
		},
		{
			value: func(row TableRow, _ []TableRow, _ []Game) rankingValue {
				return perGameValue(row.Record.Wins, row.Record.Played)
			},
		},
		{
			value: func(row TableRow, _ []TableRow, _ []Game) rankingValue {
				return perGameValue(row.Record.GoalsFor, row.Record.Played)
			},
		},
		{
			value: func(row TableRow, group []TableRow, games []Game) rankingValue {
				record := headToHeadRecord(row.Team.ID, group, games)
				return perGameValue(record.Points, record.Played)
			},
		},
		{
			value: func(row TableRow, group []TableRow, games []Game) rankingValue {
				record := headToHeadRecord(row.Team.ID, group, games)
				return perGameValue(record.GoalsFor, record.Played)
			},
		},
	}
}

func resolveTieGroup(group []TableRow, games []Game, criteria []tieBreakCriterion, index int, legacy bool) []TableRow {
	if len(group) <= 1 {
		return group
	}
	if index >= len(criteria) {
		return markUndeterminedForRules(group, legacy)
	}

	criterion := criteria[index]
	sort.SliceStable(group, func(i, j int) bool {
		left := criterion.value(group[i], group, games)
		right := criterion.value(group[j], group, games)
		if comparison := compareRankingValues(left, right); comparison != 0 {
			return comparison > 0
		}
		return deterministicLess(group[i], group[j])
	})

	result := make([]TableRow, 0, len(group))
	for _, subgroup := range splitByValue(group, func(row TableRow) rankingValue {
		return criterion.value(row, group, games)
	}) {
		if len(subgroup) == 1 {
			result = append(result, subgroup...)
			continue
		}
		next := index + 1
		if legacy && len(group) > 2 && len(subgroup) == 2 {
			next = 0
		}
		result = append(result, resolveTieGroup(subgroup, games, criteria, next, legacy)...)
	}
	return result
}

func splitByValue(rows []TableRow, value func(TableRow) rankingValue) [][]TableRow {
	if len(rows) == 0 {
		return nil
	}
	groups := [][]TableRow{}
	start := 0
	for i := 1; i < len(rows); i++ {
		if compareRankingValues(value(rows[i]), value(rows[start])) == 0 {
			continue
		}
		groups = append(groups, rows[start:i])
		start = i
	}
	return append(groups, rows[start:])
}

func headToHeadRecord(teamID string, group []TableRow, games []Game) Record {
	groupTeamIDs := make(map[string]bool, len(group))
	for _, row := range group {
		groupTeamIDs[row.Team.ID] = true
	}

	var record Record
	for _, game := range games {
		if game.Status != CompletedStatus || game.HomeScore == nil || game.AwayScore == nil {
			continue
		}
		if !groupTeamIDs[game.HomeTeamID] || !groupTeamIDs[game.AwayTeamID] {
			continue
		}
		switch teamID {
		case game.HomeTeamID:
			applyResult(&record, *game.HomeScore, *game.AwayScore)
		case game.AwayTeamID:
			applyResult(&record, *game.AwayScore, *game.HomeScore)
		}
	}
	return record
}

func markUndeterminedForRules(group []TableRow, legacy bool) []TableRow {
	tiedTeamIDs := make([]string, 0, len(group))
	for _, row := range group {
		tiedTeamIDs = append(tiedTeamIDs, row.Team.ID)
	}
	sort.Strings(tiedTeamIDs)

	sort.SliceStable(group, func(i, j int) bool {
		return deterministicLess(group[i], group[j])
	})
	rule, reason := "least disciplinary points", "disciplinary points are not available from cached game data"
	if legacy {
		rule, reason = "coin toss or drawing of lots", "coin toss or drawing of lots is not available from cached game data"
	}
	for i := range group {
		group[i].TieBreak = TieBreakStatus{
			Undetermined: true,
			Rule:         rule,
			Reason:       reason,
			TiedTeamIDs:  append([]string(nil), tiedTeamIDs...),
		}
	}
	return group
}

func deterministicLess(a, b TableRow) bool {
	if a.Team.Name != b.Team.Name {
		return a.Team.Name < b.Team.Name
	}
	return a.Team.ID < b.Team.ID
}

func criteriaForRules(rules Rules) []tieBreakCriterion {
	if !rules.legacy {
		return defaultTieBreakCriteria(rules.Order)
	}
	criteria := []tieBreakCriterion{}
	for _, venue := range []string{"", "away", "home"} {
		criteria = append(criteria, tieBreakCriterion{value: func(row TableRow, group []TableRow, games []Game) rankingValue {
			record := venueRecord(row.Team.ID, group, games, venue)
			if len(group) > 2 || rules.Order == OrderOfficialPerGame {
				return perGameValue(record.Points, record.Played)
			}
			// For two clubs the win-loss record is wins minus losses. In away/home
			// subsets this differs from points when they have unequal match counts.
			return wholeValue(record.Wins - record.Losses)
		}})
		for _, goals := range []bool{false, true} {
			criteria = append(criteria, tieBreakCriterion{value: func(row TableRow, _ []TableRow, games []Game) rankingValue {
				record := row.Record
				if venue != "" {
					record = venueRecord(row.Team.ID, nil, games, venue)
				}
				value := record.GoalDifference()
				if goals {
					value = record.GoalsFor
				}
				if rules.Order == OrderOfficialPerGame {
					return perGameValue(value, record.Played)
				}
				return wholeValue(value)
			}})
		}
	}
	return criteria
}

// A nil group includes every opponent, for overall away/home criteria.
func venueRecord(teamID string, group []TableRow, games []Game, venue string) Record {
	ids := map[string]bool{}
	for _, row := range group {
		ids[row.Team.ID] = true
	}
	var record Record
	for _, game := range games {
		if game.Status != CompletedStatus || game.HomeScore == nil || game.AwayScore == nil || game.HomeTeamID == game.AwayTeamID {
			continue
		}
		if group != nil && (!ids[game.HomeTeamID] || !ids[game.AwayTeamID]) {
			continue
		}
		if teamID == game.HomeTeamID && venue != "away" {
			applyResult(&record, *game.HomeScore, *game.AwayScore)
		}
		if teamID == game.AwayTeamID && venue != "home" {
			applyResult(&record, *game.AwayScore, *game.HomeScore)
		}
	}
	return record
}
