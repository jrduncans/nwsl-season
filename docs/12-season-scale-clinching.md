# Phase 12: season-scale clinching status

## Status

Implemented. The Phase 5 representative-scoreline oracle described below was
retired after the conservative evaluator became the live qualification
authority. This document preserves the original design and implementation
contract; for the current behavior and proof flow, see
[How clinching works](14-clinching-logic-guide.md).

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

## Locked implementation packet

This section removes the remaining architectural choices so that the phase can
be implemented mechanically. Implement it in the numbered slices below and keep
the test suite green after every slice. Do not begin Phase 13's slate search or
dedicated clinching page in this phase.

### Decisions that are fixed for the first release

- Use a custom, standard-library branch-and-bound solver for the points layer.
  Do not add a mixed-integer, constraint-programming, or graph library.
- Keep the Phase 5 scoreline enumerator as a tiny-season oracle. Move it to an
  `oracle.go` file if useful, but do not change its exported behavior while the
  new engine is being built.
- The production tiebreak frontier is conservative in this release. A points
  tie that could put the target below a cutoff is `unresolved` unless every
  remaining fixture has an actual final score. Do not run the Phase 5
  representative scorelines in production and do not infer a finite score cap.
- A completed season may use `standings.OfficialTotalRules()` to settle the
  accessible tiebreakers exactly. If the cutoff still depends on least
  disciplinary points, return `unresolved` with the missing-disciplinary
  method.
- Calculate qualification after a successful fixture refresh, persist it by a
  deterministic fixture snapshot ID plus rules version, and make HTTP handlers
  read-only consumers. Never start a solver from `loadSeasonPage`.
- The persisted batch is all-or-nothing: it contains one row for every team and
  configured achievement. A per-row timeout is represented by an `unresolved`
  row; an infrastructure or transaction failure fails the whole derived batch.
- Use canonical scores only to serialize a strict-points witness: home win
  `1-0`, draw `0-0`, and away win `0-1`. A strict points lead makes the chosen
  score irrelevant to the proof.
- Keep elimination, score-margin claims, next-slate conditions, and a public
  proof-detail page out of scope.

### Package boundaries and season rules

Add `internal/competition/rules.go` and `rules_test.go`. It owns configuration,
not calculation:

```go
type AchievementID string

const (
    AchievementShield      AchievementID = "shield"
    AchievementHomePlayoff AchievementID = "home_playoff"
    AchievementPlayoffs    AchievementID = "playoffs"
)

type Achievement struct {
    ID    AchievementID
    Label string
    TopK  int
}

type Rules struct {
    Season          string
    Stage           string
    Version         string
    ExpectedTeams   int
    GamesPerTeam    int
    Achievements    []Achievement
}
```

Provide `ForSeason(season, stage) (Rules, bool)` and initially configure the
current 2026 regular season as 16 teams, 30 games per team, and achievements
`shield=1`, `home_playoff=4`, and `playoffs=8`. Use a nonempty immutable version
such as `2026-regular-v1`. Validate that IDs and `TopK` values are unique, every
`TopK` is between 1 and `ExpectedTeams`, the stage is nonempty, and the
achievements are ordered strongest to weakest (increasing `TopK`). Return copies
of slices so callers cannot mutate the catalog.

Replace the parallel `PlayoffPlaces` and `GamesPerTeam` defaults in
`internal/app/handler.go` with one `competition.Rules` value in `app.Options`.
Tests may construct a tiny explicit `Rules`; production resolves rules from the
catalog in `cmd/server/main.go`. Forecast simulation continues to receive the
`TopK` for `AchievementPlayoffs`. Schedule-completeness checks use
`ExpectedTeams` and `GamesPerTeam`, and reject the snapshot if either the team
count or fixture count differs. Do not silently apply 2026 rules to an unknown
season.

Keep the layers acyclic:

- `internal/competition` contains only rule values.
- `internal/clinching` imports `competition` and `standings`, never `cache` or
  `app`.
- `internal/qualification` orchestrates a whole snapshot and maps between
  `cache` values and `clinching` values.
- `internal/cache` owns SQLite persistence DTOs and does not call the solver.
- `internal/syncer` knows only a small post-refresh interface; it does not know
  solver internals.

### Domain contract

