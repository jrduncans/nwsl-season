# Agent guide

## Handoff checks

After the final edit to Go, templates, JavaScript or CSS, run `make verify`
(fmt, lint, vet, test, test-e2e; run `make e2e-install` once first). Iterate
with the narrowest package test or a focused target such as
`make test-clinching` or `make test-explore`.

Add these when they apply:

- Goroutines, channels, locks, shared mutable state, the scheduler, or
  cache/HTTP concurrency: `make race`.
- `go.mod`/`go.sum` changes or release work: `make vuln`. If its database is
  unreachable, report the scan as skipped, not clean.
- `telemetry/registry` changes: `make telemetry-check-generated` and
  `make telemetry-live-check`.

`make lint` includes `gosec`; keep each suppression narrow and justified.

Build test data with `internal/apptest` (seeded cache scenarios) and
`internal/asatest` (fake ASA server). Tests run against these fakes, never the
real ASA API.

## Invariants

- Page requests read only the local SQLite cache. The scheduler and `cmd/sync`
  own ASA refreshes; `cmd/sync` is maintenance work sharing the server's
  `NWSL_DATA_DIR` and sync lease.
- When an incoming known fixture inventory is incomplete or uneven, keep the
  prior complete inventory, and publish qualification indicators only from a
  complete one.
- `config.env` may be a 1Password-backed FIFO: never read, display, copy or
  diff it. Set `NWSL_CONFIG_FILE=/dev/null` for isolated local runs. Document
  configuration shape in `config.env.example`; secrets and real telemetry
  configuration stay out of source, tests and docs.

## Read before changing

| Area | Guide |
| --- | --- |
| Sync, cache, scheduler, ASA loading | [sync guide](docs/sync-logic-guide.md) |
| Seasons, stages, capabilities, brackets | [competitions guide](docs/competitions-guide.md) |
| Standings, tiebreaks, schedule difficulty | [standings and schedule guide](docs/standings-and-schedule-guide.md) |
| Qualification, clinching, scenarios | [clinching guide](docs/clinching-logic-guide.md); its presentation section is the contract for summaries and alternatives |
| Forecast Lab | [Forecast Lab guide](docs/forecast-lab-guide.md) |
| Forecast models and evaluation | The evaluation protocol under "Command-line tools" in [README.md](README.md). Final-test seasons alone decide the recommended model, and checked-in evidence is replaced only by a complete run; `-allow-incomplete` output is diagnostic. |
| Explore charts and calculations | [History guide](docs/history-logic-guide.md); its Explore workspace table maps tab names to `view` keys |
| Telemetry | [telemetry guide](docs/telemetry/README.md) |

When behavior changes, update the guide that owns it. Change this file, the
`Makefile` and CI only when a command or required check changes.
