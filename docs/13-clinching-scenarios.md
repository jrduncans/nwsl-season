# Phase 13: clinching scenarios

## Goal

Generate the concise qualification conditions commonly reported before a
match-week, for example:

> Portland clinches a playoff place with a win and a Gotham loss.

Apply the same feature to the Shield, a home playoff place, and a playoff place.
Conditions must be mathematically sufficient, use the Phase 12 proof service,
and say exactly which upcoming fixtures and cutoff they cover.

## Define the slate before calculating

A “match-week” is presentation language, not a safe domain assumption. Delayed
and rescheduled games can cross matchday or calendar boundaries. Build an
explicit slate containing fixture IDs and a cutoff time, derived from reliable
matchday data when possible and from a documented kickoff window otherwise.

Show the included date range and fixtures in the UI. Recalculate if a kickoff,
status, or slate membership changes. A condition means the achievement is
guaranteed after the included results are final, with every later regular-season
fixture still allowed to take any feasible result.

## Scenario search

For a normal eight-match league slate, home win/draw/away win has only 6,561
(`3^8`) complete outcome assignments. That outer exploration is small enough
to be exact; the expensive part is proving qualification after each assignment.
Use the Phase 12 fixed-results oracle and share work instead of launching 6,561
independent full-season searches.

Run a cheap opportunity bound first. For example, if `K` opponents already have
more points than the target could hold after winning every target match in the
slate, the target cannot clinch that top-`K` achievement by the cutoff. This can
reject most teams early in the year without a scenario tree. An inconclusive
opportunity bound is only a reason to search, never a published scenario.

Walk a three-way slate decision tree and prune it:

- If the currently fixed conditions guarantee the achievement for every result
  in the unassigned slate fixtures, emit the partial condition immediately.
- If even the most favorable remaining slate outcomes cannot produce a clinch,
  stop that branch.
- Cache equivalent partial assignments and reuse the compiled points model and
  contention-graph components.
- Evaluate likely decisive fixtures first: the target's match, then direct
  threshold rivals and fixtures coupling two rivals.

The result is a set of sufficient conditions, not a dump of complete slate
predictions.

## Minimize and phrase conditions

For every sufficient assignment, remove one condition at a time and retain the
smaller assignment whenever it is still sufficient. Then discard any clause
that is a strict superset of another clause. The remaining clauses are minimal:
removing any named result would make the statement false.

Combine alternatives only when the proof supports the combined language:

- draw or loss becomes **does not win**;
- win or draw becomes **does not lose**;
- a single club condition may be named without its opponent only when the
  fixture is unambiguous in the displayed slate.

Prefer a short number of non-dominated clauses. If many equally minimal paths
remain, show the clearest few first and offer the complete set in a disclosure.
Ordering and prose generation must not alter the underlying machine-readable
conditions.

Examples of honest output include:

- `Clinches a playoff place with a win.`
- `Clinches a home playoff place with a win and Gotham not winning.`
- `Can clinch the Shield this slate; 3 minimal paths.`
- `Cannot clinch a home playoff place this slate.`

Avoid “needs” when a displayed condition is merely one sufficient path among
several. Use “needs” only for a necessary condition established across every
clinching assignment.

## Outcome-only claims and tiebreakers

“A win” describes an outcome, not a 1–0 score. A published outcome-only clause
must remain true for every scoreline compatible with its words. Do not silently
use the what-if page's canonical 1–0, 0–0, and 0–1 scores for this purpose.

The initial scenario release should prefer conditions certified by the Phase 12
points layer. If a possible clinch depends on goal difference or another
tiebreak, either:

- prove a score-independent condition through the validated tiebreak-frontier
  model;
- state the required score margin explicitly; or
- label the opportunity as tiebreak-dependent and withhold a simpler claim.

After actual match results arrive, recalculate the ordinary has-clinched status
from their real scores. The unavailable disciplinary rule remains an explicit
reason not to publish an exact outcome-only clause.

## Result contract

For each team, achievement, slate, and data snapshot, return:

- whether the achievement was already clinched before the slate;
- whether it can be clinched by the slate cutoff;
- all minimal sufficient clauses, as fixture IDs plus allowed outcomes;
- necessary conditions, when any exist across every successful assignment;
- proof method and any tiebreak or data limitation;
- the number of complete assignments represented and search diagnostics.

Preserve this structured form through the view layer. Generate club names and
sentences at presentation time so renamed teams, localization, and accessibility
do not require recomputing the mathematics.

The Phase 12 no-help path and the Phase 13 next-slate clauses should be available
together to consumers. They answer different questions:

- **No-help path**: what can this team guarantee through its own next results?
- **Next-slate conditions**: what combination of its result and help elsewhere
  would be enough by this cutoff?

## Information architecture

Keep achieved status in the standings because it changes how every reader
interprets the table. Use the strongest compact badge specified in Phase 12 and
link it to more detail.

Add a season-level **Clinching scenarios** section when at least one team can
clinch something in the upcoming slate. A compact overview card can show the
highest-value opportunity for each relevant team. Put the complete material at
`/seasons/{season}/clinching`, grouped by achievement or team:

- already-clinched Shield, home-playoff, and playoff status;
- next-slate minimal conditions;
- the conservative no-help path;
- cutoff, data freshness, rule assumptions, and proof limitations.

The fixtures page may link a relevant slate group to this view, but should not
repeat a long condition list beside every match. The Forecast Lab may link to
the page but must keep forecast probabilities visually and verbally separate
from mathematical qualification proofs.

Render useful structured prose without JavaScript. Enhancement may filter by
achievement, expand alternate paths, or highlight the fixtures participating in
a clause. Do not use color alone to connect a condition to a fixture.

## Computation and freshness

Run scenario generation after the Phase 12 snapshot results complete, then
persist or memoize by snapshot, season-rule version, and slate definition. Page
requests read the latest complete calculation. If newer match data arrives
while scenarios are computing, continue showing the prior result only with its
cutoff visibly marked; otherwise show that recalculation is pending.

Evaluate the opportunity bounds after every complete-schedule refresh rather
than enabling this feature on a guessed matchday. That makes the first slate in
which any achievement can be clinched observable automatically.

Do not generate scenarios from an incomplete schedule inventory. A postponed or
removed fixture invalidates every clause that mentions it and changes the
universal quantification over the rest of the slate.

## Exit criteria

- Scenario generation uses the same fixed-results qualification oracle as the
  standings badges for all three configured achievements.
- Tests exhaust every slate outcome in tiny invented seasons and confirm that
  each displayed clause is sufficient and minimal.
- Outcome-only prose is valid for every compatible scoreline or is explicitly
  limited by a margin/tiebreak statement.
- Combined phrases such as “does not win” are emitted only when every combined
  outcome is sufficient.
- The slate definition, cutoff, data snapshot, and incomplete/recalculating
  states are visible and tested.
- The standings show achieved status compactly, while a dedicated view provides
  current status, no-help paths, and complete next-slate conditions.
- Forecast probability, what-if assumptions, and mathematical clinching claims
  remain distinct in language and presentation.
