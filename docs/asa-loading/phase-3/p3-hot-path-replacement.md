# Phase 3: Replace the current hot path

## Control

- Status: Ready
- Implementation: Terra
- Review: primary agent; use a separate Sol gate only for scheduler concurrency
  or lease changes
- Depends on: P2-01 through P2-06
- Checkpoints: one plan commit, then one implementation commit per workstream

## Outcome

Move production callers from the monolithic full-season `Service.Run` path to
the split Phase 2 persistence operations. A scheduler tick plans independently
due resource jobs, batches every due game identity for a scope, and makes no
unrelated ASA request. Source checks recalculate only the products invalidated
by a material change.

This phase has two implementation workstreams. Workstream A establishes the
network-to-store operation boundary. Workstream B selects and executes due
jobs. Do not create a separate packet for every bullet below.

## Inherited contracts

Treat the completed Phase 1 and Phase 2 packets as authoritative. In
particular:

- full game and xG operations alone may delete or claim complete scope state;
- targeted omissions update check state without deleting or fabricating source
  rows;
- per-game due times and full-resource due times are persisted independently;
- unknown game participants require an independent team refresh and one
  persistence retry;
- fixture changes may invalidate qualification, scenarios, venue results, and
  forecasts; xG changes may invalidate venue xG and forecasts only; and
- leases, audits, legacy lineage, and successful source transactions remain
  owned by the cache APIs already implemented.

Do not repeat their exhaustive persistence test matrices in Phase 3. Test each
new orchestration boundary and rely on the cache package for row-level
semantics.

## Workstream A: split-operation sync adapter

### Operation boundary

Add a syncer operation vocabulary that can execute exactly one source action:

- team catalog refresh;
- authoritative game inventory refresh for one season/stage;
- targeted game refresh for a nonempty batch of IDs;
- authoritative stage xG refresh; or
- targeted xG refresh for a nonempty batch of IDs.

The scheduler-facing operation/result types may be internal to `syncer`, but
must identify resource, mode, scope, requested IDs, trigger, force, and timing.
IDs are unique, sorted private copies. Results expose the committed cache
result and whether fixture-dependent or xG-dependent downstream inputs
materially changed.

Each operation makes one logical ASA resource request. The only allowed
additional request is a team-catalog refresh after a validated game response
fails persistence with `cache.ErrUnknownGameTeams`, followed by one retry of
the same already-fetched game response. Do not refetch games for that retry.

### ASA filters and mapping

Add `GameID string` to `asa.XGoalsFilters` and serialize it as `game_id`.
Targeted game and xG batches use a deterministic comma-separated list of IDs.
Do not add chunking until a measured URL or upstream limit requires it.

Reuse the existing game/xG mapping and validation helpers. A targeted response
must be passed to the corresponding checked cache API with the exact requested
ID batch. Nil responses, unrequested identities, participant mismatches, and
invalid numeric values fail without falling back to a full request.

### Full and targeted execution

- Team refresh calls `UpsertTeams` and never fetches games or xG.
- Full games fetches the exact season/stage collection and calls
  `ReplaceGameInventory`. Empty first discovery is a successful not-published
  observation; empty replacement of a populated scope remains an error.
- Targeted games fetches only the requested IDs and calls
  `UpsertCheckedGames`. Omission is successful checked-but-unchanged work.
- Full xG fetches only xG and calls `ReplaceStageXG` after fixture inventory is
  present.
- Targeted xG fetches only the requested IDs and calls `UpsertCheckedXG`.
  Omission never erases available xG.

Fetches are sequential at the operation boundary. Remove concurrent
teams/games/xG collection, target-versus-collection reconciliation, the 250 ms
incomplete-inventory retry, and the single `TargetFixtureID` path once no
caller uses them.

### Compatibility facade

Keep `Service.Run` and its CLI/server return shape during this phase. Implement
it as a forced compatibility sequence over the new operations rather than the
legacy `ReplaceSeason`/`ReplaceGameXG` path:

1. refresh teams when forced or when the catalog is required;
2. commit full games;
3. commit full xG;
4. run derived calculations only for committed material inputs; and
5. preserve current partial-failure reporting and history pruning.

The compatibility facade is not used for targeted scheduler jobs after
Workstream B. It remains for manual callers until the later command cleanup.

Upstream or validation failures append one generalized failure audit for the
owning resource/mode and do not mutate success state. Avoid double-recording
the same failure through both legacy and generalized paths.

### Workstream A acceptance

- xG `game_id` is encoded exactly and covered by the ASA client test.
- Each operation's fake client call log proves which endpoints were and were
  not requested.
- Multiple targeted IDs produce one deterministic request and one checked
  cache call.
- Targeted empty responses preserve cached rows and still advance supplied
  check/due state.
- Unknown-team recovery performs games, teams, then persistence retry without
  a second games fetch.
- `Service.Run` no longer calls `ReplaceSeason` or `ReplaceGameXG`, while its
  current command/server behavior remains compatible.
