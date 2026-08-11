# ASA data catalog and loading plan

This document catalogs the American Soccer Analysis (ASA) data used by NWSL
Season Explorer, records why and when each value must be refreshed, and proposes
a loading model for current seasons, historical seasons, corrections, and
playoffs.

The current implementation is described separately in
[How synchronization works](sync-logic-guide.md). This document is the proposed
direction, not a description of behavior that already exists.

## Executive decision

Loading should be organized by **data product and refresh purpose**, rather
than by one operation that always replaces a complete season.

- A result refresh should request only the due game IDs and incrementally upsert
  those games. It should not also request the complete season.
- An xG refresh should request only completed games whose xG is due and update
  xG independently of fixtures and teams.
- A complete season/stage request should be reserved for initial loading,
  discovering schedule changes, authoritative inventory reconciliation, and
  correction sweeps.
- Team metadata should have its own slow refresh schedule.
- Historical seasons and stages should be explicit catalog entries with their
  own rules, capabilities, load state, and cold-data correction policy.
- Playoffs should be a separate stage of a season, not games added to the
  regular-season standings.

This keeps the existing safety rule: a failed upstream request never destroys a
usable cache. It changes which ASA request is authoritative for each job.

## Scope and source limits

The bundled ASA API definition says NWSL season filters begin in 2016 and lists
these relevant stages:

- `Regular Season`
- `Playoffs`
- `NWSL Challenge Cup Group Stage`
- `NWSL Challenge Cup Knockout Round`

It also says `game_id` can contain one ID or multiple comma-separated IDs on
both `/nwsl/games` and `/nwsl/games/xgoals`. The proposed hot path relies on
that batching behavior.

Only three ASA endpoints are currently called:

1. `/nwsl/teams`
2. `/nwsl/games`
3. `/nwsl/games/xgoals`

The API offers player, goalkeeper, shot, game-flow, period, stadium, manager,
referee, xPass, and goals-added resources too. None is required for the current
season explorer or the first useful playoffs view, so the loader should not
fetch them speculatively.

## Current source-data catalog

### Team identity and presentation

Source: `/nwsl/teams`

| ASA field | Stored form | Current consumers |
| --- | --- | --- |
| `team_id` | `teams.asa_team_id` | Foreign keys, validation, standings, schedules, forecasts, qualification, scenarios |
| `team_name` | `teams.name` | Display name throughout the site and reports |
| `team_short_name` | `teams.short_name` | Display-name fallback |
| `team_abbreviation` | `teams.abbreviation` | Compact display-name fallback |
| Complete source object | `teams.raw_json` | Diagnostics and protection against silently losing new upstream fields |

Team rows are global rather than season-specific. A season's participants are
currently inferred by joining teams to its games.

Team metadata is required before inserting a game because game team IDs are
foreign keys. It is not result data and normally changes much less often than a
game status or score.

### Fixture inventory, schedule, and results

Source: `/nwsl/games`

| ASA field | Stored form | Current consumers |
| --- | --- | --- |
| `game_id` | `games.asa_game_id` | Stable fixture identity and all joins |
| Request `season_name` scope | `games.season` | Season selection, cache isolation, backtests |
| Request `stage_name` scope | `games.stage` | Regular-season isolation; groundwork for playoffs |
| `date_time_utc` | `games.kickoff_utc` | Fixture display/grouping, overdue-result scheduling, scenario slate selection, forecast and backtest cutoffs |
| `status` | `games.status` | Completed/remaining classification, refresh eligibility, every calculation boundary |
| `home_team_id`, `away_team_id` | Same-named game columns | Standings, fixture display, schedule strength, forecasts, qualification, scenarios |
| `home_score`, `away_score` | Same-named nullable columns | Standings, tie breaks, venue summaries, models, backtests |
| `matchday` | `games.matchday` | Fixture grouping and preferred scenario-slate grouping |
| `last_updated_utc` | `games.last_updated_utc` | Currently used only to choose between season-wide and targeted copies of a game |
| Complete source object | `games.raw_json` | Diagnostics and source provenance |

