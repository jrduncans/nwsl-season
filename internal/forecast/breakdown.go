package forecast

import (
	"fmt"
	"math"

	"github.com/jrduncans/nwsl-season/internal/scheduleload"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// RateFactors is the multiplicative path to one side's expected goals:
// Venue × Attack × Defense × Load, bounded to the model's scoring-rate range.
// Attack belongs to the scoring side and Defense to its opponent.
type RateFactors struct {
	Venue, Attack, Defense, Load float64
	// Unclamped is the product of the factors before the rate bound.
	Unclamped float64
	// Expected is the scoring rate the model samples from.
	Expected float64
	Clamped  bool
}

// Breakdown explains one fixture's two scoring rates.
type Breakdown struct {
	Home, Away RateFactors
	// LoadApplied reports whether the model adjusts this fixture for
	// schedule load; HomeLoad and AwayLoad are then each team's load entering it.
	LoadApplied        bool
	HomeLoad, AwayLoad scheduleload.Team
	Neutral            bool
}

// Explainer is implemented by predictors whose scoring rates decompose into
// venue, attack, defense and schedule-load factors.
type Explainer interface {
	// Breakdown explains the rates Distribution uses for a remaining fixture.
	Breakdown(standings.Game) (Breakdown, error)
	// PostseasonBreakdown explains the rates PostseasonDistribution uses. A
	// neutral fixture averages both home assignments, as the playoff outlook does.
	PostseasonBreakdown(game standings.Game, neutral bool) (Breakdown, error)
}

func rateFactors(venue, attack, defense, load float64) RateFactors {
	product := venue * attack * defense * load
	expected := clamp(product)
	return RateFactors{Venue: venue, Attack: attack, Defense: defense, Load: load, Unclamped: product, Expected: expected, Clamped: expected != product}
}

func (p xgPredictor) strengthsFor(game standings.Game) (homeAttack, homeDefense, awayAttack, awayDefense float64, err error) {
	if game.Status != "PreMatch" {
		return 0, 0, 0, 0, fmt.Errorf("fixture %q is not remaining", game.ID)
	}
	h, hok := p.teams[game.HomeTeamID]
	a, aok := p.teams[game.AwayTeamID]
	if !hok || !aok {
		return 0, 0, 0, 0, fmt.Errorf("fixture %q references an unknown team", game.ID)
	}
	homeAttack, homeDefense = xgStrengths(h, p.leagueRate)
	awayAttack, awayDefense = xgStrengths(a, p.leagueRate)
	return homeAttack, homeDefense, awayAttack, awayDefense, nil
}

func (p xgPredictor) Breakdown(game standings.Game) (Breakdown, error) {
	ha, hd, aa, ad, err := p.strengthsFor(game)
	if err != nil {
		return Breakdown{}, err
	}
	return Breakdown{
		Home: rateFactors(p.homeRate, ha, ad, 1),
		Away: rateFactors(p.awayRate, aa, hd, 1),
	}, nil
}

func (p xgPredictor) PostseasonBreakdown(game standings.Game, neutral bool) (Breakdown, error) {
	if !neutral {
		return p.Breakdown(game)
	}
	ha, hd, aa, ad, err := p.strengthsFor(game)
	if err != nil {
		return Breakdown{}, err
	}
	venue := (p.homeRate + p.awayRate) / 2
	side := func(attack, defense float64) RateFactors {
		factors := rateFactors(venue, attack, defense, 1)
		// The outlook averages the two assignments' clamped distributions.
		asHome, asAway := clamp(p.homeRate*attack*defense), clamp(p.awayRate*attack*defense)
		factors.Expected = (asHome + asAway) / 2
		factors.Clamped = asHome != p.homeRate*attack*defense || asAway != p.awayRate*attack*defense
		return factors
	}
	return Breakdown{Home: side(ha, ad), Away: side(aa, hd), Neutral: true}, nil
}

func (p scheduleLoadXGPredictor) Breakdown(game standings.Game) (Breakdown, error) {
	base, err := p.base.Breakdown(game)
	if err != nil {
		return Breakdown{}, err
	}
	load, ok := p.fixtures[game.ID]
	if !ok {
		return Breakdown{}, fmt.Errorf("fixture %q is absent from schedule-load context", game.ID)
	}
	adjustment := (congestion(load.away) - congestion(load.home)) / 2
	return Breakdown{
		Home:        rateFactors(base.Home.Venue, base.Home.Attack, base.Home.Defense, math.Exp(adjustment)),
		Away:        rateFactors(base.Away.Venue, base.Away.Attack, base.Away.Defense, math.Exp(-adjustment)),
		LoadApplied: true,
		HomeLoad:    scheduleload.Team{Recovery: load.home.recovery, ThirdWithinNine: load.home.third},
		AwayLoad:    scheduleload.Team{Recovery: load.away.recovery, ThirdWithinNine: load.away.third},
	}, nil
}

// PostseasonBreakdown follows PostseasonDistribution, which has no schedule
// load because a future playoff pairing has no known kickoff.
func (p scheduleLoadXGPredictor) PostseasonBreakdown(game standings.Game, neutral bool) (Breakdown, error) {
	return p.base.PostseasonBreakdown(game, neutral)
}
