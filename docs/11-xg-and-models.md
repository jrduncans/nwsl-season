# Phase 11: xG and model comparison

## Goal

Add expected-goals data and multiple understandable forecast models, choose an
opinionated default using historical evidence, and let analytically inclined
visitors compare or deliberately select another model.

Model choice is part of the site's exploratory character. Transparency and a
recommended default should make that choice playful without implying that every
model is equally well supported.

## xG ingestion

The checked-in ASA API description includes NWSL game- and team-level xG
endpoints. Prefer caching game-level values so the application can aggregate
them consistently and reproduce historical cutoffs.

Extend synchronization and persistence to retain:

- Game and team identifiers needed to join xG to cached fixtures.
- Expected goals for both teams, including the exact field definitions returned
  by ASA.
- Source payloads or enough source metadata to diagnose upstream corrections.
- Retrieval and successful-refresh times.
- A clear unavailable state when xG has not yet been published for a completed
  match.

An xG failure must not corrupt the last good fixture or xG snapshot. Decide and
document whether fixtures and xG refresh atomically or have independently
visible freshness.

## Model presets

Offer a small number of curated, versioned presets rather than immediately
exposing every coefficient as a control:

- **Current pace**: a deliberately simple baseline based on observed points per
  game. Its limitations and treatment of shared fixtures must be explicit.
- **Results-based**: the score-generating model introduced in Phase 10, using
  actual results or goals, opponent context, and venue.
- **xG-informed**: a score-generating model whose attacking and defensive
  strength estimates use expected goals, opponent context, and venue.

Exact definitions may evolve through back-testing, so model identifiers and
shared URLs must include versions. Avoid presenting raw schedule difficulty as
a forecast model by itself; it is explanatory context and one possible input to
fixture-level probabilities.

## Back-testing and default selection

Use walk-forward historical evaluation. A forecast made at a historical date
may use only matches and xG from before that date. Do not calculate a historical
team rating from full-season aggregates or otherwise leak future information.

Evaluate more than final-point error. At minimum, record:

- Match-outcome log loss or another proper scoring rule.
- Brier scores and calibration for playoff and Shield probabilities.
- Error or distribution quality for final points and finishing position.
- Performance by season stage so early-season shrinkage can be evaluated
  separately from late-season behavior.

Choose the best-supported general-purpose model as **Recommended**. Record the
evaluation window and rationale. Reconsidering the default should require a new
model version and documented evidence, not an invisible production change.

## Model comparison experience

The season overview always uses the recommended model and names it. Model choice
and comparison belong in the Forecast Lab.

The lab should let a visitor:

- Change the active preset.
- Add a second model for side-by-side comparison.
- See how expected points, playoff probability, and finishing distributions
  change.
- Read a concise explanation of the inputs and major assumptions.
- Reach the full formula, back-test summary, data cutoff, and version.

Preserve fixed-result assumptions while changing models so the visitor can ask
how the same scenario looks under different beliefs. Make clear that choosing a
model changes every unfixed fixture, not the results they explicitly forced.

## Descriptive xG and future power ratings

xG can also appear as descriptive context, such as xG for, xG against, and xG
difference per match. Keep those values in a team detail or analytical view
rather than automatically adding several columns to the official standings.

Validated model strength estimates may later support a separately named **Power
rating**. Such a rating would describe inferred team quality based on completed
matches and opponent strength. It must not be called adjusted standings, and
remaining schedule difficulty must not be used to retroactively alter the
official table.

Custom controls for recency weighting, home advantage, or other coefficients
are a possible later phase. Curated presets come first so their behavior can be
tested and explained.

## Exit criteria

- Game-level NWSL xG is cached with freshness, unavailable states, and safe
  refresh behavior.
- Historical forecasts use only information available at each cutoff.
- Current-pace, results-based, and xG-informed presets have versioned,
  documented definitions.
- Back-tests evaluate calibration and uncertainty as well as point estimates.
- One model is visibly recommended with a recorded rationale.
- Visitors can switch and compare models without losing fixed-result
  assumptions.
- The overview never changes models silently and never confuses forecasts,
  power estimates, schedule difficulty, or official standings.
