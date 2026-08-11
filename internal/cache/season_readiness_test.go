package cache

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/competition"
)

var readinessTestTime = time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)

func TestSeasonReadinessMissingAndBlankScope(t *testing.T) {
	ctx := context.Background()
	db := openSeasonReadinessTestDB(t)

	snapshot, found, err := db.SeasonReadiness(ctx, "missing", "Regular Season")
	if err != nil || found || snapshot != (SeasonReadinessSnapshot{}) {
		t.Fatalf("missing readiness = %+v, %t, %v", snapshot, found, err)
	}
	for _, call := range [][2]string{{" ", "Regular Season"}, {"2026", " "}} {
		if _, _, err := db.SeasonReadiness(ctx, call[0], call[1]); err == nil || !strings.Contains(err.Error(), "blank") {
			t.Errorf("SeasonReadiness(%q, %q) error = %v, want blank scope error", call[0], call[1], err)
		}
	}
}

func TestSeasonReadinessMapsPersistedDiscoveryWithoutGames(t *testing.T) {
	ctx := context.Background()
	db := openSeasonReadinessTestDB(t)
	for _, test := range []struct {
		season    string
		discovery SourceScopeDiscovery
		want      SourceReadiness
	}{
		{"unknown", SourceScopeUnknown, SourceReadinessUnknown},
		{"not-published", SourceScopeNotPublished, SourceReadinessNotPublished},
		{"available", SourceScopeAvailable, SourceReadinessAvailable},
	} {
		insertReadinessScope(t, db, test.season, "Scope", test.discovery)
		snapshot, found, err := db.SeasonReadiness(ctx, test.season, "Scope")
		if err != nil || !found || snapshot.Readiness != test.want || snapshot.ObservedGames != 0 || snapshot.ObservedTeams != 0 {
			t.Errorf("%s readiness = %+v, %t, %v", test.season, snapshot, found, err)
		}
	}
}

func TestSeasonReadinessObservedGamesOverrideStaleDiscoveryWithoutMutation(t *testing.T) {
	ctx := context.Background()
	db := openSeasonReadinessTestDB(t)
	for _, discovery := range []SourceScopeDiscovery{SourceScopeUnknown, SourceScopeNotPublished} {
		season := string(discovery)
		insertReadinessScope(t, db, season, "Scope", discovery)
		before, found, err := db.SourceScope(ctx, season, "Scope")
		if err != nil || !found {
			t.Fatal(err)
		}
		putReadinessGames(t, db, season, "Scope", [][2]string{{"alpha", "bravo"}})
		snapshot, found, err := db.SeasonReadiness(ctx, season, "Scope")
		if err != nil || !found || snapshot.Readiness != SourceReadinessAvailable {
			t.Fatalf("readiness = %+v, %t, %v", snapshot, found, err)
		}
		after, found, err := db.SourceScope(ctx, season, "Scope")
		if err != nil || !found || after != before {
			t.Fatalf("scope after read = %+v, %t, %v; want unchanged %+v", after, found, err, before)
		}
	}
}

func TestSeasonReadinessObservedInventoryIsExactlyScoped(t *testing.T) {
	ctx := context.Background()
	db := openSeasonReadinessTestDB(t)
	insertReadinessScope(t, db, "2099", "Target", SourceScopeUnknown)
	insertReadinessScope(t, db, "2099", "Other", SourceScopeUnknown)
	insertReadinessScope(t, db, "2098", "Target", SourceScopeUnknown)
	putReadinessGames(t, db, "2099", "Target", [][2]string{{"alpha", "bravo"}, {"bravo", "alpha"}})
	putReadinessGames(t, db, "2099", "Other", [][2]string{{"charlie", "delta"}})
	putReadinessGames(t, db, "2098", "Target", [][2]string{{"echo", "foxtrot"}})
	if _, err := db.db.ExecContext(ctx, `INSERT INTO teams (asa_team_id, name, short_name, abbreviation, raw_json, updated_at) VALUES ('unused', 'Unused', 'Unused', 'UNU', '{}', ?)`, formatTime(readinessTestTime)); err != nil {
		t.Fatal(err)
	}

	snapshot, found, err := db.SeasonReadiness(ctx, "2099", "Target")
	if err != nil || !found || snapshot.ObservedGames != 2 || snapshot.ObservedTeams != 2 {
		t.Fatalf("target readiness = %+v, %t, %v", snapshot, found, err)
	}
}

