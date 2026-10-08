package app

import (
	"database/sql"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
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
