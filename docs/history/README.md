# History implementation packets

This is the implementation handoff for the first delivery in
[IDEAS.md](../IDEAS.md): **History → League trends → Scoring by season**.
These are proposed contracts, not documentation of shipped behavior. Planning
baseline: 2026-09-05. No application changes accompany these packets.

## Delivery and execution

Implement one packet per task, in order. H01–H04 produce the first shippable
view: a cache-only, responsive goals-per-match chart, selected-year detail,
shareable selection, and complete HTML data. H05 is a separately reviewable
follow-up adding an xG view and goal distribution. Do not wait for team names,
franchise identity, rankings, or forecasts to ship H04.

| Packet | Status | Suggested implementer | Review | Prerequisites |
| --- | --- | --- | --- | --- |
| [H01 Consistent archive read](h01-cache-read.md) | Complete | Terra | Sol | Existing cache/catalog helpers, verified during planning |
| [H02 Pure scoring summaries](h02-scoring-summary.md) | Draft; dependency blocked | Luna | Terra | H01 accepted |
| [H03 History HTTP and HTML](h03-history-page.md) | Draft; dependency blocked | Terra | Sol | H01–H02 accepted |
| [H04 Accessible scoring chart](h04-scoring-chart.md) | Draft; dependency blocked | Luna | Terra | H03 accepted |
| [H05 xG and scoring distribution](h05-xg-distribution.md) | Draft; dependency blocked | Luna | Terra | H04 accepted |

The model assignments follow the existing ASA packet practice: use Luna for
explicit calculation or presentation contracts, Terra for database/HTTP seams,
and a stronger review for integration. These are suggested task assignments;
this plan does not create tasks or delegate work automatically.

Paste this prompt into a new implementation task, replacing the packet path:

> Implement `docs/history/h01-cache-read.md`. Read its Control section and
> `docs/history/README.md` first, then the listed source files. Implement only
> that packet and preserve its fixed decisions. Run its focused tests and all
> required repository checks. Report changes, evidence, and any blocker. Do not
> commit, change packet status, or start another packet. If a contract conflicts
> with current code or needs a wider scope, stop and explain the conflict.

After implementation, use a separate review task:

> Review the implementation against `docs/history/<packet>.md` and the shared
> contract. Inspect the actual diff and independently verify its acceptance
> tests. Report findings first, with file/line evidence, then checks and residual
> risks. Do not implement the next packet or mark this one complete unless asked.

`Ready` means implementable now. `Draft; dependency blocked` means the design
is specified but preceding implementation must be accepted first. A review
of this plan does not complete any packet. The owner can authorize status
updates to `In progress`, `Review`, and `Complete`; keep this index and packet
Control sections consistent. Never execute dependent packets concurrently.

## Shared product contract

1. Only public, source-backed catalog entries whose stage is exactly
   `Regular Season` and which support `CapabilityFixtures` enter this slice.
   `Primary` alone is insufficient: the primary stage in 2020 is a cup.
   xG also requires `CapabilityXG`, independently of standings or forecasts.
2. Read SQLite only. No source refresh, scheduler change, new persistence,
   materialized aggregate, global result cache, background worker, or migration.
   Calculate from one coherent request snapshot, not separately timed per-year
   reads. Corrections appear on the next request; URLs do not freeze data.
3. Match means one fixture, counted once, not two team appearances. Count only
   `fixtures.CompletedStatus` with both nonnegative valid scores. Ignore other
   statuses even if they contain scores. Report malformed completed rows.
4. A chart point needs at least **20 completed, valid matches in that season**.
   This fixed presentation threshold gives several rounds of league matches
   across the catalog's different league sizes; it is a modest descriptive
   sample, not statistical significance.
   Counts and raw aggregates remain in supporting data below the threshold.
   No sample-size control. Do not invent confidence intervals.
5. Fixture completeness and expected-value coverage are different dimensions.
   Historical catalog entries currently have no `Inventory` expectations;
   `InventoryCompletenessUnknown` must remain unknown. Those observations may
   appear as **cached matches; inventory unverified**, using distinct chart
   styling. A known incomplete inventory is excluded from chart comparisons,
   with counts and reason visible. A full xG count never verifies inventory.
6. Use persisted lifecycle for active/upcoming/completed context, not just the
   year or the presence of a final score. Active labels say `through N matches`.
   A completed lifecycle with remaining nonterminal fixtures is shown as
   `historical results incomplete` and excluded from the chart. `Abandoned`
   fixtures are not scored and are terminal for this check. Missing lifecycle
   metadata is `season status unavailable` and excludes the point. Never label
   cached observations as a verified final record just because the year is old.
7. xG coverage counts valid paired home/away xG for eligible scored matches;
   xPoints coverage counts valid paired expected points separately. Zero is a
   valid value. Missing, negative, nonfinite, unavailable, or mismatched-team
   observations are not covered. xPoints must be in [0, 3] for each team.
   A metric uses **all the selected scored matches or is unavailable**. Do not
   calculate a displayed season xG average from a partial subset.
