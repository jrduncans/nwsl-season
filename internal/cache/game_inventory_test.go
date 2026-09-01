package cache

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/competition"
)

func inventoryMetadata() FullRefreshMetadata {
	due := time.Date(2026, 8, 8, 12, 1, 0, 0, time.UTC)
	return FullRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 8, 1, 12, 1, 0, 0, time.UTC), NextFullDueAt: &due}
}

func inventoryGame(id, status string, home, away int64) Game {
	return Game{ASAID: id, Season: "2030", Stage: "Example", KickoffUTC: "2030-01-01T12:00:00Z", Status: status, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeScore: sql.NullInt64{Int64: home, Valid: status == "FullTime"}, AwayScore: sql.NullInt64{Int64: away, Valid: status == "FullTime"}, Matchday: sql.NullInt64{Int64: 1, Valid: true}, LastUpdatedUTC: "2030-01-01T11:00:00Z", RawJSON: "{}"}
}

func inventoryDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "alpha", Name: "Alpha"}, {ASAID: "bravo", Name: "Bravo"}}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func TestReplaceGameInventoryEmptyDiscoveryAndPopulatedProtection(t *testing.T) {
	db, ctx := inventoryDB(t)
	result, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{}, &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 1, Games: 1}, inventoryMetadata())
	if err != nil || result.SyncRun != nil || result.Teams == nil || result.Games == nil || result.Audit.ReturnedRows != 0 || result.Audit.DownstreamInputsChanged {
		t.Fatalf("empty result = %+v, %v", result, err)
	}
	state, ok, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2030", "Example")
	if err != nil || !ok || state.LastFullSuccessAt == nil {
		t.Fatalf("state = %+v,%v,%v", state, ok, err)
	}
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{inventoryGame("one", "PreMatch", 0, 0)}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
	beforeState, beforeOK, _ := db.SourceResourceScopeState(ctx, SourceResourceGames, "2030", "Example")
	beforeRun, _ := db.LastSuccess(ctx, "2030", "Example")
	beforeVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{}, nil, inventoryMetadata()); err == nil {
		t.Fatal("populated empty response succeeded")
	}
	if got, _ := db.seasonGames(ctx, "2030", "Example"); len(got) != 1 {
		t.Fatalf("games after failed empty = %+v", got)
	}
	afterAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
	afterState, afterOK, _ := db.SourceResourceScopeState(ctx, SourceResourceGames, "2030", "Example")
	afterRun, _ := db.LastSuccess(ctx, "2030", "Example")
	afterVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !reflect.DeepEqual(beforeAudits, afterAudits) || beforeOK != afterOK || !reflect.DeepEqual(beforeState, afterState) || !reflect.DeepEqual(beforeRun, afterRun) || !reflect.DeepEqual(beforeVenue, afterVenue) {
		t.Fatal("populated empty changed metadata")
	}
}

func TestReplaceGameInventoryUnknownTeamsAndValidationAreWriteFree(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("one", "PreMatch", 0, 0)
	game.HomeTeamID = "zulu"
	game.AwayTeamID = "charlie"
	_, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata())
	var unknown *UnknownGameTeamsError
	if !errors.Is(err, ErrUnknownGameTeams) || !errors.As(err, &unknown) || !reflect.DeepEqual(unknown.TeamIDs, []string{"charlie", "zulu"}) {
		t.Fatalf("unknown error = %#v", err)
	}
	unknown.TeamIDs[0] = "mutated"
	if got, _ := db.seasonGames(ctx, "2030", "Example"); len(got) != 0 {
		t.Fatalf("games = %+v", got)
	}
	if _, err := db.ReplaceGameInventory(ctx, "2030 ", "Example", []Game{inventoryGame("one", "PreMatch", 0, 0)}, nil, inventoryMetadata()); err == nil {
		t.Fatal("padded scope succeeded")
	}
}

