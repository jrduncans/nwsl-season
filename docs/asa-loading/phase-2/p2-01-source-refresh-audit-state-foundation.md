# P2-01: Source-refresh audit/state foundation

## Control

- Status: Ready
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P1-02 persisted source-scope registry and P1-05 persisted season
  readiness
- Blocks: independent team persistence, authoritative and targeted game
  persistence, authoritative and targeted xG persistence, and the multi-scope
  loading planner

## Goal

Add an append-only generalized source-refresh audit and durable per-resource
full-refresh state, with truthful legacy-success backfill and cache APIs that
later source writes can use atomically, without changing current synchronization
or legacy audit behavior.

## Why this packet exists

Phase 2 of the
[ASA data catalog and loading plan](../../asa-data-catalog-and-loading-plan.md)
splits persistence by refresh semantics. The current `sync_runs` table combines
team and full-game work, while `xg_sync_runs` describes only the current
scope-wide xG operation. Neither can truthfully represent a targeted request,
its trigger, requested identities, deletion authority, or downstream impact.

P1-02 deliberately kept resource timing and audit data out of `source_scopes`.
This packet adds that missing metadata foundation before any source mutation is
split. It is additive: existing `sync_runs` IDs remain the lineage used by
qualification and scenarios, and existing status, health, telemetry, scheduler,
and syncer readers remain unchanged.

## Fixed decisions

### Migration 10

Increment `schemaVersion` to 10. Migration 10 creates these tables and index:

```sql
CREATE TABLE source_refresh_audits (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    resource TEXT NOT NULL
        CHECK (resource IN ('teams','games','game_xg')),
    season TEXT NOT NULL,
    stage TEXT NOT NULL,
    mode TEXT NOT NULL
        CHECK (mode IN ('full','targeted','recalculate')),
    trigger TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    outcome TEXT NOT NULL
        CHECK (outcome IN ('success','failure')),
    error_summary TEXT NOT NULL,
    requested_rows INTEGER NOT NULL CHECK (requested_rows >= 0),
    returned_rows INTEGER NOT NULL CHECK (returned_rows >= 0),
    rows_inserted INTEGER NOT NULL CHECK (rows_inserted >= 0),
    rows_updated INTEGER NOT NULL CHECK (rows_updated >= 0),
    rows_unchanged INTEGER NOT NULL CHECK (rows_unchanged >= 0),
    rows_deleted INTEGER NOT NULL CHECK (rows_deleted >= 0),
    downstream_inputs_changed INTEGER NOT NULL
        CHECK (downstream_inputs_changed IN (0,1)),
    CHECK (
        (resource = 'teams' AND season = '' AND stage = '') OR
        (resource IN ('games','game_xg') AND season <> '' AND stage <> '')
    ),
    CHECK (
        (outcome = 'success' AND error_summary = '') OR
        (outcome = 'failure' AND error_summary <> '')
    ),
    CHECK (mode = 'full' OR rows_deleted = 0)
);

CREATE INDEX source_refresh_audits_scope_idx
    ON source_refresh_audits (
        resource, season, stage, finished_at DESC, id DESC
    );

CREATE TABLE source_resource_scope_state (
    resource TEXT NOT NULL
        CHECK (resource IN ('teams','games','game_xg')),
    season TEXT NOT NULL,
    stage TEXT NOT NULL,
    last_full_success_at TEXT,
    next_full_due_at TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (resource, season, stage),
    CHECK (
        (resource = 'teams' AND season = '' AND stage = '') OR
        (resource IN ('games','game_xg') AND season <> '' AND stage <> '')
    )
);
```

Use the empty season and stage as the one global team-catalog identity. Games
and game xG always require a nonempty season and stage. Do not use nullable key
columns: SQLite permits multiple `NULL` values in otherwise unique composite
keys.

Do not add a foreign key from either new table to `source_scopes`. Existing
standalone cache users and legacy writes can have a valid resource audit before
startup seeding registers the corresponding source scope. Do not alter or
replace `sync_runs`, `xg_sync_runs`, `source_scopes`, or their indexes.

### Legacy state backfill

Migration 10 backfills only `source_resource_scope_state`; it does not invent
rows in `source_refresh_audits`.

- For `games`, insert one state for each `(season, stage)` with at least one
  successful legacy `sync_runs` row. Use the greatest successful `finished_at`
  as `last_full_success_at`.