8. Title the page `Scoring by season`, under `History` and `League trends`.
   Say `Regular seasons since 2016 in the available archive`, list plotted
   years and exclusions, and explain the absent 2020 regular season. Do not
   call records `all-time`, infer causation, or generate answer paragraphs.
9. A useful page needs no configuration. No empty tabs for future subjects,
   chart builder, date filter, team selector, percentile/rank, or editorial
   question cards. Exact-value HTML and coverage explanations work without JS.
10. Keep all application links/assets/redirects relative, including at the
    `/nwsl-season/` proxy prefix. Selection is a normal GET URL, not localStorage.

These are explicit first-slice choices where IDEAS.md leaves room for design.
They do not set eligibility or minimum samples for later team-season views.

## Current implementation map

Read these source locations; their existing behavior is the integration anchor:

- `internal/competition/catalog.go`: `PublicEntries`, independent capabilities,
  historical regular seasons without inventory counts, and 2020 cup metadata.
- `internal/cache/season_readiness.go`: `seasonReadiness`, its transaction-aware
  helper, and unknown/incomplete/complete inventory states.
- `internal/cache/source_scopes.go`: persisted lifecycle and discovery values.
- `internal/cache/cache.go`: `queryer`, `SeasonData`, `Game`, `GameXG`,
  `loadSeasonData`, `seasonGames`, `seasonXGoals`, and fixture snapshot validation.
- `internal/fixtures/fixtures.go`: source status constants.
- `internal/app/handler.go`: optional store interfaces, `seasons`, router,
  `relativeURL`, `stripBasePath`, and rendering/error conventions.
- `internal/app/assets.go`: templates and static files already embedded by glob.
- `internal/app/templates/partials.html`: common head/footer and shared fields.
- `internal/app/AGENTS.md`: relative URLs and mandatory visual verification.

Historical loading already exists. The separate ASA packet P8-07 is in Review
at planning time, but its bootstrap timing change is not a dependency for these
cache-only views. Do not change its status or implement it here.

## Verification and handoff shared by every packet

Read root `AGENTS.md`, root `README.md`, [sync guide](../sync-logic-guide.md),
[ASA index](../asa-loading/README.md), IDEAS.md, and this file. For app work,
also read `internal/app/AGENTS.md`. Recheck applicable instructions at execution.
Inspect `git status --short` before editing and preserve unrelated user work.
If a required formatter touches unrelated pre-existing files, report the
conflict and keep those changes out of the packet; do not discard user edits.

After the final Go edit, run:

```sh
export NWSL_CONFIG_FILE=/dev/null
golangci-lint fmt ./...
make lint
make vet
make test
govulncheck ./...
```

Use `mise exec --` for provisioned tools if they are not on PATH. Lint must
finish cleanly. Report an unreachable vulnerability database as a skipped
scan, never a clean result. H01 and H03 require `go test -race ./...`; so does
any other packet that touches concurrency, including HTTP shared state or
concurrency-sensitive tests. Run `git diff --check` for every packet.

Template/CSS changes require actual desktop and 390px browser verification,
keyboard access, overflow checks, and JavaScript-disabled checks. Record the
browser, viewport sizes, scenarios and screenshot paths. Missing browser access
is an outstanding verification gap, not a passing visual check. Use synthetic
fixtures and an isolated local test handler; do not launch the normal server
against production data or trigger a backfill just to create chart screenshots.
An optional test-only preview harness in app test files may serve seeded data
on loopback when explicitly invoked; ordinary test runs must terminate normally.

Keep existing telemetry classifications and generated conventions. No new
registry dimensions/spans are required by these packets. Reuse the existing
HTTP/error path rather than bypassing it. If new registry semantics become
necessary, stop for a separately scoped amendment with the telemetry checks.

Each implementation updates its allowed active guide to describe delivered
behavior only. These packets add no repository-wide verification rule, so they
do not require speculative Makefile/CI/AGENTS changes. If a new rule is needed,
stop and amend the plan to update all four together as root AGENTS requires.

Handoff: packet ID, files changed, behavior, tests/commands and outcomes,
visual evidence where required, deviations/blockers, and whether acceptance
criteria are met. Do not claim a dependent packet is ready without its review.

## Next planning boundary

After H04, ship the goals view or execute H05. Then plan the team-season
collection separately. Before its first record/scatter view, settle historical
season names and club identity, eligible finished-season inventory, complete
xPoints coverage, shared-rank and percentile populations, and active
same-match-count selection rules. Before trajectories, define comparator and
historical-band calculations and samples at each match number. Do not infer
historical winners/qualifiers from today's tiebreak code. The remaining league
trends and match records stay candidates, not hidden requirements of H01–H05.
