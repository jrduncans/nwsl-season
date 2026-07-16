# Phase 13: clinching scenarios

## Status

Implemented. The implementation packet below records the build contract for
the current feature and remains useful when revisiting its proof or persistence
design.

The live page only reads the exact current snapshot. The older optional
`LatestScenario` fallback described below was intentionally not retained,
because presenting a stale proof as current would be misleading.

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
reduce an outcome-only claim to one representative scoreline for this purpose.

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
- Forecast probability, visitor assumptions, and mathematical clinching claims
  remain distinct in language and presentation.

## Locked implementation packet

This packet removes the remaining design choices so the phase can be built
mechanically. Implement it in the numbered order near the end of this document
and keep the test suite green after every slice. The Phase 12 evaluator remains
the authority for qualification; Phase 13 only defines a slate, explores fixed
outcomes, minimizes certified clauses, persists them, and presents them.

### Decisions fixed for the first release

- Add a new `internal/scenarios` package. It owns slate definition, exact slate
  search, clause minimization, and the machine-readable result. It imports
  `clinching`, `competition`, and `standings`; it does not import `cache` or
  `app`.
- Keep the Phase 12 conservative tiebreak boundary. Publish an outcome-only
  clause only when every oracle probe needed by that clause returns `clinched`
  through `cheap_bound` or `points_optimization`. Do not use representative
  scores, infer a score cap, or publish a clause from an `unresolved` result.
- Search the complete three-outcome slate exactly. The normal eight-fixture
  case is 6,561 assignments. Support at most ten fixtures in the first release;
  a larger derived slate is an explicit unresolved result, never silently
  truncated or divided into smaller slates.
- Prefer a reliable matchday grouping. When matchday metadata is not reliable,
  use the fixed 120-hour kickoff window defined below. Do not use locale weeks,
  a guessed round number, or the server's current calendar date.
- A slate cutoff is a fixture-inclusion cutoff, not an estimate that a game is
  final. UI copy must say that the named fixtures are included by scheduled
  kickoff and that the guarantee applies once their results are final.
- Persist one all-or-nothing batch for every team and configured achievement.
  Ordinary HTTP requests only read completed batches; they never construct a
  slate or invoke an evaluator.
- Keep all certified clauses in storage. Presentation may show the clearest
  three initially and place the remainder in a native `<details>` disclosure.
  The display limit must not alter stored results or assignment counts.
- Render team and fixture names only in the app layer. Persist team IDs, fixture
  IDs, outcomes, timestamps, enums, counts, and diagnostics—not prose.
- Add a dedicated `/seasons/{season}/clinching` page and a compact season-page
  overview only when the current exact batch contains an opportunity. The
  fixtures page may link to the dedicated page but does not repeat clauses.
- Keep forecast probabilities and Forecast Lab assumptions out of the scenario
  domain, persistence schema, and condition prose.

### Package boundaries and shared Phase 12 evaluator

The Phase 13 search must not call the current top-level `clinching.Evaluate`
thousands of times because that wrapper also computes a no-help path and
revalidates the complete snapshot on each call. Refactor without changing its
public behavior.

Add a reusable, sequential evaluator in `internal/clinching/evaluator.go`:

```go
type Evaluator struct {
    // private immutable snapshot indexes and conclusive-result caches
}

func NewEvaluator(
    teams []standings.Team,
    games []standings.Game,
    fixtureOrder []string,
) (*Evaluator, error)

func (e *Evaluator) Evaluate(
    ctx context.Context,
    targetTeamID string,
    achievement competition.Achievement,
    fixed []FixedResult,
) (AchievementResult, error)

func (e *Evaluator) EvaluateStatus(
    ctx context.Context,
    targetTeamID string,
    achievement competition.Achievement,
    fixed []FixedResult,
) (AchievementResult, error)
```

`EvaluateStatus` runs only the Phase 12 qualification proof. `Evaluate` calls
`EvaluateStatus` and then adds the no-help path. Keep the existing package-level
`Evaluate(ctx, Request)` as a compatibility wrapper which constructs an
`Evaluator` and calls its `Evaluate` method. Existing qualification behavior and
tests must not change.

Compile and validate these values once in `NewEvaluator`:

- defensive copies of teams, games, and fixture order;
- team and game maps, sorted team IDs, and unfinished fixture IDs;
- points from completed games;
- the remaining-fixture topology and each team's incident fixtures; and
- the validation that Phase 12 currently performs for every request.

Per call, validate only the target, achievement, and fixed outcomes. Normalize
fixed results by game ID before building a key. Cache only conclusive
`clinched` and `not_clinched` status results. Never cache a context cancellation,
compute-budget result, or other transient error. The evaluator is deliberately
not safe for concurrent use in this release; the refreshers use it sequentially.

Use a status-cache key containing target ID, achievement ID and `TopK`, the set
of fixed fixture IDs, and the fixed points delta for every sorted team. For the
points-only Phase 13 contract, two fixed assignments with the same removed
fixtures and team points are equivalent even if the literal outcomes that
created those points differ. Cache the mathematical status only; clauses retain
their original fixture outcomes. A cached not-clinched entry stores the solver's
unfixed-fixture suffix, then reconstructs `BlockingWitness` from the caller's
current fixed outcomes; it must never return another probe's literal witness.
If safe reconstruction is not possible for a method, use the full literal key
for that entry. Reuse the current threshold component solver and memo tables
when the normalized threshold query is identical.

Update `internal/qualification/refresher.go` to construct one evaluator per
snapshot and use it for all team/achievement results and no-help probes. This is
a performance refactor, not a Phase 12 result change. Add regression tests that
the old package-level call and the reusable evaluator return identical values.

