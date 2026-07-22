# Phase 10: forecast-first simulator

## Status

Implemented. Forecast Lab provides a results-based default forecast, optional
conditional fixed outcomes, shareable versioned URLs, and a server-rendered
uncertainty view. The implementation notes below remain the maintenance
contract for this model version.

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

- [x] The simulator produces a useful forecast with no visitor input.
- [x] Every remaining fixture is represented exactly once in each simulation.
- [x] Output includes uncertainty and playoff probabilities, not only expected
  points.
- [x] Visitors can add, remove, share, and revisit a small set of fixed outcomes.
- [x] Forced outcomes preserve plausible scoreline uncertainty for tiebreakers.
- [x] The page identifies its model version, data cutoff, simulation count, and
  assumptions.
- [x] Fixed seeds and tiny invented seasons make the simulation deterministic in
  tests.
- [x] Model, simulation, URL-state, and HTTP concerns have separate packages
  and tests.

## Locked implementation decisions

These decisions define the Phase 10 checkpoint. Do not substitute a more
complicated rating system, xG input, client-side simulator, or persistent
forecast cache. Those are separate follow-up decisions, primarily in Phase 11.

### Routes and compatibility

- Add the Forecast Lab at `/seasons/{season}/forecast`.
- Remove the fixture-by-fixture `/seasons/{season}/what-if` route, template,
  package, and tests. Forecast Lab is the only scenario route.
- Preserve the existing reverse-proxy base-path behavior: generated links and
  redirects remain relative.

### Forecast URL state

The new canonical scenario encoding is:

```text
?v=1&m=results-poisson-v1&p={game-id}:{h|d|a}&p=...
```

- `v` is the URL encoding version and is `1`.
- `m` is required whenever versioned scenario parameters are present and is
  `results-poisson-v1` in this phase.
- `p` is repeated once per fixed outcome and is sorted by game ID when the app
  creates a URL. `h`, `d`, and `a` mean home win, draw, and away win.
- `/forecast` with no query string is the canonical default forecast. It uses
  the only Phase 10 model and has no fixed results.
- Reject unknown encoding/model versions, malformed or duplicate entries,
  completed or unknown fixture IDs, invalid outcomes, and more than 12 fixed
  results with `400 Bad Request`.
- A shared URL uses the latest successful fixture snapshot currently in the
  SQLite cache. It does not preserve historical data. If a fixed fixture has
  since completed or disappeared, return `400` and explain that the assumption
  is stale. State this latest-cache behavior on the page.
- A team filter used by the add-result control is presentation state, not
  scenario state. It may use `team={team-id}` but must be omitted from copy,
  reset, and remove links.

### Results model `results-poisson-v1`

The model uses completed regular-season scores only. Future fixture identities
and fixed outcomes must not affect fitted team strengths.

Use these versioned constants:

```text
prior home goals per match       = 1.50
prior away goals per match       = 1.20
league prior weight              = 20 matches
team prior weight                = 8 team-matches
minimum fixture scoring rate     = 0.20
maximum fixture scoring rate     = 4.50
```

For `M` completed matches, let `HG` and `AG` be all home and away goals:

```text
league_home = (HG + 20 * 1.50) / (M + 20)
league_away = (AG + 20 * 1.20) / (M + 20)
league_team = (league_home + league_away) / 2
```

For each team `t`, let `P_t`, `GF_t`, and `GA_t` be completed matches, goals
for, and goals against at both venues:

```text
attack_t  = ((GF_t + 8 * league_team) / (P_t + 8)) / league_team
defence_t = ((GA_t + 8 * league_team) / (P_t + 8)) / league_team
```

`defence_t` is a goals-conceded multiplier, so a value above one makes the
opponent's scoring rate higher. For a fixture with home team `H` and away team
`A`:

```text
lambda_home = clamp(league_home * attack_H * defence_A, 0.20, 4.50)
lambda_away = clamp(league_away * attack_A * defence_H, 0.20, 4.50)
```

Sample home and away goals independently from Poisson distributions with those
rates. Implement Poisson sampling locally using the standard library; do not
add a dependency for this model. The fixed priors make the forecast available
even before a team has completed a match. The methodology page must describe
the constants as transparent heuristics that have not yet been selected by the
Phase 11 back-test.

### Fixed outcomes

