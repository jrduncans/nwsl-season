# History calculation guide

The History views are calculated only from the coherent SQLite snapshot read by
`cache.DB.HistoricalRegularSeasons`; they do not refresh ASA data. This guide
records the delivered definitions for the first league-trends calculation and
applies the historical-data boundaries in [IDEAS.md](../IDEAS.md).

## Scoring page

`GET /history/scoring` is a cache-only History page. It reads the archive once
through `cache.DB.HistoricalRegularSeasons`, summarizes it once with
`history.SummarizeScoring`, and never refreshes a source or reads individual
season pages. `GET /history` redirects to the canonical route. An optional
`season=YYYY` selects detail without filtering the comparison population; every
supported regular-season catalog year remains visible, including unloaded or
excluded entries. `metric=goals|xg` selects the chart metric; omitted metric is
Goals, and generated URLs omit the default `goals` value. Metric selection is
independent of season selection: with no explicit season, the page uses H03's
newest plot-eligible completed season, then eligible active season, then newest
season with scored matches even in xG mode. Blank, repeated, or other explicit
metric values are invalid.

The page reports regular-season scope, the 20-match comparison threshold, the
missing 2020 regular season, lifecycle, inventory context, and stable exclusion
reasons. Unknown inventory is labeled as cached matches with unverified
inventory, not as a complete archive. Exact values remain available in a native
HTML table without JavaScript; displayed rates round to two decimals while the
calculation retains full precision. The primary scoring view is a server-rendered
responsive SVG chart of goals per completed match by calendar season. Its plot
uses actual year spacing, leaves 2020 as a labeled regular-season gap, and
connects only consecutive eligible completed seasons. Verified inventory uses
solid circles, unknown inventory hollow circles with dashed guide segments, and
active seasons standalone diamonds. Point links select the year through the
canonical relative URL; a native selector and collapsed exact-value table remain
available without JavaScript, and selected detail stays below the chart on
narrow screens. The 2020 axis gap is visibly annotated “No regular season”; on
phone widths the SVG typography is enlarged for legibility, with separate rows
for year and gap labels and the calendar-season title in the upper chart margin;
its transparent point hit targets remain at least 24 CSS pixels without overlapping adjacent
years.

The metric choice is a two-option Goals / xG link group. xG chart points require
both the H02 `PlotEligible` flag and a non-nil `XGPerMatch`; complete xG coverage
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
population in both metric views. Each season has a server-rendered 100% stacked
bar with five bins in order: 0, 1, 2, 3, and 4+ goals. Segment widths use count
divided by played matches; the visible table retains integer counts and
one-decimal percentages. Zero-played rates and percentages are shown as
unavailable, and displayed percentages may not sum to exactly 100% after
rounding. The bar's accessible name includes each bin's count and percentage,
so the presentation does not depend on color or hover.

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
