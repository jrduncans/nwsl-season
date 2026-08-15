# Honeycomb query cookbook

This cookbook turns the [generated telemetry catalog](catalog/README.md) into
repeatable investigations. The catalog is the contract for field names and
meanings; this page is deliberately hand-written so query advice can evolve
without adding Honeycomb-specific prose to every convention.

The examples use Honeycomb Query Builder terms. Start with a recent time range
and the [resource scope](#resource-scope), then enter the listed `VISUALIZE`,
`WHERE`, and `GROUP BY` clauses. Use field autocomplete to confirm that the
selected environment has emitted a field before treating an empty result as a
real absence.

For latency, use `P50`, `P99`, and `HEATMAP(duration_ms)` rather than `AVG`.
Group first by bounded fields such as outcome, reason, resource, or method;
keep team IDs, game IDs, snapshot IDs, scopes, slate IDs, URLs, and timestamps
for drill-down. The names below are emitted signal names such as
`scheduler.job`, not registry convention IDs such as
`span.nwsl.scheduler.job`.

## Where did this fail?

| Query Builder clause | Value |
| --- | --- |
| `VISUALIZE` | `COUNT` |
| `WHERE` | `error.type exists` |
| `GROUP BY` | `error.type`, `nwsl.error.code`, `name` |

`error.type` answers what kind of failure occurred; `nwsl.error.code` names the
recording boundary that detected it. Do not require the code to exist: parent
spans such as `scheduler.job` and `sync.source_operation` can carry a propagated
`error.type` while the coded child or exception log identifies the original
site. Open a representative trace and follow the failed branch toward the first
coded span. Switch to the correlated log dataset and query by `exception.type`
and `nwsl.error.code` when the stack trace or message is needed; never group by
the message or stack trace.

Expected overload and cancellation need different operator responses. Check
`nwsl.error.expected`; ordinary cancellation is debug-level, while terminal
failures are error-level and absorbed or partial failures are warning-level.

## Scheduler planning and deferred jobs

If span-event annotations are exposed as queryable rows in the environment,
start with planning decisions:

| Query Builder clause | Value |
| --- | --- |
| `VISUALIZE` | `COUNT` |
| `WHERE` | `name = scheduler.decision` |
| `GROUP BY` | `nwsl.scheduler.decision`, `nwsl.scheduler.reason`, `nwsl.scheduler.job_kind` |

The `source_request_budget_exhausted` reason marks jobs omitted from a tick;
inspect `nwsl.scheduler.deferred_job_count` on that decision event. Compare the
enclosing `scheduler.tick` fields `nwsl.scheduler.job_count`,
`nwsl.scheduler.request_budget`, and `nwsl.scheduler.request_count` to see what
was planned and attempted. `no_source_request_due` is a normal no-work result.

Lease deferrals happen after planning, so query them on job spans:

| Query Builder clause | Value |
| --- | --- |
| `VISUALIZE` | `COUNT` |
| `WHERE` | `name = scheduler.job` and (`nwsl.scheduler.job_outcome = deferred_global_lease` or `nwsl.scheduler.job_outcome = deferred_scope_lease`) |
| `GROUP BY` | `nwsl.scheduler.job_outcome`, `nwsl.scheduler.job_kind`, `nwsl.scheduler.job_class`, `nwsl.scheduler.job_reason` |

For non-deferred jobs, the same query grouped by `job_outcome` and `job_kind`
shows execution shape. Drill into selection fields such as candidate, eligible,
expired, and invalid-kickoff counts only after identifying an unexpected
planner reason.

## ASA requests and source-operation outcomes

Each physical ASA attempt, including a retry, is an HTTP client span named
`HTTP GET ...`. Use the Query Builder's `starts-with` operator to filter
`name starts-with HTTP GET`, then group by `name`,
`http.response.status_code`, and `error.type`; visualize `COUNT`,
`P99(duration_ms)`, and `HEATMAP(duration_ms)`. Inspect
`http.request.resend_count` to distinguish retries from first attempts. Keep
`url.full` as a drill-down field because requested identifiers make it
high-cardinality.

Then inspect the logical operation around those attempts:

| Query Builder clause | Value |
| --- | --- |
| `VISUALIZE` | `COUNT`, `P99(duration_ms)` |
| `WHERE` | `name = sync.source_operation` |
| `GROUP BY` | `nwsl.sync.resource`, `nwsl.sync.mode`, `nwsl.sync.operation.outcome`, `error.type` |

Compare `nwsl.sync.requested_rows` with `nwsl.sync.returned_rows`. When
span-event annotations are exposed as queryable rows, `name = sync.asa_response`
selects events created only after a response was successfully decoded and
before persistence; `nwsl.asa.returned_rows` then helps separate an empty source
response from a later storage failure. The parent source operation is the
better place to compare terminal outcomes across requests.

## Data changes versus no-op observations

| Query Builder clause | Value |
| --- | --- |
| `VISUALIZE` | `COUNT` |
| `WHERE` | `name = sync.source_operation` and `nwsl.sync.operation.outcome = complete` |
| `GROUP BY` | `nwsl.sync.resource`, `nwsl.sync.mode`, `nwsl.sync.update.decision`, `nwsl.sync.update.reason` |

`updated` / `source_data_changed` means the persisted source state changed;
`not_updated` / `source_data_unchanged` is a successful observation, not a
failure. Inspect row insert/update/delete/unchanged counts and
`nwsl.sync.downstream_inputs_changed` to learn whether derived calculations
needed new inputs. The normalized-value, initialization, metadata-only,
missing-value, rejected-response, and stale-response counts explain *what*
changed or why it did not.

Use `sync.game_freshness` and `sync.xg_freshness` span events only for
per-fixture drill-down. Group those by bounded `nwsl.sync.update_kind`,
`nwsl.sync.decision`, or `nwsl.sync.rejection_kind`, then inspect
`nwsl.asa.game.id` on representative events.

## Qualification and scenario budgets

Budget exhaustion can produce a valid but unresolved result, so do not limit
this investigation to error spans.

For qualification, query `name = qualification.refresh`, filter
`nwsl.qualification.result.budget_exhausted_count > 0`, and group by
`nwsl.qualification.outcome` and `nwsl.qualification.refresh_reason`. Compare
the budget with status-proof and no-help total, maximum, and slow counts.

For scenarios, query `name = scenario.refresh`, filter
`nwsl.scenario.result.budget_limited_count > 0`, and group by
`nwsl.scenario.outcome`, `nwsl.scenario.refresh_reason`,
`nwsl.scenario.slate_state`, and `nwsl.scenario.slate_source`. Inspect maximum
team-search duration, search-node counts, and oracle call/cache-hit totals.

To find expensive individual work, use separate queries for
`qualification.status_proof`, `qualification.no_help_batch`, and
`scenario.generate_team` with `COUNT`, `P50(duration_ms)`,
`P99(duration_ms)`, and `HEATMAP(duration_ms)`. These child spans are emitted
for every failure but only for successful work that crosses the 25 ms slow
threshold, so they are a diagnostic sample rather than a complete workload
distribution. First group by bounded method, status, or slate fields; then
inspect team, achievement, reduced-problem, visited-state, memo-hit, prune,
assignment, search-node, and oracle fields on an outlier trace.

## Forecast cache, models, and timeouts

| Query Builder clause | Value |
| --- | --- |
| `VISUALIZE` | `COUNT`, `P50(duration_ms)`, `P99(duration_ms)`, `HEATMAP(duration_ms)` |
| `WHERE` | `name = forecast.run` |
| `GROUP BY` | `nwsl.forecast.outcome`, `nwsl.forecast.trigger`, `nwsl.forecast.model_ids` |

The `cache_hit` and `computed` outcomes, together with
`nwsl.forecast.cache_hits` and `nwsl.forecast.calculation_count`, separate
reuse from actual calculations. Compare iteration, fixture, completed-fixture,
xG-observation, fixed-assumption, and playoff-place counts before attributing a
latency difference to the model alone.

For timeouts, add `nwsl.forecast.outcome = timed_out` and confirm
`error.type = timeout` with `nwsl.error.code = forecast.run`. An `overloaded`
outcome is expected load shedding and carries `nwsl.error.expected = true`.
To see interactive model selection, query server request spans where
`nwsl.forecast.model_id exists`, group by `nwsl.forecast.model_id` and
`nwsl.forecast.comparison_requested`, and inspect the child `forecast.run`.
For warming, query `name = forecast.precache` and group by trigger, precache
outcome, model count, and failed-model count.

## Resource scope

Apply resource filters before comparing behavior across processes or releases:

| Field | Use |
| --- | --- |
| `deployment.environment.name` | Separate `test` from `production`. |
| `service.name` | Separate `nwsl-season-server` from `nwsl-season-sync`. |
| `service.instance.id` | Isolate the shell, VM, or other stable runtime instance. |
| `service.version` | Compare the exact build or release, including a dirty local build. |

Use one environment and service for the initial query. Add `service.version` to
`GROUP BY` when checking a regression across a deployment, or filter to a
single version while investigating one trace. Use `service.instance.id` to
decide whether an anomaly is process-wide or isolated to one runtime.

Span events such as scheduler decisions and freshness observations appear as
annotations in their parent trace. If an environment's dataset mapping does not
make them directly queryable by `name`, open a representative parent trace and
copy the annotation-name field offered by Honeycomb before adapting the recipe.
