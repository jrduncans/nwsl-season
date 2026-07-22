# Phase 7: remaining strength of schedule

## Goal

Offer several understandable measures rather than a single unexplained ranking.

## Implemented measures

- **Raw opponent PPG:** the average of each remaining opponent's current points
  per game. An opponent is counted once for each remaining fixture.
- **Venue-adjusted opponent PPG:** raw opponent PPG adjusted for the fixture's
  venue. The page shows home- and away-fixture components so the result can be
  explained rather than treated as a black-box ranking.

The website labels higher values as a harder remaining schedule and identifies
both measures as model estimates rather than facts about future results.

## Venue adjustment

The adjustment is derived from completed matches in the cached season:

```text
home advantage = league home PPG - league away PPG
home fixture adjustment = -home advantage / 2
away fixture adjustment = +home advantage / 2
```

The half-gap convention keeps raw opponent PPG as the neutral midpoint. It also
allows the observed gap to be negative if away teams have performed better.
Rows with no completed history for one of their remaining opponents are marked
unavailable instead of displaying a misleading zero.

## Modeling sequence

1. Implement raw opponent PPG as a transparent baseline. **Complete.**
2. Estimate a league-wide home advantage rather than choosing an arbitrary bonus.
   **Complete.**
3. Add a simple rating model and back-test it on prior seasons. **Deferred.**
4. Add rest only after deciding how travel, international breaks, and rescheduled
   matches will be handled. **Deferred.**

The calculation lives in `internal/strength` and consumes standings-domain
values only. The HTTP layer maps its result into the season page; no cache
schema or ASA synchronization changes are required.

Avoid mixing future information into historical ratings during back-tests. Ratings
for a date should use only matches known before that date.

## Exit criteria

- At least two measures are available with clear definitions. **Complete.**
- Venue adjustment is derived and tested. **Complete.**
- Calculations use only remaining regular-season fixtures. **Complete.**
- The UI communicates that schedule strength is a model, not a fact. **Complete.**