func TestPlayoffGameFieldsAreValidatedAndMaterial(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("playoff", "FullTime", 1, 0)
	game.Season, game.Stage = "2026", "Playoffs"
	game.ExpandedMinutes = sql.NullInt64{Int64: 120, Valid: true}
	if _, err := db.ReplaceGameInventory(ctx, "2026", "Playoffs", []Game{game}, nil, inventoryMetadata()); err == nil {
		t.Fatal("unclassified playoff game succeeded")
	}
	game.KnockoutGame = true
	first, err := db.ReplaceGameInventory(ctx, "2026", "Playoffs", []Game{game}, nil, inventoryMetadata())
	if err != nil || !first.Audit.DownstreamInputsChanged {
		t.Fatalf("first=%+v,%v", first, err)
	}
	changed := game
	changed.ExpandedMinutes = sql.NullInt64{Int64: 130, Valid: true}
	changed.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	second, err := db.ReplaceGameInventory(ctx, "2026", "Playoffs", []Game{changed}, nil, inventoryMetadata())
	if err != nil || second.Audit.RowsUpdated != 1 || !second.Audit.DownstreamInputsChanged || second.SyncRun.FixtureSnapshotID == first.SyncRun.FixtureSnapshotID {
		t.Fatalf("changed=%+v,%v", second, err)
	}
	if games, err := db.seasonGames(ctx, "2026", "Playoffs"); err != nil || len(games) != 1 || !games[0].KnockoutGame || !games[0].ExpandedMinutes.Valid || games[0].ExpandedMinutes.Int64 != 130 {
		t.Fatalf("games=%+v,%v", games, err)
	}
}

func TestKnockoutSourceFactsRoundTripAndRejectInvalidPairs(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("knockout", "FullTime", 1, 0)
	game.ExtraTime = sql.NullBool{Bool: false, Valid: true}
	game.Penalties = sql.NullBool{Bool: true, Valid: true}
	game.HomePenalties = sql.NullInt64{Int64: 5, Valid: true}
	game.AwayPenalties = sql.NullInt64{Int64: 4, Valid: true}
	first, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata())
	if err != nil || !first.Audit.DownstreamInputsChanged {
		t.Fatalf("first=%+v,%v", first, err)
	}
	unchangedFull, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata())
	if err != nil || unchangedFull.Audit.RowsUnchanged != 1 || unchangedFull.Audit.RowsUpdated != 0 || unchangedFull.Audit.DownstreamInputsChanged {
		t.Fatalf("unchanged full=%+v,%v", unchangedFull, err)
	}
	unchangedTargeted, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("knockout", 3)}, []Game{game}, checkMetadata(3))
	if err != nil || unchangedTargeted.Audit.RowsUnchanged != 1 || unchangedTargeted.Audit.RowsUpdated != 0 || unchangedTargeted.Audit.DownstreamInputsChanged {
		t.Fatalf("unchanged targeted=%+v,%v", unchangedTargeted, err)
	}
	changed := game
	changed.ExtraTime = sql.NullBool{}
	changed.HomePenalties = sql.NullInt64{Int64: 6, Valid: true}
	changed.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	second, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{changed}, nil, inventoryMetadata())
	if err != nil || second.Audit.RowsUpdated != 1 || !second.Audit.DownstreamInputsChanged || second.SyncRun.FixtureSnapshotID == first.SyncRun.FixtureSnapshotID {
		t.Fatalf("changed=%+v,%v", second, err)
	}
	loaded, err := db.seasonGames(ctx, "2030", "Example")
	if err != nil || len(loaded) != 1 || loaded[0].ExtraTime.Valid || !loaded[0].Penalties.Valid || !loaded[0].Penalties.Bool || !loaded[0].HomePenalties.Valid || loaded[0].HomePenalties.Int64 != 6 || !loaded[0].AwayPenalties.Valid || loaded[0].AwayPenalties.Int64 != 4 {
		t.Fatalf("loaded=%+v,%v", loaded, err)
	}
	targeted := changed
	targeted.HomePenalties = sql.NullInt64{Int64: 7, Valid: true}
	targeted.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	checked, err := db.UpsertCheckedGames(ctx, "2030", "Example", []CheckedGameRequest{checkRequest("knockout", 4)}, []Game{targeted}, checkMetadata(4))
	if err != nil || checked.Audit.RowsUpdated != 1 || !checked.Audit.DownstreamInputsChanged || len(checked.Games) != 1 || checked.Games[0].HomePenalties.Int64 != 7 {
		t.Fatalf("checked=%+v,%v", checked, err)
	}
	for _, invalid := range []Game{
		func() Game {
			value := game
			value.Penalties = sql.NullBool{Bool: true, Valid: true}
			value.HomePenalties = sql.NullInt64{}
			value.AwayPenalties = sql.NullInt64{}
			return value
		}(),
		func() Game { value := game; value.Penalties = sql.NullBool{Bool: false, Valid: true}; return value }(),
		func() Game {
			value := game
			value.HomePenalties = sql.NullInt64{Int64: 1, Valid: true}
			value.AwayPenalties = sql.NullInt64{}
			return value
		}(),
		func() Game {
			value := game
			value.HomePenalties = sql.NullInt64{Int64: -1, Valid: true}
			value.AwayPenalties = sql.NullInt64{Int64: 0, Valid: true}
			return value
		}(),
	} {
		if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{invalid}, nil, inventoryMetadata()); err == nil {
			t.Fatalf("invalid knockout facts accepted: %+v", invalid)
		}
	}
}

