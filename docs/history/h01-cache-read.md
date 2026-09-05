# H01 — Consistent historical regular-season cache read

## Control

- Status: **Complete** (implementation and Sol review accepted).
- Mode: implementation when explicitly assigned; this document is a plan.
- Implementation: Terra. Review: Sol, focused on snapshot and missing-data semantics.
- Prerequisites: existing catalog, source-scope, and transaction-aware cache
  helpers confirmed in the planning baseline; no other History packet needed.
- Blocks: H02–H05.
- Goal: one read-only snapshot containing all supported regular seasons,
  including explicit entries for unloaded years.

## Read first

Read the shared contract/checks in [README](README.md), then
`internal/cache/cache.go` around `Season`, `loadSeasonData`, and `queryer`;
`internal/cache/season_readiness.go`; `internal/cache/source_scopes.go`;
`internal/competition/catalog.go`; and cache test setup patterns.

## Fixed interface and decisions

Add `internal/cache/history.go` exposing:

```go
type HistoricalSeason struct {
    Entry     competition.Entry
    Readiness *SeasonReadinessSnapshot // nil means no persisted scope
    Data      SeasonData
}

func (c *DB) HistoricalRegularSeasons(ctx context.Context) ([]HistoricalSeason, error)
```

Return one row per public, source-backed, fixture-capable `Regular Season`
catalog entry, in ascending numeric season order. Do not use a numeric year
range or the primary-stage list. Include missing/not-published scopes; they
are meaningful absence, not a reason to omit a year. No user-supplied season
list or arbitrary SQL scope is needed in this interface.

Use exactly one read-only transaction. Inside it, call the existing
transaction-aware `seasonReadiness` and `loadSeasonData` helpers for each
selected entry. Do not call public `Season` or `SeasonReadiness` from this
loop: they begin independent transactions and could mix correction snapshots.
The existing full `SeasonData` is intentionally reused to retain fixture
snapshot validation; do not introduce a faster raw query that bypasses it.
It loads some unused fields at this archive size; optimization is deferred.

A missing scope yields nil readiness, not fabricated complete or active
metadata. Missing fixtures yield empty data through existing helpers. An actual
query/scan/snapshot-integrity error fails the whole call with season/stage
context and no partial return. Honor context cancellation and close/rollback
on every path. Reading must never create source scopes or update audit state.

## Allowed changes

- Add `internal/cache/history.go` and `internal/cache/history_test.go`.
- Update `docs/sync-logic-guide.md` with a short section describing the new
  cache-only multi-season read, explicitly noting no new refresh behavior.
- No changes to existing cache helpers, schema, catalog, scheduler, or sync.

If existing helpers cannot meet this contract, report the exact conflict
rather than widening the allowed list.

## Implementation steps

1. Filter/sort catalog entries once per call; do not mutate catalog storage.
2. Begin the read transaction and defer the repository rollback helper.
3. Load each row's readiness and season data with that transaction; copy
   readiness into a distinct row value before storing its address.
4. Wrap errors with the failing season/stage, return nil plus error, and
   preserve cancellation identity for `errors.Is`.
5. Add tests using temporary migrated SQLite databases and established writers.
6. Document the delivered read API and execute the checks below.

## Required tests

Use `TestHistoricalRegularSeasons...` names so the focused command is reliable.

- Empty migrated DB: full supported regular-season catalog, ascending order,
  empty games, nil readiness where scopes have not been seeded; no cup row in
  2020 and no invented 2013–2015 or 2020 regular season.
- Seed regular seasons plus playoffs/cups in the same database; only the exact
  regular-season data is returned, regardless of configured/current stage.
- Completed, active, unknown and not-published scope metadata are preserved;
  historical unknown inventory stays unknown. Do not require real full seasons.
- Preserve nullable paired xG and xPoints independently, including a valid
  xG observation with absent xPoints. Do not coerce SQL null to zero.
- A cache correction through existing write APIs is visible on the next read;
  no stale process cache. Existing fixture snapshot validation remains active.
- Cancelled context returns an error matching context cancellation; closed DB
  and a deliberately corrupted snapshot return errors, never a partial archive.
- Capture fixture/scope/audit table contents before and after a read and verify
  no writes. Review confirms every query uses the same transaction. Do not add
  exported test hooks or timing/sleep-based concurrency tests to prove this.

## Verification

```sh
NWSL_CONFIG_FILE=/dev/null go test -count=1 ./internal/cache -run '^TestHistoricalRegularSeasons'
```

Then run every shared Go check and `go test -race ./...`. Review the transaction
boundary explicitly; passing tests alone do not establish snapshot consistency.

## Non-goals and stop conditions

No aggregate calculations, routes, migration, loading, historical metadata
research, source calls, or cache performance redesign. Stop for helper conflicts,
new persistence needs, or any file required outside the allowed set.

## Handoff

Use the shared handoff format. Include the exact exported types/method and how
missing scopes, corruption, cancellation, and snapshot consistency were verified.
Do not change status or begin H02 without the owner's instruction and review.
