package forecast

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func npxgTestInput(penalties map[string]*PenaltyXG) FitInput {
	start := time.Date(2026, 4, 4, 19, 0, 0, 0, time.UTC)
	xg := map[string]ExpectedGoals{
		"one": {GameID: "one", Home: 2.4, Away: 0.6},
		"two": {GameID: "two", Home: 1.1, Away: 1.3},
	}
	for id, split := range penalties {
		value := xg[id]
		value.Penalties = split
		xg[id] = value
	}
	return FitInput{
		Teams: []standings.Team{{ID: "a"}, {ID: "b"}},
		Games: []standings.Game{
			{ID: "one", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "b", Kickoff: start},
			{ID: "two", Status: standings.CompletedStatus, HomeTeamID: "b", AwayTeamID: "a", Kickoff: start.Add(7 * 24 * time.Hour)},
			{ID: "next", Status: fixtures.PreMatchStatus, HomeTeamID: "a", AwayTeamID: "b", Kickoff: start.Add(11 * 24 * time.Hour)},
		},
		XGoals: xg,
	}
}

func npxgRates(t *testing.T, model Model, input FitInput) poissonDistribution {
	t.Helper()
	predictor, err := model.Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := predictor.Distribution(input.Games[2])
	if err != nil {
		t.Fatal(err)
	}
	return distribution.(poissonDistribution)
}

func closeRates(a, b poissonDistribution) bool {
	return math.Abs(a.homeRate-b.homeRate) <= 1e-12 && math.Abs(a.awayRate-b.awayRate) <= 1e-12
}

func TestNPXGStrengthMatchesXGModelWithoutPenalties(t *testing.T) {
	input := npxgTestInput(map[string]*PenaltyXG{"one": {}, "two": {}})
	got := npxgRates(t, NewNPXGStrengthScheduleLoadV1(), input)
	want := npxgRates(t, NewXGPoissonScheduleLoadV1(), input)
	if !closeRates(got, want) {
		t.Fatalf("zero-penalty rates = %+v, want xG rates %+v", got, want)
	}
}

func TestNPXGStrengthKeepsAllShotsVenueRates(t *testing.T) {
	input := npxgTestInput(map[string]*PenaltyXG{"one": {Home: 0.79}, "two": {}})
	allShots, err := fitXGPoissonHomeTwoSeasons(input)
	if err != nil {
		t.Fatal(err)
	}
	predictor, err := NewNPXGStrengthScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	got := predictor.(scheduleLoadXGPredictor).base
	want := allShots.(xgPredictor)
	if got.homeRate != want.homeRate || got.awayRate != want.awayRate {
		t.Fatalf("venue rates = %.4f/%.4f, want all-shots %.4f/%.4f", got.homeRate, got.awayRate, want.homeRate, want.awayRate)
	}
	if got.leagueRate >= want.leagueRate {
		t.Fatalf("strength league rate %.4f is not below the all-shots rate %.4f", got.leagueRate, want.leagueRate)
	}
}

func TestNPXGStrengthRemovesPenaltyXGFromTeamStrength(t *testing.T) {
	withPenalty := npxgRates(t, NewNPXGStrengthScheduleLoadV1(), npxgTestInput(map[string]*PenaltyXG{"one": {Home: 0.79}, "two": {}}))
	allShots := npxgRates(t, NewXGPoissonScheduleLoadV1(), npxgTestInput(nil))
	if withPenalty.homeRate >= allShots.homeRate {
		t.Fatalf("penalty team's home rate %.4f is not below its all-shots rate %.4f", withPenalty.homeRate, allShots.homeRate)
	}
}

func TestNPXGStrengthLeavesOutMatchesWithoutSplit(t *testing.T) {
	input := npxgTestInput(map[string]*PenaltyXG{"two": {}})
	predictor, err := NewNPXGStrengthScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	got := predictor.(scheduleLoadXGPredictor).base.teams
	if got["a"].played != 1 || got["b"].played != 1 {
		t.Fatalf("team strengths = %+v, want only the split-covered match", got)
	}
}

func TestNPXGStrengthUsesPersistedNPXGVenueAggregate(t *testing.T) {
	input := npxgTestInput(map[string]*PenaltyXG{"one": {Home: 0.79}, "two": {}})
	input.HistoricalVenue = VenueSample{XGMatches: 10, HomeXG: 15, AwayXG: 11, NPXGMatches: 10, HomeNPXG: 14, AwayNPXG: 10}
	predictor, err := NewNPXGStrengthScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	got := predictor.(scheduleLoadXGPredictor).base

	allShots, err := fitXGPoissonHomeTwoSeasons(input)
	if err != nil {
		t.Fatal(err)
	}
	strengthInput := nonPenaltyInput(input)
	if strengthInput.HistoricalVenue != (VenueSample{XGMatches: 10, HomeXG: 14, AwayXG: 10}) {
		t.Fatalf("strength venue = %+v, want the npxG aggregate", strengthInput.HistoricalVenue)
	}
	strength, err := fitXGPoissonHomeTwoSeasons(strengthInput)
	if err != nil {
		t.Fatal(err)
	}
	if want := allShots.(xgPredictor); got.homeRate != want.homeRate || got.awayRate != want.awayRate {
		t.Fatalf("venue rates = %.4f/%.4f, want all-shots aggregate %.4f/%.4f", got.homeRate, got.awayRate, want.homeRate, want.awayRate)
	}
	if want := strength.(xgPredictor).leagueRate; got.leagueRate != want {
		t.Fatalf("strength league rate = %.4f, want npxG aggregate rate %.4f", got.leagueRate, want)
	}
}

func TestNPXGStrengthSeedDiffersFromXGModel(t *testing.T) {
	input := npxgTestInput(map[string]*PenaltyXG{"one": {}, "two": {}})
	npxg, err := NewNPXGStrengthScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	xg, err := NewXGPoissonScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(npxg.SeedMaterial(), xg.SeedMaterial()) {
		t.Fatal("npxG-strength and xG seed material are identical")
	}
}