func TestReplaceGameInventoryLineagePreferenceAndMateriality(t *testing.T) {
	db, ctx := inventoryDB(t)
	first := inventoryGame("one", "PreMatch", 0, 0)
	result, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{first}, &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 1}, inventoryMetadata())
	if err != nil || result.SyncRun == nil || !result.Audit.DownstreamInputsChanged {
		t.Fatalf("first=%+v,%v", result, err)
	}
	noop, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{first}, nil, inventoryMetadata())
	if err != nil || noop.SyncRun == nil || noop.Audit.RowsUnchanged != 1 || noop.Audit.DownstreamInputsChanged {
		t.Fatalf("noop=%+v,%v", noop, err)
	}
	if noop.SyncRun.ID == result.SyncRun.ID || noop.SyncRun.GamesUpserted != 1 || noop.SyncRun.GamesInserted != 0 || noop.SyncRun.GamesUpdated != 0 || noop.SyncRun.GamesUnchanged != 1 {
		t.Fatalf("no-op lineage=%+v", noop.SyncRun)
	}
	raw := first
	raw.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	raw.RawJSON = `{"new":true}`
	updated, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{raw}, nil, inventoryMetadata())
	if err != nil || updated.Audit.RowsUpdated != 1 || updated.Audit.DownstreamInputsChanged || updated.SyncRun == nil {
		t.Fatalf("raw=%+v,%v", updated, err)
	}
	stale := raw
	stale.Status = "FullTime"
	stale.HomeScore = sql.NullInt64{Int64: 2, Valid: true}
	stale.AwayScore = sql.NullInt64{Int64: 1, Valid: true}
	stale.LastUpdatedUTC = "2030-01-01T11:30:00Z"
	terminal, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{stale}, nil, inventoryMetadata())
	if err != nil || terminal.Audit.RowsUpdated != 1 || !terminal.Audit.DownstreamInputsChanged {
		t.Fatalf("terminal=%+v,%v", terminal, err)
	}
	season, err := db.Season(ctx, "2030", "Example")
	if err != nil || season.LastSuccess == nil || season.LastSuccess.ID != terminal.SyncRun.ID {
		t.Fatalf("season=%+v,%v", season, err)
	}
	kickoff := stale
	kickoff.KickoffUTC = "2030-01-01T13:00:00Z"
	kickoff.LastUpdatedUTC = "2030-01-01T15:00:00Z"
	if changed, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{kickoff}, nil, inventoryMetadata()); err != nil || !changed.Audit.DownstreamInputsChanged {
		t.Fatalf("kickoff=%+v,%v", changed, err)
	}
	matchday := kickoff
	matchday.Matchday.Int64 = 2
	matchday.LastUpdatedUTC = "2030-01-01T16:00:00Z"
	if changed, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{matchday}, nil, inventoryMetadata()); err != nil || !changed.Audit.DownstreamInputsChanged {
		t.Fatalf("matchday=%+v,%v", changed, err)
	}
}