For an unfixed fixture, take the first sampled scoreline. For a fixed fixture,
sample from the same distribution repeatedly until the scoreline has the
required home-win, draw, or away-win relationship. Stop and return an error
after 10,000 unsuccessful attempts as a defensive guard. The positive, clamped
rates should make that guard unreachable in ordinary inputs.

This is rejection sampling from the conditional score distribution. Do not use
canonical 1-0, 0-0, or 0-1 scores for Forecast Lab assumptions.

### Season simulation and aggregation

- Use 50,000 simulated seasons in production. The convergence check recorded
  below selected this count over the original 20,000-run candidate.
- Derive one seed from SHA-256 over a canonical byte representation of the
  model ID, sorted team IDs, sorted cached games and their statuses/scores, and
  sorted fixed outcomes. Use the first eight digest bytes as an integer seed.
  Exclude team display names, cache refresh timestamps, and simulation count.
- Sort fixtures by game ID inside the simulator. Results must not depend on
  caller slice or map iteration order.
- In each iteration, sample each `PreMatch` fixture exactly once, construct one
  completed `standings.Game` from that score, combine it with completed games,
  and call `standings.Calculate` with `standings.OfficialTotalRules()`.
- Ignore statuses other than `FullTime` and `PreMatch`; never invent a result
  for an abandoned fixture. Reject completed games without both scores and
  fixtures that reference unknown teams.
- Check `context.Context` cancellation at least every 100 iterations so an
  abandoned HTTP request does not continue consuming CPU.
- If no fixtures remain, aggregate the one known final table with the weight of
  50,000 identical seasons instead of recalculating it 50,000 times.

For each team aggregate:

- Mean final points and the empirical 10th/90th percentile interval.
- Mean finishing position and the empirical 10th/90th percentile interval.
- Probability for every finishing position.
- Playoff probability using the configured number of playoff places.
- Shield probability, meaning first-place probability.

The existing standings code marks groups that remain tied after every
accessible tiebreak and then uses a deterministic display fallback. Do not let
that fallback turn a team name or ID into forecast probability. For an
unresolved group of `n` teams occupying `n` positions, give every team `1/n` of
an observation at each occupied position. This naturally splits Shield credit
and any playoff places intersected by the group. Points observations remain
whole because all members retain their actual simulated points.

Use weighted nearest-rank quantiles over the accumulated integer histograms.
Render expected values to one decimal and probabilities to one decimal percent.
Order forecast rows by expected position ascending, expected points descending,
then display name and team ID for stable output.

### Page contract

The no-input page must render a forecast immediately. It contains:

- Heading `Forecast Lab` and language that distinguishes the forecast from
  official standings and fixed assumptions from model predictions.
- Model name `Results Poisson`, ID `results-poisson-v1`, data cutoff from the
  last successful cache refresh, 50,000 simulated seasons, fixed-result count,
  and remaining cached fixture count.
- A warning when the cached fixture inventory is not the configured full
  regular-season schedule. The forecast can still run, but the page must say it
  includes only remaining fixtures present in the cache.
- A forecast table with team, expected points and 80% interval, playoff chance,
  expected finish and 80% interval, and Shield chance.
- A native `<details>` disclosure per team containing the full finishing-
  position distribution. This supplies detailed uncertainty without requiring
  JavaScript.
- A compact list of fixed assumptions. Every item names the kickoff, home team,
  away team, forced outcome, and has a remove link that preserves all other
  assumptions.
- An `Add a result` area with a team filter, a remaining-fixture select, and an
  outcome select. Already fixed fixtures are omitted. Forms work with
  JavaScript disabled.
- Reset and copyable canonical-scenario links. A normal anchor to the canonical
  URL is sufficient; do not add clipboard JavaScript in this phase.
- A methodology disclosure containing the formula, priors, conditional-score
  behavior, accessible-tiebreak limitation, deterministic seed behavior, and
  latest-cache URL semantics.

When all remaining fixtures are fixed, keep the forecast table and assumption
list but replace the add form with a short explanation. When the schedule has
no remaining fixture, show the deterministic final outlook with 100%/0%
probabilities as appropriate.

## Package and file plan

Keep the three concerns separate with this layout.

### New `internal/forecast` package

Create:

- `internal/forecast/model.go`
- `internal/forecast/results_poisson.go`
- `internal/forecast/results_poisson_test.go`

