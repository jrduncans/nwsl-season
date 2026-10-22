# How Explore works

Explore (`/explore`) charts recorded regular-season results across the
archive. Every view is calculated from one coherent SQLite snapshot read by
`cache.DB.HistoricalRegularSeasons`; a request never refreshes ASA data or
reads individual season pages.

Naming: the archive calculations live in the `internal/history` package
("history" as in past seasons), and Season by season keeps its original
`view=team-history` key. `/history` and `/history/scoring`, a retired scoring
page, redirect to the Scoring trend.

## Views

| Group | Scope | Tab | URL |
|---|---|---|---|
| Compare teams | Every team, one season | Actual vs expected (paired dots) | `view=teams&display=chart` |
| | | Gap to expected | `view=teams&display=gap` |
| | | Outlier plot | `view=teams&display=scatter` |
| | | Scored vs allowed | `view=teams&display=quadrant` |
| | | Table | `view=teams&display=table` |
| Team profile | One team | Rankings | `view=team-rankings` |
| | | Match by match | `view=season-trend` |
| | | Season by season | `view=team-history` |
| | | Head to head | `view=head-to-head` |
| League trends | League, across seasons | Scoring trend | `view=trend` |
| | | Goal distribution | `view=distribution` |
| | | Home advantage | `view=home-advantage` |
| | | Parity | `view=parity` |
| | | Shield and playoff thresholds | `view=thresholds` |
| | | Team-seasons (ranking or actual-vs-expected scatter) | `view=team-seasons` |
| | | Scoring table | `view=table` |

An omitted view opens Actual vs expected; a view-less link that carries
`metric` (from when Scoring trend was the default) opens Scoring trend.
The view registry, `exploreViewGroups` in `internal/app/explore_views.go`,
lists every view and Compare teams display in tab order with its label and view
group, and holds each view's selection rules: points-only selection, points
note, whether it carries both scoring bases (so switching basis needs no
reload), and its forecast needs. The server validates views, builds the tabs
and applies those rules from it, and emits it as the `explore-views` payload so
`explore.js` applies the same rules in place. Explore uses the current season's
navigation with Explore marked active.

View and control selections live in the URL and restore through reload and Back/Forward. Scatter point identification is transient and clears on navigation or reload.
The server renders each view with GET forms, sortable header links and native
disclosures, so everything works without JavaScript; the script switches views
and selections in place from the same payload, without another data request.
Validated view-specific parameters return a 400 "Invalid Explore selection"
page for blank, repeated or unsupported values. Chart.js and its datalabels plugin are vendored under
`internal/app/static/vendor`.

### Principles for every view

Explore exists so visitors can notice patterns, unusual team-seasons and
comparisons for themselves. Views show the data and leave the conclusion to the
reader.

Data:

- Compare actual results and expected values over the same selected matches;
  a chart and its table use the same values and population.
- An xG aggregate needs complete xG and an xPoints aggregate needs complete
  xPoints; neither implies the other. Missing values are null, never zero, and
  actual results stay visible when expected data is absent.
- Never derive xPoints from xG, and keep ASA's retrospective xPoints
  separate from Forecast Lab projections.
- Each view owns its eligibility rule; there is no global sample threshold. An
  active season is legitimate data, shown as a current pace with its matches
  played, and is never presented as its final place in history.
- Per-match rates adjust for matches played, not for opponents or scoring era,
  so a statistical extreme is not a judgment of the best team.
- Regular season, playoffs and cups stay separate unless a view explains why
  it combines them.
- A maximum or minimum names its eligible population. Ties are decided before
  display rounding, one rule serves chart and table, and selecting a team never
  changes the population. The archive starts in 2016, so claims say "since
  2016" or "in the available archive", never "all-time".
- Official outcomes such as Shield winners come from verified history, not
  today's ranking code. Earned points are `3 × wins + draws`, kept apart from
  sanctions. Distinct ASA team IDs stay separate; franchise lineage is not
  inferred.
- Ideas that need data the cache lacks (event timing, lineups, minutes,
  pre-match odds) wait for that data rather than approximating it.

Presentation:

- Curate a few useful views with a good default, and add a control only when
  it reveals a meaningful alternative. Explore is not a chart builder; a new
  analysis joins an existing group with sub-controls rather than adding a
  top-level tab.
- Lead with a chart, keep an exact-value table available, and give each chart
  an accessible description. **Use the vendored Chart.js for Explore charts**,
  with the shared options and interaction helpers in `explore.js`, so styling,
  tooltips, touch and keyboard behavior stay consistent across views. Do not
  add SVG trend charts or a separate chart engine. SVG is reserved for existing
  supplementary geometry such as the Parity team strip, not new trend charts.
- Show individual-match goals, goals allowed, goal margins and points, and
  totals of those counts, as whole numbers. Keep two decimals for xG, xPoints,
  rolling averages, per-match rates and differences involving expected values.
- Label values, units and reference lines directly. Over-performance views use
  a zero line and a consistent favorable direction. Color is never the only
  signal; no dual axes, unexplained composite scores, or one line per team.
- Describe gaps as above or below expected, not luck; finishing, goalkeeping,
  match state, model error and randomness all contribute.
- Put coverage and sample notes next to the result they qualify.
- Essential detail works by touch and keyboard without hover; on narrow screens
  selected detail goes below the chart.
- URLs share the view and its selections, not a frozen answer; data may update.

A new view needs a registry entry and a row in the table above, a matching
`data-panel` section (the registry test checks tabs and panels agree), a pure
calculation over the archive read, canonical URL state, `make test-explore`
coverage, and browser checks at desktop width and 390px.

## Shared data rules

### Matches and seasons

`history.SummarizeScoring` accepts public, source-backed `Regular Season`
catalog entries with the `fixtures` capability and keeps a row for every season,
including unloaded ones, sorted by season. Cups and playoffs are rejected.

A match is played only when its status is `FullTime` and both scores are
present and nonnegative. `Abandoned` has its own terminal count; a malformed
`FullTime` match counts as an invalid completed result, not a score; other
statuses are pending. Team views orient home and away appearances to each team
(points are 3/1/0) and require distinct, nonempty team IDs per match.

### Expected values

xG and xPoints join a played match by match ID and matching home/away teams,
and need the xG capability and an available observation. xG must be finite and
nonnegative; xPoints must be finite and between 0 and 3. Zero is valid. Coverage
is counted separately for each. A league or team xG rate exists only when every
counted match has valid xG; the same holds for xPoints.

### Season eligibility

A season is plotted only with at least 20 valid played matches, available source
readiness, a known non-upcoming lifecycle, no invalid completed results, no
known-incomplete inventory, and no pending match in a completed lifecycle.
Team views use the same checks with a one-result minimum. Unknown inventory
does not exclude a season. Partial xG never excludes a season's goals.

Exclusion codes, in order: `source_unavailable`, `lifecycle_unknown`,
`upcoming`, `inventory_incomplete`, `historical_results_incomplete`,
`invalid_completed_results`, `incomplete_non_penalty_data` (non-penalty basis
only), `below_minimum_matches`.

### Penalties basis

