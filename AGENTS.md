# Agent guide

After the final edit to Go, templates, JavaScript or CSS, run `make verify`.
For `make race`, `make vuln`, telemetry checks and test-data builders
(`internal/apptest`, `internal/asatest`), see [verification](docs/verification.md).

`config.env` may be a 1Password-backed FIFO: never read, display, copy or diff
it. Set `NWSL_CONFIG_FILE=/dev/null` for isolated local runs. Secrets and real
telemetry configuration stay out of source, tests and docs.

## Read before changing

| Area | Guide |
| --- | --- |
| Sync, cache, scheduler, ASA loading, `cmd/sync`, page-request data access | [sync guide](docs/sync-logic-guide.md) |
| Seasons, stages, capabilities, brackets | [competitions guide](docs/competitions-guide.md) |
| Standings, tiebreaks, schedule difficulty | [standings and schedule guide](docs/standings-and-schedule-guide.md) |
| Qualification, clinching, scenarios | [clinching guide](docs/clinching-logic-guide.md); its presentation section is the contract for summaries and alternatives |
| Forecast Lab | [Forecast Lab guide](docs/forecast-lab-guide.md) |
| Forecast models and evaluation | Evaluation protocol under "Command-line tools" in [README.md](README.md) |
| Explore charts and calculations | [Explore guide](docs/explore-guide.md); its Views table maps tab names to `view` keys |
| Telemetry, including checks after `telemetry/registry` changes | [telemetry guide](docs/telemetry/README.md) |

When behavior changes, update the guide that owns it.
