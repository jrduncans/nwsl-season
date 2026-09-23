# History calculation guide

The History views are calculated only from the coherent SQLite snapshot read by
`cache.DB.HistoricalRegularSeasons`; they do not refresh ASA data. This guide
records the delivered definitions for the first league-trends calculation and
applies the historical-data boundaries in [IDEAS.md](../IDEAS.md).

## Explore workspace

`GET /explore` reuses the same coherent archive read and scoring calculations.
It groups Scoring trend, Goal distribution, and Season table under League
scoring, with a separate Team performance analysis in the same workspace.
Team performance offers Comparison chart, Gap chart, Comparison table, and Team
history as direct views. The charts show one selected measure; the table shows
all three.
Each view heading states the regular-season scope.
`view=trend|distribution|table|teams|team-history` selects the initial surface.
JavaScript switches surfaces and Goals/xG/gap selections in place, sorts numeric table
columns from unrounded values, and restores those controls with Back/Forward.
Chart.js 4.5.1 renders the charts, with chartjs-plugin-datalabels 2.2.0 for
segment percentages. The pinned browser builds and MIT licenses are vendored
under `internal/app/static/vendor`; see its README for provenance and updates.
Chart libraries load locally; team logos use the same ASA image host as the
season pages. The chart payload retains unrounded values,
null for unavailable xG and goals-minus-xG gaps, and exact match counts from the
same archive snapshot. The payload identifies an eligible active season for
tooltip and keyboard inspection.

Scoring trend offers Goals, xG, Both, and Goals − xG under the same chart.
Goals − xG uses zero-centered signed bars on a symmetric scale, with positive
bars for goals above xG and negative bars for goals below xG. It uses the
full-precision season gap already shown in the Season table. Seasons without
complete xG have no bar; if none qualify, the chart shows an explicit empty
message. Goals and xG line modes retain a scale fitted to their values; the
hidden bar series does not force those nonnegative charts to start at zero.
Missing calendar years remain gaps in all modes. The selected metric
and view remain in the URL and restore through Back/Forward.

Hover/click/tap must intersect a dot, signed bar, distribution segment, or
team-history record line. A trend tooltip shows one series value, with a visible
mark highlight; axis years are not controls. Missing calendar years and
unavailable xG break the line. Distribution legends do not
filter or toggle bins. A segment tooltip shows goal count and match count; it
adds the percentage only when the segment is too narrow for a visible label.
Arrow keys inspect marks through Chart.js's active-element API and announce
values, including zero-count bins; Escape, focus leaving the chart, or an outside
tap clears inspection. The season table has shared-scale rate bars, signed gap
badges, row shading, and a sticky season column and header on small screens. Each view shows an explicit
empty state when no seasons are eligible.
Goal distribution also has a collapsed, server-rendered values table with
eligible seasons, played-match totals, and all five bins as counts and
one-decimal shares. It uses the Explore table styling and sortable headers.
Bin columns sort by the exact share of matches, before display rounding;
ties use newest season first. Sort links and the native disclosure work without
JavaScript, and a direct sorted URL opens the disclosure.

For chart changes, verify desktop and 390px layouts, hovering/tapping a dot or
signed bar versus empty space at the same year, per-segment tooltip content, keyboard
inspection and dismissal, missing-year/xG gaps, table sorting, and direct view
URLs plus Back/Forward. View changes must not fetch another document or data;
newly visible team logos may load. The Go Explore tests check the single snapshot read and chart payload's
missing-value and count semantics.

Explore uses the same navigation builder as the configured current season,
including its capability and season-phase rules, with Explore marked active.
The archive remains a no-script season-selector fallback, not a new header
destination. Existing season features retain their navigation level. The old History routes below remain compatible for existing links.
The workspace has no selected-season panel, inventory legend,
2020 callout, or methodology sections. It uses the same plot eligibility and
complete-xG requirements; the table includes only goals-eligible seasons and
keeps unavailable xG explicit. Scope is recorded regular-season results,
including an eligible in-progress year and its match count.

