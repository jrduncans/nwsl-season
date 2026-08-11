# P1-02: Persisted source-scope registry

## Control

- Status: Complete
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P1-01 competition catalog core
- Blocks: request-scoped capabilities, historical readiness APIs, and the
  multi-scope loading planner

## Goal

Persist the season/stage scopes that the loader must retain, and seed that
registry from verified catalog entries, the configured primary scope, existing
source observations, and provisional current- and next-calendar-year regular
seasons without changing which scopes the scheduler requests.

## Why this packet exists

Phase 1 of the
[ASA data catalog and loading plan](../../asa-data-catalog-and-loading-plan.md)
separates the scopes worth checking from scopes with fully verified product
rules. P1-01 created the immutable competition catalog, but the process still
has no durable record of provisional or previously observed source scopes.

The migration belongs in `internal/cache`, whose existing schema version is 8.
The server and maintenance command already know the configured primary scope
after opening the database, so they are the narrow integration points for
idempotent seeding. The current scheduler remains single-scope until a later
packet gives it a planner and refresh-state contract.

## Fixed decisions

### Phase 1 and Phase 2 schema boundary

This packet owns durable scope identity and the small amount of state needed to
distinguish an unpublished provisional scope from a scope with cached source
data:

- season and ASA stage;
- why the scope is retained;
- lifecycle (`upcoming`, `active`, or `completed`);
- discovery state (`unknown`, `not_published`, or `available`);
- registration and update timestamps.

Do not add resource, mode, trigger, attempt/success time, next-due time,
requested/returned counts, change counts, error text, or downstream
invalidation fields. Those belong to Phase 2's generalized per-resource source
state and audit records. Existing `sync_runs` and `xg_sync_runs` remain
unchanged in this packet.

### Catalog source iteration

Add this operation to `internal/competition/catalog.go`:

```go
func SourceEntries() []Entry
```

It returns defensive copies of all catalog entries whose `SourceAvailable`
field is true, including private entries. Sort it with the same deterministic
season-descending, primary-first, label-ascending order as `PublicEntries`.
Do not expose the private catalog slice or return entries whose source is
unavailable.

### Registry vocabulary

Add `internal/cache/source_scopes.go` with these exported string types and
constants:

```go
type SourceScopeRegistration string

const (
    SourceScopeCatalog     SourceScopeRegistration = "catalog"
    SourceScopeConfigured  SourceScopeRegistration = "configured"
    SourceScopeProvisional SourceScopeRegistration = "provisional"
    SourceScopeObserved    SourceScopeRegistration = "observed"
)

type SourceScopeLifecycle string

const (
    SourceScopeUpcoming  SourceScopeLifecycle = "upcoming"
    SourceScopeActive    SourceScopeLifecycle = "active"
    SourceScopeCompleted SourceScopeLifecycle = "completed"
)

type SourceScopeDiscovery string

const (
    SourceScopeUnknown      SourceScopeDiscovery = "unknown"
    SourceScopeNotPublished SourceScopeDiscovery = "not_published"
    SourceScopeAvailable    SourceScopeDiscovery = "available"
)

type SourceScope struct {
    Season       string
    Stage        string
    Registration SourceScopeRegistration
    Lifecycle    SourceScopeLifecycle
    Discovery    SourceScopeDiscovery
    RegisteredAt time.Time
    UpdatedAt    time.Time
}
```

Use ordinary `gofmt` formatting. `RegisteredAt` is the first time the registry
retained the identity. `UpdatedAt` changes only when a persisted value changes;
an idempotent seed must not create timestamp churn.

Add these operations:

```go
func (c *DB) EnsureSourceScopes(
    ctx context.Context,
    configuredSeason string,
    configuredStage string,
    now time.Time,
) ([]SourceScope, error)

func (c *DB) SourceScopes(ctx context.Context) ([]SourceScope, error)

func (c *DB) SourceScope(
    ctx context.Context,
    season string,
    stage string,
) (SourceScope, bool, error)
```

`EnsureSourceScopes` performs seeding and returns the same complete,
deterministically ordered view as `SourceScopes`. Keep any seed-planning or
merge helpers unexported.

### Schema version 9

Increment `schemaVersion` to 9. Migration 9 creates exactly this table (SQL
formatting may follow the surrounding file):

