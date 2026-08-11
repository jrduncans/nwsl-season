# P1-05: Persisted season readiness

## Control

- Status: Ready
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P1-01 competition catalog core, P1-02 persisted source-scope
  registry, and P1-04 capability-aware factual HTTP
- Blocks: historical catalog population, public historical navigation, and the
  multi-scope loading planner

## Goal

Add a read-only cache API that reports factual source readiness and verified
fixture-inventory completeness for each persisted season/stage scope, without
changing source state, loading data, or treating an unverified inventory as
incomplete.

## Why this packet exists

Phase 1 of the
[ASA data catalog and loading plan](../../asa-data-catalog-and-loading-plan.md)
ends with season inventory/readiness APIs and a deterministic past-season test
whose format differs from 2026. P1-02 persisted source identity, lifecycle, and
discovery state, but callers must currently query that state separately from
cached games and catalog inventory expectations. P1-04 therefore still uses a
season read as its only data-presence check and deliberately leaves persisted
loading and not-published states for this packet.

The API belongs in `internal/cache`: `source_scopes` is the durable set of
source identities, and the cache owns the observed games from which a scope's
participating-team and fixture counts can be read. The competition catalog
remains the authority for expected inventory. This packet exposes a stable
read model for later loading and navigation work; it does not make HTTP consume
that model yet.

## Fixed decisions

### Read-model vocabulary

Add these exported string types and constants in a new
`internal/cache/season_readiness.go` file:

```go
type SourceReadiness string

const (
    SourceReadinessUnknown      SourceReadiness = "unknown"
    SourceReadinessNotPublished SourceReadiness = "not_published"
    SourceReadinessAvailable    SourceReadiness = "available"
)

type InventoryCompleteness string

const (
    InventoryCompletenessUnknown    InventoryCompleteness = "unknown"
    InventoryCompletenessIncomplete InventoryCompleteness = "incomplete"
    InventoryCompletenessComplete   InventoryCompleteness = "complete"
)

type SeasonReadinessSnapshot struct {
    Scope             SourceScope
    Readiness         SourceReadiness
    Completeness      InventoryCompleteness
    ObservedTeams     int
    ObservedGames     int
    ExpectedInventory *competition.InventoryExpectation
}
```

Use ordinary `gofmt` formatting. `Readiness` answers whether factual source
inventory has been observed. `Completeness` answers whether observed fixture
identity matches a verified catalog expectation. Neither value means that a
season is competitively finished, fresh, public, or ready for every product
capability.

`ExpectedInventory` must be a defensive copy. Mutating it must not affect the
competition catalog or a later returned snapshot. `Scope` carries the exact
persisted registration, lifecycle, discovery, and timestamps without
reinterpreting them.

### Read APIs

Add these exported operations:

```go
func (c *DB) SeasonReadiness(
    ctx context.Context,
    season string,
    stage string,
) (SeasonReadinessSnapshot, bool, error)

func (c *DB) SeasonReadinesses(
    ctx context.Context,
) ([]SeasonReadinessSnapshot, error)
```

Both operations are local SQLite reads and must use a read-only transaction so
the source-scope row and observed inventory come from one database snapshot.
They must not write a row, update a timestamp, call `EnsureSourceScopes`, or
contact ASA.

`SeasonReadiness` returns `(SeasonReadinessSnapshot{}, false, nil)` when the
exact persisted source-scope identity is absent. A catalog entry or cached rows
alone do not synthesize a registry identity in this read API. Reject a blank
season or stage after trimming with a descriptive error before opening the
transaction.

`SeasonReadinesses` returns exactly one result for every persisted
`source_scopes` row, including configured, provisional, catalog, and observed
registrations. Sort by season descending and then stage ascending, matching
`SourceScopes`. Return an empty non-nil slice for an empty registry.

Keep SQL scanning and evaluation behind private helpers shared by the single
and list operations. Query errors, scan errors, transaction errors, and invalid
persisted enum values must return descriptive errors rather than silently
inventing readiness.

### Observed inventory

For one exact season/stage, derive:

- `ObservedGames` from the number of cached `games` rows in that scope; and
- `ObservedTeams` from distinct nonempty `home_team_id` and `away_team_id`
  values in those games.

The global `teams` table is not season-scoped, so unrelated team rows must not
contribute to `ObservedTeams`. Also retain private per-team appearance counts
while evaluating completeness. Count one appearance for each home or away
assignment; do not infer an appearance from team metadata, standings, xG, raw
JSON, or a configured rule.

