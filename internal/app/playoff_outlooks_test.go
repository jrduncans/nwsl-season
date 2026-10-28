package app

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

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

func TestPlayoffCardsKeepOutlookWithPublishedTeamOrder(t *testing.T) {
	regular := resolvedHandoffSeason()
	for i := range regular.Teams {
		regular.Teams[i].Abbreviation = fmt.Sprintf("T%02d", i)
	}
	seeds := playoffSeedsFrom("2026", regular)
	for _, pair := range [][2]int{{1, 8}, {8, 1}} {
		t.Run(fmt.Sprint(pair), func(t *testing.T) {
			game := forecastPlayoffGame(seeds, "published", pair[0], pair[1], false)
			store := handoffStore{regular: regular, playoffs: cache.SeasonData{Teams: regular.Teams, Games: []cache.Game{game}}}
			odds := (&application{store: store}).playoffOutlooks(context.Background(), "2026", store.playoffs)[game.ASAID]
			groups := fixtureGroupsWithOutlooks(store.playoffs, time.UTC, nil)
			home, away := groups[0].Games[0].Home(), groups[0].Games[0].Away()
			for _, route := range []string{"/seasons/2026/playoffs", "/seasons/2026/playoffs/fixtures"} {
				body := handoffResponse(t, store, route)
				selector, teamSelector := ".fixtures", ".fixture-match-team"
				if route == "/seasons/2026/playoffs" {
					selector = `[data-bracket-node="quarterfinal-1"]`
					teamSelector = ".bracket-team-row"
				}
				rows := find(t, body, selector+" "+teamSelector)
				if len(rows) != 2 || !strings.Contains(text(rows[0]), home.Name) || !strings.Contains(text(rows[0]), odds.HomeAdvanceText) || !strings.Contains(text(rows[1]), away.Name) || !strings.Contains(text(rows[1]), odds.AwayAdvanceText) {
					t.Fatalf("%s lost the published team/advance order", route)
				}
				requireExact(t, body, ".fixture-outcome-labels .fixture-outcome-home", home.Code+" "+odds.HomeWinText)
				requireExact(t, body, ".fixture-outcome-labels .fixture-outcome-away", away.Code+" "+odds.AwayWinText)
				requireAttrContains(t, body, ".fixture-outcome-bar .fixture-outcome-home", "style", fmt.Sprint(odds.HomeWin))
				requireAttrContains(t, body, ".fixture-outcome-bar .fixture-outcome-away", "style", fmt.Sprint(odds.AwayWin))
				for i, seed := range pair {
					if got := findIn(t, rows[i], ".bracket-seed"); len(got) != 1 || text(got[0]) != fmt.Sprint(seed) {
						t.Fatalf("%s team %d has the wrong seed", route, i)
					}
				}
				if route == "/seasons/2026/playoffs/fixtures" {
					requireExact(t, body, ".fixture-result", "vs")
					requireExact(t, body, ".fixture-team-advance", "Advance "+odds.HomeAdvanceText, "Advance "+odds.AwayAdvanceText)
					forbidElements(t, body, ".bracket-match", ".fixture-playoff-teams")
				}
				requireElements(t, body, ".fixture-outcome-bar .fixture-outcome-draw")
				forbidElements(t, body, ".playoff-advance", ".fixture-outlook details")
			}
		})
	}
}

