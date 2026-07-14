# NWSL season explorer

A learning project written in Go for exploring NWSL seasons. The eventual site will
show results and standings, determine when teams have mathematically clinched a
playoff place, support what-if scenarios, and compare remaining strength of
schedule.

This repository intentionally grows in small phases. Start with
[`docs/00-roadmap.md`](docs/00-roadmap.md), then use the guide for the current
phase. Each phase ends with a working checkpoint and a few questions worth
exploring in the code.

## Current checkpoint

Phase 8 is implemented: the server renders cached standings, results, remaining
fixtures, cache freshness, shareable what-if projections, and transparent raw
and venue-adjusted remaining schedule strength. A single server process also
checks the local cache on a schedule and refreshes the current season only when
it is missing or plausibly stale. Normal page requests never contact ASA.

```sh
go run ./cmd/server
```

Then visit <http://localhost:8080>. The health endpoint is
<http://localhost:8080/healthz>. Cache freshness is available at
<http://localhost:8080/cache/status>.

The current season redirects to <http://localhost:8080/seasons/2026>. The
scenario builder is at <http://localhost:8080/seasons/2026/what-if> and works
with or without JavaScript. Its selected outcomes are encoded in a versioned URL
so a scenario can be bookmarked or shared.

What-if selections specify only home win, draw, or away win. The projection uses
canonical 1-0, 0-0, and 0-1 scorelines so the existing standings service can
apply goal-based tiebreakers. The page labels that assumption explicitly.

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
make build-linux-server
```

That writes `bin/nwsl-season-server-linux-arm64`, suitable for the target ARM64
Linux VM. For an x86_64 VM, run:

```sh
make build-linux-server TARGET_ARCH=amd64
```

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

The runtime must provide exactly one server instance, a writable and persistent
`NWSL_DATA_DIR`, network access to ASA, and graceful `SIGINT`/`SIGTERM`
delivery. Back up the SQLite data directory even though ASA can rebuild the
cache; it retains refresh history and shortens recovery. The process must be
allowed to bind `NWSL_HTTP_ADDR`; the deployment project may proxy it and should
probe `/healthz` and `/cache/status`.

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