Every view accepts `penalties=include` (default, omitted from URLs) or
`penalties=exclude`. The control is an **Exclude penalties** checkbox, unchecked
by default; checking it submits `penalties=exclude`. Points-only selections
hide the control, ignore and drop a carried basis, and show no points note:
Compare teams on Points except Table and Scored vs allowed; Season by season on
Points; Match by match on Points vs xPoints and Benchmarks showing Points or
xPoints; Head to head on Points vs xPoints; Parity; and Team-seasons on Points. Parity always uses all-shots
eligibility, even for an old link with `penalties=exclude`. Shield and playoff
thresholds also always ignore a carried basis and have no Penalties control.
Mixed views keep the control and the points note when they show points: Compare
teams' Table, Home advantage, and Head to head's views other than Points vs
xPoints. The server accepts valid old `penalties=exclude` links for points-only
selections, but generated links and forms omit the parameter. Blank, repeated
and unsupported values still return the standard 400 page. These rules are the
registry's `PointsOnly` and `PointsNote` rules; `apptest.ExplorePointsOnlyCases`
checks them through both the Go and the browser evaluator.
The other views change goals and xG while official results and points stay fixed.
The server-rendered checkbox is last in each view's data-control row and never
in the navigation. Mixed-view links carry its selection. Every data-control row
is a GET form, and the checkbox is that form's only penalties field: unchecked,
the form omits the parameter. Views with no other selection (Scoring trend, Goal
distribution without data, Home advantage and Scoring table) wrap the checkbox
in a form whose hidden fields carry the rest of the query
(`PenaltiesFormFields`), so without JavaScript every view switches basis through
its form's submit button.

With JavaScript, changing the checkbox reloads the page on the new basis,
because the server computes every figure. Head to head and Team-seasons carry
both bases, so their own checkboxes switch in place. `explore.js` keeps the
basis the server rendered (`renderedNonPenalty`) apart from the basis it names
in labels (`nonPenalty`): entering any other view in place reloads when the URL
basis differs from the rendered one.

`history.ScoringBasis` selects the arithmetic for every consumer. On the
non-penalty basis, per side: non-penalty goals = goals − penalty goals, and
non-penalty xG = max(0, xG − penalty xG). A match counts toward goals and xG
only with available xG and a consistent ASA penalty split; without a split its
non-penalty goals are unknown. A season with any played match outside that
coverage gets `incomplete_non_penalty_data`. Team match records keep the
official score beside the basis goals.

Official points, ASA xPoints, benchmark groups and forecasts never change with the
basis (ASA publishes no non-penalty xPoints). Mixed views that combine points
with non-penalty goals or xG show the note "Points include penalties; ASA
publishes no non-penalty xPoints." Labels follow the basis (`xG` → `npxG`, `Goals` →
`Non-penalty goals`, `xGD` → `npxGD`) through `basisText` in Go and `T` in
`explore.js`. Match results add an `npxG` line when a match had penalty xG.

## League trends

**Scoring trend** plots goals and xG per played match for eligible seasons,
with Goals, xG, Both, and Goals − xG (`metric=goals|xg|compare|gap`, default `compare`). Goals − xG
uses signed bars around zero; seasons without complete xG have no xG mark.
Missing calendar years, including 2020, are gaps. An eligible active season is
plotted and noted as in progress through N matches.

**Goal distribution** counts played matches by combined goals in five bins (0,
1, 2, 3, 4+), which always sum to the played count. The default is a 100%
stacked bar per season; `distribution-bin=0|1|2|3|4` instead plots one bin's
share by season on a shared percentage axis. A collapsed values table gives
counts and one-decimal shares, sorted by exact share with newest season first
on ties.

**Home advantage** plots, per eligible season, how much better home sides did
than away sides per played match: goals against xG and points against xPoints, as
two small multiples that share the season axis. Each mark is home minus away,
so above the labeled zero line the home side did better. There are no dual
axes, and missing calendar years, including 2020, are gaps. Each panel's
expected mark needs complete coverage of its own metric (xG for goals, xPoints
for points), so one can be missing while the other and the actual values remain;
a note names the seasons without each. An eligible active season is plotted and
noted as in progress through N matches. The table lists season, matches,
home-minus-away goals, xG, points and xPoints per match, and the home win, draw and
loss share, sorted from unrounded values (`home-sort=season|matches|goals|xg|points|xpoints|results`,
`home-order=asc|desc`, default season descending, with unavailable values last). `penalties=exclude` switches the goals panel to
non-penalty goals against npxG; points and xPoints never change and carry the
standard points note. Eligibility is the Scoring trend's, including the
non-penalty coverage rule. The calculation is `history.SummarizeHomeAdvantage`,
which reads the scoring summaries, not `venue_summaries` (those cover only the
seasons Forecast Lab needs and have no xPoints). "Home" is ASA's home
designation: ASA has no neutral-site flag, so alternate-venue and neutral
matches count as home matches. The view describes a home-minus-away gap, not its
cause.

**Parity** shows how evenly matched each eligible season was, and what made it
so. The trend plots the spread of team points per game (PPG), measured as the
population standard deviation across every team, over a shaded region from
zero to the spread chance alone would give. That chance level assumes equally
good teams: each match is a draw at the season's observed draw rate *d* and
otherwise a 50/50 win or loss, so one match's points vary by
σ² = 4.5(1 − *d*) + *d* − (1.5(1 − *d*) + *d*)², and the chance spread is
σ / √*n* for *n* mean matches per team. Drawing it on the same axis replaces
the Noll-Scully ratio (actual ÷ chance), which would be an unexplained
composite. PPG is earned points (`3 × wins + draws`) over each team's played
matches.

