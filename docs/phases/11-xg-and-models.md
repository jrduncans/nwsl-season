# Phase 11: xG and model comparison

## Status

The catalog, xG cache, Forecast Lab, and historical walk-forward evaluator are
implemented. The checked-in v1 evidence uses a six-season development window
and a three-season final test, and selects `xg-poisson-v1` as the recommended
default. Phase 10 remains the baseline for simulation, fixed-result, tiebreak,
and latest-cache URL semantics unless this document explicitly changes them.

## Goal

Add game-level expected-goals data, three understandable and versioned forecast
presets, leakage-safe historical evaluation, an evidence-backed recommended
default, and model comparison in Forecast Lab.

The implementation must keep these concepts distinct:

- **Official standings** are calculated only from actual results.
- **Forecasts** are simulated beliefs about unfinished fixtures.
- **Expected goals (xG)** are descriptive observations from completed matches
  and one possible model input.
- **Schedule difficulty** describes remaining opponents; it is not a forecast
  model.
- **Power ratings** are out of scope. No model strength may be labeled an
  adjusted standing or substituted for the official table.

## Scope and non-goals

This phase includes:

- ASA `/nwsl/games/xgoals` client support and raw-payload preservation.
- A SQLite migration for per-game xG availability and independent xG refresh
  audit records.
- Safe fixture-then-xG synchronization with separately visible freshness.
- `current-pace-v1`, the unchanged `results-poisson-v1`, and
  `xg-poisson-v1`.
- Exact match-outcome probabilities from every model distribution so proper
  scoring rules can be calculated without Monte Carlo approximation.
- A deterministic walk-forward back-test command and checked-in JSON and
  Markdown evidence.
- A versioned model catalog with one recommended model.
- Active and comparison model controls in Forecast Lab while preserving fixed
  results.
- A compact recommended outlook on the season overview and a separate
  descriptive xG page.

This phase does not include:

- Training a new shot-level xG model. ASA's team-model game xG is the input.
- Player xG, goalkeeper xG, xPass, goals added, injuries, roster strength,
  transfers, or betting data.
- User-editable priors, home advantage, recency weights, or coefficients.
- Persistent production forecast results. Forecasts remain request-time
  calculations over the latest successful cache.
- Automatic recurring retraining or silently changing a model definition.
- A public power rating.

Do not add a statistical or machine-learning dependency. The three presets,
simulation, metrics, bootstrap, JSON report, and Markdown report can use the Go
standard library.

## Locked data-source decisions

### Endpoint and metric

Use `GET /nwsl/games/xgoals` with `season_name` and `stage_name`. Cache the
endpoint's **team-model** home and away xG values, plus its optional home and
away expected-points values; do not substitute the player-model xG values.
ASA describes its team xG as the team-quality measure that reduces the value of
penalties and repeated shots in one sequence. Keep ASA's terminology in the
methodology copy and link to the source glossary.

The checked-in OpenAPI file documents filters but declares a generic response
object, so it does not establish safe JSON field names. The first implementation
packet must capture a real, complete response object in
`internal/asa/testdata/game_xgoals.json`, identify the exact team-model fields,
and add those exact JSON tags to the wire type. Do not guess field names from
this plan. If the live object does not expose all of the following normalized
values, stop this phase and amend the plan before changing the cache schema:

- game ID;
- home and away team IDs;
- home team-model xG;
- away team-model xG.

When the payload provides `home_xpoints` and `away_xpoints`, persist them as a
pair. They are ASA's estimate of the league points each team should expect from
that match, not official standings points; therefore each must be a finite
value in the inclusive range `[0, 3]`. An older payload may omit both values.

Preserve every source object as `RawJSON`, including fields not normalized.
The normalized application name is `ExpectedGoals`; the ASA wire name remains
whatever the captured payload uses.

References:

- ASA API documentation: <https://app.americansocceranalysis.com/api/v1/__docs__/>
- ASA glossary: <https://www.americansocceranalysis.com/glossary>
- ASA's explanation of team-model and player-model xG:
  <https://www.americansocceranalysis.com/home/2018/3/19/updated-game-by-game-expected-goals>

### Availability meaning

Only a completed (`FullTime`) regular-season game is expected to have xG.
Every completed cached game has one of two explicit states:

- `available`: both finite, non-negative team-model values were returned and
  validated;
- `unavailable`: the latest successful complete xG response did not contain the
  game and no earlier good value exists.

Do not create an unavailable marker for a `PreMatch` or `Abandoned` fixture.
Absence of a row must not be the only way the application represents
unavailability.

### Independent atomic snapshots

Fixture and xG refreshes are independently atomic and independently fresh:

1. Fetch, validate, and commit teams and fixtures exactly as the current sync
   does.
2. Only after that commit succeeds, fetch and validate the complete season/stage
   xG response.