```sql
CREATE TABLE source_scopes (
    season TEXT NOT NULL,
    stage TEXT NOT NULL,
    registration TEXT NOT NULL
        CHECK (registration IN ('catalog','configured','provisional','observed')),
    lifecycle TEXT NOT NULL
        CHECK (lifecycle IN ('upcoming','active','completed')),
    discovery TEXT NOT NULL
        CHECK (discovery IN ('unknown','not_published','available')),
    registered_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (season, stage)
)
```

Migration 9 must retain season/stage identities already present in any of
`games`, `sync_runs`, or `xg_sync_runs`:

- insert one `observed` row for each distinct unioned identity;
- use `available` when at least one cached `games` row exists for the scope;
- otherwise use `not_published` when at least one successful `sync_runs` row
  exists;
- otherwise use `unknown`;
- initialize lifecycle as `active`; completion must never be inferred from
  historical year or from all currently cached games being terminal;
- use one captured UTC migration timestamp for both timestamp columns;
- tolerate a version-8 database in which the source tables exist but contain
  no rows.

Do not add foreign keys from existing source or audit tables to the registry.
The migration must preserve databases that contain manually loaded scopes.

### Seed set

`EnsureSourceScopes` validates that the configured season and stage are not
blank after trimming. It converts `now` to UTC and rejects its zero value.
Callers pass a clock value; the function must not call `time.Now` internally.

Build and merge this seed set by exact season/stage identity:

1. every `competition.SourceEntries()` value, registered as `catalog`;
2. the configured primary season/stage, registered as `configured`;
3. `<current UTC calendar year>/Regular Season`, registered as `provisional`;
4. `<next UTC calendar year>/Regular Season`, registered as `provisional`.

The calendar year comes from the UTC `now` argument, not the configured season
and not local time. This is intentionally independent of a stale deployment
configuration.

When more than one reason produces the same identity, retain the strongest
registration using this precedence:

```text
catalog > configured > provisional > observed
```

The precedence is descriptive only; all four registrations remain eligible
registry members. Never delete an existing row because it is absent from a
later seed set.

### Seed lifecycle and merge behavior

For a newly inserted seed:

- use `upcoming` only when its season parses as a four-digit year greater than
  the current UTC calendar year;
- otherwise use `active`;
- use discovery state `unknown` unless migration backfill already established
  a stronger observation.

For an existing row:

- upgrade registration only when the incoming reason has higher precedence;
- promote `upcoming` to `active` when its numeric season is no longer in the
  future;
- never change `completed` automatically;
- never demote `active` to `upcoming`;
- never regress `not_published` or `available` to `unknown`, and never regress
  `available` to `not_published`;
- preserve `RegisteredAt`;
- set `UpdatedAt` to the supplied UTC timestamp only if registration or
  lifecycle actually changes.

A nonnumeric configured or catalog season is valid and seeds as `active`.
Only the automatically generated rolling seasons are required to be numeric.

### Read behavior

`SourceScopes` returns every row sorted by season descending, then stage
ascending. `SourceScope` returns `(SourceScope{}, false, nil)` for an unknown
identity. Both operations parse timestamps as UTC and return descriptive errors
for query, scan, or time-parse failures.

### Startup integration

After `cache.Open` succeeds, call `EnsureSourceScopes` with one captured
`time.Now().UTC()` value in:

- `cmd/server.run`, before constructing the sync service or scheduler; and
- `cmd/sync.run`, before dispatching prune, recalculation, or sync behavior.

Treat a seeding failure as a startup/command failure and include context in the
returned or logged error. Do not log every seeded row. Do not make
`cache.Open` accept configuration or hide the wall clock inside the cache
package.

The registry is intentionally not consumed by `internal/scheduler` or
`internal/syncer` in this packet. The existing configured single-scope network
behavior must remain unchanged.

## Allowed changes

- Add `internal/cache/source_scopes.go`.
- Add `internal/cache/source_scopes_test.go`.
- Modify `internal/cache/cache.go` for schema version 9 and migration only.
- Modify `internal/cache/cache_test.go` only for migration compatibility tests
  that need the existing helpers.
- Modify `internal/competition/catalog.go` and
  `internal/competition/catalog_test.go` for `SourceEntries`.
- Modify `cmd/server/main.go` and its existing test file, if needed, only for
  startup seeding.
- Modify `cmd/sync/main.go` and its existing test file, if needed, only for
  startup seeding.

Do not edit the scheduler, syncer, ASA client, HTTP application, templates,
configuration vocabulary, or existing source/audit storage APIs.

