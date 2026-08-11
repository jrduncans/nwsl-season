package cache

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestMigrationTenPreservesSourceScopeTableAndConstraints(t *testing.T) {
	ctx := context.Background()
	db := openSourceScopeTestDB(t)

	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 12 || version != schemaVersion {
		t.Fatalf("schema version = %d, want 12", version)
	}
	_, err := db.db.ExecContext(ctx, `INSERT INTO source_scopes (
		season, stage, registration, lifecycle, discovery, registered_at, updated_at
	) VALUES ('2026', 'Regular Season', 'invalid', 'active', 'unknown', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("source_scopes accepted an invalid registration")
	}
}

func TestMigrationNineBackfillsObservedScopeIdentities(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/cache.sqlite"
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (8, '2026-01-01T00:00:00Z')`,
		`CREATE TABLE games (season TEXT NOT NULL, stage TEXT NOT NULL, status TEXT NOT NULL)`,
		`CREATE TABLE sync_runs (season TEXT NOT NULL, stage TEXT NOT NULL, outcome TEXT NOT NULL)`,
		`CREATE TABLE xg_sync_runs (season TEXT NOT NULL, stage TEXT NOT NULL, outcome TEXT NOT NULL)`,
		`INSERT INTO games VALUES ('2024', 'Terminal Scope', 'FullTime')`,
		`INSERT INTO sync_runs VALUES ('2025', 'Published Empty', 'success')`,
		`INSERT INTO sync_runs VALUES ('2023', 'Failed Only', 'failure')`,
		`INSERT INTO xg_sync_runs VALUES ('2022', 'xG Only', 'failure')`,
	} {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			_ = legacy.Close()
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scopes, err := db.SourceScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]SourceScope, len(scopes))
	for _, scope := range scopes {
		got[scope.Season+"/"+scope.Stage] = scope
	}
	for key, discovery := range map[string]SourceScopeDiscovery{
		"2024/Terminal Scope":  SourceScopeAvailable,
		"2025/Published Empty": SourceScopeNotPublished,
		"2023/Failed Only":     SourceScopeUnknown,
		"2022/xG Only":         SourceScopeUnknown,
	} {
		scope, ok := got[key]
		if !ok {
			t.Fatalf("missing backfilled scope %q", key)
		}
		if scope.Registration != SourceScopeObserved || scope.Discovery != discovery || scope.Lifecycle != SourceScopeActive {
			t.Fatalf("backfilled %q = %+v", key, scope)
		}
		if !scope.RegisteredAt.Equal(scope.UpdatedAt) || scope.RegisteredAt.Location() != time.UTC {
			t.Fatalf("backfilled timestamps for %q = %s / %s", key, scope.RegisteredAt, scope.UpdatedAt)
		}
	}
}

func TestEnsureSourceScopesSeedsAndMergesAtFixedClock(t *testing.T) {
	ctx := context.Background()
	db := openSourceScopeTestDB(t)
	now := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.FixedZone("test", -7*60*60))
	scopes, err := db.EnsureSourceScopes(ctx, "2026", "Regular Season", now)
	if err != nil {
		t.Fatal(err)
	}
	want := []SourceScope{
		{Season: "2027", Stage: "Regular Season", Registration: SourceScopeProvisional, Lifecycle: SourceScopeUpcoming, Discovery: SourceScopeUnknown, RegisteredAt: now.UTC(), UpdatedAt: now.UTC()},
		{Season: "2026", Stage: "Regular Season", Registration: SourceScopeCatalog, Lifecycle: SourceScopeActive, Discovery: SourceScopeUnknown, RegisteredAt: now.UTC(), UpdatedAt: now.UTC()},
	}
	if !reflect.DeepEqual(scopes, want) {
		t.Fatalf("seeded scopes = %+v, want %+v", scopes, want)
	}
}