Do not add terminal-game, xG, staleness, last-attempt, or last-success fields to
this read model. Those concerns either do not describe fixture inventory or
belong to Phase 2 source audit/state.

### Source readiness evaluation

Evaluate readiness from persisted discovery plus observed factual games:

1. Return `SourceReadinessAvailable` when `ObservedGames` is greater than zero
   or persisted discovery is `SourceScopeAvailable`.
2. Otherwise return `SourceReadinessNotPublished` when persisted discovery is
   `SourceScopeNotPublished`.
3. Otherwise return `SourceReadinessUnknown`.

Observed games deliberately win over an older `unknown` or `not_published`
discovery value. P1-02 did not add runtime discovery transitions, and this
read-only API must remain truthful when a later existing sync writes games
without updating `source_scopes`. Do not persist that effective value back to
the registry.

Reject an unknown persisted discovery, registration, or lifecycle enum with a
descriptive error. Do not infer readiness or completion from the season string,
the current calendar year, lifecycle, all games being terminal, or an HTTP
capability.

### Inventory expectation and completeness

For each persisted identity, call `competition.Lookup(season, stage)`. Use a
defensive copy of `Entry.Inventory` when the entry exists and its inventory is
non-nil. An uncataloged scope, or a cataloged scope with nil inventory, has no
verified expectation.

Evaluate completeness as follows:

- no verified expectation: `InventoryCompletenessUnknown`, regardless of
  observed counts;
- verified expectation and every specified nonzero dimension matches:
  `InventoryCompletenessComplete`;
- verified expectation and any specified dimension differs:
  `InventoryCompletenessIncomplete`.

Matching a nonzero `Teams` value requires `ObservedTeams` to equal it. Matching
a nonzero `Games` value requires `ObservedGames` to equal it. Matching a
nonzero `GamesPerTeam` requires exactly the expected number of observed teams
and every observed team to have that many home-plus-away appearances. Check
each specified dimension even though production catalog validation normally
makes the fields mutually consistent.

Completeness is inventory-only. It may be `complete` while readiness is
`available`, and it must not change `SourceScope.Lifecycle` to completed. A
verified zero-row mismatch is incomplete, while an unverified zero-row or
partial inventory remains unknown rather than being compared with 2026.

Implement the readiness/completeness calculation in an unexported deterministic
helper that accepts an explicit `*competition.InventoryExpectation` and private
observed-count inputs. Tests must be able to supply an invented past-season
expectation without mutating or adding to the production competition catalog.

### Phase boundary

This packet does not change persisted state. In particular:

- do not add a schema migration or modify the `source_scopes` table;
- do not update discovery or lifecycle after a source request;
- do not add generalized resource audit, due-time, attempt, success, trigger,
  mode, or change-count state;
- do not decide whether a scope is public or add historical catalog data; and
- do not expose this read model through HTTP yet.

Those boundaries keep Phase 1 readiness separate from Phase 2 refresh state
and Phase 4 historical product/navigation work.

## Allowed changes

- Add `internal/cache/season_readiness.go`.
- Add `internal/cache/season_readiness_test.go`.
- Modify `internal/cache/source_scopes.go` only if a private scanner or enum
  validator must be shared by the compound read.
- Modify `internal/cache/source_scopes_test.go` only for shared-helper coverage.
- Modify `internal/cache/cache.go` or `internal/cache/cache_test.go` only if an
  existing private query abstraction is required for a read-only transaction.
- Update `docs/asa-loading/README.md` and this packet's control status during
  implementation and review.

Do not modify migrations, SQL schema version, production competition catalog
data, HTTP application or templates, commands, configuration, scheduler,
syncer, ASA client, qualification, scenarios, forecasts, CSS, or JavaScript.

## Required behavior

- Persisted scopes can be read with their effective factual readiness and
  observed team/game inventory in one consistent snapshot.
- An exact missing source-scope identity remains distinguishable from an
  existing unknown or not-published scope.
- Observed cached games make a scope factually available without mutating stale
  persisted discovery state.
- Global team rows and data from another season or stage do not affect observed
  inventory.
- Only a verified scope-specific catalog expectation can produce complete or
  incomplete inventory.
- A verified expectation uses every specified count, including per-team
  appearances; it never uses `competition.Rules.ExpectedTeams` or
  `Rules.GamesPerTeam`.
- An uncataloged past season with factual games reports available readiness and
  unknown completeness, never a mismatch against 2026.
- Returned catalog inventory is defensive, and repeated reads are deterministic.
- Reads do not modify source-scope timestamps, discovery, lifecycle, cached
  source rows, or audit rows and make no network request.
