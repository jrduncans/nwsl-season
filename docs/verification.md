# Verification and test data

`make verify` (fmt, lint, vet, test, test-e2e) is the check required after the
final edit to Go, templates, JavaScript or CSS; run `make e2e-install` once
first. Narrower targets such as `make test-clinching` and `make test-explore`
suit iteration. `make lint` includes `gosec`; keep each suppression narrow and
justified.

## Extra checks by change type

| Change | Also run |
| --- | --- |
| Goroutines, channels, locks, shared mutable state, the scheduler, or cache/HTTP concurrency | `make race` |
| `go.mod`/`go.sum`, or release work | `make vuln`. If its database is unreachable, report the scan as skipped, not clean. |
| `telemetry/registry` | `make telemetry-check-generated` and `make telemetry-live-check` (see the [telemetry guide](telemetry/README.md)) |

## Test data

Build test data with `internal/apptest` (seeded cache scenarios) and
`internal/asatest` (fake ASA server). Tests run against these fakes, never the
real ASA API. [README.md](../README.md) "Test layers" says which layer owns
which assertion.
