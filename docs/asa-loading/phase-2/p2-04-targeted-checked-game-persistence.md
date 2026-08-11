# P2-04: Targeted checked-game persistence

## Control

- Status: Complete
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P2-03 authoritative game-inventory persistence
- Blocks: independent full/targeted xG persistence, syncer operation
  decomposition, and due-result planner selection

## Goal

Add a non-deleting targeted cache operation that checks an explicit batch of
already cached game IDs, safely applies only preferred returned rows, records
omissions and per-game result cadence state, and atomically preserves a complete
fixture snapshot, legacy `sync_runs` lineage, generalized targeted audit, and
venue/xG consistency.

## Why this packet exists

The loading plan's hot result path requests only cached games whose result check
is due. Every returned row must belong to the requested identity and exact
scope; an omitted, empty, stale, or terminal-regressing response preserves the
cached game and never gains deletion authority.

P2-03 established authoritative replacement, complete post-write snapshots,
source preference, legacy lineage, and split venue/xG helpers. It deliberately
left targeted persistence separate. Aggregate generalized audit counts cannot
prove which omitted IDs were checked or carry each game's next due time, so
this packet also adds the narrow per-game result-check state needed before a
later scheduler can select due work.

This remains a cache packet. It does not fetch ASA data, choose cadence
durations, batch network requests, acquire a new lease shape, or change the
current syncer/scheduler path.

## Fixed decisions

### Migration 11: per-game result-check state

Increment `schemaVersion` to 11. Migration 11 creates exactly:

```sql
CREATE TABLE game_result_checks (
    asa_game_id TEXT PRIMARY KEY
        REFERENCES games(asa_game_id) ON DELETE CASCADE,
    last_checked_at TEXT NOT NULL,
    first_terminal_observed_at TEXT,
    last_material_change_at TEXT,
    next_due_at TEXT
);

CREATE INDEX game_result_checks_due_idx
    ON game_result_checks (next_due_at, asa_game_id);
```

Do not duplicate season/stage in this table; exact scope comes from the parent
game. Do not add cadence classes, attempt counters, outcome/error fields, xG
state, or source-scope foreign keys.

Backfill one state for each currently cached game whose exact scope has a
successful legacy `sync_runs` row. `last_checked_at` is the greatest successful
`finished_at` for that scope. Before P2-04, every successful game lineage row
represents a complete full-inventory observation, so that timestamp truthfully
proves the current game was checked.

Set backfilled `first_terminal_observed_at`, `last_material_change_at`, and
`next_due_at` to `NULL`. Current rows and aggregate history cannot reconstruct
those facts truthfully. `games.synced_at` is a row-write timestamp and does not
advance for an unchanged full observation, so it is not a truthful replacement
for the successful full-run timestamp. Do not backfill from it, failures,
generalized audits alone, source-scope discovery, terminal status, or current
wall time. Do not fabricate generalized audits or legacy runs.

A NULL `next_due_at` means that no result cadence has been assigned; it never
means “due immediately.” The later planner must initialize and stagger due times
from truthful check/terminal state and configured policy. It must not make every
archived migrated game hot merely because due time is NULL.

The backfill must inspect that `games` has `asa_game_id`, `season`, and `stage`,
and `sync_runs` has `season`, `stage`, `outcome`, and `finished_at`, before
querying. Skip the backfill when deliberately minimal migration fixtures lack a
table/column; a real version-10 database has them and must be backfilled.
Migration is additive, preserves all existing rows/foreign keys, and is
idempotent when the database reopens.

### Exported targeted vocabulary

Add to `internal/cache/game_checks.go`:

```go
type TargetedRefreshMetadata struct {
    Trigger    SourceRefreshTrigger
    StartedAt  time.Time
    FinishedAt time.Time
}

type CheckedGameRequest struct {
    ASAID     string
    NextDueAt *time.Time
}

type GameResultCheckState struct {
    GameID                  string
    Season                  string
    Stage                   string
    LastCheckedAt           time.Time
    FirstTerminalObservedAt *time.Time
    LastMaterialChangeAt    *time.Time
    NextDueAt               *time.Time
}
```

`TargetedRefreshMetadata` uses P2-01/P2-02 trigger and timestamp rules: trigger
is an open nonblank vocabulary; start/finish are required; validate raw finish
ordering before normalizing both values to UTC whole seconds.

Each request ID is nonblank, already trimmed, and unique. `NextDueAt` may be
nil; when present validate its raw value is not before the raw metadata finish,
then defensively copy and normalize it to UTC whole seconds. The caller chooses
per-ID due times. This packet stores them but does not define cadence policy.

All returned state timestamps are UTC and pointer values are defensive.

### Read APIs

Add:

```go
func (c *DB) GameResultCheckState(
    ctx context.Context,
    gameID string,
) (GameResultCheckState, bool, error)

func (c *DB) GameResultCheckStates(
    ctx context.Context,
    season string,
    stage string,
) ([]GameResultCheckState, error)
```

Validate trimmed nonblank identities/scopes. The exact read returns the zero
record, false, nil when state is absent. The scope list returns an empty non-nil
slice and only states whose parent games remain in the exact scope, ordered by
`GameID` ascending. Reads return descriptive query, scan, timestamp, and parent
identity errors and never seed or update state.

### Targeted write API

Add this exact method, reusing P2-03's `GameRefreshResult`:

```go
func (c *DB) UpsertCheckedGames(
    ctx context.Context,
    season string,
    stage string,
    requested []CheckedGameRequest,
    returned []Game,
    metadata TargetedRefreshMetadata,
) (GameRefreshResult, error)
```

On every error return the zero result. On success, `SyncRun` is always non-nil
and `Teams`/`Games` are non-nil defensive, deterministically ordered complete
post-write exact-scope slices under the P2-03 contract.

### Caller validation before transaction

Validate before `BeginTx`:

- season/stage are nonblank and equal to their trimmed values;
- `requested` is non-nil and nonempty;
- every requested ID is nonblank, trimmed, and unique;
- every request due time satisfies raw ordering and is defensively normalized;
- `returned` is non-nil but may be empty;
- returned IDs are nonblank, trimmed, unique, and a subset of requested IDs;
- every returned row has exact requested season/stage and passes P2-03's shared
  game validation for teams, kickoff, known status, scores, matchday, and source
  last-update timestamp; and
- targeted metadata prepares a fixed `games/targeted/success` audit through
  `prepareSourceRefresh` with no full due time.

Refactor P2-03's caller-controlled per-game validator into a package-private
helper used by both APIs; do not weaken or duplicate its contract. Do not mutate
or reorder caller slices or pointers.

### Cached identity and omission rules

Within the transaction and before mutation:

- every requested ID must already exist under the exact season/stage;
- an absent requested ID or an ID stored in another scope is an error;
- every returned row's home and away team IDs must exactly match its cached
  game; and
- participant mismatch is an identity error, not
  `ErrUnknownGameTeams`—refreshing the team catalog cannot repair it.

This operation never inserts a new game, moves an ID across scopes, changes
participants, or deletes a game. New fixture discovery and participant
corrections remain authoritative P2-03 work.

Every requested ID absent from `returned` is an omission: preserve its game row
exactly, record its successful check timestamp/due state, and do not count it as
a returned unchanged row. Empty `returned` is therefore a valid successful
targeted check of all requested identities.

### Source preference and materiality

Reuse P2-03's `preferIncomingGame` and terminal policy exactly:

- incoming terminal beats cached nonterminal;
- cached terminal rejects incoming nonterminal;
- at equal terminality only a strictly newer valid `LastUpdatedUTC` is
  preferred; and
- equal/older source timestamps preserve cached data.

For result-cadence state, terminal means P2-03's exact terminal vocabulary:
`Abandoned`, or `FullTime` with both scores. Venue xG eligibility remains
strictly `FullTime`; an Abandoned observation can start terminal correction
cadence without becoming xG-eligible.

An identical or rejected stale/regressing returned row counts unchanged. An
accepted `RawJSON`/`LastUpdatedUTC`-only change counts updated but is not
fixture-material. Add one shared private comparison for the fields included by
`FixtureSnapshotID` (ID, status, participants, scores, kickoff, and matchday;
scope is fixed). P2-03 authoritative writes and P2-04 targeted writes use it for
per-game material-change state.

Calculate `DownstreamInputsChanged` from complete pre/post scope
`FixtureSnapshotID` values, never from row counts. Raw-only updates, omissions,
stale rows, and no-ops remain false.

### Per-game state semantics

For every requested ID, atomically upsert check state:

- `last_checked_at` becomes the normalized targeted finish only when it is
  strictly later than an existing value; otherwise preserve the later/equal
  stored check;
- `next_due_at` is replaced by the request's normalized value, including NULL,
  only when `last_checked_at` advances; an equal/older delayed observation
  cannot regress or erase the current due time;
- if the returned row itself is terminal, set
  `first_terminal_observed_at` to the earlier of its current value and this
  finish; omission or a returned nonterminal row does not invent a terminal
  observation; and
- if an accepted returned row changes fixture-snapshot fields, set
  `last_material_change_at` to the later of its current value and this finish.

State creation uses this finish as `last_checked_at`, the request due time, and
the applicable terminal/material timestamps. Equal or delayed targeted audits
and legacy runs are retained, but state/due timestamps remain deterministic.

Once migration 11 exists, extend `ReplaceGameInventory` in the same package so
future full observations maintain the new state without changing P2-03's public
result/audit contract:

- upsert `last_checked_at` for every returned game under the same monotonic rule;
- derive first-terminal only from a returned terminal row;
- update last-material-change for an inserted or accepted fixture-material row;
- preserve an existing per-game `next_due_at`, because
  `FullRefreshMetadata.NextFullDueAt` belongs to scope inventory rather than
  result cadence;
- initialize a new game's next due as NULL; and
- let authoritative deletion cascade its state row.

Empty discovery creates no game state. This full-write extension shares the
authoritative transaction and must not create a targeted audit.

### Atomic audit, lineage, snapshot, and venue behavior

Within one targeted transaction:

1. load and validate every requested cached identity;
2. calculate the complete pre-write scope snapshot;
3. apply only preferred returned rows with normalized finish as `synced_at`;
4. update every requested per-game state, including omissions;
5. reload complete deterministic post-write teams/games and calculate its
   snapshot;
6. update split venue/xG state only for material game changes;
7. insert a successful legacy `sync_runs` row with the complete post-write
   snapshot;
8. insert the generalized targeted audit with `recordSourceRefresh`; and
9. commit.

The generalized audit is exact:

```text
Resource / scope:            games / exact season-stage
Mode / outcome:              targeted / success
RequestedRows:               len(requested)
ReturnedRows:                len(returned)
RowsInserted / RowsDeleted:  0 / 0
RowsUpdated:                 accepted stored updates
RowsUnchanged:               identical or preserved returned rows
DownstreamInputsChanged:     complete pre-snapshot != post-snapshot
```

`RowsUpdated + RowsUnchanged == ReturnedRows`; omissions are represented by
requested-minus-returned plus their state rows. Targeted audit recording never
creates or changes `source_resource_scope_state`, including when all requested
rows are returned.

The legacy run uses normalized metadata timestamps, exact scope, zero team
counts, `GamesUpserted == GamesSeen == len(returned)`, zero inserted/deleted,
the same returned updated/unchanged counts, and the complete post-write fixture
snapshot. Insert it even for an empty, stale, raw-only, or no-op response so
`LastSuccess`, qualification, and scenario foreign-key lineage remain valid.

Reuse P2-03 venue rules. Omission/no-op/raw-only changes leave the venue row
untouched. Score/kickoff/matchday changes recompute fixture totals while
preserving xG readiness/aggregates. Exact transitions into or out of FullTime
recompute surviving xG aggregates and set `xg_ready=0`. Abandoned-only changes
do not invalidate xG coverage. Participants cannot change in targeted mode.

Any game, state, venue, legacy run, generalized audit, or commit failure rolls
back all changes. Do not record a failure automatically; later orchestration
owns fetch/validation failure audits.

### Legacy compatibility

Keep `ReplaceSeason`, `RecordFailure`, `ReplaceGameXG`, `Status`, `Season`,
`ClinchingInputs`, qualification/scenario refreshers, `syncer.Store`, and
`Service.Run` behaviorally unchanged. They do not call `UpsertCheckedGames` in
this packet. Migration/backfill and new reads must not alter legacy readers,
snapshots, venue summaries, xG rows, scopes, leases, pruning, or product output.

## Allowed changes

- Add `internal/cache/game_checks.go`.
- Add `internal/cache/game_checks_test.go`.
- Modify `internal/cache/cache.go` for schema version 11, exact migration SQL,
  tolerant backfill helpers, and narrowly shared private queries.
- Modify `internal/cache/cache_test.go` for migration and legacy compatibility.
- Modify `internal/cache/game_inventory.go` and
  `internal/cache/game_inventory_test.go` only for shared validation/material
  helpers and atomic full-write maintenance of result-check state.
- Modify `internal/cache/source_refresh.go` only if the targeted metadata
  preparation can share a narrow existing helper without public behavior
  changes.
- Modify `internal/cache/source_refresh_test.go` only for that narrow refactor.
- Update `docs/asa-loading/README.md` and this packet status during
  implementation/review.

Do not modify `internal/syncer`, `internal/scheduler`, the ASA client, commands,
configuration, HTTP, templates, competition catalog, `source_scopes`,
qualification, scenarios, forecasts, pruning, CSS, or JavaScript.

## Tests to add or update

Use fixed timestamps, invented scopes, and temporary SQLite databases. Cover:

1. Migration 11 exact table/index/schema version/FK cascade; full version-10
   truthful backfill from greatest successful legacy scope run; failures and
   cached rows alone create no state; nullable fields remain nil.
2. Minimal migration fixtures lacking required legacy columns open without
   fabricated state; real rows and derived-data foreign keys survive reopening
   and migration is idempotent.
3. Exact/missing state reads, non-nil empty lists, scope isolation, ID ordering,
   UTC parsing, malformed stored timestamps, and defensive pointers.
