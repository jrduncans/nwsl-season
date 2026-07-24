# NWSL season explorer

NWSL season explorer is a website built in Go for browsing NWSL seasons,
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

Useful endpoints:

- <http://localhost:8080/healthz> — process health check.
- <http://localhost:8080/cache/status> — latest cache attempt and success.
- `/seasons/:season` — season overview.
- `/seasons/:season/fixtures` — results and remaining fixtures.
- `/seasons/:season/schedule-difficulty` — remaining schedule comparison.
- `/seasons/:season/clinching` — qualification and slate scenarios.
- `/seasons/:season/forecast` — interactive forecast simulation.

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
| `NWSL_SCENARIO_BUDGET` | `30s` | Maximum time for one clinching-scenario calculation batch. |
| `NWSL_HISTORY_RETENTION` | `2160h` (90 days) | Retention period for superseded operational history. |
| `NWSL_FORECAST_CONCURRENCY` | `2` | Maximum concurrent uncached Forecast Lab requests. |
| `NWSL_FORECAST_TIMEOUT` | `15s` | Maximum computation time for one uncached forecast request. |

Forecast requests that exceed the concurrency limit receive `429`. Forecast
computations that exceed their timeout receive `503`. The server also applies
connection limits independently of any reverse proxy.

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

`make build` creates host-platform server and sync binaries in `bin/`.
`make build-linux` creates Linux binaries; ARM64 is the default target and
`TARGET_ARCH=amd64` selects x86_64:

```sh
make build-linux
make build-linux TARGET_ARCH=amd64
```

The individual build targets are `build-server`, `build-sync`,
`build-linux-server`, and `build-linux-sync`.

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