Keep the dependency graph acyclic:

- `internal/clinching` owns the fixed-results qualification oracle.
- `internal/scenarios` owns slate conditions and calls `clinching.Evaluator`.
- `internal/qualification` remains the whole-snapshot Phase 12 refresher.
- `internal/scenariorefresh` maps cache data, invokes `scenarios`, and persists
  a scenario batch.
- `internal/cache` owns SQLite DTOs and never calls either proof engine.
- `internal/app` maps persisted values to names and prose.

### Slate domain contract

Add `internal/scenarios/types.go`. Use string enums and validate them at package
and cache boundaries:

```go
const DefinitionVersion = "next-slate-v1"

type SlateState string
const (
    SlateReady      SlateState = "ready"
    SlateNoUpcoming SlateState = "no_upcoming_fixtures"
    SlateUnavailable SlateState = "unavailable"
)

type SlateSource string
const (
    SourceMatchday      SlateSource = "matchday"
    SourceKickoffWindow SlateSource = "kickoff_window"
)

type Slate struct {
    ID                string
    DefinitionVersion string
    State             SlateState
    Source            SlateSource
    Matchday          int       // zero unless SourceMatchday
    StartsAtUTC       time.Time // earliest included scheduled kickoff
    LatestKickoffUTC  time.Time
    CutoffUTC         time.Time // inclusive for matchday, exclusive for window
    FixtureIDs        []string  // scheduled kickoff, then fixture ID
    Reason            string
}
```

An unavailable or no-upcoming slate has non-nil empty `FixtureIDs`. A ready
slate has a nonempty ID, version, source, ordered fixture list, and nonzero
times. `SourceMatchday` requires a positive matchday. `SourceKickoffWindow`
requires matchday zero. Times are stored and serialized in UTC.

Implement `DefineSlate(games []ScheduledGame) (Slate, error)` in
`internal/scenarios/slate.go`. `ScheduledGame` is a small scenario-domain input
containing fixture ID, status, home and away IDs, nullable scores, parsed
kickoff, and nullable matchday. Do not import `cache.Game` into this package.

The algorithm is exact and deterministic:

1. Reject duplicate or empty IDs, unknown/zero kickoffs, empty team IDs, and a
   home team equal to the away team. Only `FullTime` with scores and `PreMatch`
   without scores are safe; the refresher handles any other status as an
   unavailable slate before calling this function.
2. Sort all `PreMatch` fixtures by kickoff UTC and then fixture ID. If none
   remain, return `SlateNoUpcoming`.
3. Let the earliest pending fixture be the seed. Its matchday is reliable only
   when it is positive, every season fixture carrying that matchday has a valid
   kickoff, and no pending fixture from another matchday kicks off between the
   earliest and latest pending fixtures of the seed's matchday, inclusive.
4. For a reliable matchday, include every still-pending fixture with that
   matchday. Completed fixtures from the same matchday are already part of the
   base standings and are not added to the slate. Set `StartsAtUTC` and
   `LatestKickoffUTC` from the included fixtures and set `CutoffUTC` equal to
   `LatestKickoffUTC`.
5. Otherwise, set the window start to the seed kickoff and the cutoff to exactly
   120 hours later. Include every pending fixture with `kickoff >= start` and
   `kickoff < cutoff`, regardless of matchday. Set `LatestKickoffUTC` to the
   latest included kickoff.
6. Calculate `Slate.ID` as lower-case SHA-256 over a length-prefixed canonical
   encoding of definition version, state, source, matchday, all three times, and
   the ordered fixture IDs. Do not use JSON as the hash input.

The matchday crossing check is important: a rescheduled fixture from a
different round cannot be silently skipped even though it kicks off before the
selected matchday is over. Falling back to the window makes the inclusion rule
explicit and safe.

The refresh layer first verifies the complete Phase 12 schedule inventory. It
returns `SlateUnavailable` with a reason for an incomplete inventory, unsafe
status, invalid kickoff, missing matching qualification batch, or unknown
rules. It must not call the search in those states.

### Scenario result contract

Add these types to `internal/scenarios/types.go`:

```go
type OpportunityState string
const (
    OpportunityAlreadyClinched   OpportunityState = "already_clinched"
    OpportunityCanClinch         OpportunityState = "can_clinch"
    OpportunityCannotClinch      OpportunityState = "cannot_clinch"
    OpportunityTiebreakDependent OpportunityState = "tiebreak_dependent"
    OpportunityUnresolved        OpportunityState = "unresolved"
)

type FixtureCondition struct {
    GameID          string
    AllowedOutcomes []clinching.Outcome
}

type Clause struct {
    Conditions             []FixtureCondition
    RepresentedAssignments int
    ProofMethods           []clinching.ProofMethod
}

type Diagnostics struct {
    SearchNodes            int
    OracleCalls            int
    OracleCacheHits        int
    GuaranteePrunes        int
    OpportunityPrunes      int
    MinimizationProbes     int
    CombinationProbes      int
    InitialClauses         int
    MinimalClauses         int
    VisitedComplete        int
    ElapsedMicroseconds    int64
}

type Result struct {
    TeamID                 string
    Achievement            competition.AchievementID
    TopK                   int
    State                  OpportunityState
    AlreadyClinched        bool
    CanClinch              bool
    Clauses                []Clause
    Necessary              []FixtureCondition
    ProofMethods           []clinching.ProofMethod
    Limitation             string
    TotalAssignments       int
    CertifiedAssignments   int
    UnresolvedAssignments  int
    Diagnostics            Diagnostics
}
```

