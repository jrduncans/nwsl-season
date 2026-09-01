package bracket

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

func catalogFormat(t *testing.T, season, stage string) competition.BracketFormat {
	t.Helper()
	entry, found := competition.Lookup(season, stage)
	if !found || entry.BracketFormat == nil {
		t.Fatalf("missing bracket format for %s %s", season, stage)
	}
	return entry.BracketFormat.Copy()
}

func sourceGame(id, home, away, kickoff string) cache.Game {
	return cache.Game{ASAID: id, Season: "test", Stage: "Playoffs", KickoffUTC: kickoff, Status: "PreMatch", HomeTeamID: home, AwayTeamID: away}
}

func scored(game cache.Game, home, away int64) cache.Game {
	game.HomeScore = sql.NullInt64{Int64: home, Valid: true}
	game.AwayScore = sql.NullInt64{Int64: away, Valid: true}
	return game
}

func slots(view View) []Slot {
	values := []Slot{}
	for _, round := range view.Rounds {
		values = append(values, round.Slots...)
	}
	return values
}

func TestBuildEmptyPreservesEveryCatalogShape(t *testing.T) {
	for _, entry := range competition.PublicEntries() {
		if entry.BracketFormat == nil {
			continue
		}
		t.Run(entry.Season+" "+entry.Stage, func(t *testing.T) {
			view := Build(*entry.BracketFormat, nil)
			if view.State != StateEmpty || len(slots(view)) != len(entry.BracketFormat.Slots) {
				t.Fatalf("empty view = %+v", view)
			}
			for _, slot := range slots(view) {
				if slot.Home.TeamID != TBD || slot.Away.TeamID != TBD || slot.Winner.TeamID != TBD || slot.Game != nil {
					t.Fatalf("empty slot = %+v", slot)
				}
			}
		})
	}
}

func TestBuildPartialUsesChronologyAndTBDWithoutStatusInference(t *testing.T) {
	format := catalogFormat(t, "2024", "Playoffs")
	late := sourceGame("later-id", "charlie", "delta", "2024-11-02T20:00:00Z")
	early := sourceGame("early-id", "alpha", "bravo", "2024-11-01T20:00:00Z")
	// A source status cannot choose a bracket slot or manufacture a winner.
	late.Status = "FullTime"
	view := Build(format, []cache.Game{late, early})
	if view.State != StatePartial {
		t.Fatalf("state = %q, diagnostics=%+v", view.State, view.Diagnostics)
	}
	placed := slots(view)
	if placed[0].Game == nil || placed[0].Game.ASAID != "early-id" || placed[1].Game == nil || placed[1].Game.ASAID != "later-id" {
		t.Fatalf("games were not placed in chronological verified slots: %+v", placed[:2])
	}
	if placed[0].Winner.TeamID != TBD || placed[2].Home.TeamID != TBD || placed[2].Away.TeamID != TBD {
		t.Fatalf("status inferred an outcome or entrant: %+v", placed)
	}
	// The view owns copies of both topology and source games.
	early.HomeTeamID = "changed"
	if placed[0].Home.TeamID != "alpha" || view.SourceGames[1].HomeTeamID != "alpha" {
		t.Fatalf("input mutation leaked into view: %+v", view)
	}
}

func TestBuildAcceptsASAUTCLayoutForCompletedFixedBracket(t *testing.T) {
	format := catalogFormat(t, "2024", "Playoffs")
	games := []cache.Game{
		scored(sourceGame("quarter-1", "one", "two", "2024-11-09 01:00:00 UTC"), 4, 1),
		scored(sourceGame("quarter-2", "three", "four", "2024-11-09 17:00:00 UTC"), 1, 0),
		scored(sourceGame("quarter-3", "five", "six", "2024-11-10 17:30:00 UTC"), 2, 1),
		scored(sourceGame("quarter-4", "seven", "eight", "2024-11-10 20:00:00 UTC"), 2, 1),
		scored(sourceGame("semi-2", "five", "seven", "2024-11-16 17:00:00 UTC"), 1, 0),
		scored(sourceGame("semi-1", "one", "three", "2024-11-17 20:00:00 UTC"), 3, 2),
		scored(sourceGame("final", "one", "five", "2024-11-24 01:00:00 UTC"), 1, 0),
	}
	view := Build(format, games)
	if view.State != StateReady || len(view.Diagnostics) != 0 || slots(view)[6].Winner.TeamID != "one" {
		t.Fatalf("ASA UTC bracket = %+v", view)
	}
}

