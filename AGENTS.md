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

For changes involving authentication, authorization, HTTP input, filesystem or
SQL handling, secrets, serialization, or other security-sensitive behavior,
also run the embedded `gosec` analyzer:

```sh
golangci-lint run --enable-only gosec ./...
```

`gosec` is intentionally not part of `make lint` yet: the current codebase has
an existing baseline of findings, including intended uses and findings that
need human review. Do not add blanket suppressions; triage each finding before
making `gosec` an enforced clean gate.

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

## Observability and configuration

- Keep telemetry error classification low-cardinality: use the established
  `error.type` values and a specific `nwsl.error.code`. Preserve the existing
  severity semantics: terminal failures are `ERROR`, absorbed/retryable or
  partial failures are `WARN`, and expected cancellation is `DEBUG`.
- Never commit secrets. `config.env` is intentionally ignored; use
  `config.env.example` for documented configuration shape. Do not put API keys,
  tokens, or real telemetry configuration in source, tests, or documentation.

## Forecast model changes

- Preserve the historical evaluation protocol: daily UTC cutoffs prevent
  same-day data leakage; development seasons may guide model changes, while
  final-test seasons alone decide the recommended model.
- Do not replace checked-in evaluation evidence with partial data. The standard
  runner requires every requested season to pass its audit and every final-test
  season to reach 95% xG coverage. Use `-allow-incomplete` only for clearly
  labeled diagnostic output.
