# Agent guide

## Handoff checks

After the final edit to Go, templates, JavaScript or CSS, run `make verify`.

Add these when they apply:

- Goroutines, channels, locks, shared mutable state, the scheduler, or
  cache/HTTP concurrency: `make race`.
- `go.mod`/`go.sum` changes or release work: `make vuln`. If its database is
  unreachable, report the scan as skipped, not clean.
- `telemetry/registry` changes: `make telemetry-check-generated` and
  `make telemetry-live-check`.

Build test data with `internal/apptest` (seeded cache scenarios) and
`internal/asatest` (fake ASA server); tests never reach the real ASA API.

## Invariants

- Page requests read only the local SQLite cache. The scheduler and `cmd/sync`
  own ASA refreshes; `cmd/sync` is maintenance work sharing the server's
  `NWSL_DATA_DIR` and sync lease.
- `config.env` may be a 1Password-backed FIFO: never read, display, copy or
  diff it. Set `NWSL_CONFIG_FILE=/dev/null` for isolated local runs. Secrets
  and real telemetry configuration stay out of source, tests and docs.

## Read before changing

| Area | Guide |
| --- | --- |
| Sync, cache, scheduler, ASA loading | [sync guide](docs/sync-logic-guide.md) |
| Seasons, stages, capabilities, brackets | [competitions guide](docs/competitions-guide.md) |
| Standings, tiebreaks, schedule difficulty | [standings and schedule guide](docs/standings-and-schedule-guide.md) |
| Qualification, clinching, scenarios | [clinching guide](docs/clinching-logic-guide.md); its presentation section is the contract for summaries and alternatives |
| Forecast Lab | [Forecast Lab guide](docs/forecast-lab-guide.md) |
| Forecast models and evaluation | Evaluation protocol under "Command-line tools" in [README.md](README.md) |
| Explore charts and calculations | [Explore guide](docs/explore-guide.md); its Views table maps tab names to `view` keys |
| Telemetry | [telemetry guide](docs/telemetry/README.md) |

When behavior changes, update the guide that owns it.