3. Commit all available values and unavailable markers in one xG transaction.
4. If xG fetch, decode, validation, or persistence fails, keep both the new good
   fixture snapshot and the entire last good xG snapshot. Record an xG failure;
   do not record a fixture failure and do not delete or partially update xG.

If a later complete response omits a game that already has an available xG row,
treat the xG response as invalid and preserve the complete previous xG
snapshot. This is safer than interpreting an upstream omission as deletion. A
source correction that returns a valid row may replace the normalized values
and raw payload.

The command and scheduler may report fixture success plus an xG warning in the
same run. `/cache/status` and forecast pages must show fixture and xG freshness
separately. A completed game still marked xG-unavailable keeps the scheduler
eligible for another rate-limited refresh, allowing publication lag to heal
without harming fixture availability.

## Locked cache contract

Increase `schemaVersion` from 2 to 3. Migration 3 creates these tables; do not
rewrite migrations 1 or 2.

Migration 7 later adds the nullable `home_xpoints` and `away_xpoints` columns.
Existing caches receive expected-points values on their next successful xG
refresh.

```sql
CREATE TABLE game_xg (
    asa_game_id TEXT PRIMARY KEY REFERENCES games(asa_game_id) ON DELETE CASCADE,
    availability TEXT NOT NULL CHECK (availability IN ('available', 'unavailable')),
    home_team_id TEXT NOT NULL REFERENCES teams(asa_team_id),
    away_team_id TEXT NOT NULL REFERENCES teams(asa_team_id),
    home_xg REAL,
    away_xg REAL,
    home_xpoints REAL,
    away_xpoints REAL,
    raw_json TEXT NOT NULL,
    first_observed_at TEXT,
    last_checked_at TEXT NOT NULL,
    CHECK (
        (availability = 'available' AND home_xg IS NOT NULL AND away_xg IS NOT NULL
         AND first_observed_at IS NOT NULL)
        OR
        (availability = 'unavailable' AND home_xg IS NULL AND away_xg IS NULL
         AND first_observed_at IS NULL AND raw_json = '')
    )
)
```

```sql
CREATE TABLE xg_sync_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    season TEXT NOT NULL,
    stage TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'failure')),
    error_summary TEXT NOT NULL,
    rows_seen INTEGER NOT NULL,
    available_games INTEGER NOT NULL,
    unavailable_games INTEGER NOT NULL,
    rows_inserted INTEGER NOT NULL,
    rows_updated INTEGER NOT NULL,
    rows_unchanged INTEGER NOT NULL
)
```

Add `xg_sync_runs_season_stage_idx` on
`(season, stage, finished_at)`. Store timestamps as UTC RFC 3339, matching the
current cache.

Add cache values with these responsibilities:

```go
type XGAvailability string

const (
    XGAvailable   XGAvailability = "available"
    XGUnavailable XGAvailability = "unavailable"
)

type GameXG struct {
    GameID          string
    Availability    XGAvailability
    HomeTeamID      string
    AwayTeamID      string
    HomeXG          sql.NullFloat64
    AwayXG          sql.NullFloat64
    HomeXPoints     sql.NullFloat64
    AwayXPoints     sql.NullFloat64
    RawJSON         string
    FirstObservedAt *time.Time
    LastCheckedAt   time.Time
}

type XGSyncRun struct {
    ID, RowsSeen, AvailableGames, UnavailableGames int64
    RowsInserted, RowsUpdated, RowsUnchanged       int64
    StartedAt, FinishedAt                          time.Time
    Season, Stage, Outcome, ErrorSummary           string
}

type XGStatus struct {
    LastAttempt *XGSyncRun
    LastSuccess *XGSyncRun
}
```

`cache.SeasonData` gains `XGoals []GameXG` and `XGStatus XGStatus`. Return an
xG row for every completed cached game, including unavailable rows, ordered by
fixture kickoff then game ID. Existing consumers may ignore the new fields.

`ReplaceGameXG` receives the already-validated full fixture snapshot and xG
response. It must:

- reject duplicate or empty game IDs;
- reject unknown games or teams and home/away identity mismatches;
- reject rows for non-`FullTime` games;
- reject NaN, infinity, and negative values;
- reject omission of any previously available row;
- preserve `first_observed_at` on updates;
- update `last_checked_at` for every available or unavailable completed game;
- classify inserts, material updates, and unchanged values separately;
- insert the successful `xg_sync_runs` row in the same transaction.

Refreshing only `last_checked_at` is `unchanged`, not a material update. A
failure inserts only a failed `xg_sync_runs` audit row and never mutates
`game_xg`.

## Forecast-domain contract changes

Do not add xG fields to `standings.Game`; official standings must remain unaware
of xG. Extend the forecast boundary instead.

Replace the Phase 10 fit signature with:

```go
type ExpectedGoals struct {
    GameID string
    Home   float64
    Away   float64
}

type FitInput struct {
    Teams  []standings.Team
    Games  []standings.Game
    XGoals map[string]ExpectedGoals
}

type OutcomeProbabilities struct {
    HomeWin float64
    Draw    float64
    AwayWin float64
}

type Distribution interface {
    Sample(*rand.Rand) Scoreline
    Outcomes() OutcomeProbabilities
}

type Predictor interface {
    Distribution(standings.Game) (Distribution, error)
    SeedMaterial() []byte
}

type Model interface {
    Info() Info
    Fit(FitInput) (Predictor, error)
}
```

`Info` gains `Inputs`, `Assumptions`, and `MethodPath` strings so templates do
not hard-code one model's methodology. Keep the existing ID, name, and short
description.

`Outcomes` returns exact probabilities summing to one. For an independent
Poisson distribution, calculate the home-win/draw/away-win mass by recurrence,
continuing until both marginal tails are below `1e-12`, with a hard defensive
cap of 100 goals. Normalize the three values once to absorb floating-point
tail error. Tests must compare the analytic values with a large deterministic
sample and require a sum within `1e-12` of one.

`SeedMaterial` is canonical, model-specific fitted input that is not already in
the Phase 10 seed. It prevents a newly published xG row from changing the
random stream of a model that does not use xG. The simulator's seed becomes a
hash of the existing Phase 10 fields plus this byte string:

- `current-pace-v1` returns an empty byte slice because all of its inputs are
  already represented by model ID, teams, games, and fixed outcomes;
- `results-poisson-v1` also returns an empty byte slice, preserving its Phase 10
  seed for identical input;
- `xg-poisson-v1` includes, in game-ID order, every available xG game ID and
  both IEEE-754 values used by the fit.

Use `math.Float64bits` and big-endian encoding for floats. Return a new copied
byte slice. Display names, cache timestamps, raw JSON, unavailable markers, and
unused xG values do not belong in model seed material.

`simulation.Request` gains `XGoals map[string]forecast.ExpectedGoals` and passes
one `FitInput` to the model. `simulation.TeamResult` gains a sorted full points
distribution:

```go
type PointsProbability struct {
    Points      int
    Probability float64
}
```

This supports distribution scoring in back-tests. It need not be rendered in
the initial comparison table. Preserve every other Phase 10 simulation rule,
including fixed-outcome rejection sampling, unresolved-tie fractional credit,
50,000 production iterations, and context cancellation.

## Locked model presets

All constants are part of a model ID. Changing a formula or constant requires a
new ID; never alter these constructors in place after release.

### `current-pace-v1`

Display name: **Current pace**.

This baseline turns observed points per game into a simple scoring-strength
multiplier. It deliberately has no attack/defence decomposition and no
opponent-adjusted fitting.

Use the Phase 10 goal priors and clamps plus one new points prior:

```text
prior home goals per match       = 1.50
prior away goals per match       = 1.20
league prior weight              = 20 matches
team prior weight                = 8 team-matches
prior team points per match      = 1.35
minimum fixture scoring rate     = 0.20
maximum fixture scoring rate     = 4.50
```

For `M` completed matches, `HG` and `AG` total home and away goals, and `TP`
the total table points awarded by those matches:

```text
league_home = (HG + 20 * 1.50) / (M + 20)
league_away = (AG + 20 * 1.20) / (M + 20)
league_ppg  = (TP + 40 * 1.35) / (2*M + 40)
```

For team `t`, with completed matches `P_t` and actual points `Pts_t`:

```text
pace_t = (Pts_t + 8 * league_ppg) / (P_t + 8)
```

For home team `H` and away team `A`:

```text
lambda_home = clamp(league_home * pace_H / league_ppg, 0.20, 4.50)
lambda_away = clamp(league_away * pace_A / league_ppg, 0.20, 4.50)
```

Sample the two scores independently as in Phase 10. The fixture is still
sampled once and shared by both teams. The page must explain that this baseline
translates all past success, including defensive success, into scoring pace and
does not reduce a team's rate for a strong opposing defence. It is a useful
baseline, not a literal independent extrapolation of both teams' final points.

### `results-poisson-v1`

Display name: **Results Poisson**.

Keep the Phase 10 formula and constants byte-for-byte in meaning. Adapting it to
`FitInput`, `Outcomes`, and `SeedMaterial` must not change its fitted rates or
sample sequences for an equivalent seed. It uses actual goals, the opponent's
attack/defence multiplier in each matchup, and league home/away scoring rates.
Its model version does not become `v2` merely because the interface changes.

### `xg-poisson-v1`

Display name: **xG Poisson**.

Use the same priors, shrinkage weights, rate clamp, fixture formula, and Poisson
sampler as `results-poisson-v1`, but replace team goals for and against with
ASA team-model xG for and against.

Only completed games with an available xG row participate in the xG fit. For
the `X` usable matches, sum `HXG` and `AXG`:

```text
league_home = (HXG + 20 * 1.50) / (X + 20)
league_away = (AXG + 20 * 1.20) / (X + 20)
league_team = (league_home + league_away) / 2
```

