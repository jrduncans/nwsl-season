# Phase 6: Basic factual playoffs

## Control

- Status: Complete
- Implementation: Terra
- Review: primary agent; one Sol gate for migration and stage isolation
- Depends on: Phase 5
- Checkpoints: one plan commit and one implementation commit

## Outcome

Load, refresh, and serve the current season's `Playoffs` scope without applying
regular-season rules or inventing a bracket. The website gains explicit stage
URLs and a factual chronological playoff fixtures/results view with xG. The
existing split source operations and hot scheduler own playoff discovery,
result checks, and xG checks.

ASA currently returns useful knockout flags and elapsed-minute data, but the
payload does not provide a verified round/series/advancement contract. Preserve
the complete source object and normalize only the two fields already selected
by the architecture plan. Do not infer an advancement winner, round, bracket,
extra-time result, or shootout result from matchday, scores, or raw-only fields.

## Fixed catalog boundary

Add exactly one new catalog entry:

```text
season:           2026
stage:            Playoffs
label:            2026 Playoffs
slug:             playoffs
kind:             knockout
public:           true
primary:          false
source available: true
inventory:        nil
rules:            nil
capabilities:     fixtures, xg
```

Keep `2026 / Regular Season` as the only primary entry for the season. Do not
add historical playoff entries merely because historical rows can be queried;
historical public availability and format research are separate work.

Catalog validation must reject duplicate stage slugs within one season and
multiple public primary stages for one season. Add defensive deterministic
lookups for a season's primary entry and for an entry by season/slug. Public
season navigation lists one primary item per season, while a separate stage
selector lists every public stage for the selected season in primary-first,
then label order.

Source-scope seeding already consumes source-available catalog entries. The
new scope is therefore active in 2026. Its nil inventory expectation is
intentional: a partial knockout schedule is never declared complete merely
because every currently known match is terminal.

## Knockout persistence

Advance the cache schema from version 12 to 13. Add these columns to `games`:

```sql
expanded_minutes INTEGER
knockout_game INTEGER NOT NULL DEFAULT 0
    CHECK (knockout_game IN (0, 1))
```

Expose them as nullable integer and boolean fields on `cache.Game`. Extend all
game reads, writes, equality/materiality checks, clones, result-check reads,
fixture snapshot input, and sync mapping. A change to either normalized field
is a material game-row change and participates in the existing atomic audit,
snapshot, and rollback contracts.

Migration 13 must preserve every existing row and derived/audit table. When a
legacy row has valid `raw_json`, backfill the two normalized values from
`expanded_minutes` and `knockout_game`; otherwise retain NULL/false defaults.
The migration remains idempotent on reopen and continues to tolerate the
minimal legacy fixtures used by cache migration tests.

The ASA client already decodes both fields, retains raw JSON, and sends
`stage_name` for games and xG. Do not add a new endpoint or request. Mapping
copies the decoded values without deriving additional state. Cache validation
requires nonnegative expanded minutes when present and requires every returned
row for a cataloged knockout stage to have `knockout_game=true`. Unknown stages
remain factual and are not classified from the flag.

Do not normalize `extra_time`, `penalties`, penalty scores, matchday-as-round,
referee, stadium, attendance, or manager fields in this phase. They remain
available in `raw_json` for later contract research.

## Explicit stage URLs

Make stage identity part of every season page URL:

```text
/seasons/{season}/{stage-slug}
/seasons/{season}/{stage-slug}/fixtures
/seasons/{season}/{stage-slug}/schedule-difficulty
/seasons/{season}/{stage-slug}/forecast
/seasons/{season}/{stage-slug}/model-evaluation
/seasons/{season}/{stage-slug}/clinching
```

The root redirects to the configured current season's primary stage. Legacy
season-only URLs redirect to the corresponding primary-stage URL, preserving
the feature suffix and query string. An uncataloged season uses the explicit
`regular-season` slug so existing factual/unknown-format behavior is retained.
An unknown stage slug for a cataloged season is a not-found response, never a
fallback to the process-wide configured stage.

Resolve the complete `requestCompetition` from the URL season and stage slug.
Every cache read, readiness read, rule lookup, telemetry attribute, navigation
link, forecast/scenario lookup, and unavailable-state page uses that resolved
season/stage. Remove hidden use of `Options.Stage` from request-scoped data
loading; it remains only a configured default for compatibility and root
selection.

Keep the season selector unique by season and link each item to that season's
primary stage. Add a stage selector for seasons with multiple public stages.
All section navigation uses the resolved stage base path.

