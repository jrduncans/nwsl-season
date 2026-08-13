# AI contribution guidelines

For every AI-authored Go change, run both checks before handing work back:

```sh
golangci-lint fmt ./...
make lint
```

`golangci-lint fmt` applies the repository's configured formatters, including
`goimports`. `make lint` must finish without issues. Run these after the final
Go edit, so the reported result reflects the delivered tree.

For Go changes, also run `make vet` and `make test`. Run the narrowest relevant
tests while iterating, then run the full commands before handoff.

When a change touches goroutines, channels, locks, shared mutable state, the
scheduler, cache concurrency, HTTP concurrency, or concurrency-sensitive tests,
also run the full race suite:

```sh
go test -race ./...
```

The race suite passed in roughly 30 seconds in the development environment, so
it is appropriate as a targeted pre-handoff check without requiring it for
documentation-only or otherwise unrelated changes.

For every AI-authored Go change, run the Go vulnerability scan when network
access is available:

```sh
govulncheck ./...
```

If the vulnerability database cannot be reached, report that the scan was
skipped rather than treating it as a clean result. Run it after dependency
changes and before release work even when the code change appears unrelated.

`make lint` includes `gosec`. Keep suppressions narrow and justified; do not
add blanket exclusions. Run `golangci-lint run --enable-only gosec ./...` only
for focused diagnosis.

## Architecture and data integrity

- Page requests must read the local SQLite cache; they must not contact ASA.
  The scheduler and `cmd/sync` own ASA refreshes.
- Preserve a prior complete fixture inventory when an incoming known inventory
  is incomplete or uneven. Do not publish qualification indicators from an
  incomplete fixture inventory.
- The server and `cmd/sync` share the same persistent `NWSL_DATA_DIR` and sync
  lease. Treat the sync command as maintenance work, not a second service.
- Read [README.md](README.md) and the relevant guide in `docs/` before changing
  synchronization, qualification/clinching, or Forecast Lab behavior.

## Documentation routing

- For sync, cache, scheduler, and ASA-loading work, read the current
  [synchronization guide](docs/sync-logic-guide.md) and
  [ASA-loading index](docs/asa-loading/README.md).
- For qualification and scenarios, read
  [the clinching guide](docs/clinching-logic-guide.md). For Forecast behavior,
  read [the Forecast Lab guide](docs/forecast-lab-guide.md). For model changes,
  follow the evaluation protocol in [README.md](README.md) and its checked-in
  evaluation evidence.
- `docs/phases/` and completed work packets are design history unless they
  explicitly identify current behavior. Verify their historical claims against
  the current code.
- When behavior or required checks change, update the active guide, `AGENTS.md`,
  `Makefile`, and CI together.

## Observability and configuration

- Keep telemetry error classification low-cardinality: use the established
  `error.type` values and a specific `nwsl.error.code`. Preserve the existing
  severity semantics: terminal failures are `ERROR`, absorbed/retryable or
  partial failures are `WARN`, and expected cancellation is `DEBUG`.
- Never commit secrets. `config.env` is intentionally ignored; use
  `config.env.example` for documented configuration shape. Do not put API keys,
  tokens, or real telemetry configuration in source, tests, or documentation.
- `config.env` may be a 1Password-backed FIFO. Never read, display, copy, or
  diff it. For isolated local runs that must not load user secrets, set
  `NWSL_CONFIG_FILE=/dev/null`.

## Forecast model changes

- Preserve the historical evaluation protocol: daily UTC cutoffs prevent
  same-day data leakage; development seasons may guide model changes, while
  final-test seasons alone decide the recommended model.
- Do not replace checked-in evaluation evidence with partial data. The standard
  runner requires every requested season to pass its audit and every final-test
  season to reach 95% xG coverage. Use `-allow-incomplete` only for clearly
  labeled diagnostic output.
