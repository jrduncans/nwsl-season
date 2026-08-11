# Phase 5: Archived correction sweeps and maintenance

## Control

- Status: Ready
- Implementation: Terra
- Review: primary agent; one Sol gate for leases and concurrency
- Depends on: Phase 4
- Checkpoints: one plan commit and one implementation commit

## Outcome

Add the remaining cold-data policy without reopening the completed hot path.
Completed source scopes receive deterministic, monthly, full games-then-xG
correction sweeps. Automatic cold work runs only when no current/upcoming source
job is due, starts at most one cold request per scheduler tick, and is globally
serialized across processes. Operators can run all currently due archived work
sequentially through the same planner and executor.

Phase 3 already implemented five-minute, six-hour, and daily result/xG watches,
first-observation clocks, material-change restarts, and the day-30 boundary.
Do not duplicate or redesign them here.

## Archived eligibility and cadence

An archived scope is cold-sweep eligible only when:

- its persisted lifecycle is `completed`;
- readiness is `available` with cached games; and
- at least one successful full games observation exists.

Unknown/not-published historical catalog scopes remain the responsibility of
the explicit Phase 4 backfill. Do not turn a correction sweep into initial
historical loading.

Add `ColdSweepInterval` to scheduler configuration with a 30-day default. Zero
selects the default; negative values are invalid. Do not add a monthly team
catalog safety refresh. Unknown-team recovery remains the only extra team
request allowed during a cold games operation.

For a full-resource state with a persisted `next_full_due_at`, that timestamp
is authoritative. For a successfully loaded legacy/backfill state whose due
time is nil, derive its first cold due time from `last_full_success_at` plus a
stable scope hash offset within one cold interval. The hash input is exactly
season, a NUL separator, and stage. It must be deterministic across processes
and Go versions; do not use randomized map/hash state.

After a successful cold games or xG operation, persist the next due time as the
actual observation finish plus one cold interval. Failures do not advance
success or due state.

## Games-before-xG chain

For one archived scope, a correction cycle is:

1. one authoritative full games request and commit;
2. on a later planner pass, one authoritative full xG request and commit.

Never plan both cold resources in one scheduler tick. A due games resource
wins over xG for its scope. After games succeeds, xG is the next cold step when
its last full success predates the new games success, even if its old due
pointer was later. A games failure prevents the paired xG step. Full cache
contracts continue to own validation, deletion, audit, venue summary, and due
state atomically.

Across scopes, select one cold candidate deterministically by effective due
time, then season descending, stage ascending. A completed scope cannot produce
targeted jobs.

## Hot priority and execution

Classify planned jobs as hot or cold. Existing current/upcoming jobs are hot.
The pure `Plan` behavior is:

- if any hot job is due, return only hot jobs under the existing request
  budget; and
- otherwise return at most one due cold job.

This guarantees a cold request never consumes a tick while known hot work is
waiting. Because only one cold job is returned, the scheduler takes a fresh
snapshot before the next cold step; hot work that becomes due during an
in-flight cold request wins the next tick.

Cold execution acquires two SQLite leases in one fixed order:

1. one global cold-sweep lease shared by every process and scope;
2. the existing season/stage scope lease used by `Service.Run` and hot jobs.

If either lease is unavailable, make no ASA request and release any lease
already acquired. Release in reverse order on success, failure, timeout, or
stop. Hot jobs continue to acquire only their scope lease. Keep the active
request bounded; `Stop` may let it finish but prevents another job from
starting.

Use trigger `scheduler` for automatic cold work and add/use trigger
`maintenance` for the explicit due-sweep command. Telemetry adds job class,
cold global-lease outcome, and whether a material archived change affected
checked-in evaluation evidence.

## Maintenance command

The existing single-scope sync command remains the forced selected-scope audit:

```text
nwsl-season-sync -season 2024 -stage "Regular Season"
```

It already bypasses due policy through the Phase 3 compatibility facade. Do
not add a second selected-scope implementation.

Add `-sweep-due-archived` to `cmd/sync`. It repeatedly:

1. reads a fresh planning snapshot;
2. stops and reports deferred if hot work is due;
3. plans the next single cold job;
4. executes it through the same dual-lease scheduler path; and
5. stops when no archived resource is due, the command context expires, a
   lease is unavailable, or a request fails.

Expose only the narrow scheduler method/report needed by the command. Each ASA
request gets the configured sync timeout; the command is sequential and never
holds a transaction or lease between requests. Output identifies resource,
scope, material/no-op outcome, completion/defer reason, and logical request
count.

`-sweep-due-archived` is mutually exclusive with historical backfill,
recalculation, and history pruning. It does not force not-yet-due scopes.

## Historical downstream effects

The existing generalized audit row durably identifies every historical
resource/scope and whether downstream inputs materially changed. Do not add an
evidence table or rewrite checked-in model-evaluation artifacts.

For a material cold games or xG change:

- cache persistence already recomputes the affected historical venue summary;
- the automatic server path warms current forecasts only when the corrected
  season is one of `competition.PreviousRegularSeasons(currentSeason, 2)`;
- qualification and scenarios never run for the historical scope; and
- the automatic span/log and maintenance report mark model-evaluation evidence
  as requiring regeneration for the factual historical catalog seasons.

A no-op cold response does none of that. Forecast warming or reporting failure
must not turn a successful source transaction into a failed audit.

## Acceptance

- Fixed-clock planner tests cover completed-only eligibility, deterministic
  first staggering, persisted due precedence, 30-day recurrence, games before
  xG, and one cold job per plan.
- Hot result, xG, bootstrap, or weekly inventory work suppresses every cold job
  without changing archived due state.
- Two scheduler instances cannot overlap cold requests for different scopes;
  a manual/hot scope lease also defers the matching cold job.
- Global then scope acquisition, partial-acquire cleanup, reverse release,
  failure, timeout, and stop behavior are failure-sensitive.
- After a cold games success, a fresh snapshot selects paired xG; a games
  failure does not. The planner rechecks for hot work before that xG step.
- The maintenance command processes all and only due archived jobs in the same
  deterministic order, uses trigger `maintenance`, stops on defer/failure, and
  rejects incompatible modes.
- Material 2024/2025 corrections for current 2026 warm forecasts and report
  evidence dirty; older/no-op corrections do not warm current forecasts, and
  no historical correction runs qualification/scenarios.
- Existing Phase 3 request counts and hot fake-clock tests remain unchanged.

## Allowed changes

- `internal/scheduler` cold planning, execution, reports, and tests;
- narrow sync operation/audit trigger changes needed for persisted cold due;
- `cmd/sync` maintenance wiring and tests;
- `cmd/server` historical venue-dependent forecast warming and tests;
- this phase plan and packet index.

Do not add a migration, team refresh cadence, parallel requests, background
historical bootstrap, checked-in evidence rewriting, playoff behavior, or new
ASA resources.

## Verification

During implementation, use focused tests. Before the implementation commit run
once:

```text
go test -count=1 ./internal/scheduler ./internal/syncer ./cmd/sync ./cmd/server
go test -count=1 ./...
go vet ./...
git diff --check
```

## Stop conditions

Stop rather than broadening the phase if:

- dual global/scope leases cannot reuse the current SQLite lease API safely;
- correct hot preemption requires canceling an in-flight bounded cold request;
- cold games/xG ordering requires a new persistence schema; or
- evidence reporting requires mutating checked-in artifacts from the server.

## Handoff

Implement the whole phase in one Terra pass. The primary review covers planner
policy, maintenance behavior, and downstream isolation. Use one final Sol gate
only for dual-lease ordering, cleanup, and cross-process concurrency.
