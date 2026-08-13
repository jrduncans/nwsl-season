# Sync telemetry catalog

Load this reference for trace investigation or when mapping code to stable telemetry. It records stable, low-cardinality project attributes; add a narrow attribute only after confirming it in the implementation.

## Spans and events

| Signal | Meaning |
| --- | --- |
| `scheduler.tick` | A cache planning snapshot, pure planning result, selected sequential jobs, and cache-only derived work. |
| `scheduler.decision` event | A planner decision to check or not check, including reason and selection details. |
| `scheduler.job` | One selected source job, its lease outcome, source-operation result, and materiality. |
| `sync.source_operation` | Exactly one ASA resource request, validation/mapping, and resource-specific persistence decision. |
| `sync.game_freshness` event | Per-game source/cache freshness comparison for a game operation. |

## Shared attributes

| Attribute | Values / use |
| --- | --- |
| `nwsl.season`, `nwsl.stage` | Scope identity. |
| `error.type` | `canceled`, `timeout`, `invalid_argument`, `invalid_data`, `conflict`, `upstream_failure`, `storage_failure`, `calculation_failure`, `_OTHER`. |
| `nwsl.error.code` | Specific low-cardinality detection site; use with `error.type`, not as a replacement. |

Expected cancellation is `DEBUG`; absorbed/retryable or partial failure is `WARN`; terminal failure is `ERROR`.

## Scheduler attributes and outcomes

| Attribute | Stable values / interpretation |
| --- | --- |
| `nwsl.scheduler.action` | `read_planning_snapshot`, `recalculate`, or tick-specific action. |
| `nwsl.scheduler.outcome` | `complete`, `failure`, `deferred`, `partial_failure`, or a recalculation outcome. |
| `nwsl.scheduler.decision` | `check` or `not_check`. |
| `nwsl.scheduler.reason` | Why the planner selected or declined work, including `no_source_request_due` and `source_request_budget_exhausted`. |
| `nwsl.scheduler.job_kind` | Source job kind. |
| `nwsl.scheduler.job_class` | `hot` or `cold`. |
| `nwsl.scheduler.job_outcome` | `planned`, `complete`, `failure`, `deferred_scope_lease`, or `deferred_global_lease`. |
| `nwsl.scheduler.job_scope` | `season/stage`. |
| `nwsl.scheduler.job_material` | Whether the job changed fixture or xG inputs materially. |
| `nwsl.scheduler.request_budget`, `nwsl.scheduler.request_count`, `nwsl.scheduler.job_count` | Source-operation planning/execution counts. |
| `nwsl.scheduler.clinching_preflight_outcome` | `not_needed`, `current`, `complete`, `partial_failure`, or `failure`. |
| `nwsl.scheduler.forecast_warm.outcome` | The forecast warmer outcome, set by the server runner. |
| `nwsl.scheduler.evaluation_evidence_dirty` | True for a material historical regular-season correction. |

Selection diagnostics are `nwsl.scheduler.selection_policy`, candidate/eligible/expired/invalid-kickoff counts, missing/available xG candidate and eligible counts, polling/window seconds, and oldest/newest kickoff UTC. Use them to explain *why* targeted work did or did not select IDs.

## Source-operation attributes

The stable source-operation namespace is `nwsl.sync.*`; it reports resource, mode, trigger, outcome/decision, requested and returned counts, and freshness/materiality. Game freshness events use `nwsl.sync.decision`, old/new score values where relevant, and ASA/cache last-updated timestamps. Prefer aggregate counts in broad queries, then examine individual events only after narrowing a trace.

## Outcome interpretation

| Observation | Meaning |
| --- | --- |
| complete / success | ASA response validated and the resource-specific mutation/audit committed. |
| no-op | A valid observation produced no material source-input change. |
| omitted | A targeted response did not include a requested ID; it is a checked observation, not deletion. |
| stale | Response data was older than durable data; do not let it regress the cache. |
| failure | Request, validation, mapping, persistence, or lease-acquisition failure; inspect `error.type` and `nwsl.error.code`. |
| deferred lease | No ASA request occurred because a competing server or maintenance process holds the required lease. |
