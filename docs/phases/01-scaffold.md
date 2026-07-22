# Phase 1: Go and HTTP scaffold

## Goal

Understand how a small Go web program is assembled and keep the first checkpoint
easy to run.

## What exists

- `cmd/server/main.go` is the executable entry point.
- `internal/config` reads environment-based configuration.
- `internal/app` owns routes and HTTP handlers.
- Handler tests use `httptest`, so they do not bind a real network port.

The `internal` directory has special meaning in Go: packages beneath it can only
be imported by code in the parent tree. It is a useful boundary for application
code that is not intended to be a public library.

## Try it

```sh
go test ./...
go run ./cmd/server
```

While the server runs, visit `/` and `/healthz`. Then try changing
`NWSL_HTTP_ADDR`:

```sh
NWSL_HTTP_ADDR=:9090 go run ./cmd/server
```

## Hands-on ideas

- Add a failing test for a new `/about` route, then make it pass.
- Change the home handler to use `html/template`.
- Add read and write timeouts to `http.Server` and investigate why they matter.
- Send a `POST` request to `/healthz` and inspect the status returned by Go's
  method-aware route pattern.

## Exit criteria

- `go test ./...` passes.
- The server starts and both routes respond.
- You can explain why `NewHandler` returns `http.Handler`.
