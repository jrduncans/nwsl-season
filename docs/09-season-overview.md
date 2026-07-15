# Phase 9: season overview and schedule difficulty

## Goal

Make remaining schedule difficulty discoverable in the primary season view
without placing another dense, unfamiliar table below the standings.

This phase is a presentation iteration over the existing `internal/strength`
calculation. It should not introduce a forecast model or imply that schedule
difficulty changes the official standings.

## Terminology

Use plain, consistent labels:

- **Schedule difficulty** for compact navigation.
- **Remaining schedule difficulty** for the full heading.
- **Schedule ahead** for a compact standings-row label or column.
- **Toughest remaining schedule** and **Easiest remaining schedule** for summary
  callouts.

Avoid relying on "run-in" or an unexplained "SOS" abbreviation. Continue to
label schedule difficulty as an estimate rather than a fact about future
results.

## Season overview

The official standings remain the default and most prominent data. Incorporate
schedule context into that area rather than asking the visitor to discover a
second table farther down the page.

The initial overview should provide:

- A compact, above-the-fold callout identifying the toughest and easiest
  remaining schedules.
- A schedule-ahead indicator for each standings row. Prefer an understandable
  relative label such as `Harder`, `Near average`, or `Easier` over displaying
  another bare PPG value.
- A hover, focus, tap, or row-expansion explanation with the team's exact
  difference from the league baseline, remaining match count, and home/away
  split.
- A clear route to inspect the supporting schedule detail.

Labels must not rely on color alone. Define and test the league baseline and any
qualitative thresholds before using them. Do not let ranks exaggerate negligible
differences: the exact delta from the league baseline remains available wherever
a category or rank is shown.

The default measure is the existing venue-adjusted opponent PPG. The overview
does not place raw and venue-adjusted values side by side as if visitors must
choose a formula before understanding the result.

## Detailed presentation

Begin with a dedicated route such as
`/seasons/{season}/schedule-difficulty`. This is an initial information
architecture choice, not a commitment that schedule difficulty will always
deserve its own page. Usage and the resulting layout may justify folding the
detail back into the overview later.

Replace the current wide table with a comparison centered on the league
baseline. A horizontal bar or dot plot should make relative differences visible
while also printing the values. Sort by hardest-to-easiest by default and offer
the raw opponent-PPG measure as a comparison to the recommended venue-adjusted
measure.

Expanding a team should show:

- Every remaining opponent and venue.
- A readable difficulty contribution for each fixture.
- Remaining home and away counts.
- Raw and venue-adjusted values.
- The observed league home and away PPG used by the adjustment.

Keep the formula, data cutoff, and caveats on the same page. The detail should
explain the compact overview indicator rather than introduce a separate notion
of team quality.

## Boundaries

- Official standings never change because of future schedule difficulty.
- Remaining schedule difficulty is not called an adjusted ranking or a power
  rating.
- Do not add projection probabilities in this phase.
- Preserve useful output without JavaScript; enhancement may provide expansion
  and view switching.
- Keep a single source of truth in `internal/strength`; HTTP view code should not
  recalculate the metric.

## Exit criteria

- Schedule difficulty is visible without scrolling below the standings.
- Every team has an accessible, non-color-only schedule-ahead indicator with an
  exact supporting value.
- The venue-adjusted measure is the clearly labeled default, with raw opponent
  PPG available in the detail.
- Visitors can inspect the opponents and venues behind a team's measure.
- The former dense table is no longer the primary presentation.
- Tests cover the league baseline, qualitative labels, unavailable data, HTML
  escaping, and keyboard-accessible disclosure.
- The project records that the dedicated detail page may be consolidated after
  observing the implementation.
