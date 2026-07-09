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

Phase 4 is scaffolded: the app has a minimal HTTP server, an ASA client, a
SQLite cache that can be refreshed by a separate sync command, and a standings
calculator that can print a cached regular-season table.

```sh
go run ./cmd/server
```

Then visit <http://localhost:8080>. The health endpoint is
<http://localhost:8080/healthz>. Cache freshness is available at
<http://localhost:8080/cache/status>.

Configuration:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `NWSL_HTTP_ADDR` | `127.0.0.1:8080` | Full address on which the server listens |
| `HOST` | `127.0.0.1` | Host used to build the listen address when `NWSL_HTTP_ADDR` is unset |
| `PORT` | `8080` | Port used to build the listen address when `NWSL_HTTP_ADDR` is unset |
| `NWSL_DATA_DIR` | `data` | Directory containing the SQLite cache |

## Useful commands

```sh
go test ./...
go fmt ./...
go vet ./...
```

To refresh the local ASA cache:

```sh
go run ./cmd/sync -season 2026
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
page requests never refresh ASA data.

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
