---
name: nwsl-sync-investigation
description: Investigate NWSL Season missing fixtures or xG, "recalculation pending", unexpected scheduler jobs, ASA sync errors, cache freshness, and production traces. Use for read-only diagnosis of planner decisions, source operations, persistence, derived recalculation, forecast warming, or Honeycomb trace investigations; do not use to run a production sync or repair unless explicitly requested.
---

# NWSL sync investigation

Diagnose the local SQLite-backed synchronization pipeline without changing production state by default. Read `README.md`, `docs/sync-logic-guide.md`, and `docs/asa-loading/README.md` before drawing conclusions; inspect the implementation named by those guides when a claim needs code-level proof.

## Investigation workflow

1. Establish the scope: season/stage, game IDs, symptom, time window, whether the data is fixture or xG, and whether the environment is local or production. Treat a page as cache-only: it must not explain missing data by a page-triggered ASA request.
2. Start with the planning snapshot. Determine readiness, published inventory, refresh/audit state, per-game checks, due times, hot/cold eligibility, and the configured request budget.
3. Reproduce the pure planner decision at the relevant clock time. Report the selected job(s), their reason and class, and jobs that were intentionally deferred by priority or the source-request budget.
4. Follow one selected job end-to-end: scope/global lease → one ASA source operation → response validation and mapping → resource-specific atomic persistence plus audit/due state → downstream work. Keep the evidence for each hop distinct.
5. Classify the observation and explain the result: success/no-op/omitted/stale/validation failure/request failure/lease deferral. A failed response cannot advance successful observation state or replace durable source state.
6. Evaluate downstream effects separately. Current-scope material fixture changes can request cache-only qualification/scenario recalculation. Material fixture or xG changes in the current or two prior regular seasons can warm forecasts. Historical cold corrections instead mark evaluation evidence dirty. No-op, omitted, stale, and failed observations create no downstream work.
7. State the narrowest safe next action. Remain read-only unless the user explicitly authorizes a production sync, cache repair, or other mutation. Do not read `config.env`; use documented configuration or `NWSL_CONFIG_FILE=/dev/null` for isolated local inspection.

## Interpret the pipeline correctly

- **Planner decisions** are pure: snapshot + config + clock, with no HTTP, leases, or due-state updates. Hot work has priority; only when none is due may one archived cold job be selected.
- **Source operations** are exactly one resource at a time: team catalog, full or targeted games, or full or targeted xG. They do not themselves run derived calculations.
- **Persistence** owns data integrity. Full fixture replacement retains a prior complete fixture inventory if an incoming known inventory is incomplete or uneven. Targeted omissions are checked observations, never deletions.
- **Derived recalculation** reads cached fixtures and never contacts ASA. “Recalculation pending” means inspect its eligibility, trigger, and any qualification/scenario partial failure independently from source freshness.
- **Forecast warming** is distinct from clinching work. Explain why a material xG-only change may warm forecasts without recalculating clinching, and why a current fixture change may do both.

### Games versus xG

Games and xG have independent full-refresh and per-game check state. A game result, schedule, or inventory change is fixture input; an xG value change is xG input. Missing xG remains due for its own watch window even when a game is terminal. Do not infer a missing game from a missing xG response, or the reverse.

### Hot versus archived cold work

Hot work is current/active or upcoming responsiveness: missing inventory, targeted result checks, first full xG, targeted xG, and weekly active inventory audits. Cold work is an archived, full-resource correction sweep, selected one at a time only when no hot job is due and guarded by the global cold lease plus the scope lease. Material historical corrections require evaluation-evidence regeneration; they do not revive hot polling.

## Production telemetry

When the user requests production telemetry or a Honeycomb investigation, use the `honeycomb:production-investigation` skill before querying it. Follow its context-priming → broad query → BubbleUp → trace analysis → verification workflow. Keep the investigation read-only and use the stable attribute names and values in [the telemetry catalog](references/telemetry-catalog.md); do not invent dimensions or treat high-cardinality game IDs as query defaults.

For local code inspection, also use that catalog to map `scheduler.tick`, `scheduler.job`, and `sync.source_operation` evidence to the pipeline hop it represents.

## Report format

Report the causal chain in this order: planning snapshot → planner result → selected/deferred job → ASA response → persistence decision → derived recalculation and/or forecast-warm result. Identify confirmed facts, inferences, and missing evidence separately. Include the time basis and scope so another investigator can reproduce the decision.
