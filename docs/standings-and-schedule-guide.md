# How standings and schedule difficulty work

Both are calculated on read from cached matches. Neither is stored, and
schedule difficulty never changes the official standings.

## Standings

[`internal/standings`](../internal/standings/standings.go) turns completed
cached matches into a table. Unplayed, postponed and abandoned matches are ignored;
every participating team appears, including teams with zero points.

Regular-season pages and the standings CLI apply the source-backed historical
awards and point deductions from the competition exception file before ranking.
Awarded 3–0 matches contribute to P/W/D/L, goals and every tie-break criterion;
point deductions reduce points and PPG. Awards reconcile against observed
full-season match counts, skipping awards already counted by ASA and reporting
inconsistent counts instead of guessing. These adjustments never change cached
matches, results pages or xG (which describes played matches). Results and
matches remain readable when partial cached data cannot reconcile official
standings adjustments; their xG completeness warnings use raw match records.
Other pages fall back to the unadjusted table with a note when reconciliation
fails, rather than failing to render. The standings CLI still reports the error.

`RulesForSeason` selects the regular-season policy by year. Points come first:

- **2013–2019 and 2021:** head-to-head win–loss record (mini-league PPG for
  three or more clubs), overall goal difference, overall goals scored, then
  the same three criteria for away matches and for home matches. If a multi-club
  group shrinks to two, restart at the two-club head-to-head criterion.
- **2022 onward:** goal difference, total wins, goals scored, head-to-head
  points among tied teams, then head-to-head goals scored. Remaining subgroups
  continue with the next criterion, without restarting.

