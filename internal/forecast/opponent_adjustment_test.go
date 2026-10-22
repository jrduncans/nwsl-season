package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

var opponentStart = time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)

func opponentGame(id, home, away string, day int) standings.Game {
	return standings.Game{ID: id, Status: standings.CompletedStatus, HomeTeamID: home, AwayTeamID: away, Kickoff: opponentStart.Add(time.Duration(day) * 24 * time.Hour)}
}

func opponentInput() FitInput {
	return FitInput{
		Teams: []standings.Team{{ID: "alpha"}, {ID: "bravo"}, {ID: "charlie"}},
		Games: []standings.Game{
			opponentGame("g1", "alpha", "bravo", 0),
			// Bravo plays again after three days: full recovery pressure.
			opponentGame("g2", "bravo", "charlie", 3),
			opponentGame("g3", "charlie", "alpha", 20),
		},
		XGoals: map[string]ExpectedGoals{
			"g1": {Home: 2.0, Away: 1.0},
			"g2": {Home: 1.5, Away: 0.5},
			"g3": {Home: 0.5, Away: 40.0},
		},
		HistoricalVenue: VenueSample{XGMatches: 10, HomeXG: 15, AwayXG: 12},
	}
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("%s = %.12f, want %.12f", name, got, want)
	}
}

func TestModelFactorsMatchHandComputedFactors(t *testing.T) {
	adjustment, err := modelFactors(opponentInput())
	if err != nil {
		t.Fatal(err)
	}
	// Judging g2 leaves it out: the league sample is the 10 prior-season
	// matches plus g1 and g3, with 20 prior matches at 1.50 and 1.20.
	homeRate := (15 + 2.0 + 0.5 + 20*1.50) / (12 + 20)
	awayRate := (12 + 1.0 + 40.0 + 20*1.20) / (12 + 20)
	league := (homeRate + awayRate) / 2
	// Ratings shrink toward the league rate with eight prior matches. Charlie's
	// only other match is g3 (0.5 for, 40 against); Bravo's is g1 (1 for, 2 against).
	charlieAttack, charlieDefense := (0.5+8*league)/9/league, (40+8*league)/9/league
	bravoAttack, bravoDefense := (1.0+8*league)/9/league, (2.0+8*league)/9/league
	// Bravo, at home, has full recovery pressure (0.04); Charlie is rested.
	shift := math.Exp((0 - 0.04) / 2)
	clamped := func(rate float64) float64 { return math.Max(0.20, math.Min(4.50, rate)) }
	if homeRate*charlieDefense*shift <= 4.50 {
		t.Fatal("fixture no longer exercises the clamp")
	}
	got := adjustment.Fixtures["g2"]
	near(t, "home scored", got.Home.Scored, 4.50/league)
	near(t, "home allowed", got.Home.Allowed, clamped(awayRate*charlieAttack/shift)/league)
	near(t, "away scored", got.Away.Scored, clamped(awayRate*bravoDefense/shift)/league)
	near(t, "away allowed", got.Away.Allowed, clamped(homeRate*bravoAttack*shift)/league)
	if adjustment.XGMatches != 3 || adjustment.CompletedMatches != 3 {
		t.Fatalf("coverage = %d of %d, want 3 of 3", adjustment.XGMatches, adjustment.CompletedMatches)
	}
}