func TestReplaceGameInventoryAcceptsEarlierPreMatchReschedule(t *testing.T) {
	db, ctx := inventoryDB(t)
	original := inventoryGame("rescheduled", "PreMatch", 0, 0)
	original.KickoffUTC = "2030-01-01T16:00:00Z"
	// This mirrors the deterministic fallback used when ASA omits
	// last_updated_utc for an unplayed game.
	original.LastUpdatedUTC = original.KickoffUTC
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{original}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}

	earlier := original
	earlier.KickoffUTC = "2030-01-01T12:00:00Z"
	earlier.LastUpdatedUTC = earlier.KickoffUTC
	result, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{earlier}, nil, inventoryMetadata())
	if err != nil || result.Audit.RowsUpdated != 1 || !result.Audit.DownstreamInputsChanged {
		t.Fatalf("earlier reschedule = %+v, %v", result, err)
	}
	stored, err := db.seasonGames(ctx, "2030", "Example")
	if err != nil || len(stored) != 1 || stored[0].KickoffUTC != earlier.KickoffUTC {
		t.Fatalf("stored games = %+v, %v", stored, err)
	}
}

func TestReplaceGameInventoryRawOnlyPreservesVenueXG(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("one", "FullTime", 2, 1)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceGameXG(ctx, "2030", "Example", []Game{game}, []GameXG{{GameID: "one", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1.2, Valid: true}, AwayXG: sql.NullFloat64{Float64: .7, Valid: true}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if err != nil || len(before) != 1 || !before[0].XGReady {
		t.Fatalf("before=%+v,%v", before, err)
	}
	game.RawJSON = `{"changed":true}`
	game.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	result, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata())
	if err != nil || result.Audit.DownstreamInputsChanged {
		t.Fatalf("result=%+v,%v", result, err)
	}
	after, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("venue changed: before=%+v after=%+v", before, after)
	}
}

