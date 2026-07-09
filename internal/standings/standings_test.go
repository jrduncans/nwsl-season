package standings

import (
	"reflect"
	"testing"
)

func TestCalculateAccumulatesHomeWinAwayWinAndDraw(t *testing.T) {
	teams := []Team{
		{ID: "alpha", Name: "Alpha FC"},
		{ID: "bravo", Name: "Bravo FC"},
		{ID: "charlie", Name: "Charlie FC"},
	}
	games := []Game{
		game("alpha", "bravo", CompletedStatus, 2, 0),
		game("bravo", "charlie", CompletedStatus, 1, 3),
		game("charlie", "alpha", CompletedStatus, 1, 1),
	}

	table := Calculate(teams, games, DefaultRules())

	assertRecord(t, table, "charlie", Record{
		Played: 2, Wins: 1, Draws: 1, GoalsFor: 4, GoalsAgainst: 2, Points: 4,
	})
	assertRecord(t, table, "alpha", Record{
		Played: 2, Wins: 1, Draws: 1, GoalsFor: 3, GoalsAgainst: 1, Points: 4,
	})
	assertRecord(t, table, "bravo", Record{
		Played: 2, Losses: 2, GoalsFor: 1, GoalsAgainst: 5,
	})
}

func TestCalculateIgnoresUnplayedPostponedAndIncompleteGames(t *testing.T) {
	teams := []Team{
		{ID: "alpha", Name: "Alpha FC"},
		{ID: "bravo", Name: "Bravo FC"},
	}
	games := []Game{
		game("alpha", "bravo", "PreMatch", 4, 0),
		game("alpha", "bravo", "Postponed", 5, 0),
		game("alpha", "bravo", "Abandoned", 6, 0),
		{HomeTeamID: "alpha", AwayTeamID: "bravo", Status: CompletedStatus, HomeScore: intPtr(1)},
		{HomeTeamID: "alpha", AwayTeamID: "bravo", Status: CompletedStatus, AwayScore: intPtr(1)},
	}

	table := Calculate(teams, games, DefaultRules())

	assertRecord(t, table, "alpha", Record{})
	assertRecord(t, table, "bravo", Record{})
}

func TestCalculateIncludesTeamsWithNoCompletedGames(t *testing.T) {
	teams := []Team{
		{ID: "alpha", Name: "Alpha FC"},
		{ID: "bravo", Name: "Bravo FC"},
		{ID: "charlie", Name: "Charlie FC"},
	}
	games := []Game{game("alpha", "bravo", CompletedStatus, 1, 0)}

	table := Calculate(teams, games, DefaultRules())

	assertRecord(t, table, "charlie", Record{})
}

func TestDefaultRulesOrderByPointsPerGame(t *testing.T) {
	teams := []Team{
		{ID: "ahead", Name: "Ahead FC"},
		{ID: "behind", Name: "Behind FC"},
		{ID: "sink-1", Name: "Sink 1"},
		{ID: "sink-2", Name: "Sink 2"},
		{ID: "sink-3", Name: "Sink 3"},
	}
	games := []Game{
		game("ahead", "sink-1", CompletedStatus, 1, 0),
		game("behind", "sink-2", CompletedStatus, 1, 0),
		game("behind", "sink-3", CompletedStatus, 1, 0),
		game("sink-1", "behind", CompletedStatus, 1, 0),
	}

	table := Calculate(teams, games, DefaultRules())

	assertBefore(t, table, "ahead", "behind")
}

func TestOfficialTotalRulesOrderByTotalPoints(t *testing.T) {
	teams := []Team{
		{ID: "ahead", Name: "Ahead FC"},
		{ID: "behind", Name: "Behind FC"},
		{ID: "sink-1", Name: "Sink 1"},
		{ID: "sink-2", Name: "Sink 2"},
		{ID: "sink-3", Name: "Sink 3"},
	}
	games := []Game{
		game("ahead", "sink-1", CompletedStatus, 1, 0),
		game("behind", "sink-2", CompletedStatus, 1, 0),
		game("behind", "sink-3", CompletedStatus, 1, 0),
		game("sink-1", "behind", CompletedStatus, 1, 0),
	}

	table := Calculate(teams, games, OfficialTotalRules())

	assertBefore(t, table, "behind", "ahead")
}