func TestBuild2025FixedBracketUsesVerifiedNonChronologicalQuarterfinalSlots(t *testing.T) {
	format := catalogFormat(t, "2025", "Playoffs")
	games := []cache.Game{
		scored(sourceGame("orl-sea", "orl", "sea", "2025-11-08 01:00:00 UTC"), 2, 0),
		scored(sourceGame("was-lou", "was", "lou", "2025-11-08 17:00:00 UTC"), 1, 1),
		scored(sourceGame("kc-gotham", "kc", "gotham", "2025-11-09 17:30:00 UTC"), 1, 2),
		scored(sourceGame("por-sd", "por", "sd", "2025-11-09 20:00:00 UTC"), 1, 0),
		scored(sourceGame("semi-2", "was", "por", "2025-11-15 17:00:00 UTC"), 2, 0),
		scored(sourceGame("semi-1", "orl", "gotham", "2025-11-16 20:00:00 UTC"), 0, 1),
		scored(sourceGame("final", "was", "gotham", "2025-11-23 01:00:00 UTC"), 0, 1),
	}
	games[1].Penalties = sql.NullBool{Bool: true, Valid: true}
	games[1].HomePenalties, games[1].AwayPenalties = sql.NullInt64{Int64: 3, Valid: true}, sql.NullInt64{Int64: 1, Valid: true}
	view := Build(format, games)
	if view.State != StateReady || len(view.Diagnostics) != 0 || slots(view)[6].Winner.TeamID != "gotham" {
		t.Fatalf("2025 source-backed fixed bracket = %+v", view)
	}
	for index, want := range []struct {
		gameID, home, away string
		pair               [2]int
	}{
		{"kc-gotham", "kc", "gotham", [2]int{1, 8}},
		{"orl-sea", "orl", "sea", [2]int{4, 5}},
		{"was-lou", "was", "lou", [2]int{2, 7}},
		{"por-sd", "por", "sd", [2]int{3, 6}},
	} {
		slot := slots(view)[index]
		if slot.Game == nil || slot.Game.ASAID != want.gameID || slot.Home.TeamID != want.home || slot.Away.TeamID != want.away || slot.SeedPair == nil || *slot.SeedPair != want.pair {
			t.Fatalf("quarterfinal slot %d = %+v, want %+v", index+1, slot, want)
		}
	}
}

func TestBuildResolvesDirectPenaltyWinnerAndEqualScoreUnresolved(t *testing.T) {
	format := catalogFormat(t, "2024", "NWSL Challenge Cup Final")
	game := scored(sourceGame("final", "alpha", "bravo", "2024-08-01T20:00:00Z"), 1, 1)
	game.Penalties = sql.NullBool{Bool: true, Valid: true}
	game.HomePenalties, game.AwayPenalties = sql.NullInt64{Int64: 4, Valid: true}, sql.NullInt64{Int64: 3, Valid: true}
	view := Build(format, []cache.Game{game})
	if view.State != StateReady || slots(view)[0].Winner.TeamID != "alpha" {
		t.Fatalf("direct penalties view = %+v", view)
	}
	game.HomePenalties, game.AwayPenalties = sql.NullInt64{Int64: 4, Valid: true}, sql.NullInt64{Int64: 4, Valid: true}
	view = Build(format, []cache.Game{game})
	if view.State != StateUnresolved || slots(view)[0].Winner.TeamID != TBD || len(view.Diagnostics) != 1 || view.Diagnostics[0].Code != "unresolved_penalties" {
		t.Fatalf("equal shootout view = %+v", view)
	}
	game.Penalties = sql.NullBool{}
	game.HomePenalties, game.AwayPenalties = sql.NullInt64{}, sql.NullInt64{}
	view = Build(format, []cache.Game{game})
	if view.State != StateUnresolved || slots(view)[0].Winner.TeamID != TBD {
		t.Fatalf("tied game without shootout was resolved: %+v", view)
	}
}

