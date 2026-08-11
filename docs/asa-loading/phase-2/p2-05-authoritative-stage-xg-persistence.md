# P2-05: Authoritative stage xG persistence

## Control

- Status: Ready
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P2-01 source-refresh audit/state foundation and P2-03
  authoritative game-inventory persistence
- Blocks: P2-06 targeted checked-xG persistence, split-operation compatibility
  adapter, and later xG planner/scheduler integration

## Goal

Add an authoritative cache API for one complete stage xG observation, with
committed-fixture validation, protected available values, deterministic
observation preference, legacy `xg_sync_runs` lineage, generalized full
audit/state, and atomic venue xG readiness, without changing schema, the
current syncer, or the network path.

## Why this packet exists

Phase 2 of the
[ASA data catalog and loading plan](../../asa-data-catalog-and-loading-plan.md)
requires separate authoritative full and non-deleting targeted xG store APIs.
The smallest coherent next slice is full persistence. It establishes complete
fixture enumeration, source-value preference, audit counts, full-scope state,
legacy lineage, and venue readiness. P2-06 can then add targeted check/due state
without reopening those rules.

The current `ReplaceGameXG` accepts a caller-provided fixture snapshot, uses
wall time, writes only legacy audit, and rejects omission of a cached available
value. It is also the current production `syncer.Store` boundary. This packet
adds a new cache-only method and leaves that legacy method and every caller
behaviorally unchanged.

Full and targeted xG are deliberately split. A complete response can
authoritatively represent an omitted completed game with the existing explicit
`XGUnavailable` value row. A targeted omission proves only that one requested
ID was checked and returned no value; it must not fabricate a `game_xg` row.
P2-06 therefore owns a separate check-state table and the additional requested/
returned/omitted and due-time rules.

## Fixed decisions

### No schema migration in P2-05

Keep `schemaVersion == 11`. Do not alter `game_xg`, `xg_sync_runs`, or any other
table and do not add an index. P2-05 uses the existing xG value/observation
columns exactly:

```text
game_xg.first_observed_at
game_xg.last_checked_at
```

`first_observed_at` remains the first time an available value was accepted.
`last_checked_at` remains the latest accepted full observation time for the
value/unavailable row. P2-05 does not claim to persist per-game due time or last
material-change time.

P2-06 is locked to introduce migration 12 with a separate
`game_xg_checks` table keyed by parent game. That separation allows a targeted
omission for a newly completed game to persist check/due state without
inventing an unavailable source-value row. Its migration may truthfully
backfill `last_checked_at` and first-available observation from existing
`game_xg.last_checked_at` and `first_observed_at`; it must leave last material
change and next due NULL. P2-06 must not move or remove the legacy columns,
because `ReplaceGameXG` compatibility still depends on them.

### Exported result and read APIs

Add:

```go
type XGRefreshResult struct {
    Audit  SourceRefreshAudit
    XGRun  *XGSyncRun
    Values []GameXG
}

func (c *DB) GameXGState(
    ctx context.Context,
    gameID string,
) (GameXG, bool, error)

func (c *DB) GameXGStates(
    ctx context.Context,
    season string,
    stage string,
) ([]GameXG, error)
```

Do not change `GameXG` or `XGSyncRun`. The exact read returns the zero record,
false, nil when absent. The scope read returns an empty non-nil slice, contains
only rows whose parent games remain in the exact scope, and orders by `GameID`
ascending. Validate trimmed nonblank IDs/scopes. Return UTC timestamps and
defensive `FirstObservedAt` pointers. Return descriptive query, scan, stored
identity, availability, and timestamp errors. Reads never mutate state.

`XGRefreshResult.Values` is the complete defensive post-write exact-scope xG
row set in the same `GameID` order. It includes preserved rows for cached games
that are currently non-FullTime. `XGRun` is always non-nil on success. Every
error returns the zero result.

Add no exported sentinel or typed error. xG identities must match already
cached games and teams, so there is no retryable unknown-team error. Input and
database-identity errors are deterministic and descriptive; multi-ID text sorts
IDs ascending and does not wrap `ErrUnknownGameTeams`.

### Exact authoritative API

Add:

```go
func (c *DB) ReplaceStageXG(
    ctx context.Context,
    season string,
    stage string,
    values []GameXG,
    metadata FullRefreshMetadata,
) (XGRefreshResult, error)
```

Load the authoritative fixture inventory from SQLite. Do not accept a caller
game slice. `values` is non-nil but may be empty. An empty cached scope or a
populated scope with no FullTime games is a valid full success with an empty
candidate set, complete generalized/legacy lineage, monotonic full-scope state,
and a ready zero-row venue xG summary.

### Caller validation before transaction

Validate before `BeginTx`:

- season/stage are nonblank and equal to their trimmed values;
- `values` is non-nil;
- every `GameID` is nonblank, trimmed, and unique;
- every row has `Availability == XGAvailable`;
- home/away team IDs are nonblank, trimmed, and distinct;
- home/away xG are both valid, finite, and nonnegative;
- home/away expected-points fields are either both NULL or both finite within
  the inclusive range `[0, MaxGameExpectedPoints]`;
- caller-controlled observation fields are unset:
  `FirstObservedAt == nil` and `LastCheckedAt.IsZero()`; and
- `FullRefreshMetadata` satisfies the shared open trigger, raw timestamp
  ordering, full due, UTC whole-second normalization, and defensive-copy rules.

Do not trim/rewrite values or mutate/reorder caller data. Prepare a fixed
`game_xg/full/success` audit through `prepareSourceRefresh` with
`RequestedRows == 0`, `ReturnedRows == len(values)`, and the supplied
`NextFullDueAt`. Fill only derived counts and downstream impact inside the
transaction.

### Cached identity and candidate set

Within the transaction and before mutation:

- load all cached games in the exact scope;
- require every returned ID to reference a cached exact-scope FullTime game;
- require returned home/away IDs to exactly match the cached game; and
- reject missing, cross-scope, non-FullTime, and participant-mismatched
  identities deterministically before any write.

Only exact `FullTime` with both scores is xG-eligible. `Abandoned` is terminal
for result cadence but not xG-eligible. Parent game/team foreign keys already
prove cached team identity; do not refresh teams or accept alternate
participants.

The candidate set is every cached exact-scope FullTime game, ordered by
`GameID`. It is independent of caller ordering. Returned values must be a
subset of that set; omission supplies the authoritative unavailable observation
below.

### Authoritative omission and deletion boundary

For each omitted candidate:

- if no xG row exists, insert an `XGUnavailable` marker with cached
  participants, NULL values, empty raw JSON, NULL first observation, and this
  normalized finish as `last_checked_at`;
- if an unavailable row exists, preserve its value fields and advance only its
  monotonic last check; and
- if an available row exists, preserve the complete available value and advance
  only its monotonic last check.

A full response can create an unavailable row because it enumerates the entire
stage collection. It never erases an available value merely because ASA omitted
it. The new API therefore replaces the legacy omission error with
available-over-unavailable preservation; legacy `ReplaceGameXG` retains its
current error behavior.

`ReplaceStageXG` never directly deletes `game_xg` and always reports
`RowsDeleted == 0`. Source omission cannot prove an xG retraction even though
generalized full mode is structurally allowed to delete. Parent-game deletion
and participant-change invalidation established by P2-03 remain the only xG
deletion paths. Preserve rows for cached non-FullTime games; exclude them from
candidate and venue calculations so a later result correction can make them
eligible again.

P2-06 targeted mode is locked to narrower authority: only explicit requested
FullTime IDs may be checked; omitted requested IDs update separate check/due
state but do not create/change `game_xg`; returned available values may upsert
only requested IDs; unrequested rows are untouched; available omission is
preserved; deletion is always zero; and targeted work never creates/advances
full-resource state. A caller must never pass a partial response to
`ReplaceStageXG`.

### Available/unavailable preference and materiality

Implement a package-private full evaluator that P2-06 can reuse for returned
available values. ASA has no xG source-update timestamp, so normalized
observation finish provides deterministic ordering:

- returned available data replaces an unavailable marker even for a delayed
  observation;
- identical available data is unchanged;
- differing returned data against cached available is accepted only when this
  finish is strictly later than stored `last_checked_at`; equal/older conflicts
  preserve cached data;
- availability, home/away xG, and home/away expected-points differences are
  material;
- an accepted `RawJSON`-only difference is a stored update but is not material;
  and
- first/check timestamp changes are not material consumer changes.

For every candidate, `last_checked_at` advances only to a strictly later
normalized finish. A returned available row sets `first_observed_at` if absent
and otherwise preserves the existing earliest value. An omission never creates
a first observation. Delayed successful audits and runs remain durable even
when row preference preserves newer cached data.

Material change is calculated in the transaction for audit/venue behavior but
is not persisted in P2-05. P2-06's separate check-state migration will backfill
last material change as NULL rather than pretending current rows reveal their
latest correction time.

### Counts, generalized full state, and legacy lineage

The three change counts describe every candidate exactly once:

```text
RowsInserted + RowsUpdated + RowsUnchanged == candidate count
```

An inserted unavailable marker counts inserted. An omitted preserved marker or
available row counts unchanged even when last check advances. An accepted
RawJSON-only difference counts updated. `ReturnedRows` remains the actual
source row count and need not equal candidate count.

The generalized audit is exact:

```text
Resource / scope:            game_xg / exact season-stage
Mode / outcome:              full / success
RequestedRows:               0
ReturnedRows:                len(values)
RowsInserted/Updated/Unchanged: exact candidate counts
RowsDeleted:                 0
DownstreamInputsChanged:     accepted material values or changed venue
                             readiness/aggregate values
```

Unavailable-marker insertion, omission, preserved delayed/equal data,
identical checks, RawJSON-only updates, and observation-time-only writes do not
by themselves set downstream impact. xG-only work never changes fixture
snapshots.

Every success inserts one `xg_sync_runs` row in the same transaction:

```text
StartedAt / FinishedAt: normalized metadata
Season / Stage:         exact scope
Outcome / ErrorSummary: success / ""
RowsSeen:               len(values)
AvailableGames:         candidate IDs ending available
UnavailableGames:       candidate IDs ending unavailable
RowsInserted/Updated/Unchanged: same candidate counts as generalized audit
```

`AvailableGames + UnavailableGames == candidate count`. Insert the legacy row
for empty, all-omitted, delayed, RawJSON-only, and no-op successes and return its
assigned ID.

Record generalized audit/full state through transaction-aware
`recordSourceRefresh` with normalized `NextFullDueAt`. Equal/older successes
remain in generalized and legacy history but cannot regress
`source_resource_scope_state`. Do not automatically write failure audits for
invalid input or rolled-back transactions.

### Venue xG readiness and atomic order

Refactor the existing venue xG writer narrowly so P2-06 can later reuse its
aggregate calculation while preserving readiness. A successful full
observation always recomputes exact-scope available-FullTime xG aggregates and
sets `xg_ready=1`, including a ready zero-row summary. Preserve every fixture
readiness/count/goal/points field.

Derive downstream impact from accepted material xG/xPoints changes plus any
change to venue `xg_ready`, `xg_matches`, `home_xg`, or `away_xg`. Exclude
`updated_at`: refreshing only the timestamp on an already ready identical full
observation does not claim changed inputs.

Within one transaction:

1. validate cached identities and load candidate/current xG rows;
2. capture pre-write venue xG readiness/aggregates;
3. evaluate/write every candidate deterministically;
4. reload the complete ordered exact-scope xG result;
5. recompute full venue xG summary/readiness;
6. insert successful legacy `xg_sync_runs`;
7. insert generalized audit and monotonic full state; and
8. commit.

Any xG row/history, venue, legacy run, generalized audit/state, result scan, or
commit failure rolls back the complete operation.

### Legacy compatibility boundary

Keep `ReplaceGameXG`, `RecordXGFailure`, `XGStatus`, and current callers
behaviorally unchanged. Legacy `ReplaceGameXG` retains its caller fixture
snapshot, wall-clock finish, omission rejection, counts, venue behavior, and
legacy-only audit. It does not call the new API or write generalized audit/full
state.

Keep `ReplaceSeason`, `ReplaceGameInventory`, `UpsertCheckedGames`, `Season`,
`RefreshSnapshot`, standings, forecasts, venue history, qualification,
scenarios, readiness, pruning, `syncer.Store`, and `Service.Run` behaviorally
unchanged. The new API does not write `sync_runs`, fixture snapshots,
qualification/scenario rows, source scopes, leases, or product output.

## Allowed changes

- Add `internal/cache/xg_inventory.go` and
  `internal/cache/xg_inventory_test.go`.
- Modify `internal/cache/cache.go` only for compatible xG read scanning and
  narrowly shared legacy validation/write/venue helpers; do not modify schema
  or `GameXG`/`XGSyncRun` fields.
- Modify `internal/cache/cache_test.go` for legacy compatibility.
- Modify `internal/cache/game_inventory_test.go` or
  `internal/cache/game_checks_test.go` only if needed to prove parent-game xG
  cascade/invalidation or venue behavior remains unchanged.
- Update `docs/asa-loading/README.md` and this packet status during review.

Do not modify migration/version tests, `internal/cache/source_refresh.go`,
`internal/syncer`, `internal/scheduler`, ASA clients/filters, commands,
configuration, HTTP, competition catalog, source scopes, qualification,
scenarios, forecasts, pruning behavior, templates, CSS, or JavaScript.

## Tests to add or update

Use fixed timestamps, invented scopes, and temporary SQLite databases. Make no
network request and do not depend on wall time. Cover:

1. Assert schema version remains 11 and no table/column/index migration appears.
2. Exact/missing/scoped reads: empty non-nil list, scope isolation, ID order,
   UTC normalization, defensive first-observation pointer, malformed stored
   identity/availability/timestamps, and no read side effects.
3. Caller validation: nil values; blank/padded/duplicate IDs; explicit
   unavailable input; invalid team IDs; mismatched numeric pairs;
   NaN/infinity/negative/range errors; caller-set observation fields; raw
   timestamp precision/order; and caller input nonmutation.