// An average team (no matches, so attack = defense = 1) in a rested future
// fixture gets the default model's rates; each factor must equal that rate
// over the league rate when the model is fitted without the judged match.
func TestModelFactorsMatchDefaultModelWithoutTheMatch(t *testing.T) {
	input := opponentInput()
	for index := range input.Games {
		input.Games[index].Kickoff = opponentStart.Add(time.Duration(index*10) * 24 * time.Hour)
	}
	adjustment, err := modelFactors(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, game := range input.Games {
		without := input
		without.Teams = append(append([]standings.Team(nil), input.Teams...), standings.Team{ID: "average"})
		without.XGoals = copyXGoals(input.XGoals)
		delete(without.XGoals, game.ID)
		later := opponentStart.Add(100 * 24 * time.Hour)
		phantoms := []standings.Game{
			{ID: "average-home", Status: fixtures.PreMatchStatus, HomeTeamID: "average", AwayTeamID: game.AwayTeamID, Kickoff: later},
			{ID: "average-away", Status: fixtures.PreMatchStatus, HomeTeamID: game.HomeTeamID, AwayTeamID: "average", Kickoff: later.Add(10 * 24 * time.Hour)},
		}
		without.Games = append(append([]standings.Game(nil), input.Games...), phantoms...)
		predictor, err := NewXGPoissonScheduleLoadV1().Fit(without)
		if err != nil {
			t.Fatal(err)
		}
		base := predictor.(scheduleLoadXGPredictor).base
		rates := func(fixture standings.Game) poissonDistribution {
			distribution, err := predictor.Distribution(fixture)
			if err != nil {
				t.Fatal(err)
			}
			return distribution.(poissonDistribution)
		}
		home, away := rates(phantoms[0]), rates(phantoms[1])
		got := adjustment.Fixtures[game.ID]
		near(t, game.ID+" home scored", got.Home.Scored, home.homeRate/base.leagueRate)
		near(t, game.ID+" home allowed", got.Home.Allowed, home.awayRate/base.leagueRate)
		near(t, game.ID+" away scored", got.Away.Scored, away.awayRate/base.leagueRate)
		near(t, game.ID+" away allowed", got.Away.Allowed, away.homeRate/base.leagueRate)
		// Expected points use the same rates: 3 × win + draw.
		homeOutcomes, awayOutcomes := home.Outcomes(), away.Outcomes()
		near(t, game.ID+" home expected points", got.Home.ExpectedPoints, 3*homeOutcomes.HomeWin+homeOutcomes.Draw)
		near(t, game.ID+" away expected points", got.Away.ExpectedPoints, 3*awayOutcomes.AwayWin+awayOutcomes.Draw)
	}
}

// Normalization then rescales the whole season, so this holds before it.
func TestModelFactorsLeaveTheJudgedMatchOut(t *testing.T) {
	input := opponentInput()
	before, err := modelFactors(input)
	if err != nil {
		t.Fatal(err)
	}
	input.XGoals["g2"] = ExpectedGoals{Home: 4.0, Away: 3.0}
	after, err := modelFactors(input)
	if err != nil {
		t.Fatal(err)
	}
	if before.Fixtures["g2"] != after.Fixtures["g2"] {
		t.Fatalf("g2 factors changed with its own xG: %+v, then %+v", before.Fixtures["g2"], after.Fixtures["g2"])
	}
	if before.Fixtures["g1"] == after.Fixtures["g1"] {
		t.Fatal("g1 factors ignored g2's xG")
	}
}

func TestOpponentAdjustmentsCoverageAndMissingKickoff(t *testing.T) {
	input := opponentInput()
	delete(input.XGoals, "g3")
	input.Games[1].Kickoff = time.Time{}
	input.Games = append(input.Games, standings.Game{ID: "later", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "charlie"})
	adjustment, err := OpponentAdjustments(input)
	if err != nil {
		t.Fatal(err)
	}
	if adjustment.XGMatches != 2 || adjustment.CompletedMatches != 3 || len(adjustment.Fixtures) != 3 {
		t.Fatalf("adjustment = %d of %d with %d fixtures, want 2 of 3 with 3", adjustment.XGMatches, adjustment.CompletedMatches, len(adjustment.Fixtures))
	}
	// Without a kickoff, g2 carries no schedule-load shift.
	rested := input
	rested.Games = append([]standings.Game(nil), input.Games...)
	rested.Games[1].Kickoff = opponentStart.Add(60 * 24 * time.Hour)
	want, err := OpponentAdjustments(rested)
	if err != nil {
		t.Fatal(err)
	}
	if adjustment.Fixtures["g2"] != want.Fixtures["g2"] {
		t.Fatalf("undated g2 = %+v, want the rested %+v", adjustment.Fixtures["g2"], want.Fixtures["g2"])
	}
}

func TestOpponentAdjustmentsNormalizeEachSeasonToAMeanOfOne(t *testing.T) {
	input := opponentInput()
	model, err := modelFactors(input)
	if err != nil {
		t.Fatal(err)
	}
	adjustment, err := OpponentAdjustments(input)
	if err != nil {
		t.Fatal(err)
	}
	var scored, allowed, modelScored float64
	for _, fixture := range adjustment.Fixtures {
		scored += fixture.Home.Scored + fixture.Away.Scored
		allowed += fixture.Home.Allowed + fixture.Away.Allowed
	}
	for _, fixture := range model.Fixtures {
		modelScored += fixture.Home.Scored + fixture.Away.Scored
	}
	appearances := float64(2 * len(adjustment.Fixtures))
	near(t, "mean scored factor", scored/appearances, 1)
	near(t, "mean allowed factor", allowed/appearances, 1)
	if math.Abs(modelScored/appearances-1) < 0.01 {
		t.Fatal("fixture no longer exercises normalization")
	}
	// One divisor per side keeps the model's ratios between matches.
	near(t, "scored ratio", adjustment.Fixtures["g1"].Home.Scored/adjustment.Fixtures["g2"].Away.Scored, model.Fixtures["g1"].Home.Scored/model.Fixtures["g2"].Away.Scored)
	near(t, "allowed ratio", adjustment.Fixtures["g3"].Away.Allowed/adjustment.Fixtures["g1"].Home.Allowed, model.Fixtures["g3"].Away.Allowed/model.Fixtures["g1"].Home.Allowed)
	if adjustment.XGMatches != model.XGMatches || adjustment.CompletedMatches != model.CompletedMatches {
		t.Fatal("normalization changed coverage")
	}
}

func TestOpponentAdjustmentsAverageExpectedPoints(t *testing.T) {
	input := opponentInput()
	model, err := modelFactors(input)
	if err != nil {
		t.Fatal(err)
	}
	adjustment, err := OpponentAdjustments(input)
	if err != nil {
		t.Fatal(err)
	}
	var points float64
	for id, fixture := range adjustment.Fixtures {
		// Normalizing rescales factors, never expected points.
		near(t, id+" home expected points", fixture.Home.ExpectedPoints, model.Fixtures[id].Home.ExpectedPoints)
		near(t, id+" away expected points", fixture.Away.ExpectedPoints, model.Fixtures[id].Away.ExpectedPoints)
		points += fixture.Home.ExpectedPoints + fixture.Away.ExpectedPoints
	}
	near(t, "average points", adjustment.AveragePoints, points/float64(2*len(adjustment.Fixtures)))
	// Bravo faces Charlie, whose 40 xG allowed in g3 makes it the weakest
	// defense, so an average team in Bravo's place expects more than average.
	if g2 := adjustment.Fixtures["g2"].Home.ExpectedPoints; g2 <= adjustment.AveragePoints || g2 >= 3 {
		t.Fatalf("g2 home expected points = %.3f, want above the average %.3f", g2, adjustment.AveragePoints)
	}
}

func TestOpponentAdjustmentsRejectUnknownTeams(t *testing.T) {
	input := opponentInput()
	input.Games[0].AwayTeamID = "delta"
	if _, err := OpponentAdjustments(input); err == nil {
		t.Fatal("unknown team accepted")
	}
}