Future analyses must fit broader groups with data/metric sub-controls rather
than adding a top-level tab per metric.

### Team performance

`view=teams` compares actual and expected values per match for one regular
season. `season=YYYY` selects a catalog year; the default is the newest eligible
season, falling back to the newest catalog year when none qualify.
`measure=for|against|difference` chooses goals scored, goals allowed, or goal
differential on the chart. Goal differential is the default. Explicit unsupported years, malformed
or repeated season values, and invalid or repeated measures return 400.
Selections update locally and support direct URLs and Back/Forward.
`display=chart|gap|table` selects comparison views (paired-dot chart by default),
preserving the season and chart measure. The measure picker appears on both charts;
the table always includes all three measures. A small-screen cue identifies its
horizontal scroll. `team-sort` accepts `name`,
`played`, or a metric-qualified key such as `difference-gap`, `for-actual`, or
`against-expected`. Each of `difference`, `for`, and `against` supports `actual`,
`expected`, and `gap`. `team-order=asc|desc` controls ordering, defaulting to
`difference-gap` descending independently of the selected chart measure.
Legacy `actual|expected|gap` sort keys resolve using the URL's chart measure;
new sort links use explicit metric keys.
Missing expected values and gaps sort last in both directions; numeric sorting
uses full precision and ties use team name then ID. Sort headers are native
links, so sorting also works without JavaScript. Invalid or repeated display
and sort selections return 400.

The calculation reuses the single archive snapshot and validated scored-match
loop. Home and away appearances contribute to each team's goals for, goals
against, played count, and paired xG coverage. Unplayed, abandoned, and malformed
completed results do not contribute. Team identities must be nonempty and
distinct within each fixture. Each team needs full xG coverage of its own played
matches; other teams' missing observations do not hide its xG. Missing or invalid
xG leaves actual rates intact and expected rates and gaps unavailable. Zero xG
is valid. xPoints coverage does not gate the comparison.

Team comparison uses the league integrity checks with a one-result minimum
instead of the trend's 20-match minimum. Known-incomplete inventory, unavailable
source readiness, unknown/upcoming lifecycle, malformed results, or pending
fixtures in a completed season prevent a comparison. Unknown inventory is
permitted; the view describes recorded results rather than claiming a complete
fixture archive. A selected unavailable season remains selected with an empty
state. Only teams with valid played matches appear; teams use cached names and
fall back to their ASA identity. No cross-season franchise mapping is implied.

The paired-dot chart places actual and expected rates on one scale including
zero, ordered by actual rate (ascending for goals allowed, descending otherwise).
The Gap chart ranks teams by full-precision actual minus expected per match,
with signed bars around zero. It orders positive gaps first for goals scored
and goal differential, and negative gaps first for goals allowed. Green means
above expected and purple means below expected; neither color implies the
same performance judgment for every measure. Teams without complete xG appear
last with no bar, an `xG incomplete` label, and a named note accessible from the chart.
If every team lacks complete xG, the
chart shows an explicit empty message. The paired-dot chart remains available.
Goal differential is goals for minus goals against; xG differential is xG for
minus xG against. Gap always means actual minus expected. Lower/negative gaps
are favorable for goals allowed; higher/positive gaps are favorable for goals
scored and differential. These are descriptive comparisons, not forecasts.
Tooltip and keyboard inspection include both values, gap, and played count.
Both chart labels and table rows pair team logos with names; unavailable images
leave the names readable. Chart labels are HTML aligned to Chart.js row positions
on layout and resize. The table starts with Team and Played, followed by Goal
differential, Goals scored, and Goals allowed column groups, each with Actual,
xG, and Gap. Two header rows and contextual accessible sort labels identify
the groups. Team names stay fixed during horizontal scrolling on small screens.
A warning appears only when some teams
lack xG; coverage counts and routine coverage guidance are not shown. The native
HTML table and GET selectors provide the no-script fallback. Displayed values
round independently from full precision.

