# Phase 7: Season archive and phase-aware presentation

## Control

- Status: Complete
- Intended implementation model: Sol with bounded Luna and Terra audits
- Required review: Terra
- Depends on: Phase 6
- Blocks: none

## Goal

Make every supported season discoverable without adding pressure to the global
section navigation, and make completed regular-season pages present final
standings and results instead of controls and labels intended for an active
season.

## Why this packet exists

Phase 4 added factual historical routes and view-model selector data, but the
selector UI was disabled because placing it in global navigation substantially
worsened that navigation, especially at mobile widths. Historical pages are
directly reachable but undiscoverable, and completed seasons still say
`Current standings`, default to per-game values, and expose an empty `Upcoming`
fixture view.

This packet follows the current cache-only HTTP boundary in
[`docs/sync-logic-guide.md`](../../sync-logic-guide.md), the factual historical
scope from [Phase 4](../phase-4/p4-historical-seasons.md), and explicit stage
URLs from [Phase 6](../phase-6/p6-basic-playoffs.md).

## Fixed decisions

- Add a cache-only `GET /seasons` archive page. It is the only page that reads
  all season readiness values; ordinary season pages add no readiness-list
  query.
- Add a compact, explicitly labeled season selector to the page-title content
  of primary-stage standings and results pages only. Switching seasons
  preserves whether the visitor is viewing standings or results. Do not add a
  season selector, stage selector, archive link, or another row to the global
  `.site-header` navigation, and do not expose the selector on features without
  historical analogues.
- Keep option values to cataloged season tokens and render the canonical
  relative Standings or Results destinations as anchors. Client code activates
  the anchor matching the selected token; it must not reinterpret a
  DOM-provided option value as a navigation URL or HTML.
- Keep `GET /seasons` as the cache-only catalog and no-JavaScript fallback, but
  do not promote it as a universal parent or breadcrumb from season features.
- Use one heading hierarchy across season subpages: the compact eyebrow names
  the requested season and stage, while the `h1` names the page function
  (`Standings`, `Results`, `Schedule`, `Forecast lab`, and so on). Keep the
  archive and error pages in their own context, and keep the shared page-title
  scale subordinate to the global site header and page content.
- The archive lists one row per public primary season in deterministic
  descending order. The configured current season is identified as current.
  Each available historical season links to its canonical standings and
  results pages. Keep additional stages, playoffs, and other competitions out
  of this regular-season archive until their historical discovery experience
  is designed together.
- Archive availability comes from the public competition catalog plus optional
  cache `SeasonReadinesses` data. Missing optional readiness support degrades to
  neutral links; it is not an HTTP error.
- Add an application-only `seasonPhase` presentation value. Do not add a cache
  column, migration, scheduler rule, or source-scope lifecycle mutation.
- Classify a loaded league-table scope conservatively:
  - no games is `unknown` and continues through the existing load-state path;
  - only scheduled games is `upcoming`;
  - completed and scheduled games is `active`;
  - no scheduled games is `complete` only when every game is terminal and the
    scope satisfies its verified inventory expectation, including team count,
    total game count, and per-team appearances;
  - every other combination is `unknown`.
- `FullTime` and `Abandoned` are terminal fixture statuses. An unknown status
  never proves completion.
- A complete scope containing an abandoned fixture is not safe to label `Final
  standings`, because the standings calculator excludes abandoned fixtures.
- Historical catalog entries currently have no verified inventory expectation.
  Their presentation may say `Historical standings`, default to totals, and
  omit an empty upcoming view, but must not claim the cached table is final.
  They do have a separately verified regular-season playoff cut line: top four
  for 2016–2019, top six for 2021–2023, and top eight for 2024–2025. This
  metadata draws the standings line without asserting a complete inventory,
  enabling historical calculations, or treating the full rules as verified.
  Source-scope lifecycle and the calendar year do not prove competitive
  completion.
