package bracket

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestSeedExpectationsSurvivePartialPublicationAndAdvanceOnlyResults(t *testing.T) {
	format := catalogFormat(t, "2026", "Playoffs")
	seeds := map[int]string{}
	for i := 1; i <= 8; i++ {
		seeds[i] = fmt.Sprint(i)
	}
	empty := BuildWithSeeds(format, nil, seeds)
	if empty.State != StateEmpty || slots(empty)[0].Home.TeamID != "1" || slots(empty)[0].Away.TeamID != "8" || slots(empty)[0].Game != nil || slots(empty)[4].Home.TeamID != TBD {
		t.Fatalf("expected bracket = %+v", empty)
	}
	// ASA's first concrete game can be the fourth slot. Source side ordering
	// takes precedence; placeholders must not reserve a slot by chronology.
	games := []cache.Game{{ASAID: "placeholder"}, scored(sourceGame("q4", "6", "3", "2026-11-01T20:00:00Z"), 0, 2)}
	before := append([]cache.Game(nil), games...)
	partial := BuildWithSeeds(format, games, seeds)
	if partial.State != StatePartial || slots(partial)[3].Game == nil || slots(partial)[0].Game != nil || slots(partial)[3].Home.TeamID != "6" {
		t.Fatalf("partial publication = %+v", partial)
	}
	if !reflect.DeepEqual(games, before) {
		t.Fatal("seed derivation mutated source fixtures")
	}
	games = []cache.Game{
		scored(sourceGame("q4", "3", "6", "2026-11-01T20:00:00Z"), 2, 0),
		scored(sourceGame("q2", "4", "5", "2026-11-02T20:00:00Z"), 2, 0),
		scored(sourceGame("q3", "2", "7", "2026-11-03T20:00:00Z"), 2, 0),
		scored(sourceGame("q1", "1", "8", "2026-11-04T20:00:00Z"), 2, 0),
		scored(sourceGame("s2", "2", "3", "2026-11-10T20:00:00Z"), 2, 0),
		scored(sourceGame("s1", "1", "4", "2026-11-11T20:00:00Z"), 2, 0),
		scored(sourceGame("f", "1", "2", "2026-11-20T20:00:00Z"), 2, 0),
	}
	full := BuildWithSeeds(format, games, seeds)
	if full.State != StateReady || slots(full)[6].Winner.TeamID != "1" {
		t.Fatalf("populated bracket = %+v", full)
	}
}

func TestSeedSourceDisagreementAndUnresolvedSeedsRemainVisible(t *testing.T) {
	format := catalogFormat(t, "2026", "Playoffs")
	seeds := map[int]string{1: "one", 8: "eight"}
	game := sourceGame("conflict", "one", "seven", "2026-11-01T20:00:00Z")
	view := BuildWithSeeds(format, []cache.Game{game}, seeds)
	if view.State != StateFormatMismatch || len(view.Diagnostics) == 0 || len(view.SourceGames) != 1 || slots(view)[0].Away.TeamID != "eight" || slots(view)[0].Game != nil {
		t.Fatalf("source disagreement was hidden: %+v", view)
	}
	if slots(view)[1].Home.TeamID != TBD || slots(view)[1].Winner.TeamID != TBD {
		t.Fatal("unresolved seeds or outcomes were invented")
	}
	// A source pairing can arrive before its date. Participants identify its
	// slot without requiring or generating a chronological kickoff.
	game = sourceGame("undated", "one", "eight", "")
	view = BuildWithSeeds(format, []cache.Game{game}, seeds)
	if view.State != StatePartial || slots(view)[0].Game == nil || slots(view)[0].Game.KickoffUTC != "" {
		t.Fatalf("undated published fixture = %+v", view)
	}
}

func TestPublishedGamesFillUnresolvedSeedsWithoutDisagreement(t *testing.T) {
	format := catalogFormat(t, "2026", "Playoffs")
	// Seeds 2 and 3 are tied; resolved seed 9 cannot be a playoff entrant.
	seeds := map[int]string{1: "1", 4: "4", 5: "5", 6: "6", 7: "7", 8: "8", 9: "9"}
	games := []cache.Game{
		scored(sourceGame("q3", "2", "7", "2026-11-01T20:00:00Z"), 2, 0),
		sourceGame("q4", "6", "3", "2026-11-02T20:00:00Z"),
		sourceGame("q1", "1", "8", "2026-11-03T20:00:00Z"),
	}
	view := BuildWithSeeds(format, games, seeds)
	if view.State != StatePartial || len(view.Diagnostics) != 0 {
		t.Fatalf("unresolved seeds produced a disagreement: %+v", view)
	}
	if slots(view)[2].Game == nil || slots(view)[2].Home.TeamID != "2" || slots(view)[3].Away.TeamID != "3" || slots(view)[0].Game == nil {
		t.Fatalf("published games were not placed by their known seeds: %+v", view)
	}

	// With every opening-round seed unknown, a published game fits several
	// slots. It stays unplaced and unresolved rather than guessed or flagged.
	view = BuildWithSeeds(format, games[:1], map[int]string{9: "9"})
	if view.State != StateUnresolved || len(view.Diagnostics) != 0 || slots(view)[2].Game != nil {
		t.Fatalf("ambiguous published game = %+v", view)
	}

	// A resolved non-entrant still disagrees with the expected bracket.
	view = BuildWithSeeds(format, []cache.Game{sourceGame("q3", "9", "7", "2026-11-01T20:00:00Z")}, seeds)
	if view.State != StateFormatMismatch {
		t.Fatalf("resolved non-entrant was placed: %+v", view)
	}
}