See the [historical rules audit](historical-outcomes-audit.md#season-specific-tiebreakers)
for sources. This policy selection applies to season standings, the standings
CLI and historical outcome derivation. It does not enable historical forecasts
or clinching. Future seasons inherit the latest policy unless league rules change.
There was no 2020 regular season.

Two orderings use the selected criteria:

- **Total** ranks by the raw values. It is the official final order, and the
  default for complete and historical seasons. The server renders that order even
  without JavaScript; switching statistics back to Goals restores it.
- **Per match** ranks by each criterion divided by matches played, so the table is
  fair while teams have played different numbers of matches. It is the default
  for an active season.

Above the table, **Stats** (Goals, xG) and **Values** (Per match, Totals) are
segmented controls with their labels beside them, the same control Explore
uses. Only `standings.js` can re-sort the table, so they render disabled and the
script enables them. The Matches page's Results | Upcoming control names
itself, so its legend is visually hidden; it likewise appears only once the
script runs.

On phones the table scrolls sideways with rank and team pinned on the left and
Pts (xPts in the xG view) pinned on the right, so the ranking value is always
visible.

The modern next tiebreaker, least disciplinary points, needs card data that
ASA does not provide. The earlier policy ends at a coin toss or drawing of lots.
Rows still tied at the first unavailable criterion are marked undetermined,
naming the rule and the tied teams. Alphabetical order stabilizes display only;
it never certifies a rank. The clinching guide explains how proofs treat that case.

A playoff line is drawn only when the catalog verifies the playoff place threshold (see the
[competitions guide](competitions-guide.md)).

Safely completed regular seasons link to the same season's playoffs, even
before ASA matches reach the cache. The playoff page shows labeled opening-round
expectations from resolved official total seeds and verified bracket pairs; a
tied seed stays TBD rather than taking its alphabetical display position. The
links are plain links, so they work without JavaScript.

Finished-season tables show `Bye · Home` for seeds earning a first-round bye
and home semifinal, and `Home` for other seeds earning an opening-round home
match. The existing playoff line marks qualifying places, without repeated
Playoffs badges. Entitlements come from the catalog bracket seed pairs, not
physical venues, and unresolved ranks receive no entitlement badge. Badges
follow official total seeds in per-match mode and hide in the xG view. Active
seasons retain the existing clinching badges. A note lists the season
tie-break order and any official adjustments. In 2022 Houston places fourth
ahead of Kansas City fifth: both have 36 points, but Houston has +8 goal difference to Kansas City’s 0.

## Remaining schedule difficulty

[`internal/strength`](../internal/strength/strength.go) offers three measures of
a team's remaining regular-season matches, each counting an opponent once per
match:

1. **Raw opponent PPG**: the mean of each remaining opponent's current-season
   points per game.
2. **Venue-adjusted**: raw PPG adjusted by half the league's home/away PPG gap
   (home matches easier, away matches harder), so raw PPG stays the neutral
   midpoint. The gap pools completed matches from the current season and up to
   two previous regular seasons; opponent records stay current-season only.
3. **Schedule-load adjusted** (the primary measure): venue-adjusted PPG scaled
   per match by the same recovery and accumulated-load penalty as the default
   forecast model. The
   [Forecast Lab guide](forecast-lab-guide.md#xg-poisson-schedule-load) defines
   it.

A team with a remaining opponent that has no completed history is shown as
unavailable, not zero.

### Presentation

- The standings `SD` column shows a marker centered on the league baseline.
  Position and shape show direction, so color is never the only signal, and a
  hover or keyboard disclosure gives the exact delta, label, remaining matches
  and home/away split.
- Labels compare the rounded two-decimal delta with ±0.10 PPG: `Harder`,
  `Easier`, or `Near average`.
- `/schedule-difficulty` is one dot plot, sorted hardest first. An **Adjust
  for** segmented control picks the measure: **Venue and load** (the
  schedule-load adjusted default), **Venue only** or **None** (raw opponent
  PPG). `difficulty=venue|raw` selects it in the URL; the default omits the
  parameter, and any other value is a `400`. The control switches the plot in
  place and re-sorts it; without JavaScript an Apply button submits it. Venue
  only and None need a league comparison, so without one the control is absent
  and a requested measure falls back to the default.
- Each measure compares a team with that measure's league baseline or average
  and labels the delta with the same ±0.10 rule. Unavailable teams sort last.
- Each plot row is a disclosure: opening it lists the team's remaining
  opponents, venue and per-match contribution. A "How this is calculated"
  disclosure explains the adjustments. On phones each row puts the team and its
  value on one line, with the track beneath.

`internal/strength` is the only place these values are calculated; view code
formats them and compares the venue-only and raw values with their league
averages.

## Playoff match outlooks

The Playoffs bracket and Matches list share the default model's outlooks, fit
once per request on that season's Regular Season data. Forecast availability
for that regular season gates these outlooks. Unresolved or mismatched bracket
facts withhold them. Known expected pairings use the higher seed at home;
published home/away assignments take precedence. The final averages both venue
assignments. The always-visible three-part bar shows each team's 90-minute win
chance around the extra-time chance. Its ends use team codes (for example,
`NJY 48.5% · Extra time 25.0% · SEA 26.5%`) in both the bracket and Matches list.
The bracket's first, top team always owns the left segment, including when
published home/away assignments put the higher seed second. Each stacked bracket
row shows its advance chance under one right-aligned Advance header, using the
simulator's relative-win draw rule. The Matches list keeps the usual side-by-side
Home vs Away row, with home on the left and each team's `Advance 59.2%` beneath
its name. The code-labeled bar sits below that row, and the Knockout match tag
stays in the usual match metadata position. There is no paired advance line or
disclosure. Scenarios in Forecast Lab never change these outlooks.

Completed Matches-list rows keep the match score (for example, `2–2`) and show
`Penalties 5–4` beside Knockout match and After extra time when the penalty flag
and both tallies are valid. The tag uses home–away order, matching the score;
matches without a confirmed paired shootout tally have no penalty tag.

Completed bracket cards use the same stacked identities, with separate
right-aligned Goals and xG columns. The xG column appears only when at least one
team has a value; a missing side reads `—`, rather than zero. The header row
appears only when there are stat columns (Goals or Advance), so placeholders
have no lone Team header. The winner is bold and the loser muted, including
shootout decisions. Shootout tallies appear beside each team's goals, for
example `2 (5)` and `2 (4)`, with accessible text identifying the penalty decision
and both tallies. After extra time remains a footnote. Known final regular-season
seeds stay beside the teams in every bracket round and in upcoming and completed
Matches-list rows, including historical results. Both surfaces use the same
safe regular-season completion and resolved official total ranking rule.
Unresolved seeds remain unnumbered. Unknown later entrants name their
feeder match; known entrants retain a feeder caption until the match completes.
One publication note replaces per-card publication and advancement labels.