func TestReplaceGameInventoryValidationMatrixIsWriteFree(t *testing.T) {
	db, ctx := inventoryDB(t)
	valid := inventoryGame("one", "PreMatch", 0, 0)
	cases := []struct {
		name          string
		season, stage string
		games         []Game
		expected      *competition.InventoryExpectation
		metadata      FullRefreshMetadata
	}{
		{"blank scope", "", "Example", []Game{valid}, nil, inventoryMetadata()},
		{"nil games", "2030", "Example", nil, nil, inventoryMetadata()},
		{"duplicate id", "2030", "Example", []Game{valid, valid}, nil, inventoryMetadata()},
		{"padded id", "2030", "Example", func() []Game { g := valid; g.ASAID = " one"; return []Game{g} }(), nil, inventoryMetadata()},
		{"wrong scope", "2030", "Example", func() []Game { g := valid; g.Stage = "Else"; return []Game{g} }(), nil, inventoryMetadata()},
		{"self opponent", "2030", "Example", func() []Game { g := valid; g.AwayTeamID = "alpha"; return []Game{g} }(), nil, inventoryMetadata()},
		{"bad status", "2030", "Example", func() []Game { g := valid; g.Status = "Delayed"; return []Game{g} }(), nil, inventoryMetadata()},
		{"bad kickoff", "2030", "Example", func() []Game { g := valid; g.KickoffUTC = "no"; return []Game{g} }(), nil, inventoryMetadata()},
		{"bad update", "2030", "Example", func() []Game { g := valid; g.LastUpdatedUTC = "no"; return []Game{g} }(), nil, inventoryMetadata()},
		{"one score", "2030", "Example", func() []Game { g := valid; g.HomeScore = sql.NullInt64{Int64: 1, Valid: true}; return []Game{g} }(), nil, inventoryMetadata()},
		{"negative score", "2030", "Example", func() []Game {
			g := valid
			g.Status = "FullTime"
			g.HomeScore = sql.NullInt64{Int64: -1, Valid: true}
			g.AwayScore = sql.NullInt64{Valid: true}
			return []Game{g}
		}(), nil, inventoryMetadata()},
		{"fulltime no scores", "2030", "Example", func() []Game {
			g := inventoryGame("one", "FullTime", 0, 0)
			g.HomeScore = sql.NullInt64{}
			g.AwayScore = sql.NullInt64{}
			return []Game{g}
		}(), nil, inventoryMetadata()},
		{"negative matchday", "2030", "Example", func() []Game { g := valid; g.Matchday = sql.NullInt64{Int64: -1, Valid: true}; return []Game{g} }(), nil, inventoryMetadata()},
		{"bad expectation", "2030", "Example", []Game{valid}, &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 1, Games: 2}, inventoryMetadata()},
		{"raw clock order", "2030", "Example", []Game{valid}, nil, func() FullRefreshMetadata {
			m := inventoryMetadata()
			m.StartedAt = m.FinishedAt.Add(time.Nanosecond)
			return m
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.ReplaceGameInventory(ctx, tc.season, tc.stage, tc.games, tc.expected, tc.metadata); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
	if games, _ := db.seasonGames(ctx, "2030", "Example"); len(games) != 0 {
		t.Fatalf("validation wrote games: %+v", games)
	}
	if audits, err := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example"); err != nil || len(audits) != 0 {
		t.Fatalf("validation wrote audits: %+v,%v", audits, err)
	}
}

func TestReplaceGameInventoryExpectationDeletionAndDefensiveOrdering(t *testing.T) {
	db, ctx := inventoryDB(t)
	second := inventoryGame("two", "PreMatch", 0, 0)
	second.KickoffUTC = "2030-01-02T12:00:00Z"
	second.Matchday = sql.NullInt64{Int64: 2, Valid: true}
	first := inventoryGame("one", "PreMatch", 0, 0)
	result, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{second, first}, &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 2}, inventoryMetadata())
	if err != nil || len(result.Games) != 2 || result.Games[0].ASAID != "one" || len(result.Teams) != 2 || result.Teams[0].ASAID != "alpha" {
		t.Fatalf("ordered result=%+v,%v", result, err)
	}
	result.Games[0].Status = "mutated"
	result.Teams[0].Name = "mutated"
	other := inventoryGame("other", "PreMatch", 0, 0)
	other.Season, other.Stage = "2031", "Other"
	if _, err := db.ReplaceGameInventory(ctx, "2031", "Other", []Game{other}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	deleted, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{first}, &competition.InventoryExpectation{Games: 1}, inventoryMetadata())
	if err != nil || deleted.Audit.RowsDeleted != 1 || !deleted.Audit.DownstreamInputsChanged {
		t.Fatalf("deletion=%+v,%v", deleted, err)
	}
	if games, _ := db.seasonGames(ctx, "2030", "Example"); len(games) != 1 || games[0].ASAID != "one" {
		t.Fatalf("remaining=%+v", games)
	}
	if season, err := db.Season(ctx, "2030", "Example"); err != nil || season.Games[0].Status == "mutated" {
		t.Fatalf("defensive season=%+v,%v", season, err)
	}
	if games, _ := db.seasonGames(ctx, "2031", "Other"); len(games) != 1 || games[0].ASAID != "other" {
		t.Fatalf("other scope=%+v", games)
	}
}