Below the chart a strip places the selected season's teams on a fixed 0–3 PPG
axis, with the top-to-bottom gap (best minus worst PPG) bracketed and the
middle half (interquartile range) shaded; a sentence names every team tied at
either end. Overlapping marks stack into rows using `stripRows` in
`explore-geometry.js`. `season` selects the strip (default: the newest
completed eligible season; the active season is selectable) and is shared with
Compare teams, so an unknown year is a 400 but a catalog year without parity
values falls back to the default with a note. Selecting a season on the chart
(click, or arrow keys then Enter), the select or a table link updates the strip
in place. `parity-series=actual|both` (default `actual`; not `series`, which
Season by season already owns) adds the xPPG spread series and each team's
xPPG mark. The xPPG spread needs every team's xPoints for every played match,
otherwise that season has no xPPG mark and an `xPoints incomplete` note. An active
season is plotted hollow with a dashed segment and noted as in progress; its
chance level uses matches played so far, so it is wider. The values table lists
season, teams, matches per team, PPG spread, chance level, xPPG spread, gap and
IQR, sorted from unrounded values (`parity-sort=season|teams|matches|spread|chance|xspread|gap|iqr`,
`parity-order=asc|desc`, default season descending, unavailable values last).
Quartiles use linear interpolation between closest ranks (`history.Quantile`,
R and NumPy's default), and the chart receives them from the server. Points
never change with `penalties`; Parity has no Penalties control and always uses
all-shots eligibility, even when an old link carries `penalties=exclude`. The
calculation is `history.SummarizeParity`.

### Shield and playoff thresholds

`view=thresholds` leads with four series on a shared 0–3 PPG axis, since 2016:
Shield, Home playoff game, Playoff bye and Playoffs. The series use distinct
line patterns and markers as well as color. The intro explains what a mark is:
the points per match of the last team to qualify. The tooltip title shows the
year, playoff places and bye count (or **No byes**). The chart's keyboard hint
is screen-reader-only (`aria-describedby`), and the view has no chart note. Each tooltip row is one line:
outcome, team name and PPG; points and match counts live in the
season cards and outcome-specific table. Missing values and 2020 split
lines. Active seasons have no final mark.

`history.Thresholds` derives records from all-shots summaries and playoff matches
in the same coherent SQLite archive read. Team names come from the cached ASA
identities, as elsewhere in Explore. The existing competition catalog supplies
qualification places and bracket topology; opening seed pairs supply home playoff game
and bye thresholds. No full standings, W–D–L records or season formats are duplicated in
a threshold catalog, and no extra network request runs during page rendering.

Official records are ranked with the shared season-specific standings policy:
head-to-head first through 2021, goal difference first from 2022. The historical
policy includes multi-team mini-league PPG, restart for a remaining pair, and
away/home criteria. Awarded scores contribute to wins, goals and head-to-head
records; deductions change official points before ranking. See the
[standings guide](standings-and-schedule-guide.md) and
[rules audit](historical-outcomes-audit.md#season-specific-tiebreakers).

Completed playoff entrants, first-appearance hosts, byes and opening seed pairs
corroborate computed ranks and constrain any remaining unavailable tiebreaker.
Verified source order is used only where the bracket catalog explicitly supplies
it. The neutral final never establishes home entitlement. Contradictory
playoff evidence is retried per outcome: the Shield uses the regular-season
order alone, and the home playoff game, bye and playoff thresholds each use only their own
evidence, so a conflict withholds just the outcomes it touches. An unresolved
threshold or adjacent finisher leaves that boundary as a gap. A tie override is accepted only for a still-unresolved rank,
never to replace a result the season's computable rules already determine.

A season needs loaded, terminal regular-season results, a complete resolved
catalog playoff bracket and consistent official match counts across teams after
awards. Known incomplete inventory withholds all thresholds. Without verified
regular-season inventory, equal match counts and a completed postseason are
closure checks, not an independent audit of every match; a uniformly truncated
archive cannot be ruled out by counts alone. An in-progress season can enter as soon
as verified regular-season inventory is complete and its postseason is complete,
without waiting for the calendar to change. Ordinary later seasons need only
normal ASA synchronization and their normal competition catalog entry, not an
outcome-specific manual record. An exceptional or ambiguous season may need a
source-backed exception.

[`outcome_overrides.json`](../internal/competition/outcome_overrides.json) contains
only facts absent from ASA: awarded matches, deductions and otherwise unresolved
tied orders. Awards fill observed match-count deficits; if ASA already counts an
award, it is skipped. Asymmetric or unexplained deficits withhold the values.
Deductions change official points while preserving the played-match record.
Tie overrides must agree with equal points, unresolved computed ranks and the
observed seed constraints.
See [the exception audit](historical-outcomes-audit.md).

PPG is **official points / official matches**, including awarded forfeits and
point deductions. Unrounded ratios are shared by chart, cards and table;
three-decimal display does not break ties. Portland's 2021 Shield value is 44/24,
not its played-match 41/23. Details explicitly label equal official points at a
boundary and disclose differences from the cached played-match record.

No xG, xPoints, penalty split or forecast is required; carried Penalties has no
effect and there is no Penalties control (the view is points-only, so the
"Excluding removes penalty kicks…" help text is not shown). A thresholds request skips forecast
benchmark warming; entering Match by match from that page reloads its normal
payload so forecast controls remain available.

Season links and Chart.js marks select details below the chart by touch or keyboard
(`threshold-season`, default newest eligible season). JavaScript updates the URL
and detail visibility in place, restoring reload and Back/Forward. Native
links, the outcome form and sortable table headers work without scripts. The
selected season is headed "2025 · 8 playoff teams · no byes" and uses compact
outcome cards with two lines each: **Last in** and **First out** (for the Shield,
**Winner** and **Runner-up**). A line reads "Orlando Pride · 4th · 40 pts · 1.54",
with PPG to two decimals; match counts appear only in the table. Equal-points
boundaries have a visible badge. Outcomes that did not apply have no empty card.
A disclosure, "Format, record notes and sources", holds a one-line format such as
"8 playoff teams · top 4 host · no byes", forfeit and deduction notes (one sentence
per team, only in seasons that have them) and the sources line.

The comparison table, headed **All seasons**, shows one outcome at a time (`threshold-outcome`: `shield`,
`home`, `bye` or `playoffs`, default `playoffs`). Each row is one season's verified
threshold team, official points, official matches and PPG. Non-applicable and unavailable
thresholds have no rows. A bye comparison therefore contains only bye seasons.
Headers sort by `threshold-sort` (`season`, `team`, `points`, `played`, `ppg`)
and `threshold-order` (`asc` or `desc`, default newest season first); PPG sorting
uses the unrounded ratio, with newest season breaking ties. Outcome selection and
sorting update the URL in place and restore on reload and Back/Forward. The five
columns fit desktop and 390px without horizontal scrolling. Season links lead to
the full adjacent-team comparison. Formats show qualification places, home playoff game
threshold and relevant round, and bye threshold. Hosting means a seed's entitlement
subject to venue exceptions, excluding neutral finals and hosting conditional on
an earlier win.
Historical thresholds are observations, never future guarantees. PPG normalizes
matches played, not opponents or era.

### Team-seasons

`view=team-seasons` ranks every eligible ASA team-season since 2016 on Points,
Goals scored, Goals allowed or Goal differential, by Actual, Expected or Gap.
The default display is Ranking on actual points per match. Ranking bars and table rows are plain content:
there is no selection, highlighting, detail panel, Team select or Season select.

The heading follows Rank by: "Points per match, since 2016" for Actual,
"Expected points per match, since 2016" for Expected, and "Points compared with
expected, per match, since 2016" for Gap. The same wording applies to the three
goals measures and their non-penalty labels, including the chart's accessible
label.

A horizontal list shows the ten highest and ten lowest team-seasons. Both end
labels always appear, including for populations of twenty or fewer, where the
rows are split between the two ends without duplicating a team-season. Above
twenty, a labelled "N more team-seasons in the table" break separates the ends.
With JavaScript that break is a button that opens the table and moves focus to
it; without JavaScript it is plain text. Chart identities show rank, then the
logo beside the team name. The rank has a fixed-width gutter with right-aligned,
tabular figures, so logos and names line up even for in-progress rows. The logo
also accompanies the left-aligned, plain-text table name, using `teamName` and
`clubLogoURL`, with decorative `alt=""`. ASA's image for that ID is shared across
seasons, including folded clubs; no historic-logo lineage is inferred.

Actual and Expected use the ranked value as the bar and the other value as a
labelled thin tick on the same scale. The legend uses the same purple tick;
its narrow outline leaves close bar ends visible. Gap uses one diverging bar;
gap and goal differential have a labelled zero line. The scale starts at zero
(or centers on zero for signed values), with round bounds; points per match
uses 0–3. Narrow rows place identity and values together, with the bar beneath.
Gap always means actual − expected, with positive above expected and negative
below expected. Rank 1 is favorable-first: highest for points, goals scored
and differential, lowest for goals allowed (including gap), matching Compare
teams' Gap to expected. End labels name the measure, never best/worst.

`history.SummarizeTeamSeasons(inputs, basis)` reuses `SummarizeScoringBasis`
and `TeamComparisonEligible()` integrity checks. Completed seasons need no
extra minimum. An active season enters only once **every team has played at
least ten valid matches**, including roster teams without results. This gate
applies in all modes. Active bars are hollow and rows say "in progress through
N matches". Distinct ASA IDs remain separate, with names from that season's
team list; franchise lineage is not inferred.

Points per match are earned points divided by played matches; xPoints per match
use ASA xPoints divided by the same count. All measures use unrounded values
for competition ranks and sorting. Ties order newest season first, then team
name, then ID. Actual rankings require no expected data. Expected and Gap
require complete coverage of their own metric: xPoints for Points, xG for
goals. These are independent. Unrankable team-seasons appear last in the table
for every sort, labelled `xPoints incomplete` or `xG incomplete`; their actual
values remain visible. The coverage note above the chart gives only the count,
since the table names those rows. The only other notes above the chart are gap
direction (when ranking by Gap) and the points note when Points appears beside
a penalty-sensitive measure. On Points, Penalties is hidden, carried
exclusions are ignored, and there is no points note. One short note below the chart combines the standing
qualifications: per-match rates do not adjust for opponents or era and do not
judge the best team, and active seasons use the ten-match rule.

The **Ranking | Scatter** switch (`team-seasons-display=ranking|scatter`)
shows the same eligible population. Scatter plots expected per-match values on
x against actual on y, with equal numeric axes and a labelled dashed parity
diagonal. Points uses fixed 0–3 axes; goals measures fit both series with round
bounds, including negatives for differential. Rank by is hidden and ignored in
Scatter, even when carried in the URL. Measure and goals' Penalties still apply.
The table uses actual ranks in Scatter; its sort and order are shared with Ranking.

Each complete team-season is a plain dot; in-progress seasons are hollow.
Click or tap a dot, or use arrow keys followed by Enter, to show the logo, team,
season and the chosen measure's Actual, Expected and Gap below the plot.
Coincident dots list every team-season at that point. Identification does not
change the URL and clears on reload, Back/Forward or a control/display change;
Escape or empty plot space also clears it. There is no detail panel or default
highlight. A coverage count beside the plot reports rows without the chosen
measure's expected data; their actual values remain in the table. For goals
allowed, above the diagonal means more goals allowed than expected. Without
JavaScript, Scatter falls back to the same table and GET controls.

The table is a `<details>` disclosure, collapsed by default, whose summary is
"Table: all N team-seasons". N counts every eligible row for the current measure
and basis, including rows missing expected values. A sort or order parameter
opens it, matching Parity. Columns are Rank, Team (logo and text), Season,
Matches and the current measure's Actual, Expected and Gap. Only the column
headers are links; sorting keeps unrankable rows last in either direction.

| Parameter | Values | Default |
|---|---|---|
| `team-seasons-display` | `ranking`, `scatter` | `ranking` (omitted from links) |
| `team-seasons-measure` | `points`, `for`, `against`, `difference` | `points` |
| `team-seasons-rank-by` | `actual`, `expected`, `gap` | `actual` |
| `team-seasons-sort` | `rank`, `team`, `season`, `played`, `actual`, `expected`, `gap` | `rank` |
| `team-seasons-order` | `asc`, `desc` | `asc` |
| `penalties` | `include`, `exclude` (goals measures only) | `include` |

The sort keys refer to the current measure. Blank, repeated or unsupported
values for these parameters return the standard 400; this view adds no change
to existing `view` validation. Controls, basis and sorting switch in place
from the same payload (which includes logo URLs), with no data request, and
restore through reload and Back/Forward. Server-rendered bars, a native
collapsed disclosure, sort links and a GET form work without JavaScript.

For goals measures, `penalties=exclude` switches to non-penalty goals and npxG
through `basisText`, including `incomplete_non_penalty_data` season eligibility.
For Points the checkbox is absent and penalties never change values or the
population. A carried `exclude` is accepted and ignored without a note.
Changing measure drops stale penalties from forms and links. The scatter remains
a follow-up.

**Scoring table** lists eligible seasons with matches, goals/match, xG/match,
and goals − xG/match, sorted from unrounded values.

## Compare teams

`view=teams` compares every team's actual and expected values in one season.

| Parameter | Values | Default |
|---|---|---|
| `season` | Catalog year | Newest eligible season |
| `measure` | `for`, `against`, `difference`, `points` | `difference` |
| `units` | `per-match`, `total` | `per-match` |
| `display` | `chart`, `gap`, `scatter`, `quadrant`, `table` | `chart` |
| `team-sort` | `name`, `played`, or `<measure>-actual\|expected\|gap` | `difference-gap` |
| `team-order` | `asc`, `desc` | `desc` |
| `quadrant-data` | `goals`, `xg`, `both` | `goals` |

Gap always means actual − expected. Each team needs xG for every one of its
played matches to show xG and goal gaps, and xPoints for every match to show
xPoints and the points gap; another team's gaps never hide its values. Teams
without coverage sort last in both directions, appear last with an
`xG incomplete` or `xPoints incomplete` label on the gap chart, and are named in a
note on the scatter plots.

When `measure=points` selects a points-only chart (Actual vs expected, Gap to
expected or Outlier plot), the Exclude penalties checkbox and points note are absent.
A carried `penalties=exclude` is ignored and dropped from the generated URL,
forms and links. Table and Scored vs allowed are mixed views and retain the
control and the points note when the basis is excluded.

- **Gap to expected** ranks teams by gap, favorable first: positive for goals
  scored, differential and points; negative for goals allowed. Green is above
  expected and purple below.
- **Outlier plot** puts expected on x and actual on y with equal axes and a
  parity diagonal. Clicking a point pins its details below the plot.
- **Scored vs allowed** plots scored against allowed, with the allowed axis
  reversed so up and right are better. Dividers mark the league average:
  per-match averages divide summed totals by summed appearances, so uneven
  played counts weight correctly. The xG average uses fully covered teams.

Plot sizing, point radii and logo placement are pure functions in
`internal/app/static/explore-geometry.js`, tested by `e2e/geometry_test.go`.
Change geometry there, not in `explore.js`.

## Team profile

The team picker lists ASA team IDs with eligible results across the archive,
using each ID's newest name; headings and opponents use names from the selected
season. The default is Portland Thorns FC (`Pk5LeeNqOW`), the site owner's team,
falling back to the first team by name. A team with no results in the selected
season stays selected with an empty state.

### Rankings

`view=team-rankings` ranks one team's goals scored, goals allowed, goal
differential, xG, xG allowed and xG differential among every eligible team in
the season, using `season` and `units`. Lower is better for the two allowed
stats. Ties share competition rank (1 + the number of strictly better values),
and later ranks skip places. League xG ranks are withheld for everyone if any
team's xG coverage is incomplete; goal ranks remain.

### Match by match

`view=season-trend` follows one team's played matches in a season.

| Parameter | Values | Default |
|---|---|---|
| `trend-view` | `balance` (Scoring balance), `compare` (Goals vs xG), `difference` (Margins), `points` (Points vs xPoints), `relative` (Benchmarks) | `balance` |
| `smoothing` | `none`, `3`, `5`, `10` (N-match rolling average) | `5` |
| `trend-sort` | `match`, `venue`, `result`, `goal-margin`, `xg-margin`, `points-gap`, `scored-gap`, `allowed-gap`, `adjusted-goals-for`, `adjusted-goals-against`, `adjusted-xg-for`, `adjusted-xg-against` (derived columns depend on the chart and the opponent adjustment) | `match` |
| `trend-order` | `asc`, `desc` | `asc` |
| `average-series` | `goals`, `xg`, `points`, `xpoints` (Benchmarks) | `goals` |
| `average-reference` | `team`, `league`, `playoff`, `top-four`, `shield`, `best` | `league` |
| `opponents` | `adjusted` (Scoring balance and Margins) | absent (official values) |

The section has a visually hidden “Match by match” heading; the team name
follows the controls, with a status line such as “2026 regular season · In
progress · 27 played”. One Smoothing select offers None or a 3-, 5- or
10-match average, so the window never wraps away from the mode. Old links
with `trend-mode` and `window` still validate and redirect to the equivalent
`smoothing` value, keeping every other selection; an explicit `smoothing`
wins. The note
above the chart says only what the controls and panel titles do not: the
rolling window (“Each point averages the last 5 matches, including that one.”),
Benchmarks' reference lines (plus “Expected means use available matches.”
when the selected expected series has gaps), the xPoints definition on Points
vs xPoints, and “Penalty kicks excluded.” when it applies. With none of these,
the note is hidden. Unsmoothed panel titles carry no “Each match” suffix;
rolling titles end in “· N-match average”. Direction notes state only the
above-zero or upward meaning, in sentence case (for example “Up: win · Zero:
draw · Down: loss”).

Scoring balance always shows both Goals and xG. A carried `series` parameter
belongs to Season by season and is ignored here; it stays in the form and links
so switching views restores that selection. Benchmarks defaults to Goals even
when a saved URL carries `series=xg`; set `average-series=xg` to select xG.

Matches are numbered by kickoff instant (`fixtures.ParseKickoff` handles both
RFC 3339 and ASA timestamps), with match ID breaking ties; a missing kickoff
sorts last with a warning.

Every chart's tooltip and keyboard description starts with the match number,
venue-local date, venue, opponent, official score and result (Win, Draw or Loss).
The result follows the official score even when penalties are excluded.

With smoothing off, Scoring balance, Goals vs xG, Margins and Points vs
xPoints draw each opponent's logo under every panel's plot, centered on its
slot, with the match numbers below the logos. Rolling
charts and Benchmarks have no logos, since a rolling point spans several
opponents. Each match carries `opponentId`, and the logo comes from that
team's record in the same season payload, so the URL is not repeated per
match. The row appears only when every slot is at least about 16px wide
(logos up to 20px); narrower charts, such as on phones, keep the plain axis
rather than thinning logos, and the choice is remade on resize. Each chart
grows by the row's height so its plot keeps its height. Logos are decorative and not inspectable, because inspection already
names the opponent; one that fails to load leaves its slot blank. Head to head
uses the same bar chart without logos.

A rolling average includes the current match and resets each season. Until the
window fills it averages all matches so far (`min(played, window)`), drawn as a
dotted lead-in. An xG or xPoints average needs every observation in its exact
window, so missing expected data breaks that line until it leaves the window;
it is never interpolated.

The **Match values** table always shows individual matches, whatever the chart
or smoothing selection: Match, Date (venue local), Venue, Opponent, official
Score, Result, Points, xG for, xG against and xPoints. Excluding penalties adds
Non-penalty goals and changes xG to npxG, following the selected basis; official
scores, results and points retain penalties. Missing values say `Unavailable`.
Margins adds per-match Goal margin and xG margin; Goals vs xG adds Goals − xG
and Goals allowed − xG allowed; Points vs xPoints adds Points − xPoints.
Scoring balance and Benchmarks add no derived columns. With smoothing on, the
chart's rolling-average columns follow all per-match columns, labeled with the
window. The caption describes the trailing window and its expanding lead-in.

Sort links work with or without JavaScript for Match (played order), Venue,
Result (ordered by points) and the selected chart's derived columns. They use
exact values before display rounding, with newest-first ties. Undated matches
stay last, and unavailable values follow available ones in either direction.
`trend-sort` and `trend-order` preserve the selection in the URL; blank, repeated
or unsupported values return the standard 400 page. Sorting never changes the
chronological chart or the population used for rolling averages. Smoothing
changes retain the sort. Switching charts retains shared sorts; a derived sort
that no longer exists falls back to match order and drops both sort parameters
from the URL, including on native GET submissions. The native `<details>` opens
when either parameter is present and is otherwise closed. The table scrolls
inside its container on phones, with a scroll hint. The browser renderer also
falls back to played order if restored history names a column absent from the
current chart, keeping the sort indicator and form controls consistent.

Except for the points gap bars described below, each chart's y axis is round
and labels its own ticks (`trendAxis` in `explore-geometry.js`), rather than leaving tick choice to Chart.js, which
thins ticks on these short panels: a 1, 2, 2.5 or 5 step, two or three ticks
each side of zero for symmetric bar charts and rolling Margins (two to four
above zero otherwise), ending on a tick at least 15% beyond the largest value. Whole-number data, such as
per-match goals, keep whole-number steps; labels carry only the decimals the
step needs.

**Scoring balance** (`trend-view=balance`) always shows Goals and xG. With
smoothing off (`smoothing=none`), both panels use mirrored bars in the same
match slot: scored up from zero, allowed down. The notes read “Up: scored ·
Down: allowed” and “Up: xG for · Down: xG allowed”. Both bars use the match's
venue encoding: home solid blue (`#1F6FA8`), away orange (`#CF7424`) stripes on
paper with an orange outline. Direction distinguishes scored from allowed.
The panels share one symmetric rounded `marginAxis`, sized to the largest
scored or allowed value in either panel, with at least 15% headroom. Tick labels
show absolute counts on both sides of zero. Missing xG leaves an empty slot;
both panels remain visible even if every xG value is unavailable. Excluding
penalties uses non-penalty goals and npxG.

The same All / Home / Away tiles as Margins show W–D–L, points per match,
goals for–against, xG for–against, matches and coverage. Tiles appear only with
smoothing off; Home and Away swatches are the venue legend. With penalties
excluded, the official-result note also applies. Each slot is one inspectable
mark covering both bars: tooltip and keyboard content include the match,
official score and result, xG for and xG against, plus non-penalty goals when
selected. Rolling Scoring balance keeps its scored and allowed lines.

**Goals vs xG** (`trend-view=compare`) keeps its two Scored and Allowed
line panels in rolling mode. With smoothing off (`smoothing=none`), it shows
two gap-bar panels: **Scored · Goals − xG** and **Allowed · Goals allowed − xG
allowed**. Above zero means more scored than xG in Scored, and more conceded
than xG allowed in Allowed; each panel states its direction. Both share one
symmetric rounded axis with at least 15% headroom, sized to the largest gap
across both panels using `marginAxis`. Home is solid blue and away striped
orange, using the same venue helper as Head to head and Points vs xPoints.
Missing xG or a missing penalty split retains its chronological slot with no
bar; zero remains a short mark. Penalties excluded compares non-penalty goals
with npxG.

Three equal All / Home / Away tiles above the panels show both sides, each as
a heading, totals and gap on their own lines (for example “Scored”, “45 vs
42.35 xG”, “2.65 above”), so narrow tiles wrap between those parts rather than
inside them. There is no netted headline. Home and Away swatches are the
venue legend. Coverage appears only when incomplete. Headings stay “Scored” and
“Allowed” when penalties are excluded; the npxG value and panel titles carry
the basis. Incomplete xG withholds its
total and gap; incomplete penalty splits withhold the non-penalty goal total
and state split coverage. Gaps smaller than 0.005 in magnitude read “matched
expected”, consistent with the signed table and tooltip precision. Empty venues
show only “No matches” (“No meetings” in Head to head), without coverage lines.
These tiles appear only with smoothing off. Tooltips and keyboard descriptions
include the match, official score, selected-basis goals, xG and signed gaps at
both ends. `trend-view=compare` keeps existing links compatible.

With smoothing off, **Margins** (`trend-view=difference&smoothing=none`)
uses the same two bar panels and All / Home / Away record tiles as Head to
head. The tiles summarize the selected team's season and appear only in
unsmoothed mode. The goal and xG panels share a symmetric rounded scale,
venue patterns, match slots, and the full match description for tooltips and
keyboard inspection. Missing xG leaves an empty slot. Rolling Margins keeps
two lines and uses “Margins · N-match average”, with a “Margin per match”
axis; it has no venue tiles. The
`difference` URL value stays compatible with existing links. When penalties
are excluded, the unsmoothed tiles carry
“W–D–L and points use official results, including penalties.”

**Adjust for opponents** is a checkbox in the data-control row on Scoring
balance and Margins. It answers whether a run of scoring came only from facing
weak defenses. Checked submits `opponents=adjusted`; unchecked carries no
parameter. Blank, repeated and unsupported values return the standard 400 page.
It works through the native GET form, and `explore.js` switches it in place.
Goals vs xG, Points vs xPoints and Benchmarks hide it, as does a season without
xG; there a carried value is ignored but kept, as `series` is. The view
registry's `opponentAdjustment` rule decides where it appears, for both the Go
and browser renderers.

Each completed match carries two factors for each team (`opponents` in the
payload), from `forecast.OpponentAdjustments`. They use the default forecast
model, xG Poisson (schedule load), as written: xG attack and defense ratings
shrunk with eight prior matches, league home and away rates from the current
and two previous regular seasons, the schedule-load shift and the rate clamp.
An average team (attack = defense = 1) takes the judged team's place:

- scored factor = the average team's expected goals against that opponent's
  defense, at that venue and schedule load, ÷ the league rate;
- allowed factor = the opponent's expected goals against the average team, the
  same way, ÷ the league rate.

The model's league rate pools the two previous seasons, so in a season that
scores more or less than they did, every defense would look weaker or stronger
than average and every team's values would shrink or grow alike. Each season's
factors are therefore divided by their mean over every team's completed
matches, scored and allowed separately, so both average 1: the average opponent
is that season's. Ratios between matches, including the venue effect, are
unchanged.

Goals for and xG for are divided by the scored factor, goals against and xG
against by the allowed factor, and the adjusted margin is their difference.
Ratings use every completed match of the season (hindsight), so earlier bars
can shift slightly as the season goes on. The judged match is left out of its
own ratings and of the league venue rates, so a dominant result cannot raise
its own baseline; only the season-wide normalizing divisor includes it. Only matches with xG contribute; a match without a kickoff
has no schedule-load term. Zero stays zero: a goalless match or clean sheet
earns no credit for the opponent's strength. Both scoring bases use the same
factors (from full xG); excluding penalties changes only the values divided,
non-penalty goals and npxG. Points and xPoints stay official. See
[ADR 0001](adr/0001-opponent-adjustment.md).

When checked, the chart shapes stay the same: unsmoothed Scoring balance keeps
its mirrored bars and Margins its two bar panels, with titles such as “Adjusted
goals” and “Adjusted goal margin”. An adjusted goal margin can differ in sign
from the result, so its direction note reads “Zero: level”. Rolling lines
average adjusted values with the usual window and lead-in, labeled “Adjusted goals scored” and so on. The
note adds “Adjusted to an average opponent at a neutral venue with even rest.”
and, when the season's xG coverage is incomplete, for example “Opponent ratings
use 231 of 232 matches with xG.” Adjusted goals stay fully available; adjusted
xG has gaps only where that match's own xG is missing.

With smoothing off, the tiles total adjusted goals and xG (for example
“46.12–39.85 adjusted goals” and “43.40–40.95 adjusted xG”) while W–D–L and points per match
stay official, under the note “W–D–L and points use official results.” (with
“, including penalties” when penalties are excluded). The Match values table
keeps its official columns and adds Adjusted goals for, Adjusted goals against,
Adjusted xG for and Adjusted xG against, sortable like the other derived
columns (descending first); with smoothing on, the rolling columns average
adjusted values. Tooltips and keyboard descriptions show each official value
beside its adjusted value (for example “Goals for: 4 · adjusted 2.81”), then
both factors:
“Opponent defense × venue × rest” (scored) and “Opponent attack × venue ×
rest” (allowed).

**Points vs xPoints** with Smoothing set to None (`trend-view=points&smoothing=none`)
plots one bar per match of **points − xPoints**, using the shared Head to head
chart and its venue encoding. The axis is fixed at −3 to +3 across teams,
seasons and both views, with a signed tick at every whole point. Missing xPoints keeps an empty chronological slot,
never a zero bar. If every match lacks xPoints, the chart is replaced by
“No xPoints is available for these matches.” The panel title is “Points −
xPoints”, with “Above zero: more points than xPoints.” The tooltip
and keyboard description include the match, official score, points, xPoints
and signed difference. A win cannot fall below zero, a loss cannot rise above
zero, and draws can fall either way, because xPoints is between 0 and 3.
This points-only view has no Penalties control or points note. Old links with
`penalties=exclude` are accepted and canonical links and forms drop the value.

Three equal All / Home / Away tiles show points vs xPoints totals and the gap as
above or below expected. “N of M matches” coverage appears only when xPoints
is incomplete.
Home is solid blue (`#1F6FA8`); away is orange (`#CF7424`) diagonal stripes on
paper with a thin orange outline. Swatches on the Home and Away tiles form the
venue legend. An incomplete xPoints population says `xPoints incomplete`, keeps
actual points, and withholds the expected total and gap.
A venue with no appearances says “No matches” (“No meetings” in Head to head),
with zero coverage rather than an incomplete-data warning.

Rolling mode retains two lines for points pace vs xPoints pace and titles the
panel “Points vs xPoints · N-match average”. Venue tiles appear only with
smoothing off. Without JavaScript the same tiles and table show the comparisons.

**Benchmarks** keep original units and draw horizontal reference means for the
season: the team's own mean, plus one comparison.

- League average weights every eligible team, including the selected one, by
  match appearances.
- Playoff-team and top-4 averages use the catalog's season-specific cut (four,
  six or eight). Completed seasons take membership from final total-points
  standings; a tie across the cut withholds the comparison, because that view does not
  reconcile historical official record adjustments.
