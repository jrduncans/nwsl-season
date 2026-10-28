package app

import (
	"database/sql"
	"fmt"
	"testing"

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
	if final := page.Rounds[2].Slots[0]; final.HomeSource.ID != "semifinal-2" || final.AwaySource.ID != "semifinal-1" {
		t.Fatalf("final follows the reversed semifinal sides: %+v", final)
	}
}

func TestBracketFixedPathsFollowPublishedHomeAwayReversal(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	entry, _ := competition.Lookup("2026", "Playoffs")
	var data cache.SeasonData
	for i, pair := range [][2]int{{1, 8}, {4, 5}, {2, 7}, {3, 6}, {4, 1}} {
		game := forecastPlayoffGame(seeds, fmt.Sprintf("game-%d", i), pair[0], pair[1], true)
		game.KickoffUTC = fmt.Sprintf("2026-11-%02dT20:00:00Z", i+1)
		data.Games = append(data.Games, game)
	}
	page := seededBracketPageFor(seasonPage{}, *entry.BracketFormat, data, seeds, "")
	semi := page.Rounds[1].Slots[0]
	if semi.HomeSource.ID != "quarterfinal-2" || semi.AwaySource.ID != "quarterfinal-1" || semi.Home.ID != seeds.Seeds[4] || semi.Away.ID != seeds.Seeds[1] {
		t.Fatalf("published sides lost their feeder: %+v", semi)
	}
}