func TestPerGameRulesUsePerGameTiebreaks(t *testing.T) {
	teams := []Team{
		{ID: "efficient", Name: "Efficient FC"},
		{ID: "volume", Name: "Volume FC"},
		{ID: "sink-1", Name: "Sink 1"},
		{ID: "sink-2", Name: "Sink 2"},
		{ID: "sink-3", Name: "Sink 3"},
		{ID: "sink-4", Name: "Sink 4"},
		{ID: "sink-5", Name: "Sink 5"},
	}
	games := []Game{
		game("efficient", "sink-1", CompletedStatus, 2, 0),
		game("volume", "sink-2", CompletedStatus, 2, 0),
		game("volume", "sink-3", CompletedStatus, 2, 0),
		game("sink-4", "volume", CompletedStatus, 3, 0),
		game("sink-5", "efficient", CompletedStatus, 1, 1),
	}

	table := Calculate(teams, games, PerGameRules())

	assertBefore(t, table, "efficient", "volume")
}

func TestCalculateIsIndependentOfInputOrder(t *testing.T) {
	teams := []Team{
		{ID: "alpha", Name: "Alpha FC"},
		{ID: "bravo", Name: "Bravo FC"},
		{ID: "charlie", Name: "Charlie FC"},
	}
	forwardGames := []Game{
		game("alpha", "bravo", CompletedStatus, 2, 0),
		game("charlie", "alpha", CompletedStatus, 1, 1),
		game("bravo", "charlie", CompletedStatus, 1, 3),
	}
	reversedTeams := []Team{teams[2], teams[1], teams[0]}
	reversedGames := []Game{forwardGames[2], forwardGames[1], forwardGames[0]}

	forward := Calculate(teams, forwardGames, DefaultRules())
	reversed := Calculate(reversedTeams, reversedGames, DefaultRules())

	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("tables differ by input order\nforward:  %+v\nreversed: %+v", forward, reversed)
	}
}

func TestCalculateOrdersEqualPointsByDefaultTiebreaks(t *testing.T) {
	teams := []Team{
		{ID: "sink-1", Name: "Sink 1"},
		{ID: "sink-2", Name: "Sink 2"},
		{ID: "sink-3", Name: "Sink 3"},
		{ID: "sink-4", Name: "Sink 4"},
		{ID: "sink-5", Name: "Sink 5"},
		{ID: "sink-6", Name: "Sink 6"},
		{ID: "delta", Name: "Draws FC"},
		{ID: "plus", Name: "Plus FC"},
		{ID: "goals", Name: "Goals FC"},
		{ID: "name-b", Name: "Name B"},
		{ID: "name-a", Name: "Name A"},
		{ID: "same-b", Name: "Same Name"},
		{ID: "same-a", Name: "Same Name"},
	}
	games := []Game{
		game("plus", "sink-1", CompletedStatus, 2, 0),
		game("sink-1", "plus", CompletedStatus, 1, 0),
		game("goals", "sink-2", CompletedStatus, 3, 1),
		game("sink-2", "goals", CompletedStatus, 2, 0),
		game("name-b", "sink-3", CompletedStatus, 1, 0),
		game("sink-3", "name-b", CompletedStatus, 1, 0),
		game("name-a", "sink-4", CompletedStatus, 1, 0),
		game("sink-4", "name-a", CompletedStatus, 1, 0),
		game("same-b", "sink-5", CompletedStatus, 1, 0),
		game("sink-5", "same-b", CompletedStatus, 1, 0),
		game("same-a", "sink-6", CompletedStatus, 1, 0),
		game("sink-6", "same-a", CompletedStatus, 1, 0),
	}

	table := Calculate(teams, games, DefaultRules())

	assertBefore(t, table, "plus", "goals")
	assertBefore(t, table, "goals", "name-a")
	assertBefore(t, table, "name-a", "name-b")
	assertBefore(t, table, "same-a", "same-b")
}

func TestCalculateUsesWinsBeforeGoalsScored(t *testing.T) {
	teams := []Team{
		{ID: "wins", Name: "Wins FC"},
		{ID: "draws", Name: "Draws FC"},
		{ID: "sink-1", Name: "Sink 1"},
		{ID: "sink-2", Name: "Sink 2"},
		{ID: "sink-3", Name: "Sink 3"},
		{ID: "sink-4", Name: "Sink 4"},
		{ID: "sink-5", Name: "Sink 5"},
	}
	games := []Game{
		game("wins", "sink-1", CompletedStatus, 2, 0),
		game("sink-2", "wins", CompletedStatus, 2, 0),
		game("draws", "sink-3", CompletedStatus, 1, 1),
		game("sink-4", "draws", CompletedStatus, 1, 1),
		game("draws", "sink-5", CompletedStatus, 0, 0),
	}

	table := Calculate(teams, games, DefaultRules())

	assertBefore(t, table, "wins", "draws")
}