Add `internal/clinching/types.go`. Use string enums and validate them at package
boundaries so SQLite and JSON values remain stable:

```go
type Outcome string
const (
    HomeWin Outcome = "home_win"
    Draw    Outcome = "draw"
    AwayWin Outcome = "away_win"
)

type Status string
const (
    Clinched    Status = "clinched"
    NotClinched Status = "not_clinched"
    Unresolved  Status = "unresolved"
)

type ProofMethod string
const (
    ProofCheapBound            ProofMethod = "cheap_bound"
    ProofPointsOptimization    ProofMethod = "points_optimization"
    ProofAccessibleTiebreak    ProofMethod = "accessible_tiebreak"
    ProofMissingDisciplinary   ProofMethod = "missing_disciplinary_rule"
    ProofUnprovedScoreTiebreak ProofMethod = "unproved_score_tiebreak"
    ProofComputeBudget         ProofMethod = "compute_budget"
    ProofIncompleteSchedule    ProofMethod = "incomplete_schedule"
    ProofImplied               ProofMethod = "implied_achievement"
)

type FixedResult struct {
    GameID  string
    Outcome Outcome
}

type WitnessGame struct {
    GameID     string
    HomeTeamID string
    AwayTeamID string
    Outcome    Outcome
    HomeScore  int
    AwayScore  int
}

type CountEvidence struct {
    Value int
    Kind  string // "exact", "lower_bound", or "upper_bound"
}
```

`CountEvidence` is intentional. A cheap bound does not necessarily find the
true maximum, so it must not be persisted or described as an exact maximum.
Use exact count evidence for solver results, a lower bound for an immediate
not-clinched proof, and an upper bound for an immediate clinch proof. For an
incomplete schedule or a budget expiry before a useful count exists, store the
safe lower bound `{Value: 0, Kind: "lower_bound"}` rather than inventing an
exact value.

The new result is:

```go
type AchievementResult struct {
    TeamID             string
    Achievement        competition.AchievementID
    TopK               int
    Status             Status
    Method             ProofMethod
    Reason             string
    StrictlyAhead      CountEvidence
    AtLeastLevel       CountEvidence
    BlockingWitness    []WitnessGame
    FrontierWitness    []WitnessGame
    NoHelp             NoHelpPath
    Diagnostics        Diagnostics
}

type NoHelpState string
const (
    NoHelpNotApplicable NoHelpState = "not_applicable"
    NoHelpGuaranteed    NoHelpState = "guaranteed"
    NoHelpImpossible    NoHelpState = "impossible"
    NoHelpUnresolved    NoHelpState = "unresolved"
)

type NoHelpPath struct {
    State      NoHelpState
    FixtureIDs []string
    Reason     string
}
```

`BlockingWitness` is populated only for `not_clinched` and must be a complete,
feasible assignment of every unfinished fixture. `FrontierWitness` may be
populated for an unresolved at-least-level completion, but must never be called
a blocking schedule because it has not shown that the tied teams finish
officially ahead. `Diagnostics` contains integer fields for bound-capable teams,
reduced teams, reduced fixtures, connected components, visited states, memo
hits, and elapsed microseconds; do not put presentation strings in it.

Expose this entry point from `internal/clinching/evaluate.go`:

```go
type Request struct {
    Teams        []standings.Team
    Games        []standings.Game
    FixtureOrder []string
    TargetTeamID string
    Achievement competition.Achievement
    Fixed        []FixedResult
}

func Evaluate(ctx context.Context, request Request) (AchievementResult, error)
```

Rename the Phase 5 function to `EvaluateOracle` and update its tests to call that
name. Do not retain an old `Evaluate` wrapper—Go cannot overload it with the new
context/request signature. The new request validates unique team and game IDs,
known home/away teams, one fixed result per unfinished fixture, known outcomes,
a present target, and a valid `TopK`. `FixtureOrder` must contain every
unfinished game exactly once and is used only for the no-help prefix; solver
ordering is internal.

Only `FullTime` games with both scores are completed and only `PreMatch` games
are safely unfinished. Any other status, a `FullTime` game missing a score, or a
`PreMatch` game carrying a score makes the snapshot unresolved rather than
guessing how the fixture will be settled.

### Exact points algorithm

