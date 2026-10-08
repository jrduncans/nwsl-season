# History calculation guide

The History views are calculated only from the coherent SQLite snapshot read by
`cache.DB.HistoricalRegularSeasons`; they do not refresh ASA data. This guide
records the delivered definitions for the first league-trends calculation and
applies the historical-data boundaries in [IDEAS.md](../IDEAS.md).

## Explore workspace

`GET /explore` reuses the same coherent archive read and scoring calculations.
Its navigation groups views by scope, ordered from the current season to
league history. Compare teams holds every team in one season: Actual vs
expected (paired dots), Gap to expected, Outlier plot, Scored vs allowed, and
Table. Team profile holds Rankings, Match by match, and Season by season for a
selected team; its group link opens Rankings. League trends holds Scoring
trend, Goal distribution, and Scoring table across seasons. Only the selected
group's views are listed. The comparison charts show one selected measure;
Table and Season by season show all four. Rankings shows all six goal and xG
ranks together.
Each view heading states the regular-season scope.
`view=trend|distribution|table|teams|team-rankings|season-trend|team-history` selects the initial surface.
An omitted view opens Compare teams' Actual vs expected for the default
season, matching the current-season focus of the other site sections. Explicit
`view=trend` links still open Scoring trend, as do older view-less links that
carry `metric`, from when Scoring trend was the default.
JavaScript switches surfaces, Goals/xG/gap selections, and goal-bin chart modes
in place, sorts numeric table columns from unrounded values, and restores those
controls with Back/Forward.
Chart.js 4.5.1 renders the charts, with chartjs-plugin-datalabels 2.2.0 for
segment percentages. The pinned browser builds and MIT licenses are vendored
under `internal/app/static/vendor`; see its README for provenance and updates.
Chart libraries load locally; team logos use the same ASA image host as the
season pages. Explore data retain unrounded values, null for unavailable xG or
xPoints and their gaps, and exact match counts from the same archive snapshot.
The chart payload identifies an eligible active season for tooltip and keyboard
inspection.

Scoring trend offers Goals, xG, Both, and Goals − xG under the same chart.
Goals − xG uses zero-centered signed bars on a symmetric scale, with positive
bars for goals above xG and negative bars for goals below xG. It uses the
full-precision season gap already shown in the Scoring table. Seasons without
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
tap clears transient inspection. The Outlier plot's pinned details remain after
focus leaves or an outside tap, until the point is cleared, Escape is pressed,
or its chart selection changes.
The season table has shared-scale rate bars, signed gap
badges, row shading, and a sticky season column and header on small screens. Each view shows an explicit
empty state when no seasons are eligible.
Goal distribution also has a collapsed, server-rendered values table with
eligible seasons, played-match totals, and all five bins as counts and
one-decimal shares. It uses the Explore table styling and sortable headers.
`distribution-bin=all|0|1|2|3|4` selects the stacked distribution or a line
showing one goal total's share of played matches by season (`4` means four or
more goals). The stacked chart remains the default. Both teams' goals count
toward the match total. The line uses exact bin counts divided by exact played
matches, retains zero-count seasons as zero points, and breaks at missing
calendar years. All five selected bins use the same percentage axis, from
zero to a rounded upper bound based on the largest share across every bin and
season, with evenly spaced 5-, 10-, or 20-point ticks according to that bound.
Tooltips and keyboard inspection
state the selected bin, count, total played, and share; active seasons are
marked in progress. The selector and chart mode restore through Back/Forward.
Bin columns sort by the exact share of matches, before display rounding;
ties use newest season first. Sort links and the native disclosure work without
JavaScript, and a direct sorted URL opens the disclosure.

For chart changes, verify desktop and 390px layouts, hovering/tapping a dot or
signed bar versus empty space at the same year, per-segment tooltip content,
selected-bin counts and shares, zero-count points, keyboard inspection and
dismissal, missing-year/xG gaps, table sorting, and direct view and bin URLs
plus Back/Forward. View changes must not fetch another document or data;
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

### Compare teams

