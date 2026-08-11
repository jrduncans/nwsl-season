# P1-01: Competition catalog core

## Control

- Status: Complete
- Intended implementation model: GPT-5.6 Luna, medium reasoning
- Required review: Terra or Sol
- Depends on: the existing ASA loading plan
- Blocks: the source-scope registry and request-scoped capability packets

## Goal

Introduce an immutable, validated competition catalog entry for the verified
2026 regular season while preserving the existing `competition.Rules` API and
all current application behavior.

This packet creates the stable vocabulary that later Phase 1 packets will use.
It does not migrate the HTTP application, scheduler, or persistence layer.

## Why this packet exists

Phase 1 of the
[ASA data catalog and loading plan](../../asa-data-catalog-and-loading-plan.md)
requires season/stage catalog entries, stage kinds, inventory expectations, and
capability flags. The current implementation in
`internal/competition/rules.go` recognizes only one `Rules` value and cannot
describe a factual-only or non-league stage.

Keeping this first change inside `internal/competition` makes it deterministic
and independently reviewable before persistent registry or HTTP decisions are
built on top of it.

## Fixed decisions

### Compatibility boundary

- Keep the exported `AchievementID`, `Achievement`, and `Rules` types unchanged.
- Keep `Rules.Validate`, `Rules.Copy`, and `ForSeason` available with their
  current signatures.
- Change `ForSeason` internally to use the new catalog lookup. Its observable
  behavior remains unchanged: 2026 `Regular Season` returns defensive validated
  rules and an unknown scope returns `Rules{}, false`.
- Do not change any caller outside `internal/competition` in this packet.

The inventory fields duplicated in the existing `Rules` value are a temporary
compatibility bridge. New catalog consumers will use `Entry.Inventory`; a later
integration packet can decide when removing those fields from `Rules` is worth
the broader migration.

### New catalog vocabulary

Add `internal/competition/catalog.go` with these exported types and constants:

```go
type StageKind string

const (
    StageKindLeagueTable StageKind = "league_table"
    StageKindKnockout    StageKind = "knockout"
    StageKindGroup       StageKind = "group"
)

type Capability string

const (
    CapabilityFixtures          Capability = "fixtures"
    CapabilityStandings         Capability = "standings"
    CapabilityXG                Capability = "xg"
    CapabilityScheduleDifficulty Capability = "schedule_difficulty"
    CapabilityForecast          Capability = "forecast"
    CapabilityQualification     Capability = "qualification"
    CapabilityScenarios         Capability = "scenarios"
    CapabilityBracket           Capability = "bracket"
)

type InventoryExpectation struct {
    Teams        int
    GamesPerTeam int
    Games        int
}

type Entry struct {
    Season          string
    Stage           string
    Label           string
    Slug            string
    Kind            StageKind
    Public          bool
    Primary         bool
    SourceAvailable bool
    Inventory       *InventoryExpectation
    Rules           *Rules
    Capabilities    []Capability
}
```

The formatting above is illustrative; use ordinary `gofmt` output.

Add these exported operations:

```go
func (e Entry) Validate() error
func (e Entry) Copy() Entry
func (e Entry) Supports(capability Capability) bool
func Lookup(season, stage string) (Entry, bool)
func PublicEntries() []Entry
```

### Validation contract

`Entry.Validate` must reject:

- blank season, stage, label, or slug;
- a slug that is not lowercase kebab case matching
  `[a-z0-9]+(?:-[a-z0-9]+)*`;
- an unknown `StageKind`;
- a primary entry that is not public;
- a public entry whose ASA source is unavailable;
- negative inventory fields;
- an inventory expectation with all three values zero;
- setting only one of `Teams` and `GamesPerTeam`;
- an odd `Teams * GamesPerTeam` product;
- a nonzero `Games` value that disagrees with
  `Teams * GamesPerTeam / 2` when the team fields are present;
- rules that fail `Rules.Validate` or whose season/stage does not match the
  entry;
- an unknown or duplicate capability;
- standings, schedule difficulty, forecast, qualification, or scenarios on a
  non-league-table stage;
- qualification or scenarios without verified rules;
- bracket support on a non-knockout stage.

