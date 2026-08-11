# P2-03: Authoritative game-inventory persistence

## Control

- Status: Ready
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P2-02 independent team-catalog persistence
- Blocks: targeted checked-game persistence, independent xG persistence, and
  syncer operation decomposition

## Goal

Add one cache API for an authoritative full game-inventory observation that
atomically validates and reconciles an exact season/stage, preserves a complete
post-write fixture snapshot and legacy `sync_runs` lineage, updates generalized
full-refresh audit/state and venue summaries, and never permits an empty
response to delete previously cached games.

## Why this packet exists

The Phase 2 loading plan separates the weekly authoritative inventory operation
from hot targeted result checks. Only `ReconcileGameInventory` may delete games
absent from a response. A valid nonempty observation must preserve the complete
fixture snapshot used by qualification and scenarios; an empty first discovery
may mean “not published,” while an empty response after publication is
suspicious and cannot replace data.

The current `ReplaceSeason` combines team and game writes, deletes absent games,
resets venue xG readiness, and inserts the legacy lineage row. P2-02 split team
persistence and established reusable full-refresh metadata. This packet splits
only the authoritative game half. Targeted checked-game state and writes remain
P2-04 so deletion authority, omission semantics, and per-game cadence are not
reviewed in one patch.

## Fixed decisions

### Exported result and API

Add to `internal/cache/game_inventory.go`:

```go
type GameRefreshResult struct {
    Audit   SourceRefreshAudit
    SyncRun *SyncRun
    Teams   []Team
    Games   []Game
}

func (c *DB) ReplaceGameInventory(
    ctx context.Context,
    season string,
    stage string,
    games []Game,
    expected *competition.InventoryExpectation,
    metadata FullRefreshMetadata,
) (GameRefreshResult, error)
```

`expected` is explicit caller input. Do not look up the production competition
catalog inside this write. Nil means that no verified inventory expectation is
known; a factual unknown-format scope may still persist valid games. Do not
mutate or retain the caller's expectation pointer.

On any error return the zero `GameRefreshResult`. On success, `Teams` and
`Games` are non-nil defensive slices representing the complete post-write exact
scope. Order teams by `ASAID` ascending and games by `KickoffUTC`, then `ASAID`
ascending. Mutating the result, input games, metadata due pointer, or expected
inventory after return cannot alter cached data or another read.

For every nonempty success, `SyncRun` is non-nil and points to the legacy
success row inserted by the same transaction. Only the successful empty
discovery case has `SyncRun == nil`.

### Exported unknown-team error

Add:

```go
var ErrUnknownGameTeams = errors.New("game inventory references unknown teams")

type UnknownGameTeamsError struct {
    TeamIDs []string
}

func (e *UnknownGameTeamsError) Error() string
func (e *UnknownGameTeamsError) Unwrap() error
```

`Unwrap` returns `ErrUnknownGameTeams`. Collect every missing home/away identity,
deduplicate it, sort it ascending, and place a defensive slice in `TeamIDs`.
This is the only validation failure that directs later orchestration to refresh
teams and retry. Do not return it for a changed participant on an existing
game, a cross-scope game ID, or malformed input.

### Caller-controlled validation

Validate all caller-controlled values before beginning a transaction:

- season and stage are nonblank and equal to their trimmed values;
- `games` is non-nil; an empty non-nil slice follows the special rules below;
- metadata passes P2-02's shared `prepareSourceRefresh` rules, including raw
  temporal ordering before UTC whole-second normalization;
- every game ID, row season/stage, home/away team ID, kickoff, status, and
  source last-update timestamp is nonblank and already trimmed;
- game IDs are unique and every row season/stage exactly matches the requested
  scope;
- home and away IDs differ;
- kickoff and `LastUpdatedUTC` parse with `fixtures.ParseKickoff`;
- status is exactly `PreMatch`, `FullTime`, or `Abandoned`;
- scores are either both null or both valid, and valid values are nonnegative;
- `FullTime` has both scores; and
- a valid matchday is nonnegative.

Do not rewrite or trim input. Do not validate display names or raw JSON. Do not
require scores for `Abandoned`.

If `expected` is non-nil, validate its shape before the transaction: fields are
nonnegative and not all zero; `Teams` and `GamesPerTeam` are both zero or both
positive; their product is even; and a nonzero `Games` agrees with that product.
Derive the expected game count from `Teams * GamesPerTeam / 2` when `Games` is
zero. For a nonempty response:

