# History calculation guide

The History views are calculated only from the coherent SQLite snapshot read by
`cache.DB.HistoricalRegularSeasons`; they do not refresh ASA data. This guide
records the delivered definitions for the first league-trends calculation and
applies the historical-data boundaries in [IDEAS.md](../IDEAS.md).

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
match has valid xG. xPoints coverage is retained for later work but this first
calculation does not derive an xPoints rate. Fixture inventory and expected
value coverage remain separate dimensions, as required by [IDEAS.md](../IDEAS.md).

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
