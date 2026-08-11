# P2-06: Targeted checked-xG persistence

## Control

- Status: Ready
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P2-04 targeted checked-game persistence and P2-05 authoritative
  stage xG persistence
- Blocks: split-operation compatibility adapter, due-xG planner selection, and
  later targeted xG network/scheduler integration

## Goal

Add a non-deleting targeted cache operation for an explicit batch of cached
FullTime game IDs. Persist each successful check and caller-supplied due time
separately from xG values, apply only preferred returned available values, keep
legacy and generalized audit lineage atomic, and update venue xG aggregates
without claiming authoritative full-stage readiness.

## Why this packet exists

The loading plan requires a due xG check to make no teams or games request and
to preserve an available value when ASA omits it. P2-05 intentionally could not
model that with `game_xg`: an authoritative full omission can create an
explicit unavailable observation, but a targeted omission proves only that one
requested identity was checked. Fabricating an unavailable value row would
confuse observation evidence with cadence state.

P2-04 already established caller-owned per-ID due times and generalized
targeted audit semantics. P2-05 established committed-game identity,
available-over-unavailable preference, observation clocks, complete xG reads,
legacy `xg_sync_runs`, and venue aggregates. This packet composes those cache
boundaries. It does not fetch ASA data or choose which IDs are due.

## Fixed decisions

### Migration 12: separate xG check state

Increment `schemaVersion` to 12. Migration 12 creates exactly:

```sql
CREATE TABLE game_xg_checks (
    asa_game_id TEXT PRIMARY KEY
        REFERENCES games(asa_game_id) ON DELETE CASCADE,
    last_checked_at TEXT NOT NULL,
    first_available_observed_at TEXT,
    last_material_change_at TEXT,
    next_due_at TEXT
);

CREATE INDEX game_xg_checks_due_idx
    ON game_xg_checks (next_due_at, asa_game_id);
```

Do not duplicate season/stage or availability/value fields. Do not add cadence
classes, attempt counters, outcomes, errors, audit IDs, source-scope foreign
keys, or a foreign key to `game_xg`. The parent game is the durable identity;
an xG check row may truthfully exist when no xG value row exists.

Backfill one check row for every existing `game_xg` row:

```sql
INSERT INTO game_xg_checks (
    asa_game_id,
    last_checked_at,
    first_available_observed_at,
    last_material_change_at,
    next_due_at
)
SELECT
    asa_game_id,
    last_checked_at,
    first_observed_at,
    NULL,
    NULL
FROM game_xg;
```

Copy the stored timestamps; do not replace them with a legacy run, audit,
`games.synced_at`, migration time, or wall time. `first_observed_at` is already
truthful first-available evidence, including NULL for unavailable rows. Existing
rows cannot reveal when their latest material correction occurred, and no
historic caller supplied a due time, so those fields remain NULL. Do not create
generalized audits or legacy runs during migration.

Before backfill, inspect that `game_xg` has `asa_game_id`, `last_checked_at`, and
`first_observed_at`, and that the parent `games` table can support the foreign
key. Skip backfill when deliberately minimal migration fixtures lack required
tables/columns. A real version-11 database has them and must be backfilled.
Migration is additive, preserves all existing fixture/xG/audit/derived rows,
and is idempotent on reopen.

A NULL `next_due_at` means no cadence has been assigned. It does not mean due
immediately; later planner policy must initialize and stagger it.

### Exported state, request, metadata, and result vocabulary

Add to `internal/cache/xg_checks.go`:

```go
type CheckedXGRequest struct {
    GameID    string
    NextDueAt *time.Time
}

type GameXGCheckState struct {
    GameID                   string
    Season                   string
    Stage                    string
    LastCheckedAt            time.Time
    FirstAvailableObservedAt *time.Time
    LastMaterialChangeAt     *time.Time
    NextDueAt                *time.Time
}
```

Reuse P2-04's exported `TargetedRefreshMetadata` exactly. Do not add a duplicate
xG metadata type. It retains the open nonblank trigger vocabulary, required raw
start/finish ordering, and UTC whole-second normalization.

Reuse P2-05's exported `XGRefreshResult` exactly. On success `XGRun` is always
non-nil and `Values` is the complete defensive post-write exact-scope xG row
set ordered by `GameID`, including preserved non-FullTime rows. On every error
return the zero result.

