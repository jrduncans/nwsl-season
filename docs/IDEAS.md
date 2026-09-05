# Historical data stories

## Goal

Make the archive feel like a record book that can answer a question, not a
dashboard that happens to contain charts. The best views should produce a
sentence worth sharing:

- Is this the best or worst team-season in league history?
- Which teams got results furthest above or below their underlying play?
- Did an apparent over-performance last, or regress as the season continued?
- Is the league becoming higher-scoring, more balanced, or less
  home-field-friendly?

Each view should lead with the answer, show enough historical context to make
the answer meaningful, and expose the exact values in a table or tooltip.

## What the current archive can support

The cache contains regular-season fixtures for 2016–2019 and 2021 onward.
There was no 2020 regular season. For each completed match it has the score,
kickoff, home and away teams, xG, and ASA expected points. This is enough for
team-season records, same-match-count trajectories, league trends, and match
record books without contacting ASA during a page request.

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
   games played.
2. Describe an active team-season as its **current pace**, not its final place
   in history. Offer a “through the same number of matches” comparison when a
   historical trajectory is available.
3. Show games played beside every rate. Default historical leaderboards to a
   meaningful minimum sample, while allowing the user to include smaller
   samples explicitly.
4. Require complete xG or expected-points coverage for any aggregate that uses
   it. Never silently rank a partial aggregate alongside complete seasons.
5. Keep regular season, playoffs, and cups separate unless a view explicitly
   explains why they are combined.
6. Preserve the club name used in that season before presenting a feature as
   an historical record book. The current team catalog is keyed by ASA team ID
   and may display a club's current name for an older season.
7. Treat “luck” as playful shorthand, not a causal claim. Finishing,
   goalkeeping, game state, model error, and randomness can all contribute to
   an expected-versus-actual gap.

## Metric vocabulary

Use one definition everywhere in copy, tables, charts, and URLs.

| Label | Definition | What it helps answer |
| --- | --- | --- |
| Points per game (PPG) | `points / matches played` | Best and worst results |
| Goal difference per game (GD/G) | `(goals for - goals against) / matches played` | Scoreline dominance |
| Expected goal difference per game (xGD/G) | `(xG for - xG against) / xG-covered matches` | Underlying chance dominance |
| Results over-performance | `(points - xPoints) / xPoints-covered matches` | Results above or below ASA's shot-based expectation |
| Finishing over-performance | `(goals for - xG for) / xG-covered matches` | Scoring more or less than the chances suggest |
| Defensive over-performance | `(xG against - goals against) / xG-covered matches` | Conceding fewer or more than the chances suggest |
| Goal-difference over-performance | `(GD - xGD) / xG-covered matches` | Combined finishing and defensive gap |

Positive values should consistently mean “more favorable than expected.” Use
“results over-performance” in titles and explanatory copy; “luckiest” can
appear in a question or social description with quotation marks.

## First collection: team-seasons in history

### 1. Best and worst team-seasons

**Questions:** Is Chicago 2026 the worst team ever? What was the most dominant
team? Where does this year's leader rank?

**Best presentation:** a ranked table is the primary view because exact order,
season, sample size, and values matter. Add a compact distribution strip above
it that places the selected team-season among every other team-season.

The table should include team, season, games played, PPG, GD/G, xGD/G, and the
selected metric's rank. Controls should switch the ranked metric and choose:

- final seasons only;
- current pace among final seasons; or
- every team's record through the selected number of matches.

The answer above the view should be explicit, for example: “Chicago's current
0.73 PPG pace ranks 7th-lowest among 111 team-seasons, but the season is not
finished.” The count is illustrative; calculate it from the displayed,
eligible rows.

### 2. “Luckiest” and unluckiest team-seasons

**Questions:** Is Portland outperforming its underlying numbers by more than
any team before it? Which excellent records were backed by xG, and which were
not?

**Best presentation:** horizontal diverging bars ranked by results
over-performance. A zero reference line makes direction immediately clear.
Selecting a team-season should reveal three aligned components:

- points minus xPoints per game;
- goals minus xG per game; and
- xG against minus goals against per game.

Do not collapse those three ideas into one unexplained “luck score.” The
decomposition is the fun part: two teams can get similarly surprising results
through very different combinations of finishing and defending.

### 3. Season trajectories

**Questions:** Was a bad start historically bad? When did a great season pull
away? Did the apparent luck regress?

**Best presentation:** a line chart with team match number on the x-axis, not
calendar date, so teams with postponements remain comparable. Allow these
metrics:

- cumulative PPG;
- cumulative GD/G and xGD/G;
- cumulative points minus xPoints per game; and
- rolling PPG or xGD/G over the last five matches.

Default to one selected team-season, a historical median band, and at most two
useful comparators such as the record season and the closest historical pace.
Avoid a line for every team-season. A data table should provide each point for
keyboard and screen-reader users.

### 4. Team-season profile

**Questions:** Was this team great because of its attack or defense? Is its
record stronger than its underlying play?

**Best presentation:** aligned percentile strips or small horizontal dot plots
for PPG, goals for/G, goals against/G, xG for/G, xG against/G, and results
over-performance. Do not use a radar chart: the shared horizontal scales make
historical rank and weak spots much easier to compare.

This can become the reusable detail reached by selecting a row or mark in any
other historical view.

## Second collection: how the league has changed

### 5. Is scoring up?

