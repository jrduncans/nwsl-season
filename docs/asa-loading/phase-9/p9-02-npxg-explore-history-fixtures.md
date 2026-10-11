# P9-02: npxG across Explore, league history, and fixtures

## Control

- Status: Review
- Intended implementation model: Sonnet (one sub-agent, one pass)
- Required review: Opus
- Depends on: P9-01 (commit `07c6ffb` on branch `npxg`)
- Blocks: none

## Goal

Every place outside Forecast Lab that compares goals with xG can switch to a
non-penalty basis. Fixture results show npxG when a match had a penalty. The
UI uses the label **npxG** consistently, including on the standings page
added by P9-01.

## Why this packet exists

P9-01 stores ASA's per-game penalty split (`cache.GameXG.Penalties`: penalty
xG, goals, and shots per side) and shows non-penalty xG on standings only.
Users want the same basis in Explore and the league history page, and want to
see it on match results.

Read before editing:

- [P9-01](p9-01-non-penalty-xg-standings.md) and its implementation
  (`internal/cache/xg_penalty.go`, `internal/app/views.go` `addXGValues`).
- [History guide](../../history-logic-guide.md), especially the Explore
  workspace table and its view/display keys.
- `internal/history/scoring.go`, `internal/history/teams.go`.
- `internal/app/explore*.go`, `internal/app/history_handler.go`,
  `internal/app/history_views.go`, `internal/app/templates/explore.html`,
  `internal/app/templates/history.html`,
  `internal/app/templates/fixtures.html`, `internal/app/static/explore.js`,
  and the fixture-group code in `internal/app/views.go`.
- `e2e/explore_test.go`, `e2e/pages_test.go`.

## Fixed decisions

### Label

1. User-visible text uses these terms:
   - `npxG`, never `NPxG` or `Npxg`;
   - `npxGD` and `npxG +/-` for the derived columns;
   - "non-penalty xG" in prose and titles;
   - "non-penalty goals" in prose, legends, and headers, abbreviated
     `npG` only where an existing label is already abbreviated (for example,
     a column header that today says `G`).

   Rename every P9-01 standings string: the button, headers, titles, caption,
   warning, tests, and docs. Leave the `npxg` data-attribute values and Go
   identifiers as they are.

### Basis parameter

2. A new query parameter, `penalties`, selects the scoring basis on
   `/explore` (every view) and on the league history page:
   - absent or `include` means today's behavior;
   - `exclude` means the non-penalty basis;
   - any other value is rejected exactly the way an invalid `measure` is
     rejected today.

   Every Explore and history link and form that preserves selection state
   (season, team, view, display, measure, sort, and so on) must also preserve
   `penalties`. Omit it from generated URLs when it is `include`, so existing
   links and canonical URLs are unchanged.

3. A control labeled **Penalties**, with options **Included** and
   **Excluded**, appears once in the Explore workspace header, above the
   group and tab navigation, and once on the league history page. It uses the
   same server-rendered link/toggle styling as existing view controls and
   works without JavaScript. Include short help text: "Excluded removes
   penalty kicks from both goals and xG."

### Computation

4. Implement the basis in `internal/history` so every consumer inherits it.
   Add a `ScoringBasis` type with constants `AllShots` (the default and zero
   value) and `NonPenalty`. Thread it through the summary functions that the
   Explore and history handlers call; do not duplicate the arithmetic in
   `internal/app`.

5. Under `NonPenalty`, per completed game and per side:
   - goals = goals − penalty goals;
   - xG = max(0, xG − penalty xG).

   A game contributes goal and xG measures only when its xG is available
   **and** it has a penalty split. Without a split, non-penalty goals are
   unknown, so goals as well as xG lose coverage for that game.

6. Coverage uses the existing incomplete-xG mechanics. Under `NonPenalty`:
   - goal measures use non-penalty coverage just as xG measures use xG
     coverage today;
   - a season or team with incomplete non-penalty coverage is excluded or
     marked exactly as incomplete xG coverage is today;
   - exclusion text names the cause: "incomplete non-penalty data".

7. These are unaffected by the basis and must say so where they appear in
   non-penalty mode:
   - official points and xPts (ASA has no non-penalty xPts; never derive one);
   - forecast projections and benchmarks;
   - Forecast Lab.

   Where a points or xPts measure is selected while penalties are excluded,
   show a one-line note such as "Points include penalties; ASA publishes no
   non-penalty expected points." Do not hide the measure.

8. In non-penalty mode, labels follow the basis: `xG` → `npxG`, `Goals` →
   `Non-penalty goals`, `xGD` → `npxGD`, and chart titles, legends, axis
   labels, captions, accessible names, and table headers change with them.
   This also applies to Goal distribution: its bins count non-penalty goals
   per match.

### Fixtures and results

9. A completed match whose split shows penalty xG > 0 for either side appends
   ` · npxG <home>–<away>` (2 decimals, en dash) after the existing xG text.
   Use a `title` such as "Non-penalty expected goals". Matches without a
   penalty, or without a split, are unchanged. It must wrap cleanly at 390px.

## Allowed changes

- `internal/history/` and tests.
- `internal/app/` (Explore, history, fixtures, standings label rename, views,
  templates, `explore.js`, `standings.js`, `site.css`) and tests.
- `e2e/`.
- `internal/apptest/`, plus `internal/asatest/` only if fixtures need more
  penalty variety.
