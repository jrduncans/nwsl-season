# Phase 8: in-server cache refresh and deployment

## Goal

Keep the persistent cache fresh without an external scheduler, while retaining a
safe, low-frequency way to recover from corrections to older ASA data.

## Operating model

The Go server owns automatic refreshes. Deployment assumes exactly one server
process, so it needs neither platform cron nor a distributed lock. `cmd/sync`
remains a maintenance tool, but is not part of normal production operation.

At startup, after opening and migrating SQLite, the server constructs the same
`syncer.Service` used by `cmd/sync` and starts one background scheduler for the
current regular season. Page requests continue to read SQLite only: they never
wait for ASA or initiate a refresh.

The scheduler makes an inexpensive local decision immediately at startup and
then on a five-minute ticker. The check reads the cached season; it makes no ASA
request unless a refresh is eligible. It must stop accepting new work during
server shutdown and let an active refresh finish (up to its context deadline)
before closing SQLite.

## Eligibility and request cadence

A scheduled ASA refresh is eligible when one of the following is true:

- There is no successful snapshot for the configured season and stage.
- A cached fixture has a valid kickoff at least two hours in the past and is
  not a `FullTime` result with both scores. The two-hour completion grace leaves
  room for ASA publication delay while reducing the wait for completed results.
- The configured fixed schedule is incomplete or uneven. A 2026 regular-season
  snapshot must contain all 240 fixtures and give each of the 16 teams 30
  appearances.
- The scheduler cannot determine whether the cache covers the current match
  window, for example because a known fixture has an invalid kickoff or an
  unsupported status.

Otherwise the cache is considered current enough: if every fixture that could
plausibly have finished is already final, the server makes no ASA request. In
particular, it does not repeatedly poll an old, fully completed season merely to
look for rare historical corrections.

The first version should use these defaults, all expressed as server
configuration so production timing can be adjusted without a code change:

| Setting | Default | Meaning |
| --- | --- | --- |
| `NWSL_SYNC_SEASON` | `2026` | Current season that the server may refresh automatically. |
| `NWSL_SYNC_STAGE` | `Regular Season` | Competition stage that the server may refresh automatically. |
| `NWSL_SYNC_CHECK_INTERVAL` | `5m` | How often the server inspects cached fixtures locally. |
| `NWSL_SYNC_COMPLETION_GRACE` | `2h` | Time after kickoff before a non-final fixture can make a refresh eligible. |
| `NWSL_SYNC_TIMEOUT` | `20s` | Bound on a single ASA refresh, including its database transaction. |

An eligible cache may start one refresh on each five-minute scheduler check.
Each ASA request uses a small bounded retry sequence for transient transport,
`408`, `425`, `429`, and `5xx` failures. Once an eligible fixture is returned as
`FullTime` with both scores, it no longer causes refreshes. An in-process mutex
and a short-lived SQLite lease keep manual and scheduled syncs from overlapping,
including when the maintenance command runs in a separate process.

This policy relies on ASA returning scheduled fixtures before they are played.
For an eligible overdue cached fixture, the sync also queries ASA's
game-specific endpoint and uses it only when it is terminal or newer than the
season collection's copy. This narrowly scoped reconciliation lets a
game-specific result repair a lagging collection response without turning page
loads into polling. A complete-season response that violates a configured
fixture inventory is rejected before any cache rows are changed.

## Forced refresh for corrections

Provide an explicit `-force` mode on `cmd/sync`:

```sh
go run ./cmd/sync -season 2026 -force
```

The command performs the normal complete-season fetch and atomic replacement
without consulting scheduler eligibility. `-force` additionally rebuilds
qualification and scenario batches even if complete batches already exist for
the resulting snapshot. It is intended for rare ASA corrections, algorithm
changes, initial recovery, or diagnosis—not for routine operation. It must not
delete the existing cache before a validated replacement is ready; a failed
forced refresh leaves the last good snapshot intact.

Derived clinching data can also be recalculated without an ASA request or any
mutation to synchronized fixture, team, xG, or sync-audit data:

```sh
NWSL_QUALIFICATION_BUDGET=10m \
NWSL_SCENARIO_BUDGET=10m \
go run ./cmd/sync -season 2026 -recalculate
```

The command retries status, no-help, and scenario results that exhausted their
calculation budget. Add `-force` to ignore all completed derived-data batches.
`NWSL_SYNC_TIMEOUT` applies only to upstream synchronization; the qualification
and scenario passes use their own independently configured budgets.

Do not add a public HTTP endpoint for force refresh. Operators run the command
from the deployment environment, where access to the persistent database is
already controlled.

Track and expose:

- Last attempted and last successful refresh.
- Duration and rows inserted, updated, and unchanged.
- Which season and filters were requested.
- A concise failure reason in logs and `/cache/status`.
- Cache age on public pages.
- Whether the scheduler decided the cache was eligible or skipped it as current.
  These decisions belong in structured logs; successful and failed network
  attempts continue to be recorded in `sync_runs`.

## Deployment concerns

- The SQLite file must live on persistent storage.
- Back up the database even though it is rebuildable; a backup shortens recovery
  and preserves diagnostic history.
- Gracefully shut down the HTTP server and its active refresh.
- Run migrations before serving traffic.
- Pin dependency versions and automate tests.

## Runtime integration contract

This repository does not provision a machine, process manager, proxy, TLS,
configuration delivery, or backups. Those concerns belong to the separate
deployment project. It must run exactly one server instance and provide:

- A writable, persistent `NWSL_DATA_DIR`; this contains
  `nwsl-season.sqlite`, its WAL files, and refresh audit history.
- Network access to ASA and a graceful `SIGINT` or `SIGTERM` shutdown path. The
  server stops new scheduler checks, lets its active bounded refresh finish,
  then closes SQLite.
- Backups of the SQLite data directory and probes for `/healthz` and
  `/cache/status`.
- The server configuration documented in the README, including the automatic
  sync season/stage and timing controls.

Build artifacts are intentionally simple:

```sh
make build                            # both binaries for this host
make build-linux                      # both Linux binaries, ARM64 by default
make build-linux TARGET_ARCH=amd64    # both Linux binaries for x86_64
```

The individual `build-server`, `build-linux-server`, `build-sync`, and
`build-linux-sync` targets remain available when only one artifact is needed.

The server binary embeds its HTML, CSS, and JavaScript assets. No repository
deployment assets are required alongside either binary. Deploy the server and
maintenance binaries together. Run the maintenance binary manually as the same
user as the server, with the same persistent `NWSL_DATA_DIR`, for example:

```sh
NWSL_DATA_DIR=/var/lib/nwsl-season \
  /opt/nwsl-season/nwsl-season-sync -season 2026 -force
```

The host can put a proxy in front of `NWSL_HTTP_ADDR`; proxy and TLS
configuration are not part of this project.

## Exit criteria

- The single server process automatically refreshes a missing cache and a
  current-season fixture that is plausibly complete but not final.
- A complete known match window causes no ASA request, while an eligible cache
  can retry on the next five-minute scheduler check.
- A documented maintenance refresh bypasses normal eligibility without exposing
  an HTTP trigger or risking the last good cache.
- Scheduled refresh failures and scheduler decisions are visible without
  corrupting the last good data.
- Deployments preserve the SQLite file.
- The site accurately displays cache freshness.
