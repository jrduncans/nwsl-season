# How standings and schedule difficulty work

Both are calculated on read from cached fixtures. Neither is stored, and
schedule difficulty never changes the official standings.

## Standings

[`internal/standings`](../internal/standings/standings.go) turns completed
cached games into a table. Unplayed, postponed and abandoned games are ignored;
every participating team appears, including teams with zero points.

Ranking follows the 2026 NWSL regular-season rules: points, then goal
differential, total wins, goals scored, head-to-head points among the tied
teams, and head-to-head goals scored. Two orderings use those criteria:

- **Total** ranks by the raw values. It is the official final order, and the
  default for complete and historical seasons.
- **Per game** ranks by each criterion divided by games played, so the table is
  fair while teams have played different numbers of games. It is the default
  for an active season.

The next official tiebreaker, least disciplinary points, needs card data that
ASA does not provide. Rows still tied at that point are marked undetermined,
naming the rule and the tied teams, rather than separated by an invented
fallback. The clinching guide explains how proofs treat that case.

A playoff line is drawn only when the catalog verifies the cut line (see the
[competitions guide](competitions-guide.md)).

## Remaining schedule difficulty

[`internal/strength`](../internal/strength/strength.go) offers three measures of
a team's remaining regular-season fixtures, each counting an opponent once per
fixture:

1. **Raw opponent PPG**: the mean of each remaining opponent's current-season
   points per game.
2. **Venue-adjusted**: raw PPG adjusted by half the league's home/away PPG gap
   (home fixtures easier, away fixtures harder), so raw PPG stays the neutral
   midpoint. The gap pools completed matches from the current season and up to
   two previous regular seasons; opponent records stay current-season only.
3. **Schedule-load adjusted** (the primary measure): venue-adjusted PPG scaled
   per fixture by the same recovery and accumulated-load penalty as the default
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
- `/schedule-difficulty` shows toughest and easiest callouts, a sorted dot plot,
  per-team expansions listing each opponent, venue and fixture contribution,
  and the raw and venue-only measures as comparisons. The page labels every
  value as an estimate, not a forecast, power rating or standings adjustment,
  and works without JavaScript.

`internal/strength` is the only place these values are calculated; view code
formats them.