Implement the points engine in `internal/clinching/points.go`. Do not calculate
goal difference, wins, goals scored, or head-to-head values in this file.

For one target and one achievement:

1. Calculate current points from completed games.
2. Apply every caller-supplied fixed result once.
3. For each still-unfixed target fixture, force the target to lose and award
   three points to the opponent. Store that canonical outcome in the witness
   under construction. The target's resulting points are the frontier.
4. The only decision fixtures now left are between opponents. For a strict
   query use threshold `frontier+1`; for an at-least-level query use threshold
   `frontier`.
5. Count opponents already over the strict threshold after steps 1-3. If the
   count reaches `TopK`, return `not_clinched`, `ProofCheapBound`, lower-bound
   count evidence, and complete the witness by assigning every other decision
   fixture a draw.
6. For the at-least threshold, count each opponent whose current points plus
   `3 * remainingDecisionFixtures` can reach the threshold. If fewer than
   `TopK` are capable, return `clinched`, `ProofCheapBound`, and upper-bound
   count evidence. This is only a screen; do not use it for any other claim.
7. Otherwise run the exact component solver first for the strict threshold and
   then, only if needed, for the at-least threshold.

Build each threshold query independently because its contender set can differ:

- A contender is an opponent whose current points plus three per incident
  decision fixture can reach the query threshold.
- A fixture between a contender and noncontender is fixed as a contender win.
  This is safe: the noncontender cannot contribute to the objective even with a
  win, while three points is best for the only endpoint that can contribute.
- A fixture between two noncontenders is fixed as a draw. It cannot change the
  objective.
- Only contender-versus-contender fixtures become graph edges. Split the graph
  into connected components. Isolated contenders whose external wins reach the
  threshold contribute one directly.
- Solve each component independently and add its maximum qualifying-team count
  to the fixed count. Concatenate each component's chosen outcomes to construct
  a full assignment.

Within one component, recurse over its fixtures with three branches. Order
fixtures by: fewest combined points still needed by their endpoints, then game
ID. Try outcomes that immediately put an endpoint over the threshold first,
then draw, with game ID as the final deterministic tie-break. At each node:

- Clip each stored points value to the threshold; excess points cannot affect
  this objective.
- The memo key is `(fixtureIndex, clipped points for sorted component team IDs)`.
  Its value is the exact best count and chosen suffix assignment from that
  state, not only a visited boolean; this lets a reused state reconstruct a
  witness. Memoize a state only after all of its non-pruned children have been
  resolved (or its upper and lower bounds are equal). A branch cut only because
  another branch already set a better component-wide incumbent is not an exact
  memo value.
- The lower bound is the number already at threshold.
- The upper bound adds every below-threshold team that can still reach the
  threshold by winning all of its unassigned incident fixtures.
- Prune when that upper bound is no better than the best complete assignment
  already found for the component.
- Check `ctx.Err()` before expanding a node and every 1,024 states. Return a
  typed budget error while retaining diagnostics; never turn a partial best
  assignment into an exact answer.

Seed `best` with the all-draw assignment, then with one deterministic greedy
assignment that gives wins first to the team closest to the threshold. These
are lower bounds only. The recursive search is still responsible for proving
optimality.

Combine the two exact query results as follows:

- `maxStrict >= TopK`: `not_clinched` by points optimization; persist the
  strict query's complete witness.
- `maxStrict < TopK` and `maxLevel < TopK`: `clinched` by points optimization.
- `maxStrict < TopK` and `maxLevel >= TopK`: `unresolved` by
  `ProofUnprovedScoreTiebreak`; persist the at-least query only as a frontier
  witness.
- Context expiry at any point: `unresolved` by `ProofComputeBudget`. Do not keep
  a stale witness from an incomplete query.

When there are no unfinished fixtures with unknown scores, bypass the points
frontier and calculate the actual final table with
`standings.OfficialTotalRules()`. Count teams definitely ahead separately from
teams in the target's `TieBreak.Undetermined` group. Return an accessible-rule
clinch or non-clinch when that is decisive; otherwise return
`ProofMissingDisciplinary` and `unresolved`.

### Fixed results and no-help paths

Fixed results are applied before the target-loss transformation. They may
therefore contain a target win, draw, or loss, which is required by Phase 13.
The evaluator's quantifier is: honor the fixed outcomes, then consider every
feasible outcome for every still-unfixed fixture.

