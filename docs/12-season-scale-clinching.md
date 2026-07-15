# Phase 12: season-scale clinching status

## Goal

Calculate qualification status as soon as it can become interesting, without an
arbitrary limit on the number of league fixtures remaining. Apply one proof
engine to every configured regular-season achievement:

- **Shield**: guaranteed to finish first.
- **Home playoff place**: guaranteed to finish in the top four and therefore to
  host the opening playoff match under the configured format.
- **Playoff place**: guaranteed to finish in the top eight.

The place counts are season rules, not permanent NWSL constants. For a generic
top-`K` achievement, team `T` has clinched when no feasible completion of the
remaining schedule leaves `K` teams officially ahead of `T`.

Shield implies home playoff place, which implies playoff place. The domain
result should retain all three facts even when the standings presentation shows
only the strongest badge.

## Why this is a new phase

Phase 5 established the small-schedule oracle and the conservative treatment of
the unavailable disciplinary tiebreak. The website currently protects page
latency by running it only when four or fewer league fixtures remain. That is
too late for real qualification reporting and repeats nearly the same expensive
search for every team.

This phase retains the Phase 5 oracle for generated tiny-season comparisons,
but replaces the production search strategy. Runtime should depend primarily on
the teams that can still cross a particular threshold and the fixtures coupling
those teams, not on `3^R` or a representative-scoreline equivalent for all `R`
remaining matches.

## Layer 1: bounds available after every refresh

For each target, first consider its worst final points total: its current points
after losses in all of its remaining matches. Giving the target a better result
cannot worsen its final rank. Those losses also award three points to each
opponent, so they must be fixed in the schedule model rather than omitted.

Before invoking an optimizer, calculate these linear-time bounds:

- **Already strictly ahead**: opponents whose current points exceed the target's
  worst final points. Points never decrease. If at least `K` teams are already
  strictly ahead, retain that assignment as an immediate not-clinched witness.
- **Individually capable**: opponents whose current points plus three times
  their remaining matches can at least tie the target's worst final points. If
  fewer than `K` teams are individually capable, the target is clinched even if
  every unresolved points tie is awarded against it.

Individual maximum points are only a screen. Two capable opponents may play
each other and cannot both win that match. An inconclusive bound must therefore
flow into a coupled-fixture model; it must not be shown as a clinch or as a magic
number.

Run these bounds for all teams and all configured achievements after every
complete-schedule cache refresh, including early in the season. They are cheap,
make the first potentially interesting date observable, and greatly reduce the
exact problem before it is difficult.

## Layer 2: exact coupled points feasibility

Represent each relevant remaining fixture with exactly one of home win, draw,
or away win. Final points are linear expressions of those choices. For a fixed
target and `K`, ask two related optimization questions:

1. What is the maximum number of opponents that can finish with **strictly more
   points** than the target?
2. What is the maximum number that can finish with **at least as many points**?

The first query reaching `K` is a definitive not-clinched witness. The second
query staying below `K` is a definitive clinch under every tiebreak. Only the
gap between those answers depends on official tiebreakers.

A mixed-integer or constraint-programming model is a reasonable production
candidate. A custom branch-and-bound search is also acceptable if it exposes
the same result and witness contract. Compare both against the Phase 5 oracle on
generated small schedules before choosing. The model must assign each shared
fixture once; independent team maximums are never a proof.

Reduce the model before solving:

- Exclude opponents that cannot reach the target's points frontier.
- Collapse fixtures between two excluded teams.
- For the points layer, award a plausible contender the best result in a
  fixture against an excluded team unless that fixture also involves the
  target. Only fixtures between contenders create a coupled contention graph.
- Split disconnected contention-graph components and combine their attainable
  counts with dynamic programming.
- Reuse one compiled schedule model, warm starts, and memoized states across the
  three thresholds and sixteen target teams.

Record the reduced team count, reduced fixture count, solver states, runtime,
and proof method. Replace the website's hard remaining-fixture cutoff with a
measured compute budget and an explicit result state.

## Layer 3: the tiebreak frontier