Run `make test-explore` for calculation and HTTP regression checks (also included
in `make test` and CI). For browser verification, the `teams` scenario in
`TestHistoryPreview` supplies 16 synthetic teams, full/partial xG, and an empty
season. Verify desktop and 390px layouts, signed differential and gap axes,
gap ranking for all three measures, touch/hover, keyboard inspection/dismissal,
logos and alignment after resize, missing-data warnings and `xG incomplete` labels,
sorting in both directions, no-script sorting, season/measure/display URLs,
Back/Forward, and switching analyses without fetching new data.

### Team history

Team performance also includes Team history (`view=team-history`). The `team`
selector chooses an ASA team ID from eligible recorded results across the
archive; it defaults to the first team by name, with ID breaking ties. The
selector uses the newest eligible name for each ID, so a rename keeps its
history. Distinct IDs remain separate even when names match; relocation or
franchise lineage is not inferred. Blank, repeated, and unknown team IDs return
400. `measure=for|against|difference` controls the chart, defaulting to goal
differential and sharing the season comparison's validation.

History reuses the eligible team-season rates and per-team xG coverage already
calculated for Team performance. A single recorded result can appear with its
played count. Goals and xG are per match; differential is scored minus allowed.
A native table always shows all three pairs of measures, newest season first
by default. Every column is sortable: `history-sort` accepts `season`, `played`,
or `difference|for|against` suffixed with `-actual` or `-expected`.
`history-order=asc|desc` selects direction. Sorting uses unrounded values,
keeps unavailable xG last in both directions, and breaks metric ties by newest
season first. Blank, invalid, or repeated sorting parameters return 400.
Header links toggle direction, expose the active order with `aria-sort`, and
work without JavaScript. Sort state is independent of the chart measure and
other Explore tables, survives team and measure changes, and supports direct
URLs and Back/Forward. The chart always retains chronological order.
The table keeps season labels compact; in-progress seasons are identified in
the chart tooltip and keyboard announcement.
The trend places seasons at calendar-year intervals and breaks lines for missing
or excluded seasons and unavailable xG. Its scale includes zero and supports
negative differentials. Point inspection includes the played count. Team and
measure selections update locally with shareable URLs and Back/Forward; the
GET form and full table remain usable without JavaScript. Empty archives have
an explicit empty state, and missing xG produces a warning without hiding goals.

`series=goals|xg|both` selects the history chart's series (both by default).
`context=on|off` adds league context (off by default). Blank, invalid, or repeated
values return 400. These controls preserve table sorting and work through direct
URLs, Back/Forward, and the GET fallback. Without context, Both overlays the two
series; with context, it shows two vertically aligned charts on the same scale.
A single-series selection uses one chart. The scale includes the selected team's
values, visible seasonal bounds, historical bounds, and zero.

League context uses the same eligible team-season rates from the single archive
snapshot, independently of the selected team. The shaded band spans each
season's lowest and highest rates, including an eligible active season labeled
in progress. Missing/excluded calendar years break the band. An xG season range
requires complete xG for every team in that season's comparison; partial coverage
withholds both bounds while leaving the selected team's own fully covered xG
available. Zero is valid, signed differentials remain signed, and extrema and
ties are compared before rounding. High/low refers to the numeric value, so a
low goals-allowed rate is favorable.

Dotted horizontal record lines use only eligible completed seasons. They span
the chart even for a team with only one season. Record labels identify the
available coverage start; they do not claim records outside the cached archive.
Active results cannot replace these records. For xG, only fully covered season
comparisons contribute. Missing xG context is explained visibly.

Hover/tap on a seasonal bound or team point shows the season's high and low,
holder names, year, rate, and played count. Hover/tap along a record line shows
its historical holders. Coincident seasonal dots retain season inspection;
record lines remain inspectable between dots. Keyboard arrows inspect all
season points and each historical bound once, with full holder announcements.
Escape, focus leaving a chart, and outside taps dismiss inspection. Tooltips
wrap on small screens and bound long tie lists; the expandable “League records
and season ranges” details list every tied holder and remain usable without
JavaScript. Names and IDs are retained from the record's season, including
historical names. No franchise mapping is inferred.