Implement `nohelp.go` after the base result is correct. Split the implementation
into an unexported `evaluateStatus` that never computes a no-help path and the
exported `Evaluate` that calls it and then computes `NoHelp`. Prefix probes must
call `evaluateStatus`, not `Evaluate`, or they will recurse indefinitely:

1. If the achievement is clinched, store `not_applicable`.
2. Select the target's unfinished, unfixed fixtures in `FixtureOrder`.
3. Add a target win for the first fixture and re-evaluate; then the first two,
   and so on. Reuse the same snapshot preparation and memo tables where the
   inputs match, but correctness must not depend on reuse.
4. The first `clinched` result becomes `NoHelpGuaranteed`, with the exact prefix
   of fixture IDs.
5. If fixing every remaining target fixture as a win still produces a strict
   not-clinched witness, use `NoHelpImpossible`.
6. If the all-win case reaches the score-tie frontier, missing discipline, or
   the compute budget, use `NoHelpUnresolved` and preserve that reason.

Do not store a prose magic number. The HTTP layer may later render “wins its
next N matches” from the ordered fixture IDs, but the domain value is the IDs
and the universal guarantee.

Evaluate all three achievements independently, then enforce the safe implication
invariants before persistence: a clinched stronger achievement implies every
weaker achievement is clinched. Mark a result filled only by this normalization
as `ProofImplied` and name the source achievement in `Reason`. Never infer the
reverse direction. Add an invariant test that no persisted team can have Shield
clinched while home playoff or playoffs is non-clinched/unresolved. An implied
clinched row uses `NoHelpNotApplicable` and an empty fixture list.

### Snapshot identity and SQLite migration 4

Increase `schemaVersion` in `internal/cache/cache.go` from 3 to 4. Do not edit
the first three migrations. Migration 4 performs:

```sql
ALTER TABLE sync_runs
ADD COLUMN fixture_snapshot_id TEXT NOT NULL DEFAULT '';

CREATE TABLE qualification_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    fixture_snapshot_id TEXT NOT NULL,
    source_sync_run_id INTEGER NOT NULL REFERENCES sync_runs(id),
    season TEXT NOT NULL,
    stage TEXT NOT NULL,
    rules_version TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('complete', 'failure')),
    error_summary TEXT NOT NULL,
    expected_statuses INTEGER NOT NULL,
    written_statuses INTEGER NOT NULL
);

CREATE INDEX qualification_runs_lookup_idx
ON qualification_runs (fixture_snapshot_id, rules_version, finished_at);

CREATE TABLE qualification_statuses (
    qualification_run_id INTEGER NOT NULL
        REFERENCES qualification_runs(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL REFERENCES teams(asa_team_id),
    achievement TEXT NOT NULL,
    top_k INTEGER NOT NULL,
    status TEXT NOT NULL
        CHECK (status IN ('clinched', 'not_clinched', 'unresolved')),
    proof_method TEXT NOT NULL,
    reason TEXT NOT NULL,
    strictly_ahead_value INTEGER NOT NULL,
    strictly_ahead_kind TEXT NOT NULL
        CHECK (strictly_ahead_kind IN ('exact', 'lower_bound', 'upper_bound')),
    at_least_level_value INTEGER NOT NULL,
    at_least_level_kind TEXT NOT NULL
        CHECK (at_least_level_kind IN ('exact', 'lower_bound', 'upper_bound')),
    blocking_witness_json TEXT NOT NULL,
    frontier_witness_json TEXT NOT NULL,
    no_help_state TEXT NOT NULL,
    no_help_fixture_ids_json TEXT NOT NULL,
    no_help_reason TEXT NOT NULL,
    diagnostics_json TEXT NOT NULL,
    PRIMARY KEY (qualification_run_id, team_id, achievement)
);
```

The snapshot ID is lower-case hex SHA-256 over a length-prefixed canonical
encoding, not ad-hoc JSON. Sort team IDs and games by game ID. Include every
team ID and, for every game: ID, status, home ID, away ID, nullable home score,
nullable away score, and kickoff UTC. Do not include team display names, raw
ASA JSON, fetch timestamps, xG, or the rules version. Test that input order does
not affect the hash and that changing any included value does. Store the rules
version separately as shown above.