func TestBuildFixedAdvancementAndSourceCorrection(t *testing.T) {
	format := catalogFormat(t, "2016", "Playoffs")
	semiOne := scored(sourceGame("semi-one", "alpha", "bravo", "2016-10-01T20:00:00Z"), 2, 1)
	semiTwo := scored(sourceGame("semi-two", "charlie", "delta", "2016-10-02T20:00:00Z"), 1, 0)
	final := scored(sourceGame("final", "alpha", "charlie", "2016-10-08T20:00:00Z"), 1, 0)
	view := Build(format, []cache.Game{semiOne, semiTwo, final})
	if view.State != StateReady || slots(view)[2].Winner.TeamID != "alpha" {
		t.Fatalf("fixed ready view = %+v", view)
	}
	semiOne = scored(semiOne, 0, 2)
	final.HomeTeamID = "bravo"
	final = scored(final, 1, 0)
	view = Build(format, []cache.Game{semiOne, semiTwo, final})
	if view.State != StateReady || slots(view)[2].Home.TeamID != "bravo" || slots(view)[2].Winner.TeamID != "bravo" {
		t.Fatalf("corrected source did not replace bracket facts: %+v", view)
	}
}

func TestBuildFixedLaterRoundMatchesParticipantsNotReversedSemifinalKickoffs(t *testing.T) {
	format := catalogFormat(t, "2024", "Playoffs")
	games := []cache.Game{
		scored(sourceGame("quarter-1", "alpha", "bravo", "2024-11-01T20:00:00Z"), 1, 0),
		scored(sourceGame("quarter-2", "charlie", "delta", "2024-11-02T20:00:00Z"), 1, 0),
		scored(sourceGame("quarter-3", "echo", "foxtrot", "2024-11-03T20:00:00Z"), 1, 0),
		scored(sourceGame("quarter-4", "golf", "hotel", "2024-11-04T20:00:00Z"), 1, 0),
		// The semifinal tied to quarterfinals 3/4 is observed first. Its
		// source participants, not within-round kickoff order, choose slot 2.
		scored(sourceGame("semi-2", "echo", "golf", "2024-11-10T18:00:00Z"), 1, 0),
		scored(sourceGame("semi-1", "alpha", "charlie", "2024-11-10T20:00:00Z"), 1, 0),
		scored(sourceGame("final", "alpha", "echo", "2024-11-16T20:00:00Z"), 1, 0),
	}
	view := Build(format, games)
	all := slots(view)
	if view.State != StateReady || all[4].Game == nil || all[4].Game.ASAID != "semi-1" || all[5].Game == nil || all[5].Game.ASAID != "semi-2" {
		t.Fatalf("reversed fixed semifinals = %+v", view)
	}
}

func TestBuildRejectsFixedParticipantMismatchWithoutDroppingSourceGames(t *testing.T) {
	format := catalogFormat(t, "2016", "Playoffs")
	games := []cache.Game{
		scored(sourceGame("semi-one", "alpha", "bravo", "2016-10-01T20:00:00Z"), 2, 1),
		scored(sourceGame("semi-two", "charlie", "delta", "2016-10-02T20:00:00Z"), 1, 0),
		scored(sourceGame("final", "bravo", "charlie", "2016-10-08T20:00:00Z"), 1, 0),
	}
	view := Build(format, games)
	if view.State != StateFormatMismatch || len(view.SourceGames) != len(games) || len(view.Diagnostics) != 1 || view.Diagnostics[0].Code != "impossible_participants" {
		t.Fatalf("fixed mismatch = %+v", view)
	}
}

