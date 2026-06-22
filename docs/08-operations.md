# Phase 8: refresh operations and deployment

## Goal

Keep the persistent cache fresh, observable, and recoverable in production.

## Refresh operation

The deployed refresh should call the same `internal/sync` service used by
`cmd/sync`. Possible triggers include platform cron, a scheduled job, or a single
background goroutine when only one application instance exists.

Track and expose:

- Last attempted and last successful refresh.
- Duration and rows inserted, updated, and unchanged.
- Which season and filters were requested.
- A concise failure reason in logs.
- Cache age on public pages.

Do not expose a public unauthenticated endpoint that can trigger arbitrary API
requests or overlapping refreshes.

## Deployment concerns

- The SQLite file must live on persistent storage.
- Back up the database even though it is rebuildable; a backup shortens recovery
  and preserves diagnostic history.
- Gracefully shut down the HTTP server and any active refresh.
- Run migrations before serving traffic.
- Pin dependency versions and automate tests.

## Exit criteria

- A documented command can rebuild the cache from scratch.
- Scheduled refresh failures are visible without corrupting the last good data.
- Deployments preserve the SQLite file.
- The site accurately displays cache freshness.