`ReplaceSeason` calculates the hash from the validated incoming snapshot and
writes it into the successful `sync_runs` row. Existing pre-migration sync rows
retain the empty default and therefore have no qualification result; do not
backfill a guess. ASA's teams endpoint can contain clubs that do not participate
in the requested season. Derive the snapshot's participating team set from the
home and away IDs in that season/stage's games, require that every referenced
ID exists in the fetched team list, and hash/count only that participating set.
The qualification refresher likewise receives or derives only participating
teams; an unrelated league team must not make a 16-team snapshot look
incomplete or change its hash.

Add cache DTOs `QualificationRun`, `QualificationStatus`, and
`QualificationSnapshot`. Add these methods:

```go
QualificationForSnapshot(ctx, snapshotID, rulesVersion string) (QualificationSnapshot, bool, error)
ReplaceQualification(ctx context.Context, run QualificationRun, rows []QualificationStatus) (QualificationSnapshot, error)
RecordQualificationFailure(ctx context.Context, run QualificationRun, cause error) error
```

`ReplaceQualification` validates all enums, unique `(team, achievement)` pairs,
nonnegative counts, valid JSON, `written_statuses == len(rows)`, and
`expected_statuses == len(rows)`. It inserts the run and every row in one
transaction. `QualificationForSnapshot` returns only the newest `complete` run
for the exact snapshot and rules version. A completed batch containing
per-result `unresolved` rows is still complete.

Extend `cache.SeasonData` with the latest fixture snapshot ID and an optional
matching qualification snapshot. Keep the existing `Season(ctx, season, stage)`
signature so forecast and xG callers do not acquire an irrelevant rules
argument. Add `QualificationForSnapshot` to the app's store interface. The
season handler first reads `SeasonData.FixtureSnapshotID`, then looks up that
exact ID with `Rules.Version`. Update every fake store with this method.
`Season` itself must not attach a best-effort or latest-by-date qualification
batch, because that could let a caller display data for a different fixture
hash or rules version.

JSON columns use `encoding/json` structs, never hand-built strings. Empty
witnesses and fixture lists are encoded as `[]`, not `null`, so cache round-trip
tests are deterministic.

### Post-refresh orchestration

Add `internal/qualification/refresher.go` with a small cache interface and a
`Refresh` method. It receives the exact normalized teams and games that were
committed, the successful sync run (including snapshot ID), competition rules,
and a total calculation budget.

The refresher:

1. Returns the existing complete batch immediately when snapshot ID and rules
   version already exist, unless the operator forces recalculation or a status
   or no-help result exhausted its calculation budget.
2. Verifies exact team and fixture counts and the expected per-team fixture
   count. For an incomplete inventory, writes all expected team/achievement
   rows as `unresolved`/`ProofIncompleteSchedule`; it does not call the solver.
3. Sorts unfinished fixture IDs by parsed kickoff UTC and then game ID for the
   no-help order. A missing or invalid kickoff makes the affected results
   unresolved.
4. Uses one `context.WithTimeout` for the complete snapshot calculation. The
   default budget is five seconds and is configurable as
   `NWSL_QUALIFICATION_BUDGET`. Once it expires, fill every not-yet-calculated
   row with `ProofComputeBudget` so the batch still has exactly
   `teams * achievements` rows.
5. Evaluates teams in current official-total table order and achievements from
   easiest to guarantee to hardest (`TopK` descending). This makes the most
   useful statuses available first if a budget is reached; output rows are
   sorted by team ID then achievement ID before persistence.
6. Persists with a short cleanup context even if the calculation context has
   expired. A transaction failure is recorded as a failed qualification run.

Add a post-fixture-refresh interface to `syncer.Service`, for example:

```go
type QualificationRefresher interface {
    Refresh(context.Context, cache.SyncRun, []cache.Team, []cache.Game, bool) (bool, error)
}
```

Call it only after `ReplaceSeason` succeeds. Fixture and xG success remain
independent of qualification success: a qualification error is returned as a
warning field on `cache.SyncRun`, logged by the scheduler, and printed by
`cmd/sync`, but it must not roll back or relabel the successful fixture refresh.
Use a fresh timeout derived from `context.WithoutCancel(ctx)` for the five-second
derived calculation because the fixture transaction is already durable; this
also prevents time spent fetching ASA data from consuming the calculation
budget. The finite budget prevents shutdown from being delayed indefinitely.