The client decodes but does not normalize `referee_id`, `stadium_id`, manager
IDs, `expanded_minutes`, `attendance`, or `knockout_game`. They remain
recoverable from `raw_json`. Of these, `knockout_game` and `expanded_minutes`
are the first candidates to normalize for playoffs; stadium and attendance are
reasonable later additions for richer fixture pages. Referee and manager data
should wait for an actual feature that consumes them.

ASA returns `season_name` in a game row and the sync validates it. The cache's
stage value comes from the requested scope because the decoded game value does
not currently carry a stage field.

### Game-level expected goals and expected points

Source: `/nwsl/games/xgoals`

| ASA field | Stored form | Current consumers |
| --- | --- | --- |
| `game_id` | `game_xg.asa_game_id` | Join to one completed game |
| `home_team_id`, `away_team_id` | Same-named xG columns | Identity validation against the game |
| `home_team_xgoals`, `away_team_xgoals` | `home_xg`, `away_xg` | Standings xG totals, fixture xG, match outlooks, xG forecasts, venue baselines, backtests |
| `home_xpoints`, `away_xpoints` | Same-named xG columns | Expected-points standings columns |
| Complete source object | `game_xg.raw_json` | Diagnostics and source provenance |

The response also contains the final score, player-model xG, and difference
fields. Those are not normalized or used. Team-model xG is deliberately the
current forecasting input.

The cache represents a completed game omitted from a successful full xG
response as explicitly unavailable. It records `first_observed_at` and
`last_checked_at`, but it has no per-game retry schedule. A previously available
xG value cannot be erased merely because a later response omits it.

The xG response has no equivalent of the game's `last_updated_utc` and no
finality marker. The application can observe that a value changed, but several
unchanged responses cannot prove that ASA will not correct it later. A refresh
policy therefore needs a time-bounded correction watch; it cannot safely stop
early based on apparent stability alone.

### Derived data that does not come from ASA

The following values are important consumers of source changes but are not
additional ASA resources:

| Derived product | Source dependency | When it must be invalidated |
| --- | --- | --- |
| Standings | Teams plus completed game status/scores | Calculated on read; any material team or game change affects output |
| Schedule difficulty | Teams, completed results, remaining fixtures, historical venue summary | Any material game change or relevant historical venue-summary change |
| Fixture and season forecasts | Teams, fixtures/results, xG, historical venue summary | Relevant game, xG, or venue-summary change |
| Qualification | Exact regular-season fixture snapshot plus versioned rules | Material regular-season fixture/result change or rules-version change |
| Clinching scenarios | Qualification snapshot, fixture snapshot, matchday/kickoff | Material fixture/result change or scenario-definition change |
| Venue summary | Completed scores and available xG for one season/stage | Relevant result or xG change |
| Model evaluation | Completed historical regular seasons and xG | Explicit evidence rebuild after relevant historical corrections |

The fixture snapshot intentionally excludes team display text, fetch times,
raw JSON, and xG. Those values should not cause qualification or scenarios to
recalculate.

## Current request behavior

One production `syncer.Service.Run` starts independent requests concurrently:

| Request | Current trigger | Current consequence |
| --- | --- | --- |
| All NWSL teams | Every source sync | Required even when refreshing one known fixture |
| All games in one season/stage, including all statuses | Every source sync | Required; atomically replaces that season/stage and deletes absent games |
| One game by ID | An overdue cached fixture | Best-effort supplement to the simultaneously requested full collection |
| All game xG in one season/stage | Every source sync | Best effort; committed after fixtures |
| A second all-games request | Full response fails configured inventory validation | Immediate application-level retry after 250 ms |

The scheduler itself makes no ASA request while the known match window, xG,
and fixed 2026 inventory are current. The inefficient cases begin when work is
eligible:

- An overdue result normally causes four logical requests: teams, all games,
  one game, and all xG. The all-games request may run twice when inventory
  validation fails.
- Missing xG causes teams, all games, and all xG to be fetched again on every
  eligible check even when fixtures are already final.
- A team catalog that did not change is still fetched for every fixture or xG
  retry.