- enforce exact game count when one is available; and
- when team and appearance expectations exist, enforce the exact distinct
  participant count and exactly `GamesPerTeam` appearances for every team.

Apply the same expectation to the complete post-write inventory before commit.
Tests use explicit invented expectations; do not add production catalog rows.

### Empty discovery is not replacement

For an empty non-nil response, begin one transaction and count cached games in
the exact scope.

- If any game exists, return an error and roll back without an audit, full-state
  change, deletion, legacy run, snapshot, or venue update.
- If none exists, record exactly one generalized successful full `games` audit
  and full-state/due update through `recordSourceRefresh`. Counts are all zero,
  downstream impact is false, returned teams/games are non-nil empty slices,
  and `SyncRun` is nil. Do not write a legacy `sync_runs` row, fixture snapshot,
  venue summary, or source-scope transition.

The caller remains responsible for establishing that the scope is provisional
before interpreting this success as “not published” and for updating discovery
in a later orchestration packet. The storage boundary guarantees only that an
empty observation cannot delete or publish fixture data. A verified nonzero
catalog expectation does not make a first empty observation invalid.

### Database identity validation

Within the transaction, before any mutation:

- reject an incoming game ID already stored under a different season or stage;
- load all referenced teams and return `UnknownGameTeamsError` if any are
  absent; and
- load every existing exact-scope game needed for preference, pre-write
  materiality, and deletion decisions.

The `games.asa_game_id` primary key is global. Never move an existing game
between scopes by updating its season/stage. Deletion queries remain restricted
to exact season and stage.

### Source preference and row counts

Use one package-private preference helper that P2-04 can reuse. A game is
terminal when it is `Abandoned`, or `FullTime` with both scores.

- Identical rows are unchanged.
- Incoming terminal replaces cached nonterminal even if its source timestamp
  is older.
- Cached terminal is never replaced by incoming nonterminal.
- At equal terminality, accept the incoming row only when its parsed
  `LastUpdatedUTC` is strictly later than the cached value.
- If the cached timestamp is malformed but the validated incoming timestamp is
  valid, accept the incoming row.
- Equal or older timestamps preserve the cached row even when other fields
  differ.

An accepted source/fetch-only change to `LastUpdatedUTC` or `RawJSON` is a row
update. A stale preserved response is row-unchanged. Inserted, updated, and
unchanged counts describe every returned identity exactly once and sum to
`len(games)`.

### Complete snapshot, legacy lineage, and generalized audit

For a nonempty response, perform all of this in one transaction:

1. load the exact pre-write games and referenced teams and calculate its
   complete `FixtureSnapshotID` (the empty pre-write inventory has the normal
   deterministic empty snapshot);
2. insert new games and apply only preferred updates, using normalized
   `metadata.FinishedAt` as `synced_at`;
3. delete exact-scope games whose IDs are absent from the validated response;
4. reload all exact-scope games and their referenced teams in the deterministic
   result order;
5. validate the post-write inventory expectation and calculate its complete
   fixture snapshot;
6. update venue fixture/xG state under the rules below;
7. insert one successful legacy `sync_runs` row;
8. insert the generalized audit and monotonic full state with
   `recordSourceRefresh`; and
9. commit.

The legacy run uses the normalized metadata timestamps and exact scope:

```text
TeamsUpserted / inserted / updated / unchanged: 0
GamesUpserted:                                len(games)
GamesSeen:                                    len(games)
GamesInserted / updated / unchanged:          exact returned-row counts
GamesDeleted:                                 exact authoritative deletion count
FixtureSnapshotID:                            complete post-write snapshot
Outcome:                                      success
ErrorSummary:                                 ""
```

Insert the legacy row even for a nonempty no-op. Qualification and scenario
foreign keys continue to reference `sync_runs`, and `LastSuccess`, `Season`,
and `ClinchingInputs` must always see a snapshot matching the complete current
scope. Do not insert qualification or scenario rows in this packet.

The generalized audit is exactly:

```text
Resource:                  games
Season / Stage:            exact requested scope
Mode / Outcome:            full / success
RequestedRows:             0
ReturnedRows:              len(games)
RowsInserted / updated /
unchanged / deleted:       exact transaction counts
DownstreamInputsChanged:   pre-snapshot != post-snapshot
```

