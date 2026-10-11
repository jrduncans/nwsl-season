# Reading sync telemetry

Attribute names, types and stable values live in the generated catalog:
[scheduler](../../../../docs/telemetry/catalog/scheduler.md),
[synchronization](../../../../docs/telemetry/catalog/synchronization.md), and
[resources and errors](../../../../docs/telemetry/catalog/resources-and-errors.md).
Query only attributes listed there. Severity and `error.type` conventions are
in the [telemetry guide](../../../../docs/telemetry/README.md#error-classification).

## Signal to pipeline hop

| Signal | Pipeline hop |
| --- | --- |
| `scheduler.tick` | Planning snapshot, pure planner result, selected sequential jobs, and cache-only derived work. |
| `scheduler.decision` event | One planner check/not-check decision with its reason and selection details. |
| `scheduler.job` | One selected source job: lease outcome, source-operation result, materiality. |
| `sync.source_operation` | Exactly one ASA resource request, validation/mapping, and the resource-specific persistence decision. |
| `sync.game_freshness` event | Per-game source/cache freshness comparison within a game operation. |

The scheduler's selection diagnostics (selection policy, candidate/eligible
counts, polling windows, kickoff range) explain *why* targeted work did or did
not select IDs. Start broad queries with aggregate counts; examine individual
events only after narrowing to a trace.

## Outcome meanings

| Observation | Meaning |
| --- | --- |
| complete / success | ASA response validated and the resource-specific mutation/audit committed. |
| no-op | A valid observation produced no material source-input change. |
| omitted | A targeted response did not include a requested ID; it is a checked observation, not deletion. |
| stale | Response data was older than durable data; it must not regress the cache. |
| failure | Request, validation, mapping, persistence, or lease-acquisition failure; inspect `error.type` and `nwsl.error.code`. |
| deferred lease | No ASA request occurred because a competing server or maintenance process holds the required lease. |