func TestCompletedPlayoffCardKeepsSeedsGoalsAndXGWithEachTeam(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	game := forecastPlayoffGame(seeds, "played", 8, 1, true)
	game.HomeScore.Int64, game.AwayScore.Int64 = 0, 2
	store := handoffStore{regular: regular, playoffs: cache.SeasonData{Teams: regular.Teams, Games: []cache.Game{game}, XGoals: []cache.GameXG{{
		GameID: game.ASAID, Availability: cache.XGAvailable,
		HomeXG: sql.NullFloat64{Float64: .55, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.25, Valid: true},
	}}}}
	body := handoffResponse(t, store, "/seasons/2026/playoffs")
	requireExact(t, body, ".bracket-match-result .bracket-loser .bracket-seed", "8")
	requireExact(t, body, ".bracket-match-result .bracket-loser .bracket-team-score", "0")
	requireExact(t, body, ".bracket-match-result .bracket-loser .bracket-team-xg", "0.55")
	requireExact(t, body, ".bracket-match-result .bracket-winner .bracket-seed", "1")
	requireExact(t, body, ".bracket-match-result .bracket-winner .bracket-team-score", "2")
	requireExact(t, body, ".bracket-match-result .bracket-winner .bracket-team-xg", "1.25")
	forbidElements(t, body, ".bracket-match-result .bracket-team-advance")
	// Omit a wholly unavailable column; retain either known side without inventing zero.
	for _, available := range [][2]bool{{false, false}, {true, false}, {false, true}} {
		store.playoffs.XGoals[0].HomeXG.Valid = available[0]
		store.playoffs.XGoals[0].AwayXG.Valid = available[1]
		body = handoffResponse(t, store, "/seasons/2026/playoffs")
		if !available[0] && !available[1] {
			forbidElements(t, body, ".bracket-match-result .bracket-team-xg")
			requireExact(t, body, ".bracket-match-result [role=columnheader]", "Team", "Goals")
			forbidText(t, body, ".bracket-match-result [role=columnheader]", "xG")
			continue
		}
		homeXG, awayXG := "—", "—"
		if available[0] {
			homeXG = "0.55"
		} else {
			awayXG = "1.25"
		}
		requireExact(t, body, ".bracket-match-result .bracket-loser .bracket-team-xg", homeXG)
		requireExact(t, body, ".bracket-match-result .bracket-winner .bracket-team-xg", awayXG)
	}
	body = handoffResponse(t, store, "/seasons/2026/playoffs/fixtures")
	rows := find(t, body, ".fixtures .fixture-match-team")
	if len(rows) != 2 {
		t.Fatal("completed Matches row lost its teams")
	}
	for i, seed := range []string{"8", "1"} {
		if got := findIn(t, rows[i], ".bracket-seed"); len(got) != 1 || text(got[0]) != seed {
			t.Fatal("completed Matches row lost its seeds or published sides")
		}
	}
}

func TestFlippedBracketCardReversesItsOutlookOnly(t *testing.T) {
	regular := resolvedHandoffSeason()
	codes := map[string]string{}
	for i := range regular.Teams {
		regular.Teams[i].Abbreviation = fmt.Sprintf("T%02d", i)
		codes[regular.Teams[i].ID] = scenarioTeamCode(regular.Teams[i])
	}
	seeds := playoffSeedsFrom("2026", regular)
	var games []cache.Game
	for i, pair := range [][2]int{{1, 8}, {4, 5}, {2, 7}, {3, 6}} {
		game := forecastPlayoffGame(seeds, fmt.Sprintf("qf-%d", i), pair[0], pair[1], true)
		game.KickoffUTC = fmt.Sprintf("2026-11-%02dT20:00:00Z", i+1)
		games = append(games, game)
	}
	// The Quarterfinal 2 winner hosts, but the Quarterfinal 1 winner belongs on top.
	semi := forecastPlayoffGame(seeds, "semi", 4, 1, false)
	semi.KickoffUTC = "2026-11-10T20:00:00Z"
	store := handoffStore{regular: regular, playoffs: cache.SeasonData{Teams: regular.Teams, Games: append(games, semi)}}
	odds := (&application{store: store}).playoffOutlooks(context.Background(), "2026", store.playoffs)["semi"]
	top, bottom := seeds.Seeds[1], seeds.Seeds[4]
	body := handoffResponse(t, store, "/seasons/2026/playoffs")
	const card = `[data-bracket-node="semifinal-1"]`
	rows := find(t, body, card+" .bracket-team-row")
	if len(rows) != 2 || text(findIn(t, rows[0], ".bracket-seed")[0]) != "1" || !strings.Contains(text(rows[0]), odds.AwayAdvanceText) || !strings.Contains(text(rows[1]), odds.HomeAdvanceText) {
		t.Fatal("flipped card lost its advance chances")
	}
	requireExact(t, body, card+" .fixture-outcome-labels .fixture-outcome-home", codes[top]+" "+odds.AwayWinText)
	requireExact(t, body, card+" .fixture-outcome-labels .fixture-outcome-away", codes[bottom]+" "+odds.HomeWinText)
	requireAttrContains(t, body, card+" .fixture-outcome-bar .fixture-outcome-home", "style", fmt.Sprint(odds.AwayWin))
	body = handoffResponse(t, store, "/seasons/2026/playoffs/fixtures")
	requireExact(t, body, ".fixture-team-advance", "Advance "+odds.HomeAdvanceText, "Advance "+odds.AwayAdvanceText)
	requireExact(t, body, ".fixture-outcome-labels .fixture-outcome-home", codes[bottom]+" "+odds.HomeWinText)
}