func TestReplaceGameInventoryCrossScopeAuditAndLegacyLineage(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("one", "PreMatch", 0, 0)
	result, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if result.SyncRun.TeamsUpserted != 0 || result.SyncRun.GamesSeen != 1 || result.SyncRun.FinishedAt != inventoryMetadata().FinishedAt || result.Audit.RowsInserted != 1 || result.Audit.ID == 0 {
		t.Fatalf("lineage=%+v audit=%+v", result.SyncRun, result.Audit)
	}
	state, ok, err := db.SourceResourceScopeState(ctx, SourceResourceGames, "2030", "Example")
	if err != nil || !ok || state.UpdatedAt != inventoryMetadata().FinishedAt || state.NextFullDueAt == nil {
		t.Fatalf("state=%+v,%v,%v", state, ok, err)
	}
	other := game
	other.Season = "2031"
	other.Stage = "Else"
	if _, err := db.ReplaceGameInventory(ctx, "2031", "Else", []Game{other}, nil, inventoryMetadata()); err == nil {
		t.Fatal("cross-scope id accepted")
	}
	if got, _ := db.seasonGames(ctx, "2030", "Example"); len(got) != 1 {
		t.Fatalf("original scope changed=%+v", got)
	}
}

func TestReplaceGameInventoryInventoryExpectationFormsAndParticipants(t *testing.T) {
	db, ctx := inventoryDB(t)
	first := inventoryGame("one", "PreMatch", 0, 0)
	second := inventoryGame("two", "PreMatch", 0, 0)
	second.KickoffUTC = "2030-01-02T12:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{first, second}, &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 2, Games: 2}, inventoryMetadata()); err != nil {
		t.Fatalf("explicit expectation: %v", err)
	}
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{first, second}, &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 2}, inventoryMetadata()); err != nil {
		t.Fatalf("derived expectation: %v", err)
	}
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "charlie", Name: "Charlie"}, {ASAID: "delta", Name: "Delta"}}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 3, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	bad := []Game{first, second}
	bad[1].HomeTeamID = "alpha"
	bad[1].AwayTeamID = "charlie"
	third := inventoryGame("three", "PreMatch", 0, 0)
	third.HomeTeamID = "alpha"
	third.AwayTeamID = "delta"
	third.KickoffUTC = "2030-01-03T12:00:00Z"
	fourth := inventoryGame("four", "PreMatch", 0, 0)
	fourth.HomeTeamID = "bravo"
	fourth.AwayTeamID = "charlie"
	fourth.KickoffUTC = "2030-01-04T12:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", append(bad, third, fourth), &competition.InventoryExpectation{Teams: 4, GamesPerTeam: 2, Games: 4}, inventoryMetadata()); err == nil {
		t.Fatal("uneven appearances accepted")
	}
}