For each team `t`, `X_t` is its completed matches with xG, `XGF_t` its xG for,
and `XGA_t` its xG against:

```text
attack_t  = ((XGF_t + 8 * league_team) / (X_t + 8)) / league_team
defence_t = ((XGA_t + 8 * league_team) / (X_t + 8)) / league_team
```

For fixture `H` vs `A`:

```text
lambda_home = clamp(league_home * attack_H * defence_A, 0.20, 4.50)
lambda_away = clamp(league_away * attack_A * defence_H, 0.20, 4.50)
```

Missing xG never falls back to actual goals inside this model. With no published
xG, all teams remain at priors and the page shows `0 of N completed matches
with xG`; this is an explicit early-season state, not a hidden switch to the
results model. Always show xG coverage next to this preset.

## Model catalog and recommendation contract

Create one catalog in `internal/forecast/catalog.go`; HTTP code, URL parsing,
and tests must not construct separate lists.

```go
type CatalogEntry struct {
    Model       Model
    Recommended bool
    EvidenceID  string
}

func Catalog() []CatalogEntry
func Lookup(id string) (CatalogEntry, bool)
func Recommended() CatalogEntry
```

Return fresh model values and copied slices. Validate in a catalog test that
IDs are non-empty and unique, exactly one entry is recommended, and every
entry's method path is non-empty.

The initial implementation left `results-poisson-v1` recommended until the
back-test report packet completed. The checked-in v1 evidence selected
`xg-poisson-v1`; future candidate versions must use the development/final-test
protocol below and must not change a formula in the same packet that promotes
it.

## Leakage-safe back-testing

### Evaluation data

Use NWSL regular seasons 2016 through 2025 from the local cache. Exclude 2020,
which did not have the comparable regular-season competition. Use 2016-2019
and 2021-2022 as the development window, and 2023-2025 as the final-test
recommendation window.

The development window is operational: it may be used to compare candidate
model versions and choose fixed constants, priors, feature sets, or fitting
rules through leakage-safe rolling historical evaluations. The final-test
window must not influence any such choice. A formula or constant changed after
examining final-test results is a new model version and cannot claim those
results as untouched evidence; it needs a later completed season for a new
final test. The report must show development, final-test, and pooled
descriptive results separately; pooled results never select the recommendation.

Add season rules for back-testing rather than applying the current eight-place
format historically:

```text
2016-2019: 4 playoff places
2021:      6 playoff places
2022-2023: 6 playoff places
2024-2025: 8 playoff places
```

For every season, audit and report:

- at least two teams and a valid playoff-place count;
- no remaining `PreMatch` regular-season games;
- a parseable kickoff and valid score for every `FullTime` game;
- no duplicate game IDs;
- xG coverage among completed games;
- a final table calculable with the existing official-total standings rules.

Exclude an invalid season from every model comparison and print the exact
reason. For a fair recommendation comparison, the final-test common set must
have at least 95% xG coverage in each season. Otherwise the command fails
before replacing evidence artifacts; `-allow-incomplete` can write a clearly
marked diagnostic report, and the current recommended model remains unchanged.

Historical backfills contain ASA's currently published/corrected xG, not a
record of when ASA first published each old value. State that limitation in the
report. `first_observed_at` prevents the same ambiguity for future prospective
evaluations but must not be pretended to reconstruct old publication history.

### Walk-forward cutoffs

Group fixtures by UTC calendar date. Immediately before the first kickoff on
each date:

1. Mark only games with kickoff before that date as completed and expose their
   scores.
2. Transform every later `FullTime` game into a scoreless `PreMatch` fixture.
3. Expose xG only for completed games before the cutoff.
4. Fit every model from scratch on that cutoff input.
5. Record exact home/draw/away probabilities for every game on that date.
6. Run one 20,000-iteration season simulation with no fixed results and score
   it against the actual final season.

Games on the same UTC date cannot train one another even when kickoff times
differ. This conservative daily-slate rule is easy to audit and rules out
same-day leakage. Future fixture IDs and teams may be known because the season
schedule is assumed published; future statuses, scores, and xG may not be
known. Abandoned games stay ignored under the Phase 10 contract.

Back-tests use 20,000 iterations, a deterministic seed, and context
cancellation. Production remains 50,000. Record the back-test iteration count
in every artifact.

### Recorded metrics

For each model, overall and in stage buckets `0-20%`, `20-40%`, `40-60%`,
`60-80%`, and `80-100%` of scheduled matches completed, record:

- three-way match-outcome log loss using exact `Outcomes` probabilities;
- playoff Brier score per team/cutoff;
- Shield Brier score per team/cutoff;
- final-points mean absolute error and discrete CRPS from the points
  distribution;
- finishing-position mean absolute error and ranked probability score from the
  position distribution;
- sample count for every value.

