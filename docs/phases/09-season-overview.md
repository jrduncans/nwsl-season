# Phase 9: season overview and schedule difficulty

## Status

Implemented. The main season page keeps the official standings primary and adds
only a compact visual schedule-ahead indicator. Numeric explanation and
schedule-difficulty exploration live at
`/seasons/{season}/schedule-difficulty`.

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
- **SD** for the compact standings column; use **Schedule difficulty** in the
  accessible explanation and supporting copy.
- **Toughest remaining schedule** and **Easiest remaining schedule** for summary
  callouts.

Avoid relying on "run-in" or an unexplained "SOS" abbreviation. Continue to
label schedule difficulty as an estimate rather than a fact about future
results.

## Season overview

The official standings remain the default and most prominent data. Incorporate
only a compact schedule signal into that area; the full schedule presentation
is on the dedicated detail page.

The implemented overview provides:

- A short `SD` standings column with a baseline-centered marker for each
  available team. Marker position shows easier/harder direction; color,
  opacity, and marker size supplement the relative magnitude.
- A hover title and keyboard/click disclosure with the exact baseline delta,
  qualitative label, remaining match count, and home/away split.
- An unavailable state when remaining opponent history cannot support an
  estimate.
- A clear `Schedule difficulty` route to inspect the supporting detail.

The marker is not color-only: its position and shape communicate direction,
while exact values remain available on hover or disclosure. The qualitative
threshold is ±0.10 PPG (`Harder` above +0.10, `Easier` below −0.10, and `Near
average` within the band), with exact deltas retained so close values are not
overstated. The main table does not print another PPG column.

The default measure is the existing venue-adjusted opponent PPG. The overview
does not place raw and venue-adjusted values side by side as if visitors must
choose a formula before understanding the result.

## Detailed presentation

The dedicated route is `/seasons/{season}/schedule-difficulty`. The initial
overview callouts were moved here after review so they do not compete with the
official standings.

The detail page uses a venue-adjusted comparison centered on the league
baseline. Its dot plot is sorted hardest-to-easiest, prints the values, and
keeps a small visual inset at the plot edges. A native disclosure offers raw
opponent PPG as a comparison to the recommended venue-adjusted measure.

The page begins with toughest/easiest remaining-schedule callouts and explains
that these are estimates, not forecasts or standings adjustments.

Expanding a team shows:

- Every remaining opponent and venue.
- A readable difficulty contribution for each fixture.
- Remaining home and away counts.
- Raw and venue-adjusted values.
- The observed league home and away PPG used by the adjustment.

The page keeps the formula, cache refresh/data cutoff, observed home/away PPG,
and caveats alongside the comparison. It explains the compact overview marker
rather than introducing a separate notion of team quality.

## Boundaries

- Official standings never change because of future schedule difficulty.
- Remaining schedule difficulty is not called an adjusted ranking or a power
  rating.
- Do not add projection probabilities in this phase.
- Preserve useful output without JavaScript; native disclosures provide
  expansion and comparison behavior.
- Keep a single source of truth in `internal/strength`; HTTP view code should not
  recalculate the metric.

## Exit criteria

- [x] Schedule difficulty is available through the compact overview marker and
  dedicated detail route.
- [x] Every team has an accessible, non-color-only indicator or unavailable
  state with an exact supporting value when data permits.
- [x] The venue-adjusted measure is the clearly labeled default, with raw
  opponent PPG available in the detail.
- [x] Visitors can inspect the opponents and venues behind a team's measure.
- [x] The former dense table is no longer the primary presentation.
- [x] Tests cover the league baseline, qualitative labels, unavailable data,
  HTML escaping, routing, and keyboard-accessible disclosure.
- [x] The dedicated detail page is recorded as a presentation choice to
  reconsider after observing usage.