func TestBuildObservedReseedingUsesSourceSemifinalsWithoutFabricatingPairing(t *testing.T) {
	format := catalogFormat(t, "2021", "Playoffs")
	quarterOne := scored(sourceGame("quarter-one", "alpha", "bravo", "2021-11-01T20:00:00Z"), 2, 1)
	quarterTwo := scored(sourceGame("quarter-two", "charlie", "delta", "2021-11-02T20:00:00Z"), 2, 0)
	partial := Build(format, []cache.Game{quarterOne, quarterTwo})
	if partial.State != StatePartial || slots(partial)[2].Home.TeamID != TBD || slots(partial)[3].Away.TeamID != TBD {
		t.Fatalf("reseeded partial fabricated semifinal pairing: %+v", partial)
	}
	semiOne := scored(sourceGame("semi-one", "alpha", "echo", "2021-11-08T20:00:00Z"), 1, 0)
	partial = Build(format, []cache.Game{quarterOne, quarterTwo, semiOne})
	if partial.State != StatePartial || slots(partial)[3].Home.TeamID != TBD || slots(partial)[4].Home.TeamID != TBD {
		t.Fatalf("one observed semifinal was not retained as a partial bracket: %+v", partial)
	}
	semiTwo := scored(sourceGame("semi-two", "charlie", "foxtrot", "2021-11-09T20:00:00Z"), 3, 0)
	final := scored(sourceGame("final", "alpha", "charlie", "2021-11-15T20:00:00Z"), 1, 0)
	view := Build(format, []cache.Game{quarterOne, quarterTwo, semiOne, semiTwo, final})
	if view.State != StateReady || slots(view)[4].Winner.TeamID != "alpha" {
		t.Fatalf("observed reseeding view = %+v", view)
	}
	semiOne.HomeTeamID = "bravo"
	view = Build(format, []cache.Game{quarterOne, quarterTwo, semiOne, semiTwo, final})
	if view.State != StateFormatMismatch || len(view.SourceGames) != 5 {
		t.Fatalf("reseeded participant mismatch = %+v", view)
	}
}

func TestBuildObservedReseedingRejectsInvalidIncrementalSemifinals(t *testing.T) {
	format := catalogFormat(t, "2021", "Playoffs")
	quarters := []cache.Game{
		scored(sourceGame("quarter-one", "alpha", "bravo", "2021-11-01T20:00:00Z"), 2, 1),
		scored(sourceGame("quarter-two", "charlie", "delta", "2021-11-02T20:00:00Z"), 2, 0),
	}
	for name, semifinals := range map[string][]cache.Game{
		"quarterfinal winner versus winner": {scored(sourceGame("semi-one", "alpha", "charlie", "2021-11-08T20:00:00Z"), 1, 0)},
		"bye versus bye":                    {scored(sourceGame("semi-one", "echo", "foxtrot", "2021-11-08T20:00:00Z"), 1, 0)},
		"duplicate quarterfinal winner": {
			scored(sourceGame("semi-one", "alpha", "echo", "2021-11-08T20:00:00Z"), 1, 0),
			scored(sourceGame("semi-two", "alpha", "foxtrot", "2021-11-09T20:00:00Z"), 1, 0),
		},
		"duplicate bye": {
			scored(sourceGame("semi-one", "alpha", "echo", "2021-11-08T20:00:00Z"), 1, 0),
			scored(sourceGame("semi-two", "charlie", "echo", "2021-11-09T20:00:00Z"), 1, 0),
		},
	} {
		t.Run(name, func(t *testing.T) {
			view := Build(format, append(append([]cache.Game(nil), quarters...), semifinals...))
			if view.State != StateFormatMismatch || len(view.SourceGames) != len(quarters)+len(semifinals) {
				t.Fatalf("invalid observed semifinal = %+v", view)
			}
		})
	}
}

func TestBuildReportsTopologyAndSourceConflicts(t *testing.T) {
	format := catalogFormat(t, "2024", "NWSL Challenge Cup Final")
	valid := sourceGame("final", "alpha", "bravo", "2024-08-01T20:00:00Z")
	for name, games := range map[string][]cache.Game{
		"extra game":       {valid, sourceGame("extra", "charlie", "delta", "2024-08-02T20:00:00Z")},
		"duplicate game":   {valid, valid},
		"bad participants": {sourceGame("bad", "alpha", "alpha", "2024-08-01T20:00:00Z")},
		"bad kickoff":      {sourceGame("bad-time", "alpha", "bravo", "not-a-time")},
	} {
		t.Run(name, func(t *testing.T) {
			view := Build(format, games)
			if view.State != StateFormatMismatch || len(view.SourceGames) != len(games) || len(view.Diagnostics) == 0 {
				t.Fatalf("conflict view = %+v", view)
			}
		})
	}
}