- For global `teams`, insert one state at `('', '')` using the greatest
  successful legacy `sync_runs.finished_at` across all scopes.
- For `game_xg`, insert one state for each `(season, stage)` with at least one
  successful legacy `xg_sync_runs` row, using its greatest successful
  `finished_at`.
- Set every backfilled `next_full_due_at` to `NULL`.
- Capture one `time.Now().UTC()` value for all backfilled `updated_at` values in
  this migration.

Failures, cached rows, discovery state, lifecycle, terminal games, and season
age do not imply a full success. Do not backfill generalized audit rows: legacy
combined runs have no trustworthy resource split, trigger, requested count, or
downstream-impact value.

Migration helpers must inspect whether each legacy table and the needed
`season`, `stage`, `outcome`, and `finished_at` columns exist before querying
it. If a deliberately minimal old-schema test fixture lacks those columns,
skip that table's state backfill rather than failing the migration. A real
version-9 schema contains the full columns and must be backfilled.

### Exported vocabulary

Add `internal/cache/source_refresh.go` with these exported string types and
constants:

```go
type SourceResource string

const (
    SourceResourceTeams  SourceResource = "teams"
    SourceResourceGames  SourceResource = "games"
    SourceResourceGameXG SourceResource = "game_xg"
)

type SourceRefreshMode string

const (
    SourceRefreshFull        SourceRefreshMode = "full"
    SourceRefreshTargeted    SourceRefreshMode = "targeted"
    SourceRefreshRecalculate SourceRefreshMode = "recalculate"
)

type SourceRefreshOutcome string

const (
    SourceRefreshSuccess SourceRefreshOutcome = "success"
    SourceRefreshFailure SourceRefreshOutcome = "failure"
)

type SourceRefreshTrigger string

const (
    SourceTriggerScheduler    SourceRefreshTrigger = "scheduler"
    SourceTriggerStartup      SourceRefreshTrigger = "startup"
    SourceTriggerCLI          SourceRefreshTrigger = "cli"
    SourceTriggerBackfill     SourceRefreshTrigger = "backfill"
    SourceTriggerVenueHistory SourceRefreshTrigger = "venue_history"
)
```

`SourceRefreshTrigger` remains an open nonblank string vocabulary. Do not add a
SQL `CHECK` or Go allow-list for triggers; later operator and maintenance paths
may add precise trigger names without another migration.

Add these exported records:

```go
type SourceRefreshAudit struct {
    ID                      int64
    Resource                SourceResource
    Season                  string
    Stage                   string
    Mode                    SourceRefreshMode
    Trigger                 SourceRefreshTrigger
    StartedAt               time.Time
    FinishedAt              time.Time
    Outcome                 SourceRefreshOutcome
    ErrorSummary            string
    RequestedRows           int
    ReturnedRows            int
    RowsInserted            int
    RowsUpdated             int
    RowsUnchanged           int
    RowsDeleted             int
    DownstreamInputsChanged bool
}

type SourceResourceScopeState struct {
    Resource          SourceResource
    Season            string
    Stage             string
    LastFullSuccessAt *time.Time
    NextFullDueAt     *time.Time
    UpdatedAt         time.Time
}
```

All returned timestamps are UTC. Pointer timestamps must be defensive values;
mutating one returned record cannot affect another read.

### Metadata write API

Add:

```go
func (c *DB) RecordSourceRefresh(
    ctx context.Context,
    audit SourceRefreshAudit,
    nextFullDueAt *time.Time,
) (SourceRefreshAudit, error)
```

Validate the complete input before beginning a transaction:

- `ID` is zero;
- resource, mode, and outcome are known constants;
- teams use exactly `season == ""` and `stage == ""`;
- games and game xG use nonblank, already-trimmed season and stage;
- trigger is nonblank after trimming;
- start and finish are nonzero, and finish is not before start;
- every row count is nonnegative;
- success has an empty error summary and failure has a nonblank summary;
- targeted and recalculate audits have zero deleted rows;
- a failed or recalculate audit cannot claim downstream inputs changed; and
- `nextFullDueAt` is nil unless the audit is a successful full refresh. When
  present it is not before `FinishedAt`.

Do not require `returned_rows <= requested_rows`; a full collection request can
return rows without enumerating requested identities. Do not derive downstream
impact from row counts in this packet. The value is caller-supplied metadata
whose write-time policy belongs to later store-operation packets.