For playoff and Shield probabilities, also emit fixed decile calibration bins
with count, mean predicted probability, and observed frequency. Empty bins stay
present with count zero in JSON so report shape is stable.

Clamp a probability only for log calculation, at `1e-15`, as a defensive
floating-point guard. Report the unclamped forecast probability. Implement
metric formulas as small pure functions with hand-calculated tests.

### Precommitted recommendation rule

Compare each candidate with the incumbent `results-poisson-v1` on the final-test
common season-date blocks. Use 10,000 deterministic paired bootstrap resamples
of whole season-date blocks and report the 95% interval of each metric
difference (`candidate - incumbent`).

A candidate may replace the incumbent only if:

1. every final-test season passes the data audit and 95% xG-coverage gate;
2. the entire 95% interval for match-outcome log-loss difference is below zero;
3. its point estimate is not worse than the incumbent by more than `0.005` in
   playoff Brier, `0.002` in Shield Brier, `0.25` points in points CRPS, or
   `0.02` in position ranked probability score.

If multiple candidates qualify, choose the one with the lowest final-test match
log loss. If none qualifies, keep `results-poisson-v1`. A simpler model winning
this rule is allowed; do not hard-code xG as the desired answer.

The report must state the chosen ID, evaluation windows, exclusions, coverage,
all overall and stage metrics, calibration tables, bootstrap intervals,
selection-rule result, generation time, Git commit when available, and the
limitations above.

Write both:

- `docs/model-evaluation-v1.json` as the machine-readable source;
- `docs/model-evaluation-v1.md` as the visitor/developer summary.

The command is deterministic apart from an explicitly recorded generation
timestamp. A `-generated-at` flag used by tests must make entire fixture reports
byte-stable.

## Forecast URL state

Keep decoding Phase 10 version 1 links. Add version 2 for model comparison:

```text
?v=2&m={active-model-id}&c={comparison-model-id}&p={game-id}:{h|d|a}&p=...
```

- `m` is required in version 2.
- `c` is optional and must differ from `m`.
- `p` retains the Phase 10 meaning, ordering, validation, and 12-item limit.
- Version 1 accepts only `results-poisson-v1` and has no `c`.
- A bare `/forecast` is the navigational default and resolves through
  `forecast.Recommended()`.
- Generated share links always include `v=2` and `m`, even for the recommended
  model with no fixed results. This preserves the selected model if the future
  recommendation changes.
- `Use recommended` links to bare `/forecast` while preserving no assumptions.
- `Reset assumptions` removes every `p` but preserves `m` and `c`.
- Switching or adding a model preserves every fixed result.
- Removing a comparison preserves the active model and fixed results.
- A `team` fixture filter remains presentation-only and is omitted from share,
  reset, model-switch, and assumption-removal links.

Reject unknown versions or models, duplicate `p`, duplicate active/comparison
models, malformed values, stale fixtures, and excess assumptions with the
existing HTML `400 Bad Request` pattern.

## Forecast Lab experience

The no-query page uses and visibly labels the recommended model. The page adds:

- a **Recommended** badge and link to `model-evaluation-v1.md` on the catalog's
  recommended entry;
- a server-rendered active-model `<select>` or equivalent link list;
- an `Add comparison` control containing the other two presets;
- two model summaries, input coverage, assumptions, version IDs, fixture data
  cutoff, and xG cutoff where applicable;
- a side-by-side table of expected points, playoff probability, expected
  finish, Shield probability, and signed deltas (`comparison - active`);
- separate position-distribution disclosures for both models;
- concise per-model methodology plus links to the full formula and evidence;
- an explicit statement that model changes affect every unfixed fixture but do
  not change visitor-fixed outcomes.

With no comparison, keep the Phase 10 single-model table shape. With a
comparison, run the simulator once per model with the identical teams, fixtures,
fixed outcomes, iteration count, and playoff rules. Model-specific seeds remain
deterministic; do not force identical score samples across different
distributions.

Format expected-value deltas to one decimal, probability deltas as signed
percentage points to one decimal, and position deltas to one decimal. A
negative position delta means a better expected finish; label it so the sign is
not ambiguous. Do not use color alone to show direction.

If xG coverage is incomplete, the xG model remains selectable and uses only the
available games as its locked formula specifies. Show `X of N completed matches`
and a warning below 95%. Never silently replace missing xG with actual goals or
another model.

The latest-cache rule remains: shared URLs do not preserve a source snapshot,
and a fixed fixture that later completes makes the scenario stale.

## Season overview and descriptive xG

Add a server-rendered `Current | Outlook` control to `/seasons/{season}`:

- `Current` is the default and retains official standings.
- `Outlook` is `?view=outlook`; only that request runs a simulation.
- Outlook always uses `forecast.Recommended()`, has no model picker or fixed
  assumptions, names the model and evidence ID, and links to Forecast Lab.
- It shows expected points, playoff chance, expected finish, and Shield chance.
- Unknown `view` values return `400`.