`FixtureCondition.AllowedOutcomes` is nonempty, unique, and stored in canonical
order `home_win`, `draw`, `away_win`. It may contain one or two values. All
three means the fixture is unconstrained and the condition must be omitted.
Conditions are sorted by the slate's presentation order, then game ID. Clauses
are sorted as described below. Each clause's proof methods are the canonical,
deduplicated union of its certification probes, and result proof methods are
the corresponding union across clauses. Empty slices are `[]`, never `null`.

Validate these invariants:

- `already_clinched` has `AlreadyClinched=true`, `CanClinch=false`, and no
  clauses or necessary conditions.
- `can_clinch` has `CanClinch=true`, at least one clause, and a positive
  certified-assignment count.
- `cannot_clinch` has neither boolean set, zero certified and unresolved
  assignments, and represents a completed exhaustive search.
- `tiebreak_dependent` has no certified clause and at least one assignment whose
  only remaining classification was a Phase 12 score-tiebreak limitation.
- `unresolved` is used for missing baseline data, unsafe slate data, a slate
  larger than ten fixtures, compute budget, or infrastructure failure. It does
  not claim exhaustive assignment counts.
- `CertifiedAssignments + UnresolvedAssignments <= TotalAssignments`.
- Every condition references a fixture in the persisted slate.

`TotalAssignments` is `3^len(slate.FixtureIDs)` for a ready slate. For a
clause, `RepresentedAssignments` is the product of the allowed-outcome count for
each named condition and three for each unnamed slate fixture. Result-level
`CertifiedAssignments` is the size of the union of all clause assignment sets,
not the sum of clause counts, because clauses can overlap.

Only populate `Necessary` after a completed exhaustive search with zero
unresolved assignments. For each fixture, take the union of the outcomes used
by the certified complete assignments. If that observed set is a proper subset
of the three outcomes, store it as necessary. If any assignment is
tiebreak-unresolved, leave `Necessary` empty; otherwise the UI could incorrectly
say “needs” when an unproved clinching path violates the condition.

### Search entry point and baseline handling

Add `internal/scenarios/search.go`:

```go
type Request struct {
    Evaluator       *clinching.Evaluator
    Teams           []standings.Team
    Games           []standings.Game
    Slate           Slate
    TargetTeamID    string
    Achievement     competition.Achievement
    Baseline        clinching.AchievementResult
    MaxSlateFixtures int
}

func Generate(ctx context.Context, request Request) (Result, error)
```

The refresher supplies the Phase 12 result for the same fixture snapshot and
rules version as `Baseline`. Validate that team, achievement, and `TopK` match
the request.

Handle the baseline before searching:

- A baseline `clinched` result becomes `already_clinched`. Do not search.
- A baseline `unresolved` result becomes `unresolved`. A team might already be
  clinched, so “can clinch this slate” would be misleading.
- A baseline `not_clinched` result may be searched.
- A non-ready slate becomes `unresolved`, except `SlateNoUpcoming`, which
  becomes `cannot_clinch` for a baseline not-clinched team with zero total
  assignments.
- More than `MaxSlateFixtures` (default ten) becomes `unresolved` with the
  exact fixture count in the limitation. Do not search a prefix.

Search teams in official-total table order and achievements from easiest to
guarantee to hardest (`TopK` descending) so the useful playoff paths are most
likely to finish within a batch budget. Each result is still independently
proved and persisted in canonical team-ID/rules-achievement order.

### Opportunity bound and exact decision tree

Implement the safe opportunity bound in `internal/scenarios/bounds.go`. For one
partial slate assignment:

1. Start from points in completed games and apply the partial fixed outcomes.
2. Give the target three points for every unassigned slate fixture involving
   it. This is the target's maximum points at the cutoff for this branch.
3. Count opponents whose already-fixed points are strictly greater than that
   ceiling. Points cannot decrease.
4. If the count is at least `TopK`, no completion of this branch can create a
   points-certified clinch. Prune it.

This is an impossibility bound only. An inconclusive result must continue to the
oracle; it is never persisted as a scenario or rendered as a magic number.

Order slate decisions for performance without changing the result:

1. fixtures involving the target;
2. fixtures with both endpoints currently within two official-table positions
   of the achievement cutoff;
3. fixtures with one such endpoint;
4. smaller absolute current-points distance from the target; and
5. kickoff UTC, then fixture ID.

The search walk keeps a fixed-outcome map and the number of still-unassigned
slate fixtures. At every node:

1. Check `ctx.Err()` before doing work.
2. Apply the opportunity bound. If it rejects the branch, add `3^remaining` to
   the definitively non-clinching coverage and increment `OpportunityPrunes`.
3. Normalize the partial fixed assignment and call
   `Evaluator.EvaluateStatus`. The Phase 12 evaluator still universally
   quantifies every unfixed slate fixture and every later regular-season
   fixture.
4. If the result is `clinched` by `cheap_bound` or `points_optimization`, emit
   the current partial exact clause, add `3^remaining` certified assignments,
   and stop below this node.
5. If fixtures remain, branch in canonical outcome order. Try the target's win
   first for a target fixture. For other fixtures, try the result favoring the
   endpoint closer to the cutoff, then draw, then the opposite result. Outcome
   order affects runtime only.
6. At a leaf, classify `not_clinched` as definitive failure. Classify
   `unproved_score_tiebreak` or `missing_disciplinary_rule` as an unresolved
   assignment. A compute-budget method or context expiry aborts the entire
   team/achievement result.

Use base-3 assignment indexes in slate fixture order and a bit set to track
certified and tiebreak-unresolved complete assignments. A sufficient partial
node sets the contiguous or enumerated indexes represented by its cube. The bit
set makes union counts, necessary outcomes, and test comparison exact.