- Treat the postseason cut lines as an authoritative fact, not an inference.
  The NWSL's [official playoff history through 2024](https://www.nwslsoccer.com/news/class-is-in-session-nwsl-playoffs-history-from-2013-to-2024)
  records the seeded fields for 2016–2024, and the
  [2025 Competition Rules](https://images.nwslsoccer.com/image/private/t_q-good/prd/kqbdfpjgywmisiyhhtnp.pdf)
  state that the top eight teams qualified. The existing model-evaluation
  protocol independently uses the same three cut-line eras.
- Static catalog capabilities remain the safety boundary. Presentation may
  hide a supported feature when it is irrelevant to a complete season, but it
  must not enable a capability the catalog omitted.
- Safely complete league-table pages use `Final standings`, default to totals,
  retain the optional per-game and xG controls, retain a verified playoff line
  and final qualification badges, and omit remaining-schedule UI. Historical
  factual pages use `Historical standings` and the same retrospective total
  default without asserting finality.
- Complete results pages use `Results`, omit the Results/Upcoming toggle and
  empty upcoming section, retain the team filter, and retain factual xG.
- Upcoming presentation must not show an all-zero standings table as useful
  current standings. It points visitors to the published schedule. Active and
  conservatively unknown scopes retain current behavior.
- The root redirect remains configured by `NWSL_SYNC_SEASON`; it never changes
  automatically at the calendar-year boundary.
- Every URL and redirect added by this packet remains relative so the
  `/nwsl-season/` reverse-proxy prefix survives.

## Allowed changes

- `internal/app/**`, including new templates and focused tests;
- `internal/competition/catalog.go` and its focused tests for the verified
  postseason cut-line field only;
- `README.md` for public route and behavior documentation;
- this packet and `docs/asa-loading/README.md`.

Do not add historical inventory or full rules, or edit cache persistence,
synchronization, scheduler, telemetry schemas, or forecast/qualification
algorithms.

## Required behavior

- `GET /seasons` succeeds using local catalog/cache data and makes every
  cataloged public primary season discoverable.
- The current five-link desktop and mobile section navigation is unchanged in
  content, wrapping, and placement.
- Loaded historical standings and results pages expose the page-preserving
  season selector and only their capability-supported, phase-relevant
  navigation.
- Standings, fixtures, schedule difficulty, clinching, Forecast Lab, and model
  evaluation use the same season/stage eyebrow and functional-page-title
  hierarchy at desktop and mobile widths.
- Not-loaded and not-published public season pages retain a successful response,
  use visitor-facing copy, expose the same season selector with an archive
  fallback when JavaScript is unavailable, and make no ASA request.
- Historical standings default to totals in both server-rendered
  values and JavaScript state, draw the verified season-specific playoff line,
  and do not render an unverified-format caveat. Switching to per-game and xG
  remains functional.
- A complete season has no schedule-difficulty, forecast, or clinching link in
  shared navigation even if its static catalog entry supports those features.
- A completed results page has no interactive `Upcoming` control or empty
  upcoming view.
- An upcoming loaded season emphasizes its schedule instead of an all-zero
  standings table. Schedule difficulty and forecast remain governed by their
  existing catalog/rule/input checks; clinching stays hidden while no match is
  complete.
- Playoff factual pages remain standings-free and do not infer bracket or
  advancement semantics.

## Tests to add or update

- Archive route order, current marker, canonical relative standings/results
  links, readiness labels, absence of additional-stage links, and absence of
  2020.
- Archive behavior when readiness listing is unsupported or fails.
- Conservative phase classification for upcoming, active, verified complete,
  incomplete/no-remaining, abandoned, and unknown-status fixtures, including
  exact team, game, and appearance inventory checks.
- Completed standings caption, historical standings caption, total-mode
  default, retained xG/per-game controls, verified historical playoff line,
  absence of the historical-format caveat, and phase-relevant navigation.
- Completed results heading, team filter, absence of fixture-view toggle, and
  absence of an upcoming section.
- Upcoming loaded season schedule call-to-action and absence of an all-zero
  standings table and clinching navigation.
- Current active-season output and five shared section links remain unchanged.
- Base-path behavior for `/seasons`, selector destinations, and canonical
  season destinations, including preserving the results subsection and keeping
  option values separate from navigation URLs.

## Verification

During implementation:

```text
go test -count=1 ./internal/app
```

Before handoff:

```text
golangci-lint fmt ./...
make lint
make vet
make test
govulncheck ./...
git diff --check
```

Visually verify the archive, current season, a completed historical season,
and completed results at desktop width and a 390px viewport. Confirm the global
header has not gained another row or control.

## Non-goals

- Automatically selecting a new configured season;
- adding 2027 competition metadata or rules;
- historical playoff catalogs, brackets, or inferred competition formats;
- changing historical loading, correction cadence, or ASA requests;
- making forecasts or clinching available for factual-only historical seasons;
- adding season switching to schedule difficulty, Forecast Lab, model
  evaluation, clinching, or non-primary stages;
- redesigning the standings table or global visual system.

## Stop conditions

Stop rather than broaden the packet if:

- a useful archive requires an HTTP-triggered ASA request;
- completion cannot be established without inventing an inventory or format;
- the season selector requires changing global header wrapping;
- completed presentation requires changing forecast, qualification, scheduler,
  or cache persistence semantics; or
- factual playoff presentation would require inferred bracket semantics.

## Handoff

Report exact files changed, behavior implemented, verification outcomes,
responsive visual findings, deviations, and unresolved follow-up. Do not mark
the packet Complete or commit without an explicit request.