The HTTP client already has bounded retries for transient transport and HTTP
failures. The separate 250 ms incomplete-inventory refetch is a second retry
policy and should not be part of the redesigned path.

## Current historical-season and playoff gaps

### Historical seasons

The route accepts any `/seasons/{season}` value, but that is only a cache lookup.
It does not load the requested season.

Historical source data currently enters the cache in two special ways:

- Server startup ensures venue summaries for only the two previous regular
  seasons. It runs a complete source-only sync when either summary is missing.
- `make backfill-evaluation-data` explicitly loads 2016–2019 and 2021–2025 for
  model evaluation.

Neither mechanism defines which seasons are public website features. There is
no season selector or background backfill of a supported website catalog.

More importantly, the HTTP application owns one process-wide rules value. With
the default server configuration it is the 2026 regular-season format: 16
teams, 30 games per team, and eight playoff places. Every historical URL uses
that value for its playoff line, schedule-completeness messages, forecast, and
qualification lookup. Unknown configured seasons use another fallback with
the same 16/30/8 assumptions. This is why prior seasons can be loaded in SQLite
and still appear incomplete or incorrect in the UI.

### Playoffs

The data model and ASA filters already include `stage`, and tests prove regular
season and `Playoffs` games can coexist in SQLite. The runtime is nevertheless
single-stage:

- Server configuration chooses one stage.
- Routes do not identify a stage.
- The competition catalog contains only 2026 `Regular Season` rules.
- Standings, qualification, schedule difficulty, and season forecasts assume a
  league-table stage.
- A fixed expected-games-per-team validator is inappropriate while a knockout
  bracket is being populated.

Basic playoff schedule and result pages can use the existing teams, games, and
xG endpoints. A trustworthy bracket may need round labels, series state,
extra-time interpretation, and penalty-shootout winner data that the current
game contract does not expose. That gap should be verified before promising a
bracket; it should not be filled by guessing from score or `matchday`.

## Target product catalog

The loader needs one locally owned competition catalog in addition to ASA data.
It should make these concerns explicit for each season and stage:

| Catalog value | Purpose |
| --- | --- |
| Season and ASA stage name | Stable source scope |
| Public label and URL slug | Season/stage navigation |
| Stage kind: league table, knockout, or group | Select valid presentation and calculations |
| Source availability | Do not request regular-season data for 2020 or pre-2016 data from this API |
| Expected inventory, when known | Protect authoritative full replacements without inventing 2026 assumptions |
| Participating-team count and games per team, when meaningful | Regular-season completeness checks |
| Achievement/playoff rules and version | Playoff line, qualification, scenarios, forecasts |
| Supported capabilities | Hide or explain unavailable standings, forecast, qualification, scenario, or bracket features |
| Lifecycle state | Upcoming, active, recently completed, or archived refresh policy |

Schedule inventory expectations and competition achievement rules should be
separate values even if they live in one package. A stage may have a safely
known inventory without supporting qualification, and an in-progress knockout
stage may be valid before its final game count is known.

The set of scopes to poll should also be separate from the set of fully coded
competition rules:

- A persisted **source-scope registry** says which season/stage pairs the loader
  should check and tracks their lifecycle and refresh state.
- The versioned **competition catalog** says which features and rules are
  verified for a scope.

The registry should seed explicit supported scopes plus provisional
`Regular Season` scopes for the current calendar year and the following year,
while also retaining the configured primary scope. In 2026 it would therefore
check 2027 weekly even before the 2027 schedule exists; after the calendar turns
to 2027 it would add 2028 without relying on a deployment to update the default
season first. ASA has no endpoint that lists seasons, so deriving candidate
years (or adding an explicit operator scope) is necessary for discovery.

A provisional scope can load and cache factual fixtures before its competition
rules are implemented. It should not be advertised or enable rule-dependent
features until the catalog is ready; if reached directly after data appears, it
can use the same factual-only presentation as another unknown-format scope. An
empty response before schedule release is a successful discovery check meaning
“not published yet,” not a failed sync or a public empty season.