func TestReplaceGameInventoryRollsBackOnLegacyAuditAndVenueTriggers(t *testing.T) {
	for _, target := range []string{"sync_runs", "source_refresh_audits", "venue_summaries"} {
		t.Run(target, func(t *testing.T) {
			db, ctx := inventoryDB(t)
			if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "charlie", Name: "Charlie"}}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 4, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
				t.Fatal(err)
			}
			one := inventoryGame("one", "FullTime", 1, 0)
			two := inventoryGame("two", "FullTime", 2, 1)
			two.KickoffUTC = "2030-01-02T12:00:00Z"
			if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{one, two}, nil, inventoryMetadata()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ReplaceGameXG(ctx, "2030", "Example", []Game{one, two}, []GameXG{{GameID: "one", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1, Valid: true}}}, time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			beforeGames, _ := db.seasonGames(ctx, "2030", "Example")
			beforeVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
			beforeAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
			triggerStatements := map[string]string{
				"sync_runs":             "CREATE TRIGGER abort_insert BEFORE INSERT ON sync_runs BEGIN SELECT RAISE(ABORT, 'stop'); END",
				"source_refresh_audits": "CREATE TRIGGER abort_insert BEFORE INSERT ON source_refresh_audits BEGIN SELECT RAISE(ABORT, 'stop'); END",
				"venue_summaries":       "CREATE TRIGGER abort_insert BEFORE UPDATE ON venue_summaries BEGIN SELECT RAISE(ABORT, 'stop'); END",
			}
			if _, err := db.db.ExecContext(ctx, triggerStatements[target]); err != nil {
				t.Fatal(err)
			}
			one.HomeTeamID = "charlie"
			one.LastUpdatedUTC = "2030-01-01T12:00:00Z"
			if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{one}, nil, inventoryMetadata()); err == nil {
				t.Fatal("trigger did not abort")
			}
			afterGames, _ := db.seasonGames(ctx, "2030", "Example")
			afterVenue, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
			afterAudits, _ := db.SourceRefreshAudits(ctx, SourceResourceGames, "2030", "Example")
			var xg int
			_ = db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_xg WHERE asa_game_id='one'`).Scan(&xg)
			if !reflect.DeepEqual(beforeGames, afterGames) || !reflect.DeepEqual(beforeVenue, afterVenue) || !reflect.DeepEqual(beforeAudits, afterAudits) || xg != 1 {
				t.Fatalf("rollback games=%+v venue=%+v audits=%+v xg=%d", afterGames, afterVenue, afterAudits, xg)
			}
		})
	}
}

func TestReplaceGameInventorySourcePreferenceProtectsTerminalAndStaleRows(t *testing.T) {
	db, ctx := inventoryDB(t)
	terminal := inventoryGame("one", "FullTime", 2, 1)
	terminal.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{terminal}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	regression := terminal
	regression.Status = "PreMatch"
	regression.HomeScore = sql.NullInt64{}
	regression.AwayScore = sql.NullInt64{}
	regression.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	result, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{regression}, nil, inventoryMetadata())
	if err != nil || result.Audit.RowsUnchanged != 1 {
		t.Fatalf("terminal regression=%+v,%v", result, err)
	}
	equal := terminal
	equal.HomeScore.Int64 = 4
	result, err = db.ReplaceGameInventory(ctx, "2030", "Example", []Game{equal}, nil, inventoryMetadata())
	if err != nil || result.Audit.RowsUnchanged != 1 {
		t.Fatalf("equal timestamp=%+v,%v", result, err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE games SET last_updated_utc='broken' WHERE asa_game_id='one'`); err != nil {
		t.Fatal(err)
	}
	newer := terminal
	newer.HomeScore.Int64 = 3
	newer.LastUpdatedUTC = "2030-01-01T14:00:00Z"
	result, err = db.ReplaceGameInventory(ctx, "2030", "Example", []Game{newer}, nil, inventoryMetadata())
	if err != nil || result.Audit.RowsUpdated != 1 || !result.Audit.DownstreamInputsChanged {
		t.Fatalf("malformed cached fallback=%+v,%v", result, err)
	}
}

