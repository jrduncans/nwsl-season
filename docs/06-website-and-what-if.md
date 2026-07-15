# Phase 6: website and what-if scenarios

## Goal

Render useful season information and let a user assign hypothetical outcomes to
remaining fixtures.

Begin with server-rendered `html/template` pages. Add small amounts of JavaScript
or HTMX only when a specific interaction needs them.

## First pages

- Season table with clinching indicators and data freshness.
- Results and remaining fixtures grouped by date or round.
- What-if page where each future match is unset, home win, draw, or away win.

The what-if calculation should reuse the standings domain service: combine actual
completed games with synthetic hypothetical results, then recalculate.

## Shareable state

Prefer encoding chosen outcomes in the URL once the interaction works. A shared
link can then reproduce a scenario without accounts or server-side sessions.
Keep the encoding versioned so future format changes do not silently reinterpret
old links.

## Implemented design

- `/` redirects to the configured current season.
- `/seasons/{season}` renders the in-season table, fixture groups, source, and
  cache freshness.
- `/seasons/{season}/what-if` renders an ordinary GET form. Submitted `g.*`
  fields are redirected to a canonical URL using `v=1` and repeated
  `p={game-id}:{h|d|a}` parameters. Unset games are omitted.
- A small script submits the form when a selection changes. The submit button
  and server redirect provide the same workflow when JavaScript is unavailable.
- The `internal/whatif` package parses versioned state and converts selected
  outcomes to synthetic `standings.Game` values. Home wins use 1-0, draws use
  0-0, and away wins use 0-1. This assumption is shown next to every projected
  table because it affects score-based tiebreakers.
- Unknown versions, duplicate selections, invalid outcomes, completed games,
  and stale fixture IDs return a user-facing `400 Bad Request`.

## Schedule completeness and clinching

The 2026 regular season has 16 teams playing 30 matches each, for 240 cached
fixtures, and eight playoff places. The site compares the cached fixture count
with that expected inventory before calling the exact clinching solver. An
incomplete cache is labeled and never interpreted as a completed season.

The current exact solver is still expensive enough that it should not run
unbounded in a page request. The website evaluates it only after the full
schedule is cached and four or fewer fixtures remain. Earlier pages say that
clinching has not been evaluated rather than showing an approximate result.
The season values are explicit `app.Options` so later seasons can supply their
own format.

This is a checkpoint constraint, not the desired final availability.
[`12-season-scale-clinching.md`](12-season-scale-clinching.md) plans cheap
always-on bounds, coupled-fixture optimization, and snapshot-level computation
for the Shield, home playoff places, and playoff places. Phase 13 adds
upcoming-slate clinching conditions without turning the what-if form into an
official proof surface.

The 30-match and eight-playoff-place assumptions come from the official
[2026 NWSL competition rules](https://images.nwslsoccer.com/image/private/t_q-good/prd/tstudhfledlk7z8ygtzd.pdf).

## Verification

Tests cover the cache season snapshot, version parsing, canonical synthetic
scores, stale and invalid selections, canonical redirects, escaping, freshness,
the projected-table label, schedule-completeness protection, and a full HTTP
read from a temporary SQLite cache.

## Exit criteria

- The site is useful with JavaScript disabled for basic browsing.
- A hypothetical result immediately produces a clearly labeled projected table.
- Actual and hypothetical results cannot be visually confused.
- The page shows source and freshness information.

All four criteria are implemented. When ASA has not cached future fixtures, the
what-if page remains honest and useful by explaining that no remaining fixtures
are currently available instead of treating the season as complete.
