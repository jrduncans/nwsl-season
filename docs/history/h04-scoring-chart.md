# H04 — Responsive, accessible goals-per-match chart

## Control

- Status: **Draft; dependency blocked**.
- Implementation: Luna. Review: Terra, including actual browser inspection.
- Prerequisite: H03 accepted, including selection and proxy-route tests.
- Blocks: H05; completes the first shippable view when accepted.
- Goal: make historical scoring visible immediately, with useful selected-year
  detail and complete supporting data at desktop and phone widths.

## Read first and allowed changes

Read [shared contract/checks](README.md), H03, the active history guide,
`internal/app/AGENTS.md`, and H03's delivered handler/view/template/tests.

Allowed: `internal/app/history_views.go`, `history_views_test.go`,
`history_handler_test.go`, `templates/history.html`, History-only rules in
`static/site.css`, and `docs/history-logic-guide.md`. A test-only preview helper
may be added in `internal/app/history_preview_test.go` under the shared rules.
No new production JS, packages, dependencies, external fonts, chart library,
route changes, cache changes or aggregation changes.

## Fixed rendering and interaction decisions

Use **server-rendered SVG** plus HTML controls. A small chart of roughly ten
seasons does not need a JavaScript framework or hydration. SVG anchor links
select a year through H03's canonical URL, so touch and keyboard work without
JS. This preserves IDEAS.md's progressive-enhancement requirement; it does not
make JS a prerequisite. Keep the native HTML selector as a redundant reliable
way to choose every year, including excluded years.

- Primary chart: goals per completed match on the y-axis, calendar season on
  the x-axis. Use actual year spacing, not row indices. Include 2020 as a
  labeled gap with `No regular season`; no point and no connecting segment.
- Fixed proposed SVG viewBox: `0 0 720 360`, responsive width, explicit axis
  titles and numeric tick labels. Reserve margins for labels; plot y from zero
  to `max(1, ceil(max eligible rate))` with one-goal ticks. With no eligible
  points, render the explanatory empty state instead of empty axes.
- Connect points only when the years are consecutive and both are completed
  lifecycle rows. No segment across an excluded/missing year. Active seasons
  are standalone diamond marks, with `through N matches` in visible detail.
- Verified-inventory historical points use circles with solid fill. Unknown
  inventory uses hollow circles; guide segments touching such a point are
  dashed. Include a visible key: `Inventory unverified` and `Active season`.
  This style is about source coverage, not statistical uncertainty.
- Neutral historical marks, one selected mark emphasized by an outline and
  color. Do not rely on color alone. If the selected year is excluded, keep its
  detail and selector state, explain why it has no point, and highlight no
  substitute. Selection never changes geometry/population/axis range.
- Visible year labels and a selected detail panel supply essential context;
  no required hover tooltip. Avoid labeling every rate if labels collide.
  Every point has an accessible link name with year, rate, match count and
  coverage status. Provide a chart description explaining axes, scope and
  selection, without declaring a trend or conclusion.
- Use a group-style accessible SVG container (not an atomic `role=img` that
  hides interactive descendants), associated title/description, and real SVG
  anchors. Verify point links are reachable by Tab and activate with Enter;
  visible focus must survive SVG clipping and CSS. Give mark links transparent
  hit targets of at least 24 CSS pixels at the phone width without overlapping
  adjacent targets. Give the native selector and submit button at least 44px
  control height for an easier touch alternative. Reduce horizontal plot
  margins on phones if necessary; do not silently hide or merge year marks.
- Selected detail is below the chart at all widths, keeping mobile behavior
  simple. `View data` is initially collapsed, retains every H03 row, and is
  reachable independently from the chart. Do not hide table rows for selection.

## Implementation steps

1. Build a deterministic chart projection from H02 summaries in the app view
   layer. Store mark/link/label data and individual line segments explicitly;
   do not compute coordinates in the template or aggregate scores again.
2. Keep numeric calculations unrounded until formatting SVG coordinates and
   visible text. Use existing html/template escaping; do not construct an
   untrusted template.HTML or inject raw query values into SVG markup.
3. Render SVG above selected detail and supporting data, with explanatory
   status text from H03 adjacent to it. Reuse H03 links/state exactly.
4. Add scoped responsive and focus styling. At 390px, if all x labels cannot
   fit, label first/last and alternating years plus 2020; every year remains
   accessible through marks, the selector, and the data table.
5. Verify browser behavior and adjust dimensions/spacing within these rules;
   record any adjustment in the guide. No animation is needed.

## Required tests and acceptance scenarios

Name new automated tests `TestHistory...` to match the focused command.

- Pure geometry tests: zero data, one point, all equal values, zero goals,
  ascending year positions, zero baseline, no NaN/Infinity coordinates.
- 2019 and 2021 with both eligible produce no connecting segment; no synthetic
  2020 value. A missing or excluded intermediate year also breaks the line.
- Completed/active and verified/unknown inventory marker/segment rules.
- Selecting a different eligible or excluded season does not alter the axes,
  point population or data. Only link/detail/selection presentation changes.
- HTTP response includes SVG, title/description associations, correctly
  escaped accessible names, relative year links and the complete HTML table.
- Browser at desktop (at least 1280px) and 390px: mixed historic/active data,
  selected excluded year, long context text, no eligible data, and a single
  eligible year. Check text legibility, focus ring, no page overflow, and table
  scrolling. Test tap/click, keyboard selector, keyboard mark links, JS off,
  page reload and Back navigation. Include `/nwsl-season/` mounted paths.
- Check the browser accessibility tree includes individual point links and
  their names; the table has proper headers/caption. Do not claim screen-reader
  testing unless actually performed. No essential information requires hover.

## Verification and handoff

```sh
NWSL_CONFIG_FILE=/dev/null go test -count=1 ./internal/app -run '^TestHistory'
```

Run shared Go checks when Go files change, `git diff --check`, and required
browser verification. Race is required only if concurrency-sensitive work was
introduced (outside this presentation contract). Record screenshot paths,
viewport sizes and keyboard/accessibility-tree results in the handoff.

Acceptance means the first release in IDEAS.md is usable: useful initial chart,
truthful samples/coverage, a shareable selection, and complete no-JS data.
Update the active guide to describe that delivered behavior. Do not implement
H05 during this task.

## Non-goals and stop conditions

No xG/distribution controls, team profiles, tooltips requiring JS, ranking,
annotations asserting a conclusion, or interactive chart framework. Stop for
unaccepted H03, incorrect aggregate contracts, required route changes or any
other scope conflict. A missing visual check remains an explicit release gap.