`view=teams` compares actual and expected values for one regular season.
`season=YYYY` selects a catalog year; the default is the newest eligible
season, falling back to the newest catalog year when none qualify.
`measure=for|against|difference|points` chooses goals scored, goals allowed,
goal differential, or points on the chart. Goal differential is the default.
`units=per-match|total` controls the within-season comparison across the
paired-dot chart, Gap to expected, Outlier plot, and table. Per match is the default;
totals sum the same selected team's recorded regular-season matches. Explicit
unsupported years, malformed or repeated season values, and invalid or repeated
measures or units return 400. Selections update locally and support direct URLs
and Back/Forward. `display=chart|gap|scatter|quadrant|table` selects comparison views
(paired-dot chart by default), preserving the season and chart measure. The
measure picker appears on Actual vs expected, Gap to expected, and Outlier plot; the table always includes all four
measures. A small-screen cue identifies the table's horizontal scroll.
`team-sort` accepts `name`, `played`, or a metric-qualified key such as
`difference-gap`, `for-actual`, `against-expected`, or `points-gap`. Each of
`difference`, `for`, `against`, and
`points` supports `actual`, `expected`, and `gap`. `team-order=asc|desc` controls
ordering, defaulting to `difference-gap` descending independently of the
selected chart measure.
Legacy `actual|expected|gap` sort keys resolve using the URL's chart measure;
new sort links use explicit metric keys.
Missing expected values and gaps sort last in both directions; numeric sorting
uses full precision and ties use team name then ID. Sort headers are native
links, so sorting also works without JavaScript. Invalid or repeated display
and sort selections return 400.

The calculation reuses the single archive snapshot and validated scored-match
loop. Home and away appearances contribute to each team's goals for, goals
against, played count, actual points (three for a win, one for a draw, zero for
a loss), and paired xG and xPoints coverage. Unplayed, abandoned, and malformed
completed results do not contribute. Team identities must be nonempty and
distinct within each fixture. Each team needs full xG coverage of its own played
matches; other teams' missing observations do not hide its xG. Missing or invalid
xG leaves actual goals intact and expected goals and gaps unavailable. xPoints
coverage is independent: a team needs valid, paired ASA xPoints for each of its
played matches to show xPoints and the points gap, regardless of its xG coverage
or that of another team. Missing or invalid xPoints leaves actual points visible.
The xPoints value is the retrospective ASA observation for each recorded game;
it is not a Forecast Lab projection of final points. Zero xG and zero xPoints
are valid.

Team comparison uses the league integrity checks with a one-result minimum
instead of the trend's 20-match minimum. Known-incomplete inventory, unavailable
source readiness, unknown/upcoming lifecycle, malformed results, or pending
fixtures in a completed season prevent a comparison. Unknown inventory is
permitted; the view describes recorded results rather than claiming a complete
fixture archive. A selected unavailable season remains selected with an empty
state. Only teams with valid played matches appear; teams use cached names and
fall back to their ASA identity. No cross-season franchise mapping is implied.