### Read APIs

Add:

```go
func (c *DB) GameXGCheckState(
    ctx context.Context,
    gameID string,
) (GameXGCheckState, bool, error)

func (c *DB) GameXGCheckStates(
    ctx context.Context,
    season string,
    stage string,
) ([]GameXGCheckState, error)
```

Validate trimmed nonblank IDs/scopes. The exact read returns the zero record,
false, nil when absent. The scope read returns an empty non-nil slice, joins
through current parent games in the exact scope, and orders by `GameID`
ascending. Return UTC timestamps and defensive pointers. Return descriptive
query, scan, stored parent identity, and timestamp errors. Reads never insert,
repair, or advance state.

### Exact targeted API

Add:

```go
func (c *DB) UpsertCheckedXG(
    ctx context.Context,
    season string,
    stage string,
    requested []CheckedXGRequest,
    returned []GameXG,
    metadata TargetedRefreshMetadata,
) (XGRefreshResult, error)
```

This is a requested-identity operation, not a partial call to
`ReplaceStageXG`. It never inserts or changes games/teams, never changes xG
participants, and never deletes `game_xg`.

### Caller validation before transaction

Validate all caller-controlled data before `BeginTx`:

- season/stage are nonblank and equal to their trimmed values;
- `requested` is non-nil and nonempty;
- every requested `GameID` is nonblank, trimmed, and unique;
- each `NextDueAt` may be nil; when present its raw value is not before raw
  metadata finish, and a defensive UTC whole-second copy is prepared;
- `returned` is non-nil but may be empty;
- returned IDs are nonblank, trimmed, unique, and a subset of requested IDs;
- every returned row passes P2-05's exact available-value validation: distinct
  trimmed teams, paired finite nonnegative xG, paired expected points in
  `[0, MaxGameExpectedPoints]`, and unset caller observation fields; and
- metadata prepares an exact `game_xg/targeted/success` audit through
  `prepareSourceRefresh`, with no full due time.

Validate raw temporal ordering before normalization. Do not mutate, sort, trim,
or retain caller slices or pointers. Use deterministic private copies.

### Cached identity and eligibility

Inside the transaction and before mutation, require every requested ID to be a
cached exact-scope `FullTime` game with both scores. Reject absent, cross-scope,
PreMatch, and Abandoned requested IDs. For every returned row, require home and
away IDs to match the cached participants exactly.

Aggregate all invalid database identities and report their unique IDs sorted
ascending. These are descriptive identity errors, not
`ErrUnknownGameTeams`: cached parent games and team foreign keys already prove
the identities. Do not refresh catalogs or accept alternate participants.

### Requested, returned, omitted, and deletion authority

Only requested IDs are in scope for this operation:

- a returned available row may insert or update `game_xg` only for its own
  requested ID;
- an omitted requested ID updates only `game_xg_checks`;
- omission never creates an `XGUnavailable` value row, advances
  `game_xg.last_checked_at`, changes an existing available/unavailable value,
  or changes venue aggregates;
- an available value is never erased or downgraded by omission;
- every unrequested value and check row is untouched; and
- direct deletion is forbidden and `RowsDeleted == 0`.

An empty returned slice is a valid successful check of every requested ID. It
must be distinguishable from a nil response, which is invalid.

### Value preference and observation clocks

Refactor/reuse P2-05's package-private evaluator for each returned available
row without weakening its rules:

- available replaces an unavailable marker even for a delayed observation;
- identical available data is unchanged;
- differing available data replaces cached available only when normalized
  finish is strictly later than `game_xg.last_checked_at`;
- equal/older conflicts preserve cached values;
- accepted availability, xG, or expected-points differences are material;
- accepted `RawJSON`-only differences are stored updates but not material; and
- first/check timestamp-only changes are not material.

For a returned value, `game_xg.last_checked_at` advances only monotonically.
`game_xg.first_observed_at` records the earliest accepted available
observation. A targeted omission does not touch either value-observation clock.
Delayed successes still append audit/legacy history and may update the separate
first-available check evidence without regressing a newer value check.

### Per-game check-state semantics

For every requested ID, including omissions, atomically upsert
`game_xg_checks`:

