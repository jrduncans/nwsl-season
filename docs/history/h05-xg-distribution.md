# H05 — Optional xG comparison and goal distribution

## Control

- Status: **Draft; dependency blocked**.
- Implementation: Luna. Review: Terra for missing-data and chart semantics.
- Prerequisite: H04 accepted and usable without this addition.
- Blocks: none; later collections need a separate planning pass.
- Goal: add underlying chance scoring and the shape of scorelines without
  turning the first view into a general chart builder.

## Read first and allowed changes

Read [shared contract/checks](README.md), H02–H04, the active history guide,
`internal/app/AGENTS.md`, and the delivered History implementation.

Allowed: `internal/app/history_handler.go`, `history_views.go`,
`history_handler_test.go`, `history_views_test.go`, `templates/history.html`,
History-only CSS in `static/site.css`, existing History test-only preview
helpers, and `docs/history-logic-guide.md`. No cache/domain changes, new JS,
dependencies, routing changes, registry changes, or other pages.

H02 already calculates the needed xG totals/rates, coverage counts and bins.
If its accepted implementation lacks them, stop and report the mismatch.

## Fixed state and display decisions

Add exactly one two-option **Goals / xG** metric choice, represented by
`metric=goals|xg`; absent metric means goals. Blank, repeated or other explicit
values yield 400. Update the season form to preserve metric, and all metric
and mark links to preserve the selected year. For an absent year, use H03's
goals-based default even on the xG view. Thus changing metrics does not silently
select a different season. Generated URLs include meaningful state only.

Keep the page title `Scoring by season`; the chart axis/title indicates `Goals
per match` or `Expected goals per match`. xG chart points require both
`PlotEligible` and non-nil `XGPerMatch`. A 19/20 covered season does not appear
in xG mode; never substitute zero or average the 19-match subset. Goals mode
is unaffected. xPoints availability never gates either of these chart modes.

If selected xG is unavailable, keep the selected year/detail and state
`xG available for K of N completed matches; a season average requires N of N`.
List excluded years near the chart. If none qualify, show an xG empty state
with a normal link to Goals. Do not automatically switch metrics. On fully
covered selected rows, show actual goals/match, xG/match and goals-minus-xG
per match with separate labels; this is descriptive, not a causal assertion.
No second line on the same chart and no dual axis.

Add a secondary panel **Goals in each match**, with a server-rendered 100%
stacked horizontal bar per goals-eligible season. At phone widths each season
occupies its own row, so there is no squeezed multi-year chart. Use five
non-overlapping bins in order **0, 1, 2, 3, 4+**; widths are count/Played, not
independently rounded percentages. Give a visible legend, recognizable segment
boundaries, and a non-color-dependent accessible name containing counts and
percentages. Do not force labels inside tiny segments. A zero-count bin remains
in the legend/table but needs no visible width. Each season label links to its
selection and preserves metric. The panel stays based on actual goals even in
xG mode, with that scope stated explicitly; xG gaps must not remove its rows.

Expand the supporting HTML data with xG-covered/played and xPoints-covered/
played counts, xG and goals-minus-xG averages (or unavailable), and the five
bin counts and percentages. To avoid a single enormous table, retain H03's
summary table and add a second captioned distribution table inside `View data`.
Use one decimal for percentages and explain rounding may not sum to exactly
100%; retain integer counts. Show unavailable percentages for zero played,
never 0% for a missing sample. Do not show an xPoints metric or control.

## Implementation steps

1. Extend validated query state with the two-value metric enum. Reuse one URL
   builder for form/mark/selector/metric links so selection survives each path.
2. Project the chosen already-computed rate and metric-specific eligible set
   into H04's chart geometry. Keep H04's active/inventory and gap conventions.
3. Add honest selected-year coverage and the explicit no-eligible-xG state.
4. Project bin percentages from H02 counts, then render the secondary SVG bars
   and semantic distribution table. No re-reading or re-aggregating fixtures.
5. Update the active guide's URL vocabulary, denominators and coverage examples;
   run focused/full checks and browser verification.

## Required tests

- A goals-eligible row with 19/20 xG is excluded only from xG plotting; goals
  point, score distribution and selected detail remain. Complete xG but absent
  xPoints qualifies for xG. Valid zero xG renders as zero, not unavailable.
- All xG unavailable: explicit xG state preserved, no fake zero line, links to
  Goals preserve the year, and supporting data remains accessible.
- Missing/valid/invalid/duplicate metric query cases; selection survives Goals
  → xG → Goals, season form submission, mark links and page reload. An excluded
  selected year is not replaced with a qualifying neighbor.
- Hand-counted bins [1,1,1,1,1] show 20% each; multiply the five-match H02
  fixture set four times with unique IDs to get a plot-eligible 20-match sample.
  Additional tests cover only 0-goal matches, only 4+ matches, zeros in middle
  bins, and thirds whose displayed percentages sum to 99.9%.
- Bars use the goals population in both modes, preserve ascending year order,
  and use actual counts for widths. The distribution never counts a match twice.
- Root and proxy-prefixed URLs preserve metric and year across all controls.
- Desktop and 390px browser checks: goals and xG, partial and zero coverage,
  excluded selected year, all-in-one-bin distribution, keyboard/touch, no JS,
  table expansion and long context text. Verify both tables' header/caption
  semantics and no essential information available only by color or hover.

## Verification and handoff

```sh
NWSL_CONFIG_FILE=/dev/null go test -count=1 ./internal/app -run '^TestHistory'
```

Run shared Go checks and required browser verification. Because the handler's
request-state parsing changes, also run `go test -race ./...` for this packet's
HTTP integration gate. Record both metric states and distribution screenshots,
coverage tests and URL round-trip results. Do not advance into team seasons.

## Non-goals and stop conditions

No expected-points scatterplot, user thresholds, date ranges, scoring-era
adjustment, extra trend panels, home advantage/parity, or claims about luck.
Stop for unaccepted H04, any aggregate/API mismatch, or required edits outside
scope. Do not fix a domain bug by compensating in presentation.