func TestBuildRejectsParticipantReuseWithinOneRound(t *testing.T) {
	format := catalogFormat(t, "2024", "Playoffs")
	games := []cache.Game{
		scored(sourceGame("quarter-1", "alpha", "bravo", "2024-11-01T20:00:00Z"), 1, 0),
		scored(sourceGame("quarter-2", "alpha", "charlie", "2024-11-02T20:00:00Z"), 1, 0),
	}
	view := Build(format, games)
	if view.State != StateFormatMismatch || len(view.Diagnostics) != 1 || view.Diagnostics[0].Code != "participant_reuse" || len(view.SourceGames) != len(games) {
		t.Fatalf("same-round reuse = %+v", view)
	}
}

func TestBuildDoesNotMutateFormat(t *testing.T) {
	format := catalogFormat(t, "2024", "NWSL Challenge Cup Final")
	before := format.Copy()
	_ = Build(format, nil)
	if !reflect.DeepEqual(format, before) {
		t.Fatalf("build mutated format: before=%+v after=%+v", before, format)
	}
}

func TestBuildPopulatedFormatFamilies(t *testing.T) {
	for _, test := range []struct {
		name, season, stage, family string
	}{
		{"classic playoffs", "2016", "Playoffs", "semifinal-final"},
		{"modern playoffs", "2024", "Playoffs", "quarterfinal-semifinal-final"},
		{"observed reseeded playoffs", "2021", "Playoffs", "observed-reseeded"},
		{"modern challenge cup knockout", "2020", "NWSL Challenge Cup Knockout Round", "quarterfinal-semifinal-final"},
		{"challenge cup semifinal-final", "2022", "NWSL Challenge Cup Knockout Round", "semifinal-final"},
		{"single challenge cup knockout", "2021", "NWSL Challenge Cup Knockout Round", "single-final"},
		{"single challenge cup final", "2024", "NWSL Challenge Cup Final", "single-final"},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := Build(catalogFormat(t, test.season, test.stage), populatedFamilyGames(test.family))
			if view.State != StateReady || len(view.SourceGames) != len(slots(view)) {
				t.Fatalf("populated family %s = %+v", test.family, view)
			}
		})
	}
}

func populatedFamilyGames(family string) []cache.Game {
	game := func(id, home, away, kickoff string) cache.Game {
		return scored(sourceGame(id, home, away, kickoff), 1, 0)
	}
	switch family {
	case "single-final":
		return []cache.Game{game("final", "alpha", "bravo", "2024-01-01T20:00:00Z")}
	case "semifinal-final":
		return []cache.Game{
			game("semi-1", "alpha", "bravo", "2024-01-01T20:00:00Z"),
			game("semi-2", "charlie", "delta", "2024-01-02T20:00:00Z"),
			game("final", "alpha", "charlie", "2024-01-03T20:00:00Z"),
		}
	case "observed-reseeded":
		return []cache.Game{
			game("quarter-1", "alpha", "bravo", "2024-01-01T20:00:00Z"),
			game("quarter-2", "charlie", "delta", "2024-01-02T20:00:00Z"),
			game("semi-1", "alpha", "echo", "2024-01-03T20:00:00Z"),
			game("semi-2", "charlie", "foxtrot", "2024-01-04T20:00:00Z"),
			game("final", "alpha", "charlie", "2024-01-05T20:00:00Z"),
		}
	default: // quarterfinal-semifinal-final
		return []cache.Game{
			game("quarter-1", "alpha", "bravo", "2024-01-01T20:00:00Z"),
			game("quarter-2", "charlie", "delta", "2024-01-02T20:00:00Z"),
			game("quarter-3", "echo", "foxtrot", "2024-01-03T20:00:00Z"),
			game("quarter-4", "golf", "hotel", "2024-01-04T20:00:00Z"),
			game("semi-1", "alpha", "charlie", "2024-01-05T20:00:00Z"),
			game("semi-2", "echo", "golf", "2024-01-06T20:00:00Z"),
			game("final", "alpha", "echo", "2024-01-07T20:00:00Z"),
		}
	}
}
