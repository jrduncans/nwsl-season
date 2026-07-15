# Phase 5: exact clinching

## Goal

Say that a team has clinched only when no feasible combination of remaining match
results can push it below the playoff line.

## Definition

For a playoff field of `P` teams, team `T` is clinched when every possible set of
remaining results leaves fewer than `P` teams ahead of `T`.

Clinching should use the same total-points official ordering implemented by the
standings package, not the in-season per-game default. The ranking order is:
points, goal differential, wins, goals scored, head-to-head points, and
head-to-head goals scored.

Those criteria are already computable from cached game data, so an exact
clinching search should simulate scorelines, recalculate the final table with
`standings.OfficialTotalRules()`, and count only teams that finish ahead of `T`
under that order. This is sharper than the old points-only proof because a team
that merely ties `T` on points is not automatically treated as ahead.

One official rule remains intentionally unavailable: least disciplinary points.
If a simulated final table leaves `T` tied across all accessible criteria and the
standings row is marked undetermined, the clinching solver should treat that
unresolved tie conservatively. A row that could be ahead of `T` only through the
missing disciplinary rule still blocks an exact clinch unless the result is
reported under a separately named accessible-rules method.

## Why individual maximum points fail

Two chasing teams may play each other. Each can have a theoretical individual
maximum, but both cannot earn three points from the same match. The solver must
assign one of three outcomes to every remaining fixture and honor those shared
constraints.

## Implementation path

1. Write a brute-force enumerator for very small schedules. Each remaining match
   needs enough representative scorelines to exercise points, goal differential,
   wins, goals scored, and head-to-head criteria, not just win/draw/loss.
2. For each completed scenario, call the existing standings calculator with
   `OfficialTotalRules()` and evaluate whether at least `P` teams can finish
   ahead of the target.
3. Build examples that reproduce coupled-fixture clinching and examples where
   accessible tiebreakers decide whether a points tie is safe.
4. Include unresolved disciplinary-rule examples so conservative behavior is
   explicit and testable.
5. Use the brute-force implementation as an oracle for optimized implementations.
6. Add branch-and-bound pruning:
   - stop after enough teams can already pass the target;
   - stop when remaining fixtures cannot create enough passing teams;
   - cap irrelevant point and accessible tiebreak totals once they can no longer
     affect whether a team finishes ahead of the target;
   - evaluate fixtures involving near-threshold teams first;
   - memoize equivalent states.
7. Benchmark realistic late-season schedules before choosing an external solver.

For a proof across all outcomes, it is sufficient to give the target team losses
in all of its own remaining matches. Changing one of those results in the target's
favor cannot make its final position worse, though the search still needs the
target's simulated goals against and the opponents' goals for because those feed
final tiebreakers.

## Explainability

Return more than a boolean. When a team has not clinched, retain one witness
scenario that eliminates it. When it has clinched, report the threshold used and
the maximum number of competitors that can still finish ahead under accessible
official criteria. If the only blocking scenario depends on unavailable
disciplinary points, label that specifically instead of presenting it as a normal
points or scoreline elimination. These are valuable for debugging and for
explaining the result to users.

## Exit criteria

- Tiny exhaustive tests cover every possible outcome.
- A coupled-fixture example succeeds where independent maximum points fails.
- Tiebreak-driven examples use the existing official total standings order.
- Unresolved disciplinary ties conservatively block exact clinches, or are
  reported under an explicitly named accessible-rules result.
- The optimized result agrees with brute force on generated small schedules.
- Runtime is measured on realistic remaining schedules.

## Later generalization

This phase is the small-schedule correctness oracle. The current website limits
it to four remaining league fixtures to protect request latency, and its finite
representative-score universe is not a proof over arbitrary scorelines.
[`12-season-scale-clinching.md`](12-season-scale-clinching.md) plans the bounded
points optimization, tiebreak-frontier contract, Shield and home-playoff
thresholds, and snapshot computation needed to remove that gate. Phase 13 then
uses the same oracle for reporter-style upcoming-slate conditions.