The paired-dot chart places actual and expected values in the selected units on
one scale including zero, ordered by actual value (ascending for goals allowed,
descending otherwise). Gap to expected ranks teams by full-precision actual minus
expected, with signed bars around zero. It orders positive gaps first for goals
scored, goal differential, and points, and negative gaps first for goals allowed.
Green means above expected and purple means below expected; neither color
implies the same performance judgment for every measure. Teams without complete
xG appear
last for goal measures with no bar, an `xG incomplete` label, and a named note
accessible from the chart. For points, the same behavior follows xPoints
coverage and uses an `xPoints incomplete` label. If every team lacks complete
coverage of the selected expected value, the chart shows an explicit empty
message. The paired-dot chart remains available.
The Outlier plot places each fully covered team's expected value on the
horizontal axis and actual value on the vertical axis. Its diagonal marks equal
actual and expected values: points above it are above expected (green), and
points below it are below expected (purple). Both axes use the same numeric
range, fitted to the selected teams' actual and expected values with room around
the points. Both scatter plots fit the visible extrema with 5% padding on each
side, including the quadrant averages. They do not force a symmetric distance
from the average, since an outlier on one side would add empty space on the other.
Tied populations use a small nonzero range; nonnegative measures stop at zero.
The square Outlier plot grows with the page to at most 1200 CSS pixels. On
desktop, its size is capped near the viewport height, with a 608px minimum so
the plot stays usable in shorter windows. Small screens size it to the
available width. Its above/below/parity key sits in the chart frame directly
above the square plot and uses circles for above/below points and a dashed line
for parity. Redrawing or clearing a tooltip keeps the plot bounds fixed.
A short note explains above, below, and distance from the line for
the selected measure. Positive measures do not have to start at zero; signed goal
differentials can extend below zero. Teams without complete xG for goal measures,
or xPoints for points, have no point and are named in a note accessible from the
chart. If none are fully covered, the plot shows an explicit empty message.
Hover and tap inspect points and report actual, expected, gap, and played count.
Clicking or tapping a point pins its team and values in a details card below the
plot, so the information remains visible after the pointer moves. Gold guide
lines extend across the plot through the selected values. Two smaller hollow
circles on the equal-actual-and-expected line let the reader move both guides to
the selected team's expected or actual value. The details card names the
measures and shows the guide values. Clicking empty plot space, Clear selection,
or Escape clears the pinned point. Team logos are shown by default;
the Show team logos control can hide them. Crests appear beside points where
they do not overlap another crest or point. Team names remain available in
tooltips and the pinned details card; with logo labels off, a pinned point also
gets a text label. Keyboard inspection
reaches every plotted team; a link below the plot opens the Table view for
all teams, including those whose points overlap. The season and measure
selections, direct URLs, and Back/Forward work as on the other comparison views.
Scored vs allowed (`display=quadrant`) plots scored vertically against allowed
horizontally. The horizontal axis runs from high to low, so up/right means more
scored and fewer conceded. Both axes share a fitted numeric range and the same
units. The square chart grows with the page to at most 1200 × 1200 CSS pixels
and is capped near viewport height with a 608px minimum on desktop; small
screens retain a square within the available width. Corner labels describe
the four quadrants from reserved bands outside the data area; compact charts omit
upper-corner captions where they would collide with the centered key. Dashed
dividers show the average in the selected units.
`quadrant-data=goals|xg|both` defaults to Goals and is independent of the retained
comparison measure. Goals uses one green circle per team; xG uses purple diamonds
only for teams with complete paired xG coverage. Both shows the two points joined
by a line per covered team. A key in the square plot's top margin identifies the
Goals and xG marks and the connecting line. Goals remains visible for teams
without xG; a named note explains missing points and connectors. xG shows an explicit empty state
when no teams qualify. Missing xPoints never affects this chart.

For Goals and Both, dividers use the league goals average across all eligible
teams. Both uses this single reference for both series. For xG, dividers use
the xG average of fully covered teams, explicitly labeled as such. Per-match
averages divide summed team totals by summed played appearances, so uneven
played counts weight correctly. Totals use the arithmetic mean team total and
note that teams may have different played counts. No rounding precedes averaging
or positioning. The visible note gives scored/allowed averages and their units.

Hover/tap and keyboard inspection report team identity, goals and xG scored and
allowed, played count, and in-progress status. Click pins all teams at an
overlapping point in details cards and highlights their connectors; empty-space
clicks, Clear selection, and Escape clear the selection. Team logos are optional,
with overlap suppression against both series. Arrow keys reach all available
marks, including coincident teams, and skip missing xG. Season, data mode, units,
and display restore through Back/Forward and shareable URLs without new requests.
The Table view and GET controls remain the no-script fallback.

In both scatter plots, point radii adapt uniformly from 6 to 10 CSS pixels when
the nearest-point spacing and plot edges allow it. Crowded or coincident points
retain the minimum size and remain individually keyboard-inspectable. Logo
placement first reserves 22px boxes, then grows each to at most 48px on large
plots, checking every other logo, all visible points in both series, hover
clearance, and plot boundaries. Enlargement does not displace a previously
placed logo. Recalculate sizing and placement after data, units, mode, and
viewport changes; marker size is a layout choice, not a statistical encoding.