func TestSeasonReadinessUncatalogedInventoryIsAlwaysUnknown(t *testing.T) {
	ctx := context.Background()
	db := openSeasonReadinessTestDB(t)
	for _, test := range []struct {
		season string
		games  [][2]string
	}{
		{"empty", nil},
		{"partial", [][2]string{{"alpha", "bravo"}}},
		{"populated", [][2]string{{"alpha", "bravo"}, {"bravo", "alpha"}}},
	} {
		insertReadinessScope(t, db, test.season, "Invented", SourceScopeUnknown)
		putReadinessGames(t, db, test.season, "Invented", test.games)
		snapshot, found, err := db.SeasonReadiness(ctx, test.season, "Invented")
		if err != nil || !found || snapshot.Completeness != InventoryCompletenessUnknown || snapshot.ExpectedInventory != nil {
			t.Errorf("%s readiness = %+v, %t, %v", test.season, snapshot, found, err)
		}
	}
}

func TestEvaluateSeasonReadinessChecksEveryInventoryDimension(t *testing.T) {
	scope := readinessScope(SourceScopeAvailable)
	expected := &competition.InventoryExpectation{Teams: 2, GamesPerTeam: 2, Games: 2}
	complete := observedInventory{teams: 2, games: 2, appearances: map[string]int{"alpha": 2, "bravo": 2}}
	_, got, err := evaluateSeasonReadiness(scope, complete, expected)
	if err != nil || got != InventoryCompletenessComplete {
		t.Fatalf("complete inventory = %q, %v", got, err)
	}
	for name, observed := range map[string]observedInventory{
		"teams":       {teams: 1, games: 2, appearances: map[string]int{"alpha": 2}},
		"games":       {teams: 2, games: 1, appearances: map[string]int{"alpha": 2, "bravo": 2}},
		"appearances": {teams: 2, games: 2, appearances: map[string]int{"alpha": 1, "bravo": 2}},
	} {
		_, got, err := evaluateSeasonReadiness(scope, observed, expected)
		if err != nil || got != InventoryCompletenessIncomplete {
			t.Errorf("%s mismatch = %q, %v", name, got, err)
		}
	}
}

func TestEvaluateSeasonReadinessUsesExplicitInventedPastInventory(t *testing.T) {
	appearances := make(map[string]int, 14)
	for i := 0; i < 14; i++ {
		appearances[string(rune('a'+i))] = 26
	}
	observed := observedInventory{teams: 14, games: 182, appearances: appearances}
	scope := readinessScope(SourceScopeUnknown)
	_, complete, err := evaluateSeasonReadiness(scope, observed, &competition.InventoryExpectation{Teams: 14, GamesPerTeam: 26, Games: 182})
	if err != nil || complete != InventoryCompletenessComplete {
		t.Fatalf("invented 14/26/182 inventory = %q, %v", complete, err)
	}
	_, incompatible, err := evaluateSeasonReadiness(scope, observed, &competition.InventoryExpectation{Teams: 16, GamesPerTeam: 30, Games: 240})
	if err != nil || incompatible != InventoryCompletenessIncomplete {
		t.Fatalf("invented 16/30/240 inventory = %q, %v", incompatible, err)
	}
}

func TestSeasonReadinessesListsPersistedScopesInSourceOrder(t *testing.T) {
	ctx := context.Background()
	db := openSeasonReadinessTestDB(t)
	empty, err := db.SeasonReadinesses(ctx)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty readiness list = %#v, %v", empty, err)
	}
	for _, scope := range [][2]string{{"2026", "Z"}, {"2027", "B"}, {"2027", "A"}} {
		insertReadinessScope(t, db, scope[0], scope[1], SourceScopeUnknown)
	}
	snapshots, err := db.SeasonReadinesses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make([][2]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		got = append(got, [2]string{snapshot.Scope.Season, snapshot.Scope.Stage})
	}
	want := [][2]string{{"2027", "A"}, {"2027", "B"}, {"2026", "Z"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readiness order = %v, want %v", got, want)
	}
}

