# Phase 4: standings

## Goal

Derive the regular-season table from completed cached games.

## Domain boundary

Create small domain values such as `Team`, `Game`, `Record`, and `TableRow`.
The standings calculator should accept values and return values. It should not
know about SQL rows, JSON tags, HTTP requests, or HTML.

Start with played, wins, draws, losses, goals for, goals against, goal difference,
and points. Keep ranking/tiebreak policy separate from accumulation so rules can
vary by season.

For the 2026 NWSL regular season, the default ranking uses points, then the
official accessible tiebreakers: goal differential, total wins, goals scored,
head-to-head points, and head-to-head goals scored. If teams remain tied after
those steps, the next official rule is least disciplinary points. ASA cached game
data does not currently include the card-level inputs needed to calculate that
rule, so rows still tied at that point should be marked as undetermined rather
than silently treated as officially separated by a deterministic fallback.

## Test cases

- Home win, away win, and draw.
- Unplayed and postponed games are ignored.
- Every participating team appears, including teams with zero points.
- Input order does not affect output.
- Equal points exercise the documented tiebreak order.

Use tiny invented leagues. They make failures comprehensible and avoid baking a
historical ASA snapshot into basic arithmetic tests.

## Exit criteria

- A table is calculated from domain games with deterministic ordering.
- Rule assumptions are represented explicitly and tested.
- An integration query can load cached games and print the table.