Points settle most qualification proofs. When fewer than `K` teams can finish
strictly above the target but `K` can finish level or above, evaluate only those
tie-frontier scenarios using the official total-table order: goal difference,
wins, goals scored, head-to-head points, and head-to-head goals scored.

The current finite list of representative scores is a useful test universe, but
it is not by itself proof that every possible goal-based ordering has been
considered. Production work must choose and document one of these honest
contracts:

- derive a finite score bound sufficient for the particular tiebreak deficits
  and prove why scores beyond it cannot change feasibility;
- formulate the accessible tiebreak comparisons directly in an optimization
  model; or
- remain conservative and decline to claim a clinch whenever a level-points
  completion could depend on an unproved scoreline or unavailable rule.

Least disciplinary points remains unavailable. If a rival can deny the
achievement only through that rule, return a specific blocked-by-disciplinary
state. Never convert deterministic team-ID order into an official proof.

This conservative boundary can delay a badge, but it cannot create a false
one. Add disciplinary data in a later ingestion change if the delay proves
material.

## Reusable result contract

Return one snapshot-level result per team and achievement, not a bare boolean:

- team, achievement name, and configured `K`;
- `clinched`, `not_clinched`, or `unresolved`;
- proof method: cheap bound, points optimization, accessible tiebreak, missing
  disciplinary rule, or compute budget;
- maximum strictly-ahead and at-least-level opponent counts;
- a feasible blocking schedule when not clinched;
- data snapshot, season-rule version, calculation time, and runtime diagnostics.

Here `not_clinched` means **not yet guaranteed**, supported by a feasible
blocking completion. It does not mean the team has been eliminated. Elimination
is a separate inverse proof and is outside this phase.

The same service must accept fixed future results. Phase 13 will use that form
to ask whether a set of upcoming outcomes is sufficient to clinch. Keeping one
oracle prevents the standings badge and the reporter-style scenario from
disagreeing.

Calculate after a successful cache refresh and persist or memoize by immutable
data snapshot plus rules version. Ordinary page requests should read completed
results rather than start dozens of solvers. An incomplete fixture inventory
continues to suppress all proofs.

## Distance without misleading magic numbers

Expose a conservative **no-help path** for each unclinched achievement. Begin
with the target's next scheduled match, then its next two, and so on; fix those
matches as wins and ask whether the target is guaranteed the achievement on
points regardless of all other results. The first successful prefix supports a
statement such as:

> Portland guarantees a playoff place by winning its next three matches,
> regardless of other results.

Retain the named fixtures behind that statement because beating a direct rival
can matter more than beating another opponent. Do not publish a bare “five more
wins” or “magic number 12” unless its quantifiers are defined. “Any five wins,”
“the next five wins,” and “five wins plus rival points dropped” are different
claims in a shared-fixture league.

This no-help result belongs in the same achievement status object. Match-week
help from other teams belongs in Phase 13.

## Standings presentation

Add a compact qualification-status area to each standings row. Show the
strongest achieved state as the visible badge:

- `Shield` when first place is guaranteed;
- `Home playoff` when top four, but not first, is guaranteed;
- `Playoffs` when top eight, but not top four, is guaranteed.

Accessible text or a disclosure should enumerate every implied achievement.
Do not add three repetitive badges to a Shield winner, and do not rely on color
alone. The row can link to the full clinching view planned in Phase 13, where
the no-help path and proof explanation have room to breathe.

## Exit criteria

- Shield, home-playoff, and playoff thresholds are season configuration and use
  one generic top-`K` engine.
- Cheap bounds run after every complete-schedule refresh with no remaining-game
  gate.
- Coupled-fixture points results agree with exhaustive enumeration on generated
  tiny seasons and return witnesses.
- The tiebreak-frontier contract cannot produce a false exact claim from an
  arbitrary representative-score cap or deterministic fallback.
- Results are snapshot- and rules-versioned and are not recomputed in ordinary
  page requests.
- Every standings row can show the strongest clinched achievement, including
  the correct implied lower achievements for assistive technology.
- Every unclinched team has either a precisely worded no-help path, proof that no
  such path remains, or an explicit unresolved reason.
- Benchmarks use real schedule snapshots from multiple points in the season and
  record when bounds first become conclusive, not only runtime with four matches
  left.