Wire the same refresher in `cmd/server/main.go` and `cmd/sync/main.go`. Add the
new duration to `internal/config.Config`, environment parsing tests, and the
README configuration table. An unknown season/rules pair records a clear
qualification warning and serves no badge; it must not fall back to 2026.

### Read path and standings badge

Delete `maxClinchingFixtures` and `clinchingViews` from
`internal/app/handler.go`. `loadSeasonPage` performs only these qualification
steps:

1. Load the current season data and matching persisted qualification batch.
2. If the schedule is incomplete, no batch matches, the rules version differs,
   or the batch is not complete, render no badge and expose a short pending or
   unavailable note in the page model. Never show a prior snapshot's badge.
3. Group persisted statuses by team. Only `clinching.Clinched` counts as achieved;
   `not_clinched` and `unresolved` both render no achievement badge.
4. Choose the clinched achievement with the smallest `TopK` as the visible
   strongest badge: `Shield`, `Home playoff`, or `Playoffs`.
5. Build accessible text listing every clinched/implied achievement, strongest
   to weakest. Do not rely on the `title` attribute or color alone.

Replace the `Clinched bool` field on `tableRowView` with
`QualificationBadge`, `QualificationTitle`, and `QualificationAchievements`.
Render the badge in the `currentTable` partial in
`internal/app/templates/partials.html`; that is the partial used by the season
overview. A suitable shape is a visible badge plus a `visually-hidden` span
whose text is “Guaranteed achievements: Shield, home playoff place, playoff
place.” Add only the CSS needed for the compact badge and a non-color cue. No
JavaScript is needed.

Extend `/cache/status` with a small `qualification` object containing fixture
snapshot ID, rules version, calculation outcome, calculation time, written row
count, and whether it matches the current fixture snapshot. Do not expose full
witness JSON there.

### Required tests

Add tests in the same slice as their production code. The minimum matrix is:

`internal/competition/rules_test.go`

- 2026 resolves all three unique thresholds and returns defensive copies.
- Unknown season/stage does not receive defaults.
- Invalid or non-monotonic rule sets are rejected.

`internal/clinching/points_test.go`

- Already-strictly-ahead bound returns a complete feasible witness.
- Individually-capable bound clinches when fewer than `TopK` can reach level.
- Two contenders sharing one fixture cannot both receive three points.
- A contender receives the fixed win against an excluded team.
- Disconnected components combine their maxima and witness assignments.
- Strict and at-least queries can have different contender sets.
- Memoization with clipped points matches an exhaustive three-outcome enumerator.
- Cancellation returns the typed budget result, never a partial exact count.
- Generated schedules of 3-6 teams and 0-7 unfinished games match exhaustive
  enumeration for both maximum counts and validate every strict witness.

`internal/clinching/evaluate_test.go`

- Strict maximum reaching `TopK` is not clinched.
- At-least maximum below `TopK` is clinched.
- The gap between the two is unresolved, not a clinch.
- A completed table is decided by accessible goal difference/head-to-head.
- A completed table tied through head-to-head is blocked by discipline.
- Fixed target wins change the target frontier before forced losses.
- Invalid statuses, scores, fixture order, and fixed results are rejected or
  conservatively unresolved as specified.
- New points-conclusive results agree with the Phase 5 oracle on tiny schedules;
  frontier cases may be more conservative and should assert that explicitly.

`internal/clinching/nohelp_test.go`

- The first successful prefix is returned, not merely a count.
- Beating a direct rival can make a shorter prefix decisive.
- All target wins with a strict blocker returns `impossible`.
- A tiebreak-only all-win case returns `unresolved`.
- Stronger achievements imply every weaker result.

`internal/cache/cache_test.go`

- A version-3 database migrates to version 4 without changing old rows.
- Snapshot hashes are stable under input reordering and sensitive to every
  included field.
- Qualification replacement and load round-trip witnesses, no-help values, and
  diagnostics.
- Duplicate/missing rows, invalid enum values, invalid JSON, and a count mismatch
  roll back the entire transaction.