- `last_checked_at` advances only when normalized finish is strictly later;
- `next_due_at` is replaced by that request's normalized value, including
  NULL, only when `last_checked_at` advances; equal/older observations preserve
  the current due value;
- a returned available row sets `first_available_observed_at` to the earlier of
  its existing value and this finish, even if cached-value preference rejects a
  delayed/equal value; omission never invents availability; and
- an accepted material value change sets `last_material_change_at` to the later
  of its existing value and this finish. Omission, rejected data, identical
  values, RawJSON-only updates, and clock-only changes do not set it.

State creation uses this finish for `last_checked_at`, the request's due value,
and the applicable first-available/material timestamps. First-available may be
earlier than a preserved later last check after a delayed observation. Every
returned state timestamp is defensive and UTC.

### Exact generalized audit and legacy lineage

The generalized audit is:

```text
Resource / scope:            game_xg / exact season-stage
Mode / outcome:              targeted / success
RequestedRows:               len(requested)
ReturnedRows:                len(returned)
RowsInserted:                returned rows creating game_xg
RowsUpdated:                 accepted stored returned-row changes
RowsUnchanged:               identical or preference-rejected returned rows
RowsDeleted:                 0
DownstreamInputsChanged:     accepted material xG/xPoints change
```

`RowsInserted + RowsUpdated + RowsUnchanged == ReturnedRows`. Omissions appear
only in requested-minus-returned and check state. State/due-only changes do not
claim changed downstream inputs.

Record through transaction-aware `recordSourceRefresh` with no full due time.
A targeted audit never creates or advances
`source_resource_scope_state`, including when no prior full state exists and
when all requested rows are returned.

Every success inserts one normalized legacy `xg_sync_runs` row describing only
the returned source rows:

```text
RowsSeen:                         len(returned)
RowsInserted/Updated/Unchanged:   same returned-row counts as the audit
AvailableGames:                   len(returned)
UnavailableGames:                 0
```

All returned rows are validated available observations; omissions are not
reclassified as unavailable merely to fill legacy counts. Insert the run for
all-omitted, stale, RawJSON-only, and no-op successes, and return its assigned
ID. Do not fabricate a value row to support counts. Do not write `sync_runs` or
fixture snapshots.

### Targeted venue semantics

For every accepted material availability, home/away xG, or expected-points
change, recompute exact-scope available-FullTime `xg_matches`, `home_xg`, and
`away_xg` in the same transaction. Expected-points-only corrections leave the
aggregate numbers unchanged but still follow the one material-update path.
Preserve `fixture_ready` and all fixture totals. Preserve the existing
`xg_ready` bit exactly: targeted work cannot prove or revoke a complete
authoritative stage observation. If no venue row exists, create the same
xG-only shape used by the full helper but with `xg_ready=0` and fixture
readiness/totals unclaimed.

Omission, rejected/identical values, RawJSON-only updates, and state/time-only
changes leave venue bytes unchanged. Exclude `updated_at` from downstream
comparison.

### P2-05 full maintenance and parent invalidation

After migration 12, extend `ReplaceStageXG` inside its existing transaction:

- upsert check state for every FullTime candidate, including full omissions;
- advance last check monotonically and preserve any existing targeted
  `next_due_at`; initialize new due values as NULL;
- derive first-available only from an actually returned available row, never
  from an omitted protected cached value;
- advance last-material only for an accepted P2-05 material value change;
- retain P2-05 audit/count/full-state/venue/legacy behavior exactly; and
- let an empty/no-FullTime scope create no per-game check state.

Full omission may still create P2-05's authoritative unavailable value marker;
that authority does not transfer to targeted mode. A full success updates no
targeted audit, and check-state writes do not alter its published row counts.

Parent-game deletion cascades both value and check state. Extend P2-03's
accepted participant-change invalidation to delete `game_xg_checks` even when
no `game_xg` row exists; old-participant check history is not valid for the new
identity. FullTime status corrections without participant changes preserve the
state for later cadence decisions.

### Atomic order and rollback

Within one targeted transaction:

1. load and validate all requested cached identities;
2. capture current value/venue state;
3. evaluate and write returned values in `GameID` order;
4. update every requested check state in `GameID` order;
5. reload the complete ordered exact-scope xG result;
6. update venue aggregates only when required, preserving readiness;
7. calculate exact returned-row legacy counts;
8. insert the legacy `xg_sync_runs` row;
9. insert the generalized targeted audit without full state; and
10. commit.

Any value, check-state, result scan, venue, legacy-count construction, legacy
run, generalized audit, or commit failure rolls back the entire operation and
returns the zero result. Do not record failure automatically.

### Legacy compatibility boundary

Keep `ReplaceGameXG`, `RecordXGFailure`, `XGStatus`, `ReplaceSeason`, current
syncer/scheduler callers, and `Service.Run` behaviorally unchanged. Legacy
`ReplaceGameXG` continues its caller snapshot, wall-clock finish, omission
error, value clocks, venue behavior, and legacy-only audit. It does not call
the new targeted API or maintain generalized xG check state; the later adapter
will move production callers onto the split operations.

Keep games, teams, fixture snapshots, qualification/scenarios, forecasts,
source scopes, leases, pruning, HTTP, and product output unchanged except for
the narrowly required P2-03 participant-change invalidation of the new check
row. No targeted xG operation writes fixture or derived lineage.

## Allowed changes

- Add `internal/cache/xg_checks.go` and
  `internal/cache/xg_checks_test.go`.
- Modify `internal/cache/cache.go` for schema version 12, exact migration SQL,
  tolerant backfill, and narrow shared scanners/helpers.
- Modify `internal/cache/cache_test.go` and
  `internal/cache/source_refresh_test.go` for migration/version and audit
  compatibility.
- Modify `internal/cache/xg_inventory.go` and
  `internal/cache/xg_inventory_test.go` only for evaluator reuse, atomic full
  check-state maintenance/due preservation, and shared targeted venue helpers.
- Modify `internal/cache/game_inventory.go` and
  `internal/cache/game_inventory_test.go` only for atomic participant-change
  invalidation and parent cascade proof.
- Modify `internal/cache/game_checks.go` only if a package-private monotonic
  check-state helper can be shared without changing P2-04 behavior.
- Update `docs/asa-loading/README.md` and this packet status during
  implementation/review.

Do not modify `internal/syncer`, `internal/scheduler`, ASA clients or filters,
commands, configuration, HTTP, competition catalog/source scopes,
qualification, scenarios, forecasts, readiness, pruning, templates, CSS, or
JavaScript.

## Tests to add or update

Use fixed timestamps, invented scopes, and temporary SQLite databases. Make no
network request and do not depend on wall time. Cover:

1. Migration 12 exact table/index/columns/FK/schema version; truthful backfill
   of available and unavailable rows; first-available copied only from stored
   first observation; material/due NULL; no audit/run fabrication.
2. Minimal migration fixtures lacking required legacy tables/columns open
   without fake state; real fixture/xG/audit/derived rows survive; reopening is
   idempotent.
3. Exact/missing/scoped reads: non-nil empty, scope isolation, ID ordering, UTC
   timestamps, defensive pointers, malformed storage, and no read side effects.
4. Nil/empty/duplicate/padded requested IDs; nil returned; duplicate,
   unrequested, explicit-unavailable, malformed numeric/observation,
   missing/cross-scope/non-FullTime/Abandoned, and participant-mismatched values
   fail before mutation with deterministic sorted identity errors.
5. Empty/partial responses update every requested check/due state, never create
   unavailable values, preserve available/unavailable/missing rows and all
   unrequested state, return complete ordered values, and produce exact
   requested/returned/zero-deletion counts.
6. Returned insert, unavailable-to-available delayed promotion, identical,
   newer differing, equal/older conflict, RawJSON-only, xG-only, and
   expected-points-only cases reuse P2-05 preference and prove exact row,
   material, observation-clock, and caller-ownership behavior.
7. Last check and due are monotonic; nil due clears only on a newer check;
   first available retains the earliest returned observation even when delayed;
   last material advances only for accepted material and never regresses.
8. Targeted successes never create/change full-resource scope state and always
   append exact generalized audit plus legacy run. Every legacy count describes
   returned rows only; omissions remain visible only in requested-minus-returned
   and check state.
