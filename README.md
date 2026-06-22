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

Phase 1 is scaffolded: a minimal HTTP server with a home page and health check.
It uses only Go's standard library.

```sh
go run ./cmd/server
```

Then visit <http://localhost:8080>. The health endpoint is
<http://localhost:8080/healthz>.

Configuration:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `NWSL_HTTP_ADDR` | `:8080` | Address on which the server listens |
| `NWSL_DATA_DIR` | `data` | Future home of the SQLite cache |

## Useful commands

```sh
go test ./...
go fmt ./...
go vet ./...
```

No ASA request or SQLite database is created yet. Those are the next two
learning phases.

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