- Docs: `docs/history-logic-guide.md` (Explore and history basis), `README.md`
  (user-facing descriptions), the P9-01 packet's label text, this packet's
  Status, and its row in `docs/asa-loading/README.md`.

New files are allowed. No cache schema, sync, scheduler, forecast, or telemetry
registry changes. If one appears necessary, that is a stop condition.

## Required behavior

1. `penalties` absent: every Explore view and the history page render
   byte-for-byte the same as before, apart from the new Penalties control.
   Existing tests keep passing.
2. With `penalties=exclude`:
   - a team-season with 10.00 xG including a 0.76 penalty and 9 goals
     including 1 penalty shows 9.24 npxG and 8 non-penalty goals;
   - per-match rates use only split-covered games;
   - a season with one completed game missing a split is treated as
     incomplete, with the non-penalty exclusion reason.
3. `penalties=bogus` is rejected the same way as an invalid `measure`.
4. Navigation between Explore groups, tabs, seasons, and teams, and every
   selection form, keeps `penalties=exclude` once set. Default links omit it.
5. Points and xPts values are identical under both bases, and the note shows
   in non-penalty mode.
6. The fixtures line appears only for matches with penalty xG > 0.
7. Standings show `npxG`, `npxG +/-`, and `npxGD`; no `NPxG` string remains in
   templates, JS, Go user-facing strings, tests, or docs.

## Tests to add or update

- `internal/history`: table-driven `NonPenalty` arithmetic, coverage and
  exclusion, clamping at 0, and points/xPts invariance.
- `internal/app`: `TestExplore*` cases for parameter parsing, rejection, link
  preservation, label switching, and the points note. Add history page cases
  and fixture line presence/absence.
- `e2e`: toggle Penalties → Excluded on Compare teams and confirm labels and
  values change and survive switching tabs. Do the same on the history page.
  Check the fixture npxG line, the standings label rename, and no horizontal
  overflow at 390px for the Explore header control and fixtures.

## Verification

Run while iterating:

```sh
make test-explore
go test ./internal/history/... ./internal/app/...
```

Run before handoff, after the final edit:

```sh
golangci-lint fmt ./...
make verify            # fmt, lint, vet, test, test-e2e
govulncheck ./...      # report "skipped" if the database is unreachable
```

The race suite and telemetry checks are not required unless you touch
concurrency or telemetry, which this packet forbids.

### Live rehearsal

This is the only step allowed to contact real ASA. Work on a **copy**; the
original may be held open by a running preview server:

```sh
SCRATCH=/private/tmp/claude-501/-Users-jrduncans-workspace-personal-nwsl-season/ba1d4f3b-ccb9-4b03-bd66-61f78bfeeb61/scratchpad
mkdir -p "$SCRATCH/rehearsal2/data"
cp "$SCRATCH/rehearsal/data/nwsl-season.sqlite" "$SCRATCH/rehearsal2/data/nwsl-season.sqlite"
NWSL_CONFIG_FILE=/dev/null go run ./cmd/sync -backfill-historical -db "$SCRATCH/rehearsal2/data/nwsl-season.sqlite"
```

Do not copy `-wal`/`-shm` files. If the copy lacks P9-01 data, re-run the
2025 sync into it.

With `sqlite3`, confirm these regular-season penalty totals (sum of both
sides, ±0.1 xG):

| Season | Penalty shots | Penalty goals | Penalty xG |
| --- | ---: | ---: | ---: |
| 2016 | 27 | 21 | 18.4 |
| 2018 | 27 | 15 | 18.2 |
| 2021 | 27 | 19 | 18.2 |
| 2023 | 40 | 35 | 29.5 |
| 2025 | 47 | 34 | 34.7 |

Then serve the copy and check Explore (each group, with penalties excluded),
the history page, a 2025 results page, and standings, at desktop and 390px:

```sh
NWSL_CONFIG_FILE=/dev/null NWSL_DATA_DIR="$SCRATCH/rehearsal2/data" NWSL_SYNC_SEASON=2025 NWSL_ASA_BASE_URL=http://127.0.0.1:9 NWSL_HTTP_ADDR=127.0.0.1:8091 go run ./cmd/server
```

Stop the server before handing back.

## Non-goals

- Forecast Lab, backtests, model candidates, or forecast inputs.
- Any app-computed non-penalty expected points.
- New ASA requests, schema changes, or sync changes.
- A site-wide persisted preference (cookie or local storage) for the basis.

## Stop conditions

Stop and report instead of guessing if:

- a view computes goals or xG outside `internal/history` in a way that
  cannot take the basis without duplicating arithmetic;
- an Explore view's eligibility rules cannot express "goals need coverage"
  without restructuring;
- the rehearsal totals differ from the table;
- default-basis output changes in any existing test beyond the added
  control.

## Handoff

Commit on the current branch (`npxg`) with a descriptive message ending in:

```
Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>
```

Do not push. Report:

- the commit hash;
- files changed;
- behavior implemented;
- every verification command and its outcome;
- the rehearsal totals observed;
- deviations from this packet;
- follow-ups.

## Revision: Penalties as a data-control select

The first implementation rendered Penalties as pill links above the Explore
group navigation and as a link fieldset on the history page, which read as
navigation. It is now a labeled `<select>` ("Penalties": Included, Excluded)
last in each view's data-control row, switching through hidden destination
links (the season-selector pattern) with a noscript link fallback. The help
text is the select's title; "Penalty kicks excluded." extends the view's data
note when excluded.