`Inventory == nil` and `Rules == nil` are valid. They represent scopes whose
inventory or achievement rules are not verified. Capabilities not forbidden by
the rules above remain independent; do not invent additional dependencies.

### Copy and lookup contract

- `Entry.Copy` must deep-copy the inventory value, rules value including its
  achievement slice, and capability slice.
- `Lookup` must return a defensive copy.
- `PublicEntries` must return defensive copies of only public entries.
- Sort `PublicEntries` by season descending, then primary entries before other
  stages in the same season, then label ascending.
- `Supports` returns true only when the exact capability occurs in the entry.
  It must not infer capabilities from the stage kind or rules.

### Initial catalog data

The private catalog contains exactly one entry in this packet:

- season: `2026`;
- stage: `Regular Season`;
- label: `2026 Regular Season`;
- slug: `regular-season`;
- kind: `StageKindLeagueTable`;
- public: true;
- primary: true;
- source available: true;
- inventory: 16 teams, 30 games per team, 240 total games;
- rules: the existing `regular2026` value;
- capabilities, in declaration order: fixtures, standings, xG, schedule
  difficulty, forecast, qualification, and scenarios.

Do not add historical or playoff entries yet. Their metadata has not been
verified for this packet.

## Allowed changes

- Add `internal/competition/catalog.go`.
- Add `internal/competition/catalog_test.go`.
- Modify `internal/competition/rules.go` only to make `ForSeason` delegate to
  the catalog or to support the defensive-copy implementation.
- Modify `internal/competition/rules_test.go` only when preserving the existing
  compatibility assertions requires it.

Do not edit files outside `internal/competition`.

## Required behavior

- The initial catalog and its rules validate successfully.
- Existing callers compile without changes.
- Repeated lookups cannot observe mutations made to prior returned values.
- Unknown scopes remain distinguishable from verified scopes.
- Catalog iteration is deterministic and safe for later navigation consumers.

Return descriptive errors that identify the invalid field or relationship. No
test should depend on an entire error string; tests may check a stable relevant
substring.

## Tests to add or update

Add table-driven tests covering at least:

1. Exact 2026 lookup data and every declared capability.
2. Unknown season and unknown stage lookup failures.
3. Defensive copying of inventory, capabilities, and rule achievements across
   repeated `Lookup` calls.
4. Defensive copying from `PublicEntries`.
5. Public-entry ordering using a private test helper or an unexported sorting
   helper with multiple invented entries; do not add fake entries to the
   production catalog.
6. Every validation rejection listed above, with one focused case per rule.
7. Valid entries with nil inventory, nil rules, and factual fixture/xG
   capabilities.
8. A valid knockout entry with bracket capability and no invented league
   capabilities.
9. Preservation of the existing `ForSeason` behavior and defensive rule copy.

Tests must not perform network requests, open SQLite, depend on wall-clock time,
or use real historical competition assumptions.

## Verification

Run all of these commands from the repository root:

```text
gofmt -w internal/competition/catalog.go internal/competition/catalog_test.go internal/competition/rules.go internal/competition/rules_test.go
go test ./internal/competition
go test ./...
git diff --check
```

If a listed existing file did not change, it may be omitted from the `gofmt`
arguments. All other commands are mandatory.

## Non-goals

- Persisting source scopes or refresh state.
- Seeding the current or next calendar year.
- Changing application options or resolving rules per request.
- Removing the unknown-season presentation fallback.
- Changing scheduler behavior.
- Loading historical data or adding historical format metadata.
- Adding playoff catalog data, routes, or views.
- Removing inventory fields from `Rules`.

## Stop conditions

Stop and report without broadening the patch if:

- satisfying the contract requires a caller change outside
  `internal/competition`;
- existing behavior depends on mutating a returned `Rules` value globally;
- a required historical or playoff fact is missing;
- the packet conflicts with a newer catalog or registry implementation already
  present in the worktree;
- the full test suite exposes a pre-existing failure unrelated to the packet.

For a pre-existing failure, include the failing command and enough output to
distinguish it from a regression; do not repair unrelated code.

## Handoff

Report:

- files changed;
- the catalog and validation behavior implemented;
- every verification command and its outcome;
- any deviation from this packet;
- any issue the registry or HTTP integration packet should account for.