This prevents ordinary official-standings requests from paying forecast CPU
cost and prevents model choice from crowding the overview. When the catalog's
recommendation changes, the model name and evidence link change with it; the
overview must not contain a second hard-coded default.

Add `/seasons/{season}/xg` as a descriptive analytical page, linked near
schedule difficulty rather than inserted into official standings. Aggregate
available completed game xG into:

- matches with xG;
- xG for and xG against;
- xG difference;
- xG for, against, and difference per xG-covered match;
- completed matches missing xG.

Order rows by xG difference per covered match descending, then display name and
team ID. Render totals to two decimals and per-match values to two decimals.
Show fixture and xG freshness, coverage, ASA source/method links, and explain
that xG is descriptive rather than points or a power ranking. Teams with no xG
show unavailable values rather than zero performance.

## Package and file plan

### ASA and cache

Modify:

- `internal/asa/client.go`: filters, wire type, `GameXGoals`, raw decode, URL.
- `internal/asa/client_test.go`: request filters, raw preservation, decode and
  status failures using captured fixtures.
- `internal/cache/cache.go`: migration 3, xG values, replace/failure/status
  methods, season loading, cascade behavior.
- `internal/cache/cache_test.go`: migration from schema 2, insert/update/
  unavailable behavior, omission rollback, independent audit/freshness, and
  source corrections.

Add:

- `internal/asa/testdata/game_xgoals.json`: minimal real-shape fixture with all
  normalized and raw fields.

### Synchronization and operations

Modify:

- `internal/syncer/syncer.go`: add the ASA/cache xG surfaces and the sequential
  independent refresh described above.
- `internal/syncer/syncer_test.go`: xG success, publication lag, malformed xG,
  preservation after failure, and fixture-success/xG-failure cases.
- `internal/scheduler/scheduler.go`: retry while a completed fixture is
  xG-unavailable and log fixture and xG outcomes separately.
- `internal/scheduler/scheduler_test.go`: eligibility and rate-limited retry.
- `cmd/sync/main.go`: print xG coverage/freshness and support `-require-xg`; with
  that flag, exit nonzero after a fixture-success/xG-failure result.
- `cmd/server/main.go`: no new environment option; log independent xG warnings.
- `internal/app/handler.go`: expose nested xG status in `/cache/status`.

Keep the existing sync lease for the whole fixture-plus-xG attempt so two
processes cannot interleave snapshots.

### Forecast and simulation

Modify:

- `internal/forecast/model.go`: `FitInput`, xG, exact outcomes, seed material,
  and expanded model info.
- `internal/forecast/results_poisson.go` and tests: adapt without changing v1
  math.
- `internal/simulation/simulation.go`, `seed.go`, and tests: pass xG, include
  model seed material, expose points distributions.

Add:

- `internal/forecast/current_pace.go`
- `internal/forecast/current_pace_test.go`
- `internal/forecast/xg_poisson.go`
- `internal/forecast/xg_poisson_test.go`
- `internal/forecast/catalog.go`
- `internal/forecast/catalog_test.go`

Share unexported Poisson, validation, canonical-fingerprint, and clamp helpers
inside `internal/forecast`; do not copy the sampler into three files.

### Back-testing

Add:

- `internal/backtest/backtest.go`: audits, cutoff construction, model execution.
- `internal/backtest/metrics.go`: log loss, Brier, CRPS, ranked probability
  score, stage buckets, and calibration.
- `internal/backtest/bootstrap.go`: paired deterministic block bootstrap.
- `internal/backtest/report.go`: stable result structs, JSON, and Markdown.
- corresponding `_test.go` files with tiny invented seasons and hand-calculated
  metrics.
- `cmd/backtest/main.go`: season selection, cache path, iterations, output
  directory, seed, and `-generated-at` flags.
- `cmd/backtest/main_test.go`: flags, invalid data, and byte-stable fixture
  report.
- `docs/model-evaluation-v1.json` and `docs/model-evaluation-v1.md` after the
  command runs on the audited data.

The back-test package may import forecast, simulation, standings, and plain
input values. It must not import HTTP/template code. Metric tests must not
depend on the checked-in live database.

### URL state and application

Modify:

- `internal/forecaststate/state.go` and tests for dual v1/v2 decoding, active
  and comparison IDs, and canonical v2 encoding.
- `internal/app/forecast_handler.go`: catalog lookup, two runs, state-preserving
  actions, xG conversion, and recommendation/default behavior.
- `internal/app/forecast_views.go`: per-model summaries, comparison rows,
  deltas, coverage, and methodology links.
- `internal/app/handler.go` and `views.go`: overview mode and xG route/view.
- `internal/app/templates/forecast.html`: model controls and comparison.
- `internal/app/templates/season.html`: Current/Outlook control and recommended
  outlook.
- `internal/app/templates/schedule-difficulty.html` or shared navigation to link
  the xG analysis without calling it a standing.
