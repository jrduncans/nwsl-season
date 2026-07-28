package forecast

import (
	"math"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestStraightLinePaceDoesNotApplyHomeFieldAdjustment(t *testing.T) {
	model := NewStraightLinePaceV1()
	predictor, err := model.Fit(FitInput{Teams: []standings.Team{{ID: "a"}, {ID: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := predictor.Distribution(standings.Game{ID: "fixture", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	poisson, ok := distribution.(poissonDistribution)
	if !ok {
		t.Fatalf("distribution = %T, want poissonDistribution", distribution)
	}
	if math.Abs(poisson.homeRate-poisson.awayRate) > 1e-12 {
		t.Fatalf("rates = %.12f, %.12f; want no home-field adjustment", poisson.homeRate, poisson.awayRate)
	}
}
