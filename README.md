# NWSL season explorer

A learning project written in Go for exploring NWSL seasons. The eventual site will
show results and standings, determine when teams have mathematically clinched a
playoff place, forecast the rest of a season, and compare remaining strength of
schedule.

This repository intentionally grows in small phases. Start with
[`docs/00-roadmap.md`](docs/00-roadmap.md), then use the guide for the current
phase. Each phase ends with a working checkpoint and a few questions worth
exploring in the code.

## Current checkpoint

Phase 11 is implemented: the cache retains ASA team-model game xG separately
from fixtures, including raw payloads, availability markers, and independent
refresh freshness. Forecast Lab at `/seasons/2026/forecast` provides Current
pace, Results Poisson, and xG Poisson presets; it defaults to the catalog’s
evidence-backed recommendation, preserves fixed outcomes while models change,
and can compare two models side by side. `/seasons/2026?view=outlook` shows the
recommended forecast without changing official standings, while
`/seasons/2026/xg` is descriptive xG analysis—not a table or power rating.
Normal page requests never contact ASA.

```sh
go run ./cmd/server
```

Then visit <http://localhost:8080>. The health endpoint is
<http://localhost:8080/healthz>. Cache freshness is available at
<http://localhost:8080/cache/status>.

The current season redirects to <http://localhost:8080/seasons/2026>. Forecast
Lab is at <http://localhost:8080/seasons/2026/forecast> and works with or
without JavaScript. Its selected model, optional comparison, and outcomes are
encoded in a versioned shareable URL. Fixed outcomes sample plausible conditional scorelines, so
goal-based tiebreakers retain uncertainty. Shared forecast URLs use the latest
cached fixture snapshot; an assumption becomes stale when its fixture completes.

Configuration:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `NWSL_HTTP_ADDR` | `127.0.0.1:8080` | Full address on which the server listens |
| `HOST` | `127.0.0.1` | Host used to build the listen address when `NWSL_HTTP_ADDR` is unset |
| `PORT` | `8080` | Port used to build the listen address when `NWSL_HTTP_ADDR` is unset |
| `NWSL_DATA_DIR` | `data` | Directory containing the SQLite cache |
| `NWSL_SYNC_SEASON` | `2026` | Current season the server may refresh automatically |
| `NWSL_SYNC_STAGE` | `Regular Season` | Stage the server may refresh automatically |
| `NWSL_SYNC_CHECK_INTERVAL` | `5m` | How often the server checks cached fixtures locally |
| `NWSL_SYNC_COMPLETION_GRACE` | `3h` | Time after kickoff before an unfinished fixture is stale |
| `NWSL_SYNC_MIN_ATTEMPT_INTERVAL` | `30m` | Minimum time between ASA attempts, including failures |
| `NWSL_SYNC_TIMEOUT` | `20s` | Maximum duration of one ASA refresh and cache transaction |

## Useful commands

```sh
go test ./...
go fmt ./...
go vet ./...
```

To refresh the local ASA cache without bypassing the normal minimum-attempt
interval:

```sh
go run ./cmd/sync -season 2026
```

For an operator-led retry after an ASA correction or diagnosis, use `-force`.
It bypasses only the interval; it still validates the full ASA response and
preserves the last good snapshot if the replacement fails.

```sh
go run ./cmd/sync -season 2026 -force
```

To print standings from the local cache:

```sh
go run ./cmd/standings -season 2026
```

To regenerate the deterministic evaluation-artifact envelope (after auditing
the historical cache), run:

```sh
go run ./cmd/backtest -generated-at 2026-07-15T00:00:00Z
```

Standings default to per-game order while teams have played uneven schedules.
Use `-order total` to print the full-season total-points order.

Or use the Makefile wrappers:

```sh
make test
make fmt
make vet
```

To build the server for a Linux virtual machine:

```sh
make build-linux
```

That writes both `bin/nwsl-season-server-linux-arm64` and
`bin/nwsl-season-sync-linux-arm64`, suitable for the target ARM64 Linux VM. For
an x86_64 VM, run:

```sh
make build-linux TARGET_ARCH=amd64
```

Install the maintenance binary alongside the server and run it as the same
user, with the same persistent `NWSL_DATA_DIR`:

```sh
NWSL_DATA_DIR=/var/lib/nwsl-season \
  /opt/nwsl-season/nwsl-season-sync -season 2026 -force
```

The maintenance binary is intended for operator-led recovery or corrections;
it is not a second long-running service. The server's built-in scheduler
handles normal refreshes.

The server reads cache status from `NWSL_DATA_DIR/nwsl-season.sqlite`; normal
page requests never refresh ASA data. `/cache/status` reports the latest
configured-season attempt and success, including outcome, duration, and row
change counts. Scheduler decisions such as `current`, `eligible`, and
`rate_limited` are structured server logs.

The website expects the 2026 format of 16 teams, 30 regular-season games per
team, and eight playoff places. It suppresses clinching indicators when the
cache does not contain the complete 240-game regular-season schedule, and it
reports incomplete fixture data on both season pages.

## Runtime contract

This repository builds the application binary; the separate deployment project
owns machine provisioning, process management, proxy/TLS, configuration delivery,
and backups.

`make build-server` writes `bin/nwsl-season-server` for the host platform.
`make build-linux-server` writes
`bin/nwsl-season-server-linux-<arch>` (ARM64 by default; set
`TARGET_ARCH=amd64` for x86_64).
`make build-sync` writes `bin/nwsl-season-sync` for the host platform, and
`make build-linux-sync` writes
`bin/nwsl-season-sync-linux-<arch>` using the same architecture settings.
`make build` builds both host-platform binaries, and `make build-linux` builds
both Linux binaries.

The runtime must provide exactly one server instance, a writable and persistent
`NWSL_DATA_DIR`, network access to ASA, and graceful `SIGINT`/`SIGTERM`
delivery. Back up the SQLite data directory even though ASA can rebuild the
cache; it retains refresh history and shortens recovery. The process must be
allowed to bind `NWSL_HTTP_ADDR`; the deployment project may proxy it and should
probe `/healthz` and `/cache/status`.

The maintenance binary must be run with the same `NWSL_DATA_DIR` and operating
system user as the server so it updates the live SQLite cache and can share the
server's sync lease safely.

## Dev container

With Docker running and a dev-container-compatible editor installed, open this
repository in its container. The container includes Go 1.26, the Go editor
extension, persistent module and build caches, and forwards port 8080.

Once the container is ready:

```sh
go test ./...
go run ./cmd/server
```

Then visit <http://localhost:8080>. When Docker is provided by OrbStack, the
server is also available at <https://nwsl-season.orb.local>. Runtime data is
written to the repository's ignored `data/` directory.
