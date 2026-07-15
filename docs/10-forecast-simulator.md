# Phase 10: forecast-first simulator

## Goal

Replace the manual fixture-by-fixture what-if workflow with a season forecast
that is useful before the visitor provides any input. Let visitors optionally
fix a small number of results while the model simulates every other remaining
fixture.

The feature may be named **Forecast Lab** in navigation. "What-if" can remain in
supporting copy because fixed-result scenarios are still an important use case.

## Default experience

Opening the page with no selections should immediately show a forecast from one
transparent, results-based model. The page should state:

- The model name and version.
- The data cutoff.
- The number of simulated seasons.
- How many results, if any, the visitor has fixed.
- That every other remaining regular-season fixture was simulated.

For each team, report at least:

- Expected final points and a useful uncertainty interval.
- Playoff probability.
- Expected finishing position and a position interval or distribution.
- Shield probability when the competition format makes it meaningful.

Do not present only one deterministic final table. Forecast uncertainty is part
of the result, not an advanced detail.

## Initial model and simulation contract

Start with one explainable results-based model that produces a probability
distribution over scorelines for each remaining fixture. A reasonable first
implementation can estimate home and away scoring rates from completed goals,
team attacking and defensive records, league scoring rates, and observed home
advantage, with explicit shrinkage toward league averages when samples are
small.

Finalize and document the exact formula before implementation. Keep the model
behind a small domain interface so later xG-informed and comparison models can
produce the same match-level score distribution without changing the simulator.

Simulate shared fixtures once per season iteration; do not independently assign
points to the two teams. Feed sampled scorelines through the existing standings
rules so points and accessible tiebreakers remain internally consistent.

Use a reproducible random seed derived from the data snapshot, model version,
and canonical scenario state, or another documented strategy that makes shared
results stable enough to explain and test. Measure convergence before choosing
the production simulation count.

## Optional fixed-result assumptions

Do not render every remaining fixture as a required form. Start with no
assumptions and provide an `Add a result` interaction that lets the visitor:

1. Find an upcoming fixture, optionally filtered by team.
2. Force a home win, draw, or away win.
3. Review and remove the assumption as a compact chip or list item.

A fixed outcome applies in every simulated season. Unless the visitor later
chooses an advanced exact score, sample a plausible scoreline conditional on the
forced win/draw/loss. Do not silently convert every forced result to the current
canonical 1-0, 0-0, or 0-1 score, because that would distort goal-based
tiebreakers.

Canonical scenario state should remain shareable in the URL and include the
model version and fixed outcomes. Document whether a shared URL always uses the
latest cache or preserves a specific data snapshot; never leave that behavior
implicit.

## Presentation

The season overview may eventually offer a compact `Current | Outlook` switch
that reuses the standings area to show the recommended forecast. The complete
probability distributions, assumptions, and controls belong in the Forecast
Lab.

Phase 10 has only one forecast model, so it does not need a prominent model
picker. Its methodology and limitations remain visible in preparation for the
comparison work in Phase 11.

## Boundaries

- Call official results **standings** and model results **forecast**, **outlook**,
  or **projected finish**.
- Fixed results are user assumptions, not predictions made by the model.
- The model and simulator remain independent of HTTP and SQLite.
- Ordinary page requests continue to use cached source data only.
- Exact clinching proofs remain distinct from forecast probabilities.

## Exit criteria

- The simulator produces a useful forecast with no visitor input.
- Every remaining fixture is represented exactly once in each simulation.
- Output includes uncertainty and playoff probabilities, not only expected
  points.
- Visitors can add, remove, share, and revisit a small set of fixed outcomes.
- Forced outcomes preserve plausible scoreline uncertainty for tiebreakers.
- The page identifies its model version, data cutoff, simulation count, and
  assumptions.
- Fixed seeds and tiny invented seasons make the simulation deterministic in
  tests.
- Model, simulation, and HTTP concerns have separate tests and packages.
