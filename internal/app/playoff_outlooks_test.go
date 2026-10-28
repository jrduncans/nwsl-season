package app

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func TestPlayoffOutlooksUseRegularDataAndPublishedVenues(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	store := handoffStore{regular: regular}
	app := &application{store: store}
	expected := app.playoffOutlooks(context.Background(), "2026", store.playoffs)
	if len(expected) != 4 {
		t.Fatalf("expected pairings: %v", expected)
	}
	game := forecastPlayoffGame(seeds, "published", 8, 1, false)
	store.playoffs = cache.SeasonData{Games: []cache.Game{game}}
	app.store = store
	actual := app.playoffOutlooks(context.Background(), "2026", store.playoffs)
	if actual["published"] != actual["quarterfinal-1"] {
		t.Fatal("Matches and bracket outlooks differ")
	}
	predictor, err := forecast.Default().Model.Fit(forecast.FitInput{Teams: regular.Teams, Games: standingsGames(regular.Games), XGoals: forecastXGoals(regular), HistoricalVenue: forecastVenueSample(regular)})
	if err != nil {
		t.Fatal(err)
	}
	odds, err := simulation.PlayoffMatchOutlook(predictor, seeds.Seeds[8], seeds.Seeds[1], false)
	if err != nil || math.Abs(odds.HomeWin-actual["published"].HomeWin) > 1e-10 {
		t.Fatalf("published venue lost: %+v, %v", actual, err)
	}
	store.playoffs.Games[0].AwayTeamID = seeds.Seeds[7]
	app.store = store
	if len(app.playoffOutlooks(context.Background(), "2026", store.playoffs)) != 0 {
		t.Fatal("mismatched bracket has outlooks")
	}
	if len(app.playoffOutlooks(context.Background(), "2025", store.playoffs)) != 0 {
		t.Fatal("historical forecast enabled")
	}
}

func TestBracketNamesFeederTeamsAndStylesWinner(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	entry, _ := competition.Lookup("2026", "Playoffs")
	page := seededBracketPageFor(seasonPage{}, *entry.BracketFormat, cache.SeasonData{}, seeds, "")
	if page.Rounds[1].Slots[0].Home.Name == "TBD" || page.Rounds[2].Slots[0].Home.Name != "Semifinal 1 winner" {
		t.Fatalf("feeder labels: %+v", page.Rounds)
	}
	game := forecastPlayoffGame(seeds, "played", 1, 8, true)
	page = seededBracketPageFor(seasonPage{}, *entry.BracketFormat, cache.SeasonData{Games: []cache.Game{game}}, seeds, "")
	slot := page.Rounds[0].Slots[0]
	if !slot.HomeWinner || slot.AwayWinner || !slot.Completed || slot.Home.Seed != 1 || slot.Away.Seed != 8 {
		t.Fatalf("winner and seeds: %+v", slot)
	}
	// The decided quarterfinal advances its winner while the other side waits.
	semifinal := page.Rounds[1].Slots[0]
	if semifinal.Home.ID != seeds.Seeds[1] || !strings.Contains(semifinal.Away.Name, " or ") {
		t.Fatalf("semifinal entrants: home %+v, away %+v", semifinal.Home, semifinal.Away)
	}
}
