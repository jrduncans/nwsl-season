# P1-04: Capability-aware factual HTTP

## Control

- Status: Ready
- Intended implementation model: GPT-5.6 Terra, high reasoning
- Required review: Sol
- Depends on: P1-01 competition catalog core and P1-03 request-scoped
  competition rules
- Blocks: season inventory/readiness APIs, historical catalog population, and
  public historical navigation

## Goal

Stop applying invented 16-team, 30-game, and eight-playoff-place assumptions to
an unknown cached season, and make HTTP calculations, routes, and navigation
honor the requested catalog entry's capabilities while continuing to serve
factual results, fixtures, team names, scores, and available xG.

## Why this packet exists

Phase 1 of the
[ASA data catalog and loading plan](../../asa-data-catalog-and-loading-plan.md)
requires an unknown or partially configured season to degrade explicitly rather
than inherit the current format. P1-03 made rules request-scoped, but its
temporary `presentationFallbackRules` still invents a full league format for
every unknown cached URL. As a result, those URLs can still show a fabricated
playoff line, qualification version, expected schedule size, and forecast.

P1-01 already made capabilities and inventory independent catalog values. This
packet makes `internal/app` consume that contract. It deliberately does not
decide whether a scope is loaded, loading, stale, or public: the existing cache
read remains the only data-presence check until the next packet adds persisted
readiness APIs.

## Fixed decisions

### Request competition metadata

Resolve one private request-scope value from:

- season: `r.PathValue("season")`, falling back to
  `Options.CurrentSeason` only when the path value is empty; and
- stage: `Options.Stage`.

The value must retain the season and stage, the defensive `competition.Entry`
returned by `competition.Lookup` when present, and whether the scope is
cataloged. The exact private type and helper names may follow surrounding
style. Do not put the value in request context or add a public application API.

Catalog metadata is authoritative for HTTP capabilities and inventory. An
explicit `Options.Rules` value for the configured current season remains the
achievement-rule compatibility override established by P1-03, but it does not
create a catalog entry, capability, inventory expectation, or public product.

### No HTTP fallback rules

Change the private rule resolver so an HTTP request can distinguish verified
rules from no verified rules. A suitable contract is:

```go
func (a *application) rulesForSeason(season string) (competition.Rules, bool)
```

Its selection is:

1. Look up `(season, a.options.Stage)` in the competition catalog. If the entry
   is absent or has nil `Rules`, return `competition.Rules{}, false`.
2. For a cataloged configured current season, return a defensive copy of
   `a.options.Rules` when that value is nonzero. This preserves explicit
   current-scope achievement rules used by the server and tests.
3. Otherwise return a defensive copy of the catalog entry's rules and `true`.

Delete `presentationFallbackRules` and do not replace it with another invented
rule set. `defaultOptions` must:

- retain the current season, stage, forecast, and location defaults;
- fill an empty `Options.Rules` from the catalog when verified rules exist;
- leave `Options.Rules` at its zero value when the configured identity has no
  verified rules; and
- validate `Options.Rules` only when it is nonzero.

Keep the exported `Options` type and constructors source-compatible. Keep the
existing tolerance for explicit current rules whose internal season string
differs from `CurrentSeason`.

`/cache/status` may report an empty rules version for a configured scope with no
verified rules. It must continue to report the configured primary season and
stage. Do not change its schema.

### Effective feature availability

Use exact catalog capabilities; do not infer a feature from stage kind, rules,
inventory, cached rows, or another capability.

The effective HTTP behavior is:

| HTTP surface | Availability contract |
| --- | --- |
| `/seasons/{season}` standings | Render standings only for `CapabilityStandings`. Verified rules are optional; without them render no playoff cutline or qualification badges. |
| Standings xG toggle and columns | Require `CapabilityXG`; a standings-capable entry without xG support renders only score-derived standings values. |
| `/seasons/{season}/fixtures` | Render for a catalog entry with `CapabilityFixtures`, and also for an uncataloged scope as the factual-only escape hatch. |
| Fixture xG values | Render for a catalog entry with `CapabilityXG`, and also for an uncataloged factual-only scope. |
| Fixture pre-match outlooks | Require `CapabilityForecast`; do not calculate or link them merely because factual fixtures are available. |
| `/schedule-difficulty` and the standings schedule column/indicators | Require `CapabilityScheduleDifficulty`. |
| `/forecast` | Require `CapabilityForecast` and verified request rules containing a playoff achievement. |
| `/clinching` | Require both `CapabilityQualification` and `CapabilityScenarios`, plus verified request rules. |
| `/model-evaluation` | Follow effective forecast availability because it is reached from Forecast Lab and is mounted under the requested season. |

An uncataloged scope does not acquire capabilities from `Options.Rules`, even
when it is the configured current season. This is required so a provisional or
operator-loaded scope remains factual-only until its product contract is
verified in the catalog.