Memoize oracle calls by the evaluator's normalized status key and separately by
the literal fixed-result key used by minimization. A cache hit must reproduce
the same status and proof method. Do not memoize transient cancellation. A
normal eight-game tree should share status work; it must not launch 6,561
freshly compiled full-season searches.

If the context expires anywhere in search, minimization, or combination,
discard all clauses for that result and return `OpportunityUnresolved` with a
compute-budget limitation and partial diagnostics. Never present a partial
clause list as “all minimal paths.”

### Clause minimization, dominance, and combined outcomes

Implement this in `internal/scenarios/minimize.go`. Keep machine conditions
separate from prose throughout.

Start with the exact partial clauses emitted by the tree. For each clause,
explore every one-condition removal, not only the first successful greedy path:

1. Remove one fixture condition.
2. Certify the smaller clause by converting its singleton conditions to fixed
   results and calling `Evaluator.EvaluateStatus`.
3. If the smaller clause is points-clinched, recurse from it.
4. If no one-condition removal is sufficient, retain the clause.
5. Memoize clause certification by its canonical literal key so paths that
   reach the same subset do not repeat proof work.

This recursive deletion is bounded by the slate size and finds every
irreducible subset reachable from each emitted clause. Deduplicate exact
clauses after each starting clause.

Then combine alternative outcomes. Two clauses are merge candidates only when
all fixture conditions except one are identical and the differing condition
refers to the same fixture. Union that fixture's allowed outcomes and certify
the candidate clause by expanding the allowed outcomes' Cartesian product into
fixed-result probes. An unspecified slate fixture stays universally quantified
inside the Phase 12 oracle. Retain a merge only when every expansion is
points-clinched. Iterate pairwise merging to a fixed point.

Examples:

- `{draw}` plus `{away_win}` can become `{draw, away_win}`, rendered from the
  home team's perspective as “does not win.”
- `{home_win}` plus `{draw}` can become `{home_win, draw}`, rendered as “does
  not lose.”
- A union of all three outcomes removes that fixture condition entirely and is
  accepted only if the condition-free clause is certified.

After merging, remove semantic dominance. Clause A subsumes clause B when every
complete assignment matching B also matches A: for every fixture constrained
by A, B constrains the same fixture to an equal or narrower allowed-outcome
set. If A strictly subsumes B, discard B. Re-run the one-condition minimality
check on merged clauses as a defensive assertion.

Sort the final clauses deterministically by:

1. fewer fixture conditions;
2. more represented complete assignments—broader clauses first when condition
   counts tie;
3. a clause containing the target's fixture before a help-only clause;
4. slate fixture order; and
5. canonical allowed-outcome strings.

The ordering is presentation preference only. Persist all non-dominated
clauses. Rebuild the certified-assignment bit set from the final clauses and
assert that its union equals the tree's certified bit set. A mismatch is an
implementation error, not a limitation to hide.

When no certified assignments exist:

- if at least one leaf is tiebreak-unresolved, return
  `OpportunityTiebreakDependent` and explain that no score-independent clause
  can be published;
- if all assignments are definitive failures, return
  `OpportunityCannotClinch`;
- if the calculation did not finish, return `OpportunityUnresolved`.

When certified clauses and tiebreak-unresolved leaves both exist, return
`OpportunityCanClinch` with the certified clauses, leave `Necessary` empty, and
set a limitation saying that additional tiebreak-dependent paths were withheld.

### Outcome-only and tiebreak safety

Treat `FixedResult` as a points outcome, never as a claimed 1-0, 0-0, or 0-1
score. Canonical witness scores inside Phase 12 are serialization details and
must not flow into Phase 13 types or prose.

A Phase 13 clause is publishable only from the two points-conclusive methods:

```go
func publishable(v clinching.AchievementResult) bool {
    return v.Status == clinching.Clinched &&
        (v.Method == clinching.ProofCheapBound ||
         v.Method == clinching.ProofPointsOptimization)
}
```

An already-clinched baseline may use `ProofAccessibleTiebreak` or
`ProofImplied`, because it describes actual status before the slate rather than
an outcome-only future clause. `ProofUnprovedScoreTiebreak`,
`ProofMissingDisciplinary`, and `ProofComputeBudget` never certify a clause.

Do not implement score-margin scenarios in this release. The structured
limitation must distinguish:

- `unproved_score_tiebreak`: an outcome assignment reaches a goal-based
  frontier that Phase 12 intentionally does not prove;
- `missing_disciplinary_rule`: the unavailable official rule is decisive; and
- `compute_budget`: the exact search did not finish.

The app may say “A clinch may depend on score or unavailable tiebreak data; no
outcome-only path is published.” It must not convert that state to “cannot
clinch.”

### Snapshot identity update and SQLite migration 5

Phase 13 uses `cache.Game.Matchday`, but the current fixture snapshot hash does
not include it. Change `cache.FixtureSnapshotID` to begin its canonical input
with `fixture-snapshot-v2` and include nullable matchday after kickoff for every
game. Keep every field already hashed. This deliberately changes the hash once
after deployment and makes a matchday-only correction trigger qualification
and scenario recalculation. Add tests proving that matchday validity and value
affect the hash and that presentation metadata still does not.

Increase `schemaVersion` in `internal/cache/cache.go` from 4 to 5. Do not edit
the first four migrations. Migration 5 creates:

```sql
CREATE TABLE scenario_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    fixture_snapshot_id TEXT NOT NULL,
    qualification_run_id INTEGER NOT NULL
        REFERENCES qualification_runs(id),
    source_sync_run_id INTEGER NOT NULL REFERENCES sync_runs(id),
    season TEXT NOT NULL,
    stage TEXT NOT NULL,
    rules_version TEXT NOT NULL,
    definition_version TEXT NOT NULL,
    slate_id TEXT NOT NULL,
    slate_state TEXT NOT NULL
        CHECK (slate_state IN
            ('ready', 'no_upcoming_fixtures', 'unavailable')),
    slate_source TEXT NOT NULL,
    matchday INTEGER NOT NULL,
    starts_at_utc TEXT NOT NULL,
    latest_kickoff_utc TEXT NOT NULL,
    cutoff_utc TEXT NOT NULL,
    fixture_ids_json TEXT NOT NULL,
    slate_reason TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('complete', 'failure')),
    error_summary TEXT NOT NULL,
    expected_results INTEGER NOT NULL,
    written_results INTEGER NOT NULL
);

CREATE INDEX scenario_runs_exact_idx
ON scenario_runs (
    fixture_snapshot_id, rules_version, definition_version, finished_at
);

CREATE INDEX scenario_runs_latest_idx
ON scenario_runs (season, stage, rules_version, finished_at);

CREATE TABLE scenario_results (
    scenario_run_id INTEGER NOT NULL
        REFERENCES scenario_runs(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL REFERENCES teams(asa_team_id),
    achievement TEXT NOT NULL,
    top_k INTEGER NOT NULL,
    opportunity_state TEXT NOT NULL CHECK (opportunity_state IN
        ('already_clinched', 'can_clinch', 'cannot_clinch',
         'tiebreak_dependent', 'unresolved')),
    already_clinched INTEGER NOT NULL CHECK (already_clinched IN (0, 1)),
    can_clinch INTEGER NOT NULL CHECK (can_clinch IN (0, 1)),
    clauses_json TEXT NOT NULL,
    necessary_json TEXT NOT NULL,
    proof_methods_json TEXT NOT NULL,
    limitation TEXT NOT NULL,
    total_assignments INTEGER NOT NULL,
    certified_assignments INTEGER NOT NULL,
    unresolved_assignments INTEGER NOT NULL,
    diagnostics_json TEXT NOT NULL,
    PRIMARY KEY (scenario_run_id, team_id, achievement)
);
```

For non-ready slates, store empty strings for the three timestamps and source,
zero matchday, and `[]` fixture IDs. For ready slates, all fields are required.
The cache layer parses and formats timestamps as RFC 3339 UTC.

Add cache DTOs named `ScenarioRun`, `ScenarioResult`, and `ScenarioSnapshot`.
Do not duplicate the scenario enums in `cache`; fields use `scenarios` types.
Add these methods:

```go
ScenarioForSnapshot(
    ctx context.Context,
    snapshotID, rulesVersion, definitionVersion string,
) (ScenarioSnapshot, bool, error)

LatestScenario(
    ctx context.Context,
    season, stage, rulesVersion, definitionVersion string,
) (ScenarioSnapshot, bool, error)

ReplaceScenario(
    ctx context.Context,
    run ScenarioRun,
    rows []ScenarioResult,
) (ScenarioSnapshot, error)

RecordScenarioFailure(
    ctx context.Context,
    run ScenarioRun,
    cause error,
) error
```

`ScenarioForSnapshot` returns the newest complete run for the exact fixture
snapshot, rules version, and definition version. `LatestScenario` is used only
for the explicitly stale/recalculating view described below; it never supplies
standings badges or a current overview card.

`ReplaceScenario` validates the slate, all scenario enums and invariants,
unique `(team, achievement)` rows, known slate fixture IDs, nonnegative counts,
valid JSON, `qualification_run_id` matching the same snapshot/rules pair, and
`expected_results == written_results == len(rows)`. Decode JSON and run domain
validation before opening the transaction. Insert the run and all rows in one
transaction. A completed batch can contain unresolved result rows.

All JSON uses `encoding/json` with `[]` for empty clauses, conditions, outcome
sets, proof methods, and fixture IDs. Tests compare round-tripped structures,
not raw JSON key order.

### Scenario refresher and post-refresh orchestration

Add `internal/scenariorefresh/refresher.go` with a narrow store interface for
qualification lookup and scenario persistence. The request is the same exact
normalized teams, games, and successful sync run already committed by
`syncer.Service`.

The refresher performs these steps:

1. Validate rules and require a nonempty fixture snapshot ID.
2. Return immediately if `ScenarioForSnapshot` already has a complete batch for
   the snapshot, rules version, and `scenarios.DefinitionVersion`.
3. Load the exact matching Phase 12 qualification batch. If it is missing or
   incomplete, return a typed prerequisite error; do not use an older batch.
4. Reuse the Phase 12 complete-inventory and safe-status checks by moving the
   cache-to-domain mapping helpers from `internal/qualification` into an
   unexported shared `internal/seasoninput` package. Do not copy subtly
   different inventory rules into the scenario refresher.
5. Define the slate. A missing full inventory or unsafe status suppresses
   generation. The HTTP layer can display the schedule-unavailable state even
   when no scenario batch is written.
6. Construct one `clinching.Evaluator` for the snapshot. Map baseline
   qualification rows by `(team, achievement)` and require exactly one for
   every configured pair.
7. Use one scenario calculation context with the configured total budget.
   Evaluate teams in official-total table order and achievements with `TopK`
   descending. Once the budget expires, fill every unprocessed pair with an
   unresolved compute-budget result so the persisted batch remains complete.
   A result that expired mid-search is also unresolved and contains no clauses.
8. Sort output by team ID and the configured achievement order, then persist in
   a short cleanup context even if the calculation context expired.
9. Record infrastructure or transaction errors as failed scenario runs. A
   failed run does not replace the newest complete run.