func TestSeasonReadinessExpectedInventoryIsDefensive(t *testing.T) {
	ctx := context.Background()
	db := openSeasonReadinessTestDB(t)
	insertReadinessScope(t, db, "2026", "Regular Season", SourceScopeUnknown)
	first, found, err := db.SeasonReadiness(ctx, "2026", "Regular Season")
	if err != nil || !found || first.ExpectedInventory == nil {
		t.Fatalf("first readiness = %+v, %t, %v", first, found, err)
	}
	first.ExpectedInventory.Teams = 99
	second, found, err := db.SeasonReadiness(ctx, "2026", "Regular Season")
	if err != nil || !found || second.ExpectedInventory == nil || second.ExpectedInventory.Teams != 16 {
		t.Fatalf("second readiness = %+v, %t, %v", second, found, err)
	}
	entry, ok := competition.Lookup("2026", "Regular Season")
	if !ok || entry.Inventory.Teams != 16 {
		t.Fatalf("catalog inventory = %+v, %t", entry.Inventory, ok)
	}
}

func TestEvaluateSeasonReadinessRejectsInvalidPersistedEnums(t *testing.T) {
	base := readinessScope(SourceScopeUnknown)
	for name, mutate := range map[string]func(*SourceScope){
		"registration": func(scope *SourceScope) { scope.Registration = "bad" },
		"lifecycle":    func(scope *SourceScope) { scope.Lifecycle = "bad" },
		"discovery":    func(scope *SourceScope) { scope.Discovery = "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			scope := base
			mutate(&scope)
			if _, _, err := evaluateSeasonReadiness(scope, observedInventory{appearances: map[string]int{}}, nil); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("invalid %s error = %v", name, err)
			}
		})
	}
}

func TestSeasonReadinessReadsDoNotMutateCache(t *testing.T) {
	ctx := context.Background()
	db := openSeasonReadinessTestDB(t)
	insertReadinessScope(t, db, "2099", "Scope", SourceScopeNotPublished)
	putReadinessGames(t, db, "2099", "Scope", [][2]string{{"alpha", "bravo"}})
	before := readinessTableCounts(t, db)
	scopeBefore, found, err := db.SourceScope(ctx, "2099", "Scope")
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, found, err := db.SeasonReadiness(ctx, "2099", "Scope"); err != nil || !found {
		t.Fatalf("single readiness read = %t, %v", found, err)
	}
	if _, err := db.SeasonReadinesses(ctx); err != nil {
		t.Fatal(err)
	}
	after := readinessTableCounts(t, db)
	scopeAfter, found, err := db.SourceScope(ctx, "2099", "Scope")
	if err != nil || !found || !reflect.DeepEqual(before, after) || scopeAfter != scopeBefore {
		t.Fatalf("read changed cache: counts %v -> %v, scope %+v -> %+v, found=%t err=%v", before, after, scopeBefore, scopeAfter, found, err)
	}
}

func openSeasonReadinessTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func readinessScope(discovery SourceScopeDiscovery) SourceScope {
	return SourceScope{Season: "invented", Stage: "Scope", Registration: SourceScopeObserved, Lifecycle: SourceScopeActive, Discovery: discovery, RegisteredAt: readinessTestTime, UpdatedAt: readinessTestTime}
}

func insertReadinessScope(t *testing.T, db *DB, season, stage string, discovery SourceScopeDiscovery) {
	t.Helper()
	if _, err := db.db.ExecContext(context.Background(), `INSERT INTO source_scopes (season, stage, registration, lifecycle, discovery, registered_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, season, stage, SourceScopeObserved, SourceScopeActive, discovery, formatTime(readinessTestTime), formatTime(readinessTestTime)); err != nil {
		t.Fatal(err)
	}
}

func putReadinessGames(t *testing.T, db *DB, season, stage string, pairs [][2]string) {
	t.Helper()
	if len(pairs) == 0 {
		return
	}
	byID := map[string]Team{}
	games := make([]Game, 0, len(pairs))
	for index, pair := range pairs {
		for _, id := range pair {
			byID[id] = Team{ASAID: id, Name: id, ShortName: id, Abbreviation: id, RawJSON: "{}"}
		}
		games = append(games, cachedGame(season+"-"+stage+"-"+string(rune('a'+index)), season, stage, "FullTime", pair[0], pair[1], sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 0, Valid: true}))
	}
	teams := make([]Team, 0, len(byID))
	for _, team := range byID {
		teams = append(teams, team)
	}
	if _, err := db.ReplaceSeason(context.Background(), season, stage, teams, games, readinessTestTime); err != nil {
		t.Fatal(err)
	}
}

func readinessTableCounts(t *testing.T, db *DB) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"source_scopes", "games", "teams", "sync_runs", "xg_sync_runs"} {
		var count int
		if err := db.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	return counts
}
