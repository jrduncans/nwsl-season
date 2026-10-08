package app

import (
	"database/sql"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestFixtureWinnerUsesScoreThenShootout(t *testing.T) {
	score := func(value int64) sql.NullInt64 { return sql.NullInt64{Int64: value, Valid: true} }
	shootout := sql.NullBool{Bool: true, Valid: true}
	for _, test := range []struct {
		name string
		game cache.Game
		want string
	}{
		{"home win", cache.Game{HomeScore: score(2), AwayScore: score(1)}, "home"},
		{"away win", cache.Game{HomeScore: score(0), AwayScore: score(3)}, "away"},
		{"draw", cache.Game{HomeScore: score(1), AwayScore: score(1)}, ""},
		{"shootout", cache.Game{HomeScore: score(1), AwayScore: score(1), Penalties: shootout, HomePenalties: score(3), AwayPenalties: score(4)}, "away"},
	} {
		if got := fixtureWinner(test.game); got != test.want {
			t.Errorf("%s: fixtureWinner = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestFixtureRowsCarryClinchingTeamCodes(t *testing.T) {
	data := cache.SeasonData{
		Teams: []standings.Team{{ID: "sd", Name: "San Diego Wave FC", ShortName: "San Diego", Abbreviation: "SD"}, {ID: "orl", Name: "Orlando Pride", ShortName: "Orlando"}},
		Games: []cache.Game{{ASAID: "g1", HomeTeamID: "orl", AwayTeamID: "sd", KickoffUTC: "2026-10-02 23:00:00 UTC", Status: standings.CompletedStatus}},
	}
	groups := fixtureGroups(data, time.UTC)
	if len(groups) != 1 || len(groups[0].Games) != 1 {
		t.Fatalf("groups = %+v", groups)
	}
	game := groups[0].Games[0]
	// Codes match the Clinching page: the abbreviation, falling back to the short name.
	if game.Home().Code != "Orlando" || game.Away().Code != "SD" || game.Away().Name != "San Diego Wave FC" {
		t.Fatalf("home = %+v, away = %+v", game.Home(), game.Away())
	}
}