Within one transaction, insert the audit and return it with its assigned ID.
Only a successful `full` audit upserts `source_resource_scope_state`, setting:

- `last_full_success_at` to `audit.FinishedAt.UTC()`;
- `next_full_due_at` to the supplied UTC value or `NULL`; and
- `updated_at` to `audit.FinishedAt.UTC()`.

State is monotonic. Always append a valid delayed audit, but update existing
state only when its `FinishedAt` is later than the stored
`last_full_success_at`. An equal or older success cannot change the last
success, next due, or updated timestamp. Targeted, recalculate, and failed
audits never create or modify full-refresh state.

Implement the insertion/state update in an unexported helper that accepts a
transaction or the existing narrow SQL executor abstraction. Later team,
games, and xG write operations must be able to reuse it inside the same source
mutation transaction; they must not call the public method and create a second
transaction.

### Read APIs

Add:

```go
func (c *DB) SourceResourceScopeState(
    ctx context.Context,
    resource SourceResource,
    season string,
    stage string,
) (SourceResourceScopeState, bool, error)

func (c *DB) SourceResourceScopeStates(
    ctx context.Context,
) ([]SourceResourceScopeState, error)

func (c *DB) SourceRefreshAudits(
    ctx context.Context,
    resource SourceResource,
    season string,
    stage string,
) ([]SourceRefreshAudit, error)
```

The exact-state read returns the zero record, `false`, and nil error when no row
exists. The list returns an empty non-nil slice. Order states by resource in
`teams`, `games`, `game_xg` order, then season descending and stage ascending.

`SourceRefreshAudits` validates the same resource/scope identity and returns
only that exact identity, newest `finished_at` first and then greatest ID first.
It returns an empty non-nil slice when no rows match. It does not combine the
legacy audit tables with generalized audit history.

Reads return descriptive query, scan, timestamp-parse, enum, and boolean errors.
They do not seed scopes, update due times, prune history, or write anything.

### Compatibility and Phase 2 sequence

This packet is the metadata foundation only. The intended dependent sequence
is:

1. independent non-deleting team-catalog persistence;
2. authoritative full and non-deleting targeted game persistence, preserving
   complete post-write fixture snapshots and legacy `sync_runs` lineage;
3. authoritative full and targeted xG persistence, with omission semantics
   restricted to requested IDs; and
4. syncer operation decomposition and legacy `Run` compatibility before Phase
   3 changes scheduler selection and network hot paths.

Do not migrate qualification/scenario lineage away from `sync_runs` in Phase 2.
Do not make the scheduler consume `next_full_due_at` in this packet.

## Allowed changes

- Add `internal/cache/source_refresh.go`.
- Add `internal/cache/source_refresh_test.go`.
- Modify `internal/cache/cache.go` for schema version 10, migration SQL, and
  narrowly shared migration helpers.
- Modify `internal/cache/cache_test.go` and
  `internal/cache/source_scopes_test.go` only for migration compatibility.
- Update `docs/asa-loading/README.md` and this packet's control status during
  implementation and review.

Do not modify `internal/syncer`, `internal/scheduler`, the ASA client, commands,
configuration, HTTP, templates, competition catalog, source mutation methods,
legacy audit/status types or queries, qualification, scenarios, forecasts,
venue behavior, pruning, CSS, or JavaScript.

## Required behavior

- A real version-9 database upgrades without losing or rewriting any existing
  cache, scope, legacy audit, lease, venue, qualification, or scenario row.
- Full-success state is backfilled only where legacy success timestamps support
  it; no generalized audit history is fabricated.
- Generalized audits distinguish resource, exact scope, mode, trigger, counts,
  outcome, error, and downstream impact.
- Full replacement is the only mode whose audit can report deletions.
- Only a successful full audit creates or advances full-refresh state.
- Delayed older successes are retained as audits but cannot regress state.
- Team metadata has one global identity; games and xG remain isolated by exact
  season and stage.
- Metadata records and state changes are atomic, deterministic, and local to
  SQLite.
- Existing source-scope discovery/readiness and legacy sync/xG status behavior
  remain unchanged.

## Tests to add or update

Add deterministic tests covering at least:

1. Migration 10 creates both exact tables and the scope-history index, reports
   schema version 10, and enforces resource, mode, outcome, count, scope,
   outcome/error, boolean, and deletion-mode constraints.
2. A full version-9 fixture with successful and failed legacy game and xG runs
   backfills the exact greatest success timestamps for games, global teams, and
   game xG; failures and cached rows alone create no state, due times are nil,
   and generalized audit history is empty.
3. Migration preserves legacy rows and derived-data foreign-key lineage and is
   idempotent when the database is reopened.
4. The deliberately minimal version-8 migration fixture continues to open even
   though its legacy audit tables lack timestamps.
5. Successful full recording inserts the complete audit and creates state in
   one transaction; a later full success advances it and preserves UTC.
6. Targeted, recalculate, and failure audits append without creating or
   changing full state.
7. An older or equal full success remains queryable in audit history but cannot
   regress state or its next due time.
8. Invalid IDs, enums, scope shapes, whitespace, clocks, counts, error/outcome
   combinations, deletion claims, downstream claims, and next-due inputs fail
   before any audit or state row is written.
9. Exact state missing behavior, non-nil empty lists, state ordering, exact
   audit filtering, and newest-first audit ordering are deterministic.
10. Every audit field and nullable state timestamp round-trips, malformed stored
    timestamps/enums/booleans return descriptive errors through a private
    scanner seam, and returned pointer timestamps are independent.
11. Recording and reading metadata leaves `source_scopes` registration,
    lifecycle, discovery, and timestamps unchanged.
12. Existing `ReplaceSeason`, `ReplaceGameXG`, `Status`, `XGStatus`,
    `RefreshSnapshot`, syncer, scheduler, qualification, scenario, venue, and
    pruning tests pass without behavioral changes.

Use fixed timestamps and invented scopes. Tests make no network requests and do
not depend on the wall clock except to assert a migration timestamp is valid
UTC when the migration clock cannot be injected.

## Verification

Run all of these commands from the repository root:

```text
gofmt -w internal/cache/source_refresh.go internal/cache/source_refresh_test.go internal/cache/cache.go internal/cache/cache_test.go internal/cache/source_scopes_test.go
go test ./internal/cache
go test ./internal/syncer ./internal/scheduler ./internal/qualification ./internal/scenariorefresh
go test ./...
go vet ./...
git diff --check
```

If a listed existing or optional file did not change, omit it from the `gofmt`
arguments. All other commands are mandatory.

## Non-goals

- Recording generalized metadata from current source writes or failures.
- Changing `ReplaceSeason`, `ReplaceGameXG`, `RecordFailure`, or
  `RecordXGFailure`.
- Adding team, game, or xG source mutation APIs.
- Changing full-replacement deletion or targeted omission semantics.
- Updating discovery or lifecycle in `source_scopes`.
- Selecting due work, defining cadence durations, or running a planner.
- Adding per-game check timestamps, first-terminal/first-available observation,
  last-material-change, or next-due state.
- Pruning generalized audits.
- Changing legacy `sync_runs`/`xg_sync_runs` readers, responses, telemetry, or
  qualification/scenario foreign keys.
- Changing syncer, scheduler, network request volume, HTTP, or product behavior.

## Stop conditions

Stop and report without broadening the patch if:

- migration 10 cannot preserve legacy audit rows or derived-data foreign keys;
- truthful state backfill requires inventing a resource, trigger, request count,
  due time, downstream impact, or success absent from legacy data;
- atomic future reuse requires changing a current source mutation in this
  packet;
- a source resource needs nullable scope keys or a foreign key to
  `source_scopes` to satisfy current callers;
- implementation requires scheduler cadence, per-game state, targeted write,
  pruning, syncer, command, or network changes;
- the worktree contains a newer migration or generalized audit/state contract
  that conflicts with this packet; or
- the full suite exposes a pre-existing failure unrelated to the packet.

For a pre-existing failure, include the failing command and enough output to
distinguish it from a regression; do not repair unrelated code.

## Handoff

Report:

- files changed;
- migration, backfill, audit, and scope-state behavior implemented;
- exact validation and monotonic update rules;
- proof that legacy rows/readers and source scopes remain unchanged;
- proof that no generalized legacy audit history was fabricated;
- every verification command and its outcome;
- any deviation from this packet; and
- any issue the team, games, xG, compatibility-adapter, or planner packets
  should account for.