Add `NWSL_SCENARIO_BUDGET` to `internal/config.Config` and the README
configuration table. Start with a 30-second default, then confirm or revise it
using the checked-in benchmark evidence before enabling the UI.

Extend `syncer.Service` with a second post-refresh interface and warning:

```go
type ScenarioRefresher interface {
    Refresh(context.Context, cache.SyncRun, []cache.Team, []cache.Game) error
}

type Service struct {
    // existing fields
    Qualification       QualificationRefresher
    Scenarios           ScenarioRefresher
    QualificationTimeout time.Duration
    ScenarioTimeout      time.Duration
}
```

After `ReplaceSeason` succeeds, run qualification first. Only if qualification
succeeds, run scenarios. Give each a fresh timeout from
`context.WithoutCancel(ctx)` so ASA fetch time does not consume a derived-data
budget. Default the service timeouts to the same five-second and 30-second
values when tests omit them. Keep the refresher's own budget as a defensive
inner limit.

Add `ScenarioError string` to `cache.SyncRun` as an in-memory warning field like
`QualificationError`; it does not require a database column. A scenario failure
must not relabel or roll back fixture, qualification, or xG success. Log it from
the scheduler and `cmd/server`, print it from `cmd/sync`, and wire the same
scenario refresher in both commands. A skipped fixture refresh does not rerun
either derived calculation.

### Read path, freshness, and stale results

Add exact scenario lookup to the app's optional store surface. The season and
clinching handlers first request the current fixture snapshot, current rules
version, and current scenario definition version. They never invoke
`DefineSlate`, `Generate`, or `clinching.Evaluator`.

For the current fixture snapshot:

- a matching complete scenario batch is current;
- no matching batch while the schedule is complete is “recalculation pending”;
- an incomplete schedule or unsafe fixture state is “unavailable”; and
- an unknown rules pair has no scenarios.

The dedicated page may temporarily use `LatestScenario` only when no current
batch exists and every persisted slate fixture still exists in the current
schedule with the same home and away teams and remains `PreMatch`. Mark the
whole result as stale, show “Recalculation pending,” and display the persisted
cutoff and fixture list before any clauses. If a named fixture disappeared,
changed teams, or became final, do not show stale clauses; show only the pending
state. The season overview never shows stale opportunities.

This rule preserves the Phase 13 allowance to show prior work briefly without
presenting a clause whose named fixture is already invalid. Current qualification
badges continue to require an exact Phase 12 snapshot and never use stale data.
Already-clinched status and no-help paths on the page also come from the current
exact Phase 12 batch. If that batch is still pending, omit those sections rather
than pairing old no-help data with current standings; stale clauses keep their
separate old-cutoff label.

Extend `/cache/status` with a `scenarios` object containing fixture snapshot ID,
rules version, definition version, slate ID/state/cutoff, calculation outcome,
finished time, written row count, and `matches_current`. Do not expose clause
JSON or team-level proof details there.

### Presentation model and prose

Add `GET /seasons/{season}/clinching` in `internal/app/handler.go` and
`internal/app/templates/clinching.html`. Add `ClinchingPath` to season, fixtures,
schedule-difficulty, xG, and forecast page navigation where appropriate.

The dedicated page is server-rendered and contains:

1. a title and deterministic-proof explanation that is visually distinct from
   Forecast Lab;
2. current/stale/pending/unavailable state;
3. slate source, matchday when present, scheduled date range, inclusion cutoff,
   fixture list, fixture IDs in accessible detail, and data freshness;
4. already-clinched achievements;
5. next-slate opportunities grouped by configured achievement order, then
   official-table order;
6. each team's complete stored minimal clauses, with the first three visible
   and the rest in `<details>`;
7. the Phase 12 no-help path from the exact qualification batch; and
8. tiebreak, discipline, compute, and incomplete-data limitations.

Build prose only from `FixtureCondition`, current team metadata, and current
fixture metadata. Add pure view helpers with table-driven tests. Use these
rules:

- If a condition is on the target's only slate fixture, a singleton target win
  may render as “a win.” If the target has two slate fixtures, name the
  opponent: “a win against Gotham.”
- For a non-target fixture, choose the endpoint closer to the achievement
  cutoff in current official-table order as the grammatical subject; break a
  tie by team ID. Translate allowed home outcomes into that team's perspective.
- A singleton win/loss names the opponent unless the subject has exactly one
  fixture in the displayed slate. A draw names both clubs.
- Exactly `{draw, loss}` from the subject's perspective is “does not win.”
  Exactly `{win, draw}` is “does not lose.” No other outcome set receives a
  combined phrase.
- Join conditions with commas and a final “and.” Start every clause with
  “Clinches [achievement] with …”. Never use “needs” for a sufficient clause.
- Use “needs” only for `Necessary` conditions, which are populated solely after
  a complete search without unresolved assignments. Prefer the explicit copy
  “Every clinching path requires …” in the first release.
- A help-only clause is valid and must name its relevant fixtures; do not force
  the target's result into the sentence.

Clause ordering comes from the domain result. Prose generation must not reorder
or merge machine conditions. Escape all team names through `html/template`.
Fixture IDs may appear in a disclosure for auditing but should not replace club
names in the visible sentence.

On the season overview, show at most one opportunity per team. Choose the
strongest achievement (`TopK` smallest) among `OpportunityCanClinch` results and
show its first clause plus a link to all paths. Render the section only when at
least one current exact opportunity exists. Keep the existing strongest
achieved badge in the standings row and link that badge to the clinching page.

On the fixtures page, add one link beside a fixture group when it intersects the
current slate. Do not render condition lists beside matches. Add no JavaScript
for the initial release; native links and `<details>` satisfy the complete
workflow. Use text and borders/icons as well as color for slate membership and
proof state.

