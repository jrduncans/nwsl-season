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

func TestFixtureRowsShowOnlyConfirmedShootoutTallies(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	tally := func(value int64) sql.NullInt64 { return sql.NullInt64{Int64: value, Valid: true} }
	confirmed := sql.NullBool{Bool: true, Valid: true}
	for _, tc := range []struct {
		name       string
		penalties  sql.NullBool
		home, away sql.NullInt64
		want       string
	}{
		{"home wins shootout", confirmed, tally(5), tally(4), "Penalties 5–4"},
		{"away wins shootout", confirmed, tally(4), tally(5), "Penalties 4–5"},
		{"no penalties", sql.NullBool{}, sql.NullInt64{}, sql.NullInt64{}, ""},
		{"missing flag", sql.NullBool{}, tally(5), tally(4), ""},
		{"false flag", sql.NullBool{Valid: true}, tally(5), tally(4), ""},
		{"missing home tally", confirmed, sql.NullInt64{}, tally(4), ""},
		{"missing away tally", confirmed, tally(5), sql.NullInt64{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			game := forecastPlayoffGame(seeds, "shootout", 1, 8, true)
			game.AwayScore = game.HomeScore
			game.Penalties, game.HomePenalties, game.AwayPenalties = tc.penalties, tc.home, tc.away
			data := cache.SeasonData{Teams: regular.Teams, Games: []cache.Game{game}}
			rows := fixtureGroups(data, time.UTC)[0].Games
			if rows[0].Shootout != tc.want || rows[0].Score != "2–2" {
				t.Fatalf("match row = %+v; want unchanged score and shootout %q", rows[0], tc.want)
			}
			if got := bracketGameViewFor(game, nil).Shootout; got != tc.want {
				t.Fatalf("bracket shootout = %q, want %q", got, tc.want)
			}
			body := handoffResponse(t, handoffStore{regular: regular, playoffs: data}, "/seasons/2026/playoffs/fixtures")
			requireExact(t, body, ".fixtures .fixture-result", "2–2")
			if tc.want == "" {
				forbidText(t, body, ".fixtures .scheduled", "Penalties")
			} else {
				requireExact(t, body, ".fixtures .scheduled .fixture-meta-tag", tc.want)
			}
		})
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