func TestEnsureSourceScopesRetainsStaleConfiguredScope(t *testing.T) {
	ctx := context.Background()
	db := openSourceScopeTestDB(t)
	scopes, err := db.EnsureSourceScopes(ctx, "1999", "Invented", time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 3 {
		t.Fatalf("seeded scope count = %d, want 3", len(scopes))
	}
	stale, found, err := db.SourceScope(ctx, "1999", "Invented")
	if err != nil || !found {
		t.Fatalf("stale configured scope = %+v, %t, %v", stale, found, err)
	}
	if stale.Registration != SourceScopeConfigured || stale.Lifecycle != SourceScopeActive {
		t.Fatalf("stale configured scope = %+v", stale)
	}
}

func TestMergeSourceScopeRegistrationPrecedence(t *testing.T) {
	registrations := []SourceScopeRegistration{SourceScopeCatalog, SourceScopeConfigured, SourceScopeProvisional, SourceScopeObserved}
	for _, existing := range registrations {
		for _, incoming := range registrations {
			t.Run(string(existing)+"/"+string(incoming), func(t *testing.T) {
				merged, _ := mergeSourceScope(SourceScope{Season: "2026", Registration: existing, Lifecycle: SourceScopeActive}, sourceScopeSeed{registration: incoming}, 2026)
				want := existing
				if sourceScopeRegistrationPrecedence(incoming) > sourceScopeRegistrationPrecedence(existing) {
					want = incoming
				}
				if merged.Registration != want {
					t.Fatalf("merge registration = %q, want %q", merged.Registration, want)
				}
			})
		}
	}
}

func TestEnsureSourceScopesIsIdempotentAndPreservesDiscovery(t *testing.T) {
	ctx := context.Background()
	db := openSourceScopeTestDB(t)
	firstNow := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.EnsureSourceScopes(ctx, "2026", "Regular Season", firstNow); err != nil {
		t.Fatal(err)
	}
	before, found, err := db.SourceScope(ctx, "2026", "Regular Season")
	if err != nil || !found {
		t.Fatalf("first scope = %+v, %t, %v", before, found, err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE source_scopes SET discovery = ? WHERE season = ? AND stage = ?`, SourceScopeAvailable, "2026", "Regular Season"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE source_scopes SET discovery = ? WHERE season = ? AND stage = ?`, SourceScopeNotPublished, "2027", "Regular Season"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSourceScopes(ctx, "2026", "Regular Season", firstNow.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, found, err := db.SourceScope(ctx, "2026", "Regular Season")
	if err != nil || !found {
		t.Fatalf("second scope = %+v, %t, %v", after, found, err)
	}
	if !after.RegisteredAt.Equal(before.RegisteredAt) || !after.UpdatedAt.Equal(before.UpdatedAt) || after.Discovery != SourceScopeAvailable {
		t.Fatalf("idempotent seed changed scope from %+v to %+v", before, after)
	}
	provisional, found, err := db.SourceScope(ctx, "2027", "Regular Season")
	if err != nil || !found || provisional.Discovery != SourceScopeNotPublished {
		t.Fatalf("provisional discovery = %+v, %t, %v", provisional, found, err)
	}
}

func TestEnsureSourceScopesPromotesRetainedUpcomingScopesWithoutChangingCompleted(t *testing.T) {
	ctx := context.Background()
	db := openSourceScopeTestDB(t)
	if _, err := db.EnsureSourceScopes(ctx, "2026", "Regular Season", time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE source_scopes SET lifecycle = ? WHERE season = ? AND stage = ?`, SourceScopeCompleted, "2026", "Regular Season"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureSourceScopes(ctx, "2026", "Regular Season", time.Date(2028, time.January, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	active, found, err := db.SourceScope(ctx, "2027", "Regular Season")
	if err != nil || !found || active.Lifecycle != SourceScopeActive {
		t.Fatalf("former future scope = %+v, %t, %v", active, found, err)
	}
	completed, found, err := db.SourceScope(ctx, "2026", "Regular Season")
	if err != nil || !found || completed.Lifecycle != SourceScopeCompleted {
		t.Fatalf("completed scope = %+v, %t, %v", completed, found, err)
	}
	future, found, err := db.SourceScope(ctx, "2029", "Regular Season")
	if err != nil || !found || future.Lifecycle != SourceScopeUpcoming {
		t.Fatalf("new future scope = %+v, %t, %v", future, found, err)
	}
}

func TestEnsureSourceScopesValidationAndReadOrdering(t *testing.T) {
	ctx := context.Background()
	db := openSourceScopeTestDB(t)
	for _, call := range []struct {
		season, stage string
		now           time.Time
	}{
		{" ", "Regular Season", time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"2026", " ", time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"2026", "Regular Season", time.Time{}},
	} {
		if _, err := db.EnsureSourceScopes(ctx, call.season, call.stage, call.now); err == nil {
			t.Fatalf("EnsureSourceScopes(%q, %q, %s) succeeded", call.season, call.stage, call.now)
		}
	}
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_scopes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rows after invalid seeds = %d, %v; want 0", count, err)
	}

	stamp := "2026-01-01T00:00:00Z"
	for _, scope := range [][2]string{{"2026", "Z"}, {"2027", "B"}, {"2027", "A"}} {
		if _, err := db.db.ExecContext(ctx, `INSERT INTO source_scopes VALUES (?, ?, 'observed', 'active', 'not_published', ?, ?)`, scope[0], scope[1], stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	scopes, err := db.SourceScopes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := [][2]string{{scopes[0].Season, scopes[0].Stage}, {scopes[1].Season, scopes[1].Stage}, {scopes[2].Season, scopes[2].Stage}}
	want := [][2]string{{"2027", "A"}, {"2027", "B"}, {"2026", "Z"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scope ordering = %v, want %v", got, want)
	}
	if scope, found, err := db.SourceScope(ctx, "missing", "scope"); err != nil || found || scope != (SourceScope{}) {
		t.Fatalf("missing source scope = %+v, %t, %v", scope, found, err)
	}
}

func openSourceScopeTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