### Required tests

Add tests in the same implementation slice as the production code. Use invented
small seasons for proof properties and cache fixtures for persistence and HTTP
tests.

`internal/clinching/evaluator_test.go`

- The reusable evaluator and package-level `Evaluate` return identical status,
  witnesses, diagnostics shape, and no-help values.
- Snapshot validation occurs at construction and per-probe fixed validation
  still rejects duplicates, completed fixtures, unknown IDs, and invalid
  outcomes.
- Equal normalized fixed points/set keys reuse a conclusive status result.
- Canceled and compute-budget results are not cached.
- Reusing the evaluator across all teams and achievements does not leak target
  or threshold state.

`internal/scenarios/slate_test.go`

- A reliable seed matchday includes every remaining fixture in that matchday,
  including a delayed kickoff outside one calendar week.
- Completed fixtures from a partially played matchday are excluded while its
  remaining fixtures stay in the slate.
- A different-matchday fixture crossing the candidate date range forces the
  kickoff-window fallback.
- Missing, zero, or inconsistent matchday data uses the 120-hour window.
- The window includes its start, excludes its cutoff, and uses kickoff then ID
  ordering.
- No remaining fixtures returns `SlateNoUpcoming` with empty slices.
- Invalid kickoff/status/team data returns unavailable or an error as specified.
- Input ordering does not change the slate or ID; source, matchday, cutoff, or
  fixture membership changes the ID.

`internal/scenarios/bounds_test.go`

- `TopK` opponents already above the target's best slate points prune the root.
- An unassigned target fixture contributes at most three points.
- A draw and a loss already fixed on the target reduce the ceiling correctly.
- Opponent future points are never subtracted.
- An inconclusive bound never claims a clinch.

`internal/scenarios/search_test.go`

- A partial target-win condition stops below the node and represents every
  completion of unassigned slate fixtures.
- A target win plus one rival result produces the expected two-condition path.
- Help-only paths are retained.
- Already-clinched, baseline-unresolved, no-upcoming, oversized-slate, context
  cancellation, and no-path states map to the correct enums and booleans.
- Score-tiebreak frontier leaves produce `tiebreak_dependent`, not
  `cannot_clinch` or a clause.
- Certified and tiebreak-dependent leaves together retain only certified
  clauses and suppress necessary-language data.
- Fixture search ordering changes diagnostics at most; shuffled inputs produce
  byte-equivalent canonical clauses and assignment counts.
- Oracle cache hits occur in a scenario with equivalent fixed points states.

Add a test-only exhaustive reference which enumerates all `3^N` slate
assignments for tiny seasons and calls `Evaluator.EvaluateStatus` with every
complete assignment. For generated seasons of 3-6 teams and slates of 1-6
fixtures, assert:

- every complete assignment matched by every published clause is points-
  clinched;
- every certified reference assignment is covered by at least one final clause;
- no definitive failure or unresolved reference assignment is covered;
- `CertifiedAssignments` and `UnresolvedAssignments` match the reference;
- result state matches the exhaustive classification; and
- every stored necessary condition holds in every certified assignment.

`internal/scenarios/minimize_test.go`

- Removing any named fixture condition from any final clause makes at least one
  compatible assignment not points-clinched.
- Superset clauses are discarded.
- Duplicate clauses reached through different deletion paths are stored once.
- `{draw}` plus `{loss}` combines to “does not win” data only when both probes
  are sufficient.
- A failed alternative prevents combination.
- Three sufficient outcomes remove the fixture condition.
- Overlapping clauses use a union count rather than summed counts.
- Final clause coverage exactly equals initial tree coverage.
- Context expiry during minimization discards the partial result.

`internal/cache/cache_test.go`

- A version-4 database migrates to version 5 without changing qualification
  rows.
- Fixture snapshot v2 is reorder-stable, includes nullable matchday, and changes
  once from the previous format.
- Scenario replacement/load round-trips slate values, nested allowed outcomes,
  clauses, necessary conditions, proof methods, and diagnostics.
- Empty slices round-trip as empty, not nil.
- Duplicate/missing rows, invalid enums, unknown slate fixture references,
  malformed JSON, invalid assignment counts, a mismatched qualification run,
  or count mismatch rolls back the transaction.
- Exact lookup never returns another snapshot, rules version, or definition
  version.
- Latest lookup can return the prior complete batch but ignores failed runs.
- A failed replacement leaves the last complete batch readable.

`internal/scenariorefresh/refresher_test.go`

- A complete snapshot writes exactly one row for every qualification baseline
  row.
- The same snapshot/rules/definition triple is memoized.
- A matchday-only, kickoff, status, score, membership, rules-version, or
  definition-version change forces recalculation.
- The refresher requires the exact completed qualification batch and never uses
  the latest batch for another snapshot.
- Incomplete inventory, unsafe statuses, invalid kickoffs, and unknown rules do
  not call the search.
- Budget expiration fills all remaining rows as unresolved and commits a
  complete batch.
- An expired calculation context does not prevent the short persistence cleanup
  context.

`internal/syncer/syncer_test.go` and `internal/scheduler/scheduler_test.go`

- Scenarios run after successful qualification, never before it.
- Fixture or qualification failure suppresses scenarios.
- Scenario failure is an observable warning and leaves fixture,
  qualification, and xG success unchanged.
- Qualification and scenario refreshes receive separate fresh time budgets.
- A skipped fixture refresh does not rerun scenarios.

`internal/app/handler_test.go`

- `/seasons/2026/clinching` renders the current slate source, date range,
  cutoff, every fixture, current data time, already-clinched status, no-help
  path, and every stored clause without JavaScript.