func TestCalculateUsesHeadToHeadPointsAfterOverallGoalsScored(t *testing.T) {
	teams := []Team{
		{ID: "alpha", Name: "Alpha FC"},
		{ID: "bravo", Name: "Bravo FC"},
		{ID: "sink", Name: "Sink FC"},
	}
	games := []Game{
		game("alpha", "bravo", CompletedStatus, 1, 0),
		game("bravo", "sink", CompletedStatus, 1, 0),
		game("sink", "alpha", CompletedStatus, 1, 0),
	}

	table := Calculate(teams, games, DefaultRules())

	assertBefore(t, table, "alpha", "bravo")
}

func TestCalculateMarksDisciplinaryTiebreakAsUndetermined(t *testing.T) {
	teams := []Team{
		{ID: "alpha", Name: "Alpha FC"},
		{ID: "bravo", Name: "Bravo FC"},
		{ID: "sink-1", Name: "Sink 1"},
		{ID: "sink-2", Name: "Sink 2"},
		{ID: "sink-3", Name: "Sink 3"},
		{ID: "sink-4", Name: "Sink 4"},
	}
	games := []Game{
		game("alpha", "sink-1", CompletedStatus, 1, 0),
		game("sink-2", "alpha", CompletedStatus, 1, 0),
		game("bravo", "sink-3", CompletedStatus, 1, 0),
		game("sink-4", "bravo", CompletedStatus, 1, 0),
	}

	table := Calculate(teams, games, DefaultRules())

	alpha := findRow(t, table, "alpha")
	if !alpha.TieBreak.Undetermined {
		t.Fatalf("alpha tiebreak = %+v, want undetermined", alpha.TieBreak)
	}
	if alpha.TieBreak.Rule != "least disciplinary points" {
		t.Fatalf("alpha tiebreak rule = %q, want least disciplinary points", alpha.TieBreak.Rule)
	}
	if !reflect.DeepEqual(alpha.TieBreak.TiedTeamIDs, []string{"alpha", "bravo"}) {
		t.Fatalf("alpha tied team IDs = %+v, want alpha/bravo", alpha.TieBreak.TiedTeamIDs)
	}
}

func game(homeID, awayID, status string, homeScore, awayScore int) Game {
	return Game{
		HomeTeamID: homeID,
		AwayTeamID: awayID,
		Status:     status,
		HomeScore:  intPtr(homeScore),
		AwayScore:  intPtr(awayScore),
	}
}

func intPtr(value int) *int {
	return &value
}

func findRow(t *testing.T, table []TableRow, teamID string) TableRow {
	t.Helper()
	for _, row := range table {
		if row.Team.ID == teamID {
			return row
		}
	}
	t.Fatalf("team %q not found in table %+v", teamID, table)
	return TableRow{}
}

func assertRecord(t *testing.T, table []TableRow, teamID string, want Record) {
	t.Helper()
	for _, row := range table {
		if row.Team.ID == teamID {
			if row.Record != want {
				t.Fatalf("%s record = %+v, want %+v", teamID, row.Record, want)
			}
			return
		}
	}
	t.Fatalf("team %q not found in table %+v", teamID, table)
}

func teamIDs(table []TableRow) []string {
	ids := make([]string, 0, len(table))
	for _, row := range table {
		ids = append(ids, row.Team.ID)
	}
	return ids
}

func assertBefore(t *testing.T, table []TableRow, beforeID, afterID string) {
	t.Helper()
	positions := make(map[string]int, len(table))
	for i, row := range table {
		positions[row.Team.ID] = i
	}
	before, ok := positions[beforeID]
	if !ok {
		t.Fatalf("team %q not found in table %+v", beforeID, table)
	}
	after, ok := positions[afterID]
	if !ok {
		t.Fatalf("team %q not found in table %+v", afterID, table)
	}
	if before >= after {
		t.Fatalf("%s appears at %d, want before %s at %d in %+v", beforeID, before, afterID, after, teamIDs(table))
	}
}