`model.go` defines only model-level contracts:

```go
type Info struct {
    ID, Name, Description string
}

type Scoreline struct {
    Home, Away int
}

type Distribution interface {
    Sample(*rand.Rand) Scoreline
}

type Predictor interface {
    Distribution(standings.Game) (Distribution, error)
}

type Model interface {
    Info() Info
    Fit([]standings.Team, []standings.Game) (Predictor, error)
}
```

`results_poisson.go` implements the locked formula as
`NewResultsPoissonV1() Model`. Keep constants beside that implementation. `Fit`
validates team IDs, counts only complete `FullTime` games, and returns an error
for malformed completed data. `Distribution` accepts only a known-team
`PreMatch` fixture. Keep random generation out of the fit so the same predictor
can be reused for every iteration.

### New `internal/simulation` package

Create:

- `internal/simulation/simulation.go`
- `internal/simulation/seed.go`
- `internal/simulation/simulation_test.go`
- `internal/simulation/seed_test.go`

Use explicit request/result values:

```go
type Outcome string

const (
    HomeWin Outcome = "h"
    Draw    Outcome = "d"
    AwayWin Outcome = "a"
)

type Request struct {
    Teams          []standings.Team
    Games          []standings.Game
    Model          forecast.Model
    Fixed          map[string]Outcome
    Iterations     int
    PlayoffPlaces  int
}

type TeamResult struct {
    Team                standings.Team
    ExpectedPoints      float64
    PointsLow           int
    PointsHigh          int
    ExpectedPosition    float64
    PositionLow         int
    PositionHigh        int
    PositionProbability []float64
    PlayoffProbability  float64
    ShieldProbability   float64
}

type Result struct {
    Model       forecast.Info
    Iterations  int
    Seed        uint64
    FixedCount  int
    Remaining   int
    Teams       []TeamResult
}
```

Expose `Run(context.Context, Request) (Result, error)` and a seed helper used by
`Run`. It is acceptable to add unexported histogram/accumulator types. Validate
the entire request before fitting or sampling: positive iterations, playoff
places in `[1,len(teams)]`, unique non-empty team/game IDs, known home/away
teams, valid scores, valid fixed outcomes, and every fixed ID referring to one
remaining fixture.

The simulation package owns conditional rejection sampling and unresolved-tie
fractional credit. The forecast package knows nothing about playoffs, URLs,
fixed results, HTTP, templates, SQLite, or cache timestamps.

### New `internal/forecaststate` package

Create:

- `internal/forecaststate/state.go`
- `internal/forecaststate/state_test.go`

This package handles only versioned scenario syntax, not `net/http`:

```go
const EncodingVersion = "1"
const MaxFixed = 12

type State struct {
    ModelID string
    Fixed   map[string]simulation.Outcome
}
```

Provide parsing of `v`, `m`, and repeated `p` values; sorted encoding of `p`
values; and helpers to return a copied state with one item added or removed.
Take the supported model ID as an argument or validate it in the app so this
package does not become a model registry. Test unknown versions, malformed
values, duplicates, ordering, the 12-item limit, and copy-without-mutation.
Fixture existence and status are validated after the app loads cached season
data and again by `simulation.Run` as defense in depth.

### Application files

Create:

- `internal/app/forecast_handler.go` for the route handler, form-to-canonical
  redirects, state validation against cached fixtures, and simulation call.
- `internal/app/forecast_views.go` for forecast page/view types and formatting.
- `internal/app/templates/forecast.html` for the server-rendered page.

Modify:

- `internal/app/handler.go` to register the route, provide relative paths, and
  teach base-path/trailing-slash helpers about `forecast`.
- `internal/app/views.go` only where shared season/fixture links need a
  `ForecastPath`; do not put simulation calculations in view conversion.
- `internal/app/templates/season.html` and
  `internal/app/templates/fixtures.html` to replace visible what-if calls to
  action with Forecast Lab links.
- `internal/app/static/site.css` for the forecast table, assumption chips/list,
  add form, distributions, warnings, mobile layout, and focus-visible states.
- `internal/app/handler_test.go` for routing, relative paths, legacy behavior,
  errors, escaping, and a low-iteration end-to-end render. Put detailed
  forecast handler tests in a new `internal/app/forecast_handler_test.go` if the
  existing file becomes difficult to navigate.

