# P8-06: Integration rehearsal and final gate

## Control

- Status: Complete
- Intended implementation model: Terra
- Required review: Sol
- Depends on: P8-05b
- Blocks: none

## Goal

Prove the catalog, source facts, scheduler, factual pages, and brackets behave
correctly from empty publication through correction using fake and temporary
live-backed caches.

## Required rehearsal

- Fake ASA covers empty publication, partial rounds, terminal results,
  inventory acceleration, later-round discovery, direct penalties, later xG,
  and corrected score/shootout facts.
- Backfill a temporary SQLite cache from live ASA only; never mutate the
  configured/production cache. Verify representative 2016, 2020, 2021, 2024,
  2025, and empty/partial 2026 pages against official NWSL results.
- Audit migration safety, cross-stage reads/writes, scheduler priority and
  leases, bracket advancement, mismatch fallback, relative URLs, and 390px UI.
- Update README, active sync/clinching/Forecast guides as behavior requires,
  packet index, and every Phase 8 packet status only after its review passes.

## Final verification

```sh
golangci-lint fmt ./...
make lint
make vet
make test
go test -race ./...
govulncheck ./...
make telemetry-check-generated
make telemetry-live-check
git diff --check
```

## Stop conditions

- Stop on live/source mismatch, production-cache targeting, migration loss,
  cross-stage contamination, scheduler concurrency regression, or any bracket
  winner/advancement that requires guessing.

## Rehearsal evidence

- `TestPlayoffPublicationRehearsalPersistsBracketTransitions` uses a temporary
  SQLite cache and fake ASA responses for the 2025 cataloged playoff scope. It
  covers empty publication, partial opening inventory, a material targeted
  terminal result that accelerates the next full discovery, later-round
  publication, independent final-game xG, and a newer corrected tied final
  with an unequal shootout tally that reverses the source-backed winner.
- A forced `-backfill-catalog` rehearsal used an isolated temporary data
  directory and live ASA. It migrated a new cache through schema 14 and loaded
  every public catalog scope without touching the configured cache. Inventory
  checks covered the 2016 playoffs; 2020 Challenge Cup groups and knockout;
  2021, 2024, and 2025 playoffs; the 2024-2026 Challenge Cup finals; and the
  unpublished 2026 playoff scope.
- Browser acceptance covered the season archive, chronological Challenge Cup
  group results, historical completed brackets, empty and partial `TBD`
  structures, extra-time and direct-to-penalties results, bracket fixture
  fallbacks, desktop round connections, and the stacked semantic layout at a
  390px viewport. Representative results and formats were compared with
  official NWSL playoff and Challenge Cup histories.
- Sol's final integrity review accepted the schema-14 sentinel handling after
  the migration proved that paired `0-0` scores with penalties absent or false
  normalize only cached tallies, preserve `raw_json`, and retain transactional
  rollback and retry for incompatible legacy values.
