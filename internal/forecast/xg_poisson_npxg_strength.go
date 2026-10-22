package forecast

import (
	"fmt"
	"math"
)

const npxgStrengthScheduleLoadID = "xg-poisson-npxg-strength-v1"

// NewNPXGStrengthScheduleLoadV1 rates teams on non-penalty xG while keeping
// the league's all-shots home and away scoring rates and the schedule-load
// adjustment.
func NewNPXGStrengthScheduleLoadV1() Model { return npxgStrengthScheduleLoadV1{} }

// npxgStrengthScheduleLoadV1 separates the strength signal from the scoring
// level. Team attack and defense multipliers come from non-penalty xG,
// measured against the league npxG mean so they stay centred on one. The
// multipliers then scale the all-shots league venue rates, so simulated
// scorelines still include the league's usual penalty goals.
type npxgStrengthScheduleLoadV1 struct{}

func (npxgStrengthScheduleLoadV1) Info() Info {
	return Info{
		ID:          npxgStrengthScheduleLoadID,
		Name:        "xG Poisson (npxG strength, schedule load)",
		Description: "The schedule-load xG Poisson model with team strengths rated on non-penalty xG and league scoring rates kept on all-shots xG.",
		Inputs:      "Available ASA team-model xG and penalty splits, current-season fixture kickoffs, and the two previous regular seasons for league home and away xG and npxG rates.",
		Assumptions: "Penalty awards say little about a team's strength, but the league's usual penalty goals still occur. Matches without an observed penalty split are left out of team strengths.",
	}
}

func (npxgStrengthScheduleLoadV1) Fit(input FitInput) (Predictor, error) {
	venueBase, err := fitXGPoissonHomeTwoSeasons(input)
	if err != nil {
		return nil, err
	}
	strengthBase, err := fitXGPoissonHomeTwoSeasons(nonPenaltyInput(input))
	if err != nil {
		return nil, err
	}
	venue, venueOK := venueBase.(xgPredictor)
	strength, strengthOK := strengthBase.(xgPredictor)
	if !venueOK || !strengthOK {
		return nil, fmt.Errorf("npxG-strength base predictors have types %T and %T", venueBase, strengthBase)
	}
	material := append([]byte("npxg-strength-v1"), venue.material...)
	material = append(material, strength.material...)
	combined := xgPredictor{
		teams:      strength.teams,
		homeRate:   venue.homeRate,
		awayRate:   venue.awayRate,
		leagueRate: strength.leagueRate,
		material:   material,
	}
	return withScheduleLoad(combined, input.Games)
}

// nonPenaltyInput removes penalty xG from every current and historical
// observation and leaves out observations without a split. The persisted venue
// aggregate is replaced by its npxG totals; without them, venue rates are
// pooled from the non-penalty historical fixtures instead.
func nonPenaltyInput(input FitInput) FitInput {
	input.XGoals = nonPenaltyXGoals(input.XGoals)
	input.HistoricalXGoals = nonPenaltyXGoals(input.HistoricalXGoals)
	seasons := make([]HistoricalSeason, len(input.HistoricalSeasons))
	for i, season := range input.HistoricalSeasons {
		season.XGoals = nonPenaltyXGoals(season.XGoals)
		seasons[i] = season
	}
	input.HistoricalSeasons = seasons
	input.HistoricalVenue = VenueSample{
		XGMatches: input.HistoricalVenue.NPXGMatches,
		HomeXG:    input.HistoricalVenue.HomeNPXG,
		AwayXG:    input.HistoricalVenue.AwayNPXG,
	}
	return input
}

func nonPenaltyXGoals(source map[string]ExpectedGoals) map[string]ExpectedGoals {
	values := make(map[string]ExpectedGoals, len(source))
	for id, value := range source {
		if value.Penalties == nil {
			continue
		}
		values[id] = ExpectedGoals{
			GameID: value.GameID,
			Home:   math.Max(0, value.Home-value.Penalties.Home),
			Away:   math.Max(0, value.Away-value.Penalties.Away),
		}
	}
	return values
}
