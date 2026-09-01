# P8-07: Startup catalog bootstrap drain

## Control

- Status: Review
- Intended implementation model: Terra
- Required review: Sol
- Depends on: P8-06
- Blocks: none

## Goal

Load a missing public catalog archive promptly after server startup without a
manual sync command or an unbounded ASA request burst.

## Why this packet exists

P8-03 correctly admits every missing catalog scope on startup, but its normal
five-minute scheduler cadence leaves a visibly incomplete archive between
three-request batches. The live cache reproduction on 2026-09-01 showed that
the source work was healthy but historical playoff and Challenge Cup scopes
remained queued for several normal ticks.

## Fixed decisions

- The first scheduler tick remains unchanged and uses the configured source
  request budget.
- While the next normally selected batch contains a due public,
  source-backed catalog bootstrap job, startup waits five seconds and runs
  another batch. Each batch uses the ordinary planner, budget, sequential job
  execution, and scope lease.
- Catalog bootstrap means only a missing/not-published full games inventory or
  the first authoritative full xG load. Routine result checks, correction
  sweeps, qualification, scenarios, and forecast warming do not themselves
  continue the startup drain.
- A completed historical catalog scope remains eligible until both its fixture
  inventory and first full xG observation are available; afterward only the
  existing cold correction policy applies.
- The fast drain stops immediately after a planning read failure, a source job
  failure, a lease deferral, cancellation, or when no normally selected
  catalog bootstrap job remains. Normal interval scheduling then resumes.
- Follow-on fast batches skip the repeated current-scope clinching preflight;
  the initial startup tick and ordinary scheduler ticks retain it.
- The five-second interval is an internal scheduler default, not a new public
  environment variable. Tests may override it through `scheduler.Config`.

## Allowed changes

- `internal/scheduler/` execution and focused tests.
- `docs/sync-logic-guide.md`, `README.md`, this packet, and the ASA-loading
  packet index.

Do not change the schema, ASA client, catalog, command behavior, application
pages, request budget, or source-operation persistence.

## Required behavior

- A startup with more due catalog bootstrap work than one request-budget batch
  performs follow-on batches after the short delay.
- No source operations overlap; every operation retains the existing lease
  sequence and startup trigger.
- A failed or deferred batch does not retry rapidly.
- Non-catalog hot work alone never starts a fast loop.
- The normal scheduler ticker begins after the startup drain ends.

## Tests and verification

- Use sequenced planning snapshots and a fake runner to prove multiple
  startup batches, order, per-batch budget, no overlap, and prompt stop.
- Cover failure, lease deferral, and a non-catalog due job that must not drain.

```sh
go test -count=1 ./internal/scheduler
golangci-lint fmt ./...
make lint
make vet
make test
go test -race ./...
govulncheck ./...
git diff --check
```

## Non-goals

- A forced production cache backfill or deployment.
- New command flags or environment variables.
- Concurrent ASA calls, changed normal scheduler cadence, or page-triggered
  source loading.

## Stop conditions

- Stop if a fast follow-on batch can exceed the ordinary per-batch request
  budget, overlap an operation, weaken leases, or repeatedly retry a failed
  source call.

## Handoff

Report the files changed, measured drain behavior, verification results,
deviations, and any unresolved API-rate concern. Set this packet to `Review`;
only Sol may mark it complete after review.
