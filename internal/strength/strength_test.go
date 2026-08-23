package strength

import (
	"math"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestCalculateWithScheduleLoadAdjustsRelativeDifficulty(t *testing.T) {
	start := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	teams := []standings.Team{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	games := []standings.Game{
		datedGame("b", "d", standings.CompletedStatus, 1, 1, start.Add(-20*24*time.Hour)),
		datedGame("a", "c", standings.CompletedStatus, 1, 1, start),
		datedGame("d", "a", standings.CompletedStatus, 1, 1, start.Add(4*24*time.Hour)),
		{ID: "target", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b", Kickoff: start.Add(8 * 24 * time.Hour)},
	}

	result, err := CalculateWithVenueSampleAndScheduleLoad(teams, games, VenueSample{})
	if err != nil {
		t.Fatal(err)
	}
	rows := byID(result.Rows)
	const congestion = 0.115
	assertFloat(t, rows["a"].VenueAdjustedOpponentPPG, 1)
	assertFloat(t, rows["a"].ScheduleAdjustedPPG, math.Exp(congestion))
	assertFloat(t, rows["b"].VenueAdjustedOpponentPPG, 1)
	assertFloat(t, rows["b"].ScheduleAdjustedPPG, math.Exp(-congestion))
	assertFloat(t, rows["a"].Fixtures[0].TeamCongestion, congestion)
	assertFloat(t, rows["a"].Fixtures[0].OpponentCongestion, 0)
}

func TestCalculateRawAndVenueAdjustedStrength(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}, {ID: "c", Name: "Charlie"}}
	games := []standings.Game{
		game("a", "b", standings.CompletedStatus, 2, 0), // a=3, b=0
		game("c", "a", standings.CompletedStatus, 1, 0), // c=3, a=0
		game("b", "c", standings.CompletedStatus, 1, 1), // b=1, c=1
		{ID: "a-home", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b"},
		{ID: "a-away", Status: RemainingStatus, HomeTeamID: "c", AwayTeamID: "a"},
	}

	result := Calculate(teams, games)
	if result.CompletedMatches != 3 || result.RemainingMatches != 2 {
		t.Fatalf("match counts = %d completed, %d remaining; want 3 and 2", result.CompletedMatches, result.RemainingMatches)
	}
	// Home PPG is (3+3+1)/3 and away PPG is (0+0+1)/3.
	assertFloat(t, result.HomePPG, 7.0/3.0)
	assertFloat(t, result.AwayPPG, 1.0/3.0)
	assertFloat(t, result.VenueGap, 2)

	rows := byID(result.Rows)
	assertFloat(t, rows["a"].RawOpponentPPG, 1.25)
	// a hosts b (0.5 PPG => -1 venue adjustment) and visits c (2 PPG => +1).
	assertFloat(t, rows["a"].VenueAdjustedOpponentPPG, 1.25)
	assertFloat(t, rows["a"].HomeOpponentPPG, 0.5)
	assertFloat(t, rows["a"].AwayOpponentPPG, 2)
	if rows["a"].RemainingHome != 1 || rows["a"].RemainingAway != 1 {
		t.Fatalf("a venue counts = %d home, %d away; want 1 and 1", rows["a"].RemainingHome, rows["a"].RemainingAway)
	}
	assertFloat(t, rows["b"].RawOpponentPPG, 1.5)
	assertFloat(t, rows["b"].VenueAdjustedOpponentPPG, 2.5)
	if result.Rows[0].Team.ID != "b" {
		t.Fatalf("first row = %q, want b as the hardest remaining schedule", result.Rows[0].Team.ID)
	}
}

func TestCalculatePoolsHistoricalVenuePointsOnly(t *testing.T) {
	teams := []standings.Team{{ID: "a"}, {ID: "b"}}
	games := []standings.Game{
		game("a", "b", standings.CompletedStatus, 0, 1),
		{ID: "future", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b"},
	}
	result := CalculateWithVenueSample(teams, games, VenueSample{Matches: 9, HomePoints: 27, AwayPoints: 0})
	if result.CompletedMatches != 10 {
		t.Fatalf("venue matches = %d, want 10", result.CompletedMatches)
	}
	assertFloat(t, result.HomePPG, 2.7)
	assertFloat(t, result.AwayPPG, .3)
	// Opponent PPG is still the current-season record (b won its only match).
	assertFloat(t, byID(result.Rows)["a"].RawOpponentPPG, 3)
}

func TestCalculateMarksRowsUnavailableWithoutOpponentHistory(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}}
	result := Calculate(teams, []standings.Game{{ID: "future", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "b"}})
	if result.Rows[0].Available || result.Rows[1].Available {
		t.Fatal("rows with no completed opponent history should be unavailable")
	}
	if result.AvailableRows != 0 || len(result.Rows[0].Fixtures) != 1 || result.Rows[0].Fixtures[0].Available {
		t.Fatal("unavailable fixture detail should preserve the fixture without a zero estimate")
	}
	if result.RemainingMatches != 1 {
		t.Fatalf("remaining matches = %d, want 1", result.RemainingMatches)
	}
}

func TestCalculateSuppressesBaselineForPartialCoverage(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}, {ID: "c", Name: "Charlie"}}
	games := []standings.Game{
		game("a", "b", standings.CompletedStatus, 1, 0),
		{ID: "a-c", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "c"},
		{ID: "b-c", Status: RemainingStatus, HomeTeamID: "b", AwayTeamID: "c"},
	}

	result := Calculate(teams, games)
	if result.AvailableRows != 1 || result.ComparableRows != 3 {
		t.Fatalf("coverage = %d available of %d comparable rows, want 1 of 3", result.AvailableRows, result.ComparableRows)
	}
	row := byID(result.Rows)["c"]
	if !row.Available || row.ScheduleLabel != "" || row.DeltaFromBaseline != 0 {
		t.Fatalf("available partial row = %+v, want estimate without league comparison", row)
	}
}

