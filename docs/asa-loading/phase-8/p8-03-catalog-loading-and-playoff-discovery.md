# P8-03: Catalog loading and playoff discovery

## Control

- Status: Complete
- Intended implementation model: Terra
- Required review: Sol
- Depends on: P8-02
- Blocks: P8-04 through P8-06

## Goal

Load every public source-backed catalog stage predictably and discover later
knockout rounds promptly after a targeted result changes an incomplete active
bracket.

## Fixed decisions

- Adopt the existing uncommitted startup historical-bootstrap changes as this
  packet's starting work; they are not Phase 7 behavior.
- The planner admits every public, source-backed catalog scope until first
  games publication, then retains existing hot/cold cadence behavior.
- Missing-inventory priority is exactly: configured current primary stage,
  current-season secondary stages in catalog order, historical primary stages
  newest first, historical secondary stages newest first in catalog order.
  One job still executes at a time and all existing request budgets remain.
- Add `cmd/sync -backfill-catalog`. It forces teams once and then every public
  source-backed catalog stage sequentially in the same priority order, using
  source-only behavior outside the configured regular-season scope. Retain
  `-backfill-historical` with its existing regular-season-only semantics and
  reject incompatible flag combinations.
- A successful material targeted games refresh in an active bracket scope with
  fewer cached games than its verified bracket slot count atomically sets that
  scope's full-games due time to the operation finish time. The next scheduler
  tick performs discovery; the targeted operation does not make an additional
  ASA request.
- Do not accelerate complete brackets, non-bracket stages, nonmaterial checks,
  xG-only changes, failed operations, completed historical scopes, or a scope
  whose format has no finite expected slot count.
- Preserve the scope and global leases, hot-before-cold behavior, configured
  request budget, sequential execution, and stage-isolated derived work.

## Allowed changes

- `internal/scheduler/` planner/execution and tests
- `internal/cache/` only the atomic due-state helper needed by targeted games
  persistence and its focused tests
- `internal/syncer/` only to carry the material/incomplete bracket outcome into
  the existing atomic persistence boundary
- `cmd/sync/` flags, sequential catalog backfill, and tests
- `cmd/server/` startup wiring/tests only if required by the adopted bootstrap
- Active sync documentation, README, this packet, and packet index

Do not change schema, ASA wire facts, catalog topology, application UI, or
bracket construction.

## Tests and verification

- Planner tests cover all four priority tiers, budgets, one-job execution,
  startup admission, existing cold sweeps, and no starvation of hot work.
- Command tests cover catalog order, one teams load, every catalog stage,
  retained historical behavior, failures, and incompatible flags.
- Persistence/scheduler tests cover every acceleration positive and negative
  case and prove atomic rollback. Run the full race suite.

```sh
go test -count=1 ./internal/cache ./internal/syncer ./internal/scheduler ./cmd/sync ./cmd/server
golangci-lint fmt ./...
make lint
make vet
make test
go test -race ./...
govulncheck ./...
git diff --check
```

## Non-goals

- Bracket domain or UI.
- Production cache mutation, deployment, or concurrent backfill.
- New qualification, forecast, standings, or scenario work for non-league
  stages.

## Stop conditions

- Stop if the required order cannot be derived deterministically from catalog
  metadata, if acceleration cannot be atomic with the targeted result, or if a
  change weakens leases, budgets, sequential execution, or stage isolation.

## Handoff

Report changed files, adopted pre-existing changes, priority/acceleration
behavior, every verification result, deviations, and open questions. Set the
packet to `Review`; the primary advances it only after Sol accepts the diff.
