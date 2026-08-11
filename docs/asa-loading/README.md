# ASA loading implementation packets

This directory turns the architecture in
[ASA data catalog and loading plan](../asa-data-catalog-and-loading-plan.md)
into small, independently executable work packets.

The loading plan remains the product and architecture roadmap. A work packet
locks the decisions needed for one implementation slice so a fresh Codex task
can complete it without relying on conversation history.

## Workflow

1. Plan only the current phase in implementation detail. Record cross-phase
   decisions only when they are required to keep an interface or schema stable.
2. Mark a packet `Ready` only after its prerequisites and acceptance tests are
   concrete.
3. Start a fresh task with the model named by the packet and use this prompt:

   > Implement exactly the work packet at `<path>`. Treat its fixed decisions
   > and scope as authoritative. Run every required verification command. Stop
   > and report instead of broadening the design if a stop condition occurs.

4. The implementation task reports changed files, commands run, test results,
   and any deviation. It does not silently resolve a conflict with the packet.
5. A stronger model reviews integration-sensitive changes and updates the
   packet status before the next dependent packet is marked ready.

Packets should normally touch one package or one narrow integration seam. They
are good Luna work when the desired patch can be judged by explicit interfaces
and deterministic tests. Schema ownership, deletion semantics, concurrency,
and cross-package architecture remain Terra or Sol work.

Do not run dependent packets concurrently in the same worktree. Independent
packets may run concurrently only when their allowed file lists do not overlap.

## Status values

- `Draft`: decisions or prerequisites are still open.
- `Ready`: safe to hand to the named implementation model.
- `In progress`: an implementation task owns the packet.
- `Review`: implementation is complete and awaits the named review gate.
- `Complete`: implementation and review are accepted.
- `Superseded`: a later packet replaced this contract.

## Packet index

| Packet | Status | Implementation | Review | Depends on |
| --- | --- | --- | --- | --- |
| [P1-01 Competition catalog core](phase-1/p1-01-competition-catalog-core.md) | Complete | Luna | Terra or Sol | Existing loading plan |
| [P1-02 Persisted source-scope registry](phase-1/p1-02-persisted-source-scope-registry.md) | Complete | Terra | Sol | P1-01 |
| [P1-03 Request-scoped competition rules](phase-1/p1-03-request-scoped-competition-rules.md) | Complete | Terra | Sol | P1-01, P1-02 |
| [P1-04 Capability-aware factual HTTP](phase-1/p1-04-capability-aware-factual-http.md) | Ready | Terra | Sol | P1-01, P1-03 |

P1-04 removes invented competition-format assumptions from HTTP requests and
gates rule-dependent pages with catalog capabilities while retaining factual
fixtures and xG for an unknown cached scope. Persisted inventory/readiness APIs
remain the next Phase 1 packet.

## Packet template

Use [the work-packet template](work-packet-template.md) for subsequent slices.
