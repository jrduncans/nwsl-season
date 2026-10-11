# P9-01: Non-penalty xG source data and standings view

## Control

- Status: Review
- Intended implementation model: Sonnet (one sub-agent, one pass)
- Required review: Opus
- Depends on: none
- Blocks: later Explore, fixture, and Forecast Lab non-penalty xG work

## Goal

Every cached game with available team-model xG can also carry ASA's
penalty-pattern breakdown, and the standings page offers a third **NPxG** stat
next to Goals and xG that shows non-penalty xG for/against and difference.

## Why this packet exists

Analysts commonly prefer non-penalty xG (NPxG) because penalty awards are
noisy. ASA's `/nwsl/games/xgoals` endpoint, the app's only xG source, has no
penalty breakdown. A live investigation (2026-10-10) established:

- `/nwsl/teams/xgoals?split_by_games=true` returns one row per team per game.
  Its `xgoals_for`/`xgoals_against` equal `/games/xgoals`
  `home_team_xgoals`/`away_team_xgoals` exactly (364/364 team-games, 2025
  Regular Season). It is the same team model the app already uses.
- Adding `shot_pattern=Penalty` filters every count to penalty shots. Penalty
  plus `Regular,Fastbreak,Corner,Free kick,Set piece` equals the unfiltered
  totals for xG, shots, and goals within 4-decimal rounding (max 0.0001).
- The penalty-filtered response contains a row for **every** team-game, with
  zeros when no penalty was taken. `xpoints`/`points` are not filtered.
- `/teams/xgoals` has no `game_id` parameter; passing one returns HTTP 500.
  `start_date`/`end_date` (combined with `stage_name`, without `season_name`)
  return rows identical to the season query.
- Penalty data exists for 2016 through 2025; penalty xG is 6–9% of season xG.
- `/games/shots` `shot_xg` sums to the **player** model, not the team model.
  Do not use it.

Live sample row (penalty-filtered):

```json
{"team_id":"315VnJ759x","game_id":"4JMAb69k5K","shots_for":1,"shots_against":0,
 "goals_for":1,"goals_against":0,"goal_difference":1,"xgoals_for":0.6253,
 "xgoals_against":0,"xgoal_difference":0.6253,
 "goal_difference_minus_xgoal_difference":0.3747,"points":0,"xpoints":1.695}
```

Read before editing:

- [Synchronization guide](../../sync-logic-guide.md) and
  [ASA data catalog](../../asa-data-catalog-and-loading-plan.md) (game-level xG
  section).
- `internal/asa/client.go` (`GameXGoals`, `decodeXGoals`, `fetchArray`).
- `internal/syncer/operations.go` (`execute`, `OperationGameXG` case) and
  `internal/syncer/syncer.go` (`xgASAClient`, absorbed xG failure handling).
- `internal/cache/cache.go` (`GameXG`, migrations, `schemaVersion`),
  `internal/cache/xg_inventory.go`, `internal/cache/xg_checks.go`.
- `internal/app/views.go` (`addXGValues`), `internal/app/handler.go`
  (standings page assembly, `XGWarning`), `internal/app/templates/season.html`,
  `internal/app/templates/partials.html` (`standingsToggle`),
  `internal/app/static/standings.js`, `internal/app/static/site.css`.
- `internal/asatest/server.go`, `internal/apptest/`, `e2e/pages_test.go`
  (`testStandingsSorting`).
- `telemetry/registry/` and `internal/telemetry/nwslconv/`.

## Fixed decisions

### Source fetch

