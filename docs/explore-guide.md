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
| League trends | League, across seasons | Scoring trend | `view=trend` |
| | | Goal distribution | `view=distribution` |
| | | Home advantage | `view=home-advantage` |
| | | Parity | `view=parity` |
| | | Scoring table | `view=table` |

An omitted view opens Actual vs expected; a view-less link that carries
`metric` (from when Scoring trend was the default) opens Scoring trend.
`exploreViewLabels` in `internal/app/explore.go` holds the tab names. Explore
uses the current season's navigation with Explore marked active.

Every selection lives in the URL and restores through reload and Back/Forward.
The server renders each view with GET forms, sortable header links and native
disclosures, so everything works without JavaScript; the script switches views
and selections in place from the same payload, without another data request.
Blank, repeated, or unsupported parameter values return a 400 "Invalid Explore
selection" page. Chart.js and its datalabels plugin are vendored under
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
- Never derive expected points from xG, and keep ASA's retrospective xPoints
  separate from Forecast Lab projections.
- Each view owns its eligibility rule; there is no global sample threshold. An
  active season is legitimate data, shown as a current pace with its matches
  played, and is never presented as its final place in history.
- Per-match rates adjust for games played, not for opponents or scoring era,
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
  an accessible description.
- Label values, units and reference lines directly. Over-performance views use
  a zero line and a consistent favorable direction. Color is never the only
  signal; no dual axes, unexplained composite scores, or one line per team.
- Describe gaps as above or below expected, not luck; finishing, goalkeeping,
  game state, model error and randomness all contribute.
- Put coverage and sample notes next to the result they qualify.
- Essential detail works by touch and keyboard without hover; on narrow screens
  selected detail goes below the chart.
- URLs share the view and its selections, not a frozen answer; data may update.

A new view needs an entry in `exploreViewLabels` and the table above, a pure
calculation over the archive read, canonical URL state, `make test-explore`
coverage, and browser checks at desktop width and 390px.

## Shared data rules

### Matches and seasons

`history.SummarizeScoring` accepts public, source-backed `Regular Season`
catalog entries with fixture capability and keeps a row for every season,
including unloaded ones, sorted by season. Cups and playoffs are rejected.

A match is played only when its status is `FullTime` and both scores are
present and nonnegative. `Abandoned` has its own terminal count; a malformed
`FullTime` fixture counts as an invalid completed result, not a score; other
statuses are pending. Team views orient home and away appearances to each team
(points are 3/1/0) and require distinct, nonempty team IDs per fixture.

### Expected values

xG and xPoints join a played match by fixture ID and matching home/away teams,
and need the xG capability and an available observation. xG must be finite and
nonnegative; xPoints must be finite and between 0 and 3. Zero is valid. Coverage
is counted separately for each. A league or team xG rate exists only when every
counted match has valid xG; the same holds for xPoints.

### Season eligibility

A season is plotted only with at least 20 valid played matches, available source
readiness, a known non-upcoming lifecycle, no invalid completed results, no
known-incomplete inventory, and no pending fixture in a completed lifecycle.
Team views use the same checks with a one-result minimum. Unknown inventory
does not exclude a season. Partial xG never excludes a season's goals.

Exclusion codes, in order: `source_unavailable`, `lifecycle_unknown`,
`upcoming`, `inventory_incomplete`, `historical_results_incomplete`,
`invalid_completed_results`, `incomplete_non_penalty_data` (non-penalty basis
only), `below_minimum_matches`.

### Penalties basis

Every view accepts `penalties=include` (default, omitted from URLs) or
`penalties=exclude`. It is a server-rendered Penalties select, last in each
view's data-control row and never in the navigation; links and forms carry it.

`history.ScoringBasis` selects the arithmetic for every consumer. On the
non-penalty basis, per side: non-penalty goals = goals − penalty goals, and
non-penalty xG = max(0, xG − penalty xG). A match counts toward goals and xG
only with available xG and a consistent ASA penalty split; without a split its
non-penalty goals are unknown. A season with any played match outside that
coverage gets `incomplete_non_penalty_data`. Team match records keep the
official score beside the basis goals.