Goal differential is goals for minus goals against; xG differential is xG for
minus xG against. Points are earned from recorded results, and xPoints are ASA's
expected points summed over those same matches. Gap always means actual minus
expected in the selected units. Lower/negative gaps are favorable for goals
allowed; higher/positive gaps are favorable for goals scored, differential, and
points. These are descriptive comparisons of recorded matches, not forecasts.
Tooltip and keyboard inspection include both values, gap, and played count.
The paired-dot and Gap to expected labels and table rows pair team logos with names;
unavailable images leave the names readable. Those chart labels are HTML aligned
to Chart.js row positions on layout and resize. The Table view starts
with Team and Played, followed by Goal differential, Goals scored, Goals
allowed, and Points groups, each with Actual, expected (xG or xPts), and Gap
columns. Contextual accessible sort labels identify the measure and values.
Team names stay fixed during horizontal scrolling on small screens. Warnings
identify missing xG, xPoints, or both for the table.
Coverage counts and routine coverage guidance are not shown. The native HTML
table and GET selectors provide the no-script fallback. Displayed values round
independently from full precision.

Run `make test-explore` for calculation and HTTP regression checks (also included
in `make test` and CI). For browser verification, the `teams` scenario in
`TestHistoryPreview` supplies 16 synthetic teams, full/partial expected-value
coverage, and an empty season. Verify desktop and 390px layouts, signed
differential and gap axes, gap ranking for all four measures and both unit modes,
the Outlier plot's equal axis ranges and parity diagonal for all four measures,
positive and negative gaps, Scored vs allowed's reversed allowed axis, average
dividers and weighted per-match versus total baselines, goals/xG/both modes,
connecting lines and missing xG, tight/tied/zero bounds, adaptive uniform point
sizes and two-pass logo growth/collisions, viewport-capped Outlier sizing and
circular Outlier markers in its key, the in-chart Goals/xG key, and square
mobile layout, plot bounds after tooltip redraws and dismissals,
coincident points, touch/hover, pinned details and
axis guides, clearing a pinned point, optional logo labels with overlap
suppression, keyboard inspection/dismissal, logos and alignment after resize,
missing-data warnings and `xG incomplete` or `xPoints incomplete` labels, the
named Outlier plot omission note and empty state, sorting in both directions,
no-script sorting, season/measure/units/display URLs, Back/Forward, and switching
analyses without fetching new data.

### Rankings

`view=team-rankings` shows one team's goals scored, goals allowed, goal
differential, xG, xG allowed, and xG differential together. It shares the
validated season, team identity, and `units=per-match|total` controls with the
other team views. The default team and cross-season identity follow Team
history; a team with no eligible results in the selected season remains
selected with an empty state. Headings use the name from the selected season.

Ranks compare unrounded values among every team with eligible recorded results
in that season. Higher values rank better except for goals allowed and xG
allowed, where lower values rank better. Equal values share competition rank
(one plus the number of strictly better values); later ranks skip tied places.
The six cards show the value, ordinal rank, and season's team count. A marker
runs from best (1st) to last (the team count), using the same rank direction for
all six stats. This depicts rank rather than statistical distance. A one-team
population places its first-place marker at the best endpoint. Per-match ranks
use each team's own played count; total ranks compare sums over recorded games.
Active seasons show an in-progress label and the selected team's played count.

League xG ranks require complete xG for every team in the season's comparison.
If any team's recorded-match xG coverage is incomplete, all three xG ranks and
markers are withheld with an explanation. Fully covered teams retain their
own xG values. Goal ranks remain available. Season integrity and cache-only
boundaries match Compare teams, using the same single archive payload.
The GET selectors and six cards work without scripts; JavaScript changes them
locally with shareable URLs and Back/Forward, without another data request.

Run `make test-explore` for rank direction, ties and skipped places, full-precision
comparisons, unit changes with uneven played counts, signed and zero values,
league xG coverage, empty selections, single-team populations, fallback HTML,
URL validation, and single-snapshot checks. Use the `teams` preview scenario to
verify desktop and 390px layouts, selector keyboard access, all six cards,
season/team/unit changes, direct URLs, Back/Forward, missing xG, empty seasons,
and no-script forms.

### Match by match

`view=season-trend` follows a selected team's completed regular-season matches
within `season=YYYY`. It shares the validated season, team identity, and
`series=goals|xg|both` selections with other team views, defaulting to the newest
eligible season, the first eligible team by name, and both series.
The Chart picker (`trend-view=balance|compare|difference|points|relative`)
defaults to Scoring balance,
showing scored and allowed together. Actual vs xG compares the two bases within
Scored and Allowed panels; Differential compares goal and xG differential on
one plot. Points vs xPoints compares earned match points (3/1/0) with ASA's
retrospective xPoints on one plot. These describe played matches, not forecast
final points. Benchmarks shows original values with team-mean and optional
comparison references. The retained `relative` value preserves existing links.