A scope remains on the weekly inventory policy while it is upcoming or active.
It becomes completed only when a verified catalog expectation is satisfied and
all games are terminal, or when an explicit catalog/operator completion boundary
says so. An empty provisional scope and a partial knockout inventory must not
declare themselves complete merely because every currently known game is
terminal.

For a more complete product, source data falls into three tiers:

### Tier 1: required season explorer data

- Competition catalog owned by the application.
- ASA team identity and presentation.
- Regular-season fixture inventory, schedule, status, and scores.
- Game-level team xG and expected points.
- Versioned regular-season format and achievement rules for each public season.

This tier supports correct historical results, standings, xG, schedule pages,
and capability-aware navigation. Forecasts and clinching remain enabled only
where the corresponding rules and meaningful remaining schedule exist.

### Tier 2: basic playoffs support

- A `Playoffs` stage entry for the applicable season.
- Playoff fixture inventory, schedule, result state, and game xG.
- Normalized `knockout_game` and `expanded_minutes`.
- A stage-specific fixtures/results view and a link between regular season and
  playoffs.
- Verified round and advancement semantics before adding a bracket.

Regular-season standings, Shield/seed qualification, schedule difficulty, and
complete-season simulation should not consume playoff games.

### Tier 3: feature-driven enrichment

- Stadium metadata for venue pages or richer fixture details.
- Attendance for attendance features.
- Period or game-flow data for extra-time and shootout interpretation if it is
  shown to be sufficient.
- Shots, player, goalkeeper, xPass, or goals-added data only for a committed
  match-analysis or player-analysis feature.

Tier 3 resources should each receive their own catalog entry, storage contract,
consumer, and refresh policy when the feature is designed. They should not be
folded into a generic “load everything” job.

## Freshness and correction policy

The scheduler can continue to wake on a five-minute local interval. Each data
product should carry its own `next_due_at`, so a local check does not imply a
network request. The following are recommended starting policies; exact
durations should remain configurable and be adjusted from observed ASA
publication behavior.

| Data/job | Freshness need | Recommended starting policy |
| --- | --- | --- |
| Team catalog | Slow-moving identity and display data | Fetch on empty cache and immediately if a game references an unknown team; if a safety refresh is retained, run it monthly with no active/inactive-season distinction |
| Newly registered season/stage scope | Discover whether its schedule has been published | Make the first full games discovery request immediately; an empty first response records “not published” without creating a public season |
| Known, not-yet-completed season/stage inventory | Discover initial schedule publication plus added, removed, or rescheduled fixtures | Authoritative full games discovery/audit weekly from the upcoming state through verified completion; allow an immediate manual audit and schedule one after evidence of an inventory inconsistency |
| Due result state | User-visible soon after a game | After kickoff plus completion grace, batch only unsettled game IDs on every five-minute scheduler tick until each is terminal |
| Missing xG for a newly completed game | Expected soon after the result, but not required for standings | Batch completed game IDs with missing xG on every five-minute scheduler tick for the first five days; preserve cached fixture/result data on omission or failure |
| Recently completed result correction watch | Scores and status can be corrected after first becoming terminal | Recheck terminal games by ID every 6 hours for five days after first terminal observation or a later material correction, then daily through day 30 |
| Available recent xG correction watch | ASA commonly corrects xG in the days after a game | Recheck by ID every 6 hours until five days after xG is first observed or last materially changes, then daily through day 30 after terminal observation |
| Active playoff inventory acceleration | Later rounds may appear only after earlier results | In addition to the weekly incomplete-scope audit, schedule an immediate inventory audit after a material earlier-round result when later-round inventory is uncertain |
| Archived season corrections | Rare; no minute-level user need | Full games and full xG sweep monthly, on deployment/backfill policy, or immediately by maintenance command |
| Historical venue summary | Derived locally | Recompute only after a relevant historical result or xG change |
| Qualification/scenarios | Derived locally | Recompute only after a material fixture snapshot or version change; never because only xG changed |
| Forecast cache | Derived in process | Warm after relevant fixture, xG, or venue-summary changes; no-op source checks do nothing |