1. Add to `internal/asa`:

   ```go
   // PenaltyXGoalsFilters selects ASA's per-game penalty-pattern team xG.
   // Use either SeasonName or a StartDate/EndDate window (YYYY-MM-DD), never both.
   type PenaltyXGoalsFilters struct {
       SeasonName, StageName, StartDate, EndDate string
   }

   // TeamGamePenaltyXGoals is one team's penalty-pattern row for one game from
   // /nwsl/teams/xgoals?split_by_games=true&shot_pattern=Penalty.
   type TeamGamePenaltyXGoals struct {
       TeamID        string  `json:"team_id"`
       GameID        string  `json:"game_id"`
       ShotsFor      int     `json:"shots_for"`
       ShotsAgainst  int     `json:"shots_against"`
       GoalsFor      int     `json:"goals_for"`
       GoalsAgainst  int     `json:"goals_against"`
       XGoalsFor     float64 `json:"xgoals_for"`
       XGoalsAgainst float64 `json:"xgoals_against"`
       RawJSON       string  `json:"-"`
   }

   func (c Client) GamePenaltyXGoals(ctx context.Context, f PenaltyXGoalsFilters) ([]TeamGamePenaltyXGoals, error)
   ```

   The method always sends `split_by_games=true` and `shot_pattern=Penalty`,
   never sends `game_id`, and rejects filters that mix `SeasonName` with dates.
   Reuse `fetchArray`/`decodeArray` with a 2 MiB response cap and a row cap at
   least twice the existing xG row cap.

2. Penalty data rides inside the existing `OperationGameXG` operation. Do not
   add a new `OperationResource`, scheduler job kind, planner cadence, or check
   table. After `GameXGoals` succeeds and returns at least one row, the same
   operation makes one penalty request:
   - **Full mode:** `SeasonName` + `StageName` from the operation.
   - **Targeted mode:** `StageName` plus a date window from the minimum
     `date_time_utc` date to the maximum one in the returned `GameXGoals` rows,
     padded by one day on each side, because ASA's date-filter time zone is
     unverified. Ignore returned rows for games outside the requested IDs.
   - If `GameXGoals` returns no rows, make no penalty request.

   Add `DateTimeUTC string \`json:"date_time_utc"\`` to `asa.GameXGoals` so
   the window can be derived without a cache read.

3. A penalty request failure is **absorbed**. The xG write proceeds, the
   operation does not fail, `-require-xg` is unaffected, and the failure is
   recorded with WARN severity under a new `nwsl.error.code` value
   `sync.penalty_xg`. Context cancellation keeps the existing DEBUG semantics.

4. Validate per game in the syncer. A game's penalty split is accepted only
   when:
   - exactly two rows exist for the game;
   - their team IDs equal that game's `GameXGoals` home and away team IDs;
   - the home row's `xgoals_for`, `goals_for`, and `shots_for` equal the away
     row's `xgoals_against`, `goals_against`, and `shots_against` (xG within
     0.0001), and the reverse;
   - all values are finite and non-negative;
   - each side's penalty xG is at most that side's game xG plus 0.0001.

   Otherwise the game gets no split this time, and the syncer counts it as a
   rejected penalty game. A rejected or missing split never fails the
   operation.

### Persistence

5. Migration 15 (`schemaVersion = 15`) adds nullable columns to `game_xg`:
   `home_penalty_xg REAL`, `away_penalty_xg REAL`,
   `home_penalty_goals INTEGER`, `away_penalty_goals INTEGER`,
   `home_penalty_shots INTEGER`, `away_penalty_shots INTEGER`, and
   `penalty_raw_json TEXT NOT NULL DEFAULT ''`. Existing rows read as "no
   split observed". Follow the migration 7 (`xpoints`) pattern.

6. Add to `cache`:

   ```go
   // PenaltySplit is ASA's team-model penalty-pattern breakdown for one game.
   type PenaltySplit struct {
       HomeXG, AwayXG         float64
       HomeGoals, AwayGoals   int
       HomeShots, AwayShots   int
   }
   ```

   Add `Penalties *PenaltySplit` and `PenaltyRawJSON string` to
   `cache.GameXG`. `PenaltyRawJSON` is the two accepted source objects as a
   JSON array, home first.

7. Write semantics, for both `ReplaceStageXG` and `UpsertCheckedXG`:
   - `Penalties == nil` on an incoming value means "not observed". Preserve
     any stored split. A failed or omitted penalty observation must never
     erase a prior split. This mirrors the rule that omission cannot erase
     available xG.
   - A non-nil split overwrites the stored split.
   - A split is stored only on `available` rows. Unavailable rows always have
     null penalty columns.
   - A penalty-only difference counts as a row **update** (it is included in
     `sameAvailableXG`). It is **not** material: do not add penalty fields to
     `availableXGMaterialChange`, so forecasts, venue summaries, and
     correction cadence are unaffected.

   Every `SELECT`, `INSERT`, and `UPDATE` touching `game_xg` must carry the new
   columns, including `cache.go` (around lines 1645, 1665, 1690, 1764) and
   `xg_inventory.go` (`loadGameXG`, `stageXGStates`). `validateStoredXG`
   enforces all-or-none penalty columns and non-negative values.

