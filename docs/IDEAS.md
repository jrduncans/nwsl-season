# Historical data exploration

## Goal

Make historical patterns, relationships, and unusual performances visible.
The discovery should come from seeing the data: a visitor might notice a trend,
recognize an unusual team-season, or find a historical comparison they had not
thought to look for.

These example questions describe user needs and serve as internal design checks:

- Is this among the best or worst team-seasons in the available archive?
- Which teams got results furthest above or below their underlying play?
- Did an apparent over-performance last, or regress as the season continued?
- Is the league becoming higher-scoring, more balanced, or less
  home-field-friendly?

They are not proposed navigation labels or a required question-and-answer flow.
A visitor may simply want to explore trends over time. Views should support
multiple discoveries without prescribing a question or generating a verdict.

## Product direction

Organize **History** around a few straightforward subjects: **Team seasons**,
**League trends**, and **Match records**. Lead with charts and descriptive
titles such as “Scoring by season,” “Results and expected points,” and “Season
progression.” Make an interesting initial view available without configuration.

Curate the available views and defaults, while leaving interpretation open:

- Choose a small set of useful visual relationships and comparison groups.
  The collection is not a general-purpose chart builder and does not need to
  expose every combination of metric, filter, and chart type.
- Give each view only the controls that reveal a meaningful alternative, such
  as a team-season selector or a results/underlying-chances choice. Do not
  default to an advanced filter panel, arbitrary date ranges, column pickers,
  or user-configurable sample thresholds.
- Use charts as the primary presentation wherever they remain legible and
  meaningful. Use tables or scorecards when exact match details or ordering
  are clearer that way. Keep exact-value tables available as supporting data.
- Let selecting a mark reveal detail, including a team-season profile or
  trajectory. Preserve the selected team-season when changing a compatible
  view or metric, even when it is outside the displayed historical extremes.
- Label relevant values, reference lines, units, and necessary context. Avoid
  mandatory answer sentences, editorial question cards, or annotations that
  tell visitors what conclusion to draw.
- Make selection and detail usable by touch and keyboard. Essential context
  must remain available without hovering. On narrow screens, place selected
  detail below the chart rather than squeezing in a permanent side panel.

The views below are candidates for this curated collection, not a commitment
to ship every view or expose every listed metric as a control. Their questions
remain design checks: can someone see the answer in the visual?

## What the current archive can support

The public catalog covers regular-season fixtures for 2016–2019 and 2021
onward; actual availability must be checked against the local cache. There was
no 2020 regular season. Cached completed fixtures provide scores, kickoff, and
home and away teams, with xG and ASA expected points where available. Track xG
and expected-points coverage separately: valid xG does not guarantee expected
points are present. These inputs support team-season records, same-match-count
trajectories, league trends, and match record books without contacting ASA
during a page request.

The archive omits the league's 2013–2015 regular seasons. Use “since 2016” or
“in the available archive” for historical claims, and show the eligible years.
Do not label records “all-time” or “ever” without complete historical scope.

The first version should use regular-season matches only. Playoffs and cup
stages have different formats and should get their own deliberately designed
comparisons later.

The archive does not currently contain event timing, lineups, player minutes,
pre-match betting odds, or a historical pre-match forecast for every match.
Ideas that need those inputs should not be approximated from the data we do
have.

## Comparison rules

Historical comparisons need a few rules to stay honest:

1. Compare team-seasons primarily with per-game rates. League size and
   schedule length have changed, and at least one historical season has uneven
   games played. Rates adjust for match count, not opponent strength, schedule
   balance, or scoring era; a statistical record is not a strength-adjusted
   judgment of the best team.
2. Describe an active team-season as its **current pace**, not its final place
   in history. Offer a “through the same number of matches” comparison when a
   historical trajectory is available.
3. Show games played with rates and in selected-mark detail. Choose and document
   a meaningful minimum sample for each view. Apply eligibility automatically;
   early-season views can deliberately compare smaller equal samples without
   adding a general sample-size control.
4. Require complete xG or expected-points coverage for any aggregate that uses
   it, measured over the selected matches. A first-ten-matches comparison can
   qualify even if a later match lacks xG. Use the same matches in both sides
   of each actual-versus-expected comparison. Never silently rank a partial
   aggregate alongside complete seasons. Missing expected points excludes a
   team from that metric, not from score-only or fully covered xG views.
5. Keep regular season, playoffs, and cups separate unless a view explicitly
   explains why they are combined.