Add `ForecastIterations int` to `app.Options`. Default it to 50,000, but pass a
small value such as 100 in HTTP tests. Construct `results-poisson-v1` inside the
application wiring; do not expose an HTTP model picker in Phase 10.

Do not call the existing `loadSeasonPage` and then discard most of its work.
The forecast loader should read `Store.Season` directly, convert cached games
with the existing `standingsGames` helper, validate state, run the simulator,
and build its own focused view. This avoids schedule-strength and clinching work
on every forecast request.

### Documentation files

Implemented documentation changes:

- This document records the selected count and convergence measurement.
- `README.md` names Forecast Lab as the primary scenario tool.
- `docs/phases/06-website-and-what-if.md` records that the old checkpoint was removed.
- The roadmap records versioned URLs and latest-cache forecast semantics.

No cache schema, ASA client, syncer, configuration environment variable, or
JavaScript change is required for this phase.

## Implementation sequence

Each work packet should end with its named tests passing. Do not begin with the
HTML; make the model and simulator trustworthy before wiring a page to them.

### 1. Record the baseline

1. Run `go test ./...` and `go vet ./...` before editing.
2. Read the current `standings.Calculate` and `TieBreakStatus` behavior; do not
   duplicate standings or tiebreak logic.
3. Keep the worktree's unrelated changes intact. Phase 10 requires no database
   migration.

Checkpoint: the existing suite passes before Phase 10 changes.

### 2. Implement and test the match model

1. Add the `internal/forecast` interfaces exactly at the match-distribution
   boundary.
2. Implement the constants and formulas above.
3. Implement a Poisson sampler using a supplied `*rand.Rand`; never use the
   package-global random source.
4. Validate inputs and return errors that include the relevant team or game ID.
5. Add table-driven tests for:
   - zero completed matches producing prior rates `1.50` and `1.20`;
   - the hand-calculated smoothed league and team rates from a tiny season;
   - stronger attack raising its scoring rate and weaker defence raising the
     opponent's rate;
   - the clamp bounds;
   - ignored `PreMatch`/abandoned games during fitting;
   - malformed `FullTime` scores and unknown teams;
   - deterministic score sequences from two RNGs with the same seed.

Checkpoint: `go test ./internal/forecast` passes.

### 3. Implement deterministic seed/state helpers

1. Implement canonical sorting and length-delimited SHA-256 seed input. Use
   length prefixes or JSON encoding of fixed structs so concatenated IDs cannot
   collide accidentally.
2. Include game ID, status, team IDs, and explicit score-presence/value fields.
3. Add `forecaststate` parsing and encoding.
4. Test that reordered team/game slices and maps produce the same seed and
   canonical URL values; changing one score, status, model ID, or fixed outcome
   changes it.

Checkpoint: `go test ./internal/simulation ./internal/forecaststate` passes even
though the full simulator is not yet present.

### 4. Implement the simulator

1. Validate and partition completed and remaining games once.
2. Fit the model once, outside the iteration loop.
3. Precompute one match distribution per remaining fixture.
4. For each iteration, sample exactly once per unfixed fixture and conditionally
   for fixed fixtures, construct the shared simulated games, and calculate one
   final total-points table.
5. Aggregate point histograms and fractional position histograms.
6. Identify unresolved groups from `TieBreakStatus.TiedTeamIDs`; process each
   group once and split its occupied positions equally.
7. Convert histograms to the result contract, quantiles, and stable row order.
8. Add tests with tiny invented seasons and fake `forecast.Model`
   implementations for:
   - one model distribution call per fixture during setup and one accepted
     scoreline per fixture per iteration;
   - a shared fixture never awarding independently invented results to its two
     teams;
   - fixed home wins, draws, and away wins in every iteration while exact
     scores remain variable;
   - stale, duplicate, malformed, and unknown inputs;
   - identical results for identical seeds and reordered inputs;
   - a changed `Result.Seed` for a changed snapshot/scenario (aggregate values
     may coincidentally match in a tiny test);
   - final ordering by total points rather than in-season points per game;
   - equal split of first place and a playoff boundary for an unresolved tie;
   - known output for a season with no remaining fixtures;
   - context cancellation.

Checkpoint: `go test ./internal/forecast ./internal/simulation` passes, and no
package in either directory imports `internal/app` or `internal/cache`.

