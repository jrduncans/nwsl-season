# ASA source data

This is the catalog of [American Soccer Analysis](https://www.americansocceranalysis.com/)
data the app stores, and what uses each field. For how and when the data is
fetched, see the [synchronization guide](sync-logic-guide.md). The bundled
[ASA OpenAPI definition](asa_openapi.json) is the upstream contract.

## Scope

ASA's NWSL season filters begin in 2016 and use these stages: `Regular Season`,
`Playoffs`, `NWSL Challenge Cup Group Stage`, `NWSL Challenge Cup Knockout
Round`, and `NWSL Challenge Cup Final`. `game_id` accepts comma-separated IDs on
`/nwsl/games` and `/nwsl/games/xgoals`, which is what lets targeted checks batch
every due match in a stage into a single request.

The app calls four endpoints: `/nwsl/teams`, `/nwsl/games`, `/nwsl/games/xgoals`
and `/nwsl/teams/xgoals`. Player, goalkeeper, shot, match-flow, stadium, manager,
referee, xPass and goals-added resources exist but no feature uses them, so the
loader does not fetch them.

Every stored row keeps its complete source object in `raw_json` for diagnostics
and so that new upstream fields are not silently lost.

## Teams (`/nwsl/teams`)

| ASA field | Stored as | Used by |
| --- | --- | --- |
| `team_id` | `teams.asa_team_id` | Foreign keys and every calculation |
| `team_name` | `teams.name` | Display name |
| `team_short_name`, `team_abbreviation` | `short_name`, `abbreviation` | Display fallbacks |

Teams are global, not per season; a season's participants come from its matches.
Match team IDs are foreign keys, so a match write that finds an unknown team
refreshes the catalog once and retries.

## Games (`/nwsl/games`)

| ASA field | Stored as | Used by |
| --- | --- | --- |
| `game_id` | `games.asa_game_id` | Match identity and all joins |
| Requested `season_name` / `stage_name` | `games.season`, `games.stage` | Scope isolation (ASA's row has no stage, so the request supplies it) |
| `date_time_utc` | `kickoff_utc` | Display, result-check scheduling, scenario slates, forecast and backtest cutoffs |
| `status` | `status` | Completed/remaining split and every calculation boundary |
| `home_team_id`, `away_team_id` | Same-named columns | Standings, matches, schedule strength, forecasts, qualification |
| `home_score`, `away_score` | Nullable columns | Standings, tiebreaks, venue summaries, models |
| `matchday` | `matchday` | Regular-season grouping and scenario slates |
| `last_updated_utc` | `last_updated_utc` | Choosing between two observations of the same match |
| `knockout_game`, `expanded_minutes` | Same-named columns | Knockout validation and display |
| `extra_time`, `penalties`, `home_penalties`, `away_penalties` | Same-named nullable columns | Bracket winners and shootout tallies |

Absent is kept distinct from false or zero for the four extra-time and shootout
fields. Referee, stadium, manager and attendance IDs are decoded but kept only
in `raw_json`.

## Game xG (`/nwsl/games/xgoals`)

| ASA field | Stored as | Used by |
| --- | --- | --- |
| `game_id`, `home_team_id`, `away_team_id` | `game_xg` identity columns | Joined to and validated against the match |
| `home_team_xgoals`, `away_team_xgoals` | `home_xg`, `away_xg` | Standings xG, matches, match outlooks, xG forecasts, venue baselines, backtests |
| `home_xpoints`, `away_xpoints` | Same-named columns | Expected-points standings |

Team-model xG is the forecasting input. The player-model xG and difference
fields in the response are not stored.

A completed match missing from a successful full xG response is stored as
explicitly unavailable. xG has no source update timestamp and no finality
marker, so stable responses never prove a value is final. That is why
corrections are watched for a fixed window rather than until values stop
changing.

## Penalty-pattern team xG (`/nwsl/teams/xgoals`)

With `split_by_games=true&shot_pattern=Penalty` the endpoint returns one row
per team per game, restricted to penalty shots (zeros when none were taken;
`xpoints` and `points` are not filtered). Without `shot_pattern` its
`xgoals_for` equals the match-level team-model xG. It rejects `game_id` with HTTP
500, but `start_date`/`end_date` with `stage_name` (and no `season_name`) return
the same rows as a season query. Penalty data exists from 2016. `/nwsl/games/shots`
sums to the player model and is not used.

| ASA field (home/away team row) | Stored as | Used by |
| --- | --- | --- |
| `xgoals_for` | `game_xg.home_penalty_xg`, `away_penalty_xg` | npxG in Explore, league history and results |
| `goals_for` | `home_penalty_goals`, `away_penalty_goals` | Non-penalty goals |
| `shots_for` | `home_penalty_shots`, `away_penalty_shots` | Stored only |
| Both accepted rows, home first | `penalty_raw_json` | Provenance |

The columns are all-or-none; null means no split has been observed. The
[synchronization guide](sync-logic-guide.md#split-source-operations) has the
acceptance rules.

## Derived data

These are computed locally from the source data above, never fetched:

| Product | Depends on | Recomputed when |
| --- | --- | --- |
| Standings | Teams, completed results | On every read |
| Schedule difficulty | Results, remaining matches, venue summary | On read |
| Forecasts | Matches, xG, venue summary | Warmed after a material match or xG change |
| Venue summary | Completed scores and available xG for one stage | A relevant result or xG change |
| Qualification and clinching scenarios | Match snapshot plus versioned rules | A material match change or a rules/definition version change |
| Model evaluation | Historical regular seasons and xG | Explicit evidence rebuild |

The match snapshot that keys qualification and scenarios excludes team
display text, fetch times, raw JSON and xG, so none of those trigger a
recalculation.
