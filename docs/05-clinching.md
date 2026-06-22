# Phase 5: exact clinching

## Goal

Say that a team has clinched only when no feasible combination of remaining match
results can push it below the playoff line.

## Definition

For a playoff field of `P` teams, team `T` is clinched when every possible set of
remaining results leaves fewer than `P` teams ahead of `T`.

For a conservative points-only proof, treat a team that can tie `T` on points as
potentially ahead. A points-only clinch is therefore valid regardless of
tiebreakers. Later, scorelines and official tiebreak rules can produce sharper
answers under a separately named method.

## Why individual maximum points fail

Two chasing teams may play each other. Each can have a theoretical individual
maximum, but both cannot earn three points from the same match. The solver must
assign one of three outcomes to every remaining fixture and honor those shared
constraints.

## Implementation path

1. Write a brute-force enumerator for very small schedules.
2. Build examples that reproduce coupled-fixture clinching.
3. Use the brute-force implementation as an oracle for optimized implementations.
4. Add branch-and-bound pruning:
   - stop after enough teams can already pass the target;
   - stop when remaining fixtures cannot create enough passing teams;
   - cap irrelevant point totals at the threshold;
   - evaluate fixtures involving near-threshold teams first;
   - memoize equivalent states.
5. Benchmark realistic late-season schedules before choosing an external solver.

For a proof across all outcomes, it is sufficient to give the target team losses
in all of its own remaining matches. Changing one of those results in the target's
favor cannot make its final position worse.

## Explainability

Return more than a boolean. When a team has not clinched, retain one witness
scenario that eliminates it. When it has clinched, report the threshold used and
the maximum number of competitors that can still reach it. These are valuable for
debugging and for explaining the result to users.

## Exit criteria

- Tiny exhaustive tests cover every possible outcome.
- A coupled-fixture example succeeds where independent maximum points fails.
- The optimized result agrees with brute force on generated small schedules.
- Runtime is measured on realistic remaining schedules.
