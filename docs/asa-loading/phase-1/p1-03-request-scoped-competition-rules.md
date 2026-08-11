# P1-03: Request-scoped competition rules

## Control

- Status: Ready
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P1-01 competition catalog core and P1-02 persisted source-scope
  registry
- Blocks: removal of the historical presentation fallback, capability-aware
  HTTP behavior, and season inventory/readiness APIs

## Goal

Resolve every rule-dependent HTTP calculation and cache lookup from the season
in the request URL and the configured stage, so a historical request cannot
inherit the configured current season's competition rules.

## Why this packet exists

Phase 1 of the
[ASA data catalog and loading plan](../../asa-data-catalog-and-loading-plan.md)
requires rules to be resolved inside each HTTP request before historical data
is advertised. P1-01 added the catalog, but `internal/app` still stores one
`competition.Rules` value in `Options` and reads `a.options.Rules` throughout
season, forecast, qualification, and scenario handlers.

That process-wide value is correct only for the configured primary scope. A
request for another season currently uses its playoff line, expected inventory,
rules version, and forecast parameters. This packet removes that cross-season
leak while deliberately preserving the existing presentation fallback for an
unknown format. The next HTTP packet can then remove that fallback and gate
features using catalog capabilities without also having to untangle rule
selection.

The persisted source-scope registry is not an HTTP catalog and must not be used
to infer product rules. P1-02 is a sequencing prerequisite, but this packet
does not make registry entries public or query `source_scopes` from a request.

## Fixed decisions

### Request identity

The competition identity for the existing HTTP routes is:

- season: `r.PathValue("season")`, falling back to
  `Options.CurrentSeason` only when the path value is empty; and
- stage: `Options.Stage`.

Do not add stage URL segments or accept a stage query parameter in this packet.
All cache reads and rule resolution for one request must use the same resolved
season and configured stage.

### Private rule resolver

Add an unexported application method with this behavior; the exact private name
may follow surrounding style:

```go
func (a *application) rulesForSeason(season string) competition.Rules
```

It returns a defensive rule copy selected as follows:

1. When `season == a.options.CurrentSeason`, return a defensive copy of
   `a.options.Rules`. This preserves explicit current-scope options used by the
   server and tests.
2. Otherwise call `competition.Lookup(season, a.options.Stage)`. When the entry
   exists and has non-nil `Rules`, return a defensive copy of those rules.
3. Otherwise return `presentationFallbackRules(season, a.options.Stage)`.

The configured current-scope exception is a compatibility boundary, not a
general override: `Options.Rules` must never be used for a different season in
the URL. A catalog lookup result is already defensive, but copy its rules when
storing or passing them so later handler changes cannot mutate shared request
state.

Keep `presentationFallbackRules` unchanged in this packet, including version
`presentation-fallback-v1`, 16 expected teams, 30 games per team, and the
1/4/8 achievement thresholds. Its season and stage must always be the requested
identity. Removing those invented assumptions belongs to the next packet.

Do not add a public resolver interface, a mutable package-level lookup
function, fake production catalog entries, or competition metadata to request
context. This slice needs only private rule resolution.

### Resolve once and pass explicitly

Each rule-dependent request handler must resolve rules for its URL season and
pass that value explicitly to helpers used by the request. Do not leave a mix
of request rules and `a.options.Rules` reads in one request path.

It is acceptable for a handler to resolve the same deterministic value again
after delegating to an existing page loader when avoiding a broad return-type
change, but every resolution must use the URL season. Prefer passing the value
through a narrow private helper parameter when practical.

Update at least these request behaviors to use the resolved rules:

- standings playoff lines and total-position playoff lines;
- schedule completeness and expected-team notes;
- qualification snapshot lookups and badges by rules version;
- scenario snapshot and qualification lookups on the clinching page by rules
  version;
- forecast simulation `PlayoffPlaces`;
- forecast result cache keys;
- forecast comparison rows, cutline, and schedule notes.

Any private page, forecast, or data-loading helper whose result depends on
rules must receive the resolved value or resolve from the same request season.
After this change, no HTTP request path for a non-current season may read
`a.options.Rules` directly.

### Non-request consumers

Keep process-level behavior scoped to the configured current season:

- `Application.PrecacheForecasts` and `PrecacheForecastsWithTrigger` continue
  to use `a.options.CurrentSeason`, `a.options.Stage`, and `a.options.Rules`;
- `/cache/status` continues to report the configured primary scope and its
  configured rules version;
- the root redirect continues to target `Options.CurrentSeason`;
- scheduler, syncer, qualification refresh, and scenario refresh construction
  in `cmd/server` remain unchanged.

These are not requests for an arbitrary season and do not need the new private
resolver in this packet.

### Options compatibility

Keep the exported `Options` type and its `Rules` field. Keep `defaultOptions`
behavior for the configured current scope:

- default the season and stage as today;
- use catalog rules for the configured identity when available;
- otherwise install `presentationFallbackRules` for the configured identity;
- validate the resulting rules; and
- preserve the existing forecast and location defaults.

Do not tighten validation to reject existing explicit test rules whose season
string differs from `CurrentSeason`; that cleanup is unnecessary for the
request-scoping guarantee and would create unrelated churn.

