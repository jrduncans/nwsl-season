# Work-packet protocol

## Control checklist

Read these sections in order: status; implementation/review model; dependencies; blocks; goal; why; fixed decisions; allowed changes; required behavior; tests; verification; non-goals; stop conditions; handoff.

## Status rules

| Status | Handling |
| --- | --- |
| Draft | May be planned or implemented only when requested; do not silently promote it. |
| In progress | Inspect its recorded state and prior work before continuing. |
| Ready for review | Perform a real contract review before any Complete transition. |
| Complete | Treat as history. Verify against current code before relying on its claims. |

If a status vocabulary differs in the packet, preserve it and explain the mapping rather than rewriting it.

## Formal review checklist

1. Confirm all prerequisites and the exact packet version.
2. Inspect the diff and files outside the allowed list for accidental scope expansion.
3. Compare behavior and persistence/error/ordering semantics with every fixed decision.
4. Verify all named tests, compatibility clauses, and non-goals.
5. Run uncached focused tests when useful, packet-specific verification, then required repository-wide checks.
6. Record finding evidence and all verification failures/skips.
7. Only then, if the user requested it and no blocking finding remains, update status to Complete.

## Required Go checks

After the final Go edit: `golangci-lint fmt ./...`, `make lint`, `make vet`, `make test`, and `govulncheck ./...` when its database is reachable. Add `go test -race ./...` for goroutines, channels, locks, shared mutable state, scheduler/cache/HTTP concurrency, or concurrency-sensitive tests.

Never infer permission to commit, change dependencies, or advance another packet from a successful review.
