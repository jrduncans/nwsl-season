# Phase 4: Load and serve factual historical seasons

## Control

- Status: Ready
- Implementation: Terra
- Review: primary agent
- Depends on: Phase 3
- Checkpoints: one plan commit and one implementation commit

## Outcome

Make the completed ASA regular seasons already used by model evaluation
available as factual website scopes. One command loads them sequentially
through the Phase 3 compatibility facade, which now owns the split full-games
and full-xG operations. Public pages remain SQLite-only and expose a season
selector plus an explicit not-loaded/not-published state.

This phase intentionally does not verify historical competition formats.
Historical pages show fixtures, results, factual standings, xG, and expected
points without a playoff cutline. Forecasts, schedule difficulty,
qualification, scenarios, and model-evaluation navigation stay disabled for
those seasons.

## Fixed historical catalog

Add `Regular Season` catalog entries for:

```text
2016, 2017, 2018, 2019, 2021, 2022, 2023, 2024, 2025
```

The list matches the complete regular seasons already checked into model
evaluation evidence. ASA does not provide a 2020 regular season, so do not add
one. Keep the verified 2026 entry unchanged.

Each historical entry is:

- public and source-available;
- a primary `league_table` stage with slug `regular-season`;
- labeled `<season> Regular Season`;
- limited to fixtures, standings, and xG capabilities; and
- defined without inventory expectations or `competition.Rules`.

Do not infer old playoff places, tiebreak versions, team counts, or
games-per-team from the evaluation row counts. Standings are factual table
calculations; the format notice must say that qualification rules and the
playoff line are unverified.

Catalog and public-entry reads remain defensive and deterministic: season
descending, then the existing primary/label ordering. Source-scope seeding
registers these entries as catalog scopes. Calendar years before the seeding
clock's year are completed lifecycle scopes; the current year remains active
and future provisional scopes remain upcoming. Never regress an explicitly
completed scope.

## Sequential historical backfill

Add one `cmd/sync` mode named `-backfill-historical`. It selects the public,
source-available historical regular-season catalog entries other than the
configured current scope and processes them in deterministic season-descending
order.

For each scope, call the existing `Service.Run` compatibility facade with:

- the catalog season/stage;
- trigger `backfill`;
- a forced authoritative refresh; and
- no invented expected-team or games-per-team values.

`Service.Run` is acceptable here because Phase 3 implements it as the
sequential split-operation sequence. Do not add a second backfill executor or
call the legacy replacement APIs. Its per-scope lease, game-first/xG-second
ordering, unknown-team recovery, audits, and transactional store contracts
remain authoritative.

The command stops on the first failed fixture or xG refresh, returns nonzero,
and identifies the failing scope. A successful summary reports every completed
scope and its game/xG counts. It never runs historical qualification or
scenario calculations because those entries have no verified rules.

Make `backfill-evaluation-data` invoke this one process instead of starting one
Go process per season. Keep the explicit season list in the catalog rather than
duplicating it in the Makefile.

This phase adds no startup worker. Server startup and ordinary HTTP requests
must not contact ASA for historical pages.

## Historical HTTP behavior

Add a season selector sourced from `competition.PublicEntries`. It lists the
catalog labels in deterministic order, identifies the requested season, and
links to `/seasons/{season}`. It is present on loaded season pages and the
historical load-state page so users can recover from an unavailable selection.

For a loaded historical entry:

- `/seasons/{season}` shows factual standings with no playoff line;
- `/seasons/{season}/fixtures` shows results, fixtures, and xG when available;
- navigation contains only supported sections; and
- a format notice explains that qualification rules, forecasts, and clinching
  are not verified for the season.

For a public catalog entry without cached games, render a normal cache-only
season state instead of a generic internal error:

- `unknown` means the season has not been loaded;
- `not_published` means ASA returned no published inventory; and
- an absent readiness row is treated as not loaded, not fabricated as source
  failure.

The state should use a successful HTTP response because it is a valid public
catalog route, retain the season selector, and make no source request. Keep
uncataloged season behavior capability-safe and do not add uncataloged values
to the selector.

## Acceptance

- Catalog tests assert the exact ten public regular seasons (2026 plus the nine
  historical entries), descending order, defensive copies, the absence of
  2020, and factual-only historical capabilities.
- Source-scope tests prove historical catalog scopes seed as completed without
  regressing a preexisting completed scope; current and upcoming behavior is
  unchanged.
- Backfill selection tests prove deterministic historical order, exclusion of
  the configured current scope, one sequential `Run` per selected scope,
  `backfill`/forced options, zero invented expectations, and stop-on-first
  fixture or xG failure.
- The Make target invokes one historical-backfill command rather than a shell
  loop.
- HTTP tests prove the selector order/current marker, a loaded historical
  factual standings/fixtures view, no playoff line or rule-dependent links,
  and explicit unknown/not-published states with zero source calls.
- Existing 2026 pages, direct unknown-format pages, base-path behavior, and
  current-season forecast/qualification navigation remain unchanged.

## Allowed changes

- `internal/competition` catalog and tests;
- source-scope lifecycle seeding and focused tests;
- `cmd/sync`, its tests, and the evaluation Make target;
- `internal/app` view/handler/template/style changes and focused tests;
- this phase plan and packet index.

Do not add a migration, a background worker, historical rules or inventory
guesses, stage URLs, playoff support, monthly sweeps, maintenance commands, or
new ASA resources.

## Verification

During implementation, use focused package tests. Before the implementation
commit run once:

```text
go test -count=1 ./internal/competition ./internal/cache ./internal/app ./cmd/sync
go test -count=1 ./...
go vet ./...
git diff --check
```

## Stop conditions

Stop rather than broadening the phase if:

- an archived regular season violates the completed-game/xG contracts already
  proven by the model-evaluation backfill;
- factual standings require an unverified historical rule;
- a useful load-state page would need an HTTP-triggered source request; or
- safe sequential backfill cannot reuse the Phase 3 compatibility facade and
  its scope lease.

## Handoff

Implement the whole phase in one Terra pass. The primary review should focus
on capability isolation, backfill request order, and avoiding fabricated
historical rules. No separate Sol gate is required because this phase adds no
schema or concurrent execution.