- A newer fixture snapshot never loads an older qualification batch.
- A failed qualification attempt does not replace the newest complete batch for
  the same snapshot.

`internal/qualification/refresher_test.go`

- A complete snapshot writes exactly `teams * 3` rows.
- The same snapshot/rules pair is memoized without rerunning the evaluator.
- Changed fixtures or rules version force recalculation.
- Incomplete schedules and invalid kickoff order create explicit unresolved
  rows and never call the solver.
- Budget exhaustion fills the unprocessed rows and still commits a complete
  batch.

`internal/syncer/syncer_test.go` and `internal/scheduler/scheduler_test.go`

- Qualification runs only after fixture commit succeeds.
- Qualification failure leaves fixture success and xG behavior unchanged and
  is observable as a warning.
- Skipped/rate-limited fixture refreshes do not recalculate an existing snapshot.

`internal/app/handler_test.go`

- A matching persisted batch renders only the strongest badge.
- Shield and home-playoff badges include all implied achievements in accessible
  text.
- Unresolved, not-clinched, incomplete, pending, stale-snapshot, and
  wrong-rules-version data render no badge.
- A season page request never invokes a calculator; use a panic-on-call fake to
  prove it.
- Existing forecast, schedule difficulty, base-path, and escaping behavior is
  unchanged.

### Benchmarks and checked-in evidence

Do not benchmark against the developer's mutable `data/` database. Add a small
normalized, checked-in fixture under `internal/clinching/testdata/` from at
least one completed real regular season. Include source season/stage and a hash
in a README beside it. Replay that season at several chronological cutoffs by
revealing only results known at each cutoff while retaining the full fixture
inventory.

Add benchmarks for approximately 25%, 50%, 75%, 90%, and 97% completion, plus
the current 2026 cached snapshot if it is captured as a second immutable test
fixture. Report allocations and these domain diagnostics for all teams and all
three achievements: bounds-conclusive count, points-solver count, unresolved
tiebreak count, reduced teams/fixtures, visited states, memo hits, and total
runtime. Add `cmd/clinchingbench` only if a normal Go benchmark cannot emit the
human-readable summary cleanly.

Write the measured output and machine/Go version to
`docs/clinching-benchmark-v1.md`. Record the first replay cutoff at which each
achievement has a conclusive result. The five-second default budget is accepted
only if these checked-in runs finish a full snapshot comfortably below it on a
typical development machine; otherwise optimize or revise the documented
budget before enabling badges.

### Implementation order and stop conditions

Implement in this order. Each step is a reviewable checkpoint:

1. Add competition rules and update app/forecast callers to consume them; no
   clinching behavior changes yet.
2. Add the new domain types, fixed-result validation, and completed-season
   accessible-tiebreak path while retaining the old oracle.
3. Add exhaustive outcome helpers in tests, then implement bounds, one-component
   points search, memoization, witnesses, and disconnected components.
4. Add conservative tiebreak-frontier classification and no-help prefixes.
5. Add deterministic snapshot hashing, migration 4, cache DTOs, transactional
   replacement, and round-trip tests.
6. Add the qualification refresher and sync warning integration. At this point
   calculations happen after refresh but are not displayed.
7. Replace request-time Phase 5 calls with persisted lookup and add the strongest
   accessible standings badge.
8. Add cache-status visibility, real-snapshot benchmarks, evidence, README
   updates, and confirm that the Phase 5 oracle is referenced only by tests.

Stop and amend this plan instead of improvising if any of these occurs:

- The points solver disagrees with exhaustive enumeration on any generated
  schedule.
- A proposed tiebreak optimization needs an arbitrary maximum score.
- The full real-snapshot batch cannot meet the documented compute budget after
  reduction and profiling.
- ASA fixture inventory cannot distinguish safely unfinished fixtures from
  abandoned/cancelled fixtures.
- The official home-playoff or playoff cutoff differs from the configured
  season rules.

Before marking Phase 12 complete, run `go test ./...`, `go vet ./...`,
`go test -race ./internal/clinching ./internal/qualification ./internal/cache`,
and the real-snapshot benchmark command. Update the README current checkpoint
only after the persisted badges, no-help data, cache status, and benchmark
evidence all satisfy the exit criteria.
