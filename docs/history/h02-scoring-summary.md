# H02 — Pure season scoring and coverage summaries

## Control

- Status: **Draft; dependency blocked**.
- Implementation: Luna. Review: Terra, focused on denominators and eligibility.
- Prerequisite: H01 accepted with its documented interface unchanged.
- Blocks: H03–H05.
- Goal: deterministic calculations that HTML and charts can both consume.

## Read first and allowed changes

Read [the shared contract](README.md), H01 and its delivered cache API,
`internal/fixtures/fixtures.go`, and `cache.Game`/`cache.GameXG`.

Allowed: add `internal/history/scoring.go`, `internal/history/scoring_test.go`,
and `docs/history-logic-guide.md`. No cache, app, catalog, forecast or dependency
changes. The guide describes calculations delivered here, not future pages.

## Fixed interface

Use package `history` and export:

```go
const MinimumSeasonMatches = 20

type SeasonScoring struct {
    Season string
    Lifecycle cache.SourceScopeLifecycle // empty when unavailable
    Readiness cache.SourceReadiness      // unknown when unavailable
    Inventory cache.InventoryCompleteness
    InventoryGames, Played, Pending, Abandoned, InvalidCompleted int
    TotalGoals int64
    GoalBins [5]int // 0, 1, 2, 3, 4+ combined goals
    XGCovered, XPointsCovered int
    GoalsPerMatch, XGPerMatch, GoalsMinusXGPerMatch *float64
    PlotEligible bool
    Exclusions []string // stable reason codes, not presentation prose
}

func SummarizeScoring(inputs []cache.HistoricalSeason) ([]SeasonScoring, error)
```

Only use input values. No clock, DB, context, network, randomness, rounding,
or mutation of input slices. Imports of cache types are fine (this matches
existing domain adapters); do not instantiate a DB. Sort a copy by numeric
season. Return errors for duplicate season inputs, duplicate fixture IDs in a
season, duplicate xG IDs, blank fixture IDs, or fixture scope mismatches. Do
not silently double-count or choose a first/last conflicting observation.

Defensively reject non-regular or unsupported catalog inputs rather than
aggregating cup records. Use the capability rules from the shared contract.

## Calculation rules and steps

1. Project lifecycle/readiness/inventory from H01; absent readiness means empty
   lifecycle plus unknown readiness and unknown inventory. Keep every season.
2. `InventoryGames` counts cached fixtures. `Played` counts valid, nonnegative
   scored `FullTime` fixtures only. `InvalidCompleted` counts other `FullTime`
   rows. `Abandoned` counts that exact status; `Pending` counts all other
   statuses. Those four counts must sum to `InventoryGames`.
3. Sum home+away goals once per valid played match, increment exactly one bin,
   and calculate `TotalGoals / Played` in floating point. Zero played yields
   nil rates, not NaN or zero. Guard integer overflow in synthetic/corrupt input
   and return an error rather than wrapping goal totals.
4. Join xG by game ID and verify home/away identity. Orphan observations do not
   enter any denominator. Require `Availability == XGAvailable` for either
   expected metric, then validate xG and xPoints pairs independently. Finite,
   nonnegative xG with both values present counts as one xG-covered game;
   paired finite xPoints in [0,3] counts independently. A valid xPoints pair
   need not imply a valid xG pair. No xG capability means both counts are zero.
5. Compute season xG and goals-minus-xG only if `Played > 0` and
   `XGCovered == Played`; use the same `Played` score sample. Keep xPoints
   coverage for reuse, but do not add xPoints rates or UI in this packet.
6. Set `PlotEligible` iff at least 20 matches, available readiness, known
   lifecycle, lifecycle is not upcoming, no invalid completed rows, inventory
   is not known incomplete, and a completed lifecycle has no pending fixtures.
   Unknown inventory alone is not an exclusion: consumers must label it.
7. Append all applicable exclusion codes in this fixed order:
   `source_unavailable`, `lifecycle_unknown`, `upcoming`,
   `inventory_incomplete`, `historical_results_incomplete`,
   `invalid_completed_results`, `below_minimum_matches`.
   Do not place xG incompleteness here: goals remain eligible independently.
   H05 derives xG eligibility as `PlotEligible && XGPerMatch != nil`.
8. An excluded season may still have mathematically valid raw rates for the
   supporting table. Consumers must not treat non-nil rates as chart eligibility.

## Required tests

Use table-driven `TestScoring...` tests with hand-computed expected values.

- Completed scores 0–0, 1–0, 1–1, 2–1, 3–2: Played=5, goals=11, mean=2.2,
  bins=[1,1,1,1,1]; raw rate available but chart excluded below 20.
- Five matching xG pairs totaling 10: xG mean=2, goals-minus-xG mean=0.2.
  Removing one xG pair gives coverage=4/5 and nil xG rates, with the goals
  aggregate unchanged. Use float tolerances for numeric assertions.
- Fully covered xG and missing xPoints, then the reverse: separate counts.
  Include valid zeros, one null side, unavailable observations, NaN, infinity,
  negative values, mismatched teams, and xPoints just above 3.
- Scores on PreMatch/Abandoned/unknown statuses are ignored; a FullTime row
  with a null/negative score is counted invalid and suppresses the plot.
- Empty season and only abandoned/pending matches: no division by zero;
  goal bins sum to played and all status counts sum to inventory.
- Exactly 19 vs 20 scored matches; active vs completed vs upcoming vs missing
  lifecycle; known complete vs known incomplete vs unknown inventory.
- Completed lifecycle with pending games is excluded; active with a future
  schedule is not excluded just for pending games. Abandoned is not pending.
- Missing scope, unavailable source and zero data remain separate output rows.
- Duplicated inputs, bad fixture scopes/IDs, and overflow return errors;
  orphan xG never changes totals. Shuffling inputs/fixtures yields identical
  output and leaves inputs unchanged. Capability-disabled xG is not used.

## Verification

```sh
NWSL_CONFIG_FILE=/dev/null go test -count=1 ./internal/history -run '^TestScoring'
```

Run shared Go checks after the final edit. No race requirement for this purely
local calculation unless the implementation introduces concurrency (which is
outside the contract). Document definitions, threshold and unknown-inventory
policy in the active guide with links back to IDEAS.md.

## Non-goals, stops and handoff

No team aggregates, ranks, historical names, official standings, trajectories,
rendering, or inferred full-season fixture counts. Stop if H01 is unaccepted,
its interface differs, or a scope change is needed. Handoff must include the
coverage/eligibility test results; do not advance H03 or change packet status.