For `2026 / Playoffs`:

- the stage root and fixtures route show the existing chronological factual
  fixtures/results presentation, including xG when cached;
- the page shows kickoff, teams, returned score/status, matchday grouping, and
  the normalized fields only as stored facts—no advancement prose;
- standings, schedule difficulty, forecast, model evaluation, qualification,
  scenarios, and bracket links are absent; and
- an empty unknown/not-published cache renders the normal load-state page and
  never contacts ASA from the HTTP request.

Regular-season stage routes retain their current pages and capabilities. The
legacy redirects are compatibility only; templates and generated links use
canonical stage URLs.

## Playoff hot planning

Extend hot scope selection to include active, source-available catalog stages
for the configured current season. Keep current/upcoming regular-season work
ahead of playoff work within the shared request budget. A due playoff job is
still hot and therefore suppresses Phase 5 cold work.

Use the existing jobs without a playoff-specific network path:

1. full games discovery immediately for a never-observed scope and weekly
   while it is active, including repeated empty/not-published observations;
2. targeted game checks after kickoff plus completion grace;
3. one initial authoritative stage-xG observation after fixtures exist; and
4. targeted missing/recent xG checks under the existing Phase 3 cadence.

Every playoff operation carries `season=2026`, `stage=Playoffs`. Full inventory
has no 16-team/240-game expectation. Authoritative deletion and targeted
non-deletion remain the Phase 2 storage contracts. Unknown-team recovery stays
the only extra request.

Playoff material changes update their own factual cache, fixture snapshot,
audit, and venue summary. They never run regular-season standings-derived
qualification/scenarios and never warm the regular-season forecast. A
regular-season operation never reads or mutates playoff rows.

## Bracket research boundary

The live historical payload contains raw extra-time and shootout-related
fields, but this phase has no authoritative definition for regulation score,
advancement score, round identity, placeholder teams, or bracket edges. Record
that the bracket contract is unproven and ship no bracket capability or UI.

A later bracket packet must first prove those meanings from an authoritative
source and add explicit normalized fields. It must not reinterpret rows stored
by this phase silently.

## Acceptance

- Catalog tests prove exact 2026 playoff metadata, primary/slug uniqueness,
  deterministic public season/stage navigation, and defensive copies.
- Migration tests prove exact version-13 columns/default/check, valid raw-JSON
  backfill, malformed/minimal legacy tolerance, row preservation, and reopen
  idempotence.
- Mapping and cache tests prove nullable expanded minutes, knockout boolean,
  knockout-stage validation, material updates, rollback, and exact
  regular/playoff isolation without repeating Phase 2's full matrices.
- HTTP tests prove canonical stage routes, legacy redirects, unknown-slug
  handling, selectors/links, loaded and not-published playoff pages, xG display,
  and the absence of regular-only features.
- Scheduler fake-clock tests prove initial/weekly playoff discovery, targeted
  result and xG jobs, regular-work priority, no inventory expectation, hot
  suppression of cold work, and no historical/regular derived recalculation.
- Existing regular-season routes, request counts, storage semantics, and full
  repository tests remain green.

## Allowed changes

- `internal/competition` catalog lookups and tests;
- `internal/cache` migration 13 and normalized game-field plumbing/tests;
- `internal/syncer` game mapping/tests;
- `internal/scheduler` current-playoff scope selection/priorities/tests;
- `internal/app` stage routing, selectors, factual playoff presentation/tests;
- narrow server wiring needed to keep regular-only downstream work isolated;
- this packet and packet index.

Do not add a bracket, historical playoff catalog, historical playoff rules,
new ASA resources, parallel source requests, a new lifecycle schema, or new
derived playoff calculations.

## Verification

During implementation use focused tests. Before the implementation commit run
once:

```text
go test -count=1 ./internal/competition ./internal/cache ./internal/syncer ./internal/scheduler ./internal/app ./cmd/server
go test -count=1 ./...
go vet ./...
git diff --check
```

## Stop conditions

Stop rather than broadening the phase if:

- ASA's `Playoffs` stage cannot be loaded through the existing stage filters;
- schema migration cannot preserve legacy game rows safely;
- explicit stage routing requires one URL to resolve to different stage data;
- playoff refresh cannot remain isolated from regular derived calculations; or
- a factual view would require guessing advancement or shootout semantics.

## Handoff

Implement the whole phase in one Terra pass. The primary review covers catalog,
HTTP behavior, planner priority, and bracket isolation. Use one final Sol gate
only for migration safety and cross-stage read/write isolation.