- `internal/app/static/site.css`: badges, controls, comparison columns,
  coverage warning, responsive tables, and focus-visible states.
- `internal/app/handler_test.go`: routes, base paths, overview default, xG page,
  escaping, and catalog default.

Add:

- `internal/app/xg_views.go` for descriptive aggregation/view conversion.
- `internal/app/xg_views_test.go` for missing coverage and stable ordering.
- `internal/app/templates/xg.html`.
- `internal/app/forecast_handler_test.go` if comparison cases make the shared
  handler test unwieldy.

No client-side JavaScript is required for model comparison. Existing fixture
filter and pending-assumption enhancement may continue to operate; all new
controls must work without JavaScript.

### Documentation and build

Modify:

- `README.md`: xG cache, model comparison, recommended model, back-test command,
  and routes.
- `docs/phases/00-roadmap.md`: record the chosen recommended model and evidence ID only
  after the evidence packet.
- `Makefile`: add `backtest` only if it is a useful shorthand; do not make
  ordinary `test` depend on network or the live SQLite file.

## Implementation sequence

Each packet ends with the named checkpoint. Do not begin the UI before the data
and model contracts pass.

### 1. Record baseline and capture the ASA contract

1. Run `go test ./...` and `go vet ./...` before editing.
2. Inspect the live xG endpoint for one historical NWSL regular season and save
   a minimal real-shape test fixture.
3. Record the exact wire-to-normalized mapping in an ASA client test.
4. Confirm the response is a complete season/stage set, not paginated. If it is
   paginated or lacks required normalized values, stop and update this plan.

Checkpoint: existing tests still pass, the fixture contains no credentials or
personal data, and no guessed JSON key appears in production code.

### 2. Implement ASA xG client support

1. Add filters and `GameXGoals` using the existing request/error/decode style.
2. Preserve raw JSON per object.
3. Test URL escaping, empty arrays, malformed JSON, non-2xx limited errors,
   exact numeric decode, and unknown extra fields.

Checkpoint: `go test ./internal/asa` passes.

### 3. Migrate and test the independent xG cache

1. Add migration 3 and xG types.
2. Implement transactional successful replacement and failure audit.
3. Extend season and status reads.
4. Test a fresh database and an actual version-2-to-3 migration.
5. Test that any invalid row or omission rolls back all xG value changes while
   leaving fixtures untouched.

Checkpoint: `go test ./internal/cache` passes and foreign-key checks report no
violations.

### 4. Integrate safe synchronization

1. Add xG fetch after the successful fixture commit.
2. Validate xG against the just-fetched complete fixture/team set before cache
   mutation.
3. Represent publication lag as unavailable rows.
4. Preserve the last xG snapshot and record a warning on failure.
5. Teach scheduler, CLI, logs, and status JSON about separate outcomes.

Checkpoint: `go test ./internal/syncer ./internal/scheduler ./internal/app` passes.

### 5. Generalize forecast distributions without changing results v1

1. Add `FitInput`, exact outcome probabilities, and seed material.
2. Adapt Results Poisson.
3. Add regression tests with the existing hand-calculated rates and fixed RNG
   sequence before and after the refactor.
4. Extend simulation with xG input and full points distributions.
5. Prove unused xG changes neither the Results Poisson seed nor output; prove a
   used xG value changes the xG model seed.

Checkpoint: `go test ./internal/forecast ./internal/simulation` passes with no
Phase 10 result regression.

### 6. Implement Current pace

1. Implement the locked formula and model info.
2. Test the zero-match priors, draw and win point totals, hand-calculated pace,
   venue league rates, clamps, ignored non-completed games, and malformed data.
3. Test that a fixture is sampled once for both teams through the simulator.

Checkpoint: all Current pace and simulation tests pass.

### 7. Implement xG Poisson and catalog

1. Implement the locked xG formula and missing-row behavior.
2. Test zero xG, partial coverage, hand-calculated rates, team identity
   validation, NaN/infinity/negative rejection, clamps, and deterministic seed
   material.
3. Add all three models to the catalog with Results Poisson still recommended.

Checkpoint: `go test ./internal/forecast` passes and the catalog invariant test
finds exactly one recommendation.

### 8. Implement URL state version 2

1. Preserve version 1 decoding.
2. Add active/comparison model state and canonical sorted v2 encoding.
3. Add immutable With/Without/Switch/Compare helpers as needed.
4. Cover every malformed, duplicate, and compatibility case.

Checkpoint: `go test ./internal/forecaststate` passes.

### 9. Build the back-test engine and metrics

1. Implement season audit and daily cutoff transformation first.
2. Add a leakage sentinel test: changing any future score or xG value must not
   change predictions at an earlier cutoff.
3. Implement exact match scoring, season simulation scoring, stage buckets, and
   calibration.
4. Add hand-calculated metric and tiny-season tests.
5. Add deterministic paired bootstrap and report rendering.

