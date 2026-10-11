package app

import (
	"database/sql"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/forecast"
)

func TestForecastVenueSampleAddsNPXGOnlyWhenEverySplitIsPresent(t *testing.T) {
	data := cache.SeasonData{VenueHistory: []cache.VenueSummary{
		{Season: "2025", FixtureReady: true, XGReady: true, XGMatches: 10, HomeXG: 15, AwayXG: 11, NPXGMatches: 10, HomeNPXG: 14, AwayNPXG: 10},
		{Season: "2024", FixtureReady: true, XGReady: true, XGMatches: 8, HomeXG: 12, AwayXG: 9, NPXGMatches: 8, HomeNPXG: 11, AwayNPXG: 8.5},
	}}
	got := forecastVenueSample(data)
	if got.XGMatches != 18 || got.NPXGMatches != 18 || got.HomeNPXG != 25 || got.AwayNPXG != 18.5 {
		t.Fatalf("complete venue sample = %+v", got)
	}

	data.VenueHistory[1].NPXGMatches = 7
	got = forecastVenueSample(data)
	if got.XGMatches != 18 || got.NPXGMatches != 0 || got.HomeNPXG != 0 || got.AwayNPXG != 0 {
		t.Fatalf("venue sample with a missing split = %+v, want xG only", got)
	}

	data.VenueHistory[1].NPXGMatches = 8
	data.VenueHistory[1].XGReady = false
	if got = forecastVenueSample(data); got.XGMatches != 0 || got.NPXGMatches != 0 {
		t.Fatalf("venue sample without xG readiness = %+v, want neither xG nor npxG", got)
	}
}

func TestForecastXGoalsCarriesPenaltySplits(t *testing.T) {
	valid := func(value float64) sql.NullFloat64 { return sql.NullFloat64{Float64: value, Valid: true} }
	data := cache.SeasonData{XGoals: []cache.GameXG{
		{GameID: "split", Availability: cache.XGAvailable, HomeXG: valid(1.7), AwayXG: valid(.8), Penalties: &cache.PenaltySplit{HomeXG: .79}},
		{GameID: "unsplit", Availability: cache.XGAvailable, HomeXG: valid(.9), AwayXG: valid(1.2)},
	}}
	got := forecastXGoals(data)
	if split := got["split"].Penalties; split == nil || *split != (forecast.PenaltyXG{Home: .79}) {
		t.Fatalf("split penalties = %+v", split)
	}
	if got["unsplit"].Penalties != nil {
		t.Fatalf("unsplit penalties = %+v, want nil", got["unsplit"].Penalties)
	}
}
