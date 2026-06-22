# Phase 3: SQLite as a persistent cache

## Goal

Make SQLite the application's durable local view of ASA data. The website reads
SQLite; a synchronization process updates it. ASA availability should affect data
freshness, not whether an ordinary page can load.

## Cache semantics

SQLite is a cache, but it is persistent and operationally important. It should be
safe to delete and rebuild from ASA, while normally surviving restarts and
deployments.

The first schema will likely need:

- `teams`: stable ASA team ID and display metadata.
- `games`: ASA game ID, season, stage, kickoff, status, teams, and scores.
- `sync_runs`: start/end time, requested season, outcome, error summary, and row
  counts.
- `schema_migrations`: applied database migrations.

Also consider storing the raw JSON for each game or each successful response.
That costs little at NWSL scale and makes API surprises much easier to diagnose.

## Refresh algorithm

For a requested season:

1. Fetch the complete season from ASA into memory or a temporary staging table.
2. Validate it before changing current data: IDs are present, teams exist, scores
   make sense for completed games, and the response is not suspiciously empty.
3. Begin a database transaction.
4. Upsert teams and games by their ASA IDs.
5. Reconcile mutable fields such as kickoff, status, and score. Scheduled matches
   can be postponed and completed results can occasionally be corrected.
6. Mark or remove cached games no longer present only when a complete-season fetch
   succeeded. Never infer deletion from a failed or partial response.
7. Record a successful `sync_runs` row and commit.

On any error, roll back and keep the previous usable cache. Record the failed run
separately if possible.

## Update triggers

Support the same synchronization service from two thin entry points:

- `go run ./cmd/sync -season 2026` for development, cron, and recovery.
- An optional background scheduler in the deployed process later.

Keeping the refresh logic in `internal/sync` prevents the command and scheduler
from drifting apart. Avoid refreshing during normal page requests.

Useful refresh policy later:

- More often around match windows.
- Less often on quiet days and during the offseason.
- A manual refresh command at all times.
- A visible “data updated at” timestamp on the site.

## Concurrency and resilience

- Use a single-writer lock so two refreshes cannot overlap.
- Enable WAL mode if web reads and refresh writes need to coexist.
- Set a busy timeout rather than failing immediately on brief lock contention.
- Put a context deadline around the whole refresh.
- Do not update the freshness timestamp unless the transaction commits.

## Hands-on ideas

- Run the same sync twice and verify row counts do not grow.
- Change a fixture's test score, sync again, and verify it updates in place.
- Simulate ASA returning zero games and prove existing rows survive.
- Kill a test refresh midway and confirm the transaction rolls back.

## Exit criteria

- A new database can be created entirely by migrations and a sync.
- Repeated syncs are idempotent.
- Failed or invalid API responses leave the last good cache intact.
- The application can report when its data was last successfully refreshed.
