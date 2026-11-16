package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func breakdownFixture() FitInput {
	start := time.Date(2026, 3, 14, 19, 0, 0, 0, time.UTC)
	score := func(v int) *int { return &v }
	games := []standings.Game{
		{ID: "g1", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "b", HomeScore: score(2), AwayScore: score(0), Kickoff: start},
		{ID: "g2", Status: standings.CompletedStatus, HomeTeamID: "c", AwayTeamID: "a", HomeScore: score(1), AwayScore: score(1), Kickoff: start.Add(7 * 24 * time.Hour)},
		{ID: "g3", Status: standings.CompletedStatus, HomeTeamID: "b", AwayTeamID: "c", HomeScore: score(0), AwayScore: score(3), Kickoff: start.Add(10 * 24 * time.Hour)},
		// "a" plays a third match inside nine days with four days' rest.
		{ID: "g4", Status: standings.CompletedStatus, HomeTeamID: "a", AwayTeamID: "c", HomeScore: score(0), AwayScore: score(0), Kickoff: start.Add(11 * 24 * time.Hour)},
		{ID: "next", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "a", Kickoff: start.Add(15 * 24 * time.Hour)},
	}
	return FitInput{
		Teams: []standings.Team{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Games: games,
		XGoals: map[string]ExpectedGoals{
			"g1": {Home: 2.4, Away: 0.3}, "g2": {Home: 1.1, Away: 1.6},
			"g3": {Home: 0.5, Away: 2.2}, "g4": {Home: 0.9, Away: 0.8},
		},
		HistoricalVenue: VenueSample{XGMatches: 100, HomeXG: 145, AwayXG: 118},
	}
}

func TestDefaultModelBreakdownReproducesItsDistribution(t *testing.T) {
	input := breakdownFixture()
	predictor, err := Default().Model.Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	explainer, ok := predictor.(Explainer)
	if !ok {
		t.Fatalf("default predictor %T does not explain its rates", predictor)
	}
	game := input.Games[4]
	breakdown, err := explainer.Breakdown(game)
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := predictor.Distribution(game)
	if err != nil {
		t.Fatal(err)
	}
	rates := distribution.(poissonDistribution)
	for name, side := range map[string]struct {
		factors RateFactors
		rate    float64
	}{"home": {breakdown.Home, rates.homeRate}, "away": {breakdown.Away, rates.awayRate}} {
		f := side.factors
		if math.Abs(f.Venue*f.Attack*f.Defense*f.Load-f.Unclamped) > 1e-12 || math.Abs(f.Expected-side.rate) > 1e-12 {
			t.Errorf("%s factors %+v do not reproduce rate %v", name, f, side.rate)
		}
	}
	if !breakdown.LoadApplied || !breakdown.AwayLoad.ThirdWithinNine || breakdown.AwayLoad.Recovery != 1 || breakdown.HomeLoad.ThirdWithinNine {
		t.Errorf("schedule load = %+v", breakdown)
	}
	if breakdown.Home.Load <= 1 || math.Abs(breakdown.Home.Load*breakdown.Away.Load-1) > 1e-12 {
		t.Errorf("the congested away side should lift the home rate symmetrically: %v %v", breakdown.Home.Load, breakdown.Away.Load)
	}
}

func TestPostseasonBreakdownMatchesPostseasonDistribution(t *testing.T) {
	input := breakdownFixture()
	predictor, err := Default().Model.Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	explainer := predictor.(Explainer)
	game := standings.Game{ID: "playoff", Status: "PreMatch", HomeTeamID: "c", AwayTeamID: "a"}
	breakdown, err := explainer.PostseasonBreakdown(game, false)
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := predictor.(PostseasonPredictor).PostseasonDistribution(game)
	if err != nil {
		t.Fatal(err)
	}
	rates := distribution.(poissonDistribution)
	if breakdown.LoadApplied || breakdown.Home.Load != 1 || math.Abs(breakdown.Home.Expected-rates.homeRate) > 1e-12 || math.Abs(breakdown.Away.Expected-rates.awayRate) > 1e-12 {
		t.Fatalf("postseason breakdown %+v differs from %+v", breakdown, rates)
	}

	neutral, err := explainer.PostseasonBreakdown(game, true)
	if err != nil {
		t.Fatal(err)
	}
	reverse, _ := predictor.(PostseasonPredictor).PostseasonDistribution(standings.Game{ID: "reverse", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "c"})
	reversed := reverse.(poissonDistribution)
	if !neutral.Neutral || math.Abs(neutral.Home.Expected-(rates.homeRate+reversed.awayRate)/2) > 1e-12 || math.Abs(neutral.Away.Expected-(rates.awayRate+reversed.homeRate)/2) > 1e-12 {
		t.Fatalf("neutral breakdown %+v does not average both assignments", neutral)
	}
	if f := neutral.Home; math.Abs(f.Venue*f.Attack*f.Defense-f.Expected) > 1e-12 {
		t.Fatalf("unclamped neutral factors do not multiply to the mean rate: %+v", f)
	}
}

func TestBreakdownMarksTheRateBound(t *testing.T) {
	factors := rateFactors(1.4, 2.5, 2, 1)
	if !factors.Clamped || factors.Expected != maximumRate || factors.Unclamped != 1.4*2.5*2 {
		t.Fatalf("clamp not reported: %+v", factors)
	}
}
