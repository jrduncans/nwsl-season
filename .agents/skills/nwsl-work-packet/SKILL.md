---
name: nwsl-work-packet
description: Plan, implement, or formally review an NWSL ASA-loading work packet. Use when a request names a packet under docs/asa-loading, asks to create or review an ASA-style packet, or requires packet status, prerequisites, allowed files, fixed decisions, non-goals, stop conditions, verification, or completion handoff; do not use for unbounded feature work without a packet.
---

# NWSL work packet

Treat a work packet as a bounded contract, not a prompt suggestion. Read `README.md`, the relevant current guide, `docs/asa-loading/README.md`, the requested packet, and its dependencies before doing packet work. Completed packets are historical design records unless the current code confirms their claims.

## Start with control

1. Locate the packet and read its Control section first. Report status, prerequisite/dependency state, required review level, and blocked dependents.
2. Read fixed decisions, allowed changes, required behavior, tests, verification, non-goals, and stop conditions before editing or judging an implementation.
3. Confirm the requested mode. Use **implementation** for a requested change; use **review** for assessment. If ambiguous, infer the least-mutating mode from the request and say which mode you used.
4. Stop and report when a prerequisite is incomplete, the work needs files outside the allowed changes, a fixed decision conflicts with current code, or a stop condition is met. Do not widen the packet to resolve it.

Use [the packet protocol](references/packet-protocol.md) for the review checklist and status rules.

## Implementation mode

- Edit only allowed files and add files only where the packet permits them. Preserve all fixed decisions and explicitly leave non-goals untouched.
- Add or update the packet’s specified tests. Follow the repository’s `AGENTS.md` checks for every Go change: after the final Go edit run `golangci-lint fmt ./...`, `make lint`, `make vet`, `make test`, and `govulncheck ./...` when network access is available. Run `go test -race ./...` when the change is concurrency-sensitive.
- Run the packet’s narrow verification during iteration, then its full verification and the repository-wide checks before handoff. Record commands and results; distinguish an unavailable vulnerability database from a clean scan.
- Do not change packet status, commit, or advance dependent packets unless the user specifically asks. Hand off files changed, behavior, verification, deviations, and unresolved questions.

## Review mode

- Review the delivered diff and current code against the packet contract, not against an alternative design. Check every fixed decision, allowed-file boundary, behavior clause, test requirement, non-goal, and stop condition.
- Verify relevant tests independently. Use uncached focused tests (`go test -count=1 <packages>`) when timing, test caching, or formal-review confidence makes that useful; then run the packet/repository commands appropriate to the scope.
- Give findings first, ordered by severity with file and line evidence. Report residual risks and verification gaps separately.
- Mark the packet **Complete** only after an actual review pass finds the implementation satisfies the packet and its required checks. Do not mark Complete based only on implementation claims, passing compilation, or a plan review. Do not commit or advance dependents unless explicitly requested.

## Handoff discipline

State the packet ID and mode, current/completed status, exact files changed or reviewed, verification commands/outcomes, and any deviation or blocker. Never imply a dependent packet is ready without checking its declared prerequisites.