### 5. Add Forecast Lab HTTP behavior

1. Register `/seasons/{season}/forecast` and update relative/base-path route
   recognition.
2. Parse default and versioned state before running the simulator.
3. For add-result GET submissions, validate `fixture` and `outcome`, merge them
   into a copied state, and issue `303 See Other` to the sorted canonical URL.
4. Build remove/reset/filter URLs with `url.Values`; never concatenate raw IDs
   into query strings.
5. Load only cached season data, validate fixed fixtures against it, run with
   the configured iteration count, and convert results to view strings.
6. Treat invalid user state as `400`; preserve existing `500` behavior for
   cache/model/internal failures.
7. Add handler tests for:
   - a no-query request rendering a forecast;
   - model metadata, cutoff, simulation/fixed/remaining counts, uncertainty,
     playoff and Shield labels;
   - canonical sorted add redirects and remove links;
   - team filtering and preservation of fixed state;
   - all specified `400` cases;
   - HTML escaping for team names and fixture IDs;
   - schedule-incomplete and no-remaining states;
   - relative links under both root and preserved proxy base paths;
   - canonical trailing-slash redirect and `404` for the removed old route.

Checkpoint: `go test ./internal/app` passes with low test iteration counts.

### 6. Build the server-rendered experience

1. Add `forecast.html` in the page-contract order: identity/limitations,
   metadata, forecast table, position disclosures, assumptions, add form, and
   methodology.
2. Use semantic tables, labels, native details, and ordinary GET forms. The
   complete workflow must function without JavaScript.
3. Add responsive CSS without changing the meaning or styling of official
   standings.
4. Replace navigation copy and verify the removed route returns `404`.

Checkpoint: run the server against a populated local cache and verify default,
filtered, fixed-result, removed-result, stale-link, mobile-width, and
JavaScript-disabled flows.

### 7. Measure convergence and request cost

The opt-in `TestConvergenceMeasurement` uses one 16-team, 240-fixture
synthetic season at early (32 completed), midseason (120 completed), and late
(224 completed) cutoffs. A 200,000-iteration run is the reference. The maximum
absolute difference across teams was:

| Cutoff | Runs | Playoff pp | Shield pp | Expected points |
| --- | ---: | ---: | ---: | ---: |
| Early | 5,000 | 0.485 | 0.537 | 0.116 |
| Early | 10,000 | 1.071 | 0.375 | 0.103 |
| Early | 20,000 | 0.555 | 0.610 | 0.059 |
| Early | 50,000 | 0.164 | 0.137 | 0.031 |
| Mid | 5,000 | 0.275 | 0.447 | 0.116 |
| Mid | 10,000 | 0.126 | 0.568 | 0.097 |
| Mid | 20,000 | 0.071 | 0.474 | 0.062 |
| Mid | 50,000 | 0.096 | 0.428 | 0.034 |
| Late | 5,000 | 0.000 | 0.729 | 0.049 |
| Late | 10,000 | 0.000 | 0.317 | 0.042 |
| Late | 20,000 | 0.000 | 0.228 | 0.031 |
| Late | 50,000 | 0.000 | 0.165 | 0.006 |

The documented 0.5 percentage-point probability / 0.1-point expected-points
criterion rejects 20,000 runs at the early cutoff and accepts 50,000.
`BenchmarkRun16TeamSeason` measured a 50,000-run midseason forecast at 0.934
seconds on an Apple M4 Pro, below the two-second request-cost limit. Keep the
benchmark and the opt-in convergence check; do not add the expensive reference
run to the normal suite.

The measurement records:

- maximum absolute team error in playoff probability;
- maximum absolute team error in Shield probability;
- maximum absolute team error in expected points;
- wall-clock duration for one run.

The selected count and measurements are recorded above.

### 8. Final verification and handoff

Run:

```sh
go fmt ./...
go test ./...
go vet ./...
```

Then verify every Phase 10 exit criterion against at least one named test or
manual check. Confirm with `rg` that `internal/forecast` and
`internal/simulation` do not import HTTP, templates, SQLite, ASA, or cache
packages; that visible navigation no longer says `Build a what-if scenario`;
and that no Forecast Lab copy calls fixed assumptions predictions or calls a
forecast standings.

Only after all checks pass should the README and status sections claim Phase 10
is implemented.