- Shield winner is one team; Best team is the best full-season per-match mean
  for the chosen metric (lowest for Allowed), keeping tied holders, and needs
  complete expected coverage for every team.
- Active seasons label groups as projected. Membership comes from the default
  Forecast Lab model's baseline run (projected final points; Shield favorite by
  highest probability, keeping ties), sharing Forecast Lab's executor and cache.
  Means still use recorded matches. When no forecast is available, the
  comparison is unavailable rather than falling back to current standings.

When Benchmarks shows Points or xPoints (`average-series=points|xpoints`), the
view is points-only and hides Penalties. Carried exclusions are ignored and
removed from generated links and forms. Goals and xG benchmarks retain the
control.

#### Venue-local dates

Match dates show the venue's local calendar day, using the cached match's
`stadium_id` and the IANA zone catalog in
[`internal/fixtures/venues.go`](../internal/fixtures/venues.go). Neither the home
team, the browser, nor the server picks the zone, and there is no UTC or
usual-stadium fallback: unknown venues show `Venue date unavailable`, while
their kickoff instant still orders matches. The unavailable venue-date notice concerns
the stadium time zone only: every match retains ASA’s Home/Away designation
and stays in that venue tile, so All equals Home plus Away.

The catalog covers all 49 entries from the
[ASA NWSL stadia endpoint](https://app.americansocceranalysis.com/api/v1/nwsl/stadia)
as checked on 2026-10-01. Entries without geographic fields were confirmed from
official sources:
[Harvard's Jordan Field](https://gocrimson.com/sports/2026/6/18/know-before-you-go.aspx),
[Chicago's Northwestern match notes](https://chicagostars.com/assets/2026/03/RS-M01-LA-vs-CHI-Match-Notes.pdf),
[Seattle's Memorial Stadium history](https://www.reignfc.com/news/letter-njx5x),
[Fullerton's Titan Stadium](https://fullertontitans.com/sports/2023/8/3/athletics-titan-stadium.aspx),
[Osceola Heritage Park](https://www.osceola.org/Community/Parks-and-Public-Lands/Park-Hours-Rules-and-Reservations)
and [Denver's venues](https://www.denversummitfc.com/venues/). Two matches
missing a stadium ID are mapped to Inter&Co Stadium from match reports:
`Xj5YPveRMb` ([May 8, 2026](https://www.orlandocitysc.com/pride/news/match-report-barbra-banda-scores-late-game-winner-as-pride-beat-the-courage-1-0))
and `KXMeXv6vQ6` ([August 7, 2026](https://www.orlandocitysc.com/pride/news/match-report-orlando-pride-fall-3-1-to-racing-louisville-fc)).
A source stadium ID always wins over a correction. Add mappings only with
verified location evidence.


### Season by season

`view=team-history` shows one team's per-match goals, xG, points and xPoints for
every eligible season, reusing Compare teams' rates and coverage.

| Parameter | Values | Default |
|---|---|---|
| `team` | ASA team ID | Portland Thorns FC |
| `measure` | `for`, `against`, `difference`, `points` | `difference` |
| `series` | `goals`, `xg`, `both` | `both` |
| `context` | `on`, `off` (league context) | `off` |
| `history-sort` | `season`, `played`, or `<measure>-actual\|expected` | `season` |
| `history-order` | `asc`, `desc` | `desc` |

The chart is chronological with gaps for missing years; the table sorts from
unrounded values, with unavailable values last and newest season first on ties.
With `measure=points`, the chart shows Points and/or xPoints only, so Penalties
is hidden and a carried exclusion is ignored and removed from generated links
and forms. Other measures retain the control and points note when applicable.
For Goals and xG, Team History still shows official points and xPoints beside
the selected scoring measure, so its points note remains visible when the
penalty basis is excluded.

League context adds, per season, a bar from the lowest to the highest team rate
(an expected-value range needs complete coverage for every team that season),
plus dotted record lines from completed seasons only, labeled with the archive
start rather than claimed as all-time. Tied holders are all listed in the
"League records and season ranges" details.

### Head to head

`view=head-to-head` follows two ASA team IDs across eligible regular seasons.
Controls are Team, Opponent, View (Margins by default, Goals vs xG,
Points vs xPoints) and Penalties (Included or Excluded, hidden for
Points vs xPoints). The opponent list includes clubs met at
least once, each as "Name (N)", sorted by the ID's newest name, then ID. The
default is the most-met opponent, ties broken by newest name, then ID. A valid
never-met pair remains selected with "X and Y have not met in a regular-season
match." and a selected zero-meeting option. Blank, repeated, unknown or
same-as-team opponents return the standard 400.

The heading shows both clubs with decorative logos (`teamName` and
`clubLogoURL`); the View select names the selected view, so the heading does
not repeat it. The scope line follows, for example “Regular season 2016–2026 ·
26 meetings · 2026 in progress”. Small samples show numbers without a
conclusion. Active-season meetings enter immediately, and the table marks
"season in progress". The table caption carries the ASA team-ID and home
designation caveat.

**Margins** combines goal margin (goals for − goals against) above xG margin
(xG for − xG against), one bar per meeting in two aligned panels. Both panels
share a symmetric rounded axis with 15% headroom and the same chronological
slots and year labels. Up means win, zero draw, down loss for goal margin;
for xG margin, up means more xG for, zero level, down more xG against.
Excluding penalties labels the goal panel “Non-penalty goal margin”, with
“Zero: level” below it. Missing xG or penalty splits keep their slot without a bar; zero has a
short visible mark.

Three equal All / Home / Away record tiles give official W–D–L, points per
match, selected-basis goals for–against, xG for–against and meetings (counts
and coverage say “meetings” here, “matches” in Match by match). Home and
Away swatches are the venue legend. Non-penalty goals say “np goals”, with
“N/M with split” for incomplete coverage. xG totals require every match;
incomplete coverage says “xG incomplete” and “xG N/M meetings” rather than
summing a partial population as a complete total. Official W–D–L and points
stay fixed when excluding penalties, with the explanatory note. xPoints totals
are available in Points vs xPoints. The former Expected view is accepted as
an alias for Margins. Requests with `h2h-series=expected` (or explicit
`actual`) redirect to the same relative URL without `h2h-series`, preserving
all other selections; `both` and `points` remain in URLs.

**Goals vs xG** (`h2h-series=both`, preserving existing links) uses the same
two gap-bar panels and All / Home / Away tiles as unsmoothed Match by match.
The panels are **Goals for − xG for** and **Goals against − xG against**.
Above zero means more scored than xG for, or more conceded than xG against;
each panel explains its direction. The shared symmetric rounded axis takes
the largest magnitude across both panels, and both retain identical
chronological slots and year placement. Missing xG or penalty splits leave
empty slots, never zero bars. Each venue tile gives both goals vs xG totals,
the separate above/below gaps, and meeting coverage only when incomplete. Incomplete populations
withhold their totals and gaps. There is no separate venue legend or netted
margin-gap chart. Points comparisons live in Points vs xPoints. The option
label stays “Goals vs xG” when penalties are excluded, while the panels,
tiles, tooltip and table use non-penalty wording.

**Points vs xPoints** (`h2h-series=points`) plots official points − ASA xPoints
per meeting with the shared gap bars and fixed −3 to +3 axis. Its All / Home /
Away tiles show the same totals, gap and coverage rule as Match by match, with
coverage counted in meetings. Swatches live on these tiles,
so there is no separate venue legend. Missing xPoints retains its slot without
a bar, independently of xG or penalty-split coverage. The tooltip, keyboard
description, no-JavaScript list and values table include points, xPoints and the
signed points − xPoints difference. The panel title reads “Points − xPoints”,
and the zero-line explanation matches the season chart. This view has no Penalties control or points note;
a carried penalties parameter has no effect.

Penalties affects goals and xG in the two goal views through
`basisText`. With exclusion, Margins states the smaller split-covered
population; Goals vs xG withholds incomplete totals and shows explicit split
coverage.
Charts use non-penalty goals and/or npxG on that basis. Meetings without a split retain their
slot and official result in the tooltip and table but have no non-penalty bar.
Points and xPoints use every meeting and never change. Meeting lists and official
W–D–L also never change. Both goal views show an xG missing-note whenever
coverage is incomplete. Goals vs xG has no xPoints missing-note or points/basis
note; Margins uses the official-results note.
Points vs xPoints has neither a penalty-split nor an xG coverage note.

`history.SummarizeHeadToHead(inputs, teamID, opponentID, basis)` and
`HeadToHeadOpponents` use all-shots `TeamComparisonEligible` integrity checks.
`HeadToHead` accepts official and selected-basis summaries from one snapshot;
it does not apply non-penalty season exclusion to the meetings. Official facts
use `TeamMatch.ScoreFor`, `ScoreAgainst` and `Points`. Abandoned matches are
excluded; malformed completed results exclude their season. Distinct ASA IDs
stay separate, with no franchise lineage inferred (including Utah's gap years).
Playoff and cup meetings are excluded.

All three views use kickoff-instant order, match ID breaking ties and undated
meetings last. Home meetings are solid blue (`#1F6FA8`); away meetings have
orange (`#CF7424`) diagonal stripes on paper (`#fbfcf8`) with a thin orange
outline: 1.5px stripes every 3.4px at 45 degrees. Zero values have a short mark.
Color and pattern jointly encode venue; hollow columns remain reserved for
Team-seasons' in-progress seasons. The goal charts have a symmetric
round axis shared across both panels in both goal views, and points uses exactly ±3.
Years sit under their first meeting; overlapping intermediate labels are
skipped while first and last remain inside the plot. Up to forty columns fit
at 390px without page scrolling.

Hover, tap or keyboard focus shows the same transient tooltip and accessible
label: venue-local date, "season in progress" when applicable, venue,
opponent's season-specific name, official score and result. Non-penalty goals
are added when excluded. Margins includes goal margin, xG for, xG against
and xG margin. Goals vs xG includes selected-basis goals, xG for, xG against
and the two separate signed gaps, with `Unavailable` where missing. Hit
testing chooses the nearest slot
across the full plot height, including slots with missing values. Arrow keys,
Home and End move between meetings; Escape, blur or an outside tap dismisses
the tooltip. Pointer events queued before keyboard inspection cannot dismiss
it; a fresh hover or tap takes over. There is no pinned selection or detail
panel. `explore-geometry.js` owns chart bounds, bar values, hit testing and
year-label placement, tested in
`e2e/geometry_test.go`. Without JavaScript the chart is a chronological list
with the same basis and expected details, including a goal margin and xG margin
for every meeting in Margins, or both side gaps in Goals vs xG (with
`Unavailable` for missing observations). Date warnings and the ASA identity
and home-designation qualifications stay below the chart.

The values table is a native `<details>`, closed unless `h2h-sort` or
`h2h-order` is present, with summary "Table: all N meetings". Columns are Date,
Season, Venue, Opponent, official Score, Result and Points. Excluding penalties
adds Non-penalty goals. Both goal views add xG for and xG against. Margins
adds xG margin; Goals vs xG adds Goals for − xG for and Goals against − xG
against. Missing values say `Unavailable`. Text columns align left, with no logos or row
links. Sort links use kickoff order, venue, official result points or official
goal margin; ties use newest-first kickoff order and missing dates stay last.
`exploreMatches` supplies venue-local dates, saying `Venue date unavailable`
rather than using UTC as a fallback. The table scrolls inside its own container
on narrow screens, with a scroll hint.

| Parameter | Values | Default |
|---|---|---|
| `team` | ASA team ID | Portland Thorns FC |
| `opponent` | Different recorded ASA team ID | Most-met opponent |
| `h2h-series` | `actual` (Margins), `expected` (legacy alias for Margins), `both` (Goals vs xG), `points` | `actual` (omitted from URLs) |
| `h2h-sort` | `date`, `venue`, `result`, `margin` | `date` |
| `h2h-order` | `asc`, `desc` | `desc` |
| `penalties` | `include`, `exclude` (two goal views; ignored for points) | `include` |

The payload stores each match once with shared team identities and
season-specific names. It carries official scores, both scoring bases and
independent expected values; the browser derives either team's orientation,
records, expected totals and comparison totals. An app test bounds the raw
payload below 550 KB. Margins keeps numeric totals and coverage counts separate
from display formatting. `explore_margins.go` owns the Go formatting step;
`explore-margins.js` owns the browser's equivalent numeric and formatting steps.
Browser tests compare every tile line and accessible W–D–L label with the Go
render for complete xG, incomplete xG and a non-penalty missing-split population.

Tile calculation stays in the browser for in-place team, opponent and basis
changes. Precomputing formatted tiles for every oriented pair in both bases
would add a matrix that grows with the square of the team count to the bounded
match archive. Both renderers instead consume explicit per-match goal
coverage from the scoring calculation. That calculation still requires valid
xG and a consistent penalty split for non-penalty goals: a raw split without
valid xG, or valid xG without a split, covers neither non-penalty goals nor
npxG. All-shots goals remain covered when xG is missing. Match by match retains
its existing exclusion of seasons with incomplete non-penalty coverage.
Controls and sort links update the URL in place from that payload without a
data request and restore through Back/Forward and reload. Separate GET forms
allow changing Team without carrying the previous Opponent when JavaScript is
off. Changing Team resets Opponent to that team's default.

## Testing and previews

`make test-explore` runs the calculation and HTTP checks. `make test-e2e`
drives the `internal/apptest` scenarios in headless Chromium, covering URL
state, Back/Forward, no-request view switches, sorting, keyboard inspection,
incomplete-data labels, plot geometry, and no horizontal overflow at 390px.
Head to head also checks three equal tiles at 320–390px without horizontal
overflow, the full tiles below the controls at 390×812 and both Margins panels,
tile swatches without a separate legend,
first/last year labels, and hover, touch and keyboard tooltip parity. Tooltip
checks wait for Chart.js's animation-frame event dispatch, including reopening
the same column after Escape and an outside tap.
Look at layouts, colors and touch behavior by eye.

The shared HTTP test helper `getPage` uses 20 forecast iterations. Each call
creates a fresh application, and active-season benchmarks can trigger a
forecast even when a historical season is selected. The small test budget
keeps repeated page requests practical under the race detector.

`NWSL_CONFIG_FILE=/dev/null go run ./cmd/preview -scenario <name>` serves a
seeded temporary cache under `/nwsl-season/` on loopback and prints a matching
Explore URL. It starts no scheduler, makes no ASA request, and deletes its
cache on exit. `-no-script` adds a CSP that blocks page scripts. The same
scenarios are `apptest.Seed` (cache-backed) and `apptest.Scenario` (in-memory).
Seeded lifecycle follows the calendar year, so earlier seasons are completed
and the current year is active.

| Scenario | Data |
|---|---|
| `default` | 2018, 2019, 2021, 2022 with 20 matches each; xG complete for 2018 and 2021, 19 of 20 for 2019, none for 2022 |
| `thresholds` | Recorded ASA regular-season score/identity tuples and postseason matches for 2016–2025 (no 2020), without xG or penalty splits. Regular-season dates and match IDs are synthetic; postseason chronology, shootouts and identities are recorded. Thresholds derive from results and sparse exceptions, including official 2021 forfeits and inferred 2022/2023 equal-points boundaries |
| `overview` | 2016–2026 (no 2020), 20 matches each among six clubs covering all five goal bins, a varying home edge, full xG and xPoints; 2018 has two teams far behind, 2024 a separate top three, 2025 one runaway leader |
| `empty` | 2024 with 19 matches and no xG, below the 20-match minimum |
| `single` | 2024 with 20 matches of one goal each and no xG |
| `teams` | 2025 and 2026, 64 matches among the 16 real clubs; 2026 has one match without xPoints and one without xG; 2024 has no matches |
| `no-xg` | Like `season-trend`, but only each season's first match has xG, so no team has complete xG |
| `team-history` | `teams` plus 2016–2023 (no 2020), 96 played matches per loaded season (12 per club), scores varying by year and matches dated within their season; 2026 adds 144 pending matches to complete its inventory. 144 team-seasons; Portland–Seattle has 26 meetings (13 home, 13 away), other pairs have several meetings, and Portland–Bay has never met. Match assignment swaps preserve all team match counts. Missing xPoints with complete xG and the reverse, plus an active season above the ten-match gate. `TeamHistoryBelowGate` marks its last four played rounds pending to test the same scenario at eight matches per club. `TeamHistorySmall` retains only 2025, giving 16 ranked rows for both end labels without a break. The reverse coverage case is in-memory only: SQLite requires both valid xG values to retain an observation, including its xPoints |
| `season-trend` | 2025 and 2026 as 240-match double round robins; the last eight 2026 matches are unplayed; 2024 has no matches |

For a Team-seasons chart with twenty or fewer rows, run
`NWSL_CONFIG_FILE=/dev/null go run ./cmd/preview -scenario teams` and open
`/nwsl-season/explore?view=team-seasons` on its printed loopback address. Its
completed 2025 season supplies sixteen ranked rows; 2026 has only eight played
matches per team and stays below the active-season gate. Both end labels appear,
with no break and no duplicated team-season. Use `-no-script` to inspect the
native disclosure and submit controls with page scripts blocked.
