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
| [P1-04 Capability-aware factual HTTP](phase-1/p1-04-capability-aware-factual-http.md) | Complete | Terra | Sol | P1-01, P1-03 |
| [P1-05 Persisted season readiness](phase-1/p1-05-persisted-season-readiness.md) | Complete | Terra | Sol | P1-01, P1-02, P1-04 |
| [P2-01 Source-refresh audit/state foundation](phase-2/p2-01-source-refresh-audit-state-foundation.md) | Complete | Terra | Sol | P1-02, P1-05 |
| [P2-02 Independent team-catalog persistence](phase-2/p2-02-independent-team-catalog-persistence.md) | Complete | Terra | Sol | P2-01 |
| [P2-03 Authoritative game-inventory persistence](phase-2/p2-03-authoritative-game-inventory-persistence.md) | Complete | Terra | Sol | P2-02 |
| [P2-04 Targeted checked-game persistence](phase-2/p2-04-targeted-checked-game-persistence.md) | Complete | Terra | Sol | P2-03 |
| [P2-05 Authoritative stage xG persistence](phase-2/p2-05-authoritative-stage-xg-persistence.md) | Complete | Terra | Sol | P2-01, P2-03 |
| [P2-06 Targeted checked-xG persistence](phase-2/p2-06-targeted-checked-xg-persistence.md) | Complete | Terra | Sol | P2-04, P2-05 |
| [Phase 3 Hot-path replacement](phase-3/p3-hot-path-replacement.md) | Complete | Terra | Primary; Sol for concurrency | P2-01 through P2-06 |
| [Phase 4 Historical seasons](phase-4/p4-historical-seasons.md) | Complete | Terra | Primary | Phase 3 |
| [Phase 5 Archived correction sweeps](phase-5/p5-cold-correction-maintenance.md) | Complete | Terra | Primary; Sol for concurrency | Phase 4 |
| [Phase 6 Basic factual playoffs](phase-6/p6-basic-playoffs.md) | Complete | Terra | Primary; Sol for schema/stage isolation | Phase 5 |
| [Phase 7 Season archive and phase-aware presentation](phase-7/p7-season-archive-and-completed-presentation.md) | Complete | Sol with bounded Luna and Terra audits | Terra | Phase 6 |
| [P8-01 Competition catalog](phase-8/p8-01-competition-catalog.md) | Complete | Luna | Terra | Phase 7 |
| [P8-02 Knockout source persistence](phase-8/p8-02-knockout-source-persistence.md) | Complete | Terra | Sol | P8-01 |
| [P8-03 Catalog loading and playoff discovery](phase-8/p8-03-catalog-loading-and-playoff-discovery.md) | Complete | Terra | Sol | P8-02 |
| [P8-04 Archive and factual competition pages](phase-8/p8-04-archive-and-factual-competition-pages.md) | Complete | Luna | Terra | P8-03 |
| [P8-05a Pure bracket domain](phase-8/p8-05a-pure-bracket-domain.md) | Complete | Terra | Sol | P8-04 |
| [P8-05b Bracket HTTP and responsive UI](phase-8/p8-05b-bracket-http-and-responsive-ui.md) | Complete | Luna | Terra | P8-05a |
| [P8-06 Integration rehearsal and final gate](phase-8/p8-06-integration-rehearsal-and-final-gate.md) | Complete | Terra | Sol | P8-05b |
| [P8-07 Startup catalog bootstrap drain](phase-8/p8-07-startup-bootstrap-drain.md) | Review | Terra | Sol | P8-06 |

Phase 1 and P2-01 through P2-06 are complete. Game persistence now provides
authoritative inventory replacement, non-deleting requested-ID checks, omission
and per-game result cadence state, complete fixture snapshot lineage,
generalized audit/state, and exact venue/xG invalidation without changing
current network or scheduler behavior. P2-05 adds authoritative stage xG
persistence, protected available/unavailable observation state,
legacy/generalized full-audit lineage, and atomic venue xG readiness without a
schema change. P2-06 adds separate targeted xG check/due
state, the narrower non-deleting requested-ID operation, and atomic full-xG
maintenance of that state without changing the network or scheduler path.
Phase 3 is complete. The split-operation sync adapter owns one source request
and atomic cache write at a time, while the pure due-job planner batches result
and xG checks, schedules authoritative discovery and weekly inventory audits,
applies bounded correction cadences and a request budget, and executes jobs
sequentially under shared scope leases. Material changes trigger only their
permitted current-scope derived work.

Phase 4 is complete. The regular seasons already used by model evaluation are
factual public catalog entries, one sequential command backfills them through
the split-operation compatibility facade, and cache-only pages provide season
navigation and truthful load states without inventing historical competition
rules.

Phase 5 is complete. The hot correction tiers stay unchanged; deterministic
monthly archived games-then-xG sweeps now use global cold-work serialization,
due-only maintenance, and historical correction reporting.

Phase 6 is complete. The current Playoffs catalog scope now has explicit stage
URLs, normalized knockout facts, and existing full/targeted scheduler jobs
without inferring a bracket or playoff rules.

Phase 7 is complete. It adds a cache-only season catalog plus a
page-preserving season selector on standings and results outside the global
section navigation, records the official historical postseason cut lines, and
makes upcoming, active, and completed season pages show only phase-relevant
controls and labels.

Phase 8 is in progress. P8-01 through P8-06 provide the complete public
competition catalog, immutable versioned bracket formats, schema-14 knockout
facts, ordered catalog bootstrap and maintenance backfill, factual archive and
group-stage pages, and source-backed responsive brackets. Its isolated live
backfill, publication/correction rehearsal, responsive browser acceptance, and
final integrity review all passed. P8-07 shortens the healthy but visibly slow
startup catalog backlog while retaining its bounded, sequential source policy.

## Packet template

Use [the work-packet template](work-packet-template.md) for subsequent slices.