func TestCalculateBaselineAndScheduleLabels(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}, {ID: "c", Name: "Charlie"}, {ID: "d", Name: "Delta"}}
	games := []standings.Game{
		game("a", "b", standings.CompletedStatus, 3, 0), // a=3, b=0
		game("c", "d", standings.CompletedStatus, 1, 1), // c=1, d=1
		{ID: "a-easy", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "c"},
		{ID: "b-easy", Status: RemainingStatus, HomeTeamID: "b", AwayTeamID: "d"},
		{ID: "c-hard", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "c"},
		{ID: "d-easy", Status: RemainingStatus, HomeTeamID: "b", AwayTeamID: "d"},
	}
	result := Calculate(teams, games)
	if result.AvailableRows != 4 {
		t.Fatalf("available rows = %d, want 4", result.AvailableRows)
	}
	rows := byID(result.Rows)
	if rows["a"].ScheduleLabel != LabelEasier {
		t.Fatalf("a label = %q, want %q", rows["a"].ScheduleLabel, LabelEasier)
	}
	if rows["b"].ScheduleLabel != LabelEasier {
		t.Fatalf("b label = %q, want %q", rows["b"].ScheduleLabel, LabelEasier)
	}
	if rows["c"].ScheduleLabel != LabelHarder {
		t.Fatalf("c label = %q, want %q", rows["c"].ScheduleLabel, LabelHarder)
	}
	assertFloat(t, result.Baseline, 1.25)
	if len(rows["a"].Fixtures) != 2 || !rows["a"].Fixtures[0].Available {
		t.Fatal("available fixture detail missing")
	}
}

func TestLabelForDeltaTreatsThresholdAsNearAverage(t *testing.T) {
	for _, test := range []struct {
		delta float64
		want  string
	}{
		{QualitativeThreshold, LabelNearAverage},
		{-QualitativeThreshold, LabelNearAverage},
		{QualitativeThreshold + 0.004, LabelNearAverage},
		{-QualitativeThreshold - 0.004, LabelNearAverage},
		{QualitativeThreshold + 0.006, LabelHarder},
		{-QualitativeThreshold - 0.006, LabelEasier},
	} {
		if got := LabelForDelta(test.delta); got != test.want {
			t.Errorf("LabelForDelta(%f) = %q, want %q", test.delta, got, test.want)
		}
	}
}

func TestCalculateIgnoresUnknownAndNonRemainingGames(t *testing.T) {
	teams := []standings.Team{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Bravo"}}
	games := []standings.Game{
		game("a", "b", standings.CompletedStatus, 1, 0),
		game("a", "b", "Postponed", 9, 0),
		{ID: "unknown", Status: standings.CompletedStatus, HomeTeamID: "x", AwayTeamID: "b", HomeScore: intPtr(4), AwayScore: intPtr(0)},
		{ID: "future", Status: RemainingStatus, HomeTeamID: "a", AwayTeamID: "x"},
	}
	result := Calculate(teams, games)
	if result.CompletedMatches != 1 || result.RemainingMatches != 1 {
		t.Fatalf("match counts = %d completed, %d remaining; want 1 and 1", result.CompletedMatches, result.RemainingMatches)
	}
	if result.Rows[0].RemainingFixtures != 1 || result.Rows[1].RemainingFixtures != 0 {
		t.Fatalf("remaining fixtures = %d and %d; want 1 and 0", result.Rows[0].RemainingFixtures, result.Rows[1].RemainingFixtures)
	}
}

func game(home, away, status string, homeScore, awayScore int) standings.Game {
	return standings.Game{HomeTeamID: home, AwayTeamID: away, Status: status, HomeScore: intPtr(homeScore), AwayScore: intPtr(awayScore)}
}

func datedGame(home, away, status string, homeScore, awayScore int, kickoff time.Time) standings.Game {
	value := game(home, away, status, homeScore, awayScore)
	value.ID = home + "-" + away + "-" + kickoff.Format(time.RFC3339)
	value.Kickoff = kickoff
	return value
}

func intPtr(value int) *int { return &value }

func byID(rows []Row) map[string]Row {
	result := make(map[string]Row, len(rows))
	for _, row := range rows {
		result[row.Team.ID] = row
	}
	return result
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("value = %f, want %f", got, want)
	}
}