- Existing `SourceScope`, `SourceScopes`, `Status`, `Season`, scheduler, sync,
  and HTTP behavior remain unchanged.

## Tests to add or update

Add deterministic tests covering at least:

1. A missing exact identity returns the zero snapshot, `false`, and no error;
   blank season and stage return descriptive errors.
2. Unknown, not-published, and available persisted discoveries map to the
   corresponding readiness when no games exist.
3. Observed games override stale unknown and not-published discovery in the
   returned read model without changing the persisted `SourceScope` or its
   timestamps.
4. Observed team count uses distinct home/away participants only; unused global
   team rows, another season, and another stage are excluded.
5. Nil catalog inventory produces unknown completeness for empty, partial, and
   populated observed inventories.
6. A verified expectation is complete only when team count, total game count,
   and every per-team appearance count match; focused mismatches in each
   dimension are incomplete.
7. An invented past regular season with a different format, for example 14
   teams playing 26 games each and 182 total games, is evaluated as complete
   through the private explicit-expectation helper. The same observations are
   incomplete against an invented 16/30/240 expectation, proving evaluation
   does not inherit the production 2026 format. Do not add either entry to the
   production catalog.
8. An uncataloged invented past scope with cached games is available with
   unknown completeness through the exported database API.
9. `SeasonReadinesses` returns every registry identity exactly once in season-
   descending, stage-ascending order and returns an empty non-nil slice for an
   empty registry.
10. Mutating returned `ExpectedInventory` cannot change the production catalog
    or a later result.
11. Invalid persisted discovery, lifecycle, or registration values produce a
    descriptive error using the narrowest practical fixture or private-helper
    seam; do not weaken SQLite constraints to construct the case.
12. A read leaves source scopes, timestamps, games, teams, sync runs, and xG
    runs unchanged. No test makes a network request or depends on wall-clock
    time.

Use fixed timestamps, invented scope names, and small SQLite fixtures. Tests
must not mutate production catalog storage or rely on actual historical NWSL
format claims; the 14/26/182 values are test data, not a catalog assertion.

## Verification

Run all of these commands from the repository root:

```text
gofmt -w internal/cache/season_readiness.go internal/cache/season_readiness_test.go internal/cache/source_scopes.go internal/cache/source_scopes_test.go internal/cache/cache.go internal/cache/cache_test.go
go test ./internal/cache
go test ./...
go vet ./...
git diff --check
```

If a listed existing or optional file did not change, omit it from the `gofmt`
arguments. All other commands are mandatory.

## Non-goals

- Persisting runtime discovery transitions or completion decisions.
- Adding source reconciliation, empty-response, or deletion semantics.
- Adding generalized source audit/scope state or per-resource due times.
- Changing `ReplaceSeason`, `ReplaceGameXG`, `sync_runs`, or `xg_sync_runs`.
- Loading data, polling provisional scopes, or making an ASA request.
- Changing scheduler priorities, sync orchestration, or maintenance commands.
- Adding production historical or playoff catalog entries.
- Making provisional, observed, or catalog scopes public.
- Adding HTTP readiness states, a season selector, stage URLs, redirects, or
  navigation.
- Changing capability gates, factual-only pages, standings, forecasts,
  qualification, or scenarios.
- Inferring competitive completion from terminal games or an old season year.

## Stop conditions

Stop and report without broadening the patch if:

- a consistent readiness snapshot requires a write or schema migration;
- observed inventory cannot be scoped exactly by persisted season and stage;
- completeness requires an unverified historical fact or a production catalog
  entry added only for test coverage;
- the API cannot distinguish a missing registry identity from unknown
  readiness without changing `source_scopes` semantics;
- implementation needs Phase 2 audit, due-time, targeted-upsert, or runtime
  discovery-transition state;
- an HTTP, scheduler, syncer, command, configuration, or network change is
  required;
- the worktree contains a newer readiness API whose contract conflicts with
  this packet; or
- the full suite exposes a pre-existing failure unrelated to the packet.

For a pre-existing failure, include the failing command and enough output to
distinguish it from a regression; do not repair unrelated code.

## Handoff

Report:

- files changed;
- the exported snapshot vocabulary and read APIs implemented;
- exact readiness and completeness evaluation behavior;
- proof that invented past-season expectations remain private test inputs and
  that no 2026 fallback reaches uncataloged inventory;
- proof that the operations are read-only;
- every verification command and its outcome;
- any deviation from this packet; and
- any issue later HTTP readiness, historical catalog, navigation, or Phase 2
  planner packets should account for.
