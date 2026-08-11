# P2-02: Independent team-catalog persistence

## Control

- Status: Ready
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P2-01 source-refresh audit/state foundation
- Blocks: authoritative and targeted game persistence and syncer operation
  decomposition

## Goal

Add a cache API that atomically upserts one complete, nonempty ASA team-catalog
response and records its successful generalized full-refresh audit and global
scope state, without deleting teams or changing any current syncer, legacy
audit, fixture, derived-data, or product behavior.

## Why this packet exists

Phase 2 of the
[ASA data catalog and loading plan](../../asa-data-catalog-and-loading-plan.md)
separates persistence by refresh semantics. Its `RefreshTeams` operation
requests `/nwsl/teams`, validates unique nonempty team IDs, and upserts only the
team catalog with an independent audit and due time. A team refresh must not be
a prerequisite for every game check; a later game operation will request it
only when a response introduces an unknown team.

The current `ReplaceSeason` transaction upserts teams and games together,
deletes missing games, updates venue summaries, and writes a legacy
`sync_runs` row. Its package-private `writeTeam` helper already implements the
required non-deleting row comparison and upsert. P2-01 added the generalized
audit/state vocabulary and a transaction-aware `recordSourceRefresh` helper,
but deliberately did not connect it to a source mutation.

This packet establishes that first atomic mutation boundary. It remains
cache-only and has no production caller until later Phase 2 packets split game
and xG persistence and adapt the existing `Run` facade.

## Fixed decisions

### Reusable full-refresh metadata

Add this exported type to `internal/cache/source_refresh.go`:

```go
type FullRefreshMetadata struct {
    Trigger       SourceRefreshTrigger
    StartedAt     time.Time
    FinishedAt    time.Time
    NextFullDueAt *time.Time
}
```

It represents caller-observed source-request timing and cadence metadata for a
successful authoritative collection. Resource, scope, mode, outcome, error,
row counts, and downstream impact are not caller-settable through this type;
the source mutation derives them. Later authoritative game and xG operations
may reuse this exact type.

`StartedAt` and `FinishedAt` are required and `FinishedAt` must not precede
`StartedAt`. `Trigger` follows the P2-01 open-vocabulary contract: it must be
nonblank after trimming, but is not restricted to the declared constants.
`NextFullDueAt` may be nil; when present, it must not precede `FinishedAt`.

Validate temporal ordering on the raw input values before reducing precision.
After validation, return and store timestamps in UTC truncated to whole
seconds, matching P2-01 and SQLite storage. Copy and normalize a non-nil due
time; do not retain or mutate the caller's pointer.

### Shared audit preparation helper

Refactor the pre-transaction work in `RecordSourceRefresh` into this
package-private helper:

```go
func prepareSourceRefresh(
    audit SourceRefreshAudit,
    nextFullDueAt *time.Time,
) (SourceRefreshAudit, *time.Time, error)
```

The helper owns the existing P2-01 rules in this order:

1. require a zero audit ID and validate the complete audit vocabulary, scope,
   trigger, raw timestamps, counts, outcome/error relationship, deletion
   authority, and downstream claim;
2. validate that a due time is supplied only for a successful full refresh and
   that its raw value is not before the raw finish; and
3. normalize audit and due timestamps to UTC whole-second values, returning a
   defensive due-time pointer.

`RecordSourceRefresh` must call this helper before `BeginTx` and otherwise keep
its existing public behavior. `UpsertTeams` constructs its fixed audit with
zero derived change counts, calls the same helper before `BeginTx`, then fills
only the inserted/updated/unchanged counts obtained inside the transaction.
Those derived counts are nonnegative by construction and may not alter any
other prepared field.

This refactor must preserve the timestamp-precision fix: a raw finish before a
raw start, or a raw due time before the raw finish, remains invalid even if
whole-second truncation would make the values equal.

### Independent team write API

Add this exact method:

```go
func (c *DB) UpsertTeams(
    ctx context.Context,
    teams []Team,
    metadata FullRefreshMetadata,
) (SourceRefreshAudit, error)
```

Validate the complete caller-controlled input before beginning a transaction:

- `teams` must be non-nil and nonempty;
- every `Team.ASAID` must be nonblank and equal to its trimmed value;
- every `Team.ASAID` must be unique within the response; and
- the full-refresh metadata must satisfy the shared preparation rules above.

Do not trim or rewrite IDs. Do not add validation requirements for `Name`,
`ShortName`, `Abbreviation`, or `RawJSON`; the source contract locked by the
roadmap requires team identity, and the existing cache accepts those
presentation values as supplied.

On every error return the zero `SourceRefreshAudit`. Validation errors must not
begin a transaction or write any row.

### Atomic write and audit semantics

After validation and preparation, `UpsertTeams` performs exactly this work in
one SQLite transaction:

1. call the existing package-private `writeTeam` for every supplied team,
   passing the normalized prepared `FinishedAt` as `updated_at`;
2. accumulate exact inserted, updated, and unchanged counts from its existing
   `rowChange` result;