## Required behavior

- Opening an old database applies migration 9 without losing cached or audit
  data.
- Previously observed scopes remain registered even when they are not in the
  catalog, configured primary scope, or rolling provisional window.
- A fresh database seeded on a 2026 timestamp contains the verified 2026
  catalog scope and provisional 2027 regular season; the catalog registration
  wins for the duplicate current-year identity.
- A stale configured primary scope remains registered while rolling current
  and next years follow the supplied clock.
- Repeated seeding is idempotent and cannot erase discovery or completion
  state.
- Advancing the supplied clock across a year boundary registers the new next
  season and promotes the former upcoming season to active.
- Registry creation alone performs no ASA request and changes no scheduler
  selection.

## Tests to add or update

Add deterministic tests covering at least:

1. `SourceEntries` filtering, ordering, and defensive copies. Use an unexported
   helper with invented entries; do not add fake production catalog rows.
2. A fresh schema reports migration version 9 and contains the exact table
   constraints or rejects invalid enum values through SQLite.
3. Migration from a version-8 fixture backfills distinct identities from games,
   successful and failed fixture runs, and xG-only runs with the required
   discovery precedence.
4. Migration backfill does not infer completed lifecycle for an old season or
   a scope whose cached games are all terminal.
5. Seeding at a fixed 2026 UTC instant with default configured scope produces
   exactly the expected merged registry identities, registrations, lifecycle,
   discovery state, and timestamps.
6. A stale invented configured scope is retained alongside the clock-derived
   current and next years.
7. Registration precedence for all four registration values through a private
   table-driven merge-helper test.
8. Repeated identical seeding preserves `RegisteredAt` and `UpdatedAt`.
9. Reseeding after a year boundary adds the new future scope and promotes the
   former future scope without changing completed rows.
10. Existing `available` and `not_published` discovery values never regress
    during seeding.
11. Blank configured season/stage and a zero clock return descriptive errors
    without partial writes.
12. `SourceScope` distinguishes a missing row; `SourceScopes` ordering is
    deterministic with invented seasons and stages.
13. Server and maintenance-command startup stop on a registry-seeding error,
    using the narrowest practical test seam. Do not require an ASA request.

Tests must not make network requests, depend on the real current year, or
mutate the production competition catalog.

## Verification

Run all of these commands from the repository root:

```text
gofmt -w internal/cache/source_scopes.go internal/cache/source_scopes_test.go internal/cache/cache.go internal/cache/cache_test.go internal/competition/catalog.go internal/competition/catalog_test.go cmd/server/main.go cmd/server/main_test.go cmd/sync/main.go cmd/sync/main_test.go
go test ./internal/competition ./internal/cache
go test ./cmd/server ./cmd/sync
go test ./...
go vet ./...
git diff --check
```

If a listed existing or optional test file did not change or does not exist,
omit it from the `gofmt` arguments. All other commands are mandatory.

## Non-goals

- Making registry scopes public or adding season/stage navigation.
- Resolving catalog capabilities or rules inside HTTP requests.
- Adding historical catalog metadata or playoff scopes.
- Polling the provisional scope or changing scheduler priorities.
- Adding inventory reconciliation or special empty-response behavior.
- Updating discovery state after a later source request.
- Adding generalized refresh audit/state, due-time, or targeted-upsert tables.
- Changing `ReplaceSeason`, `ReplaceGameXG`, `sync_runs`, or `xg_sync_runs`.
- Inferring completion from year, current inventory, or terminal games.
- Adding an operator registration API or command.

## Stop conditions

Stop and report without broadening the patch if:

- migration 9 cannot retain an existing season/stage identity without changing
  an existing source or audit table;
- automatic seeding requires scheduler or syncer behavior changes;
- current callers require a new configuration value;
- a source catalog entry cannot be iterated without exposing mutable catalog
  storage;
- implementation needs per-resource due times or audit semantics from Phase 2;
- the worktree contains a newer migration or source-scope registry that
  conflicts with this schema;
- the full suite exposes a pre-existing failure unrelated to the packet.

For a pre-existing failure, include the failing command and enough output to
distinguish it from a regression; do not repair unrelated code.

## Handoff

Report:

- files changed;
- migration and registry behavior implemented;
- the exact seed set at the fixed clock used by tests;
- every verification command and its outcome;
- any deviation from this packet;
- any schema concern that should be resolved before the request-scoped or
  Phase 2 refresh-state packets.