All due IDs in the same season/stage and cadence class should be batched into
one request. A six-hour correction watch is therefore one ASA call for the set
of recent games, not one call per game.

The five-day and 30-day windows start from the application's first terminal
observation because ASA does not provide a completion timestamp distinct from
kickoff. A material correction restarts the five-day six-hour watch, even when
that extends beyond the original 30-day window. Missing xG stops being a
five-minute job after five days and joins the daily correction check; this
prevents permanently unavailable xG from keeping a season on the hot path.

The historical cadence is intentionally much slower than live result polling.
ASA does not expose a `last_updated_since` filter, so an archived correction
sweep must still request a complete season/stage. The optimization is to do
that monthly rather than on the hot result path.

Monthly full correction sweeps must use one low-priority coordinator with a
network concurrency limit of one. Stagger scopes deterministically across the
month instead of making every season due at the same instant. For one scope,
request and commit full games first, then request and commit full xG; do not
fetch those resources or different seasons in parallel. Do not start the next
cold request while current/upcoming inventory, a due result, or due xG is
waiting. If hot work becomes due during an in-flight cold request, finish that
bounded request but yield before starting the next cold step.

An operator should retain an explicit command that makes a full authoritative
games and xG correction sweep for selected season/stage scopes. “Force” should
describe bypassing the due-time policy; recalculating derived data should
remain a separate option.

## Proposed loading operations

### 1. Refresh team catalog

`RefreshTeams` requests `/nwsl/teams`, validates unique nonempty IDs, and
upserts only the team catalog. It has an independent audit record and due time.

A targeted refresh of a known cached game does not need this call. If any game
response introduces an unknown team ID, refresh teams and retry the database
operation rather than making teams a prerequisite for every result check.

### 2. Bootstrap or reconcile a season/stage

`ReconcileGameInventory` requests all games for exactly one season/stage and is
the only operation allowed to delete cached games absent from the response.

1. Fetch the full game collection.
2. For a provisional scope with no cached games, treat an empty response as a
   successful “not published” discovery check and stop without replacing data.
   An empty response for a previously populated scope remains invalid and can
   never authorize deletion.
3. Validate identities, requested scope, statuses, scores, team references, and
   the catalog's inventory expectation when one exists.
4. Refresh teams only if the cached team catalog is empty or a referenced ID is
   unknown.
5. Atomically upsert returned games, delete missing games for that scope, record
   the new fixture snapshot and full-audit success, and update the venue result
   summary.
6. Invalidate only downstream products whose inputs materially changed.

An invalid or suspicious response records a failed audit and retains the last
good scope. It does not make an immediate application-level second request;
the HTTP retry policy handles transient failures and the scheduler assigns the
next deliberate attempt.

### 3. Refresh due games by ID

`RefreshGamesByID` is the hot result operation.

1. Read all cached games whose result check is due.
2. Send one `/nwsl/games?game_id=id1,id2,...` request, chunked only if an
   observed URL or upstream limit requires it.
3. Require every returned row to match a requested ID and its cached
   season/stage and participants. A missing requested row is a checked-but-not-
   updated result, not permission to delete it.
4. Atomically upsert only material changes, record checked IDs and their next
   due time, and calculate the new fixture snapshot.
5. Trigger qualification, scenarios, venue result summary, and forecast warming
   only if material source state changed.

This operation does not fetch the full season in parallel or reconcile two
copies of the same game. A stale or empty response preserves the cached row and
keeps an unsettled result due for the next five-minute scheduler tick. Once a
result becomes terminal, the row moves to the six-hour correction watch. The
independent weekly inventory audit remains the safety net for schedule
discovery, additions, and removals.

### 4. Refresh xG independently

The ASA client should add `GameID` (and, if useful, `TeamID` and date filters)
to `XGoalsFilters` to match the API contract.

`RefreshXGByID` requests only completed games whose xG check is due. It upserts
returned values, records requested IDs omitted from the response as still
unavailable, and never erases an available value because of omission. Missing
xG remains due on every five-minute scheduler tick during its first five days.
Available xG follows the six-hour correction watch, and any material change
restarts that watch because the source offers no finality marker.