Checkpoint: `go test ./internal/backtest` passes, including reordered-input and
byte-stable report tests.

### 10. Run evidence and select the recommendation

1. Backfill/audit 2016-2025 regular seasons without committing SQLite/WAL files.
2. Run the command with the locked 20,000 iterations and 10,000 bootstrap
   resamples.
3. Inspect exclusions, xG coverage, calibration sample sizes, and metric
   sanity; fix code bugs but do not tune a released model on final-test results.
4. Generate and commit the JSON and Markdown evidence.
5. Apply the precommitted recommendation rule and change only the catalog flag
   and roadmap statement if a candidate qualifies.
6. Re-run the report after the catalog change to ensure the evidence ID and
   selected ID agree.

Checkpoint: the report is reproducible, catalog and evidence recommend the same
ID, and `go test ./... && go vet ./...` pass.

### 11. Add Forecast Lab model selection and comparison

1. Resolve the bare route through the catalog and explicit routes through v2
   state.
2. Convert cached available xG once and pass identical scenario inputs to each
   requested model.
3. Preserve assumptions through every model action and canonical URL.
4. Render single and comparison states, formulas, freshness, coverage, deltas,
   and evidence.
5. Test no-JavaScript forms, relative/base-path URLs, legacy v1 links, escaping,
   stale assumptions, partial xG, two low-iteration runs, and context
   cancellation.

Checkpoint: focused app tests and `go test ./internal/app` pass.

### 12. Add recommended overview and descriptive xG

1. Add Current/Outlook server-rendered modes and run the recommended model only
   for Outlook.
2. Add the xG analytical page and navigation.
3. Test official standings remain unchanged, no overview model picker appears,
   xG never enters standings points/order, missing values render unavailable,
   and mobile tables remain usable.

Checkpoint: app tests pass and manual checks cover desktop, narrow viewport,
keyboard navigation, and JavaScript disabled.

### 13. Finish operations and documentation

1. Update README, cache-status examples, sync output, and methodology copy.
2. Run `gofmt` on changed Go files.
3. Run `go test ./...`, `go vet ./...`, and both local builds.
4. Run a forced sync against a disposable database, then a second idempotency
   sync. Confirm fixture and xG unchanged counts, freshness, WAL behavior, and
   preservation after a deliberately failing xG test server.
5. Confirm no generated binary, disposable database, `-wal`, or `-shm` file is
   staged.

Checkpoint: all exit criteria below are demonstrated.

## Required test matrix

Before declaring the phase complete, tests must cover at least:

- ASA exact wire decode, raw payload, filters, malformed body, and non-2xx.
- Schema-2 migration, constraints, source correction, unavailable-to-available,
  omission rollback, failure audit, and cascade.
- Fixture failure, xG failure after fixture success, total xG success,
  publication lag retry, and idempotent second sync.
- Exact outcome probabilities, model validation, every locked formula, clamps,
  zero-data priors, partial xG, and model-specific seeds.
- Unchanged Phase 10 fixed-outcome and unresolved-tie behavior for all models.
- Walk-forward future-score and future-xG leakage sentinels.
- Hand-calculated log loss, Brier, CRPS, ranked probability score, calibration,
  stage bucket, and bootstrap cases.
- Deterministic reports under reordered maps/slices and fixed generation time.
- Legacy v1 and canonical v2 URL parsing, all model actions, fixed-result
  preservation, comparison duplicates, and stale scenarios.
- Single/comparison Forecast Lab rendering, recommended badge/evidence,
  coverage warnings, overview Current/Outlook, descriptive xG, base paths,
  escaping, cancellation, and no-JavaScript forms.

## Exit criteria

- [ ] Game-level NWSL team-model xG is cached with raw source, explicit
  available/unavailable state, observation/check times, and independent audit
  freshness.
- [ ] An xG failure cannot roll back a good fixture refresh or partially mutate
  the last xG snapshot.
- [ ] Current pace, Results Poisson, and xG Poisson have locked, versioned,
  tested definitions behind the shared forecast interface.
- [ ] Historical predictions cannot observe future scores or xG, and data
  exclusions/coverage are explicit.
- [ ] Back-tests record proper scoring, calibration, point/position distribution
  quality, and performance by season stage.
- [ ] A checked-in report applies the precommitted selection rule and exactly
  one catalog model is visibly recommended.
- [ ] Forecast URLs preserve active model, optional comparison, and fixed
  results while legacy Phase 10 links remain valid.
- [ ] Visitors can switch and compare models without losing assumptions and can
  see inputs, limitations, versions, cutoffs, coverage, and evidence.
- [ ] The season overview's Outlook uses only the catalog recommendation and
  names it; Current remains official standings.
- [ ] Descriptive xG is separate from official standings, schedule difficulty,
  forecast output, and any future power rating.
- [ ] All tests, vet, builds, disposable-database sync checks, responsive manual
  checks, and no-JavaScript checks pass.
