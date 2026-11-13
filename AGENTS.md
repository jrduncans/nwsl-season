# Agent guide

Git hooks from `hk.pkl` fix and lint staged files on commit (including the
telemetry code and generated-artifact checks) and run `go test` and
`govulncheck` on push. Never bypass them with `HK=0` or `--no-verify`. An
agent Stop hook may also run `hk run check --safe` on modified files when you
finish; fix what it reports. Where the hooks aren't installed, run `hk check`
and `make test vuln` instead. If `govulncheck` can't reach its database,
report the scan as skipped, not clean.

The hooks leave these to you. After the final edit to Go, templates,
JavaScript or CSS, run `make test-e2e`. Also run, when they apply:

- Goroutines, channels, locks, shared mutable state, the scheduler, or
  cache/HTTP concurrency: `make race`.
- `telemetry/registry`: `make telemetry-live-check`.

Keep each `gosec` suppression narrow and justified.

On macOS, use approved execution outside Codex's command sandbox for commands
that launch the E2E browser (`make test-e2e`, `make verify`, or
`go test -tags e2e ...`). Chrome can abort during macOS application registration
inside that outer sandbox and show a crash popup. Keep Chromium's own sandbox
enabled (`ChromiumSandbox: true`).

Page requests read only the local SQLite cache; only the scheduler and
`cmd/sync` call ASA.

Production compresses responses at the proxy, so weigh page-size trade-offs
by compressed (gzip or zstd) size, not raw size. See
[README "Runtime and deployment"](README.md#runtime-and-deployment).

## Running the server

Pitchfork already runs the primary checkout's server in watch mode at
<https://nwsl.localhost:7777>; it restarts on edits, so never start another
there. The server restarts on every edit and can be briefly unreachable after
one. If it doesn't answer, wait and retry for up to about 30 seconds, then run
`pitchfork status server` and `pitchfork logs server --tail`: a failed build
from your own edit is the usual cause, and it is yours to fix. Use
`cmd/preview` only if Pitchfork stays down for a reason unrelated to your
edits, and say so in your report. In a linked worktree, run `pitchfork start server`: it serves a snapshot
of the primary cache with the scheduler off, and `pitchfork status server`
prints its URL. Never start a server with the scheduler on, run `cmd/sync`, or
run `make backfill-evaluation-data` outside the primary checkout unless asked.
Details: [README "Linked worktrees"](README.md#linked-worktrees).

`config.env` may be a 1Password-backed FIFO: never read, display, copy or diff
it. Set `NWSL_CONFIG_FILE=/dev/null` for isolated local runs. Secrets and real
telemetry configuration stay out of source, tests and docs.

Before changing behavior, read the owning guide listed in
[README "Documentation"](README.md#documentation); update it with the change.

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues for jrduncans/nwsl-season, via the `gh`
CLI. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: one root `GLOSSARY.md` plus `docs/adr/`, both created lazily.
See `docs/agents/domain.md`.