4. Nil/empty/duplicate/padded requested IDs; nil returned slice; duplicate,
   unrequested, malformed, wrong-scope, cross-scope, absent-requested, and
   participant-mismatched rows fail with zero mutation/audit/state/lineage.
5. Empty and partial responses preserve every omitted game, write no insert or
   deletion, produce exact requested/returned/count audit values, update every
   requested check/due state, and return a complete snapshot/legacy run.
6. Identical, raw-only newer, stale, equal-time conflict, terminal advance,
   terminal regression, and malformed cached source timestamp cases reuse
   P2-03 preference and produce exact row/material counts.
7. Requested/returned input order does not affect deterministic complete result
   ordering or audit counts, and caller slices/values/due pointers are unchanged.
8. First-terminal is set only by a returned terminal observation and retains
   the earliest observation. Last-material-change ignores raw-only updates and
   advances only for accepted fixture fields. Equal/older check finishes cannot
   regress last checked, last material, or next due.
9. Targeted audits never create/modify full-resource scope state. Every success,
   including omission/no-op, appends exact targeted audit and complete legacy
   snapshot lineage.
10. Omission/no-op/raw-only preserves venue bytes. Score corrections preserve
    xG readiness/totals. FullTime entry/exit recomputes surviving xG aggregates
    and clears readiness; Abandoned-only changes do not.
11. After migration, full P2-03 inventory observations initialize/advance
    per-game check/terminal/material state, preserve targeted next due, cascade
    deleted state, and keep empty discovery state-free.
12. Test-only triggers on game state, venue, `sync_runs`, and
    `source_refresh_audits` prove rollback of accepted updates, every requested
    state including omissions, venue, lineage, audit, and snapshots.
13. Existing P2-01 through P2-03, legacy cache, syncer, scheduler,
    qualification, scenario, venue, xG, readiness, and pruning tests pass
    without current caller behavior changes.

Tests make no network requests and do not depend on the wall clock.

## Verification

Run from the repository root:

```text
gofmt -w internal/cache/game_checks.go internal/cache/game_checks_test.go internal/cache/cache.go internal/cache/cache_test.go internal/cache/game_inventory.go internal/cache/game_inventory_test.go internal/cache/source_refresh.go internal/cache/source_refresh_test.go
go test -count=1 ./internal/cache
go test -count=1 ./internal/syncer ./internal/scheduler ./internal/qualification ./internal/scenariorefresh
go test -count=1 ./...
go vet ./...
git diff --check
```

Omit unchanged optional files from `gofmt`; every other command is mandatory.

## Non-goals

- Fetching ASA data, adding filters, batching/chunking IDs, or network retries.
- Selecting due IDs, defining cadence durations/classes, or changing scheduler
  wakeups and leases.
- Inserting unknown games, moving cross-scope IDs, changing participants, or
  deleting any game in targeted mode.
- Unknown-team refresh/retry; targeted identities already have cached teams.
- Full or targeted xG source operations and xG cadence state.
- Syncer/command integration or replacing the current `Run` facade.
- Recording failed source attempts automatically.
- Calling qualification/scenario refreshers or warming forecasts.
- Source-scope discovery/lifecycle/readiness or public navigation changes.
- Migrating derived lineage away from `sync_runs` or pruning the new state.

## Stop conditions

Stop and report without broadening the patch if:

- requested-ID omissions cannot be recorded atomically without mutating game
  rows or gaining deletion authority;
- complete post-write snapshots, per-game state, legacy lineage, venue changes,
  and generalized audit cannot share one transaction;
- truthful migration requires inventing first-terminal, material-change, due,
  or failure history;
- targeted writes require participant changes, unknown-game insertion, or a
  schema change to the global game identity;
- per-game state requires scheduler cadence policy or network behavior;
- correct implementation requires changing current syncer, scheduler,
  qualification, scenario, forecast, source-scope, or product callers;
- preserving P2-03 requires weakening its deletion, preference, snapshot,
  audit, or venue guarantees; or
- the full suite exposes a pre-existing unrelated failure.

For a pre-existing failure, report the command and distinguishing output; do
not repair unrelated code.

## Handoff

Report:

- files changed and migration/backfill behavior;
- exact exported types, reads, and targeted API;
- requested/returned/omitted validation and non-deletion proof;
- source preference, row counts, and per-game timestamp/due semantics;
- complete snapshot, legacy lineage, generalized targeted audit, and proof that
  full-resource state is unchanged;
- venue/xG and full-inventory state-maintenance evidence;
- atomic rollback and legacy compatibility proof;
- all verification results, deviations, and follow-up issues.

The next independent xG packet may rely on deterministic complete fixture
snapshots, exact checked-game identities, durable result observation state,
targeted generalized audit semantics, and legacy lineage. The later syncer
adapter composes P2-02 through the game/xG APIs; only after that may a planner
select due IDs and issue targeted network requests.