func TestReplaceGameInventoryVenueFixtureAndXGInvalidationRules(t *testing.T) {
	db, ctx := inventoryDB(t)
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "charlie", Name: "Charlie"}}, FullRefreshMetadata{Trigger: SourceTriggerCLI, StartedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 2, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	first := inventoryGame("one", "FullTime", 2, 1)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{first}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceGameXG(ctx, "2030", "Example", []Game{first}, []GameXG{{GameID: "one", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: .5, Valid: true}}}, time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	score := first
	score.HomeScore.Int64 = 3
	score.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{score}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	summary, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if len(summary) != 1 || !summary[0].XGReady || summary[0].HomeGoals != 3 || summary[0].XGMatches != 1 {
		t.Fatalf("score correction venue=%+v", summary)
	}
	second := inventoryGame("two", "FullTime", 1, 0)
	second.KickoffUTC = "2030-01-02T12:00:00Z"
	second.LastUpdatedUTC = "2030-01-02T11:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{score, second}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	summary, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if summary[0].XGReady || summary[0].XGMatches != 1 || summary[0].Matches != 2 {
		t.Fatalf("completed addition venue=%+v", summary[0])
	}
	participant := score
	participant.HomeTeamID = "charlie"
	participant.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{participant, second}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	var xgCount int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_xg WHERE asa_game_id='one'`).Scan(&xgCount); err != nil {
		t.Fatal(err)
	}
	summary, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if xgCount != 0 || summary[0].XGReady || summary[0].XGMatches != 0 {
		t.Fatalf("participant xg cleanup count=%d summary=%+v", xgCount, summary[0])
	}
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{participant}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	summary, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if summary[0].Matches != 1 || summary[0].XGReady {
		t.Fatalf("completed deletion venue=%+v", summary[0])
	}
}

func TestReplaceGameInventoryFullTimeEligibilityControlsXGInvalidation(t *testing.T) {
	db, ctx := inventoryDB(t)
	full := inventoryGame("one", "FullTime", 1, 0)
	full.LastUpdatedUTC = "2030-01-01T12:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{full}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceGameXG(ctx, "2030", "Example", []Game{full}, []GameXG{{GameID: "one", Availability: XGAvailable, HomeTeamID: "alpha", AwayTeamID: "bravo", HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: .5, Valid: true}}}, time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	abandoned := inventoryGame("abandoned", "Abandoned", 0, 0)
	abandoned.KickoffUTC = "2030-01-02T12:00:00Z"
	abandoned.LastUpdatedUTC = "2030-01-02T11:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{full, abandoned}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	summary, _ := db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !summary[0].XGReady || summary[0].XGMatches != 1 {
		t.Fatalf("abandoned insertion invalidated xG: %+v", summary[0])
	}
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{full}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	summary, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if !summary[0].XGReady || summary[0].XGMatches != 1 {
		t.Fatalf("abandoned deletion invalidated xG: %+v", summary[0])
	}
	toAbandoned := full
	toAbandoned.Status = "Abandoned"
	toAbandoned.HomeScore = sql.NullInt64{}
	toAbandoned.AwayScore = sql.NullInt64{}
	toAbandoned.LastUpdatedUTC = "2030-01-01T13:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{toAbandoned}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	summary, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if summary[0].XGReady || summary[0].XGMatches != 0 || summary[0].HomeXG != 0 {
		t.Fatalf("FullTime to Abandoned = %+v", summary[0])
	}
	backToFull := full
	backToFull.LastUpdatedUTC = "2030-01-01T14:00:00Z"
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{backToFull}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	summary, _ = db.VenueSummaries(ctx, []string{"2030"}, "Example")
	if summary[0].XGReady || summary[0].XGMatches != 1 || summary[0].HomeXG != 1 {
		t.Fatalf("Abandoned to FullTime = %+v", summary[0])
	}
}

func TestReplaceGameInventoryKeepsLegacyReplaceSeasonCompatible(t *testing.T) {
	db, ctx := inventoryDB(t)
	game := inventoryGame("one", "PreMatch", 0, 0)
	if _, err := db.ReplaceGameInventory(ctx, "2030", "Example", []Game{game}, nil, inventoryMetadata()); err != nil {
		t.Fatal(err)
	}
	teams := []Team{{ASAID: "alpha", Name: "Alpha"}, {ASAID: "bravo", Name: "Bravo"}}
	game.LastUpdatedUTC = "2030-01-01T14:00:00Z"
	if _, err := db.ReplaceSeason(ctx, "2030", "Example", teams, []Game{game}, time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Season(ctx, "2030", "Example"); err != nil {
		t.Fatalf("legacy season after new inventory: %v", err)
	}
}
