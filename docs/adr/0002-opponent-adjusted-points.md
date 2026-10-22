# Opponent-adjusted points shift by an average team's expected points

Match by match's **Adjust for opponents** also covers Points vs xPoints
([#193](https://github.com/jrduncans/nwsl-season/issues/193)), so a points
haul can be checked against the opponents it came from. We decided:

- **Additive, not multiplicative.** This departs from
  [ADR 0001](0001-opponent-adjustment.md)'s first decision, which still holds
  for goals and xG. Points are 0, 1 or 3, so dividing them by a factor does
  not read naturally. Each match's points and xPoints instead shift by how many
  more points an average team would expect in that match than in the season's
  average match. Values stay in points, and season totals stay comparable
  with the official table.
- **Adjusted points, not points above an average team.** A signed gap
  (points − an average team's expected points) differs only by a constant per
  season. It would read as one more actual − expected gap beside points −
  xPoints, so we keep values in their own units, as goals and xG do.
- **The same model and choices as ADR 0001.** An average team's expected
  points come from the same clamped xg-poisson-schedule-load-v1 rates that
  give the goal factors, as independent Poisson scores: 3 × P(win) + P(draw).
  Hindsight ratings and leave-one-out carry over. The season's average match
  is the mean over every team's completed matches, so across the league
  adjusted points total the official points, as normalized factors keep goals.
- **One checkbox, the same panels.** The same shift moves points and xPoints,
  so points − xPoints cannot show it. With smoothing off, Points vs xPoints
  always draws Points, xPoints and Points − xPoints panels, so the checkbox
  moves the first two and leaves the gap. An earlier version swapped the gap
  panel for two adjusted panels, which changed the chart's shape and left
  nothing to compare. Rolling lines and tiles show adjusted values, and the gap
  matches the official one.
  Tiles swap totals in place rather than adding a line, so toggling never
  moves the chart and the two states compare directly.
