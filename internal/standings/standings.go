package standings

import "sort"

const CompletedStatus = "FullTime"

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
	Team   Team
	Record Record
}

// Rules contains the ordering policy for a standings table.
type Rules struct {
	Less func(a, b TableRow) bool
}

// DefaultRules returns the initial deterministic table order.
func DefaultRules() Rules {
	return Rules{Less: defaultLess}
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
		applyResult(&rows[home].Record, *game.HomeScore, *game.AwayScore)
		applyResult(&rows[away].Record, *game.AwayScore, *game.HomeScore)
	}

	less := rules.Less
	if less == nil {
		less = defaultLess
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return less(rows[i], rows[j])
	})
	return rows
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

func defaultLess(a, b TableRow) bool {
	if a.Record.Points != b.Record.Points {
		return a.Record.Points > b.Record.Points
	}
	if a.Record.GoalDifference() != b.Record.GoalDifference() {
		return a.Record.GoalDifference() > b.Record.GoalDifference()
	}
	if a.Record.GoalsFor != b.Record.GoalsFor {
		return a.Record.GoalsFor > b.Record.GoalsFor
	}
	if a.Team.Name != b.Team.Name {
		return a.Team.Name < b.Team.Name
	}
	return a.Team.ID < b.Team.ID
}