**Questions:** Are there more goals this year? Are moderately high-scoring
matches becoming more common, or are a few wild matches pulling up the mean?
Are teams creating more chances, or just finishing more of them?

“Scoring is up” should have several named lenses rather than one ambiguous
answer:

- average goals per match;
- share of matches with at least three goals;
- share of matches with at least four goals;
- share of scoreless matches;
- average xG per match; and
- goals minus xG per match.

**Best presentation:** lead with a season-by-season dot-and-line chart for the
selected lens and an answer sentence that gives the active season's sample,
historical rank, and change from the previous season. Do not connect a line
through the missing 2020 regular season without marking the gap.

Below it, use a 100% stacked bar for each season with non-overlapping total-goal
bins: 0, 1, 2, 3, and 4+. This shows the shape of scoring without making the
user choose an arbitrary threshold. The 3 and 4+ segments together answer the
“3 or more” question, while the 4+ segment answers the more extreme version.
Keep the exact percentages in an adjacent or expandable table.

Show xG per match and goals minus xG as aligned small multiples when explaining
*why* scoring changed. Give each panel its own clearly labeled y-axis rather
than using a dual axis. Label an active year “through N matches,” and consider
an uncertainty interval so a partial season is not presented with false
precision.

### 6. Home advantage over time

**Questions:** Has home-field advantage weakened? Does the effect show up in
chance creation as well as results?

**Best presentation:** season-by-season lines for home-minus-away goals, xG,
and points per match. These should be aligned small multiples with a visible
zero line. The persisted venue summaries may already provide most of the
aggregate input.

### 7. Parity over time

**Questions:** Which season was the most competitive? Is the table becoming
more compressed as the league grows?

**Best presentation:** one annotated line per parity measure, with a plain
language explanation that lower spread means more parity. Candidate measures:

- standard deviation of team PPG;
- gap between the top and bottom team's PPG; and
- interquartile range of team xGD/G.

Prefer the standard deviation or interquartile range as the headline; the
top-to-bottom gap is intuitive but overly sensitive to one extreme team.

### 8. What does it take to win the Shield or make the playoffs?

**Questions:** Are the thresholds rising? Which was the strongest team to miss
the playoffs or the weakest to qualify?

**Best presentation:** season dots and a connecting guide for Shield-winner
PPG and playoff-cut PPG, paired with a table of the teams immediately above and
below the cut.

**Dependency:** add or verify official historical finishing order and Shield
winners. Existing historical cut-line counts are known, but old tiebreak rules
are not fully represented. Do not infer an official winner or qualifier in a
tied table from the current ordering code.

## Third collection: record-book questions

These are usually clearer as compact tables than charts.

### 9. Match heists and hard-luck losses

Rank individual matches by the winning team's points minus expected points.
Show score, xG, expected points, opponent, venue, and date. Separate wins and
draws so a single ranking does not mix unlike outcomes.

### 10. Biggest scorelines and xG mismatches

Offer separate tables for largest margin, most combined goals, largest xG
margin, largest winner with lower xG, and most goals from the least xG. Avoid
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

### First shippable story: Is scoring up?

Question 5 is the smallest useful vertical slice. It needs no historical
franchise-name decisions or official finishing-order metadata, and it exercises
the foundations the rest of the collection will need:

1. A cache-only read across historical regular seasons.
2. Reusable score and xG coverage rules.
3. A responsive historical chart paired with an exact-value HTML table.
4. An answer sentence generated from the same eligible observations.
5. A shareable URL for the selected scoring lens.

### First full collection: team-seasons in history

Next, build one **History** page around questions 1–3 rather than launching
many shallow charts:

1. An all-time team-season leaderboard with PPG, GD/G, xGD/G, and results
   over-performance.
2. A selected team-season answer sentence and distribution strip.
3. A drill-down trajectory comparing that team with historical context through
   the same number of matches.
4. Shareable query parameters for season, team, metric, and comparison mode.
5. A server-rendered table that remains complete without JavaScript; JavaScript
   adds the charts and selection behavior.

This first slice establishes the reusable historical aggregate, eligibility
rules, metric definitions, and selected-team interaction needed by most of the
later ideas. The “Is this the worst ever?” and “Is this the luckiest ever?”
questions then become two views of the same data instead of separate features.

## Product and implementation guardrails

- All page requests read SQLite only. Historical views must never trigger an
  ASA request.
- Gate each view by the catalog capability it actually needs. Score-only
  comparisons can remain available if xG is unavailable.
- Put coverage and sample-size exclusions next to the result, not in a distant
  methodology page.
- Keep the complete exact-value table available even when a chart is the main
  presentation.
- Use direct labels and a highlighted selected team-season; render the rest of
  history neutrally. Do not rely on color alone.
- Use zero lines for over-performance metrics and keep positive direction
  visually consistent.
- Avoid dual axes, unexplained composite scores, and league-wide spaghetti
  charts.
- Make URLs shareable and preserve the selected season, team, metric, and
  comparison mode.
- Test aggregation separately from HTTP and presentation. Historical
  statistics should be deterministic pure calculations over cached inputs.

## Later data opportunities

If the source data expands, revisit player-era records, goal timing, comeback
probability, lineup continuity, travel effects, and true pre-match upsets. Keep
these out of the first implementation unless their historical inputs are
persisted and coverage can be shown honestly.