### Standings presentation

8. The existing stat toggle gains a third button, **NPxG**
   (`data-standings-stat-value="npxg"`), after xG.

9. Per team, non-penalty values are `xG − penalty xG` for each side,
   aggregated over the same completed, xG-available games that `addXGValues`
   already uses. A team's NPxG cells show `—` unless every one of its
   xG-covered games has a split.

10. In NPxG mode:
    - the two xG columns show `NPxG +/-` and `NPxGD`, with header titles
      "Non-penalty expected goals for / against" and "Non-penalty
      expected-goal difference";
    - the points column shows **official** `Pts`, labeled "Pts". ASA provides
      no non-penalty expected points, and the app must not invent one;
    - rows are ordered by NPxGD (per game or total, matching the Values
      toggle) descending. A `—` sorts last, then ties break by team name and
      then team ID, matching xG mode. Positions renumber, and the playoff line
      sits at the official cutoff position, as in xG mode;
    - the caption reads `<season label> non-penalty xG, ordered by NPxGD`,
      following the same final/in-progress wording variants as the xG
      caption.

11. Show the NPxG button only when the scope supports xG and at least one
    available game has a split (`HasNPXG`). When NPxG coverage is below xG
    coverage, render a separate warning:
    `Non-penalty xG is unavailable for N of M completed matches, so the NPxG
    columns are incomplete.` Mark it `data-standings-stat-warning="npxg"` and
    hide it unless NPxG mode is active.

12. Non-JS rendering is unchanged: the default stat stays Goals. NPxG values
    live in `data-np-total` / `data-np-per-game` attributes on the existing xG
    cells, mirroring how xG mode swaps values today.

## Allowed changes

- `internal/asa/` (client, tests, new `testdata/game_penalty_xgoals.json`).
- `internal/syncer/` (operation execution, mapping/validation, tests).
- `internal/cache/` (migration, `GameXG`, xG read/write paths, tests).
- `internal/asatest/` (fake `/nwsl/teams/xgoals` endpoint and scenario data).
- `internal/apptest/` (seed penalty splits in existing scenarios).
- `internal/app/` (`views.go`, `handler.go`, templates, `standings.js`,
  `site.css`, tests).
- `e2e/` (standings journey).
- `telemetry/registry/` and generated `internal/telemetry/nwslconv/` output,
  only for the new error code and at most two sync span attributes (accepted
  and rejected penalty game counts).
- Docs: `docs/sync-logic-guide.md`,
  `docs/asa-data-catalog-and-loading-plan.md`, `README.md` (standings
  description), and this packet's Status plus a row in
  `docs/asa-loading/README.md`.

New files are allowed in those directories.

## Required behavior

1. A full xG operation issues exactly two ASA requests (`/games/xgoals`, then
   `/teams/xgoals`), or one when the first returns no rows.
2. A targeted xG operation's penalty request has `start_date`/`end_date`
   spanning the returned games' UTC dates ±1 day, has `stage_name`, and has no
   `season_name` or `game_id`.
3. After a full sync of a fake 2-game season where one game had a home
   penalty (0.76 xG, 1 goal), that game's `GameXG.Penalties` holds the split,
   the other game holds an all-zero split, and both are non-nil.
4. Penalty endpoint failure: xG rows are written, previously stored splits are
   unchanged, the operation returns no error, and a WARN-classified error with
   code `sync.penalty_xg` is recorded.
5. An inconsistent pair (for example, the home `xgoals_for` disagrees with the
   away `xgoals_against`) leaves that game's stored split unchanged and does
   not affect other games.
6. A penalty-only change updates the row (`RowsUpdated`) without setting
   `XGInputsChanged` / `DownstreamInputsChanged`.
7. A cache migrated from schema 14 keeps all existing xG values, with null
   penalty columns.
