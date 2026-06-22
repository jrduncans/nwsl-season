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

## Exit criteria

- The site is useful with JavaScript disabled for basic browsing.
- A hypothetical result immediately produces a clearly labeled projected table.
- Actual and hypothetical results cannot be visually confused.
- The page shows source and freshness information.