`make test-explore` covers history identity, rates, eligibility, missing values,
all history columns in both sort directions, record holders/ties and coverage,
URL validation, fallback HTML,
and the single cache snapshot. The `team-history`
scenario in `TestHistoryPreview` supplies multiple years with calendar gaps,
partial xG, and an active season. Verify desktop and 390px layouts, all three
measures, team selection, point hover/tap and empty-space dismissal, keyboard
inspection, table sorting (including missing xG), direct URLs, Back/Forward,
no-script sorting/forms and full context details, record-line and seasonal-bound
inspection, shared scales for Both, and switching between
history, season comparison, and league analyses without fetching new data.

## Legacy scoring page

`GET /history/scoring` is a cache-only History page. It reads the archive once
through `cache.DB.HistoricalRegularSeasons`, summarizes it once with
`history.SummarizeScoring`, and never refreshes a source or reads individual
season pages. `GET /history` redirects to the canonical route. An optional
`season=YYYY` selects detail without filtering the comparison population; every
supported regular-season catalog year remains visible, including unloaded or
excluded entries. `metric=goals|xg|compare` selects the chart metric; omitted metric is
Goals, and generated URLs omit the default `goals` value. Metric selection is
independent of season selection: with no explicit season, the page uses H03's
newest plot-eligible completed season, then eligible active season, then newest
season with scored matches even in xG mode. Blank, repeated, or other explicit
metric values are invalid.

The page leads with the chart and metric controls. Regular-season scope and
relevant exclusion warnings remain visible, while the eligible-year list,
20-match comparison threshold, and archive methodology live in an “About this
data” disclosure. The missing 2020 regular season remains annotated on the chart.
Season details prioritize goals per match, xG per match, and completed matches;
data completeness is a separate disclosure. Complete xG coverage means coverage
of recorded scored matches and never verifies fixture completeness. Rates and
the goals-minus-xG difference are rounded independently from full precision,
with a visible explanation beside the difference. Unknown inventory is labeled
as cached matches with unverified inventory, not as a complete archive. Exact values remain available in a native
HTML table without JavaScript; displayed rates round to two decimals while the
calculation retains full precision. The primary scoring view is a server-rendered
responsive SVG chart of goals per completed match by calendar season. Its plot
uses actual year spacing, leaves 2020 as a labeled regular-season gap, and
connects only consecutive eligible completed seasons. Verified inventory uses
solid circles, unknown inventory hollow circles with dashed guide segments, and
active seasons standalone diamonds. In Compare, the xG series uses blue, smaller
markers with heavier outlines, preserving hollow versus filled inventory
semantics; the active season also has a visible through-match-count note. Point links select the year through the
canonical relative URL; a native selector and collapsed exact-value table remain
available without JavaScript, and selected detail stays below the chart on
narrow screens. The 2020 axis gap is visibly annotated “No regular season”; on
phone widths the chart keeps a 600px minimum width within a keyboard-focusable
horizontal scroll region, with a visible swipe hint and readable typography.
Separate rows retain the year and gap labels;
Its transparent point hit targets remain at least 24 CSS pixels. The chart
uses the same vertical domain for Goals, xG, and Compare, normally 2.0–3.2,
expanding to include valid eligible values outside that interval. The visible
scale description and ticks explicitly identify this nonzero domain; no value
is clipped to the usual scoring range.

