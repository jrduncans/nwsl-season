package cache

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"
)

func TestVenueSummaryNPXGFollowsPenaltyOnlyUpdates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "alpha", Name: "Alpha", RawJSON: "{}"}, {ASAID: "bravo", Name: "Bravo", RawJSON: "{}"}}
	games := []Game{
		cachedGame("game-1", "2025", "Regular Season", "FullTime", "alpha", "bravo", sql.NullInt64{Int64: 2, Valid: true}, sql.NullInt64{Int64: 1, Valid: true}),
		cachedGame("game-2", "2025", "Regular Season", "FullTime", "bravo", "alpha", sql.NullInt64{Int64: 0, Valid: true}, sql.NullInt64{Int64: 0, Valid: true}),
	}
	if _, err := db.ReplaceSeason(ctx, "2025", "Regular Season", teams, games, time.Now()); err != nil {
		t.Fatal(err)
	}
	xg := func(id, home, away string, homeXG, awayXG float64, split *PenaltySplit) GameXG {
		return GameXG{GameID: id, Availability: XGAvailable, HomeTeamID: home, AwayTeamID: away, HomeXG: sql.NullFloat64{Float64: homeXG, Valid: true}, AwayXG: sql.NullFloat64{Float64: awayXG, Valid: true}, RawJSON: "{}", Penalties: split}
	}
	values := []GameXG{
		xg("game-1", "alpha", "bravo", 1.7, .8, &PenaltySplit{HomeXG: .79, HomeGoals: 1, HomeShots: 1}),
		xg("game-2", "bravo", "alpha", .9, 1.2, nil),
	}
	if _, err := db.ReplaceGameXG(ctx, "2025", "Regular Season", games, values, time.Now()); err != nil {
		t.Fatal(err)
	}
	summaries, err := db.VenueSummaries(ctx, []string{"2025"}, "Regular Season")
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}
	if got := summaries[0]; got.XGMatches != 2 || got.NPXGMatches != 1 || math.Abs(got.HomeNPXG-.91) > 1e-9 || math.Abs(got.AwayNPXG-.8) > 1e-9 {
		t.Fatalf("partial-split summary = %+v, want one npxG match totalling .91/.8", got)
	}

	// A split that arrives later changes only penalty columns; the npxG
	// totals must still include it.
	values[1].Penalties = &PenaltySplit{AwayXG: .2}
	if _, err := db.ReplaceGameXG(ctx, "2025", "Regular Season", games, values, time.Now()); err != nil {
		t.Fatal(err)
	}
	summaries, err = db.VenueSummaries(ctx, []string{"2025"}, "Regular Season")
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}
	if got := summaries[0]; got.NPXGMatches != 2 || math.Abs(got.HomeNPXG-1.81) > 1e-9 || math.Abs(got.AwayNPXG-1.8) > 1e-9 {
		t.Fatalf("complete-split summary = %+v, want two npxG matches totalling 1.81/1.8", got)
	}
}