9. Targeted aggregate updates preserve `xg_ready` at both 0 and 1, preserve all
   fixture fields, handle a missing venue row truthfully, and recompute for
   every accepted material availability/xG/xPoints change. Omission, no-op,
   stale, RawJSON-only, and clock-only work preserve venue bytes.
10. P2-05 full observations initialize/advance every candidate check, set first
    available only for returned rows, set material only for accepted material,
    preserve targeted due through newer/equal/delayed full success, create no
    state for empty/no-FullTime scopes, and retain exact full audit/run counts.
11. Parent-game deletion cascades check state. Accepted participant change
    deletes both value and check state even when only check state existed;
    status-only corrections preserve it.
12. Input/request order does not affect writes, reads, values, legacy counts,
    or audit counts, and caller slices/values/due pointers are unchanged.
13. Test-only failures on `game_xg`, `game_xg_checks`, venue summaries,
    complete-result scans, `xg_sync_runs`, and
    `source_refresh_audits` prove rollback of values, state including omissions,
    venue, legacy/generalized lineage, and P2-05 full-resource state.
14. Legacy `ReplaceGameXG`, failure recording/status, P2-01 through P2-05,
    syncer, scheduler, qualification, scenario, venue, forecast, readiness, and
    pruning behavior remains unchanged.

## Verification

Run from the repository root:

```text
gofmt -w internal/cache/xg_checks.go internal/cache/xg_checks_test.go internal/cache/cache.go internal/cache/cache_test.go internal/cache/xg_inventory.go internal/cache/xg_inventory_test.go internal/cache/game_inventory.go internal/cache/game_inventory_test.go internal/cache/game_checks.go internal/cache/source_refresh_test.go
go test -count=1 ./internal/cache
go test -count=1 ./internal/syncer ./internal/scheduler ./internal/qualification ./internal/scenariorefresh
go test -count=1 ./...
go vet ./...
git diff --check
```

Omit unchanged optional files from `gofmt`; every other command is mandatory.

## Non-goals

- ASA `XGoalsFilters.GameID`, HTTP requests, response conversion,
  batching/chunking, retry, or failure classification.
- Selecting due IDs, cadence classes/durations, hot/recent/archive policy,
  staggering, leases, planner logic, or scheduler wakeups.
- Syncer/command integration or replacing the current `Run` facade.
- A targeted unavailable/retraction value, omission deletion, or authoritative
  completeness claim.
- Inserting/changing games or teams, accepting alternate participants, or
  treating Abandoned as xG-eligible.
- Changing fixture snapshots, `sync_runs`, qualification/scenario rows,
  forecasts, source-scope readiness, or product navigation.
- Automatically recording fetch/validation failures or pruning check state.

## Stop conditions

Stop and report without broadening the patch if:

- targeted omission cannot persist check/due state without fabricating or
  changing a `game_xg` value;
- value, state, venue, legacy run, generalized audit, and full-maintenance
  writes cannot share their required transaction;
- truthful migration requires inventing material-change, due, audit, or failure
  history;
- targeted aggregate maintenance requires changing authoritative readiness;
- legacy returned-row lineage cannot remain truthful without treating omission
  as unavailable or changing `XGSyncRun` fields;
- correct identity requires a team/game network refresh or participant change;
- implementation requires changing current syncer, scheduler, ASA client,
  planner, source-scope, qualification, scenario, forecast, or product callers;
  or
- the full suite exposes an unrelated pre-existing failure.

For an unrelated failure, report the command and distinguishing output; do not
repair unrelated code.

## Handoff

Report:

- files changed and exact migration/backfill evidence;
- exported request/state/read/API reuse and defensive ownership;
- requested/returned/omitted identity and no-fabrication/no-deletion proof;
- value preference plus value/check/material/due clock behavior;
- exact targeted audit, no-full-state proof, and returned-row legacy lineage;
- targeted venue readiness preservation and aggregate behavior;
- P2-05 full maintenance, P2-03 invalidation, cascade, and rollback evidence;
- legacy/current-caller compatibility; and
- all verification results, deviations, and follow-up issues.

After P2-06, the compatibility adapter may rely on separate team, full/targeted
game, and full/targeted xG persistence with truthful audits and cadence state.
Network filters, due selection, batching, and scheduler integration remain
later Phase 3 packets.