The Show picker appears in Scoring balance and Benchmarks. Scoring balance
retains the shared `series=goals|xg|both` preference. Benchmarks uses
`average-series=goals|xg|points|xpoints`, defaulting to xG when the shared
preference is xG and to Goals otherwise. Goals and xG use Scored and Allowed
panels; Points and xPoints use one Points panel. Hidden controls retain both
preferences in the URL and GET form, so switching views restores selections.
Comparison, differential, and Points vs xPoints always include both relevant
bases. Blank, repeated, or unsupported average-series selections return 400.
`average-reference=team|league|playoff|top-four|shield|best` defaults to League
average. The Benchmark selector replaces the single-series legend above the
Benchmarks charts and remains associated with the GET form. `team` means
None (team average only); every other selection adds one comparison to the
team's own average. Choices are retained across views and Back/Forward; blank,
repeated, or unsupported references return 400.
The season trend selection
is independent of `measure` and `units` in other team views. The team picker retains IDs
across seasons and uses their newest eligible names, while the heading and
opponents use names from the selected season. A selected team without eligible
results in a selected season stays selected with an explicit empty state.

The Smoothing picker (`trend-mode=match|rolling`) defaults to Rolling average;
None (each match) plots individual match values. It is labeled apart from
the per-match/total Values picker in other team views. `window=3|5|10` defaults
to five matches; its control is hidden in per-match mode. Match values and rolling
averages are separate modes, with lines and points in both. Blank, unsupported,
or repeated trend views/modes/windows return 400. Selections update locally from the
same single archive payload, with shareable URLs, Back/Forward, and a GET form
fallback.

The data reuse the scoring loop's validated results and paired xG observations
and the same season eligibility as Compare teams. Upcoming, abandoned, or
invalid results do not enter the series; known incomplete inventory and other
season integrity exclusions withhold the view. Home and away appearances are
oriented to the selected team. Differential is scored minus allowed for both
goals and xG. Individual xG observations remain available even when that team's
season-wide xG comparison is unavailable. Invalid, missing, unavailable,
wrong-team, or capability-disabled expected observations leave null values, never
zero. Per-match xPoints follows its own paired validation and coverage, independently
of xG; home/away orientation also applies to points and xPoints.

Match numbers follow kickoff instants, using `fixtures.ParseKickoff` for both
RFC3339 and ASA’s `2006-01-02 15:04:05 MST` timestamps, so representation and
offsets do not change order. Ties use fixture ID. Missing/invalid dates sort
last by ID with an explicit warning that their position may not be played order.
Dates display in the venue’s local calendar, using the cached fixture’s
`stadium_id` and the checked-in IANA zone catalog in `internal/fixtures/venues.go`.
Home/away teams, the browser’s zone, and server configuration do not choose the
zone. Historical daylight-saving rules apply; embedded `time/tzdata` also makes
this work without system zoneinfo. Unknown venues show `Venue date unavailable`
and a separate notice, while valid kickoff instants still determine ordering.
There is no UTC or usual-home-stadium fallback, including at neutral venues.