The metric choice is a Goals / Expected goals (xG) / Compare link group.
Compare overlays goals and xG on the same axes, retaining independently eligible
series: partial xG coverage never removes a valid goals point. Point and season
links preserve the chosen metric, and the native GET season form carries it as
an explicit hidden field. The selected season does not filter the chart. xG chart points require both the H02 `PlotEligible` flag and a non-nil `XGPerMatch`; complete xG coverage
is required for the displayed season average, while xPoints coverage remains an
independent reported count and never gates either chart. The selected season
remains selected when its xG is partial or unavailable, with coverage stated as
`K of N completed matches` and no substitute season selected. A fully covered
selected row reports goals/match, xG/match, and goals-minus-xG/match as
descriptive values only. The xG view lists excluded years and shows an explicit
empty state when no xG point qualifies; a normal Goals link preserves the
selected year.

The supporting data includes xG-covered/played and xPoints-covered/played
counts, xG and goals-minus-xG averages, and a separate captioned goal-
distribution table. Goal distributions use actual goals in the goals-eligible
population in all three metric views. Each season has a server-rendered 100% stacked
bar with five bins in order: 0, 1, 2, 3, and 4+ goals. Segment widths use count
divided by played matches; the visible table retains integer counts and
one-decimal percentages. Zero-played rates and percentages are shown as
unavailable, and displayed percentages may not sum to exactly 100% after
rounding. The bar's accessible name includes each bin's count and percentage.
Percentage guides show the common 0–100% scale, and each season has native
expandable exact values accessible by keyboard and touch without JavaScript.
The selected season is highlighted consistently in distributions and the data
table. These are shares of matches by combined actual goals, also in xG and
Compare mode; match totals provide the denominators.

## Scoring by season

`history.SummarizeScoring` accepts public, source-backed `Regular Season`
catalog entries with fixture capability. It keeps an output row for every input
season, including an unloaded source scope, and sorts rows by numeric season.
Cup, playoff, non-public, unavailable-source, and fixture-unsupported inputs
are rejected rather than mixed into league scoring.

One match is one cached fixture. A match is played only when its status is
`FullTime` and both scores are present and nonnegative. `GoalsPerMatch` is the
sum of home and away goals divided by played matches. Other statuses are
pending, except `Abandoned`, which has its own terminal count. A malformed
`FullTime` fixture is reported as an invalid completed result rather than being
scored. With no valid played matches, all rates are absent.

Goal bins are counts of matches with 0, 1, 2, 3, or 4-or-more combined goals.
The five bins therefore always sum to the played-match count.

## Expected-value coverage

xG and xPoints are joined to valid scored matches by fixture ID and matching
home/away team identity. They require the xG capability and an available
observation. xG coverage requires finite, nonnegative home and away xG;
xPoints coverage independently requires finite paired values in the inclusive
range 0 through 3. Zero is valid for either metric.

xG-per-match and goals-minus-xG-per-match are available only when every played
match has valid xG. xPoints coverage is retained for later work but this first
calculation does not derive an xPoints rate. Fixture inventory and expected
value coverage remain separate dimensions, as required by [IDEAS.md](../IDEAS.md).

## Chart eligibility

A season can be plotted only with at least 20 valid played matches, available
source readiness, a known non-upcoming lifecycle, no invalid completed results,
no known-incomplete inventory, and no pending fixture in a completed lifecycle.
Unknown inventory does not exclude a season; a consumer must label it as
unverified. Raw rates remain available for the supporting table even when a
season is excluded.

The stable exclusion codes, in display-independent order, are
`source_unavailable`, `lifecycle_unknown`, `upcoming`, `inventory_incomplete`,
`historical_results_incomplete`, `invalid_completed_results`, and
`below_minimum_matches`. Partial xG is deliberately not a scoring-chart
exclusion: goals data can still qualify, while a later xG view requires both a
plot-eligible season and complete xG coverage.

## UI verification

The opt-in `TestHistoryPreview` loopback harness supports an `overview` scenario
with ten synthetic seasons and all five goal bins, as well as partial, empty,
and single-season cases. Set `NWSL_HISTORY_PREVIEW_NO_SCRIPT=1` to block page
scripts using a preview-only Content Security Policy and verify native links,
season forms, and disclosures. This does not change application CSP or access
ASA. Run the harness with `NWSL_CONFIG_FILE=/dev/null`.