- The season overview appears only for current exact `can_clinch` rows, chooses
  the strongest opportunity per team, and links to all paths.
- More than three clauses use an accessible native disclosure without omitting
  any clause from the HTML.
- “Does not win” and “does not lose” appear only for the exact two-outcome sets.
- Multiple target fixtures force opponent names; a unique fixture may use the
  shorter wording.
- Sufficient clauses never say “needs.” Necessary conditions can say “requires”
  only when the domain supplies `Necessary`.
- Tiebreak-dependent, disciplinary, compute-budget, incomplete, pending,
  no-upcoming, and oversized-slate states use distinct honest copy.
- A safe stale batch shows its old cutoff and recalculation banner. A stale
  batch with a missing, changed-team, or completed named fixture shows no old
  clauses.
- Forecast language remains explicitly probabilistic and the clinching page
  remains explicitly deterministic; neither page presents one as the other.
- Base-path redirects, escaping malicious team names, relative navigation, and
  no-calculator-on-request behavior remain covered.

### Benchmarks and checked-in evidence

Add `BenchmarkScenarioSlate` under `internal/scenarios` using immutable
normalized schedule fixtures checked into `internal/scenarios/testdata/`. Use
at least three chronological late-season snapshots: before any opportunity,
the first playoff-clinch opportunity, and a slate with Shield/home-playoff
paths. Retain the complete schedule inventory at every cutoff and reveal only
the results known then.

For every benchmark, report:

- slate fixture count and `3^N` complete assignments;
- teams rejected by the root opportunity bound;
- team/achievement searches performed;
- search nodes and both prune counts;
- oracle calls and cache hits;
- Phase 12 visited states and memo hits aggregated across probes;
- initial and minimal clause counts;
- certified and unresolved assignment counts;
- total duration and allocations.

Write one reproducible run, Go version, machine summary, source season/stage,
and testdata hashes to `docs/clinching-scenarios-benchmark-v1.md`. The
30-second default is acceptable only when a normal eight-fixture full batch
finishes comfortably below it on a typical development machine. If it does
not, profile evaluator reuse and state keys before increasing the budget.

Add a correctness benchmark/test which compares the pruned search with naive
complete assignment enumeration for the same immutable slates. Runtime
improvement is desirable, but exact clause coverage is the gating assertion.

### Implementation order and review checkpoints

Implement in this order. Each numbered item is a reviewable checkpoint with its
own production code and tests:

1. Refactor Phase 12 into the reusable `clinching.Evaluator`. Update the
   qualification refresher to reuse it and prove behavior parity before adding
   any scenario code.
2. Add scenario enums, validation, scheduled-game inputs, deterministic slate
   definition, canonical slate hashing, and slate tests.
3. Add the safe opportunity bound, base-3 assignment helper, exhaustive test
   oracle, and the unminimized three-way search for one team/achievement.
4. Add recursive condition deletion, alternative-outcome certification,
   dominance, canonical ordering, necessary-condition extraction, coverage
   assertions, and generated tiny-season tests.
5. Change fixture snapshot hashing to v2 with matchday, then add migration 5,
   cache DTOs, exact/latest lookup, transactional replacement, failure records,
   and round-trip/rollback tests.
6. Add the scenario refresher, shared season-input mapping, total budget,
   complete-batch fill behavior, and exact Phase 12 prerequisite lookup. At
   this checkpoint scenarios calculate after refresh but are not displayed.
7. Add syncer/config/command wiring, independent warnings and timeouts, README
   configuration, scheduler logging, and `/cache/status` visibility.
8. Add the dedicated clinching handler, page model, structured prose helpers,
   fixture list, no-help display, current/pending/unavailable/stale states, and
   full handler tests.
9. Add the season overview card, standings-badge link, fixtures-page slate link,
   minimal CSS, accessibility checks, and explicit Forecast Lab separation.
10. Add immutable real-snapshot fixtures, benchmarks, checked-in evidence, and
    update the README current checkpoint only after every exit criterion passes.

Do not combine slices 1-4 into one large change. The exhaustive reference must
exist before pruning or minimization is trusted, and cache/UI work must not
begin until the machine result is stable.

### Stop conditions

Stop and amend this plan instead of improvising if any of these occurs:

- A published clause covers even one complete assignment that the Phase 12
  points layer does not classify as clinched.
- The pruned search, minimized clause union, or assignment counts disagree with
  exhaustive enumeration on any generated tiny season.
- Making an outcome-only claim would require a representative score, arbitrary
  maximum score, deterministic team-ID tiebreak, or unavailable discipline
  data.
- ASA matchday metadata cannot satisfy the reliability checks and the 120-hour
  fallback produces slates larger than ten fixtures in real snapshots.
- The first real eight-game opportunity batch cannot meet the documented budget
  after evaluator reuse and profiling.
- The current schedule inventory contains a status that cannot safely be
  classified as completed or unfinished.
- A proposed stale-result view cannot resolve every named fixture as the same
  still-upcoming matchup.
- The configured achievements or official playoff format change; update
  `competition.Rules` and its version before calculating new scenarios.

Before marking Phase 13 complete, run:

```sh
go test ./...
go vet ./...
go test -race ./internal/clinching ./internal/scenarios \
  ./internal/scenariorefresh ./internal/cache
go test -run '^$' -bench BenchmarkScenarioSlate -benchmem ./internal/scenarios
```

Also run the server with JavaScript disabled and verify the dedicated page,
alternate-path disclosure, base-path navigation, stale banner, and fixtures
link manually. Update the README checkpoint only after persisted scenarios,
exact freshness handling, no-help coexistence, benchmark evidence, and the
forecast/proof language boundary all satisfy the exit criteria.