6. Preserve the club name used in that season before presenting a feature as
   an historical record book. The current team catalog is keyed by ASA team ID
   and may display a club's current name for an older season.
7. Treat “luck” as playful shorthand, not a causal claim. Finishing,
   goalkeeping, game state, model error, and randomness can all contribute to
   an expected-versus-actual gap.
8. Define shared ranks for ties and rank before display rounding. Derive ranks,
   percentiles, and counts from the same eligible population, explicitly
   deciding whether a selected active team-season is included. Selecting or
   finding a team must not silently change its comparison population.
9. Distinguish an unfinished season from missing historical data. Label active
   seasons with their sample and scope, and explain exclusions near the visual.
   Do not add uncertainty bands until their method and interpretation are
   defined; an observed historical range is not a confidence interval.

## Metric vocabulary

Use one definition everywhere in copy, tables, charts, and URLs.

| Label | Definition | What it helps answer |
| --- | --- | --- |
| Points per game (PPG) | `points / matches played` | Best and worst results |
| Expected points per game (xPPG) | `ASA xPoints / xPoints-covered matches` | Shot-based expectation alongside actual results |
| Goal difference per game (GD/G) | `(goals for - goals against) / matches played` | Scoreline dominance |
| Expected goal difference per game (xGD/G) | `(xG for - xG against) / xG-covered matches` | Underlying chance dominance |
| Results over-performance | `(points - xPoints) / xPoints-covered matches` | Results above or below ASA's shot-based expectation |
| Finishing over-performance | `(goals for - xG for) / xG-covered matches` | Scoring more or less than the chances suggest |
| Defensive over-performance | `(xG against - goals against) / xG-covered matches` | Conceding fewer or more than the chances suggest |
| Goal-difference over-performance | `(GD - xGD) / xG-covered matches` | Combined finishing and defensive gap |

Positive values should consistently mean “more favorable than expected.” Use
“results over-performance” in titles and explanatory copy; “luckiest” can
appear in a question or social description with quotation marks.

For aggregates using expected values, completeness is required within the
selected scope, so all numerator terms and the denominator refer to the same
matches. ASA expected points here are a retrospective shot-based measure, not
Forecast Lab's projected season points.

## First collection: team-seasons in history

### 1. Team-season results and historical extremes

**Design checks:** Where does a selected team's current season sit in the
archive? Which team-seasons combined strong results with strong underlying
chances? Which performances stand out?

**Primary presentation:** a scatterplot of expected points per game on the
x-axis and actual points per game on the y-axis, with equal axis scales and an
equality line. Each mark is a team-season. This one relationship exposes strong
and weak performances as well as results above and below expectation, without
choosing one interpretation for the reader.

Use neutral marks for historical context and highlight the selected team-season.
Label a small number of notable marks; expose team, season, matches, and exact
values through accessible selection. Handle overlapping marks so coincident
team-seasons remain discoverable. The complete table belongs under “View data.”

Where ordering itself is useful, add a focused **Historical extremes** view
using ranked horizontal dots or bars. A limited results/underlying-chances
choice can expose PPG or xGD/G; do not make every metric a mandatory column or
an additional chart. Score-only extremes remain available without xG.

Use finished seasons for the initial archive overview. When arriving from a
current team's page, select that team and prefer comparison through its number
of completed matches where supported. Show the comparison scope explicitly.
These are deliberate defaults; all comparison modes need not become controls.

### 2. Results above and below expectation

**Design checks:** Which teams have the largest actual-versus-expected gaps?
Which excellent records were backed by xG, and which were not?

**Best presentation:** horizontal diverging bars ranked by results
over-performance. A zero reference line makes direction immediately clear.
This is a focused alternative to the scatterplot, useful when comparing gaps
directly. Selecting a team-season should reveal three related measures:

- points minus xPoints per game;
- goals minus xG per game; and
- xG against minus goals against per game.

Do not collapse those measures into one unexplained “luck score” or present
them as additive components of the points gap. Finishing and defensive gaps
sum to the goal-difference gap, not to points minus expected points. Keep
points and goals on separately labeled scales. Two teams can have similar
results gaps with very different finishing and defending patterns.

### 3. Season trajectories

**Design checks:** Was a bad start historically bad? When did a great season pull
away? Did the apparent luck regress?

**Best presentation:** a line chart with team match number on the x-axis, not
calendar date, so teams with postponements remain comparable. Candidate metrics
for a small set of deliberate views include:

- cumulative PPG;
- cumulative GD/G and xGD/G;
- cumulative points minus xPoints per game; and
- rolling PPG or xGD/G over the last five matches.

Default to one selected team-season, the historical median and middle 50% band,
and at most two useful comparators such as the record season and the closest
historical pace. Define the comparator selection rule before implementation.
Show the eligible sample at each match number as shorter seasons drop out.
Avoid a line for every team-season. A data table should provide each point for
keyboard and screen-reader users.

Cumulative averages alone do not establish whether over-performance persisted.
If a view examines regression after a cutoff, show performance in the subsequent
matches separately; a declining cumulative average can conceal that distinction.

### 4. Team-season profile

**Design checks:** Was this team great because of its attack or defense? Is its
record stronger than its underlying play?

**Best presentation:** aligned percentile strips or small horizontal dot plots
for PPG, goals for/G, goals against/G, xG for/G, xG against/G, and results
over-performance. Do not use a radar chart: the shared horizontal scales make
historical rank and weak spots much easier to compare.

Include a basic version in the first team-season collection as reusable detail
reached by selecting a mark in another view. For percentile strips, consistently
orient favorable performance toward the same end, including goals conceded.
Raw-value plots with different units need their own labeled scales.

## Second collection: how the league has changed

### 5. Scoring by season

**Design checks:** Are there more goals this year? Are moderately high-scoring
matches becoming more common, or are a few wild matches pulling up the mean?
Are teams creating more chances, or just finishing more of them?

Useful measures to represent include:

- average goals per match;
- share of matches with at least three goals;
- share of matches with at least four goals;
- share of scoreless matches;
- average xG per match; and
- goals minus xG per match.

**Best presentation:** lead with a season-by-season dot-and-line chart of goals
per match. Show xG in an aligned plot or a simple goals/xG choice when coverage
allows. Use descriptive titles, labeled values, and the active season's sample;
the view should support exploring any year without centering a claim about the
current season. Do not connect a line through the missing 2020 regular season
without marking the gap.

Below it, use a 100% stacked bar for each season with non-overlapping total-goal
bins: 0, 1, 2, 3, and 4+. This shows the shape of scoring without making the
user choose an arbitrary threshold. The 3 and 4+ segments together answer the
“3 or more” question, while the 4+ segment answers the more extreme version.
Keep the exact percentages in an expandable table. These measures do not all
need separate controls: the distribution already exposes several thresholds.

An additional goals-minus-xG plot can expose changes in finishing relative to
chance creation, without asserting a causal explanation. Give each panel its
own clearly labeled y-axis rather than using a dual axis. Label an active year
“through N matches.” Start with the main trend; add supporting plots only where
they improve the comparison rather than overwhelming it.

### 6. Home advantage over time

**Design checks:** Has home-field advantage weakened? Does the effect show up in
chance creation as well as results?

**Best presentation:** season-by-season lines for home-minus-away goals, xG,
and points per match. These should be aligned small multiples with a visible
zero line. The persisted venue summaries may already provide most of the
aggregate input.

### 7. Parity over time

**Design checks:** Which season was the most competitive? Is the table becoming
more compressed as the league grows?

**Best presentation:** one annotated line per parity measure, with a plain
language explanation that lower spread means more parity. Candidate measures:

- standard deviation of team PPG;
- gap between the top and bottom team's PPG; and
- interquartile range of team xGD/G.

Prefer the standard deviation or interquartile range as the headline; the
top-to-bottom gap is intuitive but overly sensitive to one extreme team.
Default to completed seasons. A wider spread in an active season can reflect
fewer matches played, so do not present it as evidence of lower parity without
comparable season progress. Per-game rates alone do not resolve this issue.

### 8. Shield and playoff thresholds

**Design checks:** Are the thresholds rising? Which was the strongest team to miss
the playoffs or the weakest to qualify?

**Best presentation:** season dots and a connecting guide for Shield-winner
PPG and playoff-cut PPG, paired with a table of the teams immediately above and
below the cut.

**Dependency:** add or verify official historical finishing order and Shield
winners. Existing historical cut-line counts are known, but old tiebreak rules
are not fully represented. Do not infer an official winner or qualifier in a
tied table from the current ordering code.

## Third collection: match records and additional comparisons

Choose charts where a relationship or change is the point, and compact tables
or scorecards where the match details would make a chart illegible. The
team-season comparisons below can join Team seasons as the collection grows.

### 9. Match heists and hard-luck losses