4. Database identity rejection for missing, cross-scope, PreMatch, Abandoned,
   and participant-mismatched games before xG, venue, legacy, generalized, or
   full-state mutation.
5. Empty/no-FullTime and populated full success: exact candidate/returned/count
   semantics, synthesized unavailable markers, protected available omission,
   preserved non-FullTime rows, complete deterministic result, legacy run,
   generalized audit/full state/due, and unchanged fixture snapshot.
6. Preference for unavailable-to-available, identical, RawJSON-only,
   numeric/xPoints corrections, equal/older conflicts, and delayed
   available-after-unavailable; exact row/material counts; earliest first
   observation and monotonic last check.
7. Caller order does not affect result/read/audit order or counts and caller
   values/slices remain unchanged.
8. Ready zero-row venue summary, readiness recovery, xG aggregate changes,
   xPoints-only materiality, fixture-field preservation, and downstream false
   for unavailable insertion, omission, no-op, delayed, RawJSON-only, and
   timestamp-only changes.
9. Test-only triggers on `game_xg`, `venue_summaries`, `xg_sync_runs`,
   `source_refresh_audits`, and `source_resource_scope_state` prove rollback of
   values/history, venue, legacy/generalized lineage, full state, and result.
10. Parent-game deletion/participant invalidation retains cascade semantics;
    the new API never deletes omitted available/unavailable or non-FullTime
    rows.
11. Legacy `ReplaceGameXG` omission errors, counts, clock/venue behavior,
    `RecordXGFailure`, and `XGStatus` remain unchanged and create no generalized
    xG audit/full state through the legacy path.
12. Existing P2-01 through P2-04, cache, syncer, scheduler, qualification,
    scenario, venue, forecast, readiness, and pruning tests pass unchanged.

## Verification

Run from the repository root:

```text
gofmt -w internal/cache/xg_inventory.go internal/cache/xg_inventory_test.go internal/cache/cache.go internal/cache/cache_test.go internal/cache/game_inventory_test.go internal/cache/game_checks_test.go
go test -count=1 ./internal/cache
go test -count=1 ./internal/syncer ./internal/scheduler ./internal/qualification ./internal/scenariorefresh
go test -count=1 ./...
go vet ./...
git diff --check
```

Omit unchanged optional files from `gofmt`; every other command is mandatory.

## Non-goals

- Any migration, `game_xg_checks`, per-game due/material-change persistence, or
  `UpsertCheckedXG`; those belong to P2-06.
- ASA `XGoalsFilters.GameID`, HTTP requests, batching/chunking, retries, or
  response conversion.
- Choosing due IDs, cadence durations/classes, five-minute/six-hour/daily
  policy, staggering, planner logic, scheduler wakeups, or leases.
- Syncer/command integration or replacing the current `Run` facade.
- Recording fetch/validation failures automatically.
- Inserting games/teams, changing xG participants, treating Abandoned as
  xG-eligible, or claiming source finality.
- Deleting xG because ASA omitted it.
- Fixture snapshots, qualification/scenario recalculation, forecast warming,
  historical evidence rebuilding, or source-scope/product changes.

## Stop conditions

Stop and report without broadening the patch if:

- full omission cannot create a truthful unavailable value marker without
  erasing cached available data;
- the evaluator cannot remain reusable for later targeted returned values
  without implementing targeted check state now;
- correct identity validation requires a teams/games network refresh;
- xG-only persistence requires changing fixture snapshots or
  qualification/scenario lineage;
- venue readiness and generalized full state cannot share the xG transaction;
- legacy compatibility requires routing `ReplaceGameXG` through the new API;
- implementation requires a migration or changing syncer, scheduler, client,
  command, configuration, source-scope, or product behavior; or
- the full suite exposes a pre-existing unrelated failure.

For a pre-existing failure, report the command and distinguishing output; do
not repair unrelated code.

## Handoff

Report:

- files changed and proof that schema/version remain unchanged;
- exported result/reads/full API, validation, and deterministic errors;
- candidate enumeration, omission protection, no deletion, preference, counts,
  and observation-clock semantics;
- generalized full audit/state and legacy `xg_sync_runs` lineage;
- venue readiness/aggregates and fixture-snapshot isolation;
- rollback/legacy compatibility evidence and verification results.

P2-06 may rely on complete reads, candidate/value validation, protected
availability, materiality, legacy-run insertion, generalized audit helpers, and
split venue mechanics. It adds migration 12 `game_xg_checks`, truthful backfill,
requested-ID authority, due replacement, targeted audits, and readiness-
preserving targeted venue updates. A later compatibility packet composes P2-02
through P2-06 behind the old `Run` facade before Phase 3 network/planner work.