8. Standings NPxG math: a team with 3.00 xG for, including a 0.76 penalty,
   shows 2.24 NPxG for in totals mode and the per-game equivalent in per-game
   mode.
9. Unchanged behavior: Goals and xG modes, xPts ordering, Explore, fixtures,
   forecasts, the scheduler plan, and the request budget accounting (still one
   job per xG operation).

## Tests to add or update

- `internal/asa`: decode the fixture; assert query parameters
  (`split_by_games=true`, `shot_pattern=Penalty`, season/stage or date window,
  never `game_id`); reject a season-plus-dates filter.
- `internal/asatest`: serve `/nwsl/teams/xgoals` from scenario data. Derive a
  zero row for each team in each served xG game unless an explicit penalty
  row is configured. Return 500 when `game_id` is present (matching the real
  API), and filter by `season_name`, `stage_name`, and the UTC date of the
  game's `date_time_utc`. Support `FailNext` on the new path.
- `internal/cache`: migration upgrade, preserve-on-nil, overwrite-on-value,
  all-or-none validation, the non-material update, and unavailable rows
  rejecting splits. Use table-driven cases.
- `internal/syncer`: required behaviors 1–6, against the fake ASA server.
- `internal/app`: `addXGValues` NP math, `—` coverage, the warning text,
  `HasNPXG` false with no splits, and template attributes present.
- `e2e`: extend `testStandingsSorting`. NPxG mode orders by NPxGD with
  sequential positions, shows the NPxG headers and caption, shows official Pts
  in the points column, and switching back to Goals and xG restores both.
  Check the three-button toggle at 390px width for no horizontal overflow.
- `internal/apptest`: seed at least one penalty split in the scenario the
  standings e2e uses, so NPxG order differs from xG order.

## Verification

Run while iterating:

```sh
go test ./internal/asa/... ./internal/asatest/... ./internal/cache/... ./internal/syncer/... ./internal/app/...
```

Run before handoff, after the final edit:

```sh
golangci-lint fmt ./...
make verify            # fmt, lint, vet, test, test-e2e
go test -race ./...    # sync and cache paths changed
make telemetry-check-generated
make telemetry-live-check
govulncheck ./...      # report "skipped" if the database is unreachable
```

Then run one isolated live rehearsal. This is the only step allowed to
contact real ASA; it is not a test:

```sh
NWSL_CONFIG_FILE=/dev/null go run ./cmd/sync -season 2025 -db "$SCRATCH/npxg.db"
```

Confirm against the investigation's figures for 2025 Regular Season:

- summed penalty xG for is 34.75 (±0.01);
- 47 penalty shots and 34 penalty goals;
- team `Pk5LeeNqOW` has 9 penalty shots for and 1 against.

Start the server on that DB and inspect the standings NPxG view at desktop
width and 390px. Use `$SCRATCH` for a temporary directory outside the repo,
and never point this at the real `NWSL_DATA_DIR`.

## Non-goals

- Explore (Compare teams, Team profile, League trends), fixture/result xG
  lines, match outlooks, and history views.
- Forecast Lab models, backtest loaders, or evaluation evidence.
- Non-penalty goals in Goals mode, or any app-computed expected points.
- New scheduler jobs, cadences, check tables, or request-budget changes.
- `/games/shots` ingestion.

## Stop conditions

Stop and report instead of guessing if:

- the live or fixture response shape differs from the sample above, or the
  rehearsal totals do not match;
- preserving splits on `nil` requires restructuring the xG write/audit model
  beyond adding columns and a preserve branch;
- adding the second request breaks an invariant asserted by existing
  scheduler or syncer tests in a way that needs a scheduler change;
- the telemetry registry change needs more than one error code and two
  attributes, or weaver policy rejects them;
- the three-button toggle cannot fit at 390px without redesigning the
  toolbar.

## Handoff

Do not commit. Leave the changes in the working tree and report:

- files changed;
- behavior implemented;
- every verification command and its outcome, including the live rehearsal
  figures;
- deviations from this packet;
- unresolved questions or recommended follow-up, especially how existing
  caches get backfilled (expected: `cmd/sync -backfill-catalog` plus normal
  correction polling).
