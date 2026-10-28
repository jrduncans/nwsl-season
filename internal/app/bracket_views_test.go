package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

func TestBracketByeSemifinalsConnectFeedersAndLabelByes(t *testing.T) {
	entry, _ := competition.Lookup("2023", "Playoffs")
	var data cache.SeasonData
	// Semifinal 2 kicks off first; the bye seed hosts each semifinal.
	for i, pair := range [][2]string{{"q-one", "out-one"}, {"q-two", "out-two"}, {"bye-two", "q-two"}, {"bye-one", "q-one"}, {"bye-two", "bye-one"}} {
		data.Games = append(data.Games, cache.Game{
			ASAID: fmt.Sprintf("game-%d", i), KickoffUTC: fmt.Sprintf("2023-11-%02dT20:00:00Z", i+1),
			Status: "FullTime", HomeTeamID: pair[0], AwayTeamID: pair[1], KnockoutGame: true,
			HomeScore: sql.NullInt64{Int64: 2, Valid: true}, AwayScore: sql.NullInt64{Valid: true},
		})
	}
	partial := data
	partial.Games = partial.Games[:2]
	page := bracketPageFor(seasonPage{}, *entry.BracketFormat, partial)
	for i, semi := range page.Rounds[1].Slots {
		if semi.HomeSource.Label != "First-round bye" || semi.AwaySource.ID != fmt.Sprintf("quarterfinal-%d", i+1) || semi.Away.Name != fmt.Sprintf("q-%s", []string{"one", "two"}[i]) {
			t.Fatalf("awaiting semifinal %d = %+v", i+1, semi)
		}
	}
	page = bracketPageFor(seasonPage{}, *entry.BracketFormat, data)
	first, second := page.Rounds[1].Slots[0], page.Rounds[1].Slots[1]
	if first.Home.ID != "bye-one" || first.AwaySource.ID != "quarterfinal-1" || second.Home.ID != "bye-two" || second.AwaySource.ID != "quarterfinal-2" || second.HomeSource.Label != "First-round bye" {
		t.Fatalf("bye paths: first=%+v second=%+v", first, second)
	}
	// The Semifinal 2 winner hosts the final, but rows follow the bracket path.
	if final := page.Rounds[2].Slots[0]; !final.Flipped || final.HomeSource.ID != "semifinal-1" || final.AwaySource.ID != "semifinal-2" || final.Home.ID != "bye-one" {
		t.Fatalf("final rows do not follow the semifinal path: %+v", final)
	}
}

func TestHistoricalCompletedBracketShowsActualSeeds(t *testing.T) {
	db, err := cache.Open(context.Background(), t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, season := range apptest.Scenario(t, apptest.ScenarioThresholds) {
		if season.Entry.Season == "2023" {
			apptest.SeedArchive(t, db, []cache.HistoricalSeason{season})
		}
	}
	response := httptest.NewRecorder()
	NewHandler(db).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2023/playoffs", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("historical bracket = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	const first = `[data-bracket-node="quarterfinal-1"]`
	requireExact(t, body, first+" .bracket-winner .bracket-seed", "4")
	requireExact(t, body, first+" .bracket-loser .bracket-seed", "5")
	// Seeds follow team identities through the neutral-site final, too.
	requireExact(t, body, `[data-bracket-node="final"] .bracket-loser .bracket-seed`, "4")
	requireExact(t, body, `[data-bracket-node="final"] .bracket-winner .bracket-seed`, "6")
	response = httptest.NewRecorder()
	NewHandler(db).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2023/playoffs/fixtures", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("historical Matches = %d: %s", response.Code, response.Body.String())
	}
	if got := len(find(t, response.Body.String(), ".fixtures .fixture-match-team .bracket-seed")); got != 10 {
		t.Fatalf("historical Matches seeds = %d, want one for each of 10 team appearances", got)
	}
	forbidElements(t, response.Body.String(), ".fixture-team-advance", ".bracket-match")
}

func TestBracketRowsFollowFeederPathOverPublishedHomeAway(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	entry, _ := competition.Lookup("2026", "Playoffs")
	var data cache.SeasonData
	for i, pair := range [][2]int{{1, 8}, {4, 5}, {2, 7}, {3, 6}, {4, 1}} {
		game := forecastPlayoffGame(seeds, fmt.Sprintf("game-%d", i), pair[0], pair[1], true)
		game.KickoffUTC = fmt.Sprintf("2026-11-%02dT20:00:00Z", i+1)
		data.Games = append(data.Games, game)
	}
	// The Quarterfinal 2 winner hosts Semifinal 1 and loses it on penalties.
	semi := &data.Games[4]
	semi.HomeScore, semi.AwayScore = sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 1, Valid: true}
	semi.ExtraTime = sql.NullBool{Bool: true, Valid: true}
	semi.Penalties = sql.NullBool{Bool: true, Valid: true}
	semi.HomePenalties, semi.AwayPenalties = sql.NullInt64{Int64: 3, Valid: true}, sql.NullInt64{Int64: 4, Valid: true}
	page := seededBracketPageFor(seasonPage{}, *entry.BracketFormat, data, seeds, "")
	got := page.Rounds[1].Slots[0]
	if !got.Flipped || got.HomeSource.ID != "quarterfinal-1" || got.AwaySource.ID != "quarterfinal-2" || got.Home.ID != seeds.Seeds[1] || got.Away.ID != seeds.Seeds[4] {
		t.Fatalf("rows do not follow the feeder path: %+v", got)
	}
	if !got.HomeWinner || got.AwayWinner || got.HomeTeam.ID != seeds.Seeds[1] || got.AwayTeam.ID != seeds.Seeds[4] {
		t.Fatalf("winner or outlook teams stayed with the published sides: %+v", got)
	}
	if g := got.Game; g.HomeScore != "1" || g.AwayScore != "1" || g.HomePenalties != "4" || g.AwayPenalties != "3" || g.Shootout != "Penalties 4–3" {
		t.Fatalf("game values stayed with the published sides: %+v", *g)
	}
	for _, quarterfinal := range page.Rounds[0].Slots {
		if quarterfinal.Flipped {
			t.Fatalf("a first-round card has no feeders to follow: %+v", quarterfinal)
		}
	}
}
