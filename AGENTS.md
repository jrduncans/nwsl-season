# Agent guide

After the final edit to Go, templates, JavaScript or CSS, run `make verify`.
Also run, when they apply:

- Goroutines, channels, locks, shared mutable state, the scheduler, or
  cache/HTTP concurrency: `make race`.
- `go.mod`/`go.sum` or release work: `make vuln`; if its database is
  unreachable, report the scan as skipped, not clean.
- `telemetry/registry`: `make telemetry-check-generated` and
  `make telemetry-live-check`.

Keep each `gosec` suppression narrow and justified.

Page requests read only the local SQLite cache; only the scheduler and
`cmd/sync` call ASA.

## Running the server

Pitchfork already runs the primary checkout's server in watch mode at
<https://nwsl.localhost:7777>; it restarts on edits, so never start another
there. In a linked worktree, run `pitchfork start server`: it serves a snapshot
of the primary cache with the scheduler off, and `pitchfork status server`
prints its URL. Never start a server with the scheduler on, run `cmd/sync`, or
run `make backfill-evaluation-data` outside the primary checkout unless asked.
Details: [README "Linked worktrees"](README.md#linked-worktrees).

`config.env` may be a 1Password-backed FIFO: never read, display, copy or diff
it. Set `NWSL_CONFIG_FILE=/dev/null` for isolated local runs. Secrets and real
telemetry configuration stay out of source, tests and docs.

Before changing behavior, read the owning guide listed in
[README "Documentation"](README.md#documentation); update it with the change.