`ReconcileStageXG` remains a complete season/stage request for bootstrap and
correction sweeps. It validates against the committed fixture inventory but is
not coupled to a fixture request in the same run.

An xG change updates the xG venue summary and forecast inputs. It does not
change the fixture snapshot or rerun qualification and scenarios.

### 5. Recalculate from cached data

Keep source synchronization and derived calculation as separate operations.
After any source write, a small invalidation planner can choose among:

- no derived work for a no-op check;
- venue result summary plus fixture-dependent calculations for game changes;
- venue xG summary plus forecast warming for xG changes;
- presentation-only refresh for team-name changes;
- historical venue recomputation and affected current forecast warming for
  relevant archived corrections.

## Scheduler model

Replace the single eligible/current decision with a planner that returns zero
or more independently due jobs. Jobs for one scheduler tick can be prioritized
as follows:

1. Missing current-season inventory.
2. Due current game results.
3. Due current game xG.
4. Due weekly upcoming/current/other incomplete-scope inventory, including the
   rolling provisional regular-season scope for the next calendar year.
5. Missing public historical scopes and forecast-history dependencies.
6. Active playoff discovery/results/xG.
7. Cold correction sweeps.
8. Missing derived calculations that can be repaired without ASA.

The planner should apply a small global request budget per tick and carry
lower-priority work forward. That prevents a historical backfill from delaying a
current result while still letting one server process make steady progress.
Cold correction jobs additionally have a hard network concurrency of one and
run their resource/scope steps sequentially.

Use the existing process mutex and SQLite lease per resource scope. The lease
key should include resource and mode where concurrent jobs are actually safe;
fixture writes for the same season/stage must remain serialized.

## Persistence and audit changes

The current `ReplaceSeason` and `ReplaceGameXG` APIs encode complete-response
semantics. Add distinct store operations so callers cannot accidentally use an
incremental response as an authoritative replacement:

- `ReplaceGameInventory(season, stage, games, ...)`
- `UpsertCheckedGames(season, stage, requestedIDs, returnedGames, ...)`
- `ReplaceStageXG(season, stage, values, ...)`
- `UpsertCheckedXG(season, stage, requestedIDs, values, ...)`
- `ReplaceTeams(teams, ...)` or `UpsertTeams(teams, ...)`

Track source refresh state separately from calculation history. A generalized
audit record needs at least:

- resource (`teams`, `games`, or `game_xg`);
- season/stage scope when applicable;
- mode (`full`, `targeted`, or `recalculate`);
- trigger (`scheduler`, `startup`, `cli`, or backfill);
- requested and returned row counts, plus inserted/updated/unchanged/deleted;
- outcome and concise error;
- attempt/success timestamps;
- whether downstream inputs materially changed.

Per-resource scope state should retain `last_full_success_at` and
`next_full_due_at`. Per-game result and xG checks need enough state to select
their cadence (`last_checked_at`, first terminal or first available observation,
last material change, and `next_due_at`). The existing xG `first_observed_at`
and `last_checked_at` can be migrated into that model.

Full replacement remains the only mode that can delete absent rows. Targeted
mode can only upsert requested identities. These semantics should be enforced
at the storage API boundary, not left to a scheduler convention.

## Historical-season product behavior

### Catalog and loading

Maintain source-scope discovery independently from public support. Seed the
registry with verified historical scopes, the configured primary scope, and
provisional regular-season scopes for the current and next calendar years.
Define supported public seasons explicitly, initially matching verified ASA
coverage. Do not advertise a season in the selector until either its required
scope is cached or the UI can show a clear loading/unavailable state.

Backfill should run outside page requests. Suitable entry points are:

- an explicit deployment/maintenance command that loads all missing catalog
  scopes;
- a low-priority startup worker that fills missing scopes behind current-season
  work;
- both, using the same planner and leases.

Ordinary HTTP requests should continue to read SQLite only.

### Per-request capabilities and rules

Resolve competition metadata from the requested season and stage, not from the
server's current-season options. For an unknown or partially configured season:

- show factual cached results, fixtures, team names, and xG when available;
- do not apply 2026 expected counts or playoff places;
- omit or explain features that require unknown rules;
- never label an unverified inventory as incomplete merely because it differs
  from the current season.

Populate and verify historical team counts, schedule sizes, playoff places, and
tiebreak versions before enabling rule-dependent historical presentation. The
existing backtest playoff-place table can seed research, but it is not a full
competition-format catalog.

### Navigation

Add a season selector sourced from the public catalog/load state. Once multiple
stages are supported, give stage its own URL identity, for example:

- `/seasons/2026/regular-season`
- `/seasons/2026/playoffs`

The existing `/seasons/2026` URL can redirect to that season's primary stage.
This prevents a process-wide hidden stage from changing the meaning of a URL.

## Playoffs implementation boundary

The first playoffs increment should promise only what current verified data can
support:

1. Load and cache the `Playoffs` stage independently.
2. Show playoff fixtures, results, kickoff, teams, and xG.
3. Normalize knockout and expanded-minutes fields.
4. Refresh known playoff results by ID while auditing the full active stage for
   newly created later-round games.
5. Keep all regular-season calculations scoped to `Regular Season`.

Before adding a bracket, inspect live historical playoff payloads and the
period/game-flow endpoints to answer:

- How are rounds identified?
- Are penalty-shootout scores and winners represented?
- Can a postponed or relocated knockout game retain the same ID?
- Is `home_score`/`away_score` a regulation, extra-time, or advancement score?
- Are future-round placeholder teams represented, and how?

If ASA cannot answer those questions reliably, retain a chronological playoff
fixtures/results view or add a separate authoritative bracket source rather
than inferring advancement.

## Delivery plan

### Phase 1: make competition scope explicit

- Replace the single 2026-only rules lookup with a season/stage catalog and
  capability flags.
- Add the persisted source-scope registry and automatically seed provisional
  current- and next-calendar-year regular-season scopes independently of
  verified rules.
- Resolve rules inside each HTTP request.
- Remove the 16/30/8 presentation fallback for historical URLs; degrade
  unsupported features explicitly.
- Add season inventory/readiness APIs and tests for a past season with a
  different format.

This fixes incorrect historical interpretation before increasing the amount of
historical data served.

### Phase 2: split persistence by refresh semantics

- Add generalized source audit/scope state.
- Add authoritative full-replace and non-deleting targeted-upsert store APIs
  for games and xG.
- Split team refresh from season refresh.
- Preserve fixture snapshot and downstream invalidation guarantees for targeted
  game updates.

Keep the old `Run` facade temporarily while migrating callers, but implement it
from the new operations rather than adding more branches to `ReplaceSeason`.

### Phase 3: replace the current hot path

- Extend xG filters with `game_id`.
- Make the planner batch all due game IDs rather than selecting the first game.
- Use targeted games only for result refreshes and targeted xG only for xG
  refreshes.
- Remove simultaneous collection/target reconciliation and the 250 ms
  incomplete-inventory refetch.
- Add weekly inventory discovery/audits for every known scope that has not been
  verified complete, including an empty upcoming scope; add five-minute
  unresolved-result and missing-xG polling plus six-hour/daily correction
  watches.
- Warm/recalculate only the downstream products invalidated by material
  changes.

Expected request counts after this phase:

| Situation | Current logical requests | Proposed logical requests |
| --- | ---: | ---: |
| Idle/current cache | 0 | 0 |
| One or more due results | 4, plus possible inventory retry | 1 batched games request |
| Missing xG for completed games | 3 | 1 batched xG request |
| Missing season bootstrap | 3 | Teams only if due/missing, then one full games and one full xG request |
| Scheduled inventory audit | 3 | 1 full games request; xG remains on its own schedule |

### Phase 4: load and serve historical seasons

- Populate verified regular-season catalog entries for supported ASA seasons.
- Add a backfill command/worker using the same scheduler jobs.
- Add season navigation and loading/unavailable states.
- Enable features per season only when rules and data prerequisites are known.
- Continue to use slow correction sweeps after initial load.

