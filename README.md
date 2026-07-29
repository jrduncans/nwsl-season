# NWSL Season Explorer

NWSL Season Explorer is a website built in Go for browsing NWSL seasons,
standings, fixtures, schedule difficulty, playoff qualification, and
probabilistic season forecasts.

The application keeps a local SQLite cache of data from [American Soccer
Analysis (ASA)](https://www.americansocceranalysis.com/), so ordinary page
requests read locally and do not contact ASA. A background scheduler refreshes
the configured season and recalculates derived xG, qualification, and
clinching-scenario data as the cache becomes stale. Standings are calculated
from the cached fixtures when a season page or CLI report is requested.

## Features

- Season overview with results, upcoming fixtures, standings, goals, and xG.
- Per-game and total standings views for seasons with uneven schedules.
- Remaining schedule difficulty with raw and home/away-adjusted comparisons.
- Qualification proofs for the Shield, top-four seed, and playoff places.
- Actionable clinching and elimination scenarios for the next slate.
- Forecast Lab with Current Pace, Results Poisson, and xG Poisson models.
- Shareable forecast URLs with fixed match outcomes and optional model comparison.
- Health and cache-status endpoints for operators.

The current rules configuration covers the 2026 regular season: 16 teams, 240
fixtures, and eight playoff places. Qualification indicators are suppressed
when the cached fixture inventory is incomplete.

## Quick start

Go 1.26 or newer is required.

```sh
go run ./cmd/server
```

Visit <http://localhost:8080>. With the default configuration, the current
season is available at <http://localhost:8080/seasons/2026> and Forecast Lab is
available at <http://localhost:8080/seasons/2026/forecast>.
The historical forecast comparison is available at
<http://localhost:8080/seasons/2026/model-evaluation>.

Useful endpoints:

- <http://localhost:8080/healthz> — process health check.
- <http://localhost:8080/cache/status> — latest cache attempt and success.
- `/seasons/:season` — season overview.
- `/seasons/:season/fixtures` — results and remaining fixtures.
- `/seasons/:season/schedule-difficulty` — remaining schedule comparison.
- `/seasons/:season/clinching` — qualification and slate scenarios.
- `/seasons/:season/forecast` — interactive forecast simulation.
- `/seasons/:season/model-evaluation` — interactive historical forecast evaluation.

## Configuration

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `NWSL_HTTP_ADDR` | `127.0.0.1:8080` | Full address on which the server listens. |
| `HOST` | `127.0.0.1` | Host used when `NWSL_HTTP_ADDR` is unset. |
| `PORT` | `8080` | Port used when `NWSL_HTTP_ADDR` is unset. |
| `NWSL_DATA_DIR` | `data` | Directory containing the SQLite cache. |
| `NWSL_SYNC_SEASON` | `2026` | Season refreshed automatically by the server. |
| `NWSL_SYNC_STAGE` | `Regular Season` | Competition stage refreshed automatically. |
| `NWSL_SYNC_CHECK_INTERVAL` | `5m` | How often the scheduler checks cache freshness. |
| `NWSL_SYNC_COMPLETION_GRACE` | `3h` | Time after kickoff before an unfinished fixture is stale. |
| `NWSL_SYNC_MIN_ATTEMPT_INTERVAL` | `30m` | Minimum time between ASA attempts, including failures. |
| `NWSL_SYNC_TIMEOUT` | `20s` | Maximum duration of one ASA refresh and cache transaction. |
| `NWSL_QUALIFICATION_BUDGET` | `5s` | Maximum time for one qualification calculation batch. |
| `NWSL_SCENARIO_BUDGET` | `2m` | Maximum time for one clinching-scenario discovery batch; it runs independently after fixture sync and qualification. |
| `NWSL_HISTORY_RETENTION` | `2160h` (90 days) | Retention period for superseded operational history. |
| `NWSL_FORECAST_CONCURRENCY` | `2` | Maximum concurrent uncached Forecast Lab requests. |
| `NWSL_FORECAST_TIMEOUT` | `15s` | Maximum computation time for one uncached forecast request. |
| `HONEYCOMB_API_KEY` | unset | Enables OpenTelemetry trace export directly to Honeycomb. Keep this secret out of the repository. |
| `HONEYCOMB_API_ENDPOINT` | `https://api.honeycomb.io` | Honeycomb ingest endpoint; use `https://api.eu1.honeycomb.io` for the EU instance. |
| `OTEL_SERVICE_NAME` | `nwsl-season-server` | Service name shown in Honeycomb. |
| `HONEYCOMB_METRICS_DATASET` | unset | Optional Honeycomb dataset for OpenTelemetry HTTP metrics. |

Forecast requests that exceed the concurrency limit receive `429`. Forecast
computations that exceed their timeout receive `503`. The server also applies
connection limits independently of any reverse proxy. During startup, it also
calculates and keeps the zero-assumption result for every Forecast Lab model in
the process-local result cache, so the initial request for each model can be
served without running a simulation. A missing or unusable fixture cache only
skips this warm-up; it does not prevent the server from starting. Later
successful fixture or xG cache changes refresh those baseline results; no-op
and rate-limited checks leave the warmed cache intact.

## Observability

The server and `sync` command create OpenTelemetry traces for HTTP routes, ASA
API calls, scheduled cache checks and refreshes, cache reads and writes,
qualification and scenario calculations, and Forecast Lab simulations. Every
ASA request is a downstream HTTP child span regardless of whether it was
started by the server or the maintenance command.

The trace data is deliberately wide rather than pre-aggregated. Page requests
record the season, stage, fixture snapshot, cache age, fixture inventory, and
xG availability. Sync traces include their trigger, data-change counts, and
independent xG/qualification/scenario outcomes. Forecast spans record the
selected model plus the iteration, team, fixture, xG-observation, playoff-place,
and fixed-assumption counts; calculation spans add the scoped fixture and
achievement counts. They do not include individual assumed results.

Local development remains silent and does not make telemetry network calls
until an exporter is configured. Metrics are optional; traces alone are enough
to investigate a reported problem in Honeycomb at this project's scale.

To send traces to Honeycomb, create an **ingest API key** in your Honeycomb
environment, then set it only in the runtime environment:

```sh
export HONEYCOMB_API_KEY='your-ingest-key'
export OTEL_SERVICE_NAME='nwsl-season'
# Set this on deploy to compare behavior between versions.
export OTEL_RESOURCE_ATTRIBUTES='service.version=git-sha-or-release'
go run ./cmd/server
```

For Honeycomb's EU instance, also set
`HONEYCOMB_API_ENDPOINT=https://api.eu1.honeycomb.io`. Add
`HONEYCOMB_METRICS_DATASET=nwsl-season-metrics` to export the HTTP metrics that
the instrumentation produces. The process flushes pending telemetry for up to
10 seconds during graceful shutdown.

### 1Password Environments

All commands load an optional `config.env` from their current working directory
before reading configuration. This makes it suitable for a 1Password
Environment-managed file. The file is read line by line, which is required for
1Password's local `.env` file pipe. The checked-in
[`config.env.example`](config.env.example) has the supported shape; the real
`config.env` is ignored by Git.

```dotenv
HONEYCOMB_API_KEY=your-ingest-key
OTEL_SERVICE_NAME=nwsl-season
```

Use `NWSL_CONFIG_FILE=/path/to/config.env` when the 1Password-managed file is
elsewhere. Environment variables already supplied by the process take
precedence over file values. 1Password also supports provisioning an
Environment's variables directly to a subprocess with `op run`, or resolving a
templated configuration file with `op inject`; this application is compatible
with either approach. See [1Password's secrets-in-scripts guide](https://developer.1password.com/docs/cli/secrets-scripts/).

## Command-line tools

Refresh the local ASA cache:

```sh
go run ./cmd/sync -season 2026
```

Use `-force` to bypass the minimum-attempt interval and rebuild qualification
and scenario results. Use `-require-xg` when an xG refresh failure should make
the command exit nonzero.

Recalculate qualification and clinching scenarios from the last successful
fixture snapshot without contacting ASA:

```sh
NWSL_QUALIFICATION_BUDGET=10m \
NWSL_SCENARIO_BUDGET=10m \
go run ./cmd/sync -season 2026 -recalculate
```

Print standings from the local cache:

```sh
go run ./cmd/standings -season 2026
go run ./cmd/standings -season 2026 -order total
```

Load and evaluate all required historical seasons with one explicit command:

```sh
make model-evaluation
```

It fetches the 2016–2019 and 2021–2025 regular seasons from ASA, including xG,
then evaluates every Forecast Lab model with the leakage-safe historical
walk-forward runner. To only load or only evaluate, use:

```sh
make backfill-evaluation-data
make backtest
```

The default protocol alternates the eligible completed seasons so both windows
span the league's history: 2017, 2019, 2022, and 2024 are development seasons;
2016, 2018, 2021, 2023, and 2025 form the five-season final test. Development
results may guide new model versions and fixed constants; final-test results
alone decide the recommended model. Pooled results are descriptive only. A
model changed after inspecting final-test results is a new version and needs
later untouched seasons for a new final test.

The runner refuses to replace the checked-in evidence unless every requested
season passes its data audit and each final-test season has at least 95% xG
coverage. This prevents a partial cache from producing a misleading report. For
inspection only, `-allow-incomplete` writes an incomplete diagnostic report
without claiming a complete evaluation.

The evaluator can also be invoked directly after the 2016–2025 regular seasons
have been synced into the same cache:

```sh
go run ./cmd/backtest
```

The runner audits each season, uses daily UTC cutoffs (so same-day results cannot
train one another), simulates each remaining season, calculates proper scoring
rules and calibration, and applies the precommitted paired-bootstrap selection
rule. It writes machine-readable evidence to `docs/model-evaluation-v1.json`
and a readable summary to `docs/model-evaluation-v1.md`. The default evaluation
uses 20,000 iterations and 10,000 resamples; the generated artifact records the
exact values used for a particular run. Use `-generated-at` for byte-stable reruns and
`-json`/`-markdown` to write elsewhere while testing.

The sync command also supports `-db` for an explicit SQLite path and
`-prune-history-before` for one-off cleanup of superseded run history. The
server and sync command must use the same persistent `NWSL_DATA_DIR` when they
share a cache.

## Build and verify

```sh
make test
make fmt
make vet
make build
```

`make build` creates host-platform server, sync, and back-test binaries in `bin/`.
`make build-linux` creates Linux binaries; ARM64 is the default target and
`TARGET_ARCH=amd64` selects x86_64:

```sh
make build-linux
make build-linux TARGET_ARCH=amd64
```

The individual build targets are `build-server`, `build-sync`, `build-backtest`,
`build-linux-server`, `build-linux-sync`, and `build-linux-backtest`.

## Runtime and deployment

This repository builds the application binaries. A separate deployment
environment is responsible for machine provisioning, process management,
proxy/TLS configuration, configuration delivery, and backups.

The runtime requires:

- Exactly one server instance.
- A writable, persistent `NWSL_DATA_DIR`.
- Network access to ASA for scheduled or operator-triggered refreshes.
- Graceful delivery of `SIGINT` or `SIGTERM`.
- Permission to bind `NWSL_HTTP_ADDR`.

The sync binary is a maintenance command, not a second long-running service.
Run it as the same operating-system user as the server and point it at the
same SQLite data directory so it can share the sync lease safely. A reverse
proxy should monitor `/healthz` and `/cache/status`.

The cache database is stored at
`NWSL_DATA_DIR/nwsl-season.sqlite`. The application retains refresh history for
operations and recovery, and automatically prunes superseded history after a
successful sync according to `NWSL_HISTORY_RETENTION`. Back up the SQLite data
directory even though the current season data can be rebuilt from ASA.

## Documentation

- [How clinching works](docs/clinching-logic-guide.md) explains qualification
  proofs, conservative tiebreak handling, no-help paths, and slate scenarios.
- [How Forecast Lab works](docs/forecast-lab-guide.md) explains model presets,
  simulation behavior, fixed outcomes, and shareable scenarios.
