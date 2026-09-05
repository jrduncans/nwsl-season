# H03 — History route, selection, and complete server-rendered data

## Control

- Status: **Ready**.
- Implementation: Terra. Review: Sol for HTTP integration and proxy paths.
- Prerequisites: H01 and H02 accepted; their interfaces match these contracts.
- Blocks: H04–H05.
- Goal: an accessible, cache-only HTML page on which H04 can add the chart.
- Release boundary: this is an integration foundation, not the finished
  chart-led release. H04 is required for the first product handoff.

## Read first

Read [shared contract/checks](README.md), H01/H02 and the delivered
`docs/history-logic-guide.md`, `internal/app/AGENTS.md`, `handler.go` (router,
optional store interfaces, `seasons`, `stripBasePath`, `relativeURL`, rendering),
`views.go`, `templates/partials.html`, `templates/seasons.html`, and app tests.

## Allowed changes

- Add `internal/app/history_handler.go`, `history_views.go`,
  `history_handler_test.go`, `history_views_test.go`, and
  `internal/app/templates/history.html`.
- Edit `internal/app/handler.go` only to register routes, support History in
  base-path handling, and provide the archive entry link described below.
- Edit `internal/app/views.go` only to add a History link field to `seasonsPage`.
- Edit `internal/app/templates/seasons.html` to add that link.
- Add narrowly scoped `.history-*` rules to `internal/app/static/site.css`.
- Update root `README.md` endpoint/documentation lists and
  `docs/history-logic-guide.md` to reflect the delivered route and data behavior.
- No changes to the common `Store` interface, existing season behavior,
  forecast executor, telemetry registry, dependencies, or cache/history logic.

## Fixed routes and state

- Canonical page: `GET /history/scoring`.
- `GET /history` redirects relatively to the canonical page with 303.
- Canonicalize trailing slash forms `/history/` and `/history/scoring/`
  without losing query parameters; ensure unknown History subpaths return 404.
- Initially only recognized state is optional `season=YYYY`. It selects detail,
  not a filter: the entire comparison population remains present.
- An absent season selects the newest plot-eligible **completed** season; if
  none, the newest eligible active season; if none, the newest row with scored
  matches; if none, no selection. Never silently pick an unrelated year when
  the URL explicitly requests a supported but unavailable year.
- A single four-digit supported catalog regular-season year is valid even if
  unloaded/excluded. Show its status and exclusion detail. Unsupported,
  malformed, blank explicit or duplicate `season` values return a useful 400.
- Ignore unrelated query keys; generated links emit only supported state.
  H05 reserves `metric=goals|xg` but H03 does not implement it.
- Build all query strings with `net/url`. Relative URLs must retain the
  external proxy prefix. Handle both rooted and `/nwsl-season/` requests.

Use a local optional interface:

```go
type historicalStore interface {
    HistoricalRegularSeasons(context.Context) ([]cache.HistoricalSeason, error)
}
```

Type-assert `a.store` as existing optional features do. A store lacking the
interface returns a clear 503 through the existing error presentation; do not
fall back to per-season reads. One request calls the archive API once, then
`history.SummarizeScoring` once. Errors use the existing error path; an empty
but successful archive is a normal 200 with explanatory content. Do not call
`loadSeasonPage`, Forecast Lab, qualification, or source loading to build History.

## Page contract and implementation steps

1. Build a History-specific view model compatible with shared head/footer.
   Use cross-season footer behavior (the existing `CatalogPage` convention),
   not a fabricated single data-fetch timestamp. Supply required common fields
   including navigation, relative asset paths and HomePath. The existing
   standings script may be loaded by the shared footer; do not modify it.
2. Add an obvious `Explore history` link on the Seasons archive. On History,
   expose a `Seasons` link and current `History` navigation item. Use `History`
   / `League trends` context and `Scoring by season` as the single h1. Avoid a
   global nav redesign or placeholder Team seasons/Match records tabs.
3. Display eligible years, regular-season scope, minimum sample, missing 2020
   explanation, and concise exclusions adjacent to the future chart area.
   Unknown inventory gets explicit per-row/detail text. Do not claim the whole
   archive was loaded or verified. Active rows say `through N matches`.
4. Add a labeled native GET season select and submit button. Every supported
   year appears, including unavailable ones. The full page works with JS off.
   Show selected year, played matches, total goals, goals per match, lifecycle,
   inventory status and exclusion reasons below the chart position.
5. Add a `View data` details element containing a semantic HTML table with a
   caption, column headers and season row headers. Include every catalog row,
   selected-year links, played matches, goals total, goals/match, status,
   inventory context, and exclusions. Show missing rates as `Unavailable`;
   valid zero is formatted as `0.00`. Explain displayed rates are rounded to
   two decimals; calculations retain full precision. Do not expose raw JSON.
6. In H03 the table/details may be open by default; H04 makes the chart primary
   and collapses supporting data. Do not add a fake chart placeholder.
7. Update the active guide and endpoint list, then verify route integration.

## Required tests

Use `TestHistory...` for HTTP tests, including relative links resolved as a
browser would resolve them, rather than mere substring checks.

- Seeded fake archive: 200, correct aggregates and status text, one archive
  read, no per-season/status/forecast calls (make unexpected fake calls fail).
- Real temporary SQLite integration: seed using cache write APIs; serve
  through NewHandler without constructing a sync service or scheduler; verify
  actual data arrives in the response. Network is unnecessary for this test.
- Empty data returns 200 with unloaded years; unsupported store returns 503;
  store and aggregation failures use the existing 500 path without leaking
  raw SQL, fixture payloads or user secrets into the response.
- Default selection hierarchy, explicit excluded year preserved, and each
  malformed/unsupported/duplicate selection returns 400. Selecting a season
  must not change the eligible set or aggregate values for other years.
- GET redirect and slash behavior, query preservation, unknown path 404, and
  unsupported method behavior consistent with the existing router.
- Run every path/redirect scenario at root and `/nwsl-season/`; resolve archive
  entry link, form action, year links, stylesheet and script against the page
  URL and verify they stay inside that mount. Existing season routes still work.
- Required headings/caption/headers, all rows, threshold and missing-year
  explanation, unknown inventory, active sample, invalid-completed and
  incomplete historical data copy. No invented final/all-time claims.

## Verification

```sh
NWSL_CONFIG_FILE=/dev/null go test -count=1 ./internal/app -run '^TestHistory'
```

Run shared Go checks plus the full race suite. Use a local synthetic preview
handler for desktop/390px browser verification: archive entry link, form,
selected detail, expanded data, keyboard and no-JS behavior. Table scrolling
may be local; the entire page must not overflow horizontally.

## Non-goals, stop conditions and handoff

No chart yet, xG display, global navigation redesign, asynchronous fetching,
source calls or global cache. Stop for unaccepted prerequisites, incompatible
shared template requirements needing wider edits, new telemetry semantics, or
files outside scope. Report the proxy-path and real-SQLite evidence in the
shared handoff. Do not mark the first release finished until H04 is accepted.