- The obsolete reconciliation and 250 ms retry tests/code are removed rather
  than adapted.

## Workstream B: due-job planner and scheduler execution

### Planning snapshot

Add one cache read API that returns a consistent, defensive planning snapshot
from a read-only transaction. It contains registered source scopes, season
readiness, full games/xG due state, scoped games and xG values, and result/xG
check states. It performs no repair or due-time mutation.

The planner is a pure function of that snapshot, a caller-supplied clock, and
configuration. It returns a deterministic ordered list of jobs; it performs no
network or database write.

### Phase 3 priorities and batching

For the configured/current scope and registered upcoming regular-season scope,
plan in this order:

1. missing or not-yet-published inventory whose full check is due;
2. one targeted games job containing every due result ID in the scope;
3. one targeted xG job containing every due completed-game xG ID in the scope;
4. weekly authoritative inventory audits for incomplete/upcoming scopes; and
5. repairable cached derived work when no source request is due.

Within a priority, order scopes by season descending then stage, and IDs
ascending. Apply a configurable small per-tick source-request budget. Carry
unselected jobs forward by leaving persisted due state unchanged.

One job owns one resource/mode/scope lease. Fixture work for the same scope is
serialized. Do not introduce parallel ASA requests; execute selected jobs in
priority order and stop starting lower-priority work when the tick budget or
context expires.

### Phase 3 cadence

Use configurable defaults, with fake-clock tests:

- unresolved results after kickoff plus completion grace: every scheduler
  interval (normally five minutes);
- completed games with missing xG: every scheduler interval for five days;
- terminal result or available xG correction watch: every six hours for five
  days after first observation or latest material change, then daily through
  day 30; and
- incomplete/upcoming authoritative game inventory: weekly.

First-observation and material-change clocks already persisted by Phase 2 are
the anchors. A material correction restarts the five-day watch. An unchanged
check advances its check/due clock but not its material clock. If migrated
terminal data lacks an observation clock, schedule a result check to establish
truthful observation state before relying on a correction window.

Phase 5 owns monthly archived full sweeps, cross-scope cold coordination,
staggering, and maintenance commands. Phase 3 must not add those policies.

### Selective downstream work

After each committed job:

- targeted/full material game changes may run qualification, scenarios, and
  forecast warming;
- targeted/full material xG changes may warm forecasts but never rerun
  qualification or scenarios;
- team presentation changes do not rerun fixture calculations; and
- no-op, omitted, stale, or failure results run no derived work.

Keep cached-data recalculation available when source state is current but a
derived batch is missing.

### Workstream B acceptance

- An idle cache produces zero ASA requests.
- Multiple due results are one games request; multiple due xG identities are
  one xG request.
- A due result makes no teams, full-games, or xG request.
- A due xG check makes no teams or games request.
- Unresolved and missing-xG jobs remain due at five-minute cadence without
  exponential backoff.
- Six-hour/daily correction windows and material-change restarts are selected
  by fixed clocks.
- An empty upcoming scope is checked weekly without becoming publicly
  available until inventory exists.
- The request budget defers lower-priority jobs without changing their due
  state.
- A no-op job triggers no derived work; material game and xG jobs trigger only
  their permitted downstream products.
- Scheduler telemetry records job kind, scope, requested/returned counts,
  material-change outcome, and request count without duplicate exceptions.

## Allowed changes

- `internal/asa` filters/client tests;
- `internal/syncer` operation adapter, compatibility facade, and tests;
- `internal/scheduler` planner/executor/configuration and tests;
- narrow cache read APIs and tests required for a consistent planning snapshot;
- server/CLI wiring and tests needed to preserve the current facade; and
- this plan and packet index status.

Do not change source persistence schemas, Phase 2 mutation semantics,
competition rules, historical product navigation, playoff modeling, monthly
cold sweeps, or maintenance command design.

## Verification

During implementation, run focused package tests. Before each implementation
checkpoint run once:

```text
go test -count=1 ./internal/asa ./internal/cache ./internal/syncer ./internal/scheduler
go test -count=1 ./cmd/sync ./cmd/server
go test -count=1 ./...
go vet ./...
git diff --check
```

Do not rerun the full suite solely for a review that changes documentation or
tests.

## Stop conditions

Stop rather than broadening the phase if:

- the ASA endpoint does not accept a deterministic comma-separated `game_id`
  batch;
- a targeted operation requires an authoritative full response to remain
  correct;
- truthful job selection requires inventing first-observation/material history;
- the current cache APIs cannot keep a source write, due state, audit, and
  lineage atomic;
- selective invalidation requires changing qualification/scenario contracts;
  or
- safe scheduler execution requires the Phase 5 cold coordinator.

## Handoff and checkpoints

After Workstream A, commit the adapter and compatibility migration with its
focused/full verification result. Then implement Workstream B and update this
plan plus the packet index to Complete after the scheduler concurrency gate.
Report logical ASA request counts for idle, due-result, due-xG, bootstrap, and
weekly-inventory cases.