3. fill those three fields on the prepared audit;
4. call the P2-01 package-private `recordSourceRefresh` with the same
   transaction and normalized due time; and
5. commit.

Any team write, audit insert, state update, or commit failure returns an error
and rolls back team rows, the generalized audit, and global teams state
together. Do not call public `RecordSourceRefresh` from inside this operation
and do not open a second transaction.

The successful audit is derived exactly as follows:

```text
Resource:                  teams
Season:                    ""
Stage:                     ""
Mode:                      full
Trigger:                   metadata.Trigger
StartedAt:                 normalized metadata.StartedAt
FinishedAt:                normalized metadata.FinishedAt
Outcome:                   success
ErrorSummary:              ""
RequestedRows:             0
ReturnedRows:              len(teams)
RowsInserted:              exact write count
RowsUpdated:               exact write count
RowsUnchanged:             exact write count
RowsDeleted:               0
DownstreamInputsChanged:   false
```

`RequestedRows` is zero because a full catalog request does not enumerate
requested identities. The three change counts must sum to `ReturnedRows`.
Omitted cached teams are retained: this method is an authoritative observation
of returned team metadata, not deletion authority for the global identity
catalog.

`DownstreamInputsChanged` is always false. Independent catalog inserts and
updates are presentation-only until a game inventory references those team
IDs. The later game persistence operation owns the downstream-input signal for
participant changes. Do not infer this flag from team row counts.

A successful no-op still appends one generalized audit and may create or
advance global teams full-refresh state. State remains subject to P2-01's
monotonic rule: an equal or older `FinishedAt` is retained in audit history but
cannot regress `last_full_success_at`, `next_full_due_at`, or `updated_at`.

The method does not automatically append a failure audit for invalid input or
a rolled-back transaction. Later orchestration may explicitly record fetch or
validation failures through `RecordSourceRefresh`; this cache mutation must
not claim a durable attempt when its atomic transaction failed.

### Legacy compatibility boundary

Keep `ReplaceSeason`, `RecordFailure`, `SyncRun`, legacy `sync_runs` readers,
and their tests behaviorally unchanged. `ReplaceSeason` continues to call
`writeTeam` directly inside its existing combined fixture transaction. It must
not call `UpsertTeams`, write a generalized team audit, or update generalized
team state.

Keep the existing `forecastInputsChanged` behavior unchanged for legacy
`SyncRun` values. Its treatment of legacy team insert/update counts does not
define `UpsertTeams.DownstreamInputsChanged`.

`UpsertTeams` must not write, delete, or recalculate:

- games or game xG;
- `sync_runs` or `xg_sync_runs`;
- fixture snapshots or their legacy lineage;
- qualification, scenario, or forecast rows;
- venue fixture summaries;
- source-scope discovery/readiness rows;
- leases or pruning history; or
- per-game check/cadence state.

Do not change the `syncer.Store` interface or `Service.Run`. Production loading
continues through the legacy combined path until the later compatibility
packet can compose all split operations.

## Allowed changes

- Add `internal/cache/teams.go`.
- Add `internal/cache/teams_test.go`.
- Modify `internal/cache/source_refresh.go` only to add
  `FullRefreshMetadata`, add `prepareSourceRefresh`, and make the existing
  public recorder use it without behavior changes.
- Modify `internal/cache/source_refresh_test.go` only for the helper refactor's
  shared validation, raw timestamp ordering, normalization, and defensive-copy
  coverage.
- Update `docs/asa-loading/README.md` and this packet's control status during
  implementation and review.

Do not modify `internal/cache/cache.go`: `writeTeam`, `rowChange`, and
`rollback` are already package-private and reusable from `teams.go`. Do not
modify migrations or schema version.

Do not modify `internal/syncer`, `internal/scheduler`, the ASA client, commands,
configuration, HTTP, templates, competition catalog, source scopes,
qualification, scenarios, forecasts, venue behavior, pruning, CSS, or
JavaScript.

## Required behavior

- A valid nonempty catalog upserts exact supplied team metadata without
  deleting any omitted global team.
- Validation rejects blank, padded, or duplicate ASA IDs before a transaction.
- The returned successful audit has an assigned ID, normalized UTC timestamps,
  exact counts, global team identity, full mode, zero requested/deleted rows,
  and false downstream impact.
- Team writes, generalized audit insertion, and monotonic full-state update are
  one atomic transaction.
- A no-op full refresh remains a successful audited observation and may advance
  due state.
- Equal or delayed successes append history without regressing current state.
- Caller input slices, team values, and due-time pointers are not mutated.
- P2-01 public metadata recording and read behavior remain unchanged.
- Current combined sync, legacy status/lineage, snapshot validation, source
  discovery/readiness, venue, derived-data, scheduler, and product behavior
  remain unchanged.

## Tests to add or update

Use fixed timestamps, invented team IDs, and a temporary SQLite database. Add
deterministic coverage for at least:

1. Nil and empty team slices, blank IDs, whitespace-padded IDs, and duplicate
   IDs return errors with no teams, generalized audits, or global state written.
2. Blank triggers, zero timestamps, raw finish-before-start, raw due-before-
   finish, and other invalid full metadata fail before any mutation. Include
   sub-second cases that truncation would otherwise conceal.
3. A multi-team insert returns exact inserted/returned counts, zero requested,
   updated, unchanged, and deleted counts, false downstream impact, and creates
   the exact global successful-full audit and state.
4. Repeating an identical response returns all rows unchanged, appends another
   audit, remains downstream-false, and advances a newer global full-success
   timestamp and due time.
5. Updating any persisted presentation/raw field returns the exact updated
   count and stored values while remaining downstream-false.
6. Supplying only a subset of already cached teams retains omitted teams,
   reports no deletion, and does not change games that reference either
   supplied or omitted teams.
7. Reversed input order produces the same stored catalog and counts. The method
   does not mutate the input slice or values.
8. A nil due time creates or advances state with a nil due; a non-nil value is
   normalized and defensively copied. Older and equal successes remain in
   newest-first audit history without regressing current state or due time.
9. A test-only SQLite `BEFORE INSERT` trigger that aborts insertion into
   `source_refresh_audits` after team writes causes the entire operation to
   fail and proves that inserted/updated teams, audit, and state all roll back.
   Remove the trigger within test cleanup; do not add it to migrations.
10. `RecordSourceRefresh` retains all P2-01 validation, raw temporal ordering,
    same-second monotonic, audit round-trip, and defensive due-pointer behavior
    after the helper refactor.
11. `UpsertTeams` leaves `source_scopes`, `sync_runs`, `xg_sync_runs`, fixture
    snapshots, venue summaries, games, xG, qualification, scenario, and
    forecast rows unchanged.
12. Calling legacy `ReplaceSeason` retains its exact team/game counts, snapshot
    and venue behavior, and does not append a generalized team audit or use the
    new API.

Tests make no network requests and do not depend on the wall clock.

## Verification

Run all of these commands from the repository root:

```text
gofmt -w internal/cache/teams.go internal/cache/teams_test.go internal/cache/source_refresh.go internal/cache/source_refresh_test.go
go test -count=1 ./internal/cache
go test -count=1 ./internal/syncer ./internal/scheduler ./internal/qualification ./internal/scenariorefresh
go test -count=1 ./...
go vet ./...
git diff --check
```

If an allowed existing file did not change, omit it from the `gofmt` command.
All other commands are mandatory.

## Non-goals

- Fetching `/nwsl/teams` or changing ASA response mapping/validation.
- Integrating this method with the syncer, scheduler, startup, CLI, or legacy
  `Run` facade.
- Recording generalized failures automatically.
- Deleting teams absent from a response.
- Adding authoritative or targeted game/xG writes or unknown-team retry logic.
- Changing fixture snapshot identity, legacy lineage, downstream recalculation,
  or forecast warming.
- Selecting cadence durations or consuming `next_full_due_at`.
- Adding per-game observation/check/due state.
- Updating source discovery, lifecycle, readiness, or public navigation.
- Adding or changing a database migration, schema constraint, lease, or audit
  pruning behavior.

## Stop conditions

Stop and report without broadening the patch if:

- atomic team and generalized metadata persistence cannot reuse the P2-01
  transaction helper;
- correct implementation requires a schema migration or modification to
  `source_scopes`, legacy audit tables, fixture snapshots, venues, or derived
  data;
- correctness requires deleting an omitted team or defining presentation-field
  validity not present in the roadmap;
- the new API cannot remain unused by current `ReplaceSeason` and syncer code;
- implementation requires scheduler cadence, network, command, HTTP, targeted
  game, xG, or unknown-team retry changes;
- the worktree contains a newer source-write contract that conflicts with this
  packet; or
- the full suite exposes a pre-existing failure unrelated to the packet.

For a pre-existing failure, include the failing command and enough output to
distinguish it from a regression; do not repair unrelated code.

## Handoff

Report:

- files changed;
- the exact exported metadata type and `UpsertTeams` signature;
- validation and timestamp preparation behavior;
- exact upsert counts and generalized audit/state values;
- proof of atomic rollback and non-deletion;
- proof that downstream impact is always false for independent team changes;
- proof that source scopes, legacy audits/lineage, snapshots, venues, derived
  rows, and current syncer behavior remain unchanged;
- every verification command and its outcome;
- any deviation from this packet; and
- any issue P2-03 game persistence or the later compatibility adapter must
  account for.

P2-03 may rely on a global, independently audited, non-deleting team catalog;
the reusable `FullRefreshMetadata` and `prepareSourceRefresh` contract; and an
atomic generalized audit/state seam. P2-03 owns authoritative and targeted game
semantics, unknown-team error/retry behavior, complete post-write fixture
snapshots, downstream-input changes when participant IDs enter game inventory,
and continued legacy `sync_runs` lineage.
