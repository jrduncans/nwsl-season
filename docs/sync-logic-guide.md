# How synchronization works

Ordinary web requests read the local SQLite cache only. ASA requests are made
by the background scheduler or an explicit `cmd/sync` maintenance command.
The scheduler does not run the former season-wide concurrent `Service.Run`
flow, and server startup does not refresh historical seasons.

The [ASA loading packet index](asa-loading/README.md) is the implementation
plan and change history. This guide summarizes the delivered operational model.

## Source data and derived data

- **Source data** is upstream-owned: the team catalog, game inventory and
  results, and game xG. Each source request has one owning cache mutation and
  its own audit and cadence state.
- **Derived data** is computed from cached fixtures: qualification and
  clinching scenarios. Recalculation reads the cache and does not contact ASA.
- **Forecast inputs** include fixtures and xG from the current regular season
  and the two preceding regular seasons. Historical seasons are loaded only by
  the explicit historical-backfill command; page reads and server startup do
  not make ASA requests to fill them.

## Scheduler: pure planning, then sequential execution

On startup and each `NWSL_SYNC_CHECK_INTERVAL` tick, the scheduler reads one
consistent planning snapshot from SQLite. `scheduler.Plan` is pure: it uses the
snapshot, configuration, and a supplied clock to return an ordered batch of
due jobs. Planning does not make HTTP requests, acquire leases, or advance due
times.

```mermaid
flowchart TD
    SNAPSHOT["Read SQLite planning snapshot"] --> PLAN["Pure planner selects due jobs"]
    PLAN --> HOT{"Hot work due?"}
    HOT -->|"Yes"| BUDGET["Keep priority-ordered jobs within request budget"]
    HOT -->|"No"| COLD["Select one due archived full-resource job"]
    BUDGET --> EXECUTE["Execute jobs sequentially"]
    COLD --> EXECUTE
    EXECUTE --> LEASE["Acquire required SQLite lease(s)"]
    LEASE --> OP["Execute exactly one source operation"]
    OP --> CACHE["Atomically persist that resource and its audit/due state"]
    CACHE --> NEXT{"Another selected job?"}
    NEXT -->|"Yes"| EXECUTE
    NEXT -->|"No"| DERIVED["Repair eligible cached derived work"]
```

Hot work takes precedence over archived correction work. Within the hot tier,
the planner orders jobs as follows:

1. authoritative games inventory when a scope is missing or not yet published;
2. one batched targeted-games request for due result checks in a scope;
3. authoritative full-stage xG when a published inventory has not yet had one;
4. one batched targeted-xG request for due completed games in a scope; and
5. weekly authoritative inventory audits for active or upcoming scopes.

The default source-request budget is three operations per tick. Jobs beyond the
budget stay due because planning does not mutate their cadence state. Selected
operations execute sequentially; there is no concurrent teams/games/xG fetch
or single-target reconciliation path.

### Independent result and xG cadence

Games and xG are independent resources with independent full and per-game
check state. A targeted operation sends all selected IDs for one resource and
scope in one request; an omitted ID is a checked observation, not a deletion.

| Work | Default cadence and window |
| --- | --- |
| Unsettled result after kickoff plus completion grace | Every scheduler interval (normally 5 minutes) while due. |
| Terminal result correction | Every 6 hours for 3 days after kickoff. |
| Missing xG for a completed game | Every 5 minutes for 5 days after kickoff. |
| Available xG correction | Every 6 hours for 5 days after kickoff. |
| Active/upcoming full inventory audit | Every 7 days. |

Invalid or missing kickoffs are not turned into immediate targeted polling;
authoritative inventory reconciliation remains the safety net. Material game
changes in the configured current scope can trigger cache-only qualification
and scenario recalculation. Material fixture or xG changes in the current or
previous two regular seasons can warm forecasts. No-op, omitted, stale, or
failed source observations do not create derived work.

## Split source operations

`syncer.Service.Execute` performs exactly one source operation:

| Resource | Full mode | Targeted mode |
| --- | --- | --- |
| Teams | Fetches `/nwsl/teams` and upserts the independent catalog. | Not used. |
| Games | Fetches the authoritative season/stage collection and replaces that scope's inventory. | Fetches requested game IDs and updates only those checked games. |
| Game xG | Fetches authoritative stage xG after fixture inventory exists. | Fetches xG for requested game IDs and updates only those checked values. |

The operation validates and maps the one ASA response before invoking its
resource-specific cache API. Full game inventory replacement retains a prior
complete inventory when an incoming known inventory is incomplete or uneven.
Targeted operations do not delete unrequested records. If a game write finds
unknown teams, the service refreshes the catalog once and retries the same
validated write without refetching games.

Each operation persists its own success or failure audit and due state. A
failed request does not advance a successful observation or replace a durable
resource state. HTTP retries remain bounded by the per-operation request
deadline.

## Leases and archived corrections

Before executing a hot job, the scheduler acquires a SQLite lease for that
season/stage scope. This serializes source writes across server and maintenance
processes. If the lease is unavailable, the job is deferred without an ASA
request.

Completed, available scopes become eligible for cold correction sweeps only
after a successful full games observation. When no hot job is due, the planner
selects at most one due cold job: full games first, then full xG on a later
planner pass. Cold work has a 30-day default interval, staggered per scope.
It acquires a global cold-sweep lease followed by the scope lease, releasing in
reverse order. A material historical correction is reported so model-evaluation
evidence can be regenerated.

## Explicit sync and maintenance commands

The normal `cmd/sync` command is a forced selected-scope compatibility
sequence, not the scheduler's hot path. It conditionally refreshes teams,
then runs full games and full xG sequentially under the scope lease. It can
then run derived calculations for a scope with configured competition rules.

The maintenance modes are mutually exclusive:

- `-recalculate` runs qualification and clinching calculations from cached
  fixtures without contacting ASA.
- `-backfill-historical` loads supported historical regular seasons
  sequentially through the compatibility sequence. It is the only historical
  source-loading path.
- `-sweep-due-archived` repeatedly takes fresh snapshots and runs due cold
  work until complete, deferred by hot work or a lease, or failed.
- `-prune-history-before` removes superseded operational history before the
  supplied RFC 3339 time.

`-force` asks derived calculations to run after the normal selected-scope
source sequence; it never bypasses source validation.

## Operational defaults

`NWSL_SYNC_TIMEOUT` defaults to `2m` and bounds one scheduler source operation
or one explicit selected-scope sync. `NWSL_SYNC_CHECK_INTERVAL` defaults to
`5m`; `NWSL_SYNC_COMPLETION_GRACE` defaults to `2h`. The public configuration
table in [README.md](../README.md#configuration) is authoritative for
environment-variable defaults.

## Implementation map

- Pure job priority, batching, cadence, and cold selection:
  [`internal/scheduler/planner.go`](../internal/scheduler/planner.go)
- Scheduler ticks, budgets, leases, execution, and cache-only recalculation:
  [`internal/scheduler/scheduler.go`](../internal/scheduler/scheduler.go)
- One-resource ASA requests and cache mutations:
  [`internal/syncer/operations.go`](../internal/syncer/operations.go)
- Selected-scope compatibility and derived recalculation:
  [`internal/syncer/syncer.go`](../internal/syncer/syncer.go)
- Explicit command modes:
  [`cmd/sync/main.go`](../cmd/sync/main.go)
