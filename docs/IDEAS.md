# Ideas

Unbuilt ideas and open questions. Nothing here is committed or scheduled.
Explore ideas follow the principles in the
[Explore guide](explore-guide.md#principles-for-every-view).

## Explore: team-seasons across the archive

Explore compares teams within one season (Compare teams) and one team across
seasons (Season by season), and Team-seasons ranks every eligible team-season
across the archive.

- **Trajectories against history.** Match number on the x-axis (not date), with
  the historical median and middle-50% band through the same match count, and
  at most two comparators (the record pace and the closest historical pace).
  Show the sample at each match number as shorter seasons drop out. Match by
  match currently compares against one season's league or benchmark averages.
- **Historical percentile profile.** Aligned percentile strips for PPG, goals
  for/against, xG for/against and over-performance against all team-seasons.
  Rankings currently ranks within one season.

## Explore: league trends

Scoring trend, Goal distribution, Home advantage, Parity, and Shield and playoff
thresholds exist. Current-team pace comparisons and prospective threshold
targets remain future work.

## Explore: match records

- **Heists and hard-luck losses**: wins ranked by points minus xPoints; losses
  from the loser's side; wins and draws ranked separately.
- **Biggest scorelines and xG mismatches**: largest margin, most goals, largest
  xG margin, winner with lower xG. Don't call a result an upset without a
  pre-match strength estimate.
- **First half vs second half**: change in PPG or xGD/G between equal halves of
  a season, with a stated rule for odd match counts.
- **Consistency**: spread of five-match rolling PPG or xGD/G within a
  team-season.
- **Head-to-head: playoff meetings**.

## Needs data the cache doesn't have

Player-era records, goal timing, comeback probability, lineup continuity,
travel effects, and true pre-match upsets.

## Open questions

- Should `/cache/status` report failed source operations? Failures are recorded
  in source-refresh audits, but the endpoint reads legacy `sync_runs`, so e2e
  journey J6 checks only that pages keep good data and recover.
- Is the two-hour completion grace right for ASA's actual post-match
  publication delay?
- How do official tiebreak rules differ by season? Historical seasons currently
  get only a verified cut line, not rules.
- Can a maintainable source supply disciplinary points, the first official
  tiebreaker missing from the cache?
- Is the dedicated Schedule difficulty page still useful now that the
  standings `SD` column carries the main signal?
- When should the forecast recommendation be re-evaluated as new completed
  seasons arrive?
- Should validated team-strength estimates appear as a separately named power
  rating? It must never replace or adjust official standings.