func TestBracketShootoutTalliesAndPlaceholderHeaders(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	game := forecastPlayoffGame(seeds, "shootout", 8, 1, true)
	game.AwayScore = game.HomeScore
	game.ExtraTime = sql.NullBool{Bool: true, Valid: true}
	game.Penalties = sql.NullBool{Bool: true, Valid: true}
	game.HomePenalties = sql.NullInt64{Int64: 5, Valid: true}
	game.AwayPenalties = sql.NullInt64{Int64: 4, Valid: true}
	store := handoffStore{regular: regular, playoffs: cache.SeasonData{Teams: regular.Teams, Games: []cache.Game{game}}}
	body := handoffResponse(t, store, "/seasons/2026/playoffs")
	requireExact(t, body, ".bracket-match-result .bracket-winner .bracket-team-score", "2 (5)")
	requireExact(t, body, ".bracket-match-result .bracket-loser .bracket-team-score", "2 (4)")
	requireAttrContains(t, body, ".bracket-match-result", "aria-label", "Match decided on penalties:", "5", "4")
	requireAttr(t, body, ".bracket-winner .bracket-team-penalties", "aria-label", "5 penalties scored")
	requireAttr(t, body, ".bracket-loser .bracket-team-penalties", "aria-label", "4 penalties scored")
	requireExact(t, body, ".bracket-facts span", "After extra time")
	forbidText(t, body, ".bracket-facts", "Penalties")
	forbidElements(t, body, ".bracket-match-result .bracket-team-xg", `[data-bracket-node="final"] .bracket-match-header`, `[data-bracket-node="semifinal-1"] .bracket-match-header`)
	requireExact(t, body, ".bracket-match-result [role=columnheader]", "Team", "Goals")
}

func TestPlayoffMatchesOmitUnresolvedSeeds(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	store := handoffStore{regular: regular, playoffs: cache.SeasonData{Teams: regular.Teams, Games: []cache.Game{forecastPlayoffGame(seeds, "published", 8, 1, false)}}}
	for _, incomplete := range []bool{false, true} {
		for i := range store.regular.Games {
			store.regular.Games[i].HomeScore.Int64, store.regular.Games[i].AwayScore.Int64 = 0, 0
		}
		if incomplete {
			store.regular.Games[0].Status = "PreMatch"
		}
		body := handoffResponse(t, store, "/seasons/2026/playoffs/fixtures")
		forbidElements(t, body, ".fixtures .bracket-seed")
		requireElements(t, body, ".fixtures .fixture-match-team")
	}
}

func TestBracketLabelsFeedersAndStylesWinner(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	entry, _ := competition.Lookup("2026", "Playoffs")
	page := seededBracketPageFor(seasonPage{}, *entry.BracketFormat, cache.SeasonData{}, seeds, "")
	if page.Rounds[1].Slots[0].Home.Name != "Winner of Quarterfinal 1" || page.Rounds[2].Slots[0].Home.Name != "Winner of Semifinal 1" {
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
	if semifinal.Home.ID != seeds.Seeds[1] || semifinal.Away.Name != "Winner of Quarterfinal 2" {
		t.Fatalf("semifinal entrants: home %+v, away %+v", semifinal.Home, semifinal.Away)
	}
}