Use ranked bars where the gap is the focus, or compact scorecards where the
match detail is more useful. Rank wins by the winning team's points minus
expected points; show hard-luck losses from the losing team's perspective.
Show score, xG, expected points, opponent, venue, and date. Separate wins and
draws so a single ranking does not mix unlike outcomes.

### 10. Biggest scorelines and xG mismatches

Candidate groups include largest margin, most combined goals, largest xG
margin, largest winner with lower xG, and most goals from the least xG. Choose
a small useful set, presented as ranked bars or compact match records. Avoid
calling an outcome an “upset” without a historical pre-match strength or odds
estimate.

### 11. First half versus second half

Rank completed team-seasons by their change in PPG or xGD/G between equal-size
halves. A slope chart is useful for a curated top-ten risers and fallers; the
full result belongs in a table. Split odd match counts deterministically and
state the rule.

### 12. Consistency and chaos

Compare the spread of five-match rolling PPG or xGD/G within a team-season.
This can answer which good teams were relentlessly steady and which arrived
via streaks. Require enough matches for several non-overlapping observations
or clearly explain the overlapping rolling window.

### 13. Head-to-head history

For two selected clubs, show the chronological results and an aggregate record
split by home venue. A table plus a narrow cumulative-wins line is likely more
useful than a league-wide matrix.

**Dependency:** decide how relocations, rebrands, and folded clubs map to a
historical franchise identity, while retaining the name shown in the season.

## Suggested delivery order

Detailed implementation packets for the first scoring-by-season release are in
[History implementation packets](history/README.md). They define the initial
scope, dependencies, fixed decisions, tests, and handoffs for smaller models.

### First shippable view: scoring by season

View 5 remains the smallest useful vertical slice. It needs no historical
franchise-name decisions or official finishing-order metadata, and it exercises
the foundations the rest of the collection will need:

1. A cache-only read across historical regular seasons.
2. Reusable score and xG coverage rules.
3. A responsive historical chart with a supporting exact-value HTML table.
4. Descriptive labels and sample/coverage context derived from the same
   eligible observations as the chart.
5. A useful default and a shareable URL for any supported view selection.

Start with goals per match and add the xG comparison and goal-distribution
detail as they earn their space. Do not require a question picker or a generated
answer to make this a complete first release.

### First full collection: team-seasons in history

Next, extend **History** with a connected set of team-season visuals:

1. A results-versus-expected-points scatterplot with a useful archive default.
2. A basic selected team-season profile and a trajectory against historical
   context through the same number of matches.
3. Focused ranked dots or diverging bars for historical extremes where they
   add useful comparisons beyond the scatterplot.
4. A small set of meaningful selections preserved in shareable URLs.
5. Complete server-rendered data and context available without JavaScript;
   JavaScript enhances the experience with charts and selection behavior.

This first slice establishes the reusable historical aggregate, eligibility
rules, metric definitions, and selected-team interaction needed by most of the
later ideas. Readers can discover dominance, unusual results, or historical
counterparts within the same visual relationships. The exact first-release
chart set should stay small; the remaining candidates are future options.

## Product and implementation guardrails

- All page requests read SQLite only. Historical views must never trigger an
  ASA request.
- Gate each view by the catalog capability it actually needs. Score-only
  comparisons can remain available if xG is unavailable.
- Put coverage and sample-size exclusions next to the result, not in a distant
  methodology page.
- Keep the complete exact-value table available even when a chart is the main
  presentation. “View data” is supporting access, not a requirement to put a
  large table beside every chart. Provide an accessible chart description
  without prescribing an editorial conclusion.
- Use direct labels and a highlighted selected team-season; render the rest of
  history neutrally. Do not rely on color alone.
- Use zero lines for over-performance metrics and keep positive direction
  visually consistent.
- Avoid dual axes, unexplained composite scores, and league-wide spaghetti
  charts.
- Make URLs shareable and preserve the selected season, team, metric, and
  comparison mode where those selections exist. A normal link preserves the
  view and may show updated data later; do not promise a frozen historical
  answer without a separately designed snapshot feature.
- Test aggregation separately from HTTP and presentation. Historical
  statistics should be deterministic pure calculations over cached inputs.

## Later data opportunities

If the source data expands, revisit player-era records, goal timing, comeback
probability, lineup continuity, travel effects, and true pre-match upsets. Keep
these out of the first implementation unless their historical inputs are
persisted and coverage can be shown honestly.