Official points, ASA xPts, benchmark groups and forecasts never change with the
basis (ASA publishes no non-penalty xPoints). Where points appear on the
non-penalty basis, a note says "Points include penalties; ASA publishes no
non-penalty expected points." Labels follow the basis (`xG` → `npxG`, `Goals` →
`Non-penalty goals`, `xGD` → `npxGD`) through `basisText` in Go and `T` in
`explore.js`; option text and select labels never change, so controls keep
their size. Fixture results add an `npxG` line when a match had penalty xG.

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
than away sides per played match: goals against xG and points against xPts, as
two small multiples that share the season axis. Each mark is home minus away,
so above the labeled zero line the home side did better. There are no dual
axes, and missing calendar years, including 2020, are gaps. Each panel's
expected mark needs complete coverage of its own metric (xG for goals, xPoints
for points), so one can be missing while the other and the actual values remain;
a note names the seasons without each. An eligible active season is plotted and
noted as in progress through N matches. The table lists season, matches,
home-minus-away goals, xG, points and xPts per match, and the home win, draw and
loss share, sorted from unrounded values (`home-sort=season|matches|goals|xg|points|xpoints|results`,
`home-order=asc|desc`, default season descending, with unavailable values last). `penalties=exclude` switches the goals panel to
non-penalty goals against npxG; points and xPts never change and carry the
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
otherwise that season has no xPPG mark and an `xPts incomplete` note. An active
season is plotted hollow with a dashed segment and noted as in progress; its
chance level uses matches played so far, so it is wider. The values table lists
season, teams, matches per team, PPG spread, chance level, xPPG spread, gap and
IQR, sorted from unrounded values (`parity-sort=season|teams|matches|spread|chance|xspread|gap|iqr`,
`parity-order=asc|desc`, default season descending, unavailable values last).
Quartiles use linear interpolation between closest ranks (`history.Quantile`,
R and NumPy's default), and the chart receives them from the server. Points
never change with `penalties`; that basis keeps its eligibility rule and the
standard points note. The calculation is `history.SummarizeParity`.

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
`xG incomplete` or `xPts incomplete` label on the gap chart, and are named in a
note on the scatter plots.

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
| `trend-view` | `balance` (Scoring balance), `compare` (Actual vs xG), `difference`, `points` (Points vs xPoints), `relative` (Benchmarks) | `balance` |
| `series` | `goals`, `xg`, `both` | `both` |
| `trend-mode` | `match`, `rolling` | `rolling` |
| `window` | `3`, `5`, `10` | `5` |
| `average-series` | `goals`, `xg`, `points`, `xpoints` (Benchmarks) | `xg` if `series=xg`, else `goals` |
| `average-reference` | `team`, `league`, `playoff`, `top-four`, `shield`, `best` | `league` |

Matches are numbered by kickoff instant (`fixtures.ParseKickoff` handles both
RFC 3339 and ASA timestamps), with fixture ID breaking ties; a missing kickoff
sorts last with a warning.

A rolling average includes the current match and resets each season. Until the
window fills it averages all matches so far (`min(played, window)`), drawn as a
dotted lead-in. An xG or xPoints average needs every observation in its exact
window, so missing expected data breaks that line until it leaves the window;
it is never interpolated.

**Benchmarks** keep original units and draw horizontal reference means for the
season: the team's own mean, plus one comparison.

- League average weights every eligible team, including the selected one, by
  match appearances.
- Playoff-team and top-4 averages use the catalog's season-specific cut (four,
  six or eight). Completed seasons take membership from final total-points
  standings; a tie across the cut withholds the comparison, because historical
  tiebreak rules are not verified.
- Shield winner is one team; Best team is the best full-season per-match mean
  for the chosen metric (lowest for Allowed), keeping tied holders, and needs
  complete expected coverage for every team.
- Active seasons label groups as projected. Membership comes from the default
  Forecast Lab model's baseline run (projected final points; Shield favorite by
  highest probability, keeping ties), sharing Forecast Lab's executor and cache.
  Means still use recorded matches. When no forecast is available, the
  comparison is unavailable rather than falling back to current standings.

#### Venue-local dates

Match dates show the venue's local calendar day, using the cached fixture's
`stadium_id` and the IANA zone catalog in
[`internal/fixtures/venues.go`](../internal/fixtures/venues.go). Neither the home
team, the browser, nor the server picks the zone, and there is no UTC or
usual-stadium fallback: unknown venues show `Venue date unavailable`, while
their kickoff instant still orders matches.

The catalog covers all 49 entries from the
[ASA NWSL stadia endpoint](https://app.americansocceranalysis.com/api/v1/nwsl/stadia)
as checked on 2026-10-01. Entries without geographic fields were confirmed from
official sources:
[Harvard's Jordan Field](https://gocrimson.com/sports/2026/6/18/know-before-you-go.aspx),
[Chicago's Northwestern match notes](https://chicagostars.com/assets/2026/03/RS-M01-LA-vs-CHI-Match-Notes.pdf),
[Seattle's Memorial Stadium history](https://www.reignfc.com/news/letter-njx5x),
[Fullerton's Titan Stadium](https://fullertontitans.com/sports/2023/8/3/athletics-titan-stadium.aspx),
[Osceola Heritage Park](https://www.osceola.org/Community/Parks-and-Public-Lands/Park-Hours-Rules-and-Reservations)
and [Denver's venues](https://www.denversummitfc.com/venues/). Two fixtures
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

League context adds, per season, a bar from the lowest to the highest team rate
(an expected-value range needs complete coverage for every team that season),
plus dotted record lines from completed seasons only, labeled with the archive
start rather than claimed as all-time. Tied holders are all listed in the
"League records and season ranges" details.

## Testing and previews

`make test-explore` runs the calculation and HTTP checks. `make test-e2e`
drives the `internal/apptest` scenarios in headless Chromium, covering URL
state, Back/Forward, no-request view switches, sorting, keyboard inspection,
incomplete-data labels, plot geometry, and no horizontal overflow at 390px.
Look at layouts, colors and touch behavior by eye.

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
| `overview` | 2016–2026 (no 2020), 20 matches each among six clubs covering all five goal bins, a varying home edge, full xG and xPts; 2018 has two teams far behind, 2024 a separate top three, 2025 one runaway leader |
| `empty` | 2024 with 19 matches and no xG, below the 20-match minimum |
| `single` | 2024 with 20 matches of one goal each and no xG |
| `teams` | 2025 and 2026, 64 matches among the 16 real clubs; 2026 has one match without xPts and one without xG; 2024 has no fixtures |
| `no-xg` | Like `season-trend`, but only each season's first match has xG, so no team has complete xG |
| `team-history` | `teams` plus 2016–2023 (no 2020) with scores varying by year |
| `season-trend` | 2025 and 2026 as 240-match double round robins; the last eight 2026 matches are unplayed; 2024 has no fixtures |