Raw JSON and `LastUpdatedUTC` are excluded from `FixtureSnapshotID`, so a
raw/source-timestamp-only update is audited as updated while downstream impact
remains false. Insertions, deletions, participant/status/score/kickoff/matchday
changes affect the snapshot. The assigned generalized audit and legacy run are
returned only after commit.

Any game, xG cleanup, venue, legacy run, audit, state, snapshot, or commit error
rolls back the whole transaction. Do not append a generalized or legacy failure
row automatically; later orchestration owns failed-attempt recording.

### Venue and xG readiness

Do not call or change the legacy `updateVenueFixtureSummary`, which intentionally
sets `xg_ready=0` for the existing combined `ReplaceSeason`/`ReplaceGameXG`
flow. Add new private helpers for split game persistence.

- A no-op or source/fetch-only update leaves the venue row byte-for-byte
  unchanged.
- Any fixture-snapshot change recomputes the fixture half from the complete
  post-write scope. On conflict, update only fixture readiness/counts/goals/
  points and `updated_at`; preserve existing xG readiness and aggregates.
- A score-only, kickoff-only, or matchday-only correction preserves xG
  readiness and aggregates.
- A newly inserted `FullTime` game, deletion of a `FullTime` game, accepted
  transition into `FullTime`, or accepted participant change on a game with an
  xG row invalidates stage xG coverage.
- Before changing participants, delete that game's incompatible `game_xg` row;
  game deletion continues to use its existing cascade.
- When coverage is invalidated, recompute xG aggregates from surviving current
  game/xG rows and set `xg_ready=0` in the same transaction. Do not fabricate an
  xG audit or legacy `xg_sync_runs` row.

If no venue row exists, the first material fixture write creates one with
`fixture_ready=1`, truthful fixture totals, zero truthful surviving xG totals,
and `xg_ready=0`. Use normalized `metadata.FinishedAt` for new venue timestamps.

### Legacy compatibility boundary

Keep `ReplaceSeason`, `RecordFailure`, `ReplaceGameXG`, current venue helpers,
`Status`, `Season`, `ClinchingInputs`, qualification/scenario refreshers,
`syncer.Store`, and `Service.Run` behaviorally unchanged. They do not call the
new API or emit generalized game audits yet.

Do not change current network requests, retry/reconcile behavior, leases,
scheduler selection, forecast warming, source-scope discovery/readiness, or
public product behavior. The compatibility adapter comes only after full,
targeted, and xG store operations exist.

## Allowed changes

- Add `internal/cache/game_inventory.go`.
- Add `internal/cache/game_inventory_test.go`.
- Modify `internal/cache/cache.go` only for narrowly shared private game,
  snapshot, legacy-run, and venue query/write helpers; preserve all existing
  method behavior.
- Modify `internal/cache/cache_test.go` only for legacy compatibility coverage.
- Modify `internal/cache/source_refresh_test.go` only if atomic generalized
  audit/state integration needs shared test support.
- Update `docs/asa-loading/README.md` and this packet's control status during
  implementation and review.

Do not add a migration or modify schema version. Do not modify
`internal/syncer`, `internal/scheduler`, the ASA client, commands,
configuration, HTTP, templates, production competition catalog rows,
`source_scopes`, qualification, scenarios, forecasts, pruning, CSS, or
JavaScript.

## Tests to add or update

Use fixed timestamps, invented scopes and expectations, and temporary SQLite
databases. Cover at least:

1. Blank/padded scope, nil games, malformed metadata/expectation, duplicate or
   padded IDs, row-scope mismatch, self-opponent, unknown status, malformed
   kickoff/update time, incoherent/negative scores, missing FullTime scores, and
   negative matchday fail without writes.
2. Empty unpopulated inventory records only the exact successful full audit and
   state, returns non-nil empty slices and nil sync run, ignores a valid nonzero
   expectation for publication purposes, and leaves scopes/venues/legacy rows
   unchanged.
3. Empty populated inventory fails without deleting or changing its last audit,
   state, legacy run, snapshot, venue, xG, or derived rows.
4. Nil expectation accepts an invented unknown-format inventory. Explicit game
   count, team count, per-team appearances, derived game count, and inconsistent
   response/post-write expectations are deterministic and do not consult or
   mutate the production catalog.
5. A cross-scope existing game ID is rejected and both scopes remain unchanged.
6. Multiple missing teams produce `errors.Is(ErrUnknownGameTeams)`, an ascending
   unique defensive `TeamIDs` slice, zero writes, and no misleading retry type
   for participant/cross-scope errors.