Keep `NewHandler`, `NewHandlerWithOptions`, `NewApplication`, and the exported
`Application` methods source-compatible.

### Error and unsupported-scope behavior

Preserve current HTTP status and error behavior. In particular:

- an uncached requested season still renders the existing season-unavailable
  error;
- an unknown but cached season still uses the temporary fallback rules;
- no route is hidden or rejected based on `Entry.Capabilities` yet;
- no unknown-format warning or new loading state is added; and
- qualification/scenario absence continues to render its existing pending or
  unavailable state.

This packet changes which rules are selected, not the presentation contract.

## Allowed changes

- Modify Go files in `internal/app` only where necessary to pass resolved rules
  through existing season, clinching, and forecast request paths.
- Modify existing `internal/app` test files for deterministic request-scoping
  coverage.

Do not modify templates, CSS, JavaScript, `cmd/server`, `internal/cache`,
`internal/competition`, scheduler, syncer, configuration, or persistence
schemas in this packet.

## Required behavior

- A request for the configured current season preserves all existing output and
  uses the explicit `Options.Rules` value.
- A request for a different cataloged season/stage uses that entry's rules,
  regardless of the configured current-season rules.
- A request for an unknown non-current season receives fallback rules labeled
  with that requested season and the configured stage, never the configured
  current identity.
- Rule-dependent persisted lookups use the requested season's rules version.
- Forecast simulations and cache keys use the requested season's playoff-place
  count.
- Schedule notes and standings cutlines use the requested season's inventory
  and achievements.
- Mutating one returned rules value cannot affect a later request.
- Current-scope precaching, cache status, root redirect, and all network and
  scheduler behavior remain unchanged.

## Tests to add or update

Add deterministic tests covering at least:

1. The private resolver returns explicit `Options.Rules` for the configured
   current season and returns a defensive copy.
2. With an invented non-2026 configured current scope and invented explicit
   rules, resolving `2026` returns catalog version `2026-regular-v2`, 30 games
   per team, and eight playoff places rather than the configured values.
3. Resolving an unknown non-current season returns
   `presentation-fallback-v1` whose season is the requested value and whose
   stage is `Options.Stage`.
4. A `/seasons/2026` request made while another invented season is configured
   renders the 2026 playoff line and 2026 schedule expectation. Use the real
   2026 catalog entry; do not add a fake catalog row.
5. Qualification badge lookup for a non-current cataloged request passes the
   requested rules version, not the configured rules version. Extend a fake
   store to record the argument rather than relying only on rendered text.
6. Clinching qualification and scenario lookups use the requested rules
   version. Keep the fixture snapshot and scenario definition assertions
   intact.
7. A forecast request for a non-current cataloged season supplies the requested
   playoff-place count to every simulation task and uses that value in the
   result cache key and rendered cutline.
8. Current-season forecast precaching still uses the explicit configured rules
   and retains the existing zero-assumption cache behavior.
9. Existing unknown-season fallback, reverse-proxy relative paths, error
   responses, and current-season handler tests continue to pass.

Tests must not mutate the production competition catalog, make network
requests, depend on SQLite, or depend on the wall clock. Prefer invented
configured seasons and the existing fake stores.

## Verification

Run all of these commands from the repository root:

```text
gofmt -w internal/app/handler.go internal/app/handler_test.go internal/app/forecast_handler.go internal/app/forecast_executor_test.go
go test ./internal/app
go test ./cmd/server
go test ./...
go vet ./...
git diff --check
```

If a listed test file does not change, omit it from the `gofmt` arguments. All
other commands are mandatory.

## Non-goals

- Removing or changing `presentationFallbackRules`.
- Hiding standings, forecast, qualification, scenarios, or other routes based
  on catalog capabilities.
- Adding unsupported-feature explanations or factual-only page variants.
- Adding season or stage navigation.
- Adding stage URL identity or redirects.
- Reading `source_scopes` from HTTP handlers.
- Adding inventory/readiness cache APIs.
- Making provisional or observed registry scopes public.
- Adding historical or playoff catalog entries.
- Changing forecast algorithms, models, or concurrency behavior.
- Changing qualification/scenario calculation or refresh construction.
- Changing scheduler scope selection or making ASA requests.

## Stop conditions

Stop and report without broadening the patch if:

- correct request-scoped selection requires adding an unverified historical or
  playoff catalog entry;
- a request path cannot use requested rules without changing a public URL or
  persistence schema;
- a rule-dependent handler cannot be separated from process-wide scheduler or
  sync behavior within `internal/app`;
- current behavior depends on mutating `Options.Rules` after application
  construction;
- the full suite exposes a pre-existing failure unrelated to the packet; or
- capability gating or removal of the 16/30/8 fallback is required to make the
  request-scoping tests pass.

For a pre-existing failure, include the failing command and enough output to
distinguish it from a regression; do not repair unrelated code.

## Handoff

Report:

- files changed;
- every HTTP calculation and persisted lookup moved to requested rules;
- the compatibility behavior retained for the configured current scope;
- every verification command and its outcome;
- any deviation from this packet;
- any `a.options.Rules` read that remains and why it is process-scoped; and
- any issue the fallback-removal or capability-aware packet should account for.