The catalog covers all 49 entries from the [ASA NWSL stadia endpoint](https://app.americansocceranalysis.com/api/v1/nwsl/stadia),
checked on 2026-10-01, including historical and alternate venues. ASA IDs and
geographic fields are the primary source; entries with missing geographic fields
also use official venue/club sources, including [Harvard’s Jordan Field](https://gocrimson.com/sports/2026/6/18/know-before-you-go.aspx),
[Chicago’s Northwestern match notes](https://chicagostars.com/assets/2026/03/RS-M01-LA-vs-CHI-Match-Notes.pdf),
[Seattle’s Memorial Stadium history](https://www.reignfc.com/news/letter-njx5x),
[Fullerton’s Titan Stadium](https://fullertontitans.com/sports/2023/8/3/athletics-titan-stadium.aspx),
[Osceola Heritage Park](https://www.osceola.org/Community/Parks-and-Public-Lands/Park-Hours-Rules-and-Reservations),
and [Denver’s venues](https://www.denversummitfc.com/venues/).

Two fixtures with missing cached stadium IDs have narrow, independently verified
Inter&Co Stadium corrections: `Xj5YPveRMb` ([May 8, 2026 match report](https://www.orlandocitysc.com/pride/news/match-report-barbra-banda-scores-late-game-winner-as-pride-beat-the-courage-1-0))
and `KXMeXv6vQ6` ([August 7, 2026 match report](https://www.orlandocitysc.com/pride/news/match-report-orlando-pride-fall-3-1-to-racing-louisville-fc)).
Corrections apply only when the cached stadium ID is empty; a corrected source
ID always takes precedence. Add new stadium mappings or fixture corrections only
with verified location evidence. These presentation corrections do not modify
the source cache or trigger ASA requests.

A trailing average includes the current match and the
preceding window-minus-one matches and resets each season. Before the first full
window, it averages all matches played so far: the effective window is
`min(matches played, selected window)`. An xG or xPoints average requires every observation within that exact
played-match window, including the shorter initial windows; missing expected data breaks that rolling line until it
leaves the window, without hiding actual values or skipping a match. Values remain unrounded until
display. Before the first full window, dotted lead-ins show expanding averages, including
for seasons shorter than the selected window; they never stand in for missing
full-window averages later in the season. Tooltips and keyboard announcements
identify the actual match count and selected window for early averages.
The season-trend values table stays in match order and shows the same expanding
averages, identified by its caption.

The default scoring balance puts scored and allowed together at each match's
exact horizontal position. Scored uses a solid green line with circles; allowed
uses a solid orange line with open squares. These encodings stay consistent
between goals and xG. Per-match lines connect successive recorded observations
without curve smoothing. Rolling lines use dotted segments through the first
full-window point, then solid segments between full-window averages.
Missing xG breaks both types of xG line; it is never interpolated or bridged.

In Scoring balance, selecting both goals and xG shows two vertically aligned,
separately titled panels; selecting one shows only that panel. Actual vs xG
instead shows Scored above Allowed, with actual goals and xG paired in each
panel. Differential pairs goal differential and xG differential on one plot.
Points vs xPoints uses the same actual/expected encodings on one plot.
Comparison lines encode actual values with solid green circles and expected
values with solid brighter purple diamonds. The darker chart green and lighter
purple also distinguish the solid series by brightness. Each plot has at most two match series at identical
match positions. Two-panel views use identical match and value scales. Scored and
allowed scales start at zero; differential uses symmetric signed limits. All
panels use the selected mode and window. Empty panels explain missing expected
observations while preserving available goals and early
individual match values. Each position has one value per series; there is no
raw/rolling overlay mode.

Benchmarks (`trend-view=relative`, retained for existing links) keeps per-match
and rolling values in their original units. A subdued dotted horizontal
reference always shows the team's arithmetic mean over raw completed-match
observations. A selected comparison adds one colored dotted reference. Actual
means use all recorded matches; xG and xPoints means use available paired
observations. Means reset each season and do not change with mode or window.
Active seasons use all recorded matches so far; references stay horizontal
rather than reconstructing earlier standings or forecasts.

League average includes every eligible team, including the selected team,
weighted by recorded match appearances. Each fixture contributes its two
team-perspective observations. Scored and Allowed have the same league mean.
Coverage reports available team match appearances out of all recorded
appearances; xG and xPoints coverage remain independent.

Playoff-team average follows the catalog's season-specific playoff cut (four,
six, or eight). Top 4 average uses four teams. Completed seasons use final
total-points standings, with Shield winner selecting one team across all
metrics. A missing standings capability, insufficient group size, unplayed
completed-season fixtures, or unresolved tie across the group cut withholds
that comparison. Historical catalogs lack season-specific tiebreak rules, so
any points tie across their cut is withheld even if the current-season ordering
would resolve it. Unknown historical tiebreaks never certify final playoff or
Shield membership.

Active seasons label the groups Projected top N average, Projected top 4
average, and Shield favorite. The default Forecast Lab model's projected final
points order selects the playoff and top-4 groups; its highest Shield probability
selects the favorite. Equal Shield probabilities retain all tied favorites.
Forecasts select membership only: reference means still use recorded matches
so far, weighted by match appearances and including the selected team when
applicable. Shield favorite is distinct from the metric-specific Best team
average.

Explore runs the same baseline forecast as Forecast Lab, without fixed results,
using the existing archive snapshot, including xG and historical venue rates.
It shares Forecast Lab's bounded executor and result cache; unchanged inputs
reuse warmed results, while changed fixture or xG inputs invalidate them. Only
active scopes with explicit forecast capability, verified rules, recorded team
observations, and remaining fixtures request a projection. Unsupported scopes,
failed or busy forecasts, and selected teams without observations leave the
affected comparisons unavailable; current standings never substitute for a
forecast. Comparison details identify the model, xG coverage, available fixture
and xG update timestamps, and fixture/venue coverage warnings. Completed-season
comparisons remain factual and do not run a forecast.

Best team average selects the best full-season per-match mean for the chosen
metric: lowest for Allowed, highest otherwise. Scored and Allowed can name
different teams; ties retain every holder, comparing values before display
rounding. Expected best values require complete coverage for every league team
for that metric; a team without recorded matches also withholds Best. Other
means can remain available from partial expected observations, with coverage
counts. Unavailable comparisons have a visible explanation and do not fill line gaps
or shorten windows to bypass missing observations.

Scored and Allowed have separate panels with shared nonnegative axes. Points
has one panel. Other horizontal gridlines are hidden in Benchmarks. Numeric
reference labels occupy a reserved right-side area, use subdued/series colors,
and are separated when values coincide. Short connectors identify their true
reference positions. Labels draw before tooltips. Tooltips and keyboard
announcements show only the active panel’s values and selected means. Shield
and Best references also name their teams, including tied Best holders.
Coverage counts and membership notes stay in the expandable details.
References add no keyboard stops or tooltip datasets. Expand Benchmark values,
teams and coverage to inspect names, means, coverage and membership notes; this
also works without JavaScript. The fallback match table retains original values
and ordinary headings. Above an Allowed reference means more conceded; below
means fewer. Other views retain original values without references.

Pointer/touch inspection works by match position and shows the inspected panel’s
values for that match, including unavailable expected data. Scored and Allowed
inspection stays within its own panel; Scoring balance shows only its Goals or
xG panel. Each value appears once in the tooltip. Keyboard inspection advances one match
at a time, with Home/End and Escape, and announces the same values. Tooltips
include venue-local date, opponent, home/away, team-perspective score, and the
exact match-number range for an average. A vertical guide identifies the match
being inspected. The collapsed chronological Match values table follows the
selected view, series, and averaging mode; it also works through the GET form
without scripts.

Run `make test-explore` for both kickoff formats, chronological ordering
(including timezone offsets), venue-local midnight/DST boundaries, missing venue
handling, venue orientation, all five trend views, points/xPoints orientation and independent coverage,
benchmark membership/cuts/ties, forecast-selected groups and Shield favorites,
shared forecast cache and unavailable forecasts, match weighting, best-metric
direction and holders, validation, and retained Data preferences,
all three windows, unrounded rolling arithmetic, missing xG and recovery,
season eligibility, selection validation, fallback and single-snapshot
regressions. The `season-trend` scenario in `TestHistoryPreview` provides 29
played matches and one remaining fixture per team in the active season, 30
played matches per team in the completed season, partial xG, and an empty season. Verify desktop and 390px
layouts; scored/allowed at the same match position; matched axes across paired panels;
all series/view/mode/window selections and restored Data preferences; pointer/touch and keyboard match
inspection; labeled team and comparison means, reference label collisions, panel-specific tooltips and reference holder names, group membership details, match-weighted league arithmetic,
identical league scored/allowed references, independent xG/xPoints coverage counts, unchanged means across
modes/windows, original units, one-match values and expanding averages before full windows,
zero, ties, and negative values; solid core series and dotted expanding-average lead-ins,
including tooltip/keyboard identification and fallback tables; xG line gaps; short windows and
empty seasons; chronological table and no-script GET fallback; direct URLs and
Back/Forward; and switching views without another document/data request (team
logos may load).

### Season by season

Team profile also includes Season by season (`view=team-history`). The `team`
selector chooses an ASA team ID from eligible recorded results across the
archive; it defaults to the first team by name, with ID breaking ties. The
selector uses the newest eligible name for each ID, so a rename keeps its
history. Distinct IDs remain separate even when names match; relocation or
franchise lineage is not inferred. Blank, repeated, and unknown team IDs return
400. `measure=for|against|difference|points` controls the chart, defaulting to
goal differential and sharing the season comparison's validation.

History reuses the eligible team-season rates and per-team xG and xPoints
coverage already calculated for Compare teams. A single recorded result can
appear with its played count. Goals, xG, points, and xPoints are per match;
differential is scored minus allowed. The within-season `units` control does not
change history.
A native table always shows all four pairs of measures, newest season first
by default. Every column is sortable: `history-sort` accepts `season`, `played`,
or `difference|for|against|points` suffixed with `-actual` or `-expected`.
`history-order=asc|desc` selects direction. Sorting uses unrounded values,
keeps unavailable xG or xPoints last in both directions, and breaks metric ties
by newest season first. Blank, invalid, or repeated sorting parameters return 400.
Header links toggle direction, expose the active order with `aria-sort`, and
work without JavaScript. Sort state is independent of the chart measure and
other Explore tables, survives team and measure changes, and supports direct
URLs and Back/Forward. The chart always retains chronological order.
The table keeps season labels compact; in-progress seasons are identified in
the chart tooltip and keyboard announcement.
The trend places seasons at calendar-year intervals and breaks lines for missing
or excluded seasons and unavailable xG or xPoints. Its scale includes zero and
supports negative differentials. Point inspection includes the played count.
Team and measure selections update locally with shareable URLs and Back/Forward;
the GET form and full table remain usable without JavaScript. Empty archives have
an explicit empty state, and missing expected values produce a warning without
hiding actual goals or points.

`series=goals|xg|both` selects the history chart's actual or expected series
(both by default); the labels change to Points and xPoints for the points measure.
`context=on|off` toggles league context (off by default). Blank, invalid, or
repeated values return 400. These controls preserve table sorting and work
through direct URLs, Back/Forward, and the GET fallback. Without context, Both
overlays the two series; with context, it shows two vertically aligned charts
on the same scale.
A single-series selection uses one chart. The scale includes the selected team's
values, visible seasonal bounds, historical bounds, and zero.

League context uses the same eligible team-season rates from the single archive
snapshot, independently of the selected team. Floating bars span each season's
lowest and highest rates, including an eligible active season labeled in
progress. Bars use the same year positions as team points, with full
width at the first and last seasons; equal bounds remain inspectable as a thin
mark. Missing/excluded calendar years leave no range. An expected-value season
range requires complete xG or xPoints, as appropriate for the measure, for every
team in that season's comparison. Partial coverage withholds both bounds while
leaving the selected team's own fully covered expected value available. Zero is
valid, signed differentials remain signed, and extrema and ties are compared
before rounding. High/low refers to the numeric value, so a low goals-allowed
rate is favorable.

Dotted horizontal record lines use only eligible completed seasons. They span
the chart even for a team with only one season. Record labels identify the
available coverage start; they do not claim records outside the cached archive.
Active results cannot replace these records. For xG or xPoints, only fully
covered season comparisons contribute. Missing expected-value context is
explained visibly.

Hover/tap on a floating bar or team point shows the season's high and low,
holder names, year, rate, and played count. Hover/tap along a record line shows
its historical holders. Coincident team points retain season inspection;
record lines remain inspectable between dots. Keyboard arrows inspect team
points, one range bar per season, and each historical bound once, with full
holder announcements.
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
partial expected-value coverage, and an active season. Verify desktop and 390px
layouts, all four measures, team selection, point hover/tap and empty-space
dismissal, keyboard inspection, table sorting (including missing xG and xPoints),
direct URLs, Back/Forward, no-script sorting/forms and full context details,
record-line and floating-bar
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
match has valid xG. The league scoring calculation reports xPoints coverage
without adding an xPoints scoring trend. Compare teams separately sums ASA's
retrospective xPoints for each team's recorded regular-season matches and derives
the per-match rate when that team's xPoints coverage is complete. Coverage of
xPoints does not require complete xG, and incomplete xPoints do not hide actual
points. Fixture inventory and expected-value coverage remain separate
dimensions, as required by [IDEAS.md](../IDEAS.md).

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