Keep helpers deterministic and directly testable with invented `Entry` values;
do not mutate the production catalog or add fake production entries for
coverage.

### Factual-only season presentation

When an uncataloged requested scope has cached games:

- `GET /seasons/{season}` returns `200 OK` with the ordinary page chrome, a
  clear unknown-format notice, and a link to results and fixtures;
- it does not render a standings table, playoff line, qualification badge,
  schedule-difficulty indicator, completeness warning, or any 16/30/8-derived
  text;
- `GET /seasons/{season}/fixtures` returns `200 OK` and shows cached teams,
  kickoff times, statuses, scores, and available xG;
- the fixtures page repeats or retains a clear factual-only notice;
- it does not show schedule-completeness claims, fixture outlooks, a forecast
  link, or rule-dependent content; and
- navigation contains Results & fixtures only.

Use one concise product message consistently. Its exact punctuation may follow
template style, but it must say that the competition format is not verified and
that standings, forecasts, and clinching calculations are unavailable. Do not
call the scope unsupported, invalid, or empty when factual cached data exists.

A known catalog entry without standings follows the same no-standings page
shape, but its message should describe standings as unavailable for that scope
rather than claiming that the whole entry is unknown. A known entry without
fixtures does not receive the uncataloged factual escape hatch.

### Capability-aware navigation

Change the private season navigation builder to accept the resolved request
scope or an equivalent immutable availability value. Include links only when
their effective feature is available:

- Standings for standings capability;
- Results & fixtures under the fixture rule above;
- Schedule difficulty for its capability;
- Clinching scenarios only when both qualification and scenarios are
  effective;
- Forecast lab only when forecast is effective.

Model evaluation remains outside the shared navigation. Preserve current-link
marking, relative reverse-proxy paths, and declaration order for every included
item. The 2026 catalog entry must retain the current five-item navigation.

### Unsupported feature requests

Direct requests to a capability-gated page that is unavailable return a
rendered `404 Not Found`, not a redirect and not a generic `500` cache error.
The page must:

- identify the requested feature as unavailable for the requested season and
  stage;
- retain any factual navigation available for that scope;
- link back to the requested season using a reverse-proxy-safe relative URL;
  and
- use the existing error-page visual treatment where practical.

Perform the capability check before loading season data, parsing forecast
scenario state, looking up qualification/scenario snapshots, or submitting a
forecast task. An unsupported request must have no expensive calculation or
persisted derived-data side effect.

Do not change the existing behavior for a supported route whose cache read
fails or returns no games: it continues to use the existing season-unavailable
error path and status. An unmatched route continues to use the standard router
404 behavior.

### Inventory and derived calculations

For cataloged scopes, use `Entry.Inventory`, not the duplicated
`Rules.ExpectedTeams` and `Rules.GamesPerTeam`, for HTTP schedule-completeness
notes. Only state an expected team, per-team fixture, or total-fixture count
when the corresponding nonzero catalog inventory value is present.

For uncataloged scopes or catalog entries with nil/partial inventory, omit the
unknown expectation instead of deriving or guessing it. Observed factual
conditions such as an unsupported fixture status may still be reported on a
feature whose capability is enabled, but a factual-only page must not run
schedule-difficulty diagnostics merely to produce those notes.

Do not call `playoffPlaces`, standings cutline helpers, forecast helpers, or
qualification/scenario stores unless verified request rules and the required
capability are present. A standings-capable entry with nil rules still renders
ordinary positions and results with no playoff line. Qualification badges
require `CapabilityQualification`; their absence is not an error.

### Process-scoped forecast warming

`Application.PrecacheForecasts` and `PrecacheForecastsWithTrigger` remain scoped
to `Options.CurrentSeason` and `Options.Stage`, but must no-op successfully
before reading the store when the configured catalog entry lacks effective
forecast availability. They must not warm a forecast from zero or fallback
rules. Existing 2026 and explicit-current-rule precache behavior, forecast
cache keys, concurrency, deadlines, and telemetry remain unchanged.

The root redirect remains configured by `Options.CurrentSeason`. Scheduler,
syncer, qualification refresh, and scenario refresh construction remain out of
scope.

## Allowed changes

- Modify Go files in `internal/app` needed for private request metadata,
  capability gates, page construction, navigation, and forecast warming.
- Modify `internal/app/templates/season.html`, `fixtures.html`, `error.html`, and
  `partials.html` only as needed for factual-only, unavailable-feature, and
  independently capability-aware standings presentation.
- Modify existing `internal/app` tests for deterministic capability coverage.
- Update the packet index and P1-03 control status in documentation.

Do not modify CSS, JavaScript, `cmd/server`, `cmd/sync`, `internal/cache`,
`internal/competition`, scheduler, syncer, configuration, persistence schemas,
or production catalog data in this packet.

## Required behavior

- No HTTP path constructs or consumes `presentation-fallback-v1` rules.
- Unknown cached scopes remain useful for factual fixture, result, team, score,
  and xG presentation.
