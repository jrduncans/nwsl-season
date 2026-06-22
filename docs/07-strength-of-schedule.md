# Phase 7: remaining strength of schedule

## Goal

Offer several understandable measures rather than a single unexplained ranking.

## Candidate measures

- Raw average opponent points per game.
- Average opponent points per game split by venue.
- Venue-adjusted opponent strength.
- Expected points from a simple rating model.
- Rest-adjusted difficulty using days since each team's previous match.
- Congestion indicators such as midweek matches and short-rest sequences.

Define each measure in prose beside the table. Show its components when practical;
users should be able to understand why two teams are ranked differently.

## Modeling sequence

1. Implement raw opponent PPG as a transparent baseline.
2. Estimate a league-wide home advantage rather than choosing an arbitrary bonus.
3. Add a simple rating model and back-test it on prior seasons.
4. Add rest only after deciding how travel, international breaks, and rescheduled
   matches will be handled.

Avoid mixing future information into historical ratings during back-tests. Ratings
for a date should use only matches known before that date.

## Exit criteria

- At least two measures are available with clear definitions.
- Venue adjustment is derived and tested.
- Calculations use only remaining regular-season fixtures.
- The UI communicates that schedule strength is a model, not a fact.