The existing model-evaluation backfill should call the new full games/xG
operations instead of the monolithic current-season sync.

### Phase 5: add correction tiers

- Add six-hour checks for the first five days after a terminal result or xG
  observation/change, daily checks through day 30, and monthly archived
  full-audit due times.
- Store first-observed-terminal and last-material-change timestamps so a
  correction restarts the short watch without pretending unchanged xG is final.
- Add one low-priority cold-sweep coordinator, stagger monthly scope due times,
  and enforce a single in-flight games-or-xG request across all cold sweeps.
- Add maintenance commands for selected scopes and all due archived scopes.
- Record which historical changes affect venue baselines or checked-in model
  evaluation evidence.
- Do not automatically rewrite checked-in evidence; report that regeneration is
  required.

### Phase 6: add basic playoffs

- Add `Playoffs` catalog entries and stage URLs/navigation.
- Normalize the needed knockout fields and build the stage-specific fixtures
  view.
- Add active playoff discovery and targeted result/xG jobs.
- Research bracket semantics and add a bracket only after the data contract is
  proven.

## Verification requirements

The migration should include deterministic tests that prove:

- A due result produces no teams, full-season, or xG request.
- Multiple due results are batched and only requested rows can change.
- An unsettled due result remains eligible on every five-minute tick without
  exponential backoff.
- A targeted empty/failed response never deletes or regresses a cached game.
- Only a validated full inventory response can delete a disappeared game.
- A due xG check makes no teams or games request and never erases available xG
  because of omission.
- Missing xG remains eligible on every five-minute tick during the hot window;
  available recent xG remains eligible every six hours even when prior checks
  were unchanged.
- A material result or xG correction restarts its five-day correction watch,
  while unchanged xG does not falsely mark the source value final.
- A provisional next-calendar-year regular-season scope is checked weekly
  before the schedule exists; the rolling registry advances at the calendar
  year boundary even if primary-season configuration is stale. An empty
  response does not publish the scope, and its first valid inventory can
  trigger an unknown-team refresh.
- Every known, not-yet-completed scope becomes due weekly and a manual full
  audit bypasses that due time.
- Team refresh is triggered by an unknown ID independently of the optional
  monthly safety refresh.
- Monthly correction sweeps stagger scope due times and never overlap games and
  xG requests or requests for two historical scopes.
- A due hot job prevents the next cold-sweep request from starting.
- A no-op source check does not recalculate qualification/scenarios or warm
  forecasts.
- A material targeted result update creates the correct new fixture snapshot
  and invalidates fixture-dependent products.
- An xG-only update leaves the fixture snapshot and qualification data intact.
- Historical pages use their requested season's rules and do not inherit 2026
  expected counts or playoff places.
- An unknown-format season can still show factual results without fabricated
  completeness warnings.
- Regular-season and playoff rows for one year remain isolated in storage,
  refresh decisions, URLs, and calculations.
- Hot, recent, and archived fake clocks select their configured refresh cadence.
- Audit rows identify resource, scope, mode, trigger, call counts, changes, and
  downstream invalidation.

Telemetry should expose enough data to compare request volume and ASA
publication delay before and after the migration: job type, requested ID count,
returned count, result age at first terminal observation, xG delay, and whether
a request made a material change. That evidence can tune the suggested
cadences without coupling correctness to a guess about ASA timing.

## Decisions to confirm during implementation

- The exact supported public season list and authoritative historical format
  metadata.
- Whether the optional monthly team-catalog safety refresh is worth retaining
  in addition to unknown-team-triggered refreshes.
- Whether the automatic discovery horizon should contain only the next regular
  season or more than one provisional future year.
- The request-size limit, if any, for comma-separated game IDs.
- Whether archived correction sweeps run in the server, deployment automation,
  or only the maintenance binary.
- Whether ASA exposes enough postseason detail for a trustworthy bracket.
- Whether team display history matters. The current global team row shows the
  latest name in old seasons; season-specific branding would require a separate
  historical identity model.