- Unknown scopes never show fabricated team counts, fixture counts, playoff
  places, schedule difficulty, qualification, scenarios, or forecasts.
- Catalog capabilities independently control their HTTP features and shared
  navigation entries.
- Catalog inventory, when present, owns HTTP completeness expectations.
- Unsupported feature requests return an explanatory 404 without touching the
  store or forecast executor.
- A cataloged standings scope without rules can render without a cutline or
  panic.
- The verified 2026 catalog scope preserves current supported pages,
  calculations, navigation, relative links, and explicit current-rule
  compatibility.
- Forecast warming skips a configured scope without effective forecast support
  and otherwise retains current behavior.

## Tests to add or update

Add deterministic tests covering at least:

1. `defaultOptions` uses 2026 catalog rules, preserves valid explicit rules,
   and leaves rules zero for an uncataloged configured identity; no fallback
   version remains.
2. The private request resolver returns no verified rules for an uncataloged
   current or non-current scope and retains the P1-03 defensive explicit-rule
   behavior for a cataloged configured current scope.
3. Effective feature helpers use exact capabilities with invented private test
   entries, including standings without rules, forecast without usable rules,
   and the uncataloged fixture/xG exception.
4. An uncataloged cached `/seasons/2099` request returns 200, explains the
   unknown format, links to fixtures, and contains no standings table,
   qualification badge, playoff line, schedule expectation, or 16/30/8 text.
5. The matching factual fixtures request renders scores and available xG but
   no schedule-completeness warning, outlook, forecast link, or rule-dependent
   navigation.
6. Direct uncataloged schedule-difficulty, forecast, clinching, and
   model-evaluation requests return explanatory 404 pages. A recording store
   and forecast executor prove no season read, persisted derived lookup, or
   simulation occurs.
7. Capability-aware navigation includes only allowed links, marks the current
   link, and keeps reverse-proxy-relative paths. The real 2026 entry retains
   Standings, Results & fixtures, Schedule difficulty, Clinching scenarios,
   and Forecast lab.
8. 2026 standings and forecast still use explicit configured current rules for
   playoff places, while completeness expectations come from the catalog
   inventory.
9. Qualification badges are read only when qualification capability and
   verified rules are effective; clinching requires both qualification and
   scenarios.
10. Unknown-scope forecast precaching returns nil before reading the store or
    starting work; existing current-scope zero-assumption precache tests remain
    intact.
11. Existing current-season handlers, invalid forecast scenarios, uncached
    season errors, standard route 404s, trailing-slash redirects, and
    reverse-proxy relative paths continue to pass.

Tests must not mutate the production competition catalog, make network
requests, depend on SQLite, or depend on the wall clock. Use invented unknown
seasons, private helper inputs, recording fake stores, and the real 2026 entry.

## Verification

Run all of these commands from the repository root:

```text
gofmt -w internal/app/handler.go internal/app/handler_test.go internal/app/views.go internal/app/forecast_handler.go internal/app/forecast_executor_test.go
go test ./internal/app
go test ./cmd/server
go test ./...
go vet ./...
git diff --check
```

If a listed Go file does not change, omit it from the `gofmt` arguments. All
other commands are mandatory.

## Non-goals

- Adding historical or playoff catalog entries or researching their formats.
- Making uncataloged or provisional scopes public.
- Adding a season selector or stage URL identity.
- Adding season inventory/readiness APIs or querying `source_scopes` from HTTP.
- Distinguishing loading, not-published, stale, and unavailable persistence
  states.
- Changing the existing error status for a supported route with missing cache
  data.
- Loading data during an HTTP request.
- Changing standings, forecast, qualification, or scenario algorithms.
- Changing forecast concurrency, model selection, state encoding, or cache-key
  semantics.
- Changing scheduler, syncer, server wiring, or source refresh behavior.
- Adding CSS or JavaScript for the new message states.
- Adding stage navigation, knockout presentation, or bracket support.

## Stop conditions

Stop and report without broadening the patch if:

- factual fixture rendering requires inventing a catalog entry or competition
  format;
- capability gating cannot occur before a source or derived-data store read;
- removing fallback HTTP rules requires changing scheduler, syncer, server
  wiring, a public URL, or a persistence schema;
- a catalog capability cannot be honored independently without changing the
  `competition.Entry` validation contract;
- the current 2026 routes cannot retain their behavior without reintroducing
  fallback rules; or
- the full suite exposes a pre-existing failure unrelated to the packet.

For a pre-existing failure, include the failing command and enough output to
distinguish it from a regression; do not repair unrelated code.

## Handoff

Report:

- files changed;
- the request metadata and effective capability rules implemented;
- which routes are factual-only, capability-gated, or unchanged;
- proof that no 16/30/8 fallback reaches HTTP calculations;
- the configured-current compatibility and precache behavior retained;
- every verification command and its outcome;
- any deviation from this packet; and
- any issue the inventory/readiness packet should account for.