7. First insert, identical no-op, accepted newer update, stale response,
   nonterminal-to-terminal preference, terminal-regression protection, equal
   timestamp conflict, and malformed cached timestamp fallback produce exact
   counts and stored rows.
8. Raw/last-update-only accepted changes count updated but keep snapshot and
   downstream false. Every snapshot field change and authoritative deletion
   produces the exact new snapshot and downstream true.
9. Only absent exact-scope games are deleted; other season/stage rows survive.
   Post-write result teams/games are complete, deterministically ordered,
   defensive, and match `Season`/`ClinchingInputs` snapshot validation.
10. Every nonempty success inserts an exact legacy `sync_runs` lineage row with
    zero team counts, including no-op success; empty discovery inserts none.
11. No-op/raw-only writes preserve the full venue row. Score-only changes
    recompute fixture totals while preserving xG readiness/totals. Completed
    additions/deletions, FullTime transition, and xG-bearing participant change
    recompute surviving xG totals, set readiness false, and remove only
    incompatible/cascaded xG rows.
12. Test-only triggers aborting `sync_runs`, `source_refresh_audits`, and venue
    writes prove rollback of inserted, updated, and deleted games, xG cleanup,
    venue state, legacy lineage, generalized audit, and full state.
13. `ReplaceSeason`, `ReplaceGameXG`, source-scope readiness, status,
    qualification/scenario lineage, current venue behavior, syncer, and
    scheduler tests pass unchanged and do not use the new API.

Tests make no network requests and do not depend on the wall clock.

## Verification

Run from the repository root:

```text
gofmt -w internal/cache/game_inventory.go internal/cache/game_inventory_test.go internal/cache/cache.go internal/cache/cache_test.go internal/cache/source_refresh_test.go
go test -count=1 ./internal/cache
go test -count=1 ./internal/syncer ./internal/scheduler ./internal/qualification ./internal/scenariorefresh
go test -count=1 ./...
go vet ./...
git diff --check
```

Omit unchanged optional files from `gofmt`; every other command is mandatory.

## Non-goals

- Targeted checked-game writes, omissions, or per-game result cadence/state.
- xG full/targeted source operations or xG audits.
- Fetching teams/games, refreshing unknown teams, retrying a DB write, or
  changing ASA filters/validation.
- Recording failed attempts automatically.
- Source-scope discovery/lifecycle transitions or public-season publication.
- Scheduler cadence, planner selection, lease-key changes, or hot/cold work.
- Calling qualification/scenario refreshers or warming forecasts.
- Migrating lineage away from `sync_runs`.
- Integrating or branching the current `Run` facade.
- A database migration, production catalog change, or schema-level status
  vocabulary change.

## Stop conditions

Stop and report without broadening the patch if:

- complete post-write snapshots and both audit rows cannot share the mutation
  transaction;
- empty inventory cannot be distinguished from populated inventory without
  deleting or fabricating legacy lineage;
- correct isolation requires changing the global game primary key or another
  schema migration;
- unknown teams cannot be reported before mutation as a deterministic typed
  retry condition;
- truthful venue/xG state requires fabricating an xG success or modifying the
  legacy xG audit contract;
- implementation requires target omission/cadence state, syncer, scheduler,
  network, command, source-scope, qualification, scenario, or forecast changes;
- preserving the old `ReplaceSeason` path requires it to call the new API; or
- the full suite exposes a pre-existing unrelated failure.

For a pre-existing failure, report the command and distinguishing output; do
not repair unrelated code.

## Handoff

Report:

- files changed and exact exported API/error types;
- caller, expectation, database identity, and source-preference validation;
- empty discovery versus populated-empty behavior;
- exact authoritative insert/update/unchanged/delete counts;
- complete deterministic post-write snapshot and legacy lineage evidence;
- generalized audit/state and raw-only materiality evidence;
- venue and xG readiness preservation/invalidation evidence;
- atomic rollback proof;
- legacy behavior proof and every verification result;
- deviations and issues P2-04 must account for.

P2-04 may rely on the complete `GameRefreshResult`, deterministic scope snapshot
helpers, typed unknown-team boundary, shared source-preference policy, legacy
lineage insertion, generalized audit seam, and split venue/xG readiness helpers.
It adds non-deleting requested-ID checks, omission semantics, and per-game
result observation/cadence state; it does not gain deletion authority.
